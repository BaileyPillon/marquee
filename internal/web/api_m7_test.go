package web

import (
	"context"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/BaileyPillon/marquee/internal/accounts"
	"github.com/BaileyPillon/marquee/internal/alerts"
	"github.com/BaileyPillon/marquee/internal/calendar"
	"github.com/BaileyPillon/marquee/internal/core/db"
	"github.com/BaileyPillon/marquee/internal/core/events"
	"github.com/BaileyPillon/marquee/internal/core/outbound"
	"github.com/BaileyPillon/marquee/internal/core/secrets"
	"github.com/BaileyPillon/marquee/internal/library"
	"github.com/BaileyPillon/marquee/internal/settings"
	"github.com/BaileyPillon/marquee/internal/titles"
	"github.com/BaileyPillon/marquee/internal/watchlists"
)

// ---- fakes for the Plex side ------------------------------------------------------

// fakePlexAPI is Plex as the watchlists module sees it.
type fakePlexAPI struct {
	mu       sync.Mutex
	nextPin  int64
	approved map[int64]bool
	owner    watchlists.PlexUser
	items    []watchlists.Item
	friends  []watchlists.PlexUser
	friendWL map[string][]watchlists.Item
	guids    map[string][]string
}

func (f *fakePlexAPI) CreatePIN(context.Context, watchlists.Auth) (watchlists.Pin, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.nextPin++
	return watchlists.Pin{ID: 700 + f.nextPin, Code: "WXYZ"}, nil
}

func (f *fakePlexAPI) CheckPIN(_ context.Context, _ watchlists.Auth, id int64, _ string) (string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	switch {
	case id > 700+f.nextPin || id <= 700:
		return "", watchlists.ErrPinExpired
	case f.approved[id]:
		return "tok-owner", nil
	}
	return "", nil
}

func (f *fakePlexAPI) Account(context.Context, watchlists.Auth) (watchlists.PlexUser, error) {
	return f.owner, nil
}

func (f *fakePlexAPI) OwnerWatchlist(context.Context, watchlists.Auth) ([]watchlists.Item, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]watchlists.Item(nil), f.items...), nil
}

func (f *fakePlexAPI) Friends(context.Context, watchlists.Auth) ([]watchlists.PlexUser, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]watchlists.PlexUser(nil), f.friends...), nil
}

func (f *fakePlexAPI) FriendWatchlist(_ context.Context, _ watchlists.Auth, id string) ([]watchlists.Item, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]watchlists.Item(nil), f.friendWL[id]...), nil
}

func (f *fakePlexAPI) ItemGuids(_ context.Context, _ watchlists.Auth, key string) ([]string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.guids[key], nil
}

// fakeAsker records the asks and numbers them.
type fakeAsker struct {
	mu    sync.Mutex
	calls []watchlists.AskInput
}

func (a *fakeAsker) Ask(_ context.Context, in watchlists.AskInput) (watchlists.AskResult, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.calls = append(a.calls, in)
	return watchlists.AskResult{RequestID: int64(len(a.calls))}, nil
}

// ---- the rig -------------------------------------------------------------------

// m7App is a whole server with the catalog, the library, the calendar, alert
// channels (sending to a fake server) and Plex watchlists (on a fake Plex).
type m7App struct {
	*app
	plex   *fakePlexAPI
	asker  *fakeAsker
	hook   *httptest.Server
	hookMu sync.Mutex
	hooks  []string // paths the fake alert target saw
	alerts *alerts.Service
	lib    *library.Service
	cat    *titles.Service
}

