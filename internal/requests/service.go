package requests

import (
	"context"
	"database/sql"
	"errors"
	"io"
	"log/slog"
	"slices"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/BaileyPillon/marquee/internal/core/events"
	"github.com/BaileyPillon/marquee/internal/grab"
	"github.com/BaileyPillon/marquee/internal/library"
	"github.com/BaileyPillon/marquee/internal/settings"
	"github.com/BaileyPillon/marquee/internal/titles"
)

// Catalog is what requests needs from the titles service.
type Catalog interface {
	Get(ctx context.Context, id int64) (*titles.Detail, error)
	Add(ctx context.Context, kind, provider, providerID string, opts titles.AddOptions) (*titles.Detail, bool, error)
	SetMonitored(ctx context.Context, id int64, monitored bool) error
	MonitorSeasons(ctx context.Context, id int64, seasons []int) error
	MonitorAlbums(ctx context.Context, id int64, albums []int64) error
}

// Library is what requests needs from the library service.
type Library interface {
	ListFolders(ctx context.Context) ([]library.Folder, error)
	Folder(ctx context.Context, id int64) (library.Folder, error)
	Files(ctx context.Context, f library.FileFilter) ([]library.File, int, error)
}

var (
	_ Catalog         = (*titles.Service)(nil)
	_ Library         = (*library.Service)(nil)
	_ grab.CopySource = (*Service)(nil)
)

// Options configure the service.
type Options struct {
	Logger *slog.Logger
	// Hub gets a "requests" event whenever a request is made or changes.
	Hub *events.Hub
	// Now replaces the clock (tests).
	Now func() time.Time
}

// Service is the requests module.
type Service struct {
	store *Store
	cat   Catalog
	lib   Library
	cfg   *settings.Service
	log   *slog.Logger
	hub   *events.Hub
	now   func() time.Time

	searcher atomic.Pointer[searcherBox]
	rec      atomic.Pointer[recorderBox] // the history, see SetRecorder

	mu      sync.Mutex
	closed  bool
	baseCtx context.Context
	cancel  context.CancelFunc
	wg      sync.WaitGroup
}

type searcherBox struct{ s grab.Searcher }

// NewService builds the requests module on the main database.
func NewService(d *sql.DB, cat Catalog, lib Library, cfg *settings.Service, o Options) *Service {
	if o.Logger == nil {
		o.Logger = slog.New(slog.NewTextHandler(io.Discard, nil))
	}
	if o.Now == nil {
		o.Now = time.Now
	}
	ctx, cancel := context.WithCancel(context.Background())
	return &Service{
		store: NewStore(d), cat: cat, lib: lib, cfg: cfg,
		log: o.Logger, hub: o.Hub, now: o.Now,
		baseCtx: ctx, cancel: cancel,
	}
}

// SetSearcher wires in the module that looks for releases. Until then (and for
// nil) approving a request only makes the copy.
func (s *Service) SetSearcher(sr grab.Searcher) {
	if sr == nil {
		s.searcher.Store(nil)
		return
	}
	s.searcher.Store(&searcherBox{s: sr})
}

// Close stops background searches and waits for them to end.
func (s *Service) Close() {
	s.mu.Lock()
	s.closed = true
	s.mu.Unlock()
	s.cancel()
	s.wg.Wait()
}

// Config reads the "requests" settings over the defaults. A section that cannot
// be read is logged, and the defaults are used.
func (s *Service) Config(ctx context.Context) Config {
	c, err := LoadConfig(ctx, s.cfg)
	if err != nil {
		s.log.Warn("the requests settings cannot be read; using the defaults", "err", err)
	}
	return c
}

// ---- limits ----------------------------------------------------------------

