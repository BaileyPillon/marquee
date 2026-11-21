package web

import (
	"context"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/BaileyPillon/marquee/internal/accounts"
	"github.com/BaileyPillon/marquee/internal/core/db"
	"github.com/BaileyPillon/marquee/internal/core/events"
	"github.com/BaileyPillon/marquee/internal/core/outbound"
	"github.com/BaileyPillon/marquee/internal/core/secrets"
	"github.com/BaileyPillon/marquee/internal/decide"
	"github.com/BaileyPillon/marquee/internal/downloads"
	"github.com/BaileyPillon/marquee/internal/grab"
	"github.com/BaileyPillon/marquee/internal/history"
	"github.com/BaileyPillon/marquee/internal/importer"
	"github.com/BaileyPillon/marquee/internal/library"
	"github.com/BaileyPillon/marquee/internal/mediaservers"
	"github.com/BaileyPillon/marquee/internal/metadata"
	"github.com/BaileyPillon/marquee/internal/requests"
	"github.com/BaileyPillon/marquee/internal/settings"
	"github.com/BaileyPillon/marquee/internal/titles"
)

// ---- test rig --------------------------------------------------------------

// impMovies knows one film.
type impMovies struct{}

func (impMovies) SearchMovies(context.Context, string, int) ([]metadata.SearchHit, error) {
	return nil, nil
}

func (impMovies) Movie(_ context.Context, id string) (*metadata.MovieInfo, error) {
	if id != "10331" {
		return nil, metadata.ErrNotFound
	}
	return &metadata.MovieInfo{TMDBID: 10331, IMDbID: "tt0063350", Title: "Night of the Living Dead", Year: 1968,
		Poster: metadata.Image{Ref: "tmdb/w500/notld.jpg"}}, nil
}

// impClient is a download client that takes anything.
type impClient struct {
	mu    sync.Mutex
	items map[string]*downloads.Item
}

func (c *impClient) Test(context.Context) (string, error) { return "fake", nil }
func (c *impClient) Add(_ context.Context, in downloads.AddInput) (string, error) {
	h, err := downloads.MagnetInfoHash(in.MagnetURL)
	return h, err
}
func (c *impClient) List(context.Context, string) ([]downloads.Item, error) { return nil, nil }
func (c *impClient) Remove(context.Context, string, bool) error             { return nil }
func (c *impClient) Pause(context.Context, string) error                    { return nil }
func (c *impClient) Resume(context.Context, string) error                   { return nil }

type impApp struct {
	*app
	cat      *titles.Service
	lib      *library.Service
	req      *requests.Service
	dl       *downloads.Service
	hist     *history.Service
	srvs     *mediaservers.Service
	imp      *importer.Service
	moviesTo string // the library folder
	dlDir    string
	clientID int64
}

const notldRel = "Night.of.the.Living.Dead.1968.1080p.BluRay.x264-GRP"

