// Package sources talks to the places Marquee finds releases: Torznab feeds
// (torrents) and Newznab feeds (usenet), either added by hand or imported from
// a Prowlarr or Jackett the owner already runs.
//
// It owns the indexers table and the short memory of what the feeds showed
// lately (releases_seen). Deciding which release is good is the decide module's
// job; this package only fetches and parses.
//
// API keys travel in the query string of a Torznab URL, so three rules hold
// everywhere: a key is sealed at rest, it is shown masked, and no error text,
// log line or response ever contains the request URL's query.
package sources

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/url"
	"strings"
	"sync/atomic"
	"time"
	"unicode/utf8"

	"github.com/BaileyPillon/marquee/internal/core/outbound"
	"github.com/BaileyPillon/marquee/internal/core/secrets"
	"github.com/BaileyPillon/marquee/internal/grab"
	"github.com/BaileyPillon/marquee/internal/settings"
)

// Kinds of indexer.
const (
	KindTorznab = "torznab"
	KindNewznab = "newznab"
)

// Protocol is what a feed of that kind carries.
func Protocol(kind string) string {
	if kind == KindNewznab {
		return grab.ProtocolUsenet
	}
	return grab.ProtocolTorrent
}

// Defaults and limits.
const (
	DefaultPriority = 25
	MinPriority     = 1
	MaxPriority     = 50

	// SearchTimeout is how long one indexer gets to answer a search.
	SearchTimeout = 30 * time.Second
	// SearchConcurrency is how many indexers are asked at once.
	SearchConcurrency = 8
	// DefaultLimit is how many items one search asks for.
	DefaultLimit = 100

	// RSSJob and RSSInterval describe the scheduled feed sync.
	RSSJob      = "sources.rss"
	RSSInterval = 15 * time.Minute
	// SeenKeep is how long releases_seen remembers an item.
	SeenKeep = 7 * 24 * time.Hour

	// capsMaxAge is how old cached capabilities get before a search refreshes them.
	capsMaxAge = 7 * 24 * time.Hour

	maxNameRunes = 100
	maxURLLen    = 2048
	maxKeyLen    = 512
	maxCats      = 100
)

// Errors the web layer maps to HTTP statuses.
var (
	ErrNotFound  = errors.New("sources: no such source")
	ErrDuplicate = errors.New("sources: a source with that address already exists")
)

// InvalidError is a request the caller can fix; Message is plain words.
type InvalidError struct{ Message string }

func (e *InvalidError) Error() string { return e.Message }

func invalid(msg string) error { return &InvalidError{Message: msg} }

// Error is a failure talking to a source, in plain words. Code is stable.
type Error struct {
	Code    string
	Message string
}

func (e *Error) Error() string { return e.Message }

// Codes of Error.
const (
	CodeUnreachable = "unreachable"
	CodeKeyRefused  = "key_refused"
	CodeNotAFeed    = "not_a_feed"
	CodeSourceError = "source_error"
	CodeSlowDown    = "slow_down"
	CodeTimeout     = "timeout"
)

func failure(code, msg string) *Error { return &Error{Code: code, Message: msg} }

// Indexer is a configured source as shown to the owner. The key is masked.
type Indexer struct {
	ID          int64      `json:"id"`
	Name        string     `json:"name"`
	Kind        string     `json:"kind"`
	URL         string     `json:"url"`
	APIKey      string     `json:"apiKey"` // masked, never the key
	Categories  []int      `json:"categories"`
	Enabled     bool       `json:"enabled"`
	Priority    int        `json:"priority"`
	Caps        *Caps      `json:"caps"`
	LastOKAt    *time.Time `json:"lastOkAt"`
	LastError   string     `json:"lastError"`
	LastErrorAt *time.Time `json:"lastErrorAt"`
	CreatedAt   time.Time  `json:"createdAt"`
}

// Protocol is torrent for Torznab and usenet for Newznab.
func (i Indexer) Protocol() string { return Protocol(i.Kind) }

// Input is what the owner sends to add a source.
type Input struct {
	Name       string
	Kind       string
	URL        string
	APIKey     string
	Categories []int
	Enabled    *bool // nil: enabled
	Priority   int   // 0: the default
}

// Patch changes a source. A nil field is left alone; an APIKey that is empty or
// still masked keeps the stored key.
type Patch struct {
	Name       *string
	Kind       *string
	URL        *string
	APIKey     *string
	Categories *[]int
	Enabled    *bool
	Priority   *int
}

// Matcher is told about feed items the service has not seen before. The search
// module implements it and grabs the ones a wanted copy needs.
type Matcher interface {
	MatchNew(ctx context.Context, items []Item)
}

// Options configure the service.
type Options struct {
	Logger *slog.Logger
	Now    func() time.Time
}

