package watchlists

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/BaileyPillon/marquee/internal/accounts"
	"github.com/BaileyPillon/marquee/internal/core/db"
	"github.com/BaileyPillon/marquee/internal/core/outbound"
	"github.com/BaileyPillon/marquee/internal/core/secrets"
	"github.com/BaileyPillon/marquee/internal/metadata"
	"github.com/BaileyPillon/marquee/internal/settings"
)

func bg() context.Context { return context.Background() }

func quiet() *slog.Logger { return slog.New(slog.NewTextHandler(io.Discard, nil)) }

var light = &accounts.PasswordParams{Time: 1, Memory: 64, Threads: 1, KeyLen: 32, SaltLen: 16}

// ---- a fake Plex ------------------------------------------------------------

// fakePlex stands in for plex.tv, the discover service and the community API,
// all on one test server under three path prefixes.
type fakePlex struct {
	t   *testing.T
	srv *httptest.Server

	mu       sync.Mutex
	nextPin  int64
	pinCode  map[int64]string
	approved map[int64]string // pin id -> token once the owner agreed
	token    string           // the only token Plex accepts
	owner    PlexUser
	ownerWL  []Item            // the owner's watchlist, with ids
	friends  []PlexUser        // the owner's friends
	friendWL map[string][]Item // by friend id; items have no ids
	guids    map[string][]string
	calls    []string
	headers  map[string]http.Header // first request per "METHOD path"
	queries  map[string]string      // raw query of the last request per "METHOD path"
	fail     map[string]int         // path -> status to answer with
	hold     chan struct{}          // when set, the owner watchlist waits for it
	entered  chan struct{}
}

func newFakePlex(t *testing.T) *fakePlex {
	f := &fakePlex{
		t: t, pinCode: map[int64]string{}, approved: map[int64]string{}, token: "plex-token-owner-secret",
		owner:    PlexUser{ID: "1001", Username: "bailey-plex", DisplayName: "Bailey"},
		friendWL: map[string][]Item{}, guids: map[string][]string{},
		headers: map[string]http.Header{}, queries: map[string]string{}, fail: map[string]int{},
	}
	f.srv = httptest.NewServer(http.HandlerFunc(f.serve))
	t.Cleanup(f.srv.Close)
	return f
}

func (f *fakePlex) urls() PlexURLs {
	return PlexURLs{Plex: f.srv.URL + "/plextv", Discover: f.srv.URL + "/discover", Community: f.srv.URL + "/community"}
}

func (f *fakePlex) count(prefix string) int {
	f.mu.Lock()
	defer f.mu.Unlock()
	n := 0
	for _, c := range f.calls {
		if strings.HasPrefix(c, prefix) {
			n++
		}
	}
	return n
}

func (f *fakePlex) approve(pin int64) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.approved[pin] = f.token
}

func writeJSON(w http.ResponseWriter, v any) {
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(v)
}