// effective works out the limits and flags that apply to a person. Members get
// the defaults unless the owner set something for them. The owner and admins
// have no weekly limit unless one was set for them by name, and their own asks
// are always approved.
func effective(role string, o LimitOverrides, c Config) EffectiveLimits {
	e := EffectiveLimits{AutoApproveHD: c.AutoApproveHD, AutoApproveUHD: c.AutoApproveUHD}
	if role == "member" {
		hd, uhd := c.HDPerWeek, c.UHDPerWeek
		e.HDPerWeek, e.UHDPerWeek = &hd, &uhd
	}
	if o.HDPerWeek != nil {
		v := *o.HDPerWeek
		e.HDPerWeek = &v
	}
	if o.UHDPerWeek != nil {
		v := *o.UHDPerWeek
		e.UHDPerWeek = &v
	}
	if o.AutoApproveHD != nil {
		e.AutoApproveHD = *o.AutoApproveHD
	}
	if o.AutoApproveUHD != nil {
		e.AutoApproveUHD = *o.AutoApproveUHD
	}
	if isStaff(role) {
		e.AutoApproveHD, e.AutoApproveUHD = true, true
	}
	return e
}

func (e EffectiveLimits) limit(quality string) *int {
	if quality == QualityUHD {
		return e.UHDPerWeek
	}
	return e.HDPerWeek
}

func (e EffectiveLimits) auto(quality string) bool {
	if quality == QualityUHD {
		return e.AutoApproveUHD
	}
	return e.AutoApproveHD
}

func (s *Service) limitsFor(ctx context.Context, userID int64, role string, c Config) (EffectiveLimits, LimitOverrides, error) {
	o, err := s.store.q().overrides(ctx, userID)
	if err != nil {
		return EffectiveLimits{}, LimitOverrides{}, err
	}
	return effective(role, o, c), o, nil
}

func usageOf(times []time.Time, limit *int) QualityUsage {
	return QualityUsage{Used: len(times), Limit: limit, ResetsAt: resetsAt(times, limit)}
}

// usage reports a person's last 7 days.
func (s *Service) usage(ctx context.Context, userID int64, e EffectiveLimits) (Usage, error) {
	since := s.now().Add(-Window)
	hd, err := s.store.q().usageTimes(ctx, userID, QualityHD, since)
	if err != nil {
		return Usage{}, err
	}
	uhd, err := s.store.q().usageTimes(ctx, userID, QualityUHD, since)
	if err != nil {
		return Usage{}, err
	}
	return Usage{HD: usageOf(hd, e.HDPerWeek), UHD: usageOf(uhd, e.UHDPerWeek)}, nil
}

// ---- folders ---------------------------------------------------------------

// resolveFolder picks the library folder a copy of a kind and quality goes to
// (see pickFolder). ErrNoFolder means the library has no folder for the kind.
func (s *Service) resolveFolder(ctx context.Context, c Config, kind, quality string) (library.Folder, error) {
	folders, err := s.lib.ListFolders(ctx)
	if err != nil {
		return library.Folder{}, err
	}
	f, ok := pickFolder(c, kind, quality, folders)
	if !ok {
		return library.Folder{}, ErrNoFolder
	}
	return f, nil
}

// ---- asking ----------------------------------------------------------------

func cleanText(s string, max int, what string) (string, error) {
	s = strings.TrimSpace(s)
	if utf8.RuneCountInString(s) > max {
		return "", invalid("invalid_request", "That "+what+" is too long.")
	}
	for _, r := range s {
		if unicode.IsControl(r) && r != '\n' && r != '\t' {
			return "", invalid("invalid_request", "That "+what+" has a control character in it.")
		}
	}
	return s, nil
}

