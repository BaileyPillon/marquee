package web

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/url"
	"path/filepath"
	"strconv"
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
	"github.com/BaileyPillon/marquee/internal/grab"
	"github.com/BaileyPillon/marquee/internal/search"
	"github.com/BaileyPillon/marquee/internal/settings"
	"github.com/BaileyPillon/marquee/internal/sources"
	"github.com/BaileyPillon/marquee/internal/sources/sourcestest"
	"github.com/BaileyPillon/marquee/internal/titles"
)

const srcKey = "9f8e7d6c5b4a39281706f5e4d3c2b1a0"

// ---- rig -------------------------------------------------------------------------------

type srcCopies struct {
	mu     sync.Mutex
	copies []grab.Copy
}

func (c *srcCopies) Copy(_ context.Context, id int64) (grab.Copy, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	for _, x := range c.copies {
		if x.ID == id {
			return x, nil
		}
	}
	return grab.Copy{}, errors.New("no such copy")
}

func (c *srcCopies) Wanted(context.Context) ([]grab.Copy, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	return append([]grab.Copy(nil), c.copies...), nil
}

type srcGrabber struct {
	mu    sync.Mutex
	calls []srcGrabCall
}

type srcGrabCall struct {
	Target  grab.Target
	Release grab.Release
}

func (g *srcGrabber) Grab(_ context.Context, t grab.Target, r grab.Release) (int64, error) {
	g.mu.Lock()
	defer g.mu.Unlock()
	g.calls = append(g.calls, srcGrabCall{t, r})
	return int64(40 + len(g.calls)), nil
}

type srcApp struct {
	*app
	src     *sources.Service
	dec     *decide.Service
	search  *search.Service
	feed    *sourcestest.Server
	copies  *srcCopies
	grabber *srcGrabber
}

func newSrcApp(t *testing.T, withGrabber bool) *srcApp {
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
	hc := outbound.New("test", outbound.Options{Logger: log, Backoff: func(int) time.Duration { return 0 }, HostRPS: 1000, HostBurst: 1000})

	srcSvc := sources.New(sources.NewStore(mainDB), box, hc, sources.Options{Logger: log})
	decSvc := decide.New(decide.NewStore(mainDB), decide.Options{})
	catalog := titles.NewService(mainDB, titles.Providers{}, titles.Options{})
	searchSvc := search.New(mainDB, srcSvc, decSvc, catalog, search.Options{Logger: log, Pace: -1})
	a := &srcApp{src: srcSvc, dec: decSvc, search: searchSvc, copies: &srcCopies{}, grabber: &srcGrabber{}}
	searchSvc.SetCopySource(a.copies)
	if withGrabber {
		searchSvc.SetGrabber(a.grabber)
	}
	srcSvc.SetMatcher(searchSvc)
	a.feed = sourcestest.NewServer(t, srcKey)

	s := New(Deps{
		Version:  "9.9.9-test",
		Log:      log,
		MainDB:   mainDB,
		EventsDB: eventsDB,
		Accounts: accounts.NewService(mainDB, accounts.Options{Logger: log, PasswordParams: lightParams}),
		Settings: settings.New(mainDB, box),
		Hub:      hub,
		Sources:  srcSvc,
		Decide:   decSvc,
		Search:   searchSvc,
		UI:       testUI(),
	})
	srv := httptest.NewServer(s.Handler())
	t.Cleanup(srv.Close)
	a.app = &app{t: t, srv: srv, mainDB: mainDB, hub: hub}
	return a
}

// admin creates an admin through an invite and returns a signed-in client.
func (a *srcApp) admin(owner *client) *client {
	a.t.Helper()
	inv := owner.do("POST", "/api/v1/invites", map[string]string{"role": "admin"})
	if inv.Status != http.StatusCreated {
		a.t.Fatalf("invite = %d %s", inv.Status, inv.Body)
	}
	c := a.newClient()
	r := c.do("POST", "/api/v1/invites/accept", map[string]string{
		"token": inv.JSON(a.t)["token"].(string), "name": "Alice", "password": "admin password"})
	if r.Status != http.StatusCreated {
		a.t.Fatalf("accept = %d %s", r.Status, r.Body)
	}
	return c
}

// apiKey makes a key with the given scopes and returns its secret.
func (a *srcApp) apiKey(owner *client, scopes ...string) string {
	a.t.Helper()
	r := owner.do("POST", "/api/v1/apikeys", map[string]any{"name": "test key", "scopes": scopes})
	if r.Status != http.StatusCreated {
		a.t.Fatalf("create key = %d %s", r.Status, r.Body)
	}
	return r.JSON(a.t)["secret"].(string)
}

func (a *srcApp) addSource(owner *client, name string) map[string]any {
	a.t.Helper()
	r := owner.do("POST", "/api/v1/sources", map[string]any{"name": name, "kind": "torznab", "url": a.feed.Feed(), "apiKey": srcKey, "categories": []int{2000, 5000}})
	if r.Status != http.StatusCreated {
		a.t.Fatalf("create source = %d %s", r.Status, r.Body)
	}
	return r.JSON(a.t)["source"].(map[string]any)
}

