package mcp

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
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
	"github.com/BaileyPillon/marquee/internal/core/secrets"
	"github.com/BaileyPillon/marquee/internal/downloads"
	"github.com/BaileyPillon/marquee/internal/grab"
	"github.com/BaileyPillon/marquee/internal/library"
	"github.com/BaileyPillon/marquee/internal/metadata"
	"github.com/BaileyPillon/marquee/internal/requests"
	"github.com/BaileyPillon/marquee/internal/settings"
	"github.com/BaileyPillon/marquee/internal/sources"
	"github.com/BaileyPillon/marquee/internal/titles"
)

func bg() context.Context { return context.Background() }

func quietLog() *slog.Logger { return slog.New(slog.NewTextHandler(io.Discard, nil)) }

var lightParams = &accounts.PasswordParams{Time: 1, Memory: 64, Threads: 1, KeyLen: 32, SaltLen: 16}

// t0 is the test clock's start.
var t0 = time.Date(2026, 11, 20, 12, 0, 0, 0, time.UTC)

type testClock struct {
	mu sync.Mutex
	t  time.Time
}

func (c *testClock) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.t
}

func (c *testClock) Advance(d time.Duration) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.t = c.t.Add(d)
}

// ---- fake providers ------------------------------------------------------------

// fakeMovies knows Metropolis (19) and Nosferatu (20); every other number is
// "Movie N". 404 does not exist. err, when set, fails every call.
type fakeMovies struct {
	mu  sync.Mutex
	err error
}

func (f *fakeMovies) setErr(err error) {
	f.mu.Lock()
	f.err = err
	f.mu.Unlock()
}

func (f *fakeMovies) failure() error {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.err
}

func (f *fakeMovies) SearchMovies(_ context.Context, q string, _ int) ([]metadata.SearchHit, error) {
	if err := f.failure(); err != nil {
		return nil, err
	}
	var out []metadata.SearchHit
	for _, m := range []struct {
		id   string
		name string
		year int
	}{{"19", "Metropolis", 1927}, {"20", "Nosferatu", 1922}} {
		if strings.Contains(strings.ToLower(m.name), strings.ToLower(q)) {
			out = append(out, metadata.SearchHit{Kind: "movie", Provider: "tmdb", ProviderID: m.id, Name: m.name, Year: m.year,
				Overview: "A silent film."})
		}
	}
	return out, nil
}

func (f *fakeMovies) Movie(_ context.Context, id string) (*metadata.MovieInfo, error) {
	if err := f.failure(); err != nil {
		return nil, err
	}
	n, err := strconv.ParseInt(id, 10, 64)
	if err != nil || n == 404 {
		return nil, metadata.ErrNotFound
	}
	m := &metadata.MovieInfo{TMDBID: n, Title: "Movie " + id, Year: 1990 + int(n%30)}
	switch n {
	case 19:
		m.Title, m.Year = "Metropolis", 1927
	case 20:
		m.Title, m.Year = "Nosferatu", 1922
	}
	return m, nil
}

// fakeShows: 99001 is Pioneer One (a special, two episodes in season 1, two in
// season 2 where one has not aired); 99002 is Weekly Show with episodes that
// air one, three and ten days after t0.
type fakeShows struct{}

func (fakeShows) SearchShows(_ context.Context, q string) ([]metadata.SearchHit, error) {
	if strings.Contains(strings.ToLower("pioneer one"), strings.ToLower(q)) {
		return []metadata.SearchHit{{Kind: "show", Provider: "tvmaze", ProviderID: "99001", Name: "Pioneer One", Year: 2010}}, nil
	}
	return nil, nil
}

func (fakeShows) ShowByIMDb(context.Context, string) (*metadata.SearchHit, error) {
	return nil, metadata.ErrNotFound
}

func day(n int) string { return t0.AddDate(0, 0, n).Format("2006-01-02") }