// Ask is the one way in. It looks the title up (adding it, unmonitored, if it is
// not in the catalog), applies the rules and stores the ask: approved at once
// when the rules allow it, otherwise waiting for the owner.
//
// Failures the caller can act on: *InvalidError, *LimitError, *DuplicateError,
// ErrNoFolder (an owner's or admin's ask that would be approved, with no library
// folder to put it in) and the catalog's own errors for a title it cannot find.
func (s *Service) Ask(ctx context.Context, actor Actor, in AskInput) (*AskResult, error) {
	if actor.UserID <= 0 || (actor.Role != "owner" && actor.Role != "admin" && actor.Role != "member") {
		return nil, ErrForbidden
	}
	source := actor.Source
	if source == "" {
		source = SourceManual
	}
	if !validSource(source) {
		return nil, invalid("invalid_request", "That is not a known source.")
	}
	if !validQuality(in.Quality) {
		return nil, invalid("invalid_quality", `quality must be "hd" or "uhd".`)
	}
	if in.TitleID == 0 && in.Kind == KindArtist && in.Quality == QualityUHD {
		return nil, invalid("invalid_quality", "Music comes in one quality. Ask for \"hd\".") // before any lookup
	}
	note, err := cleanText(in.Note, maxNoteRunes, "note")
	if err != nil {
		return nil, err
	}
	ref, err := cleanText(in.SourceRef, 200, "source reference")
	if err != nil {
		return nil, err
	}

	cfg := s.Config(ctx)
	eff, _, err := s.limitsFor(ctx, actor.UserID, actor.Role, cfg)
	if err != nil {
		return nil, err
	}

	// A first look at the limit, before the catalog is asked for anything. The
	// check that counts is the one inside the transaction below.
	var lim *limitCheck
	if l := eff.limit(in.Quality); l != nil {
		lim = &limitCheck{Limit: *l}
		times, err := s.store.q().usageTimes(ctx, actor.UserID, in.Quality, s.now().Add(-Window))
		if err != nil {
			return nil, err
		}
		if len(times) >= lim.Limit {
			return nil, &LimitError{Quality: in.Quality, Used: len(times), Limit: lim.Limit, ResetsAt: resetsAt(times, l)}
		}
	}

	d, err := s.resolveTitle(ctx, in)
	if err != nil {
		return nil, err
	}
	if d.Kind == KindArtist && in.Quality == QualityUHD {
		return nil, invalid("invalid_quality", "Music comes in one quality. Ask for \"hd\".")
	}
	scope, err := normalizeScope(d, in.Scope)
	if err != nil {
		return nil, err
	}

	auto := eff.auto(in.Quality)
	var folder library.Folder
	if auto {
		folder, err = s.resolveFolder(ctx, cfg, d.Kind, in.Quality)
		switch {
		case errors.Is(err, ErrNoFolder):
			if isStaff(actor.Role) {
				return nil, err // tell the owner what to set up
			}
			auto = false // the ask waits; the owner will hit this when answering
		case err != nil:
			return nil, err
		}
	}

	nr := newRequest{
		UserID: actor.UserID, TitleID: d.ID, Kind: d.Kind, Quality: in.Quality, Scope: scope,
		Note: note, Source: source, SourceRef: ref, Now: s.now(),
	}
	var ap *autoApproval
	if auto {
		ap = &autoApproval{FolderID: folder.ID}
		if isStaff(actor.Role) {
			ap.DecidedBy = actor.UserID
		}
	}
	id, copyID, err := s.store.create(ctx, nr, lim, ap)
	if err != nil {
		var dup duplicateID
		if errors.As(err, &dup) {
			return nil, s.duplicate(ctx, actor, int64(dup))
		}
		return nil, err
	}
	s.log.Info("request made", "id", id, "user", actor.UserID, "title", d.ID, "quality", in.Quality, "auto", auto)

	if auto {
		if err := s.afterApprove(ctx, id, d.ID, d.Kind, scope, copyID); err != nil {
			return nil, err
		}
	} else {
		s.publish("created", id, StatePending)
	}

	item, err := s.item(ctx, id)
	if err != nil {
		return nil, err
	}
	s.record(ctx, requestedEntry(item, copyID, auto))
	if auto {
		s.record(ctx, approvedEntry(item, copyID, true))
	}
	use, err := s.usage(ctx, actor.UserID, eff)
	if err != nil {
		return nil, err
	}
	return &AskResult{Request: item, AutoApproved: auto, Usage: use}, nil
}