// addMovie puts a movie in the catalog (rows only) and returns its id.
func (a *srcApp) addMovie(name string, year int, imdb string, tmdb int64) int64 {
	a.t.Helper()
	ts := db.TS(time.Now())
	res, err := a.mainDB.Exec(`INSERT INTO titles (kind, tmdb_id, imdb_id, name, sort_name, year, runtime_min, added_at, updated_at, refreshed_at)
		VALUES ('movie', ?, ?, ?, ?, ?, 120, ?, ?, ?)`, tmdb, imdb, name, name, year, ts, ts, ts)
	if err != nil {
		a.t.Fatal(err)
	}
	id, _ := res.LastInsertId()
	return id
}

func (a *srcApp) want(titleID int64, quality string) {
	a.copies.mu.Lock()
	defer a.copies.mu.Unlock()
	a.copies.copies = append(a.copies.copies, grab.Copy{ID: int64(len(a.copies.copies) + 1), TitleID: titleID, Kind: "movie", Quality: quality, Monitored: true})
}

func torrentItem(title string, size int64, seeders int) sourcestest.Item {
	it := sourcestest.Torrent(title, size, seeders)
	it.Age = 5 * time.Hour
	return it
}

func (a *srcApp) serveMetropolis() {
	best := torrentItem("Metropolis.1927.1080p.BluRay.x264-GRP", 8<<30, 25)
	best.GUID, best.Magnet = "metropolis-1080", true
	a.feed.Search = func(url.Values) []sourcestest.Item {
		return []sourcestest.Item{
			best,
			torrentItem("Metropolis.1927.720p.WEB-DL.x264-GRP", 2<<30, 9),
			torrentItem("Metropolis.1927.CAM.x264-GRP", 1<<30, 3),
			torrentItem("Metropolis.1927.2160p.BluRay.x265-GRP", 30<<30, 12),
		}
	}
}

func itoa64(n float64) string { return strconv.FormatInt(int64(n), 10) }

// ---- permissions ------------------------------------------------------------------------

func TestSourceRoutesPermissions(t *testing.T) {
	a := newSrcApp(t, true)
	owner := a.owner()
	admin := a.admin(owner)
	member := a.member(owner, "Mia")
	anon := a.newClient()
	titleID := a.addMovie("Metropolis", 1927, "tt0017136", 19)
	a.addSource(owner, "Alpha")
	ownerKey := a.apiKey(owner, "see")                  // too little for anything below
	adminScopeKey := a.apiKey(owner, "see", "admin")    // can search
	downloadsKey := a.apiKey(owner, "see", "downloads") // can grab
	settingsKey := a.apiKey(owner, "see", "settings")   // can manage sources
	a.serveMetropolis()

	type route struct {
		method, path string
		body         any
		who          string // owner | manage | download: who may call it
	}
	releases := "/api/v1/titles/" + strconv.FormatInt(titleID, 10) + "/releases"
	grabPath := "/api/v1/titles/" + strconv.FormatInt(titleID, 10) + "/grab"
	routes := []route{
		{"GET", "/api/v1/sources", nil, "owner"},
		{"POST", "/api/v1/sources", map[string]any{}, "owner"},
		{"PUT", "/api/v1/sources/1", map[string]any{}, "owner"},
		{"DELETE", "/api/v1/sources/9999", nil, "owner"},
		{"POST", "/api/v1/sources/1/test", nil, "owner"},
		{"POST", "/api/v1/sources/import", map[string]any{}, "owner"},
		{"GET", "/api/v1/profiles", nil, "owner"},
		{"PUT", "/api/v1/profiles/1", map[string]any{}, "owner"},
		{"GET", "/api/v1/blocklist", nil, "owner"},
		{"DELETE", "/api/v1/blocklist/9999", nil, "owner"},
		{"GET", releases, nil, "manage"},
		{"POST", grabPath, map[string]any{}, "download"},
	}
	for _, rt := range routes {
		call := func(c *client) reply {
			if rt.method == "GET" {
				return c.send(rt.method, rt.path, nil)
			}
			return c.do(rt.method, rt.path, rt.body)
		}
		if r := call(anon); r.Status != 401 || r.errCode(t) != "unauthorized" {
			t.Errorf("%s %s signed out: %d %s", rt.method, rt.path, r.Status, r.Body)
		}
		if r := call(member); r.Status != 403 || r.errCode(t) != "forbidden" {
			t.Errorf("%s %s as a member: %d %s", rt.method, rt.path, r.Status, r.Body)
		}
		adminMay := rt.who != "owner"
		if r := call(admin); adminMay == (r.Status == 403) {
			t.Errorf("%s %s as an admin: %d %s (may call: %v)", rt.method, rt.path, r.Status, r.Body, adminMay)
		}
		if r := call(owner); r.Status == 401 || r.Status == 403 {
			t.Errorf("%s %s as the owner: %d %s", rt.method, rt.path, r.Status, r.Body)
		}
	}

	// API keys carry only the ticks they were given.
	bearer := func(secret, method, path string, body any) reply {
		return a.newClient().send(method, path, body, "Authorization", "Bearer "+secret)
	}
	if r := bearer(ownerKey, "GET", "/api/v1/sources", nil); r.Status != 403 || r.errCode(t) != "insufficient_scope" {
		t.Errorf("see-only key on sources: %d %s", r.Status, r.Body)
	}
	if r := bearer(ownerKey, "GET", releases, nil); r.Status != 403 || r.errCode(t) != "insufficient_scope" {
		t.Errorf("see-only key on releases: %d %s", r.Status, r.Body)
	}
	if r := bearer(adminScopeKey, "GET", releases, nil); r.Status != 200 {
		t.Errorf("admin key on releases: %d %s", r.Status, r.Body)
	}
	if r := bearer(adminScopeKey, "POST", grabPath, map[string]any{"indexerId": 1, "guid": "x"}); r.Status != 403 {
		t.Errorf("admin key on grab: %d %s", r.Status, r.Body)
	}
	if r := bearer(downloadsKey, "POST", grabPath, map[string]any{"indexerId": 1, "guid": "x"}); r.Status == 403 || r.Status == 401 {
		t.Errorf("downloads key on grab: %d %s", r.Status, r.Body)
	}
	if r := bearer(settingsKey, "GET", "/api/v1/sources", nil); r.Status != 200 {
		t.Errorf("settings key on sources: %d %s", r.Status, r.Body)
	}
	// A cookie session must send the CSRF header for changes, as everywhere else.
	if r := owner.send("POST", "/api/v1/sources", map[string]any{}); r.Status != 403 || r.errCode(t) != "csrf" {
		t.Errorf("no CSRF header: %d %s", r.Status, r.Body)
	}
}

