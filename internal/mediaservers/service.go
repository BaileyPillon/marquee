package mediaservers

import (
	"context"
	"database/sql"
	"errors"
	"io"
	"log/slog"
	"strings"
	"sync"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/BaileyPillon/marquee/internal/core/outbound"
	"github.com/BaileyPillon/marquee/internal/core/pathmap"
	"github.com/BaileyPillon/marquee/internal/core/secrets"
	"github.com/BaileyPillon/marquee/internal/settings"
	"github.com/BaileyPillon/marquee/internal/titles"
)

// Options are what a Service needs besides the database.
type Options struct {
	Logger *slog.Logger
	// HTTP is the one outbound client every call to a server goes through.
	HTTP *outbound.Client
	// Box seals and opens the servers' tokens.
	Box *secrets.Box
	// Now is a test hook.
	Now func() time.Time
}

// Service is the media-servers module.
type Service struct {
	store *Store
	log   *slog.Logger
	hc    *outbound.Client
	box   *secrets.Box
	now   func() time.Time
}

// New builds the service on the main database.
func New(d *sql.DB, o Options) *Service {
	if o.Logger == nil {
		o.Logger = slog.New(slog.NewTextHandler(io.Discard, nil))
	}
	if o.Now == nil {
		o.Now = time.Now
	}
	return &Service{store: NewStore(d), log: o.Logger, hc: o.HTTP, box: o.Box, now: o.Now}
}

// ---- what the owner sees -------------------------------------------------------

// View is a media server as the API shows it. The token is masked: a value
// starting with settings.MaskPrefix means "set"; sending it back unchanged
// keeps the stored one.
type View struct {
	ID        int64          `json:"id"`
	Name      string         `json:"name"`
	Kind      string         `json:"kind"`
	URL       string         `json:"url"`
	Token     string         `json:"token"`
	Enabled   bool           `json:"enabled"`
	PathMap   []pathmap.Rule `json:"pathMap"`
	LastOKAt  *time.Time     `json:"lastOkAt"`
	LastError string         `json:"lastError"`
	CreatedAt time.Time      `json:"createdAt"`
}

func (s *Service) view(r serverRow) View {
	pm := r.PathMap
	if pm == nil {
		pm = []pathmap.Rule{}
	}
	return View{
		ID: r.ID, Name: r.Name, Kind: r.Kind, URL: r.URL, Token: s.mask(r.TokenSealed), Enabled: r.Enabled,
		PathMap: pm, LastOKAt: r.LastOKAt, LastError: r.LastError, CreatedAt: r.CreatedAt,
	}
}

// mask shows a stored token the way settings does: dots, plus the last four
// characters of a long one. A token that cannot be opened shows plain dots.
func (s *Service) mask(sealed string) string {
	if sealed == "" {
		return ""
	}
	plain, err := s.box.Open(sealed)
	if err != nil {
		return settings.MaskPrefix
	}
	return settings.Mask(plain)
}

// Input is what the owner sends to add or change a server. When changing, a
// field left out keeps its value. A token that is left out or still masked
// keeps the stored one; an empty one clears it.
type Input struct {
	Name    *string         `json:"name"`
	Kind    *string         `json:"kind"`
	URL     *string         `json:"url"`
	Token   *string         `json:"token"`
	Enabled *bool           `json:"enabled"`
	PathMap *[]pathmap.Rule `json:"pathMap"`
}

const (
	maxNameRunes  = 100
	maxTokenRunes = 500
)

// List returns every server, enabled or not.
func (s *Service) List(ctx context.Context) ([]View, error) {
	rows, err := s.store.list(ctx, false)
	if err != nil {
		return nil, err
	}
	out := make([]View, len(rows))
	for i, r := range rows {
		out[i] = s.view(r)
	}
	return out, nil
}

// Get returns one server.
func (s *Service) Get(ctx context.Context, id int64) (View, error) {
	r, err := s.store.get(ctx, id)
	if err != nil {
		return View{}, err
	}
	return s.view(r), nil
}