// resolveTitle finds (or adds) the title an ask is for.
func (s *Service) resolveTitle(ctx context.Context, in AskInput) (*titles.Detail, error) {
	if in.TitleID != 0 {
		return s.cat.Get(ctx, in.TitleID)
	}
	if strings.TrimSpace(in.Kind) == "" || strings.TrimSpace(in.Provider) == "" || strings.TrimSpace(in.ProviderID) == "" {
		return nil, invalid("invalid_request", "Name the title: give a titleId, or a kind, provider and providerId.")
	}
	d, _, err := s.cat.Add(ctx, in.Kind, in.Provider, in.ProviderID, titles.AddOptions{Monitored: false})
	return d, err
}

// normalizeScope checks what was asked for against the title and returns it in
// its plain form: {} for a movie; for a show the seasons (sorted, once each) or
// all of it; for an artist the albums or all of them. Asking for nothing in
// particular means all.
func normalizeScope(d *titles.Detail, sc Scope) (Scope, error) {
	switch d.Kind {
	case KindMovie:
		return Scope{}, nil
	case KindShow:
		if len(sc.Albums) > 0 {
			return Scope{}, invalid("invalid_scope", "A show is asked for by season, not by album.")
		}
		if sc.All && len(sc.Seasons) > 0 {
			return Scope{}, invalid("invalid_scope", `Ask for "all" or for some seasons, not both.`)
		}
		if len(sc.Seasons) == 0 {
			return Scope{All: true}, nil
		}
		have := map[int]bool{}
		for _, se := range d.Seasons {
			have[se.Number] = true
		}
		seen := map[int]bool{}
		var out []int
		for _, n := range sc.Seasons {
			if n < 0 || n > 9999 || (len(have) > 0 && !have[n]) {
				return Scope{}, invalid("invalid_scope", "This show has no season "+strconv.Itoa(n)+".")
			}
			if !seen[n] {
				seen[n] = true
				out = append(out, n)
			}
		}
		slices.Sort(out)
		return Scope{Seasons: out}, nil
	default:
		if len(sc.Seasons) > 0 {
			return Scope{}, invalid("invalid_scope", "An artist is asked for by album, not by season.")
		}
		if sc.All && len(sc.Albums) > 0 {
			return Scope{}, invalid("invalid_scope", `Ask for "all" or for some albums, not both.`)
		}
		if len(sc.Albums) == 0 {
			return Scope{All: true}, nil
		}
		have := map[int64]bool{}
		for _, al := range d.Albums {
			have[al.ID] = true
		}
		seen := map[int64]bool{}
		var out []int64
		for _, id := range sc.Albums {
			if !have[id] {
				return Scope{}, invalid("invalid_scope", "This artist has no album "+strconv.FormatInt(id, 10)+".")
			}
			if !seen[id] {
				seen[id] = true
				out = append(out, id)
			}
		}
		slices.Sort(out)
		return Scope{Albums: out}, nil
	}
}

// duplicate builds the DuplicateError for an ask that is already open, showing
// the existing one the way this person may see it.
func (s *Service) duplicate(ctx context.Context, actor Actor, existingID int64) error {
	it, err := s.item(ctx, existingID)
	if err != nil {
		return err
	}
	if !isStaff(actor.Role) && it.userID != actor.UserID {
		// Someone else's ask: say it exists, not whose it is or what they wrote.
		it.User, it.DecidedBy, it.Note, it.DeclineReason = nil, nil, "", ""
	}
	return &DuplicateError{Existing: it}
}

// ---- answering -------------------------------------------------------------