func newM7App(t *testing.T) *m7App {
	t.Helper()
	ctx := context.Background()
	dir := t.TempDir()
	mainDB, err := db.Open(filepath.Join(dir, db.MainFile))
	if err != nil {
		t.Fatal(err)
	}
	eventsDB, err := db.Open(filepath.Join(dir, db.EventsFile))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { mainDB.Close(); eventsDB.Close() })
	if _, err := db.Migrate(ctx, mainDB, db.SetMain); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Migrate(ctx, eventsDB, db.SetEvents); err != nil {
		t.Fatal(err)
	}
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	box, err := secrets.LoadOrCreate(filepath.Join(dir, "marquee.key"), log)
	if err != nil {
		t.Fatal(err)
	}
	hub := events.NewHub(log, events.WithHeartbeat(50*time.Millisecond))
	t.Cleanup(hub.Close)
	cfg := settings.New(mainDB, box)
	cat := titles.NewService(mainDB, titles.Providers{Movies: rqMovies{}, Shows: rqShows{}, Artists: rqArtists{}}, titles.Options{Logger: log})
	t.Cleanup(cat.Close)
	lib := library.NewService(mainDB, library.Options{Logger: log, Hub: hub})
	t.Cleanup(lib.Close)

	m := &m7App{
		plex: &fakePlexAPI{
			owner:    watchlists.PlexUser{ID: "1001", Username: "bailey-plex"},
			approved: map[int64]bool{}, friendWL: map[string][]watchlists.Item{}, guids: map[string][]string{},
		},
		asker: &fakeAsker{},
		lib:   lib,
		cat:   cat,
	}
	m.hook = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		m.hookMu.Lock()
		m.hooks = append(m.hooks, r.URL.Path)
		m.hookMu.Unlock()
		if r.URL.Path == "/broken" {
			http.Error(w, `{"message": "nope"}`, http.StatusInternalServerError)
			return
		}
		w.WriteHeader(http.StatusNoContent)
	}))
	t.Cleanup(m.hook.Close)

	hc := outbound.New("9.9.9-test", outbound.Options{Logger: log, MaxRetries: -1})
	acc := accounts.NewService(mainDB, accounts.Options{Logger: log, PasswordParams: lightParams})
	wl := watchlists.NewService(mainDB, cfg, box, watchlists.Options{
		Logger: log, Plex: m.plex, Asker: m.asker, Members: webMembers{acc},
	})
	m.alerts = alerts.NewService(mainDB, box, hc, alerts.Options{Logger: log, Hub: hub})
	t.Cleanup(m.alerts.Close)

	s := New(Deps{
		Version: "9.9.9-test", Log: log, MainDB: mainDB, EventsDB: eventsDB,
		Accounts: acc, Settings: cfg, Hub: hub, Titles: cat, Library: lib,
		Watchlists: wl, Calendar: calendar.NewService(mainDB, calendar.Options{}), Alerts: m.alerts,
		UI: testUI(),
	})
	srv := httptest.NewServer(s.Handler())
	t.Cleanup(srv.Close)
	m.app = &app{t: t, srv: srv, mainDB: mainDB, settings: cfg, hub: hub}
	return m
}

// webMembers is the watchlists.Members the program wires in.
type webMembers struct{ a *accounts.Service }

func (m webMembers) CreateMember(ctx context.Context, name string) (int64, error) {
	u, err := m.a.CreateMember(ctx, name)
	if err == accounts.ErrNameTaken {
		return 0, watchlists.ErrNameTaken
	}
	return u.ID, err
}

// admin creates an admin through an invite and returns a signed-in client.
func (a *m7App) admin(owner *client, name string) *client {
	a.t.Helper()
	inv := owner.do("POST", "/api/v1/invites", map[string]string{"role": "admin"})
	if inv.Status != http.StatusCreated {
		a.t.Fatalf("invite = %d %s", inv.Status, inv.Body)
	}
	c := a.newClient()
	r := c.do("POST", "/api/v1/invites/accept", map[string]string{
		"token": inv.JSON(a.t)["token"].(string), "name": name, "password": "admin password"})
	if r.Status != http.StatusCreated {
		a.t.Fatalf("accept = %d %s", r.Status, r.Body)
	}
	return c
}

// key makes an API key with the given scopes for whoever c is signed in as.
func (a *m7App) key(c *client, scopes ...string) keyClient {
	a.t.Helper()
	r := c.do("POST", "/api/v1/apikeys", map[string]any{"name": "test key", "scopes": scopes})
	if r.Status != http.StatusCreated {
		a.t.Fatalf("apikey = %d %s", r.Status, r.Body)
	}
	return keyClient{c: a.newClient(), secret: r.JSON(a.t)["secret"].(string)}
}
