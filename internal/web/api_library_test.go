package web

import (
	"context"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/BaileyPillon/marquee/internal/accounts"
	"github.com/BaileyPillon/marquee/internal/core/db"
	"github.com/BaileyPillon/marquee/internal/core/events"
	"github.com/BaileyPillon/marquee/internal/core/secrets"
	"github.com/BaileyPillon/marquee/internal/library"
	"github.com/BaileyPillon/marquee/internal/settings"
)

// ---- test rig --------------------------------------------------------------

// libFakeTitles is a library.TitleSource with a few fixed answers.
type libFakeTitles struct{}

func (libFakeTitles) Candidates(_ context.Context, kind string) ([]library.Candidate, error) {
	switch kind {
	case library.CandidateMovie:
		return []library.Candidate{{ID: 1, Kind: "movie", Name: "Metropolis", Year: 1927}}, nil
	case library.CandidateShow:
		return []library.Candidate{{ID: 20, Kind: "show", Name: "Pioneer One", Year: 2010}}, nil
	}
	return nil, nil
}

func (libFakeTitles) EpisodeID(_ context.Context, titleID int64, season, number int) (int64, bool, error) {
	if titleID == 20 && season == 1 && number == 1 {
		return 201, true, nil
	}
	return 0, false, nil
}

// libApp is a whole Marquee server with a library on a temp database.
type libApp struct {
	*app
	lib *library.Service
}

func newLibApp(t *testing.T) *libApp {
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
	lib := library.NewService(mainDB, library.Options{Logger: log, Hub: hub})
	t.Cleanup(lib.Close)
	lib.SetTitleSource(libFakeTitles{})
	s := New(Deps{
		Version:  "9.9.9-test",
		Log:      log,
		MainDB:   mainDB,
		EventsDB: eventsDB,
		Accounts: accounts.NewService(mainDB, accounts.Options{Logger: log, PasswordParams: lightParams}),
		Settings: cfg,
		Hub:      hub,
		Library:  lib,
		UI:       testUI(),
	})
	srv := httptest.NewServer(s.Handler())
	t.Cleanup(srv.Close)
	return &libApp{app: &app{t: t, srv: srv, mainDB: mainDB, settings: cfg, hub: hub}, lib: lib}
}

