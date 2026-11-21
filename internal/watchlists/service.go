package watchlists

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"strconv"
	"strings"
	"sync"
	"time"
	"unicode"

	"github.com/BaileyPillon/marquee/internal/core/secrets"
	"github.com/BaileyPillon/marquee/internal/metadata"
	"github.com/BaileyPillon/marquee/internal/settings"
)

// Options configure the service.
type Options struct {
	Logger *slog.Logger
	// Now replaces the clock (tests).
	Now func() time.Time
	// Plex is the Plex client. Required.
	Plex PlexAPI
	// Asker makes the asks. Required.
	Asker Asker
	// Members makes a person for a Plex friend (the autoCreateMembers option).
	// Without it the option does nothing.
	Members Members
	// Shows finds a show at TVmaze. Without it a show cannot be resolved.
	Shows ShowFinder
	// HasTMDBKey says whether the owner has given Marquee a TMDB key, which a
	// movie needs. Without it every movie is tried.
	HasTMDBKey func(ctx context.Context) bool
}

// Service is the watchlists module.
type Service struct {
	store *Store
	cfg   *settings.Service
	box   *secrets.Box
	o     Options
	log   *slog.Logger
	now   func() time.Time

	syncMu   sync.Mutex // one check at a time
	clientMu sync.Mutex // the client identifier is made once

	pinMu sync.Mutex
	pins  map[int64]pendingPin // links waiting for the owner's yes
	done  map[int64]bool       // links that went through
}

type pendingPin struct {
	code    string
	expires time.Time
}

// pinLife is how long a pending link is remembered; Plex's own pins live
// about 30 minutes.
const pinLife = 30 * time.Minute

// NewService builds the module on the main database.
func NewService(d *sql.DB, cfg *settings.Service, box *secrets.Box, o Options) *Service {
	if o.Logger == nil {
		o.Logger = slog.New(slog.NewTextHandler(io.Discard, nil))
	}
	if o.Now == nil {
		o.Now = time.Now
	}
	return &Service{
		store: NewStore(d), cfg: cfg, box: box, o: o, log: o.Logger, now: o.Now,
		pins: map[int64]pendingPin{}, done: map[int64]bool{},
	}
}

// ---- who Marquee is to Plex --------------------------------------------------

// clientID is the stable identifier Marquee gives Plex. It is made the first
// time it is needed and kept in the "watchlists-client" settings section.
func (s *Service) clientID(ctx context.Context) (string, error) {
	s.clientMu.Lock()
	defer s.clientMu.Unlock()
	var v struct {
		ClientID string `json:"clientId"`
	}
	if _, err := s.cfg.Get(ctx, ClientSection, &v); err != nil {
		return "", err
	}
	if v.ClientID != "" {
		return v.ClientID, nil
	}
	b := make([]byte, 16)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	v.ClientID = "marquee-" + hex.EncodeToString(b)
	if err := s.cfg.Set(ctx, ClientSection, v); err != nil {
		return "", err
	}
	return v.ClientID, nil
}

// ---- linking the owner's Plex account -----------------------------------------

// StartLink asks Plex for a link request. Send the owner to Link.AuthURL, then
// poll CheckLink with Link.PinID.
func (s *Service) StartLink(ctx context.Context) (Link, error) {
	cid, err := s.clientID(ctx)
	if err != nil {
		return Link{}, err
	}
	pin, err := s.o.Plex.CreatePIN(ctx, Auth{ClientID: cid})
	if err != nil {
		return Link{}, err
	}
	s.pinMu.Lock()
	now := s.now()
	for id, p := range s.pins { // forget old ones
		if now.After(p.expires) {
			delete(s.pins, id)
		}
	}
	s.pins[pin.ID] = pendingPin{code: pin.Code, expires: now.Add(pinLife)}
	s.pinMu.Unlock()
	return Link{PinID: pin.ID, Code: pin.Code, AuthURL: authURL(cid, pin.Code)}, nil
}