func (fakeShows) Show(_ context.Context, id string) (*metadata.ShowInfo, error) {
	n, err := strconv.ParseInt(id, 10, 64)
	if err != nil || n == 404 {
		return nil, metadata.ErrNotFound
	}
	switch n {
	case 99001:
		return &metadata.ShowInfo{
			TVmazeID: 99001, Name: "Pioneer One", Year: 2010, Status: "ended",
			Seasons: []metadata.SeasonInfo{{Number: 0, EpisodeCount: 1}, {Number: 1, EpisodeCount: 2}, {Number: 2, EpisodeCount: 2}},
			Episodes: []metadata.EpisodeInfo{
				{Season: 0, Number: 1, Name: "Behind the Scenes", AirDate: "2010-07-10", Special: true},
				{Season: 1, Number: 1, Name: "Earthfall", AirDate: "2010-06-19"},
				{Season: 1, Number: 2, Name: "Anomalies", AirDate: "2010-07-03"},
				{Season: 2, Number: 1, Name: "Landing Zone", AirDate: "2011-01-30"},
				{Season: 2, Number: 2, Name: "Not Yet", AirDate: "2099-01-01"},
			},
		}, nil
	case 99002:
		return &metadata.ShowInfo{
			TVmazeID: 99002, Name: "Weekly Show", Year: 2026, Status: "running",
			Seasons: []metadata.SeasonInfo{{Number: 1, EpisodeCount: 4}},
			Episodes: []metadata.EpisodeInfo{
				{Season: 1, Number: 1, Name: "Yesterday", AirDate: day(-1)},
				{Season: 1, Number: 2, Name: "Tomorrow", AirDate: day(1)},
				{Season: 1, Number: 3, Name: "Midweek", AirDate: day(3)},
				{Season: 1, Number: 4, Name: "Far Away", AirDate: day(10)},
			},
		}, nil
	}
	return &metadata.ShowInfo{
		TVmazeID: n, Name: "Show " + id, Year: 2000, Status: "ended",
		Seasons:  []metadata.SeasonInfo{{Number: 1, EpisodeCount: 1}},
		Episodes: []metadata.EpisodeInfo{{Season: 1, Number: 1, Name: "Pilot", AirDate: "2001-01-01"}},
	}, nil
}

const chopinMBID = "a1b2c3d4-0000-4000-8000-000000000001"

type fakeArtists struct{}

func (fakeArtists) SearchArtists(_ context.Context, q string) ([]metadata.SearchHit, error) {
	if strings.Contains(strings.ToLower("frédéric chopin"), strings.ToLower(q)) {
		return []metadata.SearchHit{{Kind: "artist", Provider: "musicbrainz", ProviderID: chopinMBID, Name: "Frédéric Chopin", Year: 1810}}, nil
	}
	return nil, nil
}

func (fakeArtists) Artist(_ context.Context, id string) (*metadata.ArtistInfo, error) {
	if id == chopinMBID {
		return &metadata.ArtistInfo{MBID: chopinMBID, Name: "Frédéric Chopin", Year: 1810, Albums: []metadata.AlbumInfo{
			{MBID: "b0000000-0000-4000-8000-000000000101", Title: "Nocturnes", Year: 1998, Type: "album",
				Tracks: []metadata.TrackInfo{{Disc: 1, Position: 1, Title: "Nocturne 1"}, {Disc: 1, Position: 2, Title: "Nocturne 2"}}},
		}}, nil
	}
	return nil, metadata.ErrNotFound
}

// stubAdapter is a download client that does nothing.
type stubAdapter struct{}

func (stubAdapter) Test(context.Context) (string, error)                    { return "stub 1.0", nil }
func (stubAdapter) Add(context.Context, downloads.AddInput) (string, error) { return "ref-1", nil }
func (stubAdapter) List(context.Context, string) ([]downloads.Item, error)  { return nil, nil }
func (stubAdapter) Remove(context.Context, string, bool) error              { return nil }
func (stubAdapter) Pause(context.Context, string) error                     { return nil }
func (stubAdapter) Resume(context.Context, string) error                    { return nil }

// catalogSource lets the library match files to the catalog's titles.
type catalogSource struct{ cat *titles.Service }

func (c catalogSource) Candidates(ctx context.Context, kind string) ([]library.Candidate, error) {
	list, err := c.cat.Candidates(ctx, kind)
	if err != nil {
		return nil, err
	}
	out := make([]library.Candidate, len(list))
	for i, x := range list {
		out[i] = library.Candidate{ID: x.ID, Kind: x.Kind, Name: x.Name, Year: x.Year, AltNames: x.AltNames}
	}
	return out, nil
}