func TestRoutesAreAbsentWithoutTheServices(t *testing.T) {
	a := newApp(t, nil) // a server with no sources, decide or search
	owner := a.owner()
	for _, p := range []string{"/api/v1/sources", "/api/v1/profiles", "/api/v1/blocklist", "/api/v1/titles/1/releases"} {
		if r := owner.send("GET", p, nil); r.Status != 404 || r.errCode(t) != "not_found" {
			t.Errorf("%s: %d %s", p, r.Status, r.Body)
		}
	}
}

// ---- sources ---------------------------------------------------------------------------------

func TestSourceCRUDMasksTheKeyEverywhere(t *testing.T) {
	a := newSrcApp(t, true)
	owner := a.owner()

	src := a.addSource(owner, "Alpha")
	id := itoa64(src["id"].(float64))
	if src["apiKey"] != "••••b1a0" || src["kind"] != "torznab" || src["enabled"] != true || src["priority"].(float64) != 25 {
		t.Errorf("created = %v", src)
	}
	if cats := src["categories"].([]any); len(cats) != 2 {
		t.Errorf("categories = %v", cats)
	}

	list := owner.send("GET", "/api/v1/sources", nil)
	if list.Status != 200 {
		t.Fatalf("list = %d %s", list.Status, list.Body)
	}
	if len(list.JSON(t)["sources"].([]any)) != 1 {
		t.Errorf("list = %s", list.Body)
	}

	// Rename; the masked key comes back unchanged and the stored key survives.
	r := owner.do("PUT", "/api/v1/sources/"+id, map[string]any{"name": "Alpha 2", "apiKey": "••••b1a0", "priority": 7, "enabled": true})
	if r.Status != 200 {
		t.Fatalf("update = %d %s", r.Status, r.Body)
	}
	up := r.JSON(t)["source"].(map[string]any)
	if up["name"] != "Alpha 2" || up["priority"].(float64) != 7 || up["apiKey"] != "••••b1a0" {
		t.Errorf("updated = %v", up)
	}
	if tr := owner.do("POST", "/api/v1/sources/"+id+"/test", nil); tr.Status != 200 || tr.JSON(t)["ok"] != true {
		t.Errorf("the key must still work after a masked update: %d %s", tr.Status, tr.Body)
	}

	// Nowhere does the full key appear: list, create, update, test, and the database row is sealed.
	for what, body := range map[string][]byte{"list": list.Body, "update": r.Body} {
		if strings.Contains(string(body), srcKey) {
			t.Errorf("the %s answer contains the API key", what)
		}
	}
	var sealed string
	if err := a.mainDB.QueryRow(`SELECT api_key_sealed FROM indexers`).Scan(&sealed); err != nil || strings.Contains(sealed, srcKey) || sealed == "" {
		t.Errorf("stored key: %q %v", sealed, err)
	}

	// Switch it off, then delete it.
	off := owner.do("PUT", "/api/v1/sources/"+id, map[string]any{"enabled": false})
	if off.Status != 200 || off.JSON(t)["source"].(map[string]any)["enabled"] != false {
		t.Errorf("disable = %d %s", off.Status, off.Body)
	}
	if d := owner.do("DELETE", "/api/v1/sources/"+id, nil); d.Status != 204 {
		t.Errorf("delete = %d %s", d.Status, d.Body)
	}
	if d := owner.do("DELETE", "/api/v1/sources/"+id, nil); d.Status != 404 || d.errCode(t) != "not_found" {
		t.Errorf("second delete = %d %s", d.Status, d.Body)
	}
	if d := owner.do("PUT", "/api/v1/sources/"+id, map[string]any{"name": "x"}); d.Status != 404 {
		t.Errorf("update after delete = %d", d.Status)
	}
	if d := owner.do("DELETE", "/api/v1/sources/abc", nil); d.Status != 404 {
		t.Errorf("non-numeric id = %d", d.Status)
	}
}

