package web

import (
	"context"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/url"
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
	"github.com/BaileyPillon/marquee/internal/grab"
	"github.com/BaileyPillon/marquee/internal/library"
	"github.com/BaileyPillon/marquee/internal/metadata"
	"github.com/BaileyPillon/marquee/internal/requests"
	"github.com/BaileyPillon/marquee/internal/search"
	"github.com/BaileyPillon/marquee/internal/settings"
	"github.com/BaileyPillon/marquee/internal/sources"
	"github.com/BaileyPillon/marquee/internal/sources/sourcestest"
	"github.com/BaileyPillon/marquee/internal/subtitles"
	"github.com/BaileyPillon/marquee/internal/titles"
)

// ---- the rig: subtitles and music over HTTP -------------------------------------

const (
	m9Chopin    = "a1b2c3d4-0000-4000-8000-000000000001"
	m9Nocturnes = "b0000000-0000-4000-8000-000000000101"
	m9Etudes    = "b0000000-0000-4000-8000-000000000102"
	m9Srt       = "1\n00:00:01,000 --> 00:00:02,000\nHello\n\n"
)

type m9Artists struct{}

func (m9Artists) SearchArtists(context.Context, string) ([]metadata.SearchHit, error) {
	return nil, nil
}

func (m9Artists) Artist(_ context.Context, id string) (*metadata.ArtistInfo, error) {
	if id != m9Chopin {
		return nil, metadata.ErrNotFound
	}
	return &metadata.ArtistInfo{MBID: m9Chopin, Name: "Frédéric Chopin", SortName: "Chopin, Frédéric", Year: 1810, Ended: true,
		Albums: []metadata.AlbumInfo{
			{MBID: m9Nocturnes, Title: "Nocturnes", Year: 1998, Type: "album", Tracks: []metadata.TrackInfo{
				{Disc: 1, Position: 1, Title: "Nocturne One"}, {Disc: 1, Position: 2, Title: "Nocturne Two"}}},
			{MBID: m9Etudes, Title: "Études", Year: 1985, Type: "album", Tracks: []metadata.TrackInfo{{Disc: 1, Position: 1, Title: "Etude One"}}},
		}}, nil
}

// m9Provider is a subtitle source that always offers one good English subtitle.
type m9Provider struct {
	mu        sync.Mutex
	searches  int
	downloads int
	err       error
}

func (p *m9Provider) Name() string { return "opensubtitles" }
func (p *m9Provider) Search(context.Context, subtitles.Query) ([]subtitles.Candidate, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.searches++
	if p.err != nil {
		return nil, p.err
	}
	return []subtitles.Candidate{
		{Provider: "opensubtitles", ProviderID: "101", Language: "en", Downloads: 50, IDMatch: true, Year: 2009,
			Releases: []string{"Movie.19.2009.1080p.BluRay.x264-GRP"}},
		{Provider: "opensubtitles", ProviderID: "102", Language: "en", Downloads: 5, IDMatch: true, Year: 2009, HI: true,
			Releases: []string{"Movie.19.2009.720p.WEB-DL.x264-OTHER"}},
	}, nil
}
func (p *m9Provider) Download(context.Context, subtitles.Candidate) (subtitles.DownloadResult, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.downloads++
	return subtitles.DownloadResult{Data: []byte(m9Srt)}, nil
}
func (p *m9Provider) Test(context.Context) error { return p.err }

type m9Grabber struct {
	mu    sync.Mutex
	calls []grab.Target
	rels  []grab.Release
	err   error
}

func (g *m9Grabber) Grab(_ context.Context, t grab.Target, r grab.Release) (int64, error) {
	g.mu.Lock()
	defer g.mu.Unlock()
	if g.err != nil {
		return 0, g.err
	}
	g.calls = append(g.calls, t)
	g.rels = append(g.rels, r)
	return int64(900 + len(g.calls)), nil
}

type m9App struct {
	*app
	lib      *library.Service
	cat      *titles.Service
	reqs     *requests.Service
	srch     *search.Service
	src      *sources.Service
	feed     *sourcestest.Server
	prov     *m9Provider
	grabber  *m9Grabber
	moviesAt string
	musicAt  string
}