// CheckLink asks Plex whether the owner has agreed to a link yet. Once they
// have, the token is sealed and kept, the Plex account is stored as linked to
// the Marquee person userID (the owner, whose own asks the account's watchlist
// makes), and the answer is true (and stays true for that pin). ErrPinExpired
// means Plex no longer knows the pin.
func (s *Service) CheckLink(ctx context.Context, pinID, userID int64) (bool, error) {
	s.pinMu.Lock()
	if s.done[pinID] {
		s.pinMu.Unlock()
		return true, nil
	}
	code := s.pins[pinID].code
	s.pinMu.Unlock()

	cid, err := s.clientID(ctx)
	if err != nil {
		return false, err
	}
	token, err := s.o.Plex.CheckPIN(ctx, Auth{ClientID: cid}, pinID, code)
	if err != nil {
		if errors.Is(err, ErrPinExpired) {
			s.pinMu.Lock()
			delete(s.pins, pinID)
			s.pinMu.Unlock()
		}
		return false, err
	}
	if token == "" {
		return false, nil
	}
	who, err := s.o.Plex.Account(ctx, Auth{ClientID: cid, Token: token})
	if err != nil {
		return false, err
	}
	sealed, err := s.box.Seal(token)
	if err != nil {
		return false, err
	}
	if err := s.store.saveAccount(ctx, account{
		UserID: userID, PlexUserID: who.ID, PlexUsername: who.Username, TokenSealed: sealed, LinkedAt: s.now(),
	}); err != nil {
		return false, err
	}
	s.pinMu.Lock()
	delete(s.pins, pinID)
	s.done[pinID] = true
	s.pinMu.Unlock()
	s.log.Info("plex account linked", "plex_user", who.Username)
	return true, nil
}

// Unlink forgets the Plex account and its token. The links between friends and
// people, and the items already looked at, stay, so linking again does not ask
// for everything a second time.
func (s *Service) Unlink(ctx context.Context) error {
	// A check that is running goes on with the token it already has; its last
	// result finds no account to be saved on, which is fine.
	s.pinMu.Lock()
	s.done = map[int64]bool{}
	s.pinMu.Unlock()
	return s.store.deleteAccount(ctx)
}

// ---- reading ------------------------------------------------------------------

// Status is the link, the settings and the last check, for the owner.
func (s *Service) Status(ctx context.Context) (Status, error) {
	cfg, err := LoadConfig(ctx, s.cfg)
	if err != nil {
		s.log.Warn("the watchlists settings cannot be read; using the defaults", "err", err)
	}
	st := Status{
		Enabled: cfg.Plex.Enabled, IncludeFriends: cfg.Plex.IncludeFriends,
		AutoCreateMembers: cfg.Plex.AutoCreateMembers, Quality: cfg.Plex.Quality,
		Friends: []Friend{}, LastResult: newResult(),
	}
	acct, linked, err := s.store.account(ctx)
	if err != nil || !linked {
		return st, err
	}
	st.Linked = true
	name := acct.PlexUsername
	st.User = &name
	st.LastCheckedAt, st.LastError, st.LastResult = acct.LastCheckedAt, acct.LastError, acct.LastResult
	if st.Friends, err = s.store.friends(ctx); err != nil {
		return Status{}, err
	}
	return st, nil
}

// SetFriendLink links a Plex friend to a Marquee person, or (userID nil) unlinks
// them. From the next check the friend's items become that person's asks.
func (s *Service) SetFriendLink(ctx context.Context, plexUserID string, userID *int64) error {
	f, ok, err := s.store.friend(ctx, plexUserID)
	if err != nil {
		return err
	}
	if !ok {
		return ErrUnknownFriend
	}
	if userID == nil {
		return s.store.deleteLink(ctx, plexUserID)
	}
	if _, ok, err := s.store.user(ctx, *userID); err != nil {
		return err
	} else if !ok {
		return ErrUserNotFound
	}
	return s.store.setLink(ctx, plexUserID, *userID, firstNonEmpty(f.Username, f.DisplayName), s.now())
}

// ---- the check ----------------------------------------------------------------