func newImpApp(t *testing.T, mods ...func(*Deps)) *impApp {
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
	hc := outbound.New("test", outbound.Options{Logger: log, HostRPS: 100000, HostBurst: 100000, MaxRetries: -1})

	a := &impApp{dlDir: filepath.Join(dir, "downloads"), moviesTo: filepath.Join(dir, "movies")}
	for _, p := range []string{a.dlDir, a.moviesTo} {
		if err := os.MkdirAll(p, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	a.cat = titles.NewService(mainDB, titles.Providers{Movies: impMovies{}, Shows: rqShows{}, Artists: rqArtists{}}, titles.Options{Logger: log})
	a.lib = library.NewService(mainDB, library.Options{Logger: log, Hub: hub})
	t.Cleanup(a.lib.Close)
	if _, err := a.lib.AddFolder(ctx, "movie", a.moviesTo, "Movies"); err != nil {
		t.Fatal(err)
	}
	a.req = requests.NewService(mainDB, a.cat, a.lib, cfg, requests.Options{Logger: log, Hub: hub})
	t.Cleanup(a.req.Close)
	a.hist = history.New(mainDB, history.Options{Logger: log})
	a.req.SetRecorder(a.hist)
	a.dl = downloads.NewService(mainDB, downloads.Options{
		Logger: log, Hub: hub, HTTP: hc, Box: box,
		Adapters: func(downloads.AdapterConfig) (downloads.Adapter, error) { return &impClient{}, nil },
	})
	a.srvs = mediaservers.New(mainDB, mediaservers.Options{Logger: log, HTTP: hc, Box: box})
	dec := decide.New(decide.NewStore(mainDB), decide.Options{})
	a.imp = importer.New(mainDB, importer.Deps{
		Downloads: a.dl, Copies: a.req, Titles: a.cat, Library: a.lib, Profiles: dec, Blocklist: dec, Requests: a.req,
		Servers: a.srvs, History: a.hist,
	}, importer.Options{Logger: log, Hub: hub})
	t.Cleanup(a.imp.Close)

	deps := Deps{
		Version: "9.9.9-test", Log: log, MainDB: mainDB, EventsDB: eventsDB,
		Accounts: accounts.NewService(mainDB, accounts.Options{Logger: log, PasswordParams: lightParams}),
		Settings: cfg, Hub: hub, Titles: a.cat, Metadata: nil, Library: a.lib, Requests: a.req, Downloads: a.dl,
		Importer: a.imp, MediaServers: a.srvs, History: a.hist, UI: testUI(),
	}
	for _, m := range mods {
		m(&deps)
	}
	s := New(deps)
	srv := httptest.NewServer(s.Handler())
	t.Cleanup(srv.Close)
	a.app = &app{t: t, srv: srv, mainDB: mainDB, settings: cfg, hub: hub}
	return a
}

// people returns an owner, an admin and a member, and a signed-out client.
func (a *impApp) people() (owner, admin, member, anon *client) {
	owner = a.owner()
	inv := owner.do("POST", "/api/v1/invites", map[string]string{"role": "admin"})
	admin = a.newClient()
	if r := admin.do("POST", "/api/v1/invites/accept", map[string]string{
		"token": inv.JSON(a.t)["token"].(string), "name": "Alice", "password": "admin password"}); r.Status != http.StatusCreated {
		a.t.Fatalf("accept = %d %s", r.Status, r.Body)
	}
	return owner, admin, a.member(owner, "Mia"), a.newClient()
}

// film adds the film, an approved request and a completed download whose files
// are in the client's folder. It returns the title id and the download id.
func (a *impApp) film(owner *client) (titleID, downloadID int64) {
	a.t.Helper()
	ctx := context.Background()
	d, _, err := a.cat.Add(ctx, titles.KindMovie, metadata.ProviderTMDB, "10331", titles.AddOptions{Monitored: true})
	if err != nil {
		a.t.Fatal(err)
	}
	u := userID(a.t, owner)
	if _, err := a.req.Ask(ctx, requests.Actor{UserID: u, Role: "owner"}, requests.AskInput{TitleID: d.ID, Quality: "hd"}); err != nil {
		a.t.Fatal(err)
	}
	cp, err := a.req.EnsureCopy(ctx, d.ID, "hd")
	if err != nil {
		a.t.Fatal(err)
	}
	c := owner.do("POST", "/api/v1/clients", map[string]any{"name": "qb", "kind": "qbittorrent", "url": "http://qb.test:8080"})
	if c.Status != http.StatusCreated {
		a.t.Fatalf("add client = %d %s", c.Status, c.Body)
	}
	id, err := a.dl.Grab(ctx, grab.Target{CopyID: cp.ID, TitleID: d.ID, Kind: "movie", Quality: "hd"},
		grab.Release{Title: notldRel, Protocol: grab.ProtocolTorrent, MagnetURL: "magnet:?xt=urn:btih:" + hashA + "&dn=x", Size: 10})
	if err != nil {
		a.t.Fatal(err)
	}
	content := filepath.Join(a.dlDir, notldRel)
	if err := os.MkdirAll(content, 0o755); err != nil {
		a.t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(content, notldRel+".mkv"), []byte("the film"), 0o644); err != nil {
		a.t.Fatal(err)
	}
	if _, err := a.mainDB.Exec(`UPDATE downloads SET content_path = ?, save_path = ? WHERE id = ?`, content, a.dlDir, id); err != nil {
		a.t.Fatal(err)
	}
	return d.ID, id
}

func (a *impApp) setState(id int64, state string) {
	a.t.Helper()
	if err := a.dl.SetState(context.Background(), id, state, ""); err != nil {
		a.t.Fatal(err)
	}
}

// fakeJelly is a Jellyfin that knows one token and a list of items.
func fakeJelly(t *testing.T, items string) *httptest.Server {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("X-Emby-Token") != "jelly-token-0123456789" {
			http.Error(w, "no", http.StatusUnauthorized)
			return
		}
		switch r.URL.Path {
		case "/System/Info":
			_, _ = io.WriteString(w, `{"Version":"12.4.0","Id":"sid"}`)
		case "/Library/VirtualFolders":
			_, _ = io.WriteString(w, `[{"Name":"Movies","ItemId":"l1","Locations":["/media/movies"]}]`)
		case "/Items":
			_, _ = io.WriteString(w, items)
		default:
			w.WriteHeader(http.StatusNoContent)
		}
	}))
	t.Cleanup(srv.Close)
	return srv
}