// Approve says yes to a waiting request: the copy is made (in the right folder)
// and monitored, the title is monitored (the seasons or albums asked for, if the
// ask named some), and the searcher is started.
func (s *Service) Approve(ctx context.Context, actor Actor, id int64, opts ApproveOptions) (Item, error) {
	if !isStaff(actor.Role) {
		return Item{}, ErrForbidden
	}
	it, err := s.store.q().item(ctx, id)
	if err != nil {
		return Item{}, err
	}
	if it.State != StatePending {
		return Item{}, ErrNotPending
	}
	folder, err := s.resolveFolder(ctx, s.Config(ctx), it.Title.Kind, it.Quality)
	if err != nil {
		return Item{}, err
	}
	copyID, err := s.store.approve(ctx, id, actor.UserID, folder.ID, s.now(), opts.AlwaysAllowUHD)
	if err != nil {
		return Item{}, err
	}
	s.log.Info("request approved", "id", id, "by", actor.UserID)
	if err := s.afterApprove(ctx, id, it.Title.ID, it.Title.Kind, it.Scope, copyID); err != nil {
		return Item{}, err
	}
	out, err := s.item(ctx, id)
	if err == nil {
		s.record(ctx, approvedEntry(out, copyID, false))
	}
	return out, err
}

// afterApprove is what follows an approval, once it is stored: monitor the
// title, tell the browsers, start the search.
func (s *Service) afterApprove(ctx context.Context, id, titleID int64, kind string, scope Scope, copyID int64) error {
	var err error
	switch {
	case kind == KindShow && len(scope.Seasons) > 0:
		err = s.cat.MonitorSeasons(ctx, titleID, scope.Seasons)
	case kind == KindArtist && len(scope.Albums) > 0:
		err = s.cat.MonitorAlbums(ctx, titleID, scope.Albums)
	default:
		err = s.cat.SetMonitored(ctx, titleID, true)
	}
	if err != nil {
		s.log.Error("request approved, but its title could not be set to monitored", "id", id, "title", titleID, "err", err)
		return err
	}
	s.publish("approved", id, StateApproved)
	s.search(copyID)
	return nil
}

// search starts the searcher for a copy, in the background.
func (s *Service) search(copyID int64) {
	box := s.searcher.Load()
	if box == nil {
		return
	}
	s.mu.Lock()
	if s.closed {
		s.mu.Unlock()
		return
	}
	s.wg.Add(1)
	s.mu.Unlock()
	go func() {
		defer s.wg.Done()
		ctx, cancel := context.WithTimeout(s.baseCtx, 10*time.Minute)
		defer cancel()
		if err := box.s.SearchCopy(ctx, copyID); err != nil && ctx.Err() == nil {
			s.log.Warn("search for a new copy failed", "copy", copyID, "err", err)
		}
	}()
}

// Decline says no to a waiting request. The reason is optional and is shown to
// the person who asked.
func (s *Service) Decline(ctx context.Context, actor Actor, id int64, reason string) (Item, error) {
	if !isStaff(actor.Role) {
		return Item{}, ErrForbidden
	}
	reason, err := cleanText(reason, maxReasonRunes, "reason")
	if err != nil {
		return Item{}, err
	}
	if err := s.store.decline(ctx, id, actor.UserID, reason, s.now()); err != nil {
		return Item{}, err
	}
	s.log.Info("request declined", "id", id, "by", actor.UserID)
	s.publish("declined", id, StateDeclined)
	out, err := s.item(ctx, id)
	if err == nil {
		s.record(ctx, declinedEntry(out))
	}
	return out, err
}

// Cancel withdraws a request. The person who asked may cancel their own while
// it is waiting; the owner may cancel any that is still open (waiting, approved,
// partial or failed). A copy that an approval already made stays as it is.
func (s *Service) Cancel(ctx context.Context, actor Actor, id int64) (Item, error) {
	it, err := s.store.q().item(ctx, id)
	if err != nil {
		return Item{}, err
	}
	var from []string
	switch {
	case actor.Role == "owner":
		from = []string{StatePending, StateApproved, StatePartial, StateFailed}
	case it.userID == actor.UserID && actor.UserID > 0:
		from = []string{StatePending}
	case isStaff(actor.Role):
		return Item{}, ErrForbidden
	default:
		return Item{}, ErrNotFound // not theirs: do not say it exists
	}
	switch err := s.store.transition(ctx, id, from, StateCancelled, s.now()); {
	case errors.Is(err, errWrongState):
		return Item{}, ErrNotCancellable
	case err != nil:
		return Item{}, err
	}
	s.publish("cancelled", id, StateCancelled)
	return s.item(ctx, id)
}