func (f *fakePlex) serve(w http.ResponseWriter, r *http.Request) {
	f.mu.Lock()
	key := r.Method + " " + r.URL.Path
	f.calls = append(f.calls, key)
	if _, seen := f.headers[key]; !seen {
		f.headers[key] = r.Header.Clone()
	}
	f.queries[key] = r.URL.RawQuery
	if code := f.fail[r.URL.Path]; code != 0 {
		f.mu.Unlock()
		http.Error(w, "no", code)
		return
	}
	f.mu.Unlock()

	tokenOK := func() bool {
		tok := r.Header.Get("X-Plex-Token")
		if tok == "" {
			tok = r.URL.Query().Get("X-Plex-Token")
		}
		return tok == f.token
	}

	switch {
	case r.Method == http.MethodPost && r.URL.Path == "/plextv/api/v2/pins":
		f.mu.Lock()
		f.nextPin++
		id := f.nextPin + 4000
		f.pinCode[id] = "CODE" + strconv.FormatInt(id, 10)
		code := f.pinCode[id]
		f.mu.Unlock()
		writeJSON(w, map[string]any{"id": id, "code": code, "authToken": nil, "trusted": false})

	case r.Method == http.MethodGet && strings.HasPrefix(r.URL.Path, "/plextv/api/v2/pins/"):
		id, _ := strconv.ParseInt(strings.TrimPrefix(r.URL.Path, "/plextv/api/v2/pins/"), 10, 64)
		f.mu.Lock()
		code, known := f.pinCode[id]
		tok, ok := f.approved[id]
		f.mu.Unlock()
		if !known || (r.URL.Query().Get("code") != "" && r.URL.Query().Get("code") != code) {
			http.NotFound(w, r)
			return
		}
		var auth any
		if ok {
			auth = tok
		}
		writeJSON(w, map[string]any{"id": id, "code": code, "authToken": auth})

	case r.Method == http.MethodGet && r.URL.Path == "/plextv/api/v2/user":
		if !tokenOK() {
			http.Error(w, "bad token", http.StatusUnauthorized)
			return
		}
		id, _ := strconv.Atoi(f.owner.ID)
		writeJSON(w, map[string]any{"id": id, "username": f.owner.Username, "title": f.owner.DisplayName})

	case r.Method == http.MethodGet && r.URL.Path == "/discover/library/sections/watchlist/all":
		if !tokenOK() {
			http.Error(w, "bad token", http.StatusUnauthorized)
			return
		}
		if f.hold != nil {
			f.entered <- struct{}{}
			<-f.hold
		}
		q := r.URL.Query()
		start, _ := strconv.Atoi(q.Get("X-Plex-Container-Start"))
		size, _ := strconv.Atoi(q.Get("X-Plex-Container-Size"))
		if size <= 0 {
			size = 50
		}
		f.mu.Lock()
		all := f.ownerWL
		f.mu.Unlock()
		end := min(start+size, len(all))
		var meta []map[string]any
		if start < len(all) {
			for _, it := range all[start:end] {
				meta = append(meta, metaEntry(it, q.Get("includeGuids") == "1"))
			}
		}
		writeJSON(w, map[string]any{"MediaContainer": map[string]any{
			"offset": start, "size": len(meta), "totalSize": len(all), "Metadata": meta,
		}})

	case r.Method == http.MethodGet && strings.HasPrefix(r.URL.Path, "/discover/library/metadata/"):
		if !tokenOK() {
			http.Error(w, "bad token", http.StatusUnauthorized)
			return
		}
		key := strings.TrimPrefix(r.URL.Path, "/discover/library/metadata/")
		f.mu.Lock()
		g, ok := f.guids[key]
		f.mu.Unlock()
		if !ok {
			http.NotFound(w, r)
			return
		}
		writeJSON(w, map[string]any{"MediaContainer": map[string]any{"size": 1, "Metadata": []any{metaEntry(Item{Key: key, Guids: g}, true)}}})

	case r.Method == http.MethodPost && r.URL.Path == "/community":
		if !tokenOK() {
			http.Error(w, "bad token", http.StatusUnauthorized)
			return
		}
		f.graphQL(w, r)

	default:
		http.NotFound(w, r)
	}
}

func metaEntry(it Item, withGuids bool) map[string]any {
	m := map[string]any{"type": it.Type, "title": it.Title, "year": it.Year}
	if it.Key != "" {
		m["ratingKey"], m["key"] = it.Key, "/library/metadata/"+it.Key
	}
	if withGuids {
		var gs []map[string]string
		for _, g := range it.Guids {
			gs = append(gs, map[string]string{"id": g})
		}
		m["Guid"] = gs
	}
	return m
}