// ---- history ---------------------------------------------------------------

func TestTitleHistoryRoute(t *testing.T) {
	a := newImpApp(t)
	owner, _, member, anon := a.people()
	titleID, dlID := a.film(owner)
	path := "/api/v1/titles/" + itoa(titleID) + "/history"

	// The ask above already wrote "requested" and "approved".
	if err := a.hist.Record(context.Background(), history.Entry{TitleID: titleID, DownloadID: dlID, Event: history.Grabbed,
		Detail: map[string]any{"release": notldRel}}); err != nil {
		t.Fatal(err)
	}

	if r := anon.do("GET", path, nil); r.Status != 401 || r.errCode(t) != "unauthorized" {
		t.Errorf("anon = %d %s", r.Status, r.Body)
	}
	for who, c := range map[string]*client{"owner": owner, "member": member} {
		r := c.do("GET", path, nil)
		if r.Status != 200 {
			t.Fatalf("%s = %d %s", who, r.Status, r.Body)
		}
		items, _ := r.JSON(t)["items"].([]any)
		if len(items) != 3 {
			t.Fatalf("%s items = %s", who, r.Body)
		}
		first := items[0].(map[string]any)
		if first["event"] != "grabbed" || first["at"] == nil || first["detail"].(map[string]any)["release"] != notldRel {
			t.Errorf("newest item = %v", first)
		}
		if items[2].(map[string]any)["event"] != "requested" {
			t.Errorf("oldest item = %v", items[2])
		}
	}
	if r := owner.do("GET", path+"?limit=2", nil); len(r.JSON(t)["items"].([]any)) != 2 {
		t.Errorf("limit not applied: %s", r.Body)
	}
	for _, q := range []string{"?limit=0", "?limit=abc", "?limit=100000"} {
		if r := owner.do("GET", path+q, nil); r.Status != 400 {
			t.Errorf("%s = %d %s", q, r.Status, r.Body)
		}
	}
	if r := owner.do("GET", "/api/v1/titles/9999/history", nil); r.Status != 404 || r.errCode(t) != "not_found" {
		t.Errorf("unknown title = %d %s", r.Status, r.Body)
	}
	if r := owner.do("GET", "/api/v1/titles/abc/history", nil); r.Status != 404 {
		t.Errorf("bad id = %d", r.Status)
	}

	// An API key needs the "see" tick.
	see := a.keyFor(owner, "see")
	if r := a.newClient().send("GET", path, nil, bearer(see)...); r.Status != 200 {
		t.Errorf("see key = %d %s", r.Status, r.Body)
	}
	ask := a.keyFor(owner, "ask")
	if r := a.newClient().send("GET", path, nil, bearer(ask)...); r.Status != 403 || r.errCode(t) != "insufficient_scope" {
		t.Errorf("ask key = %d %s", r.Status, r.Body)
	}
}