// SetState records how an approved request went on: available (everything is in
// the library), partial, failed, or back to approved. The importer and the
// downloader call it. Requests that are waiting, declined or cancelled cannot
// be moved this way.
func (s *Service) SetState(ctx context.Context, id int64, state string) error {
	switch state {
	case StateApproved, StateAvailable, StatePartial, StateFailed:
	default:
		return ErrBadState
	}
	err := s.store.transition(ctx, id, []string{StateApproved, StateAvailable, StatePartial, StateFailed}, state, s.now())
	switch {
	case errors.Is(err, errWrongState):
		return ErrBadState
	case err != nil:
		return err
	}
	s.publish("state", id, state)
	return nil
}

func (s *Service) publish(event string, id int64, state string) {
	if s.hub == nil {
		return
	}
	s.hub.Publish(Topic, map[string]any{"event": event, "id": id, "state": state})
}

// ---- reading ---------------------------------------------------------------

// item loads one request and fills in where its copy goes.
func (s *Service) item(ctx context.Context, id int64) (Item, error) {
	it, err := s.store.q().item(ctx, id)
	if err != nil {
		return Item{}, err
	}
	items := []Item{it}
	s.fillFacts(ctx, items)
	return items[0], nil
}

// fillFacts sets Facts.FolderPath: the copy's folder once there is one, and for
// a waiting request the folder it would go to.
func (s *Service) fillFacts(ctx context.Context, items []Item) {
	if len(items) == 0 {
		return
	}
	folders, err := s.lib.ListFolders(ctx)
	if err != nil {
		s.log.Warn("could not list library folders for the request list", "err", err)
		return
	}
	path := map[int64]string{}
	for _, f := range folders {
		path[f.ID] = f.Path
	}
	var cfg *Config
	for i := range items {
		it := &items[i]
		switch it.State {
		case StatePending:
			if cfg == nil {
				c := s.Config(ctx)
				cfg = &c
			}
			if f, ok := pickFolder(*cfg, it.Title.Kind, it.Quality, folders); ok {
				it.Facts.FolderPath = f.Path
			}
		case StateApproved, StateAvailable, StatePartial, StateFailed:
			it.Facts.FolderPath = path[it.folderID]
		}
	}
}

// Get returns one request. A member can only see their own; anyone else's is
// reported as not found.
func (s *Service) Get(ctx context.Context, actor Actor, id int64) (Item, error) {
	it, err := s.item(ctx, id)
	if err != nil {
		return Item{}, err
	}
	if !isStaff(actor.Role) && it.userID != actor.UserID {
		return Item{}, ErrNotFound
	}
	return it, nil
}

// List returns one page of requests, newest first. Members get only their own;
// the owner and admins get everyone's unless Mine is set.
func (s *Service) List(ctx context.Context, actor Actor, f ListFilter) (*ListResult, error) {
	if f.State != "" && !validState(f.State) {
		return nil, invalid("invalid_request", "state must be one of "+strings.Join(States, ", ")+".")
	}
	switch {
	case f.Limit <= 0:
		f.Limit = DefaultLimit
	case f.Limit > MaxLimit:
		f.Limit = MaxLimit
	}
	if f.Offset < 0 {
		return nil, invalid("invalid_request", "offset cannot be negative.")
	}
	var userID int64
	if f.Mine || !isStaff(actor.Role) {
		userID = actor.UserID
		if userID <= 0 {
			return nil, ErrForbidden
		}
	}
	items, total, err := s.store.q().list(ctx, itemFilter{UserID: userID, State: f.State, Limit: f.Limit, Offset: f.Offset})
	if err != nil {
		return nil, err
	}
	s.fillFacts(ctx, items)
	return &ListResult{Items: items, Total: total, Limit: f.Limit, Offset: f.Offset}, nil
}

