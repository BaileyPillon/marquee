package web

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
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
	"github.com/BaileyPillon/marquee/internal/downloads"
	"github.com/BaileyPillon/marquee/internal/grab"
	"github.com/BaileyPillon/marquee/internal/settings"
)

// ---- test rig --------------------------------------------------------------

// dlQBT is a minimal qBittorrent: it accepts anything a test sends it and
// remembers what it was asked.
type dlQBT struct {
	srv *httptest.Server

	mu       sync.Mutex
	adds     int
	cmds     []string // "stop:<hash>", "start:<hash>", "delete:<hash>:<deleteFiles>"
	torrents []map[string]any
}

func newDLQBT(t *testing.T) *dlQBT {
	q := &dlQBT{}
	q.srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		q.mu.Lock()
		defer q.mu.Unlock()
		_ = r.ParseForm()
		switch strings.TrimPrefix(r.URL.Path, "/api/v2") {
		case "/auth/login":
			http.SetCookie(w, &http.Cookie{Name: "SID", Value: "s", Path: "/"})
			_, _ = io.WriteString(w, "Ok.")
		case "/app/version":
			_, _ = io.WriteString(w, "v5.0.1")
		case "/torrents/categories":
			_, _ = io.WriteString(w, "{}")
		case "/torrents/createCategory":
		case "/torrents/add":
			q.adds++
			_, _ = io.WriteString(w, "Ok.")
		case "/torrents/info":
			out := q.torrents
			if out == nil {
				out = []map[string]any{}
			}
			_ = json.NewEncoder(w).Encode(out)
		case "/torrents/stop", "/torrents/start":
			q.cmds = append(q.cmds, strings.TrimPrefix(r.URL.Path, "/api/v2/torrents/")+":"+r.PostForm.Get("hashes"))
		case "/torrents/delete":
			q.cmds = append(q.cmds, "delete:"+r.PostForm.Get("hashes")+":"+r.PostForm.Get("deleteFiles"))
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(q.srv.Close)
	return q
}

func (q *dlQBT) commands() []string {
	q.mu.Lock()
	defer q.mu.Unlock()
	return append([]string(nil), q.cmds...)
}

// dlApp is a whole Marquee server with the downloads module on real adapters.
type dlApp struct {
	*app
	dl *downloads.Service
}

func newDLApp(t *testing.T) *dlApp {
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
	hc := outbound.New("test", outbound.Options{Logger: log, HostRPS: 100000, HostBurst: 100000, MaxRetries: -1})
	dl := downloads.NewService(mainDB, downloads.Options{Logger: log, Hub: hub, HTTP: hc, Box: box})
	s := New(Deps{
		Version:   "9.9.9-test",
		Log:       log,
		MainDB:    mainDB,
		EventsDB:  eventsDB,
		Accounts:  accounts.NewService(mainDB, accounts.Options{Logger: log, PasswordParams: lightParams}),
		Settings:  settings.New(mainDB, box),
		Hub:       hub,
		Downloads: dl,
		UI:        testUI(),
	})
	srv := httptest.NewServer(s.Handler())
	t.Cleanup(srv.Close)
	return &dlApp{app: &app{t: t, srv: srv, mainDB: mainDB, hub: hub}, dl: dl}
}

// people returns an owner, an admin, a member and a signed-out client.
func (a *dlApp) people() (owner, admin, member, anon *client) {
	owner = a.owner()
	inv := owner.do("POST", "/api/v1/invites", map[string]string{"role": "admin"})
	if inv.Status != http.StatusCreated {
		a.t.Fatalf("invite = %d %s", inv.Status, inv.Body)
	}
	admin = a.newClient()
	if r := admin.do("POST", "/api/v1/invites/accept", map[string]string{
		"token": inv.JSON(a.t)["token"].(string), "name": "Alice", "password": "admin password"}); r.Status != http.StatusCreated {
		a.t.Fatalf("accept = %d %s", r.Status, r.Body)
	}
	return owner, admin, a.member(owner, "Mia"), a.newClient()
}

// addTitle inserts a movie row and returns its id.
func (a *dlApp) addTitle(name string, year int) int64 {
	a.t.Helper()
	ts := db.TS(time.Now())
	res, err := a.mainDB.Exec(`INSERT INTO titles (kind, name, sort_name, year, poster_ref, added_at, updated_at, refreshed_at)
		VALUES ('movie', ?, ?, ?, 'tmdb/w500/poster.jpg', ?, ?, ?)`, name, strings.ToLower(name), year, ts, ts, ts)
	if err != nil {
		a.t.Fatal(err)
	}
	id, _ := res.LastInsertId()
	return id
}

// addClient creates a qBittorrent client through the API and returns its id.
func (a *dlApp) addClient(owner *client, q *dlQBT, name string) int64 {
	a.t.Helper()
	r := owner.do("POST", "/api/v1/clients", map[string]any{
		"name": name, "kind": "qbittorrent", "url": q.srv.URL, "username": "admin", "password": "a long qbittorrent password",
	})
	if r.Status != http.StatusCreated {
		a.t.Fatalf("add client = %d %s", r.Status, r.Body)
	}
	return int64(r.JSON(a.t)["client"].(map[string]any)["id"].(float64))
}

func (a *dlApp) grab(titleID int64, hash string) int64 {
	a.t.Helper()
	id, err := a.dl.Grab(context.Background(),
		grab.Target{CopyID: 4, TitleID: titleID, Kind: "movie", Quality: grab.QualityHD},
		grab.Release{Title: "Metropolis.1927.1080p", Protocol: grab.ProtocolTorrent, Size: 1 << 30,
			MagnetURL: "magnet:?xt=urn:btih:" + hash + "&dn=Metropolis"})
	if err != nil {
		a.t.Fatal(err)
	}
	return id
}

func bearer(secret string) []string { return []string{"Authorization", "Bearer " + secret} }

func (a *dlApp) key(owner *client, scopes ...string) string {
	a.t.Helper()
	r := owner.do("POST", "/api/v1/apikeys", map[string]any{"name": "k", "scopes": scopes})
	if r.Status != http.StatusCreated {
		a.t.Fatalf("create key = %d %s", r.Status, r.Body)
	}
	return r.JSON(a.t)["secret"].(string)
}

const hashA = "dd8255ecdc7ca55fb0bbf81323d87062db1f6d1c"

// ---- permissions -------------------------------------------------------------

func TestDownloadRoutesArePermissioned(t *testing.T) {
	a := newDLApp(t)
	owner, admin, member, anon := a.people()
	q := newDLQBT(t)
	cid := a.addClient(owner, q, "qb")
	id := a.grab(a.addTitle("Metropolis", 1927), hashA)

	dlRoutes := []struct{ method, path string }{
		{"GET", "/api/v1/downloads"},
		{"GET", "/api/v1/downloads/summary"},
		{"POST", "/api/v1/downloads/" + itoa(id) + "/pause"},
		{"POST", "/api/v1/downloads/" + itoa(id) + "/resume"},
		{"DELETE", "/api/v1/downloads/" + itoa(id)},
	}
	clientRoutes := []struct{ method, path string }{
		{"GET", "/api/v1/clients"},
		{"POST", "/api/v1/clients"},
		{"PUT", "/api/v1/clients/" + itoa(cid)},
		{"DELETE", "/api/v1/clients/" + itoa(cid)},
		{"POST", "/api/v1/clients/" + itoa(cid) + "/test"},
	}
	all := append(append([]struct{ method, path string }{}, dlRoutes...), clientRoutes...)

	// Signed out: every route says to sign in.
	for _, rt := range all {
		if r := anon.do(rt.method, rt.path, map[string]any{}); r.Status != 401 || r.errCode(t) != "unauthorized" {
			t.Errorf("anon %s %s = %d %s", rt.method, rt.path, r.Status, r.Body)
		}
	}
	// A member may do none of it.
	for _, rt := range all {
		if r := member.do(rt.method, rt.path, map[string]any{}); r.Status != 403 || r.errCode(t) != "forbidden" {
			t.Errorf("member %s %s = %d %s", rt.method, rt.path, r.Status, r.Body)
		}
	}
	// An admin runs downloads but cannot touch clients (they hold passwords).
	for _, rt := range clientRoutes {
		if r := admin.do(rt.method, rt.path, map[string]any{}); r.Status != 403 || r.errCode(t) != "forbidden" {
			t.Errorf("admin %s %s = %d %s", rt.method, rt.path, r.Status, r.Body)
		}
	}
	for _, rt := range dlRoutes[:2] {
		if r := admin.do(rt.method, rt.path, nil); r.Status != 200 {
			t.Errorf("admin %s %s = %d %s", rt.method, rt.path, r.Status, r.Body)
		}
	}
	if r := admin.do("POST", dlRoutes[2].path, nil); r.Status != 200 {
		t.Errorf("admin pause = %d %s", r.Status, r.Body)
	}

	// API keys: the "downloads" tick opens downloads, the "settings" tick opens clients.
	see := a.key(owner, "see", "ask")
	dlKey := a.key(owner, "downloads")
	setKey := a.key(owner, "settings")
	check := func(who string, c *client, secret, method, path string, want int, code string) {
		t.Helper()
		r := c.send(method, path, nil, bearer(secret)...)
		if r.Status != want || (code != "" && r.errCode(t) != code) {
			t.Errorf("%s %s %s = %d %s, want %d %s", who, method, path, r.Status, r.Body, want, code)
		}
	}
	script := a.newClient()
	check("see key", script, see, "GET", "/api/v1/downloads", 403, "insufficient_scope")
	check("see key", script, see, "GET", "/api/v1/clients", 403, "insufficient_scope")
	check("downloads key", script, dlKey, "GET", "/api/v1/downloads", 200, "")
	check("downloads key", script, dlKey, "GET", "/api/v1/downloads/summary", 200, "")
	check("downloads key", script, dlKey, "GET", "/api/v1/clients", 403, "insufficient_scope")
	check("settings key", script, setKey, "GET", "/api/v1/clients", 200, "")
	check("settings key", script, setKey, "GET", "/api/v1/downloads", 403, "insufficient_scope")

	// A key of an admin is held to the admin's role, even with a settings tick (admins cannot have one).
	adminKey := a.admKey(admin, "downloads")
	check("admin downloads key", script, adminKey, "GET", "/api/v1/downloads", 200, "")
	check("admin downloads key", script, adminKey, "GET", "/api/v1/clients", 403, "")

	// A browser session still needs the CSRF header.
	if r := owner.send("POST", "/api/v1/clients", map[string]any{"name": "x"}); r.Status != 403 || r.errCode(t) != "csrf" {
		t.Errorf("POST /clients without X-Marquee = %d %s", r.Status, r.Body)
	}
}

func (a *dlApp) admKey(admin *client, scopes ...string) string {
	a.t.Helper()
	r := admin.do("POST", "/api/v1/apikeys", map[string]any{"name": "k", "scopes": scopes})
	if r.Status != http.StatusCreated {
		a.t.Fatalf("admin key = %d %s", r.Status, r.Body)
	}
	return r.JSON(a.t)["secret"].(string)
}

func TestDownloadRoutesAbsentWithoutTheService(t *testing.T) {
	a := newApp(t, nil)
	owner := a.owner()
	for _, p := range []string{"/api/v1/downloads", "/api/v1/downloads/summary", "/api/v1/clients"} {
		if r := owner.send("GET", p, nil); r.Status != 404 || r.errCode(t) != "not_found" {
			t.Errorf("GET %s = %d %s", p, r.Status, r.Body)
		}
	}
}

// ---- clients -----------------------------------------------------------------

func TestClientAPIMasksSecretsEverywhere(t *testing.T) {
	a := newDLApp(t)
	owner := a.owner()
	const password = "a long qbittorrent password"
	const apiKey = "0123456789abcdef0123456789abcdef"

	cr := owner.do("POST", "/api/v1/clients", map[string]any{
		"name": "SAB", "kind": "sabnzbd", "url": "http://sab.invalid:8080/", "apiKey": apiKey,
		"password": password, "category": "films", "savePath": `M:\dl`, "priority": 3,
	})
	if cr.Status != 201 {
		t.Fatalf("create = %d %s", cr.Status, cr.Body)
	}
	if strings.Contains(string(cr.Body), apiKey) || strings.Contains(string(cr.Body), password) {
		t.Fatalf("the create response leaks a secret: %s", cr.Body)
	}
	c := cr.JSON(t)["client"].(map[string]any)
	if c["apiKey"] != "••••cdef" || c["password"] != "••••word" || c["url"] != "http://sab.invalid:8080" ||
		c["kind"] != "sabnzbd" || c["category"] != "films" || c["savePath"] != `M:\dl` || c["priority"] != 3.0 ||
		c["enabled"] != true || c["lastOkAt"] != nil || c["lastError"] != "" {
		t.Errorf("client = %v", c)
	}
	id := itoa(int64(c["id"].(float64)))

	list := owner.send("GET", "/api/v1/clients", nil)
	if list.Status != 200 || strings.Contains(string(list.Body), apiKey) || strings.Contains(string(list.Body), password) {
		t.Fatalf("list = %d, leaks: %s", list.Status, list.Body)
	}
	if clients := list.JSON(t)["clients"].([]any); len(clients) != 1 {
		t.Errorf("clients = %v", clients)
	}

	// Only sealed text is in the database.
	var sealedPW, sealedKey string
	if err := a.mainDB.QueryRow(`SELECT password_sealed, api_key_sealed FROM clients`).Scan(&sealedPW, &sealedKey); err != nil {
		t.Fatal(err)
	}
	if sealedPW == "" || strings.Contains(sealedPW, password) || strings.Contains(sealedKey, apiKey) {
		t.Errorf("stored = %q %q", sealedPW, sealedKey)
	}

	// The UI sends the whole form back with the masks in it: the secrets stay.
	put := owner.do("PUT", "/api/v1/clients/"+id, map[string]any{
		"name": "SAB main", "url": "http://sab.invalid:8080", "apiKey": c["apiKey"], "password": c["password"], "enabled": false,
	})
	if put.Status != 200 || strings.Contains(string(put.Body), apiKey) {
		t.Fatalf("put = %d %s", put.Status, put.Body)
	}
	pc := put.JSON(t)["client"].(map[string]any)
	if pc["name"] != "SAB main" || pc["enabled"] != false || pc["apiKey"] != "••••cdef" || pc["category"] != "films" {
		t.Errorf("after put = %v", pc)
	}
	var afterPW, afterKey string
	_ = a.mainDB.QueryRow(`SELECT password_sealed, api_key_sealed FROM clients`).Scan(&afterPW, &afterKey)
	if afterPW != sealedPW || afterKey != sealedKey {
		t.Error("sending the masks back changed the stored secrets")
	}
	// An empty secret clears it; a new one replaces it.
	put = owner.do("PUT", "/api/v1/clients/"+id, map[string]any{"password": ""})
	if put.Status != 200 || put.JSON(t)["client"].(map[string]any)["password"] != "" {
		t.Errorf("clear = %d %s", put.Status, put.Body)
	}
	// Changing the address with the saved key kept is refused.
	if r := owner.do("PUT", "/api/v1/clients/"+id, map[string]any{"url": "http://elsewhere.invalid:8080"}); r.Status != 400 || r.errCode(t) != "invalid_request" {
		t.Errorf("moving a saved key = %d %s", r.Status, r.Body)
	}
	// Nor can the secrets be read back through the settings API or an events.
	if r := owner.send("GET", "/api/v1/settings/downloads", nil); strings.Contains(string(r.Body), apiKey) {
		t.Error("settings API shows a client secret")
	}
}

func TestClientAPIErrors(t *testing.T) {
	a := newDLApp(t)
	owner := a.owner()
	q := newDLQBT(t)
	id := a.addClient(owner, q, "qb")

	cases := []struct {
		name         string
		method, path string
		body         any
		status       int
		code         string
	}{
		{"bad kind", "POST", "/api/v1/clients", map[string]any{"name": "x", "kind": "plex", "url": "http://x:1"}, 400, "invalid_request"},
		{"transmission is not there yet", "POST", "/api/v1/clients", map[string]any{"name": "x", "kind": "transmission", "url": "http://x:1"}, 400, "invalid_request"},
		{"bad url", "POST", "/api/v1/clients", map[string]any{"name": "x", "kind": "qbittorrent", "url": "nope"}, 400, "invalid_request"},
		{"sab needs a key", "POST", "/api/v1/clients", map[string]any{"name": "x", "kind": "sabnzbd", "url": "http://x:1"}, 400, "invalid_request"},
		{"duplicate name", "POST", "/api/v1/clients", map[string]any{"name": "QB", "kind": "deluge", "url": "http://x:1"}, 409, "name_taken"},
		{"no body", "POST", "/api/v1/clients", nil, 400, "invalid_json"},
		{"kind change", "PUT", "/api/v1/clients/" + itoa(id), map[string]any{"kind": "deluge"}, 400, "invalid_request"},
		{"unknown client", "PUT", "/api/v1/clients/999", map[string]any{"name": "x"}, 404, "not_found"},
		{"bad id", "PUT", "/api/v1/clients/abc", map[string]any{"name": "x"}, 404, "not_found"},
		{"delete unknown", "DELETE", "/api/v1/clients/999", nil, 404, "not_found"},
		{"test unknown", "POST", "/api/v1/clients/999/test", nil, 404, "not_found"},
	}
	for _, c := range cases {
		r := owner.do(c.method, c.path, c.body)
		if r.Status != c.status || r.errCode(t) != c.code {
			t.Errorf("%s: %s %s = %d %s, want %d %s", c.name, c.method, c.path, r.Status, r.Body, c.status, c.code)
		}
	}

	// A client with a download in progress cannot be deleted; with none, it can.
	a.grab(a.addTitle("Metropolis", 1927), hashA)
	if r := owner.do("DELETE", "/api/v1/clients/"+itoa(id), nil); r.Status != 409 || r.errCode(t) != "client_in_use" {
		t.Errorf("delete in use = %d %s", r.Status, r.Body)
	}
	other := a.addClient(owner, q, "spare")
	if r := owner.do("DELETE", "/api/v1/clients/"+itoa(other), nil); r.Status != 204 {
		t.Errorf("delete spare = %d %s", r.Status, r.Body)
	}
}

func TestClientTestEndpoint(t *testing.T) {
	a := newDLApp(t)
	owner := a.owner()
	q := newDLQBT(t)
	id := a.addClient(owner, q, "qb")

	r := owner.do("POST", "/api/v1/clients/"+itoa(id)+"/test", nil)
	if r.Status != 200 {
		t.Fatalf("test = %d %s", r.Status, r.Body)
	}
	if got := r.JSON(t); got["ok"] != true || got["version"] != "v5.0.1" || got["error"] != "" {
		t.Errorf("test result = %v", got)
	}
	list := owner.send("GET", "/api/v1/clients", nil).JSON(t)["clients"].([]any)
	if c := list[0].(map[string]any); c["lastOkAt"] == nil || c["lastError"] != "" {
		t.Errorf("after a good test: %v", c)
	}

	// The client goes away: still a 200, with ok false and words that help.
	q.srv.Close()
	r = owner.do("POST", "/api/v1/clients/"+itoa(id)+"/test", nil)
	got := r.JSON(t)
	if r.Status != 200 || got["ok"] != false || !strings.HasPrefix(got["error"].(string), "Marquee could not reach the client.") ||
		strings.Contains(string(r.Body), "a long qbittorrent password") {
		t.Errorf("failed test = %d %s", r.Status, r.Body)
	}
	list = owner.send("GET", "/api/v1/clients", nil).JSON(t)["clients"].([]any)
	if c := list[0].(map[string]any); c["lastError"] == "" || c["lastErrorAt"] == nil {
		t.Errorf("after a bad test: %v", c)
	}
}

// ---- downloads ---------------------------------------------------------------

func TestDownloadListShape(t *testing.T) {
	a := newDLApp(t)
	owner := a.owner()
	q := newDLQBT(t)
	cid := a.addClient(owner, q, "qb")
	title := a.addTitle("Metropolis", 1927)
	id := a.grab(title, hashA)

	r := owner.send("GET", "/api/v1/downloads", nil)
	if r.Status != 200 {
		t.Fatalf("list = %d %s", r.Status, r.Body)
	}
	body := r.JSON(t)
	if body["total"] != 1.0 || body["limit"] != 50.0 || body["offset"] != 0.0 {
		t.Errorf("paging = %v", body)
	}
	item := body["downloads"].([]any)[0].(map[string]any)
	wantTitle := map[string]any{"id": float64(title), "name": "Metropolis", "year": 1927.0, "kind": "movie", "posterRef": "tmdb/w500/poster.jpg"}
	gotTitle := item["title"].(map[string]any)
	for k, v := range wantTitle {
		if gotTitle[k] != v {
			t.Errorf("title.%s = %v, want %v", k, gotTitle[k], v)
		}
	}
	client := item["client"].(map[string]any)
	if client["id"] != float64(cid) || client["name"] != "qb" || client["kind"] != "qbittorrent" {
		t.Errorf("client = %v", client)
	}
	for k, v := range map[string]any{
		"id": float64(id), "state": "queued", "progress": 0.0, "speedBps": 0.0, "etaSeconds": nil, "size": float64(1 << 30),
		"quality": "hd", "releaseTitle": "Metropolis.1927.1080p", "protocol": "torrent", "kind": "movie",
		"completedAt": nil, "error": "", "copyId": 4.0,
	} {
		if item[k] != v {
			t.Errorf("%s = %v (%T), want %v", k, item[k], item[k], v)
		}
	}
	for _, k := range []string{"createdAt", "updatedAt"} {
		if s, _ := item[k].(string); !strings.HasSuffix(s, "Z") {
			t.Errorf("%s = %v", k, item[k])
		}
	}
	if eps, ok := item["episodeIds"].([]any); !ok || len(eps) != 0 {
		t.Errorf("episodeIds = %v", item["episodeIds"])
	}

	// Filters and paging.
	if b := owner.send("GET", "/api/v1/downloads?state=history", nil).JSON(t); b["total"] != 0.0 || len(b["downloads"].([]any)) != 0 {
		t.Errorf("history = %v", b)
	}
	if b := owner.send("GET", "/api/v1/downloads?state=all&limit=1&offset=1", nil).JSON(t); b["total"] != 1.0 || len(b["downloads"].([]any)) != 0 || b["limit"] != 1.0 || b["offset"] != 1.0 {
		t.Errorf("all, page 2 = %v", b)
	}
	for _, bad := range []string{"state=sideways", "limit=0", "limit=201", "limit=x", "offset=-1"} {
		if r := owner.send("GET", "/api/v1/downloads?"+bad, nil); r.Status != 400 || r.errCode(t) != "invalid_request" {
			t.Errorf("?%s = %d %s", bad, r.Status, r.Body)
		}
	}
}

func TestDownloadPauseResumeDelete(t *testing.T) {
	a := newDLApp(t)
	owner := a.owner()
	q := newDLQBT(t)
	a.addClient(owner, q, "qb")
	id := a.grab(a.addTitle("Metropolis", 1927), hashA)
	base := "/api/v1/downloads/" + itoa(id)

	r := owner.do("POST", base+"/pause", nil)
	if r.Status != 200 || r.JSON(t)["download"].(map[string]any)["state"] != "paused" {
		t.Fatalf("pause = %d %s", r.Status, r.Body)
	}
	if r := owner.do("POST", base+"/pause", nil); r.Status != 409 || r.errCode(t) != "invalid_state" {
		t.Errorf("pause twice = %d %s", r.Status, r.Body)
	}
	r = owner.do("POST", base+"/resume", nil)
	if r.Status != 200 || r.JSON(t)["download"].(map[string]any)["state"] != "queued" {
		t.Fatalf("resume = %d %s", r.Status, r.Body)
	}
	if r := owner.do("POST", base+"/resume", nil); r.Status != 409 || r.errCode(t) != "invalid_state" {
		t.Errorf("resume a running one = %d %s", r.Status, r.Body)
	}

	if r := owner.do("DELETE", base+"?deleteData=maybe", nil); r.Status != 400 || r.errCode(t) != "invalid_request" {
		t.Errorf("bad deleteData = %d %s", r.Status, r.Body)
	}
	if r := owner.do("DELETE", base+"?deleteData=true", nil); r.Status != 204 {
		t.Fatalf("delete = %d %s", r.Status, r.Body)
	}
	if got := strings.Join(q.commands(), ","); got != "stop:"+hashA+",start:"+hashA+",delete:"+hashA+":true" {
		t.Errorf("commands the client saw = %s", got)
	}
	// It stays in the history as removed; deleting again is quiet.
	h := owner.send("GET", "/api/v1/downloads?state=history", nil).JSON(t)["downloads"].([]any)
	if len(h) != 1 || h[0].(map[string]any)["state"] != "removed" {
		t.Errorf("history = %v", h)
	}
	if r := owner.do("DELETE", base, nil); r.Status != 204 {
		t.Errorf("delete twice = %d %s", r.Status, r.Body)
	}
	if a := owner.send("GET", "/api/v1/downloads", nil).JSON(t); a["total"] != 0.0 {
		t.Errorf("active after delete = %v", a)
	}

	for _, rt := range []struct{ method, path string }{
		{"POST", "/api/v1/downloads/999/pause"}, {"POST", "/api/v1/downloads/999/resume"}, {"DELETE", "/api/v1/downloads/999"},
		{"POST", "/api/v1/downloads/abc/pause"}, {"DELETE", "/api/v1/downloads/0"},
	} {
		if r := owner.do(rt.method, rt.path, nil); r.Status != 404 || r.errCode(t) != "not_found" {
			t.Errorf("%s %s = %d %s", rt.method, rt.path, r.Status, r.Body)
		}
	}
}

func TestDownloadActionsWhenTheClientIsDown(t *testing.T) {
	a := newDLApp(t)
	owner := a.owner()
	q := newDLQBT(t)
	a.addClient(owner, q, "qb")
	id := a.grab(a.addTitle("Metropolis", 1927), hashA)
	q.srv.Close()

	for _, rt := range []struct{ method, path string }{
		{"POST", "/api/v1/downloads/" + itoa(id) + "/pause"},
		{"DELETE", "/api/v1/downloads/" + itoa(id)},
	} {
		if r := owner.do(rt.method, rt.path, nil); r.Status != 502 || r.errCode(t) != "client_unreachable" {
			t.Errorf("%s %s = %d %s", rt.method, rt.path, r.Status, r.Body)
		}
	}
	// And nothing changed.
	d := owner.send("GET", "/api/v1/downloads", nil).JSON(t)["downloads"].([]any)[0].(map[string]any)
	if d["state"] != "queued" {
		t.Errorf("state = %v", d["state"])
	}
}

func TestDownloadSummary(t *testing.T) {
	a := newDLApp(t)
	owner := a.owner()
	q := newDLQBT(t)
	cid := a.addClient(owner, q, "qb")
	a.grab(a.addTitle("Metropolis", 1927), hashA)
	a.grab(a.addTitle("Nosferatu", 1922), strings.Repeat("a", 40))

	if err := a.dl.Poll(context.Background()); err != nil {
		t.Fatal(err)
	}
	r := owner.send("GET", "/api/v1/downloads/summary", nil)
	if r.Status != 200 {
		t.Fatalf("summary = %d %s", r.Status, r.Body)
	}
	s := r.JSON(t)
	if s["queued"] != 2.0 || s["downloading"] != 0.0 || s["importing"] != 0.0 {
		t.Errorf("counts = %v", s)
	}
	clients := s["clients"].([]any)
	if len(clients) != 1 {
		t.Fatalf("clients = %v", clients)
	}
	c := clients[0].(map[string]any)
	if c["id"] != float64(cid) || c["name"] != "qb" || c["kind"] != "qbittorrent" || c["ok"] != true || c["lastError"] != "" {
		t.Errorf("client health = %v", c)
	}

	// The client stops answering: the summary says so.
	q.srv.Close()
	if err := a.dl.Poll(context.Background()); err != nil {
		t.Fatal(err)
	}
	c = owner.send("GET", "/api/v1/downloads/summary", nil).JSON(t)["clients"].([]any)[0].(map[string]any)
	if c["ok"] != false || c["lastError"] == "" {
		t.Errorf("health after the client went down = %v", c)
	}
}