func (a *impApp) keyFor(owner *client, scopes ...string) string {
	a.t.Helper()
	r := owner.do("POST", "/api/v1/apikeys", map[string]any{"name": "k", "scopes": scopes})
	if r.Status != http.StatusCreated {
		a.t.Fatalf("create key = %d %s", r.Status, r.Body)
	}
	return r.JSON(a.t)["secret"].(string)
}

// ---- media servers -----------------------------------------------------------

func TestMediaServerRoutes(t *testing.T) {
	a := newImpApp(t)
	owner, admin, member, anon := a.people()
	jelly := fakeJelly(t, `{"Items":[]}`)

	body := map[string]any{"name": "Jellyfin", "kind": "jellyfin", "url": jelly.URL + "/", "token": "jelly-token-0123456789",
		"pathMap": []map[string]string{{"from": `M:\MarqueeTest\media`, "to": "/media"}}}

	for _, rt := range []struct{ method, path string }{
		{"GET", "/api/v1/mediaservers"}, {"POST", "/api/v1/mediaservers"},
		{"PUT", "/api/v1/mediaservers/1"}, {"DELETE", "/api/v1/mediaservers/1"}, {"POST", "/api/v1/mediaservers/1/test"},
	} {
		if r := anon.do(rt.method, rt.path, body); r.Status != 401 {
			t.Errorf("anon %s %s = %d", rt.method, rt.path, r.Status)
		}
		for who, c := range map[string]*client{"member": member, "admin": admin} {
			if r := c.do(rt.method, rt.path, body); r.Status != 403 || r.errCode(t) != "forbidden" {
				t.Errorf("%s %s %s = %d %s (media servers hold tokens: owner only)", who, rt.method, rt.path, r.Status, r.Body)
			}
		}
	}

	// A CSRF-less request is refused before anything else.
	if r := owner.send("POST", "/api/v1/mediaservers", body); r.Status != 403 || r.errCode(t) != "csrf" {
		t.Errorf("no csrf header = %d %s", r.Status, r.Body)
	}

	r := owner.do("POST", "/api/v1/mediaservers", body)
	if r.Status != http.StatusCreated {
		t.Fatalf("add = %d %s", r.Status, r.Body)
	}
	srv := r.JSON(t)["server"].(map[string]any)
	id := int64(srv["id"].(float64))
	if srv["url"] != jelly.URL || srv["kind"] != "jellyfin" || srv["enabled"] != true {
		t.Errorf("server = %v", srv)
	}
	if tok, _ := srv["token"].(string); tok == "jelly-token-0123456789" || !strings.HasPrefix(tok, settings.MaskPrefix) {
		t.Errorf("the token must come back masked, got %q", tok)
	}
	if strings.Contains(string(r.Body), "jelly-token-0123456789") {
		t.Error("the token leaked into the response")
	}
	if pm := srv["pathMap"].([]any); len(pm) != 1 || pm[0].(map[string]any)["to"] != "/media" {
		t.Errorf("pathMap = %v", srv["pathMap"])
	}

	list := owner.do("GET", "/api/v1/mediaservers", nil)
	if got := list.JSON(t)["servers"].([]any); len(got) != 1 {
		t.Errorf("list = %s", list.Body)
	}

	// Test: ok with the right token.
	tr := owner.do("POST", "/api/v1/mediaservers/"+itoa(id)+"/test", nil)
	res := tr.JSON(t)
	libs, _ := res["libraries"].([]any)
	if tr.Status != 200 || res["ok"] != true || res["version"] != "12.4.0" || res["error"] != "" || len(libs) != 1 ||
		libs[0].(map[string]any)["name"] != "Movies" || libs[0].(map[string]any)["paths"].([]any)[0] != "/media/movies" {
		t.Errorf("test = %d %s", tr.Status, tr.Body)
	}

	// Update: a masked token keeps the stored one (the test still passes); a new name sticks.
	up := owner.do("PUT", "/api/v1/mediaservers/"+itoa(id), map[string]any{"name": "Living room", "token": srv["token"]})
	if up.Status != 200 || up.JSON(t)["server"].(map[string]any)["name"] != "Living room" {
		t.Fatalf("update = %d %s", up.Status, up.Body)
	}
	if tr := owner.do("POST", "/api/v1/mediaservers/"+itoa(id)+"/test", nil); tr.JSON(t)["ok"] != true {
		t.Errorf("the token should have been kept: %s", tr.Body)
	}
	// A new address without a new token is refused.
	if r := owner.do("PUT", "/api/v1/mediaservers/"+itoa(id), map[string]any{"url": "http://elsewhere.example:8096"}); r.Status != 400 || r.errCode(t) != "invalid_request" {
		t.Errorf("new address, old token = %d %s", r.Status, r.Body)
	}
	// With the wrong token the test says so (still a 200).
	owner.do("PUT", "/api/v1/mediaservers/"+itoa(id), map[string]any{"token": "wrong wrong wrong"})
	bad := owner.do("POST", "/api/v1/mediaservers/"+itoa(id)+"/test", nil)
	if bad.Status != 200 || bad.JSON(t)["ok"] != false || !strings.Contains(bad.JSON(t)["error"].(string), "refused the token") {
		t.Errorf("wrong token = %d %s", bad.Status, bad.Body)
	}

	// Validation and conflicts.
	for name, b := range map[string]map[string]any{
		"no kind":  {"name": "x", "url": "http://a:1"},
		"bad kind": {"name": "x", "kind": "kodi", "url": "http://a:1"},
		"bad url":  {"name": "x", "kind": "plex", "url": "ftp://a"},
		"no name":  {"kind": "plex", "url": "http://a:1"},
	} {
		if r := owner.do("POST", "/api/v1/mediaservers", b); r.Status != 400 || r.errCode(t) != "invalid_request" {
			t.Errorf("%s = %d %s", name, r.Status, r.Body)
		}
	}
	if r := owner.do("POST", "/api/v1/mediaservers", map[string]any{"name": "LIVING ROOM", "kind": "plex", "url": "http://a:1"}); r.Status != 409 || r.errCode(t) != "name_taken" {
		t.Errorf("duplicate name = %d %s", r.Status, r.Body)
	}
	if r := owner.send("POST", "/api/v1/mediaservers", nil, "X-Marquee", "1"); r.Status != 400 || r.errCode(t) != "invalid_json" {
		t.Errorf("no body = %d %s", r.Status, r.Body)
	}

	// Unknown ids.
	for _, rt := range []struct{ method, path string }{
		{"PUT", "/api/v1/mediaservers/999"}, {"DELETE", "/api/v1/mediaservers/999"}, {"POST", "/api/v1/mediaservers/999/test"},
		{"PUT", "/api/v1/mediaservers/abc"}, {"DELETE", "/api/v1/mediaservers/abc"},
	} {
		if r := owner.do(rt.method, rt.path, map[string]any{}); r.Status != 404 || r.errCode(t) != "not_found" {
			t.Errorf("%s %s = %d %s", rt.method, rt.path, r.Status, r.Body)
		}
	}

	if d := owner.do("DELETE", "/api/v1/mediaservers/"+itoa(id), nil); d.Status != http.StatusNoContent {
		t.Errorf("delete = %d %s", d.Status, d.Body)
	}
	if got := owner.do("GET", "/api/v1/mediaservers", nil).JSON(t)["servers"].([]any); len(got) != 0 {
		t.Errorf("after delete: %v", got)
	}

	// An API key needs the "settings" tick, and only the owner's key has it.
	set := a.keyFor(owner, "settings")
	if r := a.newClient().send("GET", "/api/v1/mediaservers", nil, bearer(set)...); r.Status != 200 {
		t.Errorf("settings key = %d %s", r.Status, r.Body)
	}
	see := a.keyFor(owner, "see")
	if r := a.newClient().send("GET", "/api/v1/mediaservers", nil, bearer(see)...); r.Status != 403 || r.errCode(t) != "insufficient_scope" {
		t.Errorf("see key = %d %s", r.Status, r.Body)
	}
}