// Summary is the numbers for the home screen.
func (s *Service) Summary(ctx context.Context, actor Actor) (Summary, error) {
	var sum Summary
	if isStaff(actor.Role) {
		all, err := s.store.q().stateCounts(ctx, 0)
		if err != nil {
			return Summary{}, err
		}
		sum.WaitingForYou = all[StatePending]
	}
	mine, err := s.store.q().stateCounts(ctx, actor.UserID)
	if err != nil {
		return Summary{}, err
	}
	sum.Mine.Pending, sum.Mine.Approved = mine[StatePending], mine[StateApproved]
	eff, _, err := s.limitsFor(ctx, actor.UserID, actor.Role, s.Config(ctx))
	if err != nil {
		return Summary{}, err
	}
	if sum.Usage, err = s.usage(ctx, actor.UserID, eff); err != nil {
		return Summary{}, err
	}
	return sum, nil
}

// ---- people ----------------------------------------------------------------

func personInfo(u userRow, o LimitOverrides, c Config, times map[usageKey][]time.Time) PersonInfo {
	eff := effective(u.Role, o, c)
	return PersonInfo{
		ID: u.ID, Name: u.Name, Role: u.Role, Disabled: u.Disabled,
		Limits: PersonLimits{Effective: eff, Overrides: o},
		Usage: Usage{
			HD:  usageOf(times[usageKey{u.ID, QualityHD}], eff.HDPerWeek),
			UHD: usageOf(times[usageKey{u.ID, QualityUHD}], eff.UHDPerWeek),
		},
		AutoApprove: AutoApprove{HD: eff.AutoApproveHD, UHD: eff.AutoApproveUHD},
	}
}

// People lists everyone with their limits, this week's usage and auto-approve
// flags. Owner and admins only.
func (s *Service) People(ctx context.Context, actor Actor) (*PeopleResult, error) {
	if !isStaff(actor.Role) {
		return nil, ErrForbidden
	}
	users, err := s.store.q().users(ctx)
	if err != nil {
		return nil, err
	}
	over, err := s.store.q().allOverrides(ctx)
	if err != nil {
		return nil, err
	}
	times, err := s.store.q().usageAll(ctx, s.now().Add(-Window))
	if err != nil {
		return nil, err
	}
	c := s.Config(ctx)
	out := &PeopleResult{People: make([]PersonInfo, 0, len(users)), Defaults: c}
	for _, u := range users {
		out.People = append(out.People, personInfo(u, over[u.ID], c, times))
	}
	return out, nil
}

// SetLimits changes one person's overrides. Owner only. A field of the patch
// that is not set is left alone; one that is set to nil goes back to the default.
func (s *Service) SetLimits(ctx context.Context, actor Actor, userID int64, p LimitsPatch) (PersonInfo, error) {
	if actor.Role != "owner" {
		return PersonInfo{}, ErrForbidden
	}
	for _, v := range []Patch[int]{p.HDPerWeek, p.UHDPerWeek} {
		if v.Set && v.Value != nil && (*v.Value < 0 || *v.Value > maxPerWeek) {
			return PersonInfo{}, invalid("invalid_limit", "A weekly limit is a whole number from 0 to "+strconv.Itoa(maxPerWeek)+".")
		}
	}
	o, err := s.store.setOverrides(ctx, userID, p)
	if err != nil {
		return PersonInfo{}, err
	}
	u, err := s.store.q().user(ctx, userID)
	if err != nil {
		return PersonInfo{}, err
	}
	times, err := s.store.q().usageAll(ctx, s.now().Add(-Window))
	if err != nil {
		return PersonInfo{}, err
	}
	s.log.Info("request limits changed", "user", userID, "by", actor.UserID)
	return personInfo(u, o, s.Config(ctx), times), nil
}