// m9Shelf tells the search what the artist already has, from the requests module.
type m9Shelf struct{ reqs *requests.Service }

func (s m9Shelf) Have(ctx context.Context, c grab.Copy) (search.Have, error) {
	var h search.Have
	if c.Kind == "artist" {
		on, err := s.reqs.AlbumsOnDisk(ctx, c.TitleID)
		if err != nil {
			return h, err
		}
		for id, ok := range on {
			if ok {
				h.AlbumIDs = append(h.AlbumIDs, id)
			}
		}
	}
	return h, nil
}

func newM9App(t *testing.T) *m9App {
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
	hc := outbound.New("test", outbound.Options{Logger: log, MaxRetries: -1, HostRPS: 1000, HostBurst: 1000})

	meta, err := metadata.New(mainDB, hc, metadata.Options{ArtworkDir: filepath.Join(dir, "cache", "artwork"), Logger: log})
	if err != nil {
		t.Fatal(err)
	}
	cat := titles.NewService(mainDB, titles.Providers{Movies: rqMovies{}, Shows: rqShows{}, Artists: m9Artists{}}, titles.Options{Logger: log})
	t.Cleanup(cat.Close)
	lib := library.NewService(mainDB, library.Options{Logger: log, Hub: hub})
	t.Cleanup(lib.Close)
	a := &m9App{lib: lib, cat: cat, prov: &m9Provider{}, grabber: &m9Grabber{}}
	for kind, dst := range map[string]*string{"movie": &a.moviesAt, "music": &a.musicAt} {
		p := filepath.Join(dir, kind)
		if err := os.MkdirAll(p, 0o755); err != nil {
			t.Fatal(err)
		}
		if _, err := lib.AddFolder(ctx, kind, p, kind); err != nil {
			t.Fatal(err)
		}
		*dst = p
	}
	a.reqs = requests.NewService(mainDB, cat, lib, cfg, requests.Options{Logger: log, Hub: hub})
	t.Cleanup(a.reqs.Close)

	a.src = sources.New(sources.NewStore(mainDB), box, hc, sources.Options{Logger: log})
	dec := decide.New(decide.NewStore(mainDB), decide.Options{})
	a.srch = search.New(mainDB, a.src, dec, cat, search.Options{Logger: log, Pace: -1})
	a.srch.SetCopySource(a.reqs)
	a.srch.SetGrabber(a.grabber)
	a.srch.SetShelf(m9Shelf{a.reqs})
	a.feed = sourcestest.NewServer(t, srcKey)

	subs := subtitles.New(mainDB, subtitles.Deps{Library: lib, Catalog: cat, Settings: cfg}, subtitles.Options{
		Logger: log, HTTP: hc, Version: "9.9.9-test", Hub: hub,
		Providers: func(subtitles.Config) []subtitles.Provider { return []subtitles.Provider{a.prov} },
	})

	s := New(Deps{
		Version: "9.9.9-test", Log: log, MainDB: mainDB, EventsDB: eventsDB,
		Accounts: accounts.NewService(mainDB, accounts.Options{Logger: log, PasswordParams: lightParams}),
		Settings: cfg, Hub: hub, Metadata: meta, Titles: cat, Library: lib, Requests: a.reqs, Sources: a.src, Decide: dec, Search: a.srch,
		Subtitles: subs, UI: testUI(),
	})
	srv := httptest.NewServer(s.Handler())
	t.Cleanup(srv.Close)
	a.app = &app{t: t, srv: srv, mainDB: mainDB, settings: cfg, hub: hub}
	return a
}