// ---- open on the server ------------------------------------------------------------

func TestTitleOpenRoute(t *testing.T) {
	a := newImpApp(t)
	owner, _, member, anon := a.people()
	titleID, _ := a.film(owner)
	path := "/api/v1/titles/" + itoa(titleID) + "/open"

	if r := anon.do("GET", path, nil); r.Status != 401 {
		t.Errorf("anon = %d", r.Status)
	}
	// No media server set up.
	if r := member.do("GET", path, nil); r.Status != 404 || r.errCode(t) != "not_found_on_server" {
		t.Errorf("no servers = %d %s", r.Status, r.Body)
	}

	jelly := fakeJelly(t, `{"Items":[{"Id":"abc123","Name":"Night of the Living Dead","Type":"Movie","ProductionYear":1968,"ProviderIds":{"Imdb":"tt0063350"}}]}`)
	add := owner.do("POST", "/api/v1/mediaservers", map[string]any{"name": "Jellyfin", "kind": "jellyfin", "url": jelly.URL, "token": "jelly-token-0123456789"})
	if add.Status != http.StatusCreated {
		t.Fatalf("add = %d %s", add.Status, add.Body)
	}
	for who, c := range map[string]*client{"owner": owner, "member": member} {
		r := c.do("GET", path, nil)
		got := r.JSON(t)
		srv, _ := got["server"].(map[string]any)
		if r.Status != 200 || got["url"] != jelly.URL+"/web/#/details?id=abc123" || srv["name"] != "Jellyfin" || srv["kind"] != "jellyfin" || srv["id"] == nil {
			t.Errorf("%s = %d %s", who, r.Status, r.Body)
		}
	}
	if r := owner.do("GET", "/api/v1/titles/9999/open", nil); r.Status != 404 || r.errCode(t) != "not_found" {
		t.Errorf("unknown title = %d %s", r.Status, r.Body)
	}

	// A server that refuses the token is a bad gateway, not "not found".
	owner.do("PUT", "/api/v1/mediaservers/1", map[string]any{"token": "wrong wrong wrong"})
	if r := owner.do("GET", path, nil); r.Status != http.StatusBadGateway || r.errCode(t) != "media_server_unreachable" {
		t.Errorf("bad token = %d %s", r.Status, r.Body)
	}
}