// Service manages sources and talks to them.
type Service struct {
	store *Store
	box   *secrets.Box
	hc    *outbound.Client
	log   *slog.Logger
	now   func() time.Time

	matcher atomic.Pointer[matcherBox]
}

type matcherBox struct{ m Matcher }

// New builds the service on the main database.
func New(store *Store, box *secrets.Box, hc *outbound.Client, o Options) *Service {
	if o.Logger == nil {
		o.Logger = slog.New(slog.NewTextHandler(io.Discard, nil))
	}
	if o.Now == nil {
		o.Now = time.Now
	}
	return &Service{store: store, box: box, hc: hc, log: o.Logger, now: o.Now}
}

// SetMatcher wires in whoever wants to hear about new feed items.
func (s *Service) SetMatcher(m Matcher) {
	if m == nil {
		s.matcher.Store(nil)
		return
	}
	s.matcher.Store(&matcherBox{m: m})
}

// ---- masking and cleaning ----------------------------------------------------

// MaskKey shows an API key the way settings do: only the end of a long key.
func MaskKey(key string) string { return settings.Mask(key) }

// redact removes a secret from text that might echo it back.
func redact(text, secret string) string {
	if len(secret) < 4 {
		return text
	}
	return strings.ReplaceAll(text, secret, settings.MaskPrefix)
}

func cleanName(name string) (string, error) {
	name = strings.TrimSpace(name)
	if name == "" {
		return "", invalid("Give the source a name.")
	}
	if utf8.RuneCountInString(name) > maxNameRunes {
		return "", invalid("That name is too long.")
	}
	return name, nil
}

func cleanKind(kind string) (string, error) {
	kind = strings.ToLower(strings.TrimSpace(kind))
	if kind != KindTorznab && kind != KindNewznab {
		return "", invalid("kind must be torznab or newznab.")
	}
	return kind, nil
}

// cleanURL checks a feed address and splits an API key out of it: pasting
// "https://host/api?apikey=abc" works, and the query is dropped so no key is
// ever stored in the clear. It returns the address and any key found.
func cleanURL(raw string) (clean, keyInURL string, err error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return "", "", invalid("Give the address of the feed.")
	}
	if len(raw) > maxURLLen {
		return "", "", invalid("That address is too long.")
	}
	u, perr := url.Parse(raw)
	if perr != nil || u.Host == "" || (u.Scheme != "http" && u.Scheme != "https") {
		return "", "", invalid("The address must start with http:// or https://.")
	}
	q := u.Query()
	for _, name := range []string{"apikey", "api_key", "key"} {
		if v := q.Get(name); v != "" && keyInURL == "" {
			keyInURL = v
		}
	}
	if u.User != nil {
		return "", "", invalid("Do not put a user name or password in the address.")
	}
	u.RawQuery, u.Fragment = "", ""
	return u.String(), keyInURL, nil
}

func cleanKey(key string) (string, error) {
	key = strings.TrimSpace(key)
	if len(key) > maxKeyLen {
		return "", invalid("That API key is too long.")
	}
	for _, r := range key {
		if r < 0x20 || r == 0x7f || r == ' ' {
			return "", invalid("An API key cannot contain spaces or control characters.")
		}
	}
	return key, nil
}

func cleanCategories(cats []int) ([]int, error) {
	if len(cats) > maxCats {
		return nil, invalid("Too many categories.")
	}
	seen := map[int]bool{}
	out := []int{}
	for _, c := range cats {
		if c < 100 || c > 999999 {
			return nil, invalid(fmt.Sprintf("%d is not a category number (they look like 2000 or 5040).", c))
		}
		if !seen[c] {
			seen[c] = true
			out = append(out, c)
		}
	}
	return out, nil
}

func cleanPriority(p int) (int, error) {
	if p == 0 {
		return DefaultPriority, nil
	}
	if p < MinPriority || p > MaxPriority {
		return 0, invalid(fmt.Sprintf("Priority is a number from %d to %d (lower is asked first).", MinPriority, MaxPriority))
	}
	return p, nil
}

// ---- CRUD --------------------------------------------------------------------

func (s *Service) public(r record) Indexer {
	key, _ := s.open(r.KeySealed)
	return Indexer{
		ID: r.ID, Name: r.Name, Kind: r.Kind, URL: r.URL, APIKey: MaskKey(key),
		Categories: r.Categories, Enabled: r.Enabled, Priority: r.Priority, Caps: r.Caps,
		LastOKAt: r.LastOKAt, LastError: r.LastError, LastErrorAt: r.LastErrorAt, CreatedAt: r.CreatedAt,
	}
}

func (s *Service) open(sealed string) (string, error) {
	if sealed == "" {
		return "", nil
	}
	return s.box.Open(sealed)
}