// SyncJob is the scheduled "watchlists.sync" job: a check when a Plex account
// is linked and the option is on, nothing otherwise.
func (s *Service) SyncJob(ctx context.Context) error {
	_, err := s.Sync(ctx)
	switch {
	case errors.Is(err, ErrNotLinked), errors.Is(err, ErrDisabled), errors.Is(err, ErrRunning):
		return nil
	case err != nil && ctx.Err() != nil:
		return nil // shutting down
	}
	return err
}

// run is one check.
type run struct {
	cfg  Config
	auth Auth
	res  Result
	now  time.Time
}

// Sync reads the owner's watchlist (and the friends', if that is on) and makes
// an ask for each item not looked at before. It returns what it did, and keeps
// that as the last result. ErrNotLinked, ErrDisabled and ErrRunning say why
// nothing happened.
func (s *Service) Sync(ctx context.Context) (Result, error) {
	if !s.syncMu.TryLock() {
		return Result{}, ErrRunning
	}
	defer s.syncMu.Unlock()

	acct, linked, err := s.store.account(ctx)
	if err != nil {
		return Result{}, err
	}
	if !linked {
		return Result{}, ErrNotLinked
	}
	cfg, err := LoadConfig(ctx, s.cfg)
	if err != nil {
		s.log.Warn("the watchlists settings cannot be read; using the defaults", "err", err)
	}
	if !cfg.Plex.Enabled {
		return Result{}, ErrDisabled
	}
	token, err := s.box.Open(acct.TokenSealed)
	if err != nil {
		msg := "The saved Plex link cannot be read (was the master key replaced?). Link the account again."
		_ = s.store.saveCheck(ctx, s.now(), newResult(), msg)
		return Result{}, errors.New(msg)
	}
	cid, err := s.clientID(ctx)
	if err != nil {
		return Result{}, err
	}
	r := &run{cfg: cfg, auth: Auth{ClientID: cid, Token: token}, res: newResult(), now: s.now()}

	runErr := s.syncOwner(ctx, r, acct)
	if runErr == nil || !errors.Is(runErr, ErrPlexAuth) {
		if cfg.Plex.IncludeFriends {
			if err := s.syncFriends(ctx, r); err != nil && runErr == nil {
				runErr = err
			}
		}
	}
	if ctx.Err() != nil { // shutting down: do not keep half a check as the result
		return r.res, ctx.Err()
	}
	msg := ""
	if runErr != nil {
		msg = describe(runErr)
		s.log.Warn("the Plex watchlist check did not finish cleanly", "err", runErr)
	}
	if err := s.store.saveCheck(ctx, s.now(), r.res, msg); err != nil {
		return r.res, err
	}
	s.log.Info("plex watchlists checked", "asked", r.res.Asked, "skipped", r.res.Skipped)
	return r.res, runErr
}

// describe puts a failure into words for the owner.
func describe(err error) string {
	if errors.Is(err, ErrPlexAuth) {
		return "Plex no longer accepts the link. Link the account again."
	}
	return "Plex did not answer as expected: " + err.Error()
}

func (s *Service) syncOwner(ctx context.Context, r *run, acct account) error {
	who, ok, err := s.store.user(ctx, acct.UserID)
	if err != nil {
		return err
	}
	if !ok {
		return errors.New("there is no Marquee person for the owner's own watchlist")
	}
	items, err := s.o.Plex.OwnerWatchlist(ctx, r.auth)
	if err != nil {
		return err
	}
	for _, it := range items {
		if err := s.processItem(ctx, r, acct.PlexUserID, who, it); err != nil {
			return err
		}
	}
	return nil
}

func (s *Service) syncFriends(ctx context.Context, r *run) error {
	friends, err := s.o.Plex.Friends(ctx, r.auth)
	if err != nil {
		return err
	}
	if err := s.store.replaceFriends(ctx, friends, r.now); err != nil {
		return err
	}
	var failed int
	var first error
	for _, f := range friends {
		if err := s.syncFriend(ctx, r, f); err != nil {
			if ctx.Err() != nil || errors.Is(err, ErrPlexAuth) {
				return err
			}
			failed++
			if first == nil {
				first = err
			}
			s.log.Warn("could not read a Plex friend's watchlist", "friend", f.Username, "err", err)
		}
	}
	if failed > 0 {
		return fmt.Errorf("%d of %d friends' watchlists could not be read: %w", failed, len(friends), first)
	}
	return nil
}