// ---- manual import and status --------------------------------------------------------

func TestManualImportRouteAndStatus(t *testing.T) {
	a := newImpApp(t)
	owner, admin, member, anon := a.people()
	titleID, dlID := a.film(owner)
	a.setState(dlID, downloads.StateCompleted)
	path := "/api/v1/downloads/" + itoa(dlID) + "/import"

	if r := anon.do("POST", path, nil); r.Status != 401 {
		t.Errorf("anon = %d", r.Status)
	}
	if r := member.do("POST", path, nil); r.Status != 403 || r.errCode(t) != "forbidden" {
		t.Errorf("member = %d %s", r.Status, r.Body)
	}
	if r := member.do("GET", "/api/v1/import/status", nil); r.Status != 403 {
		t.Errorf("member status = %d", r.Status)
	}
	if r := owner.do("POST", "/api/v1/downloads/9999/import", nil); r.Status != 404 || r.errCode(t) != "not_found" {
		t.Errorf("unknown = %d %s", r.Status, r.Body)
	}
	if r := owner.do("POST", "/api/v1/downloads/abc/import", nil); r.Status != 404 {
		t.Errorf("bad id = %d", r.Status)
	}
	// The key needs the downloads tick.
	if r := a.newClient().send("POST", path, nil, bearer(a.keyFor(owner, "see"))...); r.Status != 403 || r.errCode(t) != "insufficient_scope" {
		t.Errorf("see key = %d %s", r.Status, r.Body)
	}

	r := admin.do("POST", path, nil)
	if r.Status != http.StatusAccepted {
		t.Fatalf("admin import = %d %s", r.Status, r.Body)
	}
	body := r.JSON(t)
	dl, _ := body["download"].(map[string]any)
	if body["queued"] != true || dl == nil || int64(dl["id"].(float64)) != dlID {
		t.Errorf("body = %s", r.Body)
	}
	a.imp.Wait()

	dest := filepath.Join(a.moviesTo, "Night of the Living Dead (1968)", "Night of the Living Dead (1968) Bluray-1080p.mkv")
	if b, err := os.ReadFile(dest); err != nil || string(b) != "the film" {
		t.Fatalf("the film was not imported: %v", err)
	}
	if st := owner.do("GET", "/api/v1/downloads?state=all", nil).JSON(t)["downloads"].([]any)[0].(map[string]any)["state"]; st != "imported" {
		t.Errorf("download state = %v", st)
	}

	// An imported download cannot be imported again.
	if r := owner.do("POST", path, nil); r.Status != 409 || r.errCode(t) != "invalid_state" {
		t.Errorf("second import = %d %s", r.Status, r.Body)
	}

	st := owner.do("GET", "/api/v1/import/status", nil)
	got := st.JSON(t)
	recs, _ := got["imports"].([]any)
	if st.Status != 200 || len(recs) != 1 || got["running"] == nil {
		t.Fatalf("status = %d %s", st.Status, st.Body)
	}
	rec := recs[0].(map[string]any)
	title, _ := rec["title"].(map[string]any)
	if rec["outcome"] != "imported" || rec["filesImported"].(float64) != 1 || rec["manual"] != true || rec["releaseTitle"] != notldRel ||
		title["name"] != "Night of the Living Dead" || rec["startedAt"] == nil || rec["finishedAt"] == nil || rec["downloadId"].(float64) != float64(dlID) {
		t.Errorf("record = %v", rec)
	}
	if hs := owner.do("GET", "/api/v1/titles/"+itoa(titleID)+"/history", nil).JSON(t)["items"].([]any); hs[0].(map[string]any)["event"] != "imported" {
		t.Errorf("history = %v", hs)
	}
}