// admin creates an admin through an invite and returns a signed-in client.
func (a *libApp) admin(owner *client) *client {
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

// people returns an owner, an admin, a member and a signed-out client.
func (a *libApp) people() (owner, admin, member, anon *client) {
	owner = a.owner()
	return owner, a.admin(owner), a.member(owner, "Mia"), a.newClient()
}

// mediaDir makes a folder with empty files in it, each with a fixed time.
func mediaDir(t *testing.T, rels ...string) string {
	t.Helper()
	root := t.TempDir()
	when := time.Date(2020, 1, 2, 3, 4, 5, 0, time.UTC)
	for _, rel := range rels {
		p := filepath.Join(root, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, nil, 0o644); err != nil {
			t.Fatal(err)
		}
		if err := os.Chtimes(p, when, when); err != nil {
			t.Fatal(err)
		}
	}
	return root
}

func addFolder(t *testing.T, c *client, kind, path, label string) reply {
	t.Helper()
	return c.do("POST", "/api/v1/library/folders", map[string]string{"kind": kind, "path": path, "label": label})
}

func folderID(t *testing.T, r reply) int64 {
	t.Helper()
	if r.Status != http.StatusCreated {
		t.Fatalf("add folder = %d %s", r.Status, r.Body)
	}
	f := r.JSON(t)["folder"].(map[string]any)
	return int64(f["id"].(float64))
}

// waitForFiles polls the folder list until the folder has n files and no scan is running.
func waitForFiles(t *testing.T, c *client, id int64, n int) map[string]any {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for {
		r := c.do("GET", "/api/v1/library/folders", nil)
		if r.Status != 200 {
			t.Fatalf("folders = %d %s", r.Status, r.Body)
		}
		for _, f := range r.JSON(t)["folders"].([]any) {
			fm := f.(map[string]any)
			if int64(fm["id"].(float64)) == id && int(fm["files"].(float64)) == n && fm["scanning"] == false {
				return fm
			}
		}
		if time.Now().After(deadline) {
			t.Fatalf("folder %d never reached %d files: %s", id, n, r.Body)
		}
		time.Sleep(20 * time.Millisecond)
	}
}

// ---- tests -----------------------------------------------------------------

func TestLibraryRoutesAreAbsentWithoutALibrary(t *testing.T) {
	a := newApp(t, nil) // the shared rig has no library
	owner := a.owner()
	for _, path := range []string{"/api/v1/library/folders", "/api/v1/library/files", "/api/v1/library/stats"} {
		if r := owner.do("GET", path, nil); r.Status != 404 || r.errCode(t) != "not_found" {
			t.Errorf("GET %s = %d %s", path, r.Status, r.Body)
		}
	}
}

func TestLibraryNeedsASignIn(t *testing.T) {
	a := newLibApp(t)
	anon := a.newClient()
	for _, c := range []struct{ method, path string }{
		{"GET", "/api/v1/library/folders"},
		{"POST", "/api/v1/library/folders"},
		{"DELETE", "/api/v1/library/folders/1"},
		{"POST", "/api/v1/library/scan"},
		{"GET", "/api/v1/library/files"},
		{"GET", "/api/v1/library/stats"},
	} {
		r := anon.do(c.method, c.path, map[string]string{})
		if r.Status != http.StatusUnauthorized || r.errCode(t) != "unauthorized" {
			t.Errorf("%s %s signed out = %d %s", c.method, c.path, r.Status, r.Body)
		}
	}
	// Mutations also want the CSRF header, as everywhere else.
	owner := a.owner()
	if r := owner.send("POST", "/api/v1/library/folders", map[string]string{"kind": "movie", "path": t.TempDir()}); r.Status != 403 || r.errCode(t) != "csrf" {
		t.Errorf("without X-Marquee = %d %s", r.Status, r.Body)
	}
}

func TestLibraryPermissions(t *testing.T) {
	a := newLibApp(t)
	owner, admin, member, _ := a.people()
	dir := mediaDir(t, "Metropolis (1927)/Metropolis (1927).mkv")
	spare := t.TempDir()

	// A folder to read, scan and delete.
	id := folderID(t, addFolder(t, owner, "movie", dir, "Films"))
	idPath := "/api/v1/library/folders/" + strconv.FormatInt(id, 10)

	// Who may do what, as HTTP statuses for owner, admin and member.
	checks := []struct {
		name                 string
		method, path         string
		body                 any
		owner, admin, member int
	}{
		{"list folders", "GET", "/api/v1/library/folders", nil, 200, 200, 200},
		{"stats", "GET", "/api/v1/library/stats", nil, 200, 200, 200},
		{"list files", "GET", "/api/v1/library/files", nil, 200, 200, 403},
		{"scan", "POST", "/api/v1/library/scan", map[string]any{"folderId": id}, 202, 202, 403},
		{"add folder", "POST", "/api/v1/library/folders", map[string]string{"kind": "show", "path": spare}, 201, 403, 403},
		{"remove folder", "DELETE", idPath, nil, 204, 403, 403},
	}
	// Admin and member first: the owner's calls change things (the last two
	// remove the folder the others use).
	for _, c := range checks {
		for _, who := range []struct {
			name   string
			c      *client
			status int
		}{{"admin", admin, c.admin}, {"member", member, c.member}} {
			r := who.c.do(c.method, c.path, c.body)
			if r.Status != who.status {
				t.Errorf("%s as %s = %d, want %d: %s", c.name, who.name, r.Status, who.status, r.Body)
			}
			if who.status == 403 && r.errCode(t) != "forbidden" {
				t.Errorf("%s as %s: code %q, want forbidden", c.name, who.name, r.errCode(t))
			}
		}
	}
	waitForFiles(t, owner, id, 1) // the scan the admin started
	for _, c := range checks {
		r := owner.do(c.method, c.path, c.body)
		if r.Status != c.owner {
			t.Errorf("%s as owner = %d, want %d: %s", c.name, r.Status, c.owner, r.Body)
		}
		if c.name == "scan" {
			waitForFiles(t, owner, id, 1)
		}
	}
}

func TestLibraryFolderLifecycle(t *testing.T) {
	a := newLibApp(t)
	owner := a.owner()
	movies := mediaDir(t, "Metropolis (1927)/Metropolis (1927).mkv", "Nosferatu (1922)/Nosferatu (1922).mkv")
	shows := mediaDir(t, "Pioneer One (2010)/Season 1/Pioneer One - S01E01.mkv")

	if r := owner.do("GET", "/api/v1/library/folders", nil); r.Status != 200 || len(r.JSON(t)["folders"].([]any)) != 0 {
		t.Fatalf("empty list = %d %s", r.Status, r.Body)
	}

	r := addFolder(t, owner, "movie", movies, "Open movies")
	id := folderID(t, r)
	f := r.JSON(t)["folder"].(map[string]any)
	if f["kind"] != "movie" || f["path"] != movies || f["label"] != "Open movies" || f["files"] != float64(0) ||
		f["scanning"] != false || f["createdAt"] == "" {
		t.Errorf("created folder = %v", f)
	}
	if free, _ := f["freeBytes"].(float64); free <= 0 {
		t.Errorf("freeBytes = %v, want a positive number", f["freeBytes"])
	}
	if total, _ := f["totalBytes"].(float64); total <= 0 {
		t.Errorf("totalBytes = %v", f["totalBytes"])
	}
	// The label defaults to the folder's name.
	showID := folderID(t, addFolder(t, owner, "show", shows, ""))
	if f := owner.do("GET", "/api/v1/library/folders", nil).JSON(t)["folders"].([]any); len(f) != 2 ||
		f[1].(map[string]any)["label"] != filepath.Base(shows) {
		t.Errorf("folders = %v", f)
	}

	// Rejections, each with its own stable code.
	file := filepath.Join(movies, "Metropolis (1927)", "Metropolis (1927).mkv")
	for _, c := range []struct {
		name         string
		kind, path   string
		status       int
		wantErrorKey string
	}{
		{"bad kind", "game", t.TempDir(), 400, "invalid_kind"},
		{"relative", "movie", "media/movies", 400, "invalid_path"},
		{"empty", "movie", "", 400, "invalid_path"},
		{"missing", "movie", filepath.Join(movies, "nowhere"), 400, "path_not_found"},
		{"a file", "movie", file, 400, "not_a_folder"},
		{"duplicate", "movie", movies, 409, "folder_exists"},
		{"inside another", "movie", filepath.Join(movies, "Metropolis (1927)"), 409, "folder_nested"},
		{"contains another", "movie", filepath.Dir(movies), 409, "folder_nested"},
	} {
		r := addFolder(t, owner, c.kind, c.path, "")
		if r.Status != c.status || r.errCode(t) != c.wantErrorKey {
			t.Errorf("%s = %d %s, want %d %s", c.name, r.Status, r.Body, c.status, c.wantErrorKey)
		}
	}
	if r := owner.do("POST", "/api/v1/library/folders", "not an object"); r.Status != 400 || r.errCode(t) != "invalid_json" {
		t.Errorf("bad body = %d %s", r.Status, r.Body)
	}

	// Delete: rows go, files stay, and a second delete is a 404.
	if r := owner.do("POST", "/api/v1/library/scan", map[string]any{"folderId": id}); r.Status != 202 {
		t.Fatal(r.Status, string(r.Body))
	}
	waitForFiles(t, owner, id, 2)
	del := "/api/v1/library/folders/" + strconv.FormatInt(id, 10)
	if r := owner.do("DELETE", del, nil); r.Status != 204 || len(r.Body) != 0 {
		t.Fatalf("delete = %d %s", r.Status, r.Body)
	}
	if _, err := os.Stat(file); err != nil {
		t.Errorf("deleting a library folder removed a file: %v", err)
	}
	if r := owner.do("DELETE", del, nil); r.Status != 404 || r.errCode(t) != "not_found" {
		t.Errorf("second delete = %d %s", r.Status, r.Body)
	}
	for _, bad := range []string{"/api/v1/library/folders/abc", "/api/v1/library/folders/9999"} {
		if r := owner.do("DELETE", bad, nil); r.Status != 404 {
			t.Errorf("DELETE %s = %d", bad, r.Status)
		}
	}
	if r := owner.do("GET", "/api/v1/library/files?folderId="+strconv.FormatInt(id, 10), nil); r.Status != 200 ||
		r.JSON(t)["total"] != float64(0) {
		t.Errorf("rows of a removed folder: %d %s", r.Status, r.Body)
	}
	if got := owner.do("GET", "/api/v1/library/folders", nil).JSON(t)["folders"].([]any); len(got) != 1 ||
		int64(got[0].(map[string]any)["id"].(float64)) != showID {
		t.Errorf("folders after delete = %v", got)
	}
}

func TestLibraryScanFilesAndStats(t *testing.T) {
	a := newLibApp(t)
	owner := a.owner()
	movies := mediaDir(t,
		"Metropolis (1927)/Metropolis (1927).mkv",
		"Nosferatu (1922)/Nosferatu (1922).mkv",
		"Sintel (2010)/Sintel (2010).mkv",
		"Metropolis (1927)/Extras/Making of.mkv", // skipped
	)
	shows := mediaDir(t,
		"Pioneer One (2010)/Season 1/Pioneer One - S01E01.mkv",
		"Pioneer One (2010)/Season 1/Pioneer One - S01E02.mkv",
	)
	movieID := folderID(t, addFolder(t, owner, "movie", movies, ""))
	showID := folderID(t, addFolder(t, owner, "show", shows, ""))

	// Nothing is scanned until asked.
	if r := owner.do("GET", "/api/v1/library/files", nil); r.JSON(t)["total"] != float64(0) {
		t.Fatalf("files before a scan = %s", r.Body)
	}

	// An empty body scans every folder; the answer comes back at once with 202.
	r := owner.do("POST", "/api/v1/library/scan", nil)
	if r.Status != http.StatusAccepted {
		t.Fatalf("scan = %d %s", r.Status, r.Body)
	}
	body := r.JSON(t)
	if started := body["started"].([]any); len(started) != 2 || len(body["running"].([]any)) != 0 {
		t.Errorf("scan body = %v", body)
	}
	waitForFiles(t, owner, movieID, 3)
	waitForFiles(t, owner, showID, 2)

	// Files: unmatched first (Nosferatu and Sintel are not in the fake catalog).
	r = owner.do("GET", "/api/v1/library/files?state=unmatched", nil)
	if r.Status != 200 {
		t.Fatalf("files = %d %s", r.Status, r.Body)
	}
	list := r.JSON(t)
	files := list["files"].([]any)
	if list["total"] != float64(3) || len(files) != 3 || list["limit"] != float64(100) || list["offset"] != float64(0) {
		t.Fatalf("unmatched files = %v", list)
	}
	first := files[0].(map[string]any)
	for _, k := range []string{"id", "folderId", "relPath", "size", "mtime", "kind", "parsed", "quality", "titleId", "episodeIds", "trackId", "state", "addedAt", "missingSince"} {
		if _, ok := first[k]; !ok {
			t.Errorf("file lacks %q: %v", k, first)
		}
	}
	if first["state"] != "unmatched" || first["titleId"] != nil || first["missingSince"] != nil || first["mtime"] != "2020-01-02T03:04:05Z" {
		t.Errorf("first file = %v", first)
	}

	// Matched: the movie and the first episode, whose ids come from the catalog.
	r = owner.do("GET", "/api/v1/library/files?state=matched", nil)
	matched := r.JSON(t)["files"].([]any)
	if len(matched) != 2 {
		t.Fatalf("matched = %s", r.Body)
	}
	byPath := map[string]map[string]any{}
	for _, f := range matched {
		fm := f.(map[string]any)
		byPath[fm["relPath"].(string)] = fm
	}
	metropolis := byPath["Metropolis (1927)/Metropolis (1927).mkv"]
	episode := byPath["Pioneer One (2010)/Season 1/Pioneer One - S01E01.mkv"]
	if metropolis == nil || metropolis["titleId"] != float64(1) || metropolis["episodeIds"] != nil {
		t.Errorf("movie row = %v", metropolis)
	}
	if ids, _ := episode["episodeIds"].([]any); episode == nil || episode["titleId"] != float64(20) || len(ids) != 1 || ids[0] != float64(201) {
		t.Errorf("episode row = %v", episode)
	}
	// The second episode is not in the catalog, so it stays unmatched.
	if r := owner.do("GET", "/api/v1/library/files?state=unmatched&folderId="+strconv.FormatInt(showID, 10), nil); r.JSON(t)["total"] != float64(1) {
		t.Errorf("show unmatched = %s", r.Body)
	}

	// Filters and paging.
	if r := owner.do("GET", "/api/v1/library/files?folderId="+strconv.FormatInt(movieID, 10), nil); r.JSON(t)["total"] != float64(3) {
		t.Errorf("movie folder = %s", r.Body)
	}
	r = owner.do("GET", "/api/v1/library/files?limit=2&offset=3", nil)
	if p := r.JSON(t); p["total"] != float64(5) || len(p["files"].([]any)) != 2 || p["limit"] != float64(2) || p["offset"] != float64(3) {
		t.Errorf("page = %s", r.Body)
	}
	for _, q := range []string{"state=bogus", "limit=0", "limit=501", "limit=abc", "offset=-1", "folderId=x", "folderId=-3"} {
		if r := owner.do("GET", "/api/v1/library/files?"+q, nil); r.Status != 400 || r.errCode(t) != "invalid_request" {
			t.Errorf("?%s = %d %s", q, r.Status, r.Body)
		}
	}

	// Stats.
	r = owner.do("GET", "/api/v1/library/stats", nil)
	if r.Status != 200 {
		t.Fatalf("stats = %d %s", r.Status, r.Body)
	}
	st := r.JSON(t)
	kinds := st["kinds"].(map[string]any)
	movie, show, music := kinds["movie"].(map[string]any), kinds["show"].(map[string]any), kinds["music"].(map[string]any)
	states := st["states"].(map[string]any)
	if st["files"] != float64(5) || st["missing"] != float64(0) || st["bytes"] != float64(0) ||
		states["matched"] != float64(2) || states["unmatched"] != float64(3) || states["ignored"] != float64(0) ||
		movie["files"] != float64(3) || movie["matched"] != float64(1) || movie["unmatched"] != float64(2) ||
		show["files"] != float64(2) || show["matched"] != float64(1) || music["files"] != float64(0) {
		t.Errorf("stats = %s", r.Body)
	}

	// A change on disk shows up after the next scan, and a vanished file is counted as missing.
	if err := os.WriteFile(filepath.Join(movies, "Sintel (2010)", "Sintel (2010).mkv"), []byte("12345"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(filepath.Join(movies, "Nosferatu (1922)", "Nosferatu (1922).mkv")); err != nil {
		t.Fatal(err)
	}
	if r := owner.do("POST", "/api/v1/library/scan", map[string]any{"folderId": movieID}); r.Status != 202 ||
		len(r.JSON(t)["started"].([]any)) != 1 {
		t.Fatalf("scan = %d %s", r.Status, r.Body)
	}
	deadline := time.Now().Add(10 * time.Second)
	for {
		st = owner.do("GET", "/api/v1/library/stats", nil).JSON(t)
		if st["missing"] == float64(1) {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("the missing file never showed up: %v", st)
		}
		time.Sleep(20 * time.Millisecond)
	}
	if st["bytes"] != float64(5) || st["files"] != float64(4) {
		t.Errorf("stats after a change = %v", st)
	}
	if r := owner.do("GET", "/api/v1/library/files?state=missing", nil); r.JSON(t)["total"] != float64(1) {
		t.Errorf("missing files = %s", r.Body)
	}
}

func TestLibraryScanArguments(t *testing.T) {
	a := newLibApp(t)
	owner := a.owner()
	dir := mediaDir(t, "Metropolis (1927)/Metropolis (1927).mkv")
	id := folderID(t, addFolder(t, owner, "movie", dir, ""))

	if r := owner.do("POST", "/api/v1/library/scan", map[string]any{"folderId": 9999}); r.Status != 404 || r.errCode(t) != "not_found" {
		t.Errorf("unknown folder = %d %s", r.Status, r.Body)
	}
	for _, bad := range []any{map[string]any{"folderId": 0}, map[string]any{"folderId": -1}} {
		if r := owner.do("POST", "/api/v1/library/scan", bad); r.Status != 400 || r.errCode(t) != "invalid_request" {
			t.Errorf("%v = %d %s", bad, r.Status, r.Body)
		}
	}
	if r := owner.do("POST", "/api/v1/library/scan", "folderId"); r.Status != 400 || r.errCode(t) != "invalid_json" {
		t.Errorf("not an object = %d %s", r.Status, r.Body)
	}
	// An empty object means everything, like no body at all.
	if r := owner.do("POST", "/api/v1/library/scan", map[string]any{}); r.Status != 202 ||
		len(r.JSON(t)["started"].([]any)) != 1 {
		t.Errorf("empty object = %d %s", r.Status, r.Body)
	}
	waitForFiles(t, owner, id, 1)
	// With no folders at all there is nothing to start, and that is fine.
	b := newLibApp(t)
	o2 := b.owner()
	if r := o2.do("POST", "/api/v1/library/scan", nil); r.Status != 202 || len(r.JSON(t)["started"].([]any)) != 0 {
		t.Errorf("scan with no folders = %d %s", r.Status, r.Body)
	}
}

func TestLibraryAPIKeys(t *testing.T) {
	a := newLibApp(t)
	owner := a.owner()
	dir := mediaDir(t, "Metropolis (1927)/Metropolis (1927).mkv")
	id := folderID(t, addFolder(t, owner, "movie", dir, ""))

	mint := func(scopes ...string) string {
		t.Helper()
		r := owner.do("POST", "/api/v1/apikeys", map[string]any{"name": "test", "scopes": scopes})
		if r.Status != http.StatusCreated {
			t.Fatalf("mint = %d %s", r.Status, r.Body)
		}
		return r.JSON(t)["secret"].(string)
	}
	anon := a.newClient()
	do := func(secret, method, path string, body any) reply {
		return anon.send(method, path, body, "Authorization", "Bearer "+secret)
	}

	see := mint("see")
	if r := do(see, "GET", "/api/v1/library/folders", nil); r.Status != 200 {
		t.Errorf("a see key lists folders: %d %s", r.Status, r.Body)
	}
	if r := do(see, "GET", "/api/v1/library/stats", nil); r.Status != 200 {
		t.Errorf("a see key reads stats: %d %s", r.Status, r.Body)
	}
	for _, c := range []struct{ method, path string }{
		{"POST", "/api/v1/library/scan"}, {"GET", "/api/v1/library/files"},
	} {
		if r := do(see, c.method, c.path, map[string]any{}); r.Status != 403 || r.errCode(t) != "insufficient_scope" {
			t.Errorf("a see key on %s %s = %d %s", c.method, c.path, r.Status, r.Body)
		}
	}
	if r := do(see, "POST", "/api/v1/library/folders", map[string]string{"kind": "movie", "path": t.TempDir()}); r.Status != 403 {
		t.Errorf("a see key adds a folder: %d %s", r.Status, r.Body)
	}

	adminKey := mint("see", "admin")
	if r := do(adminKey, "POST", "/api/v1/library/scan", map[string]any{"folderId": id}); r.Status != 202 {
		t.Errorf("an admin key scans: %d %s", r.Status, r.Body)
	}
	waitForFiles(t, owner, id, 1)
	if r := do(adminKey, "GET", "/api/v1/library/files", nil); r.Status != 200 || r.JSON(t)["total"] != float64(1) {
		t.Errorf("an admin key lists files: %d %s", r.Status, r.Body)
	}
	if r := do(adminKey, "DELETE", "/api/v1/library/folders/"+strconv.FormatInt(id, 10), nil); r.Status != 403 {
		t.Errorf("an admin key removes a folder: %d %s", r.Status, r.Body)
	}

	settingsKey := mint("see", "settings")
	other := t.TempDir()
	if r := do(settingsKey, "POST", "/api/v1/library/folders", map[string]string{"kind": "show", "path": other}); r.Status != 201 {
		t.Errorf("a settings key adds a folder: %d %s", r.Status, r.Body)
	}
}

func TestLibraryScanProgressReachesTheEventStream(t *testing.T) {
	a := newLibApp(t)
	owner := a.owner()
	dir := mediaDir(t, "Metropolis (1927)/Metropolis (1927).mkv")
	id := folderID(t, addFolder(t, owner, "movie", dir, ""))

	// Subscribe before the scan; the hub is the same one the SSE endpoint serves.
	client, unsubscribe := a.hub.Subscribe("library.scan")
	defer unsubscribe()
	if r := owner.do("POST", "/api/v1/library/scan", map[string]any{"folderId": id}); r.Status != 202 {
		t.Fatal(r.Status, string(r.Body))
	}
	var seen []string
	deadline := time.After(10 * time.Second)
	for !contains(seen, `"state":"done"`) {
		select {
		case msg := <-client.C():
			seen = append(seen, string(msg.Data))
		case <-deadline:
			t.Fatalf("events so far: %v", seen)
		}
	}
	if !strings.Contains(seen[0], `"state":"started"`) || !strings.Contains(seen[len(seen)-1], `"folderId":`+strconv.FormatInt(id, 10)) {
		t.Errorf("events = %v", seen)
	}
}

func contains(list []string, sub string) bool {
	for _, s := range list {
		if strings.Contains(s, sub) {
			return true
		}
	}
	return false
}