func (c catalogSource) EpisodeID(ctx context.Context, titleID int64, season, number int) (int64, bool, error) {
	return c.cat.EpisodeID(ctx, titleID, season, number)
}

// ---- the rig -----------------------------------------------------------------------

// rig is a whole Marquee on a temp dir, with the MCP server behind httptest.
type rig struct {
	t      *testing.T
	db     *sql.DB
	srv    *httptest.Server
	mcp    *Server
	acc    *accounts.Service
	cfg    *settings.Service
	cat    *titles.Service
	lib    *library.Service
	req    *requests.Service
	dl     *downloads.Service
	src    *sources.Service
	clock  *testClock
	movies *fakeMovies

	owner, admin, mia, noah accounts.User
	moviesDir, movies4kDir  string
	showsDir                string
	moviesFolder            library.Folder
	movies4kFolder          library.Folder
	showsFolder             library.Folder
}

type rigOpt func(*Deps)

func newRig(t *testing.T, opts ...rigOpt) *rig {
	t.Helper()
	dir := t.TempDir()
	d, err := db.Open(filepath.Join(dir, db.MainFile))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { d.Close() })
	if _, err := db.Migrate(bg(), d, db.SetMain); err != nil {
		t.Fatal(err)
	}
	log := quietLog()
	box, err := secrets.LoadOrCreate(filepath.Join(dir, "marquee.key"), log)
	if err != nil {
		t.Fatal(err)
	}
	r := &rig{t: t, db: d, clock: &testClock{t: t0}, movies: &fakeMovies{}}
	hub := events.NewHub(log)
	t.Cleanup(hub.Close)
	r.cfg = settings.New(d, box)
	r.acc = accounts.NewService(d, accounts.Options{Logger: log, PasswordParams: lightParams})
	providers := titles.Providers{Movies: r.movies, Shows: fakeShows{}, Artists: fakeArtists{}}
	r.cat = titles.NewService(d, providers, titles.Options{Logger: log})
	r.lib = library.NewService(d, library.Options{Logger: log, Hub: hub})
	t.Cleanup(r.lib.Close)
	r.lib.SetTitleSource(catalogSource{r.cat})
	r.req = requests.NewService(d, r.cat, r.lib, r.cfg, requests.Options{Logger: log, Hub: hub, Now: r.clock.Now})
	t.Cleanup(r.req.Close)
	r.dl = downloads.NewService(d, downloads.Options{
		Logger: log, Box: box, Now: r.clock.Now,
		Adapters: func(downloads.AdapterConfig) (downloads.Adapter, error) { return stubAdapter{}, nil },
	})
	r.src = sources.New(sources.NewStore(d), box, nil, sources.Options{Logger: log})

	folder := func(kind, label string) (string, library.Folder) {
		p := filepath.Join(dir, label)
		if err := os.MkdirAll(p, 0o755); err != nil {
			t.Fatal(err)
		}
		f, err := r.lib.AddFolder(bg(), kind, p, label)
		if err != nil {
			t.Fatal(err)
		}
		return p, f
	}
	r.moviesDir, r.moviesFolder = folder("movie", "Movies")
	r.movies4kDir, r.movies4kFolder = folder("movie", "Movies 4K")
	r.showsDir, r.showsFolder = folder("show", "Shows")

	login, err := r.acc.SetupOwner(bg(), "Bailey", "correct horse", accounts.ClientInfo{})
	if err != nil {
		t.Fatal(err)
	}
	r.owner = login.User
	r.admin = r.invite(accounts.RoleAdmin, "Alice")
	r.mia = r.invite(accounts.RoleMember, "Mia")
	r.noah = r.invite(accounts.RoleMember, "Noah")

	deps := Deps{
		Version: "9.9.9-test", Started: t0.Add(-3 * time.Hour), Log: log, DB: d,
		Accounts: r.acc, Settings: r.cfg, Titles: r.cat, Providers: providers, Requests: r.req,
		Downloads: r.dl, Sources: r.src, Now: r.clock.Now,
	}
	for _, o := range opts {
		o(&deps)
	}
	r.mcp = New(deps)
	r.srv = httptest.NewServer(r.handler())
	t.Cleanup(r.srv.Close)
	return r
}