// Add creates a server.
func (s *Service) Add(ctx context.Context, in Input) (View, error) {
	r := serverRow{Enabled: true, PathMap: []pathmap.Rule{}}
	if in.Kind == nil || strings.TrimSpace(*in.Kind) == "" {
		return View{}, &InvalidError{Message: "Say which media server this is."}
	}
	r.Kind = strings.ToLower(strings.TrimSpace(*in.Kind))
	if !validKind(r.Kind) {
		return View{}, &InvalidError{Message: "The kind must be jellyfin, emby or plex."}
	}
	if err := s.apply(&r, in, nil); err != nil {
		return View{}, err
	}
	if r.Name == "" {
		return View{}, &InvalidError{Message: "Give the server a name."}
	}
	if r.URL == "" {
		return View{}, &InvalidError{Message: "Give the address of the server."}
	}
	row, err := s.store.insert(ctx, r, s.now())
	if err != nil {
		return View{}, err
	}
	return s.view(row), nil
}

// Update changes a server. Its kind cannot change.
func (s *Service) Update(ctx context.Context, id int64, in Input) (View, error) {
	old, err := s.store.get(ctx, id)
	if err != nil {
		return View{}, err
	}
	if in.Kind != nil && strings.ToLower(strings.TrimSpace(*in.Kind)) != old.Kind {
		return View{}, &InvalidError{Message: "The kind of a media server cannot be changed. Add a new one instead."}
	}
	r := old
	if err := s.apply(&r, in, &old); err != nil {
		return View{}, err
	}
	if r.Name == "" || r.URL == "" {
		return View{}, &InvalidError{Message: "A media server needs a name and an address."}
	}
	if err := s.store.update(ctx, r); err != nil {
		return View{}, err
	}
	row, err := s.store.get(ctx, id)
	if err != nil {
		return View{}, err
	}
	return s.view(row), nil
}

func (s *Service) apply(r *serverRow, in Input, old *serverRow) error {
	if in.Name != nil {
		name := strings.TrimSpace(*in.Name)
		if name == "" || utf8.RuneCountInString(name) > maxNameRunes || hasControl(name) {
			return &InvalidError{Message: "The name must be 1 to 100 characters."}
		}
		r.Name = name
	}
	if in.URL != nil {
		base, err := normalizeBase(*in.URL)
		if err != nil {
			return err
		}
		r.URL = base
	}
	if in.Enabled != nil {
		r.Enabled = *in.Enabled
	}
	if in.PathMap != nil {
		rules, err := pathmap.Clean(*in.PathMap)
		if err != nil {
			return &InvalidError{Message: "The path rules are not valid: " + err.Error() + "."}
		}
		r.PathMap = rules
	}
	switch {
	case in.Token == nil, strings.HasPrefix(*in.Token, settings.MaskPrefix):
	case *in.Token == "":
		r.TokenSealed = ""
	case utf8.RuneCountInString(*in.Token) > maxTokenRunes || hasControl(*in.Token):
		return &InvalidError{Message: "That token is not valid."}
	default:
		sealed, err := s.box.Seal(strings.TrimSpace(*in.Token))
		if err != nil {
			return err
		}
		r.TokenSealed = sealed
	}
	// A stored token is never sent to a different host: someone who can edit the
	// address could otherwise collect it. Type it again for the new address.
	if old != nil && hostOf(r.URL) != hostOf(old.URL) && old.TokenSealed != "" && r.TokenSealed == old.TokenSealed {
		return &InvalidError{Message: "You changed the address, so type the token again. Marquee does not send a saved one to a new address."}
	}
	return nil
}

func hasControl(s string) bool {
	for _, r := range s {
		if unicode.IsControl(r) {
			return true
		}
	}
	return false
}

// Delete removes a server.
func (s *Service) Delete(ctx context.Context, id int64) error { return s.store.delete(ctx, id) }

// ---- talking to a server -------------------------------------------------------

var errTokenUnreadable = errors.New("the saved token cannot be read (was the master key file replaced?); type it again in Settings")

func (s *Service) adapter(r serverRow) (adapter, error) {
	token := ""
	if r.TokenSealed != "" {
		t, err := s.box.Open(r.TokenSealed)
		if err != nil {
			return nil, errTokenUnreadable
		}
		token = t
	}
	switch r.Kind {
	case KindJellyfin, KindEmby:
		return newJellyfin(s.hc, r.Kind, r.URL, token), nil
	case KindPlex:
		return newPlex(s.hc, r.URL, token), nil
	}
	return nil, &InvalidError{Message: "That kind of media server is not supported."}
}