func (f *fakePlex) graphQL(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Query     string         `json:"query"`
		Variables map[string]any `json:"variables"`
	}
	if err := json.NewDecoder(r.Body).Decode(&in); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	switch {
	case strings.Contains(in.Query, "allFriendsV2"):
		var out []map[string]any
		f.mu.Lock()
		for _, fr := range f.friends {
			out = append(out, map[string]any{"user": map[string]any{"id": fr.ID, "username": fr.Username, "displayName": fr.DisplayName}})
		}
		f.mu.Unlock()
		writeJSON(w, map[string]any{"data": map[string]any{"allFriendsV2": out}})
	case strings.Contains(in.Query, "watchlist"):
		id, _ := in.Variables["user"].(string)
		first := 50
		if v, ok := in.Variables["first"].(float64); ok {
			first = int(v)
		}
		after, _ := in.Variables["after"].(string)
		f.mu.Lock()
		all := f.friendWL[id]
		f.mu.Unlock()
		start := 0
		if after != "" {
			start, _ = strconv.Atoi(strings.TrimPrefix(after, "c"))
		}
		end := min(start+first, len(all))
		var nodes []map[string]any
		for _, it := range all[min(start, len(all)):end] {
			nodes = append(nodes, map[string]any{"id": it.Key, "title": it.Title, "type": strings.ToUpper(it.Type), "publicationYear": it.Year})
		}
		writeJSON(w, map[string]any{"data": map[string]any{"user": map[string]any{"watchlist": map[string]any{
			"nodes":    nodes,
			"pageInfo": map[string]any{"hasNextPage": end < len(all), "endCursor": "c" + strconv.Itoa(end)},
		}}}})
	default:
		writeJSON(w, map[string]any{"errors": []map[string]string{{"message": "unknown query"}}})
	}
}

// ---- fakes for the rest of the program ----------------------------------------

type fakeAsker struct {
	mu     sync.Mutex
	calls  []AskInput
	nextID int64
	// errFor, when set, decides the answer for one ask.
	errFor func(AskInput) error
}

func (a *fakeAsker) Ask(_ context.Context, in AskInput) (AskResult, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.calls = append(a.calls, in)
	if a.errFor != nil {
		if err := a.errFor(in); err != nil {
			return AskResult{}, err
		}
	}
	a.nextID++
	return AskResult{RequestID: a.nextID + 100}, nil
}

func (a *fakeAsker) asked() []AskInput {
	a.mu.Lock()
	defer a.mu.Unlock()
	return append([]AskInput(nil), a.calls...)
}

// fakeShows knows shows by IMDb id and by name.
type fakeShows struct {
	mu     sync.Mutex
	byIMDb map[string]string // imdb -> tvmaze id
	byName map[string][]metadata.SearchHit
	err    error
	imdbQs []string
}

func (s *fakeShows) ShowByIMDb(_ context.Context, imdb string) (*metadata.SearchHit, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.imdbQs = append(s.imdbQs, imdb)
	if s.err != nil {
		return nil, s.err
	}
	if id, ok := s.byIMDb[imdb]; ok {
		return &metadata.SearchHit{Kind: "show", Provider: "tvmaze", ProviderID: id}, nil
	}
	return nil, metadata.ErrNotFound
}

func (s *fakeShows) SearchShows(_ context.Context, q string) ([]metadata.SearchHit, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.err != nil {
		return nil, s.err
	}
	return s.byName[q], nil
}

// accountsMembers is the Members the program wires in: accounts.CreateMember with
// the name-taken error put into the package's own words.
type accountsMembers struct{ a *accounts.Service }

func (m accountsMembers) CreateMember(ctx context.Context, name string) (int64, error) {
	u, err := m.a.CreateMember(ctx, name)
	if errors.Is(err, accounts.ErrNameTaken) {
		return 0, ErrNameTaken
	}
	return u.ID, err
}

// ---- the whole rig -----------------------------------------------------------

type env struct {
	t      *testing.T
	db     *sql.DB
	svc    *Service
	plex   *fakePlex
	asker  *fakeAsker
	shows  *fakeShows
	cfg    *settings.Service
	box    *secrets.Box
	acc    *accounts.Service
	owner  accounts.User
	now    time.Time
	hasKey bool
	keyMu  sync.Mutex
}