func TestManualImportOfAFailedDownload(t *testing.T) {
	a := newImpApp(t)
	owner, _, _, _ := a.people()
	_, dlID := a.film(owner)
	// The client says the files are somewhere Marquee cannot see: the import fails (and says what to do).
	if _, err := a.mainDB.Exec(`UPDATE downloads SET content_path = ? WHERE id = ?`, filepath.Join(a.dlDir, "missing"), dlID); err != nil {
		t.Fatal(err)
	}
	a.setState(dlID, downloads.StateCompleted)
	if r := owner.do("POST", "/api/v1/downloads/"+itoa(dlID)+"/import", nil); r.Status != http.StatusAccepted {
		t.Fatalf("import = %d %s", r.Status, r.Body)
	}
	a.imp.Wait()
	recs := owner.do("GET", "/api/v1/import/status", nil).JSON(t)["imports"].([]any)
	if len(recs) != 1 || recs[0].(map[string]any)["outcome"] != "failed" || !strings.Contains(recs[0].(map[string]any)["reason"].(string), "path rule") {
		t.Fatalf("status = %v", recs)
	}
	dls := owner.do("GET", "/api/v1/downloads?state=all", nil).JSON(t)["downloads"].([]any)
	d := dls[0].(map[string]any)
	if d["state"] != "failed" || !strings.HasPrefix(d["error"].(string), "Import failed: ") {
		t.Errorf("download = %v", d)
	}

	// Fix the path, and import it again by hand.
	if _, err := a.mainDB.Exec(`UPDATE downloads SET content_path = ? WHERE id = ?`, filepath.Join(a.dlDir, notldRel), dlID); err != nil {
		t.Fatal(err)
	}
	if r := owner.do("POST", "/api/v1/downloads/"+itoa(dlID)+"/import", nil); r.Status != http.StatusAccepted {
		t.Fatalf("retry = %d %s", r.Status, r.Body)
	}
	a.imp.Wait()
	if d := owner.do("GET", "/api/v1/downloads?state=all", nil).JSON(t)["downloads"].([]any)[0].(map[string]any); d["state"] != "imported" {
		t.Errorf("download = %v", d)
	}
}