func TestSourceValidationAndDuplicates(t *testing.T) {
	a := newSrcApp(t, true)
	owner := a.owner()
	a.addSource(owner, "Alpha")
	for name, body := range map[string]map[string]any{
		"no name":    {"kind": "torznab", "url": "http://x.example.invalid/api"},
		"bad kind":   {"name": "A", "kind": "rss", "url": "http://x.example.invalid/api"},
		"bad url":    {"name": "A", "kind": "torznab", "url": "ftp://x"},
		"bad cats":   {"name": "A", "kind": "torznab", "url": "http://x.example.invalid/api", "categories": []int{3}},
		"bad prio":   {"name": "A", "kind": "torznab", "url": "http://x.example.invalid/api", "priority": 80},
		"empty body": {},
	} {
		if r := owner.do("POST", "/api/v1/sources", body); r.Status != 400 || r.errCode(t) != "invalid_request" {
			t.Errorf("%s: %d %s", name, r.Status, r.Body)
		}
	}
	// The same address twice.
	r := owner.do("POST", "/api/v1/sources", map[string]any{"name": "Beta", "kind": "newznab", "url": a.feed.Feed()})
	if r.Status != 409 || r.errCode(t) != "duplicate" {
		t.Errorf("duplicate: %d %s", r.Status, r.Body)
	}
	if r := owner.do("POST", "/api/v1/sources", "not an object"); r.Status != 400 {
		t.Errorf("bad json: %d", r.Status)
	}
}

func TestSourceTestEndpoint(t *testing.T) {
	a := newSrcApp(t, true)
	owner := a.owner()
	src := a.addSource(owner, "Alpha")
	id := itoa64(src["id"].(float64))

	r := owner.do("POST", "/api/v1/sources/"+id+"/test", nil)
	if r.Status != 200 {
		t.Fatalf("test = %d %s", r.Status, r.Body)
	}
	m := r.JSON(t)
	if m["ok"] != true || !strings.Contains(m["message"].(string), "Works.") || m["caps"] == nil {
		t.Errorf("test = %v", m)
	}
	if got := m["source"].(map[string]any); got["lastOkAt"] == nil || got["caps"] == nil {
		t.Errorf("the source should show what the test learned: %v", got)
	}

	// A wrong key is a 200 with ok:false, in plain words, without the key.
	bad := owner.do("PUT", "/api/v1/sources/"+id, map[string]any{"apiKey": "ffffffffffffffffffffffffffffffff"})
	if bad.Status != 200 {
		t.Fatal(bad.Body)
	}
	r = owner.do("POST", "/api/v1/sources/"+id+"/test", nil)
	m = r.JSON(t)
	if r.Status != 200 || m["ok"] != false || m["code"] != "key_refused" || strings.Contains(string(r.Body), "ffffffffffffffffffffffffffffffff") {
		t.Errorf("wrong key: %d %s", r.Status, r.Body)
	}
	if got := m["source"].(map[string]any); got["lastError"] == "" || got["lastErrorAt"] == nil {
		t.Errorf("the failure should show on the source: %v", got)
	}
	if r := owner.do("POST", "/api/v1/sources/9999/test", nil); r.Status != 404 {
		t.Errorf("unknown source: %d", r.Status)
	}
}