func (s *Service) syncFriend(ctx context.Context, r *run, f PlexUser) error {
	items, err := s.o.Plex.FriendWatchlist(ctx, r.auth, f.ID)
	if err != nil {
		return err
	}
	who, linked, err := s.store.link(ctx, f.ID)
	if err != nil {
		return err
	}
	if !linked && r.cfg.Plex.AutoCreateMembers && s.o.Members != nil && len(items) > 0 {
		if who, linked, err = s.makeMember(ctx, f, r.now); err != nil {
			return err
		}
	}
	if !linked {
		r.res.Skipped[SkipUnlinkedFriend] += len(items)
		return nil
	}
	for _, it := range items {
		if err := s.processItem(ctx, r, f.ID, who, it); err != nil {
			return err
		}
	}
	return nil
}

// makeMember makes a Marquee member for a Plex friend and links them. When no
// name works it reports the friend as not linked.
func (s *Service) makeMember(ctx context.Context, f PlexUser, now time.Time) (userRef, bool, error) {
	base := firstNonEmpty(f.DisplayName, f.Username)
	names := []string{base, base + " (Plex)"}
	for i := 2; i <= 5; i++ {
		names = append(names, base+" (Plex "+strconv.Itoa(i)+")")
	}
	for _, name := range names {
		id, err := s.o.Members.CreateMember(ctx, name)
		if errors.Is(err, ErrNameTaken) {
			continue
		}
		if err != nil {
			s.log.Warn("could not make a member for a Plex friend", "friend", f.Username, "err", err)
			return userRef{}, false, nil
		}
		if err := s.store.setLink(ctx, f.ID, id, firstNonEmpty(f.Username, f.DisplayName), now); err != nil {
			return userRef{}, false, err
		}
		u, ok, err := s.store.user(ctx, id)
		s.log.Info("member made for a Plex friend", "friend", f.Username, "user", id)
		return u, ok, err
	}
	return userRef{}, false, nil
}

// ---- one item ----------------------------------------------------------------

func (s *Service) processItem(ctx context.Context, r *run, plexUserID string, who userRef, it Item) error {
	if it.Key == "" {
		return nil
	}
	row, found, err := s.store.item(ctx, SourcePlex, plexUserID, it.Key)
	if err != nil {
		return err
	}
	if found && row.done() {
		return nil // dealt with at an earlier check
	}
	if !found {
		row = itemRow{
			Source: SourcePlex, PlexUserID: plexUserID, Key: it.Key, Title: it.Title, Year: it.Year,
			Type: it.Type, Guids: it.Guids, FirstSeenAt: r.now,
		}
	} else if len(row.Guids) == 0 {
		row.Guids = it.Guids
	}
	skip := func(reason string) error {
		row.RequestID, row.SkippedReason = 0, reason
		r.res.Skipped[reason]++
		return s.store.putItem(ctx, row)
	}

	if row.Type != "movie" && row.Type != "show" {
		return skip(SkipUnsupportedType)
	}
	if who.Disabled {
		return skip(SkipUserDisabled)
	}
	if len(row.Guids) == 0 { // a friend's items come without ids
		guids, err := s.o.Plex.ItemGuids(ctx, r.auth, it.Key)
		if err != nil {
			if ctx.Err() != nil || errors.Is(err, ErrPlexAuth) {
				return err
			}
			s.log.Debug("could not read the ids of a watchlist item", "err", err)
			return skip(SkipLookupFailed)
		}
		row.Guids = guids
	}

	in := AskInput{
		UserID: who.ID, Role: who.Role, Quality: r.cfg.Plex.Quality,
		SourceRef: "plex:" + plexUserID + ":" + it.Key,
	}
	if reason := s.resolve(ctx, row, &in); reason != "" {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		return skip(reason)
	}

	res, err := s.o.Asker.Ask(ctx, in)
	switch {
	case err == nil:
		row.RequestID, row.SkippedReason = res.RequestID, ""
		r.res.Asked++
		return s.store.putItem(ctx, row)
	case ctx.Err() != nil:
		return ctx.Err()
	}
	return skip(s.askFailure(err))
}