func TestClientPathMapOverHTTP(t *testing.T) {
	a := newImpApp(t)
	owner, _, _, _ := a.people()
	r := owner.do("POST", "/api/v1/clients", map[string]any{"name": "qb", "kind": "qbittorrent", "url": "http://qb.test:8080",
		"pathMap": []map[string]string{{"from": "/downloads", "to": `D:\dl`}}})
	if r.Status != http.StatusCreated {
		t.Fatalf("add = %d %s", r.Status, r.Body)
	}
	c := r.JSON(t)["client"].(map[string]any)
	pm, _ := c["pathMap"].([]any)
	if len(pm) != 1 || pm[0].(map[string]any)["from"] != "/downloads" || pm[0].(map[string]any)["to"] != `D:\dl` {
		t.Errorf("pathMap = %v", c["pathMap"])
	}
	id := itoa(int64(c["id"].(float64)))
	// Left out, the rules stay.
	up := owner.do("PUT", "/api/v1/clients/"+id, map[string]any{"category": "films"})
	if len(up.JSON(t)["client"].(map[string]any)["pathMap"].([]any)) != 1 {
		t.Errorf("rules lost: %s", up.Body)
	}
	if bad := owner.do("PUT", "/api/v1/clients/"+id, map[string]any{"pathMap": []map[string]string{{"from": "/x"}}}); bad.Status != 400 || bad.errCode(t) != "invalid_request" {
		t.Errorf("a half rule = %d %s", bad.Status, bad.Body)
	}
	cleared := owner.do("PUT", "/api/v1/clients/"+id, map[string]any{"pathMap": []map[string]string{}})
	if got := cleared.JSON(t)["client"].(map[string]any)["pathMap"].([]any); len(got) != 0 {
		t.Errorf("rules not cleared: %s", cleared.Body)
	}
}

func TestImportRoutesAreAbsentWithoutTheServices(t *testing.T) {
	a := newImpApp(t, func(d *Deps) { d.Importer, d.MediaServers, d.History = nil, nil, nil })
	owner := a.owner()
	for _, rt := range []struct{ method, path string }{
		{"GET", "/api/v1/mediaservers"}, {"POST", "/api/v1/mediaservers"}, {"GET", "/api/v1/titles/1/history"},
		{"GET", "/api/v1/titles/1/open"}, {"POST", "/api/v1/downloads/1/import"}, {"GET", "/api/v1/import/status"},
	} {
		if r := owner.do(rt.method, rt.path, map[string]any{}); r.Status != 404 || r.errCode(t) != "not_found" {
			t.Errorf("%s %s = %d %s", rt.method, rt.path, r.Status, r.Body)
		}
	}
}