func newEnv(t *testing.T) *env {
	t.Helper()
	d, err := db.Open(filepath.Join(t.TempDir(), db.MainFile))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { d.Close() })
	if _, err := db.Migrate(bg(), d, db.SetMain); err != nil {
		t.Fatal(err)
	}
	box, err := secrets.LoadOrCreate(filepath.Join(t.TempDir(), "marquee.key"), quiet())
	if err != nil {
		t.Fatal(err)
	}
	acc := accounts.NewService(d, accounts.Options{Logger: quiet(), PasswordParams: light})
	login, err := acc.SetupOwner(bg(), "Bailey", "correct horse", accounts.ClientInfo{IP: "127.0.0.1"})
	if err != nil {
		t.Fatal(err)
	}
	e := &env{
		t: t, db: d, plex: newFakePlex(t), asker: &fakeAsker{}, cfg: settings.New(d, box), box: box, acc: acc,
		owner: login.User, hasKey: true, now: time.Date(2026, 11, 21, 12, 0, 0, 0, time.UTC),
		shows: &fakeShows{byIMDb: map[string]string{}, byName: map[string][]metadata.SearchHit{}},
	}
	hc := outbound.New("test", outbound.Options{Logger: quiet(), MaxRetries: -1, HostRPS: 100000, HostBurst: 100000})
	e.svc = NewService(d, e.cfg, box, Options{
		Logger: quiet(), Now: func() time.Time { return e.now },
		Plex:  NewPlexClient(hc, e.plex.urls()),
		Asker: e.asker, Members: accountsMembers{acc}, Shows: e.shows,
		HasTMDBKey: func(context.Context) bool { e.keyMu.Lock(); defer e.keyMu.Unlock(); return e.hasKey },
	})
	return e
}

func (e *env) setKey(has bool) { e.keyMu.Lock(); e.hasKey = has; e.keyMu.Unlock() }

// link goes through the PIN flow and leaves the owner's Plex account linked.
func (e *env) link() {
	e.t.Helper()
	l, err := e.svc.StartLink(bg())
	if err != nil {
		e.t.Fatal(err)
	}
	e.plex.approve(l.PinID)
	ok, err := e.svc.CheckLink(bg(), l.PinID, e.owner.ID)
	if err != nil || !ok {
		e.t.Fatalf("link = %v, %v", ok, err)
	}
}

func (e *env) member(name string) accounts.User {
	e.t.Helper()
	u, err := e.acc.CreateMember(bg(), name)
	if err != nil {
		e.t.Fatal(err)
	}
	return u
}

func (e *env) sync() Result {
	e.t.Helper()
	res, err := e.svc.Sync(bg())
	if err != nil {
		e.t.Fatalf("Sync: %v", err)
	}
	return res
}

func (e *env) setConfig(c Config) {
	e.t.Helper()
	if err := e.cfg.Set(bg(), SettingsSection, c); err != nil {
		e.t.Fatal(err)
	}
}

func (e *env) count(query string, args ...any) int {
	e.t.Helper()
	var n int
	if err := e.db.QueryRow(query, args...).Scan(&n); err != nil {
		e.t.Fatal(err)
	}
	return n
}

// movie and show make watchlist items.
func movie(key, title string, year int, tmdb string) Item {
	it := Item{Key: key, Title: title, Year: year, Type: "movie"}
	if tmdb != "" {
		it.Guids = append(it.Guids, "tmdb://"+tmdb)
	}
	it.Guids = append(it.Guids, "imdb://tt"+fmt.Sprintf("%07s", key))
	return it
}

func show(key, title string, year int, imdb string) Item {
	it := Item{Key: key, Title: title, Year: year, Type: "show", Guids: []string{"tvdb://9" + key}}
	if imdb != "" {
		it.Guids = append(it.Guids, "imdb://"+imdb)
	}
	return it
}