func TestSourceImport(t *testing.T) {
	a := newSrcApp(t, true)
	owner := a.owner()

	const prowlarrKey = "prowlarr-key-0123456789abcdef-qrst"
	prowlarr := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/v1/indexer" || r.Header.Get("X-Api-Key") != prowlarrKey {
			http.Error(w, "no", http.StatusUnauthorized)
			return
		}
		_ = json.NewEncoder(w).Encode([]map[string]any{
			{"id": 1, "name": "Open Torrents", "enable": true, "protocol": "torrent", "priority": 12},
			{"id": 2, "name": "Open Usenet", "enable": true, "protocol": "usenet", "priority": 25},
			{"id": 3, "name": "Off", "enable": false, "protocol": "torrent", "priority": 25},
		})
	}))
	t.Cleanup(prowlarr.Close)
	jackett := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/v2.0/indexers" || r.URL.Query().Get("apikey") != prowlarrKey || r.URL.Query().Get("configured") != "true" {
			http.Error(w, "no", http.StatusUnauthorized)
			return
		}
		_ = json.NewEncoder(w).Encode([]map[string]any{{"id": "openfilms", "name": "Open Films", "configured": true}})
	}))
	t.Cleanup(jackett.Close)

	r := owner.do("POST", "/api/v1/sources/import", map[string]any{"from": "prowlarr", "url": prowlarr.URL, "apiKey": prowlarrKey})
	if r.Status != 201 {
		t.Fatalf("import = %d %s", r.Status, r.Body)
	}
	m := r.JSON(t)
	created := m["sources"].([]any)
	if len(created) != 2 || m["skipped"].(float64) != 0 {
		t.Fatalf("import = %s", r.Body)
	}
	first := created[0].(map[string]any)
	if first["url"] != prowlarr.URL+"/1/api" || first["kind"] != "torznab" || first["priority"].(float64) != 12 || first["apiKey"] != "••••qrst" {
		t.Errorf("first = %v", first)
	}
	if second := created[1].(map[string]any); second["kind"] != "newznab" || second["url"] != prowlarr.URL+"/2/api" {
		t.Errorf("second = %v", second)
	}
	if strings.Contains(string(r.Body), prowlarrKey) {
		t.Error("the import answer contains the Prowlarr key")
	}

	// Again: nothing new.
	if again := owner.do("POST", "/api/v1/sources/import", map[string]any{"from": "prowlarr", "url": prowlarr.URL, "apiKey": prowlarrKey}).JSON(t); len(again["sources"].([]any)) != 0 || again["skipped"].(float64) != 2 {
		t.Errorf("second import = %v", again)
	}
	// Jackett.
	r = owner.do("POST", "/api/v1/sources/import", map[string]any{"from": "jackett", "url": jackett.URL, "apiKey": prowlarrKey})
	if r.Status != 201 || len(r.JSON(t)["sources"].([]any)) != 1 {
		t.Errorf("jackett import = %d %s", r.Status, r.Body)
	}
	// Errors.
	if r := owner.do("POST", "/api/v1/sources/import", map[string]any{"from": "prowlarr", "url": prowlarr.URL, "apiKey": "wrong-wrong-wrong-wrong"}); r.Status != 502 || r.errCode(t) != "key_refused" || strings.Contains(string(r.Body), "wrong-wrong-wrong-wrong") {
		t.Errorf("wrong key: %d %s", r.Status, r.Body)
	}
	if r := owner.do("POST", "/api/v1/sources/import", map[string]any{"from": "sonarr", "url": prowlarr.URL, "apiKey": "k"}); r.Status != 400 || r.errCode(t) != "invalid_request" {
		t.Errorf("unknown bridge: %d %s", r.Status, r.Body)
	}
	if list := owner.send("GET", "/api/v1/sources", nil).JSON(t)["sources"].([]any); len(list) != 3 {
		t.Errorf("%d sources after the imports, want 3", len(list))
	}
}

// ---- profiles and the blocklist -----------------------------------------------------------------

func TestProfiles(t *testing.T) {
	a := newSrcApp(t, true)
	owner := a.owner()
	r := owner.send("GET", "/api/v1/profiles", nil)
	if r.Status != 200 {
		t.Fatalf("list = %d %s", r.Status, r.Body)
	}
	list := r.JSON(t)["profiles"].([]any)
	if len(list) != 5 {
		t.Fatalf("%d profiles", len(list))
	}
	first := list[0].(map[string]any)
	if first["name"] != "HD" || first["kind"] != "movie" || first["quality"] != "hd" || first["preferredRes"].(float64) != 1080 ||
		first["maxSizeMb"].(float64) != 20480 || first["minSeeders"].(float64) != 1 || first["minSizeMb"] != nil || first["upgradeUntil"] != nil {
		t.Errorf("first profile = %v", first)
	}
	id := itoa64(first["id"].(float64))

	// Send only what changes.
	up := owner.do("PUT", "/api/v1/profiles/"+id, map[string]any{"maxSizeMb": 8000, "rejectedWords": []string{"HC", "Korsub"}, "preferHdr": true})
	if up.Status != 200 {
		t.Fatalf("update = %d %s", up.Status, up.Body)
	}
	got := up.JSON(t)["profile"].(map[string]any)
	if got["maxSizeMb"].(float64) != 8000 || got["preferHdr"] != true || got["name"] != "HD" || got["preferredRes"].(float64) != 1080 || len(got["rejectedWords"].([]any)) != 2 {
		t.Errorf("updated = %v", got)
	}
	// null clears a size.
	cl := owner.do("PUT", "/api/v1/profiles/"+id, map[string]any{"maxSizeMb": nil})
	if cl.Status != 200 || cl.JSON(t)["profile"].(map[string]any)["maxSizeMb"] != nil {
		t.Errorf("clear = %d %s", cl.Status, cl.Body)
	}
	// Mistakes are plain 400s; the profile is unchanged.
	for name, body := range map[string]map[string]any{
		"bad resolution": {"allowedRes": []int{1080, 999}},
		"bad source":     {"allowedSources": []string{"VHS"}},
		"min over max":   {"minSizeMb": 99999, "maxSizeMb": 100},
		"empty name":     {"name": ""},
	} {
		if r := owner.do("PUT", "/api/v1/profiles/"+id, body); r.Status != 400 || r.errCode(t) != "invalid_request" {
			t.Errorf("%s: %d %s", name, r.Status, r.Body)
		}
	}
	if r := owner.do("PUT", "/api/v1/profiles/"+id, map[string]any{"name": "4K"}); r.Status != 409 || r.errCode(t) != "duplicate" {
		// movie/uhd is named 4K already
		t.Errorf("duplicate name: %d %s", r.Status, r.Body)
	}
	if r := owner.do("PUT", "/api/v1/profiles/9999", map[string]any{}); r.Status != 404 {
		t.Errorf("unknown profile: %d", r.Status)
	}
	if r := owner.do("PUT", "/api/v1/profiles/"+id, "oops"); r.Status != 400 {
		t.Errorf("bad json: %d", r.Status)
	}
	if cur := owner.send("GET", "/api/v1/profiles", nil).JSON(t)["profiles"].([]any)[0].(map[string]any); cur["name"] != "HD" || cur["maxSizeMb"] != nil {
		t.Errorf("a refused update changed the profile: %v", cur)
	}
}

