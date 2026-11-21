package web

import (
	"bytes"
	"context"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
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
	"github.com/BaileyPillon/marquee/internal/metadata"
	"github.com/BaileyPillon/marquee/internal/settings"
	"github.com/BaileyPillon/marquee/internal/titles"
)

// ---- test rig --------------------------------------------------------------

// upstream is one fake server standing in for TMDB, TVmaze, MusicBrainz and the
// three image hosts, each under its own path prefix.
type upstream struct {
	srv      *httptest.Server
	mu       sync.Mutex
	handlers map[string]http.HandlerFunc
	hits     map[string]int
}

func newUpstream(t *testing.T) *upstream {
	u := &upstream{handlers: map[string]http.HandlerFunc{}, hits: map[string]int{}}
	u.srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		u.mu.Lock()
		u.hits[r.URL.Path]++
		h := u.handlers[r.URL.Path]
		u.mu.Unlock()
		if h == nil {
			http.NotFound(w, r)
			return
		}
		h(w, r)
	}))
	t.Cleanup(u.srv.Close)
	return u
}

func (u *upstream) handle(path string, h http.HandlerFunc) {
	u.mu.Lock()
	defer u.mu.Unlock()
	u.handlers[path] = h
}

func (u *upstream) count(path string) int {
	u.mu.Lock()
	defer u.mu.Unlock()
	return u.hits[path]
}

func (u *upstream) total() int {
	u.mu.Lock()
	defer u.mu.Unlock()
	n := 0
	for _, c := range u.hits {
		n += c
	}
	return n
}

func fixtureFile(t *testing.T, name string) http.HandlerFunc {
	t.Helper()
	body, err := os.ReadFile(filepath.Join("..", "metadata", "testdata", name))
	if err != nil {
		t.Fatal(err)
	}
	return func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write(body)
	}
}

func serveBody(body []byte) http.HandlerFunc {
	return func(w http.ResponseWriter, _ *http.Request) { _, _ = w.Write(body) }
}

func code(c int) http.HandlerFunc {
	return func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(c) }
}

const (
	chopinMBID = "a1b2c3d4-0000-4000-8000-000000000001"
	nocturnes  = "b0000000-0000-4000-8000-000000000101"
	testTMDB   = "0123456789abcdef0123456789abcdef"
)

var (
	jpegImage = append([]byte{0xFF, 0xD8, 0xFF, 0xE0, 0x00, 0x10, 'J', 'F', 'I', 'F', 0x00}, bytes.Repeat([]byte{0x33}, 2500)...)
	pngImage  = append([]byte("\x89PNG\r\n\x1a\n"), bytes.Repeat([]byte{0x44}, 800)...)
)

// catalogApp is a whole Marquee server, with the catalog, on fake providers.
type catalogApp struct {
	*app
	up     *upstream
	evil   *upstream // an image host that must never be contacted
	artDir string
	meta   *metadata.Service
}

func newCatalogApp(t *testing.T) *catalogApp {
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

	up, evil := newUpstream(t), newUpstream(t)
	up.handle("/tvmaze/search/shows", fixtureFile(t, "tvmaze_search_pioneer.json"))
	up.handle("/tvmaze/shows/99001", fixtureFile(t, "tvmaze_show_pioneer.json"))
	up.handle("/tmdb/3/search/movie", fixtureFile(t, "tmdb_search_movie_metropolis.json"))
	up.handle("/tmdb/3/movie/19", fixtureFile(t, "tmdb_movie_metropolis.json"))
	up.handle("/mb/ws/2/artist", fixtureFile(t, "mb_artist_search_chopin.json"))
	up.handle("/mb/ws/2/artist/"+chopinMBID, fixtureFile(t, "mb_artist_chopin.json"))
	up.handle("/mb/ws/2/release-group", fixtureFile(t, "mb_release_groups_chopin.json"))
	releases := map[string]http.HandlerFunc{
		"b0000000-0000-4000-8000-000000000101": fixtureFile(t, "mb_releases_nocturnes.json"),
		"b0000000-0000-4000-8000-000000000102": fixtureFile(t, "mb_releases_etudes.json"),
		"b0000000-0000-4000-8000-000000000103": fixtureFile(t, "mb_releases_ballades.json"),
	}
	up.handle("/mb/ws/2/release", func(w http.ResponseWriter, r *http.Request) {
		if h, ok := releases[r.URL.Query().Get("release-group")]; ok {
			h(w, r)
			return
		}
		http.NotFound(w, r)
	})
	up.handle("/mb/ws/2/release/c0000000-0000-4000-8000-000000000203", fixtureFile(t, "mb_release_nocturnes.json"))
	up.handle("/mb/ws/2/release/c0000000-0000-4000-8000-000000000211", fixtureFile(t, "mb_release_etudes.json"))
	up.handle("/mb/ws/2/release/c0000000-0000-4000-8000-000000000221", fixtureFile(t, "mb_release_ballades.json"))
	up.handle("/img/tmdb/w500/metropolis1927.jpg", serveBody(jpegImage))
	up.handle("/img/tvmaze/uploads/images/original_untouched/99/99001.jpg", serveBody(jpegImage))
	up.handle("/img/caa/release-group/"+nocturnes+"/front-500", serveBody(pngImage))
	up.handle("/img/tmdb/w500/page.jpg", serveBody([]byte("<html>not an image</html>")))

	// Waiting is skipped: the providers' rate limits are tested in the
	// metadata package, and a full artist add would otherwise take seconds.
	hc := outbound.New("9.9.9-test", outbound.Options{
		Logger: log, MaxRetries: -1,
		Sleep: func(context.Context, time.Duration) error { return nil },
	})
	artDir := filepath.Join(dir, "cache", "artwork")
	meta, err := metadata.New(mainDB, hc, metadata.Options{
		TMDBURL:        up.srv.URL + "/tmdb/3",
		TVmazeURL:      up.srv.URL + "/tvmaze",
		MusicBrainzURL: up.srv.URL + "/mb",
		ArtworkDir:     artDir,
		ArtworkBases: map[string]string{
			"tmdb": up.srv.URL + "/img/tmdb", "tvmaze": up.srv.URL + "/img/tvmaze", "caa": up.srv.URL + "/img/caa",
		},
		TMDBKey: func(ctx context.Context) (string, error) {
			v, _, err := settings.GetAs[struct {
				APIKey string `json:"apiKey"`
			}](ctx, cfg, "tmdb")
			return v.APIKey, err
		},
		Logger: log,
	})
	if err != nil {
		t.Fatal(err)
	}
	cat := titles.NewService(mainDB, titles.Providers{Movies: meta.TMDB, Shows: meta.TVmaze, Artists: meta.MusicBrainz}, titles.Options{Logger: log})

	s := New(Deps{
		Version: "9.9.9-test", Log: log, MainDB: mainDB, EventsDB: eventsDB,
		Accounts: accounts.NewService(mainDB, accounts.Options{Logger: log, PasswordParams: lightParams}),
		Settings: cfg, Hub: hub, Metadata: meta, Titles: cat, UI: testUI(),
	})
	srv := httptest.NewServer(s.Handler())
	t.Cleanup(srv.Close)
	return &catalogApp{
		app: &app{t: t, srv: srv, mainDB: mainDB, settings: cfg, hub: hub},
		up:  up, evil: evil, artDir: artDir, meta: meta,
	}
}