// Test signs in to a server, reads its version and lists its libraries. A server
// that does not answer is not an error here: the result says so.
func (s *Service) Test(ctx context.Context, id int64) (TestResult, error) {
	r, err := s.store.get(ctx, id)
	if err != nil {
		return TestResult{}, err
	}
	a, err := s.adapter(r)
	var (
		version string
		libs    []Library
	)
	if err == nil {
		version, libs, err = a.test(ctx)
	}
	if err != nil {
		msg := plain(err)
		_ = s.store.failed(context.WithoutCancel(ctx), id, msg)
		return TestResult{OK: false, Version: version, Error: msg, Libraries: []Library{}}, nil
	}
	_ = s.store.ok(context.WithoutCancel(ctx), id, s.now())
	if libs == nil {
		libs = []Library{}
	}
	return TestResult{OK: true, Version: version, Libraries: libs}, nil
}

// Notify tells every enabled server about a change. It waits for the servers
// (they are asked at the same time) and returns how each took it. A server that
// fails is logged and its row records the problem, but nothing here is fatal: an
// import is not undone because Jellyfin is down.
func (s *Service) Notify(ctx context.Context, ev Event) []NotifyResult {
	if ev.Kind != EventCreated && ev.Kind != EventDeleted {
		return nil
	}
	rows, err := s.store.list(ctx, true)
	if err != nil {
		s.log.Warn("media servers: cannot list the servers", "err", err)
		return nil
	}
	if len(rows) == 0 {
		return nil
	}
	results := make([]NotifyResult, len(rows))
	var wg sync.WaitGroup
	for i, r := range rows {
		wg.Add(1)
		go func() {
			defer wg.Done()
			results[i] = s.notifyOne(ctx, r, ev)
		}()
	}
	wg.Wait()
	return results
}

// NotifyRenamed tells the servers that files moved: the old paths are gone and
// the new ones are there.
func (s *Service) NotifyRenamed(ctx context.Context, oldPaths, newPaths []string) []NotifyResult {
	out := s.Notify(ctx, Event{Kind: EventDeleted, Paths: oldPaths})
	return append(out, s.Notify(ctx, Event{Kind: EventCreated, Paths: newPaths})...)
}

func (s *Service) notifyOne(ctx context.Context, r serverRow, ev Event) NotifyResult {
	res := NotifyResult{Server: ServerRef{ID: r.ID, Name: r.Name, Kind: r.Kind}}
	paths := make([]string, len(ev.Paths))
	for i, p := range ev.Paths {
		paths[i] = pathmap.Map(r.PathMap, p)
	}
	res.Sent = paths
	a, err := s.adapter(r)
	if err == nil {
		err = a.notify(ctx, Event{Kind: ev.Kind, Paths: paths})
	}
	bg := context.WithoutCancel(ctx)
	if err != nil {
		msg := plain(err)
		res.Error = msg
		s.log.Warn("media server was not told about a change", "server", r.Name, "kind", r.Kind, "err", msg)
		_ = s.store.failed(bg, r.ID, msg)
		return res
	}
	res.OK = true
	_ = s.store.ok(bg, r.ID, s.now())
	return res
}

// OpenURL finds a title on the enabled servers and returns the address of its
// page there. The first server that has it wins. ErrNotOnServer means none has
// it (or none is set up); if every server that was asked failed, the last
// failure is returned instead.
func (s *Service) OpenURL(ctx context.Context, t titles.Title) (*OpenResult, error) {
	rows, err := s.store.list(ctx, true)
	if err != nil {
		return nil, err
	}
	if len(rows) == 0 {
		return nil, ErrNotOnServer
	}
	q := queryOf(t)
	var lastErr error
	failed := 0
	for _, r := range rows {
		a, err := s.adapter(r)
		if err != nil {
			lastErr, failed = err, failed+1
			continue
		}
		url, ok, err := a.open(ctx, q)
		if err != nil {
			s.log.Warn("media server could not look up a title", "server", r.Name, "err", plain(err))
			lastErr, failed = err, failed+1
			continue
		}
		if ok {
			return &OpenResult{URL: url, Server: ServerRef{ID: r.ID, Name: r.Name, Kind: r.Kind}}, nil
		}
	}
	if failed == len(rows) && lastErr != nil {
		return nil, lastErr
	}
	return nil, ErrNotOnServer
}

// PlainError says what went wrong with a server in words the owner can act on.
func PlainError(err error) string { return plain(err) }