// handler mounts the MCP server where main does: at /mcp and /api/v1/mcp.
func (r *rig) handler() http.Handler {
	mux := http.NewServeMux()
	mux.Handle("/mcp", r.mcp)
	mux.Handle("/api/v1/mcp", r.mcp)
	return mux
}

func (r *rig) invite(role accounts.Role, name string) accounts.User {
	r.t.Helper()
	token, _, err := r.acc.CreateInvite(bg(), r.owner, role)
	if err != nil {
		r.t.Fatal(err)
	}
	login, err := r.acc.AcceptInvite(bg(), token, name, "member password", accounts.ClientInfo{})
	if err != nil {
		r.t.Fatal(err)
	}
	return login.User
}

// key makes an API key for a person and returns its secret.
func (r *rig) key(u accounts.User, scopes ...string) string {
	r.t.Helper()
	_, secret, err := r.acc.CreateAPIKey(bg(), u, "test key", scopes)
	if err != nil {
		r.t.Fatal(err)
	}
	return secret
}

// ---- seeding ---------------------------------------------------------------------------

// addTitle adds a title to the catalog (monitored).
func (r *rig) addTitle(kind, provider, id string) *titles.Detail {
	r.t.Helper()
	d, _, err := r.cat.Add(bg(), kind, provider, id, titles.AddOptions{Monitored: true})
	if err != nil {
		r.t.Fatal(err)
	}
	return d
}

// putFile writes a small file and scans the folder, so the library matches it.
func (r *rig) putFile(dir string, f library.Folder, rel string, size int) {
	r.t.Helper()
	p := filepath.Join(dir, filepath.FromSlash(rel))
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		r.t.Fatal(err)
	}
	if err := os.WriteFile(p, bytes.Repeat([]byte("x"), size), 0o644); err != nil {
		r.t.Fatal(err)
	}
	if _, err := r.lib.ScanNow(bg(), f.ID); err != nil {
		r.t.Fatal(err)
	}
}

// seedLibrary gives the catalog Metropolis (HD file), Pioneer One (S01E01 and
// S01E02 on disk) and Weekly Show (nothing on disk).
func (r *rig) seedLibrary() (metropolis, pioneer, weekly *titles.Detail) {
	r.t.Helper()
	metropolis = r.addTitle("movie", "tmdb", "19")
	pioneer = r.addTitle("show", "tvmaze", "99001")
	weekly = r.addTitle("show", "tvmaze", "99002")
	r.putFile(r.moviesDir, r.moviesFolder, "Metropolis (1927)/Metropolis.1927.1080p.BluRay.x264-GRP.mkv", 2048)
	r.putFile(r.showsDir, r.showsFolder, "Pioneer One (2010)/Season 01/Pioneer One (2010) S01E01.mkv", 100)
	r.putFile(r.showsDir, r.showsFolder, "Pioneer One (2010)/Season 01/Pioneer One - S01E02 - Earthfall.mkv", 100)
	return
}

// seedDownload puts one download in progress.
func (r *rig) seedDownload(titleID int64) {
	r.t.Helper()
	name := "qbit"
	kind := "qbittorrent"
	url := "http://127.0.0.1:1"
	if _, err := r.dl.AddClient(bg(), downloads.ClientInput{Name: &name, Kind: &kind, URL: &url}); err != nil {
		r.t.Fatal(err)
	}
	id, err := r.dl.Grab(bg(), grab.Target{TitleID: titleID, Kind: "movie", Quality: "hd"}, grab.Release{
		Protocol: grab.ProtocolTorrent, Title: "Metropolis.1927.1080p.BluRay.x264-GRP", Size: 4 << 30,
		MagnetURL: "magnet:?xt=urn:btih:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa&dn=Metropolis",
	})
	if err != nil {
		r.t.Fatal(err)
	}
	if _, err := r.db.Exec(`UPDATE downloads SET state = 'downloading', progress = 0.425, speed_bps = 3145728, eta_seconds = 720 WHERE id = ?`, id); err != nil {
		r.t.Fatal(err)
	}
}