// admin creates an admin through an invite and returns a signed-in client.
func (a *catalogApp) admin(owner *client, name string) *client {
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

// keyClient sends every request with an API key as a Bearer token and no cookie.
type keyClient struct {
	c      *client
	secret string
}

func (k keyClient) send(method, path string, body any, headers ...string) reply {
	k.c.a.t.Helper()
	return k.c.send(method, path, body, append([]string{"Authorization", "Bearer " + k.secret}, headers...)...)
}

// key makes an API key with the given scopes for whoever c is signed in as.
func (a *catalogApp) key(c *client, scopes ...string) keyClient {
	a.t.Helper()
	r := c.do("POST", "/api/v1/apikeys", map[string]any{"name": "test key", "scopes": scopes})
	if r.Status != http.StatusCreated {
		a.t.Fatalf("apikey = %d %s", r.Status, r.Body)
	}
	return keyClient{c: a.newClient(), secret: r.JSON(a.t)["secret"].(string)}
}

func (a *catalogApp) setTMDBKey(owner *client) {
	a.t.Helper()
	if r := owner.do("PUT", "/api/v1/settings/tmdb", map[string]any{"apiKey": testTMDB}); r.Status != 200 {
		a.t.Fatalf("settings = %d %s", r.Status, r.Body)
	}
}

func idOf(t *testing.T, r reply) int64 {
	t.Helper()
	f, ok := r.JSON(t)["id"].(float64)
	if !ok {
		t.Fatalf("no id in %s", r.Body)
	}
	return int64(f)
}

func titlePath(id int64) string { return "/api/v1/titles/" + strconv.FormatInt(id, 10) }

func group(t *testing.T, r reply, kind string) map[string]any {
	t.Helper()
	g, ok := r.JSON(t)[kind].(map[string]any)
	if !ok {
		t.Fatalf("no %q group in %s", kind, r.Body)
	}
	return g
}

func results(t *testing.T, g map[string]any) []any {
	t.Helper()
	res, ok := g["results"].([]any)
	if !ok {
		t.Fatalf("group has no results: %v", g)
	}
	return res
}

// ---- search ----------------------------------------------------------------

func TestSearchAllReturnsGroupsWithPerProviderErrors(t *testing.T) {
	a := newCatalogApp(t)
	owner := a.owner()

	// No TMDB key yet: movies fail with a code, shows and music still answer.
	r := owner.send("GET", "/api/v1/search?q=pioneer&kind=all", nil)
	if r.Status != 200 {
		t.Fatalf("status %d: %s", r.Status, r.Body)
	}
	movie := group(t, r, "movie")
	if movie["error"] != "tmdb_key_missing" || movie["message"] == "" || movie["results"] != nil {
		t.Errorf("movie group = %v", movie)
	}
	shows := results(t, group(t, r, "show"))
	if len(shows) != 2 {
		t.Fatalf("shows = %v", shows)
	}
	first := shows[0].(map[string]any)
	if first["name"] != "Pioneer One" || first["providerId"] != "99001" || first["provider"] != "tvmaze" || first["kind"] != "show" ||
		first["year"] != float64(2010) || first["inLibrary"] != false || first["imdbId"] != "tt9900001" {
		t.Errorf("first show = %v", first)
	}
	if p, _ := first["poster"].(map[string]any); p["ref"] != "tvmaze/uploads/images/original_untouched/99/99001.jpg" {
		t.Errorf("poster = %v", first["poster"])
	}
	if _, has := first["titleId"]; has {
		t.Errorf("an unsaved result must not carry a titleId: %v", first)
	}
	artists := results(t, group(t, r, "artist"))
	if len(artists) != 2 || artists[0].(map[string]any)["providerId"] != chopinMBID {
		t.Errorf("artists = %v", artists)
	}
	if a.up.count("/tmdb/3/search/movie") != 0 {
		t.Error("TMDB was called without a key")
	}

	// Save the key: now movies answer too, and it is sent as api_key.
	a.setTMDBKey(owner)
	r = owner.send("GET", "/api/v1/search?q=metropolis&kind=all", nil)
	movies := results(t, group(t, r, "movie"))
	if len(movies) != 2 || movies[0].(map[string]any)["name"] != "Metropolis" || movies[0].(map[string]any)["year"] != float64(1927) {
		t.Errorf("movies = %v", movies)
	}
	if a.up.count("/tmdb/3/search/movie") != 1 {
		t.Errorf("TMDB calls = %d", a.up.count("/tmdb/3/search/movie"))
	}
}

func TestSearchOneProviderDownDoesNotHideTheOthers(t *testing.T) {
	a := newCatalogApp(t)
	owner := a.owner()
	a.up.handle("/tvmaze/search/shows", code(500))
	r := owner.send("GET", "/api/v1/search?q=anything", nil) // kind defaults to all
	if r.Status != 200 {
		t.Fatalf("status %d", r.Status)
	}
	if g := group(t, r, "show"); g["error"] != "upstream_error" || g["message"] == "" {
		t.Errorf("show group = %v", g)
	}
	if len(results(t, group(t, r, "artist"))) != 2 {
		t.Error("the artist group was lost")
	}
}

func TestSearchOneKindAndValidation(t *testing.T) {
	a := newCatalogApp(t)
	owner := a.owner()
	member := a.member(owner, "Friend") // a plain member holds the "see" scope

	r := member.send("GET", "/api/v1/search?q=pioneer&kind=show", nil)
	if r.Status != 200 {
		t.Fatalf("member search = %d %s", r.Status, r.Body)
	}
	m := r.JSON(t)
	if len(m) != 1 || m["show"] == nil {
		t.Errorf("kind=show returned %v, want only the show group", m)
	}

	for _, tc := range []struct{ path, code string }{
		{"/api/v1/search?q=x&kind=book", "invalid_kind"},
		{"/api/v1/search?kind=show", "invalid_query"},
		{"/api/v1/search?q=%20%20&kind=show", "invalid_query"},
		{"/api/v1/search?q=" + strings.Repeat("x", 201), "invalid_query"},
	} {
		r := owner.send("GET", tc.path, nil)
		if r.Status != 400 || r.errCode(t) != tc.code {
			t.Errorf("%s: %d %s", tc.path, r.Status, r.Body)
		}
	}
	if a.up.total() != 1 {
		t.Errorf("invalid searches reached the providers (%d calls)", a.up.total())
	}
}

// ---- the catalog, happy paths ---------------------------------------------

func TestAddListGetPatchRefreshDelete(t *testing.T) {
	a := newCatalogApp(t)
	owner := a.owner()

	// Add a show; providerId may be a number.
	r := owner.do("POST", "/api/v1/titles", map[string]any{
		"kind": "show", "provider": "tvmaze", "providerId": 99001, "monitored": true, "rootFolder": "/tv",
	})
	if r.Status != http.StatusCreated {
		t.Fatalf("add = %d %s", r.Status, r.Body)
	}
	id := idOf(t, r)
	body := r.JSON(t)
	if body["name"] != "Pioneer One" || body["kind"] != "show" || body["monitored"] != true || body["rootFolder"] != "/tv" ||
		body["tvmazeId"] != float64(99001) || body["posterRef"] != "tvmaze/uploads/images/original_untouched/99/99001.jpg" {
		t.Errorf("created show = %v", body)
	}
	if eps := body["episodes"].([]any); len(eps) != 4 {
		t.Errorf("episodes = %d", len(eps))
	}
	if seasons := body["seasons"].([]any); len(seasons) != 3 {
		t.Errorf("seasons = %d", len(seasons))
	}

	// Adding it again returns it (200, not 201), also with the id as a string.
	r = owner.do("POST", "/api/v1/titles", map[string]any{"kind": "show", "provider": "tvmaze", "providerId": "99001"})
	if r.Status != http.StatusOK || idOf(t, r) != id {
		t.Errorf("second add = %d %s", r.Status, r.Body)
	}
	if n := a.up.count("/tvmaze/shows/99001"); n != 1 {
		t.Errorf("TVmaze asked %d times, want 1", n)
	}

	// An artist, with albums and tracks.
	r = owner.do("POST", "/api/v1/titles", map[string]any{"kind": "artist", "provider": "musicbrainz", "providerId": chopinMBID})
	if r.Status != http.StatusCreated {
		t.Fatalf("add artist = %d %s", r.Status, r.Body)
	}
	albums := r.JSON(t)["albums"].([]any)
	if len(albums) != 3 || r.JSON(t)["monitored"] != true { // monitored defaults to true
		t.Fatalf("artist = %s", r.Body)
	}
	var trackCount int
	for _, al := range albums {
		trackCount += len(al.(map[string]any)["tracks"].([]any))
	}
	if trackCount != 6 {
		t.Errorf("tracks = %d, want 6 (Nocturnes 3, Etudes 2, Ballades 1)", trackCount)
	}

	// A movie needs the key.
	if r := owner.do("POST", "/api/v1/titles", map[string]any{"kind": "movie", "provider": "tmdb", "providerId": 19}); r.Status != 409 || r.errCode(t) != "tmdb_key_missing" {
		t.Errorf("movie without key: %d %s", r.Status, r.Body)
	}
	a.setTMDBKey(owner)
	r = owner.do("POST", "/api/v1/titles", map[string]any{"kind": "movie", "provider": "tmdb", "providerId": "19", "monitored": false})
	if r.Status != http.StatusCreated {
		t.Fatalf("add movie = %d %s", r.Status, r.Body)
	}
	mv := r.JSON(t)
	if mv["name"] != "Metropolis" || mv["monitored"] != false || mv["certification"] != "NR" || mv["trailerKey"] != "trailerKEY1" ||
		mv["imdbId"] != "tt0017136" || mv["runtimeMin"] != float64(153) {
		t.Errorf("movie = %v", mv)
	}
	movieID := idOf(t, r)

	// The list: kinds, search, sort, paging.
	r = owner.send("GET", "/api/v1/titles", nil)
	list := r.JSON(t)
	if r.Status != 200 || list["total"] != float64(3) || len(list["items"].([]any)) != 3 || list["limit"] != float64(50) {
		t.Fatalf("list = %d %s", r.Status, r.Body)
	}
	if r.Header.Get("Cache-Control") != "no-store" {
		t.Errorf("a JSON list must not be cached: %q", r.Header.Get("Cache-Control"))
	}
	if items := list["items"].([]any); items[0].(map[string]any)["id"] != float64(movieID) {
		t.Errorf("default order should be newest first: %v", items[0])
	}
	if _, has := list["items"].([]any)[0].(map[string]any)["episodes"]; has {
		t.Error("list items must not carry children")
	}
	for path, want := range map[string]int{
		"/api/v1/titles?kind=show":                  1,
		"/api/v1/titles?kind=movie&monitored=false": 1,
		"/api/v1/titles?monitored=true":             2,
		"/api/v1/titles?q=pioneer":                  1,
		"/api/v1/titles?q=zzz":                      0,
	} {
		if got := owner.send("GET", path, nil).JSON(t)["total"]; got != float64(want) {
			t.Errorf("%s: total %v, want %d", path, got, want)
		}
	}
	page := owner.send("GET", "/api/v1/titles?sort=name&limit=1&offset=1", nil).JSON(t)
	if len(page["items"].([]any)) != 1 || page["total"] != float64(3) || page["offset"] != float64(1) {
		t.Errorf("page = %v", page)
	}
	// "Chopin, Frédéric" < "metropolis" < "pioneer one": offset 1 is the movie.
	if page["items"].([]any)[0].(map[string]any)["name"] != "Metropolis" {
		t.Errorf("sort=name page = %v", page["items"])
	}

	// Get.
	r = owner.send("GET", titlePath(id), nil)
	if r.Status != 200 || r.JSON(t)["name"] != "Pioneer One" || len(r.JSON(t)["episodes"].([]any)) != 4 {
		t.Errorf("get = %d %s", r.Status, r.Body)
	}

	// The same title shows as "in library" in search.
	r = owner.send("GET", "/api/v1/search?q=pioneer&kind=show", nil)
	hit := results(t, group(t, r, "show"))[0].(map[string]any)
	if hit["inLibrary"] != true || hit["titleId"] != float64(id) {
		t.Errorf("search hit after add = %v", hit)
	}

	// Patch: unmonitor the show.
	r = owner.do("PATCH", titlePath(id), map[string]any{"monitored": false})
	if r.Status != 200 || r.JSON(t)["monitored"] != false {
		t.Fatalf("patch = %d %s", r.Status, r.Body)
	}
	for _, s := range r.JSON(t)["seasons"].([]any) {
		if s.(map[string]any)["monitored"] != false {
			t.Errorf("season still monitored: %v", s)
		}
	}

	// Refresh.
	callsBefore := a.up.count("/tvmaze/shows/99001")
	r = owner.do("POST", titlePath(id)+"/refresh", nil)
	if r.Status != 200 || r.JSON(t)["name"] != "Pioneer One" || r.JSON(t)["monitored"] != false {
		t.Errorf("refresh = %d %s", r.Status, r.Body)
	}
	if a.up.count("/tvmaze/shows/99001") != callsBefore+1 {
		t.Error("refresh did not ask TVmaze again")
	}

	// Delete (owner only): rows go, a second delete is a 404.
	if r := owner.do("DELETE", titlePath(id), nil); r.Status != 204 || len(r.Body) != 0 {
		t.Errorf("delete = %d %s", r.Status, r.Body)
	}
	if r := owner.send("GET", titlePath(id), nil); r.Status != 404 || r.errCode(t) != "not_found" {
		t.Errorf("get after delete = %d %s", r.Status, r.Body)
	}
	if r := owner.do("DELETE", titlePath(id), nil); r.Status != 404 {
		t.Errorf("second delete = %d", r.Status)
	}
	if got := owner.send("GET", "/api/v1/titles", nil).JSON(t)["total"]; got != float64(2) {
		t.Errorf("total after delete = %v", got)
	}
}

// ---- permissions -----------------------------------------------------------

func TestTitlesNeedSignIn(t *testing.T) {
	a := newCatalogApp(t)
	c := a.newClient()
	for _, tc := range []struct{ method, path string }{
		{"GET", "/api/v1/search?q=x"},
		{"GET", "/api/v1/titles"},
		{"GET", "/api/v1/titles/1"},
		{"GET", "/api/v1/metadata/status"},
		{"GET", "/api/v1/artwork/tmdb/w500/metropolis1927.jpg"},
		{"POST", "/api/v1/titles"},
		{"PATCH", "/api/v1/titles/1"},
		{"DELETE", "/api/v1/titles/1"},
		{"POST", "/api/v1/titles/1/refresh"},
	} {
		r := c.do(tc.method, tc.path, map[string]any{})
		if r.Status != 401 || r.errCode(t) != "unauthorized" {
			t.Errorf("%s %s signed out = %d %s", tc.method, tc.path, r.Status, r.Body)
		}
	}
	// Writes also need the CSRF header, like the rest of the API.
	if r := c.send("POST", "/api/v1/titles", map[string]any{}); r.Status != 403 || r.errCode(t) != "csrf" {
		t.Errorf("POST without X-Marquee = %d %s", r.Status, r.Body)
	}
	if a.up.total() != 0 {
		t.Errorf("a signed-out request reached a provider (%d calls)", a.up.total())
	}
}

func TestRolesOnTitleWrites(t *testing.T) {
	a := newCatalogApp(t)
	owner := a.owner()
	member := a.member(owner, "Friend")
	admin := a.admin(owner, "Helper")

	add := map[string]any{"kind": "show", "provider": "tvmaze", "providerId": 99001}

	// A member can read everything but change nothing.
	for _, tc := range []struct {
		method, path string
		body         any
	}{
		{"POST", "/api/v1/titles", add},
		{"PATCH", "/api/v1/titles/1", map[string]any{"monitored": false}},
		{"DELETE", "/api/v1/titles/1", nil},
		{"POST", "/api/v1/titles/1/refresh", nil},
	} {
		r := member.do(tc.method, tc.path, tc.body)
		if r.Status != 403 || r.errCode(t) != "forbidden" {
			t.Errorf("member %s %s = %d %s", tc.method, tc.path, r.Status, r.Body)
		}
	}
	if a.up.total() != 0 {
		t.Error("a forbidden add reached a provider")
	}

	// An admin adds, changes and refreshes, but cannot delete.
	r := admin.do("POST", "/api/v1/titles", add)
	if r.Status != 201 {
		t.Fatalf("admin add = %d %s", r.Status, r.Body)
	}
	id := idOf(t, r)
	if r := admin.do("PATCH", titlePath(id), map[string]any{"monitored": false}); r.Status != 200 {
		t.Errorf("admin patch = %d %s", r.Status, r.Body)
	}
	if r := admin.do("POST", titlePath(id)+"/refresh", nil); r.Status != 200 {
		t.Errorf("admin refresh = %d %s", r.Status, r.Body)
	}
	if r := admin.do("DELETE", titlePath(id), nil); r.Status != 403 || r.errCode(t) != "forbidden" {
		t.Errorf("admin delete = %d %s", r.Status, r.Body)
	}

	// Members and admins read.
	for name, c := range map[string]*client{"member": member, "admin": admin} {
		if r := c.send("GET", titlePath(id), nil); r.Status != 200 {
			t.Errorf("%s get = %d", name, r.Status)
		}
		if r := c.send("GET", "/api/v1/titles", nil); r.Status != 200 || r.JSON(t)["total"] != float64(1) {
			t.Errorf("%s list = %d %s", name, r.Status, r.Body)
		}
	}

	// The owner deletes.
	if r := owner.do("DELETE", titlePath(id), nil); r.Status != 204 {
		t.Errorf("owner delete = %d %s", r.Status, r.Body)
	}
}

func TestAPIKeyScopesOnTitles(t *testing.T) {
	a := newCatalogApp(t)
	owner := a.owner()

	seeOnly := a.key(owner, "see", "ask")
	if r := seeOnly.send("GET", "/api/v1/titles", nil); r.Status != 200 {
		t.Errorf("see key list = %d %s", r.Status, r.Body)
	}
	if r := seeOnly.send("GET", "/api/v1/search?q=pioneer&kind=show", nil); r.Status != 200 {
		t.Errorf("see key search = %d %s", r.Status, r.Body)
	}
	add := map[string]any{"kind": "show", "provider": "tvmaze", "providerId": 99001}
	if r := seeOnly.send("POST", "/api/v1/titles", add); r.Status != 403 || r.errCode(t) != "insufficient_scope" {
		t.Errorf("see key add = %d %s", r.Status, r.Body)
	}

	// Claude can see and ask, but never manage or delete without those ticks.
	manager := a.key(owner, "see", "admin")
	r := manager.send("POST", "/api/v1/titles", add)
	if r.Status != 201 {
		t.Fatalf("admin key add = %d %s", r.Status, r.Body)
	}
	id := idOf(t, r)
	if r := manager.send("DELETE", titlePath(id), nil); r.Status != 403 || r.errCode(t) != "insufficient_scope" {
		t.Errorf("key without delete = %d %s", r.Status, r.Body)
	}
	deleter := a.key(owner, "see", "delete")
	if r := deleter.send("DELETE", titlePath(id), nil); r.Status != 204 {
		t.Errorf("key with delete = %d %s", r.Status, r.Body)
	}
	// A key with no "see" cannot read.
	noSee := a.key(owner, "ask")
	if r := noSee.send("GET", "/api/v1/titles", nil); r.Status != 403 || r.errCode(t) != "insufficient_scope" {
		t.Errorf("key without see = %d %s", r.Status, r.Body)
	}
}

// ---- errors ----------------------------------------------------------------

func TestAddValidationAndErrors(t *testing.T) {
	a := newCatalogApp(t)
	owner := a.owner()

	post := func(body any) reply { return owner.do("POST", "/api/v1/titles", body) }
	for name, tc := range map[string]struct {
		body   any
		status int
		code   string
	}{
		"unknown kind":        {map[string]any{"kind": "book", "provider": "tmdb", "providerId": 1}, 400, "invalid_request"},
		"missing kind":        {map[string]any{"provider": "tmdb", "providerId": 1}, 400, "invalid_request"},
		"wrong provider":      {map[string]any{"kind": "show", "provider": "tmdb", "providerId": 1}, 400, "invalid_request"},
		"missing id":          {map[string]any{"kind": "show", "provider": "tvmaze"}, 400, "invalid_request"},
		"id not a number":     {map[string]any{"kind": "show", "provider": "tvmaze", "providerId": "abc"}, 400, "invalid_request"},
		"id negative":         {map[string]any{"kind": "show", "provider": "tvmaze", "providerId": -5}, 400, "invalid_request"},
		"id a fraction":       {map[string]any{"kind": "show", "provider": "tvmaze", "providerId": 12.5}, 400, "invalid_request"},
		"id an object":        {map[string]any{"kind": "show", "provider": "tvmaze", "providerId": map[string]any{"a": 1}}, 400, "invalid_json"},
		"artist id not mbid":  {map[string]any{"kind": "artist", "provider": "musicbrainz", "providerId": "chopin"}, 400, "invalid_request"},
		"folder too long":     {map[string]any{"kind": "show", "provider": "tvmaze", "providerId": 99001, "rootFolder": strings.Repeat("x", 2000)}, 400, "invalid_request"},
		"unknown at provider": {map[string]any{"kind": "show", "provider": "tvmaze", "providerId": 123456}, 404, "not_found"},
		"not a body":          {"just a string", 400, "invalid_json"},
	} {
		r := post(tc.body)
		if r.Status != tc.status || r.errCode(t) != tc.code {
			t.Errorf("%s: %d %s, want %d %s", name, r.Status, r.Body, tc.status, tc.code)
		}
	}
	// The body can be missing altogether.
	if r := owner.do("POST", "/api/v1/titles", nil); r.Status != 400 || r.errCode(t) != "invalid_json" {
		t.Errorf("no body: %d %s", r.Status, r.Body)
	}
	if got := owner.send("GET", "/api/v1/titles", nil).JSON(t)["total"]; got != float64(0) {
		t.Errorf("a failed add saved something: total %v", got)
	}

	// Provider outage: 502 with its own code, nothing saved.
	a.up.handle("/tvmaze/shows/99001", code(503))
	if r := post(map[string]any{"kind": "show", "provider": "tvmaze", "providerId": 99001}); r.Status != 502 || r.errCode(t) != "upstream_error" {
		t.Errorf("provider down: %d %s", r.Status, r.Body)
	}
	// TMDB rejecting the owner's key is a 409 with its code, never a 401.
	a.setTMDBKey(owner)
	a.up.handle("/tmdb/3/movie/19", code(401))
	if r := post(map[string]any{"kind": "movie", "provider": "tmdb", "providerId": 19}); r.Status != 409 || r.errCode(t) != "tmdb_key_invalid" {
		t.Errorf("bad TMDB key: %d %s", r.Status, r.Body)
	}
	// An error body is the standard shape.
	r := post(map[string]any{"kind": "book"})
	if e, _ := r.JSON(t)["error"].(map[string]any); e["code"] == nil || e["message"] == nil || len(r.JSON(t)) != 1 {
		t.Errorf("error shape = %s", r.Body)
	}
}

func TestTitleEndpointsRejectBadInput(t *testing.T) {
	a := newCatalogApp(t)
	owner := a.owner()

	for _, path := range []string{"/api/v1/titles/abc", "/api/v1/titles/0", "/api/v1/titles/-1", "/api/v1/titles/99999", "/api/v1/titles/1.5"} {
		if r := owner.send("GET", path, nil); r.Status != 404 || r.errCode(t) != "not_found" {
			t.Errorf("GET %s = %d %s", path, r.Status, r.Body)
		}
	}
	if r := owner.do("PATCH", "/api/v1/titles/99999", map[string]any{"monitored": true}); r.Status != 404 {
		t.Errorf("patch unknown = %d", r.Status)
	}
	if r := owner.do("POST", "/api/v1/titles/99999/refresh", nil); r.Status != 404 {
		t.Errorf("refresh unknown = %d", r.Status)
	}
	if r := owner.do("DELETE", "/api/v1/titles/99999", nil); r.Status != 404 {
		t.Errorf("delete unknown = %d", r.Status)
	}
	// A patch needs a monitored value.
	r := owner.do("POST", "/api/v1/titles", map[string]any{"kind": "show", "provider": "tvmaze", "providerId": 99001})
	id := idOf(t, r)
	for name, body := range map[string]any{"empty": map[string]any{}, "wrong type": map[string]any{"monitored": "yes"}} {
		if r := owner.do("PATCH", titlePath(id), body); r.Status != 400 {
			t.Errorf("patch %s = %d %s", name, r.Status, r.Body)
		}
	}

	for _, tc := range []struct{ query, code string }{
		{"limit=abc", "invalid_request"}, {"offset=-1", "invalid_request"}, {"offset=x", "invalid_request"},
		{"sort=random", "invalid_request"}, {"kind=book", "invalid_request"}, {"monitored=maybe", "invalid_request"},
	} {
		if r := owner.send("GET", "/api/v1/titles?"+tc.query, nil); r.Status != 400 || r.errCode(t) != tc.code {
			t.Errorf("list ?%s = %d %s", tc.query, r.Status, r.Body)
		}
	}
	// A huge limit is clamped, not refused.
	if got := owner.send("GET", "/api/v1/titles?limit=99999", nil).JSON(t)["limit"]; got != float64(200) {
		t.Errorf("limit = %v, want 200", got)
	}
}

// ---- metadata status -------------------------------------------------------

func TestMetadataStatus(t *testing.T) {
	a := newCatalogApp(t)
	owner := a.owner()
	member := a.member(owner, "Friend")

	r := member.send("GET", "/api/v1/metadata/status", nil)
	if r.Status != 200 {
		t.Fatalf("status = %d %s", r.Status, r.Body)
	}
	st := r.JSON(t)
	if st["hasKey"] != false {
		t.Errorf("hasKey = %v with no key", st["hasKey"])
	}
	provs, _ := st["providers"].(map[string]any)
	for _, p := range []string{"tmdb", "tvmaze", "musicbrainz", "artwork"} {
		e, ok := provs[p].(map[string]any)
		if !ok || e["lastError"] != "" {
			t.Errorf("provider %s = %v", p, provs[p])
		}
	}

	// A provider failure shows up as its last error.
	a.up.handle("/tvmaze/search/shows", code(500))
	owner.send("GET", "/api/v1/search?q=x&kind=show", nil)
	provs = member.send("GET", "/api/v1/metadata/status", nil).JSON(t)["providers"].(map[string]any)
	if e := provs["tvmaze"].(map[string]any); !strings.Contains(e["lastError"].(string), "upstream_error") || e["lastErrorAt"] == nil {
		t.Errorf("tvmaze status = %v", e)
	}

	// Saving a key flips hasKey, and the key itself never appears.
	a.setTMDBKey(owner)
	r = member.send("GET", "/api/v1/metadata/status", nil)
	if r.JSON(t)["hasKey"] != true {
		t.Errorf("hasKey after saving a key = %v", r.JSON(t)["hasKey"])
	}
	if strings.Contains(string(r.Body), testTMDB) {
		t.Error("the TMDB key is in the status response")
	}
}

// ---- artwork proxy ---------------------------------------------------------

func TestArtworkServesCachedImagesWithCacheHeaders(t *testing.T) {
	a := newCatalogApp(t)
	owner := a.owner()
	member := a.member(owner, "Friend")
	path := "/api/v1/artwork/tmdb/w500/metropolis1927.jpg"

	r := member.send("GET", path, nil)
	if r.Status != 200 || !bytes.Equal(r.Body, jpegImage) {
		t.Fatalf("artwork = %d (%d bytes)", r.Status, len(r.Body))
	}
	if got := r.Header.Get("Content-Type"); got != "image/jpeg" {
		t.Errorf("Content-Type = %q", got)
	}
	if got := r.Header.Get("Cache-Control"); got != "public, max-age=604800" {
		t.Errorf("Cache-Control = %q", got)
	}
	etag := r.Header.Get("ETag")
	if etag == "" || r.Header.Get("X-Content-Type-Options") != "nosniff" {
		t.Errorf("ETag %q, nosniff %q", etag, r.Header.Get("X-Content-Type-Options"))
	}

	// Again, from the disk cache (and for another user).
	r2 := owner.send("GET", path, nil)
	if r2.Status != 200 || !bytes.Equal(r2.Body, jpegImage) {
		t.Fatalf("second fetch = %d", r2.Status)
	}
	if n := a.up.count("/img/tmdb/w500/metropolis1927.jpg"); n != 1 {
		t.Errorf("image host asked %d times, want 1", n)
	}
	// The file is in <config>/cache/artwork under a sha256 name.
	entries, err := os.ReadDir(a.artDir)
	if err != nil || len(entries) != 1 || len(entries[0].Name()) != 64 {
		t.Errorf("cache dir = %v, %v", entries, err)
	}

	// Conditional and range requests.
	if r := member.send("GET", path, nil, "If-None-Match", etag); r.Status != 304 {
		t.Errorf("If-None-Match = %d, want 304", r.Status)
	}
	if r := member.send("GET", path, nil, "Range", "bytes=0-9"); r.Status != 206 || len(r.Body) != 10 {
		t.Errorf("Range = %d with %d bytes", r.Status, len(r.Body))
	}
	if r := member.send("HEAD", path, nil); r.Status != 200 || len(r.Body) != 0 || r.Header.Get("Content-Length") != strconv.Itoa(len(jpegImage)) {
		t.Errorf("HEAD = %d, %d bytes, Content-Length %q", r.Status, len(r.Body), r.Header.Get("Content-Length"))
	}
}

func TestArtworkFromEveryProviderAndByAPIKey(t *testing.T) {
	a := newCatalogApp(t)
	owner := a.owner()
	key := a.key(owner, "see")
	for _, tc := range []struct{ path, ct string }{
		{"/api/v1/artwork/tvmaze/uploads/images/original_untouched/99/99001.jpg", "image/jpeg"},
		{"/api/v1/artwork/caa/release-group/" + nocturnes + "/front-500", "image/png"},
	} {
		r := key.send("GET", tc.path, nil)
		if r.Status != 200 || r.Header.Get("Content-Type") != tc.ct {
			t.Errorf("%s = %d %q", tc.path, r.Status, r.Header.Get("Content-Type"))
		}
	}
	// An API key without "see" is refused.
	if r := a.key(owner, "ask").send("GET", "/api/v1/artwork/tvmaze/uploads/images/original_untouched/99/99001.jpg", nil); r.Status != 403 {
		t.Errorf("key without see = %d", r.Status)
	}
}

func TestArtworkRefusesAnythingOffTheAllowlist(t *testing.T) {
	a := newCatalogApp(t)
	owner := a.owner()
	evilHost := strings.TrimPrefix(a.evil.srv.URL, "http://")

	for _, tc := range []struct {
		name, path string
		status     int
		code       string
	}{
		{"unknown provider", "/api/v1/artwork/evil/x.jpg", 404, "not_found"},
		{"a host as provider", "/api/v1/artwork/" + evilHost + "/x.jpg", 404, "not_found"},
		{"the real host names", "/api/v1/artwork/image.tmdb.org/t/p/w500/x.jpg", 404, "not_found"},
		{"no path", "/api/v1/artwork/tmdb/", 400, "invalid_artwork"},
		{"bad extension", "/api/v1/artwork/tmdb/w500/x.svg", 400, "invalid_artwork"},
		{"extra segment", "/api/v1/artwork/tmdb/w500/a/b.jpg", 400, "invalid_artwork"},
		{"userinfo trick", "/api/v1/artwork/tmdb/w500/@" + evilHost + "/x.jpg", 400, "invalid_artwork"},
		{"query smuggling", "/api/v1/artwork/tmdb/w500/x.jpg%3Fhost=" + evilHost, 400, "invalid_artwork"},
		{"encoded slash traversal", "/api/v1/artwork/tmdb/w500/..%2f..%2fx.jpg", 0, ""},
		{"encoded dot traversal", "/api/v1/artwork/tvmaze/uploads/images/%2e%2e/%2e%2e/x.jpg", 0, ""},
		{"plain traversal", "/api/v1/artwork/tvmaze/uploads/images/../../x.jpg", 0, ""},
		{"backslashes", "/api/v1/artwork/tmdb/w500%5C..%5Cx.jpg", 400, "invalid_artwork"},
		{"tvmaze outside uploads", "/api/v1/artwork/tvmaze/secret/x.jpg", 400, "invalid_artwork"},
		{"caa not a uuid", "/api/v1/artwork/caa/release-group/not-a-uuid/front-500", 400, "invalid_artwork"},
		{"caa other path", "/api/v1/artwork/caa/release-group/" + nocturnes + "/back", 400, "invalid_artwork"},
	} {
		r := owner.send("GET", tc.path, nil)
		if tc.status == 0 {
			// The router may tidy the path with a redirect; whatever it does,
			// the image is not served.
			if r.Status == 200 {
				t.Errorf("%s: served an image for %s", tc.name, tc.path)
			}
			continue
		}
		if r.Status != tc.status || r.errCode(t) != tc.code {
			t.Errorf("%s: %d %s, want %d %s", tc.name, r.Status, r.Body, tc.status, tc.code)
		}
	}
	if a.evil.total() != 0 {
		t.Errorf("a refused address still reached the other host (%d requests)", a.evil.total())
	}
	if a.up.total() != 0 {
		t.Errorf("refused addresses reached the image host (%d requests)", a.up.total())
	}
}

func TestArtworkUpstreamProblems(t *testing.T) {
	a := newCatalogApp(t)
	owner := a.owner()

	if r := owner.send("GET", "/api/v1/artwork/tmdb/w500/gone.jpg", nil); r.Status != 404 || r.errCode(t) != "not_found" {
		t.Errorf("missing image = %d %s", r.Status, r.Body)
	}
	if r := owner.send("GET", "/api/v1/artwork/tmdb/w500/page.jpg", nil); r.Status != 502 || r.errCode(t) != "upstream_error" {
		t.Errorf("non-image answer = %d %s", r.Status, r.Body)
	}
	if entries, _ := os.ReadDir(a.artDir); len(entries) != 0 {
		t.Errorf("failed fetches left %d files", len(entries))
	}
	// Asking for the missing image again does not ask the host again.
	owner.send("GET", "/api/v1/artwork/tmdb/w500/gone.jpg", nil)
	if n := a.up.count("/img/tmdb/w500/gone.jpg"); n != 1 {
		t.Errorf("missing image fetched %d times", n)
	}
	if st := owner.send("GET", "/api/v1/metadata/status", nil).JSON(t)["providers"].(map[string]any)["artwork"].(map[string]any); st["lastError"] == "" {
		t.Errorf("artwork status should show the bad answer: %v", st)
	}
}

// ---- wiring ----------------------------------------------------------------

func TestWithoutCatalogServicesTheRoutesAreJSON404(t *testing.T) {
	a := newApp(t, nil) // the plain test server has no Titles or Metadata
	owner := a.owner()
	for _, path := range []string{"/api/v1/titles", "/api/v1/search?q=x", "/api/v1/metadata/status", "/api/v1/artwork/tmdb/w500/x.jpg"} {
		r := owner.send("GET", path, nil)
		if r.Status != 404 || r.errCode(t) != "not_found" {
			t.Errorf("%s = %d %s", path, r.Status, r.Body)
		}
	}
}

func TestSearchRunsProvidersInParallel(t *testing.T) {
	a := newCatalogApp(t)
	owner := a.owner()
	a.setTMDBKey(owner)

	// Every provider holds its answer until all three have been asked. If the
	// search went one after another this would never complete.
	var wg sync.WaitGroup
	wg.Add(3)
	release := make(chan struct{})
	go func() { wg.Wait(); close(release) }()
	gate := func(inner http.HandlerFunc) http.HandlerFunc {
		return func(w http.ResponseWriter, r *http.Request) {
			wg.Done()
			select {
			case <-release:
			case <-time.After(5 * time.Second):
			}
			inner(w, r)
		}
	}
	a.up.handle("/tmdb/3/search/movie", gate(fixtureFile(t, "tmdb_search_movie_metropolis.json")))
	a.up.handle("/tvmaze/search/shows", gate(fixtureFile(t, "tvmaze_search_pioneer.json")))
	a.up.handle("/mb/ws/2/artist", gate(fixtureFile(t, "mb_artist_search_chopin.json")))

	start := time.Now()
	r := owner.send("GET", "/api/v1/search?q=anything&kind=all", nil)
	if r.Status != 200 || time.Since(start) > 4*time.Second {
		t.Fatalf("search took %v (status %d): the providers were not asked in parallel", time.Since(start), r.Status)
	}
	for _, k := range []string{"movie", "show", "artist"} {
		if len(results(t, group(t, r, k))) == 0 {
			t.Errorf("%s group is empty", k)
		}
	}
}