// List returns every source, best priority first.
func (s *Service) List(ctx context.Context) ([]Indexer, error) {
	recs, err := s.store.List(ctx)
	if err != nil {
		return nil, err
	}
	out := make([]Indexer, len(recs))
	for i, r := range recs {
		out[i] = s.public(r)
	}
	return out, nil
}

// Enabled returns the enabled sources, best priority first.
func (s *Service) Enabled(ctx context.Context) ([]Indexer, error) {
	all, err := s.List(ctx)
	if err != nil {
		return nil, err
	}
	out := all[:0]
	for _, i := range all {
		if i.Enabled {
			out = append(out, i)
		}
	}
	return out, nil
}

// Get returns one source.
func (s *Service) Get(ctx context.Context, id int64) (Indexer, error) {
	r, err := s.store.Get(ctx, id)
	if err != nil {
		return Indexer{}, err
	}
	return s.public(r), nil
}

// Create adds a source. It does not call it; Test does.
func (s *Service) Create(ctx context.Context, in Input) (Indexer, error) {
	name, err := cleanName(in.Name)
	if err != nil {
		return Indexer{}, err
	}
	kind, err := cleanKind(in.Kind)
	if err != nil {
		return Indexer{}, err
	}
	addr, keyInURL, err := cleanURL(in.URL)
	if err != nil {
		return Indexer{}, err
	}
	key, err := cleanKey(in.APIKey)
	if err != nil {
		return Indexer{}, err
	}
	if key == "" || strings.HasPrefix(key, settings.MaskPrefix) {
		key = keyInURL
	}
	cats, err := cleanCategories(in.Categories)
	if err != nil {
		return Indexer{}, err
	}
	prio, err := cleanPriority(in.Priority)
	if err != nil {
		return Indexer{}, err
	}
	enabled := in.Enabled == nil || *in.Enabled
	sealed := ""
	if key != "" {
		if sealed, err = s.box.Seal(key); err != nil {
			return Indexer{}, err
		}
	}
	id, err := s.store.Insert(ctx, record{
		Name: name, Kind: kind, URL: addr, KeySealed: sealed, Categories: cats,
		Enabled: enabled, Priority: prio, CreatedAt: s.now(),
	})
	if err != nil {
		return Indexer{}, err
	}
	return s.Get(ctx, id)
}

// Update changes a source. Changing the address or the key forgets what was
// learned about the old one (capabilities, last error).
func (s *Service) Update(ctx context.Context, id int64, p Patch) (Indexer, error) {
	r, err := s.store.Get(ctx, id)
	if err != nil {
		return Indexer{}, err
	}
	reset := false
	if p.Name != nil {
		if r.Name, err = cleanName(*p.Name); err != nil {
			return Indexer{}, err
		}
	}
	if p.Kind != nil {
		kind, err := cleanKind(*p.Kind)
		if err != nil {
			return Indexer{}, err
		}
		if kind != r.Kind {
			r.Kind, reset = kind, true
		}
	}
	keyInURL := ""
	if p.URL != nil {
		var addr string
		if addr, keyInURL, err = cleanURL(*p.URL); err != nil {
			return Indexer{}, err
		}
		if addr != r.URL {
			r.URL, reset = addr, true
		}
	}
	newKey := ""
	switch {
	case p.APIKey != nil:
		key, err := cleanKey(*p.APIKey)
		if err != nil {
			return Indexer{}, err
		}
		switch {
		case strings.HasPrefix(key, settings.MaskPrefix):
			// The masked text came back unchanged: keep the stored key.
		case key == "":
			r.KeySealed, reset = "", true
		default:
			newKey = key
		}
	case keyInURL != "":
		newKey = keyInURL
	}
	if newKey != "" {
		if r.KeySealed, err = s.box.Seal(newKey); err != nil {
			return Indexer{}, err
		}
		reset = true
	}
	if p.Categories != nil {
		if r.Categories, err = cleanCategories(*p.Categories); err != nil {
			return Indexer{}, err
		}
	}
	if p.Enabled != nil {
		r.Enabled = *p.Enabled
	}
	if p.Priority != nil {
		if *p.Priority < MinPriority || *p.Priority > MaxPriority {
			return Indexer{}, invalid(fmt.Sprintf("Priority is a number from %d to %d (lower is asked first).", MinPriority, MaxPriority))
		}
		r.Priority = *p.Priority
	}
	if reset {
		r.Caps, r.LastError, r.LastErrorAt, r.LastOKAt = nil, "", nil, nil
	}
	if err := s.store.Update(ctx, r); err != nil {
		return Indexer{}, err
	}
	return s.Get(ctx, id)
}

// Delete removes a source and what its feed showed.
func (s *Service) Delete(ctx context.Context, id int64) error { return s.store.Delete(ctx, id) }