// ---- talking to the server --------------------------------------------------------------

type answer struct {
	Status int
	Header http.Header
	Body   []byte
}

func (r answer) JSON(t *testing.T) map[string]any {
	t.Helper()
	var m map[string]any
	if err := json.Unmarshal(r.Body, &m); err != nil {
		t.Fatalf("body is not a JSON object (%d): %q", r.Status, r.Body)
	}
	return m
}

func (r answer) JSONArray(t *testing.T) []map[string]any {
	t.Helper()
	var m []map[string]any
	if err := json.Unmarshal(r.Body, &m); err != nil {
		t.Fatalf("body is not a JSON array (%d): %q", r.Status, r.Body)
	}
	return m
}

// result is the "result" object of a JSON-RPC answer.
func (r answer) result(t *testing.T) map[string]any {
	t.Helper()
	m := r.JSON(t)
	res, _ := m["result"].(map[string]any)
	if res == nil {
		t.Fatalf("no result in %s (HTTP %d)", r.Body, r.Status)
	}
	return res
}

// rpcError is the "error" object of a JSON-RPC answer.
func (r answer) rpcError(t *testing.T) map[string]any {
	t.Helper()
	e, _ := r.JSON(t)["error"].(map[string]any)
	if e == nil {
		t.Fatalf("no error in %s (HTTP %d)", r.Body, r.Status)
	}
	if msg, _ := e["message"].(string); msg == "" {
		t.Errorf("an error without a message: %s", r.Body)
	}
	return e
}

func (r answer) errCode(t *testing.T) int {
	t.Helper()
	code, _ := r.rpcError(t)["code"].(float64)
	return int(code)
}