func TestBlocklist(t *testing.T) {
	a := newSrcApp(t, true)
	owner := a.owner()
	title := a.addMovie("Metropolis", 1927, "tt0017136", 19)
	if r := owner.send("GET", "/api/v1/blocklist", nil); r.Status != 200 || len(r.JSON(t)["items"].([]any)) != 0 || r.JSON(t)["total"].(float64) != 0 {
		t.Errorf("empty blocklist = %d %s", r.Status, r.Body)
	}
	e, err := a.dec.Block(context.Background(), decide.BlockEntry{TitleID: title, GUID: "g-1", InfoHash: "0123456789abcdef0123456789abcdef01234567",
		ReleaseTitle: "Metropolis.1927.1080p.BluRay.x264-GRP", Reason: "wrong cut"})
	if err != nil {
		t.Fatal(err)
	}
	r := owner.send("GET", "/api/v1/blocklist", nil)
	items := r.JSON(t)["items"].([]any)
	if r.Status != 200 || len(items) != 1 {
		t.Fatalf("blocklist = %d %s", r.Status, r.Body)
	}
	it := items[0].(map[string]any)
	if it["titleId"].(float64) != float64(title) || it["guid"] != "g-1" || it["reason"] != "wrong cut" || it["releaseTitle"] == "" || it["createdAt"] == nil {
		t.Errorf("entry = %v", it)
	}
	if r := owner.send("GET", "/api/v1/blocklist?limit=abc", nil); r.Status != 400 {
		t.Errorf("bad limit: %d", r.Status)
	}
	if d := owner.do("DELETE", "/api/v1/blocklist/"+strconv.FormatInt(e.ID, 10), nil); d.Status != 204 {
		t.Errorf("delete = %d %s", d.Status, d.Body)
	}
	if d := owner.do("DELETE", "/api/v1/blocklist/"+strconv.FormatInt(e.ID, 10), nil); d.Status != 404 || d.errCode(t) != "not_found" {
		t.Errorf("second delete = %d %s", d.Status, d.Body)
	}
	if d := owner.do("DELETE", "/api/v1/blocklist/xyz", nil); d.Status != 404 {
		t.Errorf("bad id = %d", d.Status)
	}
}

// ---- releases and grabbing ----------------------------------------------------------------------------