func (a *m9App) admin(owner *client, name string) *client {
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

// movieFile adds "Movie 19" to the catalog and a video of it to the library.
func (a *m9App) movieFile() (fileID int64, video string) {
	a.t.Helper()
	d, _, err := a.cat.Add(context.Background(), titles.KindMovie, metadata.ProviderTMDB, "19", titles.AddOptions{Monitored: true})
	if err != nil {
		a.t.Fatal(err)
	}
	folders, _ := a.lib.ListFolders(context.Background())
	var folder library.Folder
	for _, f := range folders {
		if f.Kind == "movie" {
			folder = f
		}
	}
	rel := "Movie 19 (2009)/Movie 19 (2009) Bluray-1080p.mkv"
	video = filepath.Join(folder.Path, filepath.FromSlash(rel))
	if err := os.MkdirAll(filepath.Dir(video), 0o755); err != nil {
		a.t.Fatal(err)
	}
	if err := os.WriteFile(video, []byte("not really a video"), 0o644); err != nil {
		a.t.Fatal(err)
	}
	if err := a.lib.ImportFiles(context.Background(), folder.ID, []library.ImportedFile{{RelPath: rel, TitleID: d.ID}}); err != nil {
		a.t.Fatal(err)
	}
	ids, err := a.lib.FileIDs(context.Background(), folder.ID, []string{rel})
	if err != nil || ids[rel] == 0 {
		a.t.Fatalf("FileIDs = %v, %v", ids, err)
	}
	return ids[rel], video
}

func subtitlePathFor(video, lang string, hi bool) string {
	return subtitles.SubtitlePath(video, lang, hi)
}

// ---- subtitles -----------------------------------------------------------------------

func TestSubtitleRoutesPermissions(t *testing.T) {
	a := newM9App(t)
	owner := a.owner()
	admin := a.admin(owner, "Alice")
	member := a.member(owner, "Mia")
	anon := a.newClient()
	id, _ := a.movieFile()
	file := "/api/v1/library/files/" + itoa(id) + "/subtitles"

	type call struct {
		method, path string
		body         any
	}
	calls := map[string]call{
		"status":   {"GET", "/api/v1/subtitles/status", nil},
		"list":     {"GET", file, nil},
		"search":   {"POST", file + "/search", map[string]any{"language": "en"}},
		"download": {"POST", file + "/download", map[string]any{"provider": "opensubtitles", "providerId": "101", "language": "en"}},
		"delete":   {"DELETE", "/api/v1/subtitles/1", nil},
		"test":     {"POST", "/api/v1/subtitles/test", map[string]any{"provider": "opensubtitles"}},
	}
	// Who may: status, search, download, delete are for the owner and admins; the
	// list of a file's subtitles for everyone signed in; the test button for the owner.
	allowed := map[string]map[*client]bool{
		"status":   {owner: true, admin: true},
		"list":     {owner: true, admin: true, member: true},
		"search":   {owner: true, admin: true},
		"download": {owner: true, admin: true},
		"delete":   {owner: true, admin: true},
		"test":     {owner: true},
	}
	for name, c := range calls {
		if r := anon.do(c.method, c.path, c.body); r.Status != http.StatusUnauthorized {
			t.Errorf("%s as nobody = %d, want 401", name, r.Status)
		}
		for who, cl := range map[string]*client{"owner": owner, "admin": admin, "member": member} {
			r := cl.do(c.method, c.path, c.body)
			if allowed[name][cl] {
				if r.Status == http.StatusUnauthorized || r.Status == http.StatusForbidden {
					t.Errorf("%s as %s = %d %s, want allowed", name, who, r.Status, r.Body)
				}
			} else if r.Status != http.StatusForbidden || r.errCode(t) != "forbidden" {
				t.Errorf("%s as %s = %d %s, want 403 forbidden", name, who, r.Status, r.Body)
			}
		}
	}
}

func TestSubtitleSearchDownloadListDelete(t *testing.T) {
	a := newM9App(t)
	owner := a.owner()
	id, video := a.movieFile()
	file := "/api/v1/library/files/" + itoa(id) + "/subtitles"

	// Before: nothing, English is missing, and a source is there to search.
	r := owner.send("GET", file, nil)
	if r.Status != http.StatusOK {
		t.Fatalf("list = %d %s", r.Status, r.Body)
	}
	body := r.JSON(t)
	if body["fileId"] != float64(id) || len(asSlice(t, body["subtitles"])) != 0 || body["canSearch"] != true ||
		len(body["missing"].([]any)) != 1 || body["missing"].([]any)[0] != "en" || body["languages"].([]any)[0] != "en" {
		t.Errorf("list = %s", r.Body)
	}

	r = owner.do("POST", file+"/search", map[string]any{"language": "en"})
	if r.Status != http.StatusOK {
		t.Fatalf("search = %d %s", r.Status, r.Body)
	}
	body = r.JSON(t)
	results := asSlice(t, body["results"])
	if body["language"] != "en" || body["minScore"] != float64(60) || len(results) != 2 || len(asSlice(t, body["providers"])) != 1 {
		t.Fatalf("search = %s", r.Body)
	}
	best := results[0]
	if best["providerId"] != "101" || best["provider"] != "opensubtitles" || best["accepted"] != true || best["score"].(float64) < 60 ||
		best["release"] != "Movie.19.2009.1080p.BluRay.x264-GRP" || best["hi"] != false || len(best["reasons"].([]any)) == 0 {
		t.Errorf("best = %v", best)
	}
	if strings.Contains(string(r.Body), "downloadUrl") || strings.Contains(string(r.Body), "link") {
		t.Errorf("a result carries a download link: %s", r.Body)
	}

	r = owner.do("POST", file+"/download", map[string]any{"provider": "opensubtitles", "providerId": "101", "language": "en"})
	if r.Status != http.StatusCreated {
		t.Fatalf("download = %d %s", r.Status, r.Body)
	}
	sub := obj(t, r.JSON(t), "subtitle")
	if sub["language"] != "en" || sub["hi"] != false || sub["provider"] != "opensubtitles" || sub["name"] != "Movie 19 (2009) Bluray-1080p.en.srt" ||
		sub["fileId"] != float64(id) || sub["id"] == nil {
		t.Errorf("subtitle = %v", sub)
	}
	for _, k := range []string{"path"} {
		if _, has := sub[k]; has {
			t.Errorf("the answer leaks %q: %v", k, sub)
		}
	}
	data, err := os.ReadFile(subtitlePathFor(video, "en", false))
	if err != nil || string(data) != m9Srt {
		t.Fatalf("the subtitle file: %q, %v", data, err)
	}

	// The list now shows it, to everyone signed in, by file name only.
	member := a.member(owner, "Mia")
	r = member.send("GET", file, nil)
	body = r.JSON(t)
	subs := asSlice(t, body["subtitles"])
	if len(subs) != 1 || subs[0]["language"] != "en" || subs[0]["source"] != "marquee" || subs[0]["id"] != sub["id"] || len(body["missing"].([]any)) != 0 {
		t.Errorf("list = %s", r.Body)
	}
	if strings.Contains(string(r.Body), a.moviesAt) || strings.Contains(string(r.Body), `\\`) {
		t.Errorf("the list leaks a folder path: %s", r.Body)
	}

	// Delete: only what Marquee wrote.
	if r = owner.do("DELETE", "/api/v1/subtitles/"+itoa(int64(sub["id"].(float64))), nil); r.Status != http.StatusNoContent {
		t.Fatalf("delete = %d %s", r.Status, r.Body)
	}
	if _, err := os.Stat(subtitlePathFor(video, "en", false)); !os.IsNotExist(err) {
		t.Error("the subtitle file is still there")
	}
	if _, err := os.Stat(video); err != nil {
		t.Error("the video was touched")
	}
	if r = owner.do("DELETE", "/api/v1/subtitles/"+itoa(int64(sub["id"].(float64))), nil); r.Status != http.StatusNotFound || r.errCode(t) != "not_found" {
		t.Errorf("a second delete = %d %s", r.Status, r.Body)
	}
	if r = owner.do("DELETE", "/api/v1/subtitles/abc", nil); r.Status != http.StatusNotFound {
		t.Errorf("a bad id = %d %s", r.Status, r.Body)
	}
}

func TestSubtitleErrors(t *testing.T) {
	a := newM9App(t)
	owner := a.owner()
	id, video := a.movieFile()
	file := "/api/v1/library/files/" + itoa(id) + "/subtitles"

	expect := func(r reply, status int, code string) {
		t.Helper()
		if r.Status != status || r.errCode(t) != code {
			t.Errorf("= %d %s, want %d %s", r.Status, r.Body, status, code)
		}
	}
	expect(owner.do("POST", file+"/search", map[string]any{"language": "not a language"}), 400, "invalid_request")
	expect(owner.do("POST", file+"/search", map[string]any{}), 400, "invalid_request")
	expect(owner.do("POST", file+"/download", map[string]any{"provider": "opensubtitles"}), 400, "invalid_request")
	expect(owner.do("POST", file+"/search", "nope"), 400, "invalid_json")
	expect(owner.do("POST", "/api/v1/library/files/99999/subtitles/search", map[string]any{"language": "en"}), 404, "not_found")
	expect(owner.send("GET", "/api/v1/library/files/abc/subtitles", nil), 404, "not_found")
	expect(owner.send("GET", "/api/v1/library/files/99999/subtitles", nil), 404, "not_found")

	// A subtitle file already there that Marquee did not write is never replaced.
	if err := os.WriteFile(subtitlePathFor(video, "en", false), []byte("mine"), 0o644); err != nil {
		t.Fatal(err)
	}
	expect(owner.do("POST", file+"/download", map[string]any{"provider": "opensubtitles", "providerId": "101", "language": "en"}), 409, "file_exists")
	if b, _ := os.ReadFile(subtitlePathFor(video, "en", false)); string(b) != "mine" {
		t.Errorf("the foreign file was changed: %q", b)
	}
	_ = os.Remove(subtitlePathFor(video, "en", false))

	// The provider failing is a 502 the page can show, never a 401 or 403.
	a.prov.err = &subtitles.UnavailableError{Provider: "opensubtitles", Message: "OpenSubtitles did not answer (HTTP 503). Try again later."}
	r := owner.do("POST", file+"/search", map[string]any{"language": "en"})
	body := r.JSON(t)
	if r.Status != http.StatusOK || asSlice(t, body["providers"])[0]["ok"] != false || len(asSlice(t, body["results"])) != 0 {
		t.Errorf("a failing provider = %d %s", r.Status, r.Body)
	}
	a.prov.err = &subtitles.AuthError{Provider: "opensubtitles", Message: "OpenSubtitles did not accept the API key or login (HTTP 401)."}
	expect(owner.do("POST", "/api/v1/subtitles/test", map[string]any{"provider": "opensubtitles"}), 502, "provider_auth")
	a.prov.err = &subtitles.UnavailableError{Provider: "opensubtitles", Message: "OpenSubtitles could not be reached."}
	expect(owner.do("POST", "/api/v1/subtitles/test", map[string]any{"provider": "opensubtitles"}), 502, "provider_failed")
	a.prov.err = nil
	if r := owner.do("POST", "/api/v1/subtitles/test", map[string]any{"provider": "opensubtitles"}); r.Status != http.StatusOK || r.JSON(t)["ok"] != true {
		t.Errorf("a working test = %d %s", r.Status, r.Body)
	}
	expect(owner.do("POST", "/api/v1/subtitles/test", map[string]any{"provider": "napster"}), 400, "invalid_request")

	// The video gone from disk.
	if err := os.Remove(video); err != nil {
		t.Fatal(err)
	}
	expect(owner.do("POST", file+"/search", map[string]any{"language": "en"}), 409, "file_missing")
}

func TestSubtitleStatusAndSettings(t *testing.T) {
	a := newM9App(t)
	owner := a.owner()
	a.movieFile()

	r := owner.send("GET", "/api/v1/subtitles/status", nil)
	if r.Status != http.StatusOK {
		t.Fatalf("status = %d %s", r.Status, r.Body)
	}
	st := r.JSON(t)
	provs := asSlice(t, st["providers"])
	if st["missingCount"] != float64(1) || len(provs) != 2 || provs[0]["name"] != "opensubtitles" || provs[1]["name"] != "podnapisi" ||
		provs[1]["enabled"] != true || len(st["languages"].([]any)) != 1 {
		t.Errorf("status = %s", r.Body)
	}

	// The settings go through the generic section; the key and the password are
	// sealed and shown masked, and saving the masked form keeps them.
	put := owner.do("PUT", "/api/v1/settings/subtitles", map[string]any{
		"languages": []string{"en", "fr"}, "hearingImpaired": "prefer", "minScore": 70, "searchAfterImport": true, "rescanEveryHours": 12,
		"providers": map[string]any{
			"opensubtitles": map[string]any{"enabled": true, "apiKey": "abcdefghijklmnop1234", "username": "bailey", "password": "hunter2hunter2"},
			"podnapisi":     map[string]any{"enabled": true},
		},
	})
	if put.Status != http.StatusOK {
		t.Fatalf("PUT = %d %s", put.Status, put.Body)
	}
	if strings.Contains(string(put.Body), "abcdefghijklmnop1234") || strings.Contains(string(put.Body), "hunter2hunter2") {
		t.Errorf("the PUT answer shows a secret: %s", put.Body)
	}
	get := owner.send("GET", "/api/v1/settings/subtitles", nil)
	val := obj(t, get.JSON(t), "value")
	os_ := obj(t, val, "providers", "opensubtitles")
	if !strings.HasPrefix(os_["apiKey"].(string), "••••") || !strings.HasPrefix(os_["password"].(string), "••••") || os_["username"] != "bailey" {
		t.Errorf("opensubtitles = %v", os_)
	}
	if put = owner.do("PUT", "/api/v1/settings/subtitles", val); put.Status != http.StatusOK {
		t.Fatalf("PUT of the masked form = %d %s", put.Status, put.Body)
	}
	var raw string
	if err := a.mainDB.QueryRow(`SELECT value FROM settings WHERE key = 'subtitles'`).Scan(&raw); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(raw, "abcdefghijklmnop1234") || strings.Contains(raw, "hunter2hunter2") {
		t.Errorf("a secret is stored in the clear: %s", raw)
	}
	st = owner.send("GET", "/api/v1/subtitles/status", nil).JSON(t)
	if len(st["languages"].([]any)) != 2 {
		t.Errorf("the saved languages did not reach the status: %v", st["languages"])
	}
}

func TestSubtitleRoutesAbsentWithoutTheService(t *testing.T) {
	a := newApp(t, testUI())
	owner := a.owner()
	if r := owner.send("GET", "/api/v1/subtitles/status", nil); r.Status != http.StatusNotFound || r.errCode(t) != "not_found" {
		t.Errorf("= %d %s, want the JSON 404", r.Status, r.Body)
	}
}

// ---- music ----------------------------------------------------------------------------------

// artist adds Chopin (two albums), asks for him so he has a copy, and returns his
// id and his album ids by name.
func (a *m9App) artist(owner *client) (int64, map[string]int64) {
	a.t.Helper()
	d, _, err := a.cat.Add(context.Background(), titles.KindArtist, metadata.ProviderMusicBrainz, m9Chopin, titles.AddOptions{Monitored: true})
	if err != nil {
		a.t.Fatal(err)
	}
	ids := map[string]int64{}
	for _, al := range d.Albums {
		ids[al.Name] = al.ID
	}
	if _, err := a.reqs.EnsureCopy(context.Background(), d.ID, "hd"); err != nil {
		a.t.Fatal(err)
	}
	return d.ID, ids
}

func (a *m9App) serveChopin() {
	a.feed.Search = func(q url.Values) []sourcestest.Item {
		text := strings.ToLower(q.Get("album") + " " + q.Get("q"))
		if strings.Contains(text, "nocturnes") {
			return []sourcestest.Item{
				torrentItem("Frédéric Chopin - Nocturnes (1998) [FLAC]", 600<<20, 20),
				torrentItem("Frédéric Chopin - Nocturnes (1998) [MP3 128]", 60<<20, 20),
			}
		}
		return nil
	}
}

func (a *m9App) addSource(owner *client) {
	a.t.Helper()
	r := owner.do("POST", "/api/v1/sources", map[string]any{"name": "Alpha", "kind": "torznab", "url": a.feed.Feed(), "apiKey": srcKey})
	if r.Status != http.StatusCreated {
		a.t.Fatalf("create source = %d %s", r.Status, r.Body)
	}
}

func TestAlbumSearchRoute(t *testing.T) {
	a := newM9App(t)
	owner := a.owner()
	admin := a.admin(owner, "Alice")
	member := a.member(owner, "Mia")
	anon := a.newClient()
	artist, albums := a.artist(owner)
	a.serveChopin()
	a.addSource(owner)
	path := func(album int64) string {
		return "/api/v1/titles/" + itoa(artist) + "/albums/" + itoa(album) + "/search"
	}

	if r := anon.do("POST", path(albums["Nocturnes"]), nil); r.Status != http.StatusUnauthorized {
		t.Errorf("as nobody = %d", r.Status)
	}
	if r := member.do("POST", path(albums["Nocturnes"]), nil); r.Status != http.StatusForbidden {
		t.Errorf("as a member = %d", r.Status)
	}

	// An admin searches the album: the best acceptable release is sent.
	r := admin.do("POST", path(albums["Nocturnes"]), nil)
	if r.Status != http.StatusCreated {
		t.Fatalf("search = %d %s", r.Status, r.Body)
	}
	body := r.JSON(t)
	rel := obj(t, body, "release")
	if body["grabbed"] != true || body["found"] != float64(2) || body["downloadId"] != float64(901) ||
		rel["title"] != "Frédéric Chopin - Nocturnes (1998) [FLAC]" || rel["quality"] != "FLAC" || len(rel["reasons"].([]any)) == 0 {
		t.Errorf("answer = %s", r.Body)
	}
	if len(a.grabber.calls) != 1 {
		t.Fatalf("grabs = %d", len(a.grabber.calls))
	}
	tg := a.grabber.calls[0]
	if tg.Kind != "artist" || tg.TitleID != artist || len(tg.EpisodeIDs) != 1 || tg.EpisodeIDs[0] != albums["Nocturnes"] {
		t.Errorf("target = %+v, want the album in its episode ids", tg)
	}

	// Nothing for the other album: not an error.
	r = owner.do("POST", path(albums["Études"]), nil)
	body = r.JSON(t)
	if r.Status != http.StatusOK || body["grabbed"] != false || body["found"] != float64(0) || body["note"] != "Nothing found" {
		t.Errorf("nothing found = %d %s", r.Status, r.Body)
	}
	if _, has := body["release"]; has {
		t.Errorf("a release in an answer that grabbed nothing: %s", r.Body)
	}

	// Mistakes.
	for name, c := range map[string]struct {
		path   string
		status int
		code   string
	}{
		"an album the artist does not have": {path(99999), 400, "invalid_request"},
		"a bad album id":                    {"/api/v1/titles/" + itoa(artist) + "/albums/abc/search", 404, "not_found"},
		"a bad title id":                    {"/api/v1/titles/abc/albums/1/search", 404, "not_found"},
		"an unknown title":                  {"/api/v1/titles/99999/albums/1/search", 404, "not_found"},
	} {
		if r := owner.do("POST", c.path, nil); r.Status != c.status || r.errCode(t) != c.code {
			t.Errorf("%s = %d %s, want %d %s", name, r.Status, r.Body, c.status, c.code)
		}
	}
	a.grabber.err = search.ErrNoGrabber
	if r := owner.do("POST", path(albums["Nocturnes"]), nil); r.Status != http.StatusConflict || r.errCode(t) != "no_grabber" {
		t.Errorf("no download client = %d %s", r.Status, r.Body)
	}
}

func TestAlbumSearchWithNoSources(t *testing.T) {
	a := newM9App(t)
	owner := a.owner()
	artist, albums := a.artist(owner)
	r := owner.do("POST", "/api/v1/titles/"+itoa(artist)+"/albums/"+itoa(albums["Nocturnes"])+"/search", nil)
	if r.Status != http.StatusConflict || r.errCode(t) != "no_sources" {
		t.Errorf("= %d %s, want 409 no_sources", r.Status, r.Body)
	}
}

func TestArtistDetailSaysWhichAlbumsAreInTheLibrary(t *testing.T) {
	a := newM9App(t)
	owner := a.owner()
	member := a.member(owner, "Mia")
	artist, albums := a.artist(owner)
	album := func(c *client, name string) map[string]any {
		t.Helper()
		r := c.send("GET", titlePath(artist), nil)
		for _, al := range asSlice(t, r.JSON(t)["albums"]) {
			if al["name"] == name {
				return al
			}
		}
		t.Fatalf("no album %q in %s", name, r.Body)
		return nil
	}
	for _, c := range []*client{owner, member} {
		if al := album(c, "Nocturnes"); al["inLibrary"] != false || al["haveTracks"] != float64(0) {
			t.Errorf("before: %v", al)
		}
	}

	// A track of Nocturnes is imported with its track id; Études has a file the
	// scanner could only match by its folder name.
	folders, _ := a.lib.ListFolders(context.Background())
	var music library.Folder
	for _, f := range folders {
		if f.Kind == "music" {
			music = f
		}
	}
	d, _ := a.cat.Get(context.Background(), artist)
	var track int64
	for _, al := range d.Albums {
		if al.Name == "Nocturnes" {
			track = al.Tracks[0].ID
		}
	}
	for _, rel := range []string{"Frédéric Chopin/Nocturnes (1998)/01 - Nocturne One.flac", "Frédéric Chopin/Études (1985) [FLAC]/05 - Whatever.flac"} {
		p := filepath.Join(music.Path, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte("x"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	if err := a.lib.ImportFiles(context.Background(), music.ID, []library.ImportedFile{
		{RelPath: "Frédéric Chopin/Nocturnes (1998)/01 - Nocturne One.flac", TitleID: artist, TrackID: track},
		{RelPath: "Frédéric Chopin/Études (1985) [FLAC]/05 - Whatever.flac", TitleID: artist},
	}); err != nil {
		t.Fatal(err)
	}
	for _, c := range []*client{owner, member} { // everyone signed in sees it
		if al := album(c, "Nocturnes"); al["inLibrary"] != true || al["haveTracks"] != float64(1) {
			t.Errorf("Nocturnes: %v", al)
		}
		if al := album(c, "Études"); al["inLibrary"] != true || al["haveTracks"] != float64(0) {
			t.Errorf("Études (by its folder): %v", al)
		}
	}
	_ = albums
}

func TestTitleSubtitlesForEveryoneSignedIn(t *testing.T) {
	a := newM9App(t)
	owner := a.owner()
	member := a.member(owner, "Mia")
	anon := a.newClient()
	id, video := a.movieFile()
	if err := os.WriteFile(subtitlePathFor(video, "fr", false), []byte(m9Srt), 0o644); err != nil {
		t.Fatal(err)
	}
	var titleID int64
	if err := a.mainDB.QueryRow(`SELECT title_id FROM files WHERE id = ?`, id).Scan(&titleID); err != nil {
		t.Fatal(err)
	}
	path := titlePath(titleID) + "/subtitles"
	if r := anon.send("GET", path, nil); r.Status != http.StatusUnauthorized {
		t.Errorf("as nobody = %d", r.Status)
	}
	for _, c := range []*client{owner, member} {
		r := c.send("GET", path, nil)
		if r.Status != http.StatusOK {
			t.Fatalf("= %d %s", r.Status, r.Body)
		}
		files := asSlice(t, r.JSON(t)["files"])
		if len(files) != 1 || files[0]["fileId"] != float64(id) || files[0]["name"] != "Movie 19 (2009) Bluray-1080p.mkv" ||
			len(asSlice(t, files[0]["subtitles"])) != 1 || files[0]["canSearch"] != true {
			t.Errorf("files = %s", r.Body)
		}
		if strings.Contains(string(r.Body), a.moviesAt) {
			t.Errorf("a folder path leaked: %s", r.Body)
		}
	}
	if r := owner.send("GET", "/api/v1/titles/abc/subtitles", nil); r.Status != http.StatusNotFound {
		t.Errorf("a bad id = %d", r.Status)
	}
}