// send POSTs raw bytes to a path. headers are name, value pairs.
func (r *rig) send(method, path string, key string, body []byte, headers ...string) answer {
	r.t.Helper()
	req, err := http.NewRequest(method, r.srv.URL+path, bytes.NewReader(body))
	if err != nil {
		r.t.Fatal(err)
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	req.Header.Set("Accept", "application/json, text/event-stream")
	if key != "" {
		req.Header.Set("Authorization", "Bearer "+key)
	}
	for i := 0; i+1 < len(headers); i += 2 {
		req.Header.Set(headers[i], headers[i+1])
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		r.t.Fatal(err)
	}
	defer resp.Body.Close()
	b, _ := io.ReadAll(resp.Body)
	return answer{Status: resp.StatusCode, Header: resp.Header, Body: b}
}

// post sends v as JSON to /mcp.
func (r *rig) post(key string, v any, headers ...string) answer {
	r.t.Helper()
	b, err := json.Marshal(v)
	if err != nil {
		r.t.Fatal(err)
	}
	return r.send("POST", "/mcp", key, b, headers...)
}

var rpcID int

// rpc sends one JSON-RPC request to /mcp.
func (r *rig) rpc(key, method string, params any, headers ...string) answer {
	r.t.Helper()
	rpcID++
	m := map[string]any{"jsonrpc": "2.0", "id": rpcID, "method": method}
	if params != nil {
		m["params"] = params
	}
	return r.post(key, m, headers...)
}

// toolResult is what a tools/call returned.
type toolResult struct {
	Text       string
	Structured map[string]any
	IsError    bool
	Raw        map[string]any
}

// call runs a tool and returns its result; the HTTP and JSON-RPC layers must be fine.
func (r *rig) call(key, name string, args map[string]any) toolResult {
	r.t.Helper()
	if args == nil {
		args = map[string]any{}
	}
	rep := r.rpc(key, "tools/call", map[string]any{"name": name, "arguments": args})
	if rep.Status != http.StatusOK {
		r.t.Fatalf("tools/call %s: HTTP %d %s", name, rep.Status, rep.Body)
	}
	res := rep.result(r.t)
	out := toolResult{Raw: res}
	out.IsError, _ = res["isError"].(bool)
	content, _ := res["content"].([]any)
	if len(content) != 1 {
		r.t.Fatalf("tools/call %s: content = %v, want one text block", name, res["content"])
	}
	block, _ := content[0].(map[string]any)
	if block["type"] != "text" {
		r.t.Fatalf("tools/call %s: content type = %v", name, block["type"])
	}
	out.Text, _ = block["text"].(string)
	out.Structured, _ = res["structuredContent"].(map[string]any)
	if !out.IsError {
		if out.Structured == nil {
			r.t.Fatalf("tools/call %s: no structuredContent in %v", name, res)
		}
		r.checkSchema(name, out.Structured)
	}
	return out
}

// mustCall is call, and fails when the tool reports an error.
func (r *rig) mustCall(key, name string, args map[string]any) toolResult {
	r.t.Helper()
	out := r.call(key, name, args)
	if out.IsError {
		r.t.Fatalf("tools/call %s: error result: %s", name, out.Text)
	}
	return out
}

// checkSchema validates structured output against the tool's outputSchema.
func (r *rig) checkSchema(name string, v map[string]any) {
	r.t.Helper()
	tl := r.mcp.byName[name]
	if tl == nil {
		r.t.Fatalf("no tool %q", name)
	}
	var schema any
	b, _ := json.Marshal(tl.def.OutputSchema)
	_ = json.Unmarshal(b, &schema)
	if errs := validate(schema, v, name); len(errs) > 0 {
		r.t.Errorf("structuredContent of %s breaks its outputSchema:\n  %s\n  data: %v", name, strings.Join(errs, "\n  "), v)
	}
}

// validate is a small JSON Schema checker: type, enum, properties, required and
// items, which is all the tools' schemas use.
func validate(schema, v any, path string) []string {
	s, _ := schema.(map[string]any)
	if s == nil {
		return nil
	}
	var errs []string
	if t, ok := s["type"].(string); ok {
		okType := false
		switch t {
		case "object":
			_, okType = v.(map[string]any)
		case "array":
			_, okType = v.([]any)
		case "string":
			_, okType = v.(string)
		case "boolean":
			_, okType = v.(bool)
		case "number":
			_, okType = v.(float64)
		case "integer":
			f, isNum := v.(float64)
			okType = isNum && f == float64(int64(f))
		}
		if !okType {
			return []string{fmt.Sprintf("%s: want %s, got %T (%v)", path, t, v, v)}
		}
	}
	if enum, ok := s["enum"].([]any); ok {
		found := false
		for _, e := range enum {
			if e == v {
				found = true
			}
		}
		if !found {
			errs = append(errs, fmt.Sprintf("%s: %v is not one of %v", path, v, enum))
		}
	}
	if obj, ok := v.(map[string]any); ok {
		props, _ := s["properties"].(map[string]any)
		if req, ok := s["required"].([]any); ok {
			for _, k := range req {
				if _, has := obj[k.(string)]; !has {
					errs = append(errs, fmt.Sprintf("%s: missing required %q", path, k))
				}
			}
		}
		for k, val := range obj {
			if sub, ok := props[k]; ok {
				errs = append(errs, validate(sub, val, path+"."+k)...)
			}
		}
	}
	if arr, ok := v.([]any); ok {
		if items, ok := s["items"]; ok {
			for i, el := range arr {
				errs = append(errs, validate(items, el, fmt.Sprintf("%s[%d]", path, i))...)
			}
		}
	}
	return errs
}

func mustJSON(t *testing.T, v any) []byte {
	t.Helper()
	b, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	return b
}

// lockedWriter lets a log sink be shared by the server's goroutines and the test.
type lockedWriter struct {
	mu sync.Mutex
	w  io.Writer
}

func (l *lockedWriter) Write(p []byte) (int, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.w.Write(p)
}

// toolNames lists the names tools/list returns for a key.
func (r *rig) toolNames(key string) []string {
	r.t.Helper()
	rep := r.rpc(key, "tools/list", nil)
	if rep.Status != http.StatusOK {
		r.t.Fatalf("tools/list: HTTP %d %s", rep.Status, rep.Body)
	}
	var names []string
	for _, x := range rep.result(r.t)["tools"].([]any) {
		names = append(names, x.(map[string]any)["name"].(string))
	}
	return names
}