func TestReleasesShapeAndNoLinks(t *testing.T) {
	a := newSrcApp(t, true)
	owner := a.owner()
	admin := a.admin(owner)
	title := a.addMovie("Metropolis", 1927, "tt0017136", 19)
	a.addSource(owner, "Alpha")
	a.serveMetropolis()
	path := "/api/v1/titles/" + strconv.FormatInt(title, 10) + "/releases"

	for who, c := range map[string]*client{"owner": owner, "admin": admin} {
		r := c.send("GET", path+"?quality=hd", nil)
		if r.Status != 200 {
			t.Fatalf("%s: releases = %d %s", who, r.Status, r.Body)
		}
		m := r.JSON(t)
		for _, k := range []string{"results", "searchedAt", "indexersFailed"} {
			if _, ok := m[k]; !ok {
				t.Errorf("%s: the answer has no %q: %s", who, k, r.Body)
			}
		}
		if _, err := time.Parse(time.RFC3339, m["searchedAt"].(string)); err != nil {
			t.Errorf("searchedAt = %v", m["searchedAt"])
		}
		if f, ok := m["indexersFailed"].([]any); !ok || len(f) != 0 {
			t.Errorf("indexersFailed = %v, want an empty list", m["indexersFailed"])
		}
		results := m["results"].([]any)
		if len(results) != 4 {
			t.Fatalf("%d results", len(results))
		}
		first := results[0].(map[string]any)
		// Every key the table reads, and nothing that is a link.
		for _, k := range []string{"indexer", "guid", "title", "size", "protocol", "seeders", "age", "quality", "score", "accepted", "reasons", "rejection"} {
			if _, ok := first[k]; !ok {
				t.Errorf("%s: a result has no %q: %v", who, k, first)
			}
		}
		ix := first["indexer"].(map[string]any)
		if ix["name"] != "Alpha" || ix["id"].(float64) < 1 {
			t.Errorf("indexer = %v", ix)
		}
		if first["title"] != "Metropolis.1927.1080p.BluRay.x264-GRP" || first["guid"] != "metropolis-1080" || first["protocol"] != "torrent" ||
			first["quality"] != "Bluray-1080p" || first["accepted"] != true || first["seeders"].(float64) != 25 || first["size"].(float64) != 8<<30 ||
			first["rejection"] != "" || first["score"].(float64) <= 0 {
			t.Errorf("first result = %v", first)
		}
		if age := first["age"].(float64); age != 0 {
			t.Errorf("age = %v days for a release 5 hours old", age)
		}
		reasons := first["reasons"].([]any)
		if len(reasons) == 0 || reasons[0] != "Best match: 1080p BluRay encode under your size limit" {
			t.Errorf("reasons = %v", reasons)
		}
		var rejectedWith []string
		for _, x := range results {
			res := x.(map[string]any)
			if res["accepted"] == false {
				if res["score"].(float64) != 0 || res["rejection"] == "" || len(res["reasons"].([]any)) == 0 {
					t.Errorf("a rejected result needs its reasons: %v", res)
				}
				rejectedWith = append(rejectedWith, res["rejection"].(string))
			}
		}
		if len(rejectedWith) != 2 {
			t.Errorf("rejections = %v, want two (CAM and 2160p)", rejectedWith)
		}
		// No download link, magnet, hash, key or source address anywhere in the answer.
		body := string(r.Body)
		for _, leak := range []string{"download", "magnet", "btih", "apikey", srcKey, "127.0.0.1", "secretpass", "infoHash", "link"} {
			if strings.Contains(strings.ToLower(body), strings.ToLower(leak)) {
				t.Errorf("%s: the releases answer contains %q", who, leak)
			}
		}
	}
}

func TestReleasesRequestChecks(t *testing.T) {
	a := newSrcApp(t, true)
	owner := a.owner()
	title := a.addMovie("Metropolis", 1927, "tt0017136", 19)
	base := "/api/v1/titles/" + strconv.FormatInt(title, 10) + "/releases"
	for path, status := range map[string]int{
		base + "?quality=sd":           400,
		base + "?quality=HD":           400,
		base + "?season=x":             400,
		base + "?quality=hd&episode=2": 400, // an episode needs a season
		base + "?season=-1":            400,
		"/api/v1/titles/9999/releases": 404,
		"/api/v1/titles/abc/releases":  404,
		base:                           200, // quality defaults to hd
		base + "?quality=uhd":          200,
	} {
		r := owner.send("GET", path, nil)
		if r.Status != status {
			t.Errorf("GET %s = %d %s, want %d", path, r.Status, r.Body, status)
		}
		if status != 200 {
			_ = r.errCode(t)
		}
	}
	// With no sources the answer is an empty list, not an error.
	r := owner.send("GET", base, nil).JSON(t)
	if len(r["results"].([]any)) != 0 || len(r["indexersFailed"].([]any)) != 0 || r["indexersSearched"].(float64) != 0 {
		t.Errorf("no sources: %v", r)
	}
}

func TestReleasesListsFailedSources(t *testing.T) {
	a := newSrcApp(t, true)
	owner := a.owner()
	title := a.addMovie("Metropolis", 1927, "tt0017136", 19)
	a.addSource(owner, "Alpha")
	a.serveMetropolis()
	bad := sourcestest.NewServer(t, srcKey)
	bad.Fail = 500
	if r := owner.do("POST", "/api/v1/sources", map[string]any{"name": "Broken", "kind": "torznab", "url": bad.Feed(), "apiKey": srcKey}); r.Status != 201 {
		t.Fatal(r.Body)
	}
	r := owner.send("GET", "/api/v1/titles/"+strconv.FormatInt(title, 10)+"/releases", nil)
	m := r.JSON(t)
	failed := m["indexersFailed"].([]any)
	if r.Status != 200 || len(failed) != 1 || m["indexersSearched"].(float64) != 2 || len(m["results"].([]any)) != 4 {
		t.Fatalf("answer = %d %s", r.Status, r.Body)
	}
	f := failed[0].(map[string]any)
	if f["name"] != "Broken" || f["error"] == "" || f["id"].(float64) < 1 {
		t.Errorf("failure = %v", f)
	}
	if strings.Contains(string(r.Body), srcKey) {
		t.Error("the answer contains the key")
	}
}

func TestGrabNeedsADownloadClient(t *testing.T) {
	a := newSrcApp(t, false) // no download client is set up
	owner := a.owner()
	title := a.addMovie("Metropolis", 1927, "tt0017136", 19)
	a.want(title, "hd")
	src := a.addSource(owner, "Alpha")
	a.serveMetropolis()
	path := "/api/v1/titles/" + strconv.FormatInt(title, 10)

	rel := owner.send("GET", path+"/releases", nil).JSON(t)["results"].([]any)[0].(map[string]any)
	r := owner.do("POST", path+"/grab", map[string]any{"indexerId": src["id"], "guid": rel["guid"], "quality": "hd"})
	if r.Status != 409 || r.errCode(t) != "no_grabber" {
		t.Errorf("grab without a client: %d %s", r.Status, r.Body)
	}
	if len(a.grabber.calls) != 0 {
		t.Error("something was sent")
	}
}