// askFailure names why an ask did not happen.
func (s *Service) askFailure(err error) string {
	switch {
	case errors.Is(err, ErrLimitReached):
		return SkipLimitReached
	case errors.Is(err, ErrAlreadyAsked):
		return SkipAlreadyAsked
	case errors.Is(err, ErrNoFolder):
		return SkipNoLibraryFolder
	case errors.Is(err, metadata.ErrTMDBKeyMissing), errors.Is(err, metadata.ErrTMDBKeyInvalid):
		return SkipTMDBKeyMissing
	case errors.Is(err, metadata.ErrNotFound):
		return SkipNotFound
	case errors.Is(err, metadata.ErrUpstream):
		return SkipLookupFailed
	}
	s.log.Warn("a watchlist ask failed", "err", err)
	return SkipAskFailed
}

// resolve works out which title an item is: a TMDB movie, or a TVmaze show. It
// fills in the title part of in, and otherwise returns the reason the item
// cannot be resolved.
func (s *Service) resolve(ctx context.Context, row itemRow, in *AskInput) string {
	switch row.Type {
	case "movie":
		if s.o.HasTMDBKey != nil && !s.o.HasTMDBKey(ctx) {
			return SkipTMDBKeyMissing
		}
		id := guidValue(row.Guids, "tmdb")
		if n, err := strconv.ParseInt(id, 10, 64); err != nil || n <= 0 {
			return SkipNoTMDBID
		}
		in.Kind, in.Provider, in.ProviderID = "movie", metadata.ProviderTMDB, id
		return ""
	default: // show
		id, reason := s.findShow(ctx, row)
		if reason != "" {
			return reason
		}
		in.Kind, in.Provider, in.ProviderID = "show", metadata.ProviderTVmaze, id
		return ""
	}
}

// findShow finds a show at TVmaze: by its IMDb id when Plex gave one, else (or
// when TVmaze does not know it) by name and year.
func (s *Service) findShow(ctx context.Context, row itemRow) (id, reason string) {
	if s.o.Shows == nil {
		return "", SkipNoMatch
	}
	if imdb := guidValue(row.Guids, "imdb"); imdb != "" {
		hit, err := s.o.Shows.ShowByIMDb(ctx, imdb)
		switch {
		case err == nil && hit != nil && hit.ProviderID != "":
			return hit.ProviderID, ""
		case err == nil, errors.Is(err, metadata.ErrNotFound):
			// not there by IMDb id: try the name
		default:
			return "", SkipLookupFailed
		}
	}
	if strings.TrimSpace(row.Title) == "" {
		return "", SkipNoMatch
	}
	hits, err := s.o.Shows.SearchShows(ctx, row.Title)
	if err != nil {
		if errors.Is(err, metadata.ErrNotFound) {
			return "", SkipNoMatch
		}
		return "", SkipLookupFailed
	}
	want := normalizeName(row.Title)
	for _, h := range hits {
		if normalizeName(h.Name) != want || h.ProviderID == "" {
			continue
		}
		if row.Year != 0 && h.Year != 0 && abs(h.Year-row.Year) > 1 {
			continue
		}
		return h.ProviderID, ""
	}
	return "", SkipNoMatch
}

func abs(n int) int {
	if n < 0 {
		return -n
	}
	return n
}

// normalizeName lower-cases a name and drops everything but letters and digits,
// so "Marvel's Agents of S.H.I.E.L.D." and "marvels agents of shield" compare equal.
func normalizeName(s string) string {
	var b strings.Builder
	for _, r := range strings.ToLower(s) {
		if unicode.IsLetter(r) || unicode.IsDigit(r) {
			b.WriteRune(r)
		}
	}
	return b.String()
}