func TestGrabSendsTheReleaseWithoutShowingItsLink(t *testing.T) {
	a := newSrcApp(t, true)
	owner := a.owner()
	title := a.addMovie("Metropolis", 1927, "tt0017136", 19)
	a.want(title, "hd")
	src := a.addSource(owner, "Alpha")
	a.serveMetropolis()
	path := "/api/v1/titles/" + strconv.FormatInt(title, 10)

	results := owner.send("GET", path+"/releases?quality=hd", nil).JSON(t)["results"].([]any)
	pick := results[1].(map[string]any) // the 720p release
	r := owner.do("POST", path+"/grab", map[string]any{"indexerId": pick["indexer"].(map[string]any)["id"], "guid": pick["guid"], "quality": "hd"})
	if r.Status != 201 {
		t.Fatalf("grab = %d %s", r.Status, r.Body)
	}
	m := r.JSON(t)
	if m["downloadId"].(float64) != 41 {
		t.Errorf("downloadId = %v", m["downloadId"])
	}
	rel := m["release"].(map[string]any)
	if rel["title"] != pick["title"] || rel["quality"] != "WEBDL-720p" || rel["score"].(float64) != pick["score"].(float64) {
		t.Errorf("release = %v", rel)
	}
	if body := strings.ToLower(string(r.Body)); strings.Contains(body, "download/") || strings.Contains(body, "apikey") || strings.Contains(body, "magnet") {
		t.Errorf("the grab answer shows a link: %s", r.Body)
	}

	if len(a.grabber.calls) != 1 {
		t.Fatalf("%d grabs", len(a.grabber.calls))
	}
	c := a.grabber.calls[0]
	if c.Target.TitleID != title || c.Target.Quality != "hd" || c.Target.Kind != "movie" || c.Target.CopyID == 0 {
		t.Errorf("target = %+v", c.Target)
	}
	if c.Release.Title != pick["title"] || !strings.Contains(c.Release.DownloadURL, "/download/") || c.Release.IndexerID != int64(src["id"].(float64)) {
		t.Errorf("the grabber needs the real link: %+v", c.Release)
	}
	if len(c.Release.Reasons) == 0 || c.Release.Reasons[0] != "Picked by hand from the releases found" {
		t.Errorf("reasons = %q", c.Release.Reasons)
	}
}

func TestGrabErrors(t *testing.T) {
	a := newSrcApp(t, true)
	owner := a.owner()
	title := a.addMovie("Metropolis", 1927, "tt0017136", 19)
	src := a.addSource(owner, "Alpha")
	a.serveMetropolis()
	path := "/api/v1/titles/" + strconv.FormatInt(title, 10) + "/grab"
	results := owner.send("GET", "/api/v1/titles/"+strconv.FormatInt(title, 10)+"/releases", nil).JSON(t)["results"].([]any)
	guid := results[0].(map[string]any)["guid"]

	// No copy of the title is wanted yet.
	if r := owner.do("POST", path, map[string]any{"indexerId": src["id"], "guid": guid, "quality": "hd"}); r.Status != 409 || r.errCode(t) != "no_copy" {
		t.Errorf("no copy: %d %s", r.Status, r.Body)
	}
	a.want(title, "hd")
	// A release nobody remembers.
	if r := owner.do("POST", path, map[string]any{"indexerId": src["id"], "guid": "never-seen", "quality": "hd"}); r.Status != 404 || r.errCode(t) != "release_gone" {
		t.Errorf("unknown release: %d %s", r.Status, r.Body)
	}
	// Bad input.
	for name, body := range map[string]any{
		"no guid":     map[string]any{"indexerId": src["id"], "quality": "hd"},
		"no indexer":  map[string]any{"guid": guid},
		"bad quality": map[string]any{"indexerId": src["id"], "guid": guid, "quality": "8k"},
	} {
		if r := owner.do("POST", path, body); r.Status != 400 || r.errCode(t) != "invalid_request" {
			t.Errorf("%s: %d %s", name, r.Status, r.Body)
		}
	}
	if r := owner.do("POST", "/api/v1/titles/9999/grab", map[string]any{"indexerId": src["id"], "guid": guid}); r.Status != 404 {
		t.Errorf("unknown title: %d %s", r.Status, r.Body)
	}
	if r := owner.do("POST", path, "nope"); r.Status != 400 {
		t.Errorf("bad json: %d", r.Status)
	}
	if len(a.grabber.calls) != 0 {
		t.Errorf("%d grabs after only mistakes", len(a.grabber.calls))
	}
	// The quality defaults to hd.
	if r := owner.do("POST", path, map[string]any{"indexerId": src["id"], "guid": guid}); r.Status != 201 {
		t.Errorf("default quality: %d %s", r.Status, r.Body)
	}
}
