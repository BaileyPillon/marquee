package web

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"sync"
	"testing"
	"time"

	"github.com/BaileyPillon/marquee/internal/accounts"
	"github.com/BaileyPillon/marquee/internal/core/db"
	"github.com/BaileyPillon/marquee/internal/core/events"
	"github.com/BaileyPillon/marquee/internal/core/secrets"
	"github.com/BaileyPillon/marquee/internal/library"
	"github.com/BaileyPillon/marquee/internal/metadata"
	"github.com/BaileyPillon/marquee/internal/requests"
	"github.com/BaileyPillon/marquee/internal/settings"
	"github.com/BaileyPillon/marquee/internal/titles"
)

// ---- test rig --------------------------------------------------------------

// rqMovies is every number a movie: "Movie N", from 1990 + N%30. 404 is unknown.
type rqMovies struct{}

func (rqMovies) SearchMovies(context.Context, string, int) ([]metadata.SearchHit, error) {
	return nil, nil
}

func (rqMovies) Movie(_ context.Context, id string) (*metadata.MovieInfo, error) {
	n, err := strconv.ParseInt(id, 10, 64)
	if err != nil || n == 404 {
		return nil, metadata.ErrNotFound
	}
	return &metadata.MovieInfo{TMDBID: n, Title: "Movie " + id, Year: 1990 + int(n%30),
		Poster: metadata.Image{Ref: "tmdb/w500/m" + id + ".jpg"}}, nil
}

type rqShows struct{}

func (rqShows) SearchShows(context.Context, string) ([]metadata.SearchHit, error) { return nil, nil }

func (rqShows) ShowByIMDb(context.Context, string) (*metadata.SearchHit, error) {
	return nil, metadata.ErrNotFound
}

func (rqShows) Show(_ context.Context, id string) (*metadata.ShowInfo, error) {
	n, err := strconv.ParseInt(id, 10, 64)
	if err != nil || n == 404 {
		return nil, metadata.ErrNotFound
	}
	return &metadata.ShowInfo{
		TVmazeID: n, Name: "Show " + id, Year: 2005, Status: "ended",
		Seasons: []metadata.SeasonInfo{{Number: 1, EpisodeCount: 1}, {Number: 2, EpisodeCount: 1}},
		Episodes: []metadata.EpisodeInfo{
			{Season: 1, Number: 1, Name: "One", AirDate: "2005-01-01"},
			{Season: 2, Number: 1, Name: "Two", AirDate: "2006-01-01"},
		},
	}, nil
}

type rqArtists struct{}

func (rqArtists) SearchArtists(context.Context, string) ([]metadata.SearchHit, error) {
	return nil, nil
}

func (rqArtists) Artist(context.Context, string) (*metadata.ArtistInfo, error) {
	return nil, metadata.ErrNotFound
}

// rqSearcher remembers which copies it was asked to search for.
type rqSearcher struct {
	mu    sync.Mutex
	calls []int64
	got   chan int64
}

func (s *rqSearcher) SearchCopy(_ context.Context, copyID int64) error {
	s.mu.Lock()
	s.calls = append(s.calls, copyID)
	s.mu.Unlock()
	s.got <- copyID
	return nil
}

// reqApp is a whole Marquee server with the catalog, a library and requests.
type reqApp struct {
	*app
	req      *requests.Service
	sr       *rqSearcher
	movies   string // library folder paths
	movies4k string
	shows    string
}

func newReqApp(t *testing.T) *reqApp {
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
	lib := library.NewService(mainDB, library.Options{Logger: log, Hub: hub})
	t.Cleanup(lib.Close)
	folder := func(kind, label string) string {
		p := filepath.Join(dir, label)
		if err := os.MkdirAll(p, 0o755); err != nil {
			t.Fatal(err)
		}
		if _, err := lib.AddFolder(ctx, kind, p, label); err != nil {
			t.Fatal(err)
		}
		return p
	}
	a := &reqApp{
		movies:   folder("movie", "Movies"),
		movies4k: folder("movie", "Movies 4K"),
		shows:    folder("show", "Shows"),
		sr:       &rqSearcher{got: make(chan int64, 32)},
	}
	a.req = requests.NewService(mainDB, cat, lib, cfg, requests.Options{Logger: log, Hub: hub})
	a.req.SetSearcher(a.sr)
	t.Cleanup(a.req.Close)

	s := New(Deps{
		Version: "9.9.9-test", Log: log, MainDB: mainDB, EventsDB: eventsDB,
		Accounts: accounts.NewService(mainDB, accounts.Options{Logger: log, PasswordParams: lightParams}),
		Settings: cfg, Hub: hub, Titles: cat, Library: lib, Requests: a.req, UI: testUI(),
	})
	srv := httptest.NewServer(s.Handler())
	t.Cleanup(srv.Close)
	a.app = &app{t: t, srv: srv, mainDB: mainDB, settings: cfg, hub: hub}
	return a
}

// admin creates an admin through an invite and returns a signed-in client.
func (a *reqApp) admin(owner *client, name string) *client {
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
func (a *reqApp) key(c *client, scopes ...string) keyClient {
	a.t.Helper()
	r := c.do("POST", "/api/v1/apikeys", map[string]any{"name": "test key", "scopes": scopes})
	if r.Status != http.StatusCreated {
		a.t.Fatalf("apikey = %d %s", r.Status, r.Body)
	}
	return keyClient{c: a.newClient(), secret: r.JSON(a.t)["secret"].(string)}
}

// userID asks the server who c is.
func userID(t *testing.T, c *client) int64 {
	t.Helper()
	r := c.do("GET", "/api/v1/me", nil)
	u, _ := r.JSON(t)["user"].(map[string]any)
	f, ok := u["id"].(float64)
	if !ok {
		t.Fatalf("no user id in %s", r.Body)
	}
	return int64(f)
}

// movieAsk is a request body for "Movie N".
func movieAsk(n int, quality string) map[string]any {
	return map[string]any{"kind": "movie", "provider": "tmdb", "providerId": n, "quality": quality}
}

// obj digs a JSON object out of nested keys.
func obj(t *testing.T, m map[string]any, keys ...string) map[string]any {
	t.Helper()
	for _, k := range keys {
		next, ok := m[k].(map[string]any)
		if !ok {
			t.Fatalf("no object %q in %v", k, m)
		}
		m = next
	}
	return m
}

func requestBody(t *testing.T, r reply) map[string]any {
	t.Helper()
	return obj(t, r.JSON(t), "request")
}

func requestID64(t *testing.T, r reply) int64 {
	t.Helper()
	f, ok := requestBody(t, r)["id"].(float64)
	if !ok {
		t.Fatalf("no request id in %s", r.Body)
	}
	return int64(f)
}

func reqPath(id int64) string { return "/api/v1/requests/" + strconv.FormatInt(id, 10) }

func (a *reqApp) mustAsk(c *client, body map[string]any) int64 {
	a.t.Helper()
	r := c.do("POST", "/api/v1/requests", body)
	if r.Status != http.StatusCreated {
		a.t.Fatalf("ask %v = %d %s", body, r.Status, r.Body)
	}
	return requestID64(a.t, r)
}

// ---- asking ----------------------------------------------------------------

func TestAskReturnsTheRequestAndUsage(t *testing.T) {
	a := newReqApp(t)
	owner := a.owner()

	r := owner.do("POST", "/api/v1/requests", movieAsk(19, "hd"))
	if r.Status != http.StatusCreated {
		t.Fatalf("ask = %d %s", r.Status, r.Body)
	}
	body := r.JSON(t)
	if body["autoApproved"] != true {
		t.Errorf("autoApproved = %v", body["autoApproved"])
	}
	usage := obj(t, body, "usage")
	if hd := obj(t, usage, "hd"); hd["used"] != float64(1) || hd["limit"] != nil {
		t.Errorf("usage.hd = %v, want 1 used and no limit for the owner", hd)
	}
	if uhd := obj(t, usage, "uhd"); uhd["used"] != float64(0) || uhd["limit"] != nil {
		t.Errorf("usage.uhd = %v", uhd)
	}
	req := obj(t, body, "request")
	for _, k := range []string{"id", "title", "user", "quality", "state", "note", "source", "createdAt", "decidedBy", "decidedAt", "declineReason", "facts", "scope"} {
		if _, ok := req[k]; !ok {
			t.Errorf("request has no %q: %v", k, req)
		}
	}
	if req["quality"] != "hd" || req["state"] != "approved" || req["source"] != "manual" || req["note"] != "" {
		t.Errorf("request = %v", req)
	}
	title := obj(t, req, "title")
	if title["name"] != "Movie 19" || title["year"] != float64(2009) || title["kind"] != "movie" || title["posterRef"] != "tmdb/w500/m19.jpg" || title["id"] == nil {
		t.Errorf("title = %v", title)
	}
	if u := obj(t, req, "user"); u["name"] != "Bailey" || u["id"] != float64(userID(t, owner)) {
		t.Errorf("user = %v", u)
	}
	if d := obj(t, req, "decidedBy"); d["name"] != "Bailey" || req["decidedAt"] == nil {
		t.Errorf("decidedBy = %v, decidedAt = %v", d, req["decidedAt"])
	}
	facts := obj(t, req, "facts")
	if v, ok := facts["sizeEstimate"]; !ok || v != nil || facts["folderPath"] != a.movies {
		t.Errorf("facts = %v, want a null sizeEstimate and folder %q", facts, a.movies)
	}
	if sc := obj(t, req, "scope"); len(sc) != 0 {
		t.Errorf("a movie's scope = %v, want {}", sc)
	}
	select {
	case <-a.sr.got:
	case <-time.After(3 * time.Second):
		t.Error("the searcher was not started for the approved ask")
	}

	// By title id, at 4K, with a note.
	tid := int64(title["id"].(float64))
	r = owner.do("POST", "/api/v1/requests", map[string]any{"titleId": tid, "quality": "uhd", "note": "the good print"})
	if r.Status != http.StatusCreated || requestBody(t, r)["note"] != "the good print" ||
		obj(t, requestBody(t, r), "facts")["folderPath"] != a.movies4k {
		t.Errorf("ask by id = %d %s", r.Status, r.Body)
	}
	// A show by season.
	r = owner.do("POST", "/api/v1/requests", map[string]any{
		"kind": "show", "provider": "tvmaze", "providerId": "5", "quality": "hd", "scope": map[string]any{"seasons": []int{2, 1}}})
	if r.Status != http.StatusCreated {
		t.Fatalf("show ask = %d %s", r.Status, r.Body)
	}
	if sc := obj(t, requestBody(t, r), "scope"); sc["seasons"].([]any)[0] != float64(1) || sc["all"] != nil {
		t.Errorf("show scope = %v", sc)
	}
}

func TestAskNeedsSignInAndCSRF(t *testing.T) {
	a := newReqApp(t)
	owner := a.owner()
	if r := a.newClient().do("POST", "/api/v1/requests", movieAsk(1, "hd")); r.Status != 401 || r.errCode(t) != "unauthorized" {
		t.Errorf("anonymous ask: %d %s", r.Status, r.Body)
	}
	if r := owner.send("POST", "/api/v1/requests", movieAsk(1, "hd")); r.Status != 403 || r.errCode(t) != "csrf" {
		t.Errorf("ask without the CSRF header: %d %s", r.Status, r.Body)
	}
	for _, p := range []string{"/api/v1/requests", "/api/v1/requests/summary", "/api/v1/requests/1", "/api/v1/people"} {
		if r := a.newClient().send("GET", p, nil); r.Status != 401 {
			t.Errorf("anonymous GET %s: %d", p, r.Status)
		}
	}
}

func TestAskValidation(t *testing.T) {
	a := newReqApp(t)
	owner := a.owner()
	cases := []struct {
		name   string
		body   any
		status int
		code   string
	}{
		{"no quality", map[string]any{"kind": "movie", "provider": "tmdb", "providerId": 1}, 400, "invalid_quality"},
		{"odd quality", movieAsk(1, "8k"), 400, "invalid_quality"},
		{"no title", map[string]any{"quality": "hd"}, 400, "invalid_request"},
		{"bad titleId", map[string]any{"titleId": -3, "quality": "hd"}, 400, "invalid_request"},
		{"no such titleId", map[string]any{"titleId": 999, "quality": "hd"}, 404, "not_found"},
		{"provider does not fit the kind", map[string]any{"kind": "movie", "provider": "tvmaze", "providerId": 1, "quality": "hd"}, 400, "invalid_request"},
		{"unknown provider id", movieAsk(404, "hd"), 404, "not_found"},
		{"4K music", map[string]any{"kind": "artist", "provider": "musicbrainz", "providerId": "a1b2c3d4-0000-4000-8000-000000000001", "quality": "uhd"}, 400, "invalid_quality"},
		{"scope that does not fit", map[string]any{"kind": "show", "provider": "tvmaze", "providerId": 1, "quality": "hd", "scope": map[string]any{"seasons": []int{7}}}, 400, "invalid_scope"},
		{"no body", "none", 400, "invalid_json"},
	}
	for _, c := range cases {
		var r reply
		if _, ok := c.body.(string); ok {
			// No body at all.
			r = owner.send("POST", "/api/v1/requests", nil, "X-Marquee", "1", "Content-Type", "application/json")
		} else {
			r = owner.do("POST", "/api/v1/requests", c.body)
		}
		if r.Status != c.status || r.errCode(t) != c.code {
			t.Errorf("%s: %d %s, want %d %s", c.name, r.Status, r.Body, c.status, c.code)
		}
	}
	if r := owner.do("GET", "/api/v1/requests?state=lost", nil); r.Status != 400 || r.errCode(t) != "invalid_request" {
		t.Errorf("bad state filter: %d %s", r.Status, r.Body)
	}
}

func TestMemberAsksAndTheOwnerAnswers(t *testing.T) {
	a := newReqApp(t)
	owner := a.owner()
	mia := a.member(owner, "Mia")

	// HD goes through; 4K waits.
	r := mia.do("POST", "/api/v1/requests", movieAsk(1, "hd"))
	if r.Status != 201 || r.JSON(t)["autoApproved"] != true || requestBody(t, r)["state"] != "approved" {
		t.Fatalf("member HD ask: %d %s", r.Status, r.Body)
	}
	if requestBody(t, r)["decidedBy"] != nil {
		t.Errorf("the rules approved it, but decidedBy = %v", requestBody(t, r)["decidedBy"])
	}
	r = mia.do("POST", "/api/v1/requests", movieAsk(2, "uhd"))
	if r.Status != 201 || r.JSON(t)["autoApproved"] != false || requestBody(t, r)["state"] != "pending" {
		t.Fatalf("member 4K ask: %d %s", r.Status, r.Body)
	}
	waiting := requestID64(t, r)
	if facts := obj(t, requestBody(t, r), "facts"); facts["folderPath"] != a.movies4k {
		t.Errorf("a waiting 4K ask shows folder %v, want %q", facts["folderPath"], a.movies4k)
	}
	if uhd := obj(t, r.JSON(t), "usage", "uhd"); uhd["used"] != float64(1) || uhd["limit"] != float64(2) || uhd["resetsAt"] == nil {
		t.Errorf("usage.uhd = %v", uhd)
	}

	// The owner's home shows it waiting; Mia's shows hers.
	sum := owner.do("GET", "/api/v1/requests/summary", nil).JSON(t)
	if sum["waitingForYou"] != float64(1) {
		t.Errorf("owner summary = %v", sum)
	}
	if m := obj(t, sum, "mine"); m["pending"] != float64(0) || m["approved"] != float64(0) {
		t.Errorf("owner mine = %v", m)
	}
	sum = mia.do("GET", "/api/v1/requests/summary", nil).JSON(t)
	if m := obj(t, sum, "mine"); sum["waitingForYou"] != float64(0) || m["pending"] != float64(1) || m["approved"] != float64(1) {
		t.Errorf("Mia's summary = %v", sum)
	}
	if u := obj(t, sum, "usage"); obj(t, u, "hd")["used"] != float64(1) || obj(t, u, "hd")["limit"] != float64(10) ||
		obj(t, u, "uhd")["limit"] != float64(2) {
		t.Errorf("Mia's usage = %v", u)
	}

	// Mia cannot answer. The owner approves, always allowing 4K for her.
	if r := mia.do("POST", reqPath(waiting)+"/approve", nil); r.Status != 403 || r.errCode(t) != "forbidden" {
		t.Errorf("a member approving: %d %s", r.Status, r.Body)
	}
	r = owner.do("POST", reqPath(waiting)+"/approve", map[string]any{"alwaysAllowUHD": true})
	if r.Status != 200 || requestBody(t, r)["state"] != "approved" || obj(t, requestBody(t, r), "decidedBy")["name"] != "Bailey" {
		t.Fatalf("approve: %d %s", r.Status, r.Body)
	}
	select {
	case <-a.sr.got:
	case <-time.After(3 * time.Second):
		t.Error("the searcher was not started")
	}
	r = owner.do("POST", reqPath(waiting)+"/approve", nil)
	if r.Status != 409 || r.errCode(t) != "not_pending" {
		t.Errorf("approving twice: %d %s", r.Status, r.Body)
	}
	if r := mia.do("POST", "/api/v1/requests", movieAsk(3, "uhd")); r.Status != 201 || r.JSON(t)["autoApproved"] != true {
		t.Errorf("4K after always-allow: %d %s", r.Status, r.Body)
	}
	if r := owner.do("POST", reqPath(99999)+"/approve", nil); r.Status != 404 || r.errCode(t) != "not_found" {
		t.Errorf("approving nothing: %d %s", r.Status, r.Body)
	}
	if r := owner.do("POST", "/api/v1/requests/abc/approve", nil); r.Status != 404 {
		t.Errorf("bad id: %d", r.Status)
	}
}

func TestDeclineAndCancelOverHTTP(t *testing.T) {
	a := newReqApp(t)
	owner := a.owner()
	admin := a.admin(owner, "Alice")
	mia := a.member(owner, "Mia")
	noah := a.member(owner, "Noah")

	first := a.mustAsk(mia, movieAsk(1, "uhd"))
	r := admin.do("POST", reqPath(first)+"/decline", map[string]any{"reason": "Too big."})
	if r.Status != 200 {
		t.Fatalf("decline = %d %s", r.Status, r.Body)
	}
	if got := requestBody(t, r); got["state"] != "declined" || got["declineReason"] != "Too big." || obj(t, got, "decidedBy")["name"] != "Alice" {
		t.Errorf("declined = %v", got)
	}
	// Mia sees the reason on her own request.
	if got := requestBody(t, mia.do("GET", reqPath(first), nil)); got["declineReason"] != "Too big." {
		t.Errorf("Mia's view = %v", got)
	}
	if r := mia.do("POST", reqPath(first)+"/decline", nil); r.Status != 403 {
		t.Errorf("a member declining: %d", r.Status)
	}
	if r := owner.do("POST", reqPath(first)+"/decline", nil); r.Status != 409 || r.errCode(t) != "not_pending" {
		t.Errorf("declining twice: %d %s", r.Status, r.Body)
	}
	// A body is optional.
	second := a.mustAsk(mia, movieAsk(2, "uhd"))
	if r := owner.do("POST", reqPath(second)+"/decline", nil); r.Status != 200 || requestBody(t, r)["declineReason"] != "" {
		t.Errorf("decline with no body: %d %s", r.Status, r.Body)
	}

	// Cancelling.
	third := a.mustAsk(mia, movieAsk(3, "uhd"))
	if r := noah.do("DELETE", reqPath(third), nil); r.Status != 404 {
		t.Errorf("another member cancelling: %d %s", r.Status, r.Body)
	}
	if r := admin.do("DELETE", reqPath(third), nil); r.Status != 403 {
		t.Errorf("an admin cancelling someone else's: %d %s", r.Status, r.Body)
	}
	r = mia.do("DELETE", reqPath(third), nil)
	if r.Status != 200 || requestBody(t, r)["state"] != "cancelled" {
		t.Fatalf("cancel = %d %s", r.Status, r.Body)
	}
	if r := mia.do("DELETE", reqPath(third), nil); r.Status != 409 || r.errCode(t) != "not_cancellable" {
		t.Errorf("cancelling twice: %d %s", r.Status, r.Body)
	}
	approved := a.mustAsk(mia, movieAsk(4, "hd"))
	if r := mia.do("DELETE", reqPath(approved), nil); r.Status != 409 || r.errCode(t) != "not_cancellable" {
		t.Errorf("a member cancelling an approved ask: %d %s", r.Status, r.Body)
	}
	if r := owner.do("DELETE", reqPath(approved), nil); r.Status != 200 || requestBody(t, r)["state"] != "cancelled" {
		t.Errorf("the owner cancelling an approved ask: %d %s", r.Status, r.Body)
	}
	if r := owner.do("DELETE", reqPath(99999), nil); r.Status != 404 {
		t.Errorf("cancelling nothing: %d", r.Status)
	}
}

// ---- limits and duplicates -------------------------------------------------

func TestLimitReachedBody(t *testing.T) {
	a := newReqApp(t)
	owner := a.owner()
	mia := a.member(owner, "Mia")
	// Two 4K asks a week is the default; let hers be approved so they count.
	r := owner.do("PUT", "/api/v1/people/"+strconv.FormatInt(userID(t, mia), 10)+"/limits", map[string]any{"autoApproveUHD": true})
	if r.Status != 200 {
		t.Fatalf("limits = %d %s", r.Status, r.Body)
	}
	before := time.Now()
	a.mustAsk(mia, movieAsk(1, "uhd"))
	a.mustAsk(mia, movieAsk(2, "uhd"))
	r = mia.do("POST", "/api/v1/requests", movieAsk(3, "uhd"))
	if r.Status != 429 || r.errCode(t) != "limit_reached" {
		t.Fatalf("third 4K ask = %d %s", r.Status, r.Body)
	}
	e := obj(t, r.JSON(t), "error")
	if e["used"] != float64(2) || e["limit"] != float64(2) || e["quality"] != "uhd" || e["message"] == "" {
		t.Errorf("error = %v", e)
	}
	resets, err := time.Parse(time.RFC3339Nano, e["resetsAt"].(string))
	if err != nil {
		t.Fatalf("resetsAt = %v: %v", e["resetsAt"], err)
	}
	if d := resets.Sub(before.Add(requests.Window)); d < -time.Second || d > 30*time.Second {
		t.Errorf("resetsAt = %v, want about a week after the first ask (%v)", resets, before.Add(requests.Window))
	}
	if secs, _ := strconv.Atoi(r.Header.Get("Retry-After")); secs < 604000 || secs > 604900 {
		t.Errorf("Retry-After = %q", r.Header.Get("Retry-After"))
	}
	// HD is counted apart.
	if r := mia.do("POST", "/api/v1/requests", movieAsk(3, "hd")); r.Status != 201 {
		t.Errorf("HD ask: %d %s", r.Status, r.Body)
	}
}

func TestAlreadyRequestedBody(t *testing.T) {
	a := newReqApp(t)
	owner := a.owner()
	mia := a.member(owner, "Mia")
	noah := a.member(owner, "Noah")

	first := a.mustAsk(mia, movieAsk(1, "uhd")) // waiting
	r := owner.do("POST", "/api/v1/requests", movieAsk(1, "uhd"))
	if r.Status != 409 || r.errCode(t) != "already_requested" {
		t.Fatalf("duplicate = %d %s", r.Status, r.Body)
	}
	existing := obj(t, r.JSON(t), "error", "request")
	if existing["id"] != float64(first) || existing["state"] != "pending" || obj(t, existing, "user")["name"] != "Mia" {
		t.Errorf("the owner is shown %v", existing)
	}
	// Noah is told it exists but not whose it is.
	r = noah.do("POST", "/api/v1/requests", movieAsk(1, "uhd"))
	if r.Status != 409 || r.errCode(t) != "already_requested" {
		t.Fatalf("duplicate for Noah = %d %s", r.Status, r.Body)
	}
	existing = obj(t, r.JSON(t), "error", "request")
	if existing["user"] != nil || existing["note"] != "" || existing["state"] != "pending" {
		t.Errorf("Noah is shown %v", existing)
	}
	// HD of the same title is a different ask.
	if r := noah.do("POST", "/api/v1/requests", movieAsk(1, "hd")); r.Status != 201 {
		t.Errorf("HD while a 4K ask waits: %d %s", r.Status, r.Body)
	}
}

// ---- reading ---------------------------------------------------------------

func TestMembersOnlySeeTheirOwnRequestsOverHTTP(t *testing.T) {
	a := newReqApp(t)
	owner := a.owner()
	admin := a.admin(owner, "Alice")
	mia := a.member(owner, "Mia")
	noah := a.member(owner, "Noah")
	miaID, noahID := userID(t, mia), userID(t, noah)

	miaReq := a.mustAsk(mia, movieAsk(1, "uhd"))
	noahReq := a.mustAsk(noah, movieAsk(2, "hd"))
	ownerReq := a.mustAsk(owner, movieAsk(3, "hd"))

	list := func(c *client, query string) (ids []int64, total float64) {
		t.Helper()
		r := c.do("GET", "/api/v1/requests"+query, nil)
		if r.Status != 200 {
			t.Fatalf("list %s = %d %s", query, r.Status, r.Body)
		}
		body := r.JSON(t)
		for _, it := range body["items"].([]any) {
			ids = append(ids, int64(it.(map[string]any)["id"].(float64)))
		}
		return ids, body["total"].(float64)
	}
	if ids, total := list(mia, ""); len(ids) != 1 || ids[0] != miaReq || total != 1 {
		t.Errorf("Mia's list = %v (%v)", ids, total)
	}
	if ids, _ := list(mia, "?mine=0"); len(ids) != 1 || ids[0] != miaReq {
		t.Errorf("Mia asking for everyone's = %v", ids)
	}
	if ids, _ := list(noah, "?state=pending"); len(ids) != 0 {
		t.Errorf("Noah's pending list = %v", ids)
	}
	if r := mia.do("GET", reqPath(noahReq), nil); r.Status != 404 || r.errCode(t) != "not_found" {
		t.Errorf("Mia reading Noah's request: %d %s", r.Status, r.Body)
	}
	if r := mia.do("GET", reqPath(miaReq), nil); r.Status != 200 || requestBody(t, r)["id"] != float64(miaReq) {
		t.Errorf("Mia reading her own: %d %s", r.Status, r.Body)
	}
	for name, c := range map[string]*client{"owner": owner, "admin": admin} {
		ids, total := list(c, "")
		if len(ids) != 3 || total != 3 {
			t.Errorf("%s's list = %v (%v)", name, ids, total)
		}
		for _, id := range []int64{miaReq, noahReq, ownerReq} {
			if r := c.do("GET", reqPath(id), nil); r.Status != 200 {
				t.Errorf("%s reading %d: %d", name, id, r.Status)
			}
		}
	}
	if ids, _ := list(owner, "?mine=1"); len(ids) != 1 || ids[0] != ownerReq {
		t.Errorf("the owner's own = %v", ids)
	}
	if ids, _ := list(owner, "?state=pending"); len(ids) != 1 || ids[0] != miaReq {
		t.Errorf("pending = %v", ids)
	}
	if ids, _ := list(owner, "?limit=2&offset=1"); len(ids) != 2 || ids[0] != noahReq {
		t.Errorf("page = %v", ids)
	}
	// Each item names the person and the title.
	items := owner.do("GET", "/api/v1/requests?state=pending", nil).JSON(t)["items"].([]any)
	it := items[0].(map[string]any)
	if obj(t, it, "user")["id"] != float64(miaID) || obj(t, it, "user")["name"] != "Mia" || obj(t, it, "title")["name"] != "Movie 1" {
		t.Errorf("item = %v", it)
	}
	_ = noahID
	if r := owner.do("GET", "/api/v1/requests?limit=0", nil); r.Status != 400 {
		t.Errorf("limit=0: %d", r.Status)
	}
	if r := owner.do("GET", "/api/v1/requests?mine=maybe", nil); r.Status != 400 {
		t.Errorf("mine=maybe: %d", r.Status)
	}
	if r := owner.do("GET", "/api/v1/requests/abc", nil); r.Status != 404 {
		t.Errorf("bad id: %d", r.Status)
	}
}

// ---- API key scopes --------------------------------------------------------

func TestAPIKeyScopesOnRequests(t *testing.T) {
	a := newReqApp(t)
	owner := a.owner()
	admin := a.admin(owner, "Alice")
	mia := a.member(owner, "Mia")

	// ask versus see.
	seeKey := a.key(mia, "see")
	if r := seeKey.send("GET", "/api/v1/requests", nil); r.Status != 200 {
		t.Errorf("see key listing: %d %s", r.Status, r.Body)
	}
	if r := seeKey.send("POST", "/api/v1/requests", movieAsk(1, "hd")); r.Status != 403 || r.errCode(t) != "insufficient_scope" {
		t.Errorf("see key asking: %d %s", r.Status, r.Body)
	}
	askKey := a.key(mia, "ask")
	r := askKey.send("POST", "/api/v1/requests", movieAsk(1, "hd")) // no CSRF header needed with a key
	if r.Status != 201 {
		t.Fatalf("ask key asking: %d %s", r.Status, r.Body)
	}
	id := requestID64(t, r)
	if r := askKey.send("GET", "/api/v1/requests", nil); r.Status != 403 || r.errCode(t) != "insufficient_scope" {
		t.Errorf("ask key listing: %d %s", r.Status, r.Body)
	}
	if r := askKey.send("DELETE", reqPath(id), nil); r.Status != 409 { // allowed to try; it is approved
		t.Errorf("ask key cancelling an approved ask: %d %s", r.Status, r.Body)
	}
	if r := seeKey.send("DELETE", reqPath(id), nil); r.Status != 403 {
		t.Errorf("see key cancelling: %d", r.Status)
	}
	// A member key cannot be given the approve tick at all.
	if r := mia.do("POST", "/api/v1/apikeys", map[string]any{"name": "k", "scopes": []string{"approve"}}); r.Status != 400 {
		t.Errorf("member approve key: %d", r.Status)
	}

	// approve versus ask on an admin and the owner.
	noah := a.member(owner, "Noah")
	waiting := a.mustAsk(mia, movieAsk(2, "uhd"))
	other := a.mustAsk(mia, movieAsk(3, "uhd"))
	adminAsk := a.key(admin, "see", "ask")
	if r := adminAsk.send("POST", reqPath(waiting)+"/approve", nil); r.Status != 403 || r.errCode(t) != "insufficient_scope" {
		t.Errorf("admin key without approve: %d %s", r.Status, r.Body)
	}
	adminApprove := a.key(admin, "see", "approve")
	if r := adminApprove.send("POST", "/api/v1/requests", movieAsk(9, "hd")); r.Status != 403 || r.errCode(t) != "insufficient_scope" {
		t.Errorf("approve key asking: %d %s", r.Status, r.Body)
	}
	if r := adminApprove.send("POST", reqPath(waiting)+"/approve", nil); r.Status != 200 {
		t.Errorf("admin key with approve: %d %s", r.Status, r.Body)
	}
	if r := adminApprove.send("POST", reqPath(other)+"/decline", map[string]any{"reason": "later"}); r.Status != 200 {
		t.Errorf("decline with the approve key: %d %s", r.Status, r.Body)
	}
	ownerKey := a.key(owner, "see", "ask")
	third := a.mustAsk(noah, movieAsk(4, "uhd"))
	if r := ownerKey.send("POST", reqPath(third)+"/decline", nil); r.Status != 403 || r.errCode(t) != "insufficient_scope" {
		t.Errorf("owner key without approve declining: %d %s", r.Status, r.Body)
	}
	if r := ownerKey.send("POST", "/api/v1/requests", movieAsk(5, "hd")); r.Status != 201 {
		t.Errorf("owner key asking: %d %s", r.Status, r.Body)
	}

	// people: admin tick to read, settings tick to change.
	if r := a.key(owner, "see").send("GET", "/api/v1/people", nil); r.Status != 403 || r.errCode(t) != "insufficient_scope" {
		t.Errorf("see key reading people: %d %s", r.Status, r.Body)
	}
	if r := a.key(owner, "admin").send("GET", "/api/v1/people", nil); r.Status != 200 {
		t.Errorf("admin key reading people: %d %s", r.Status, r.Body)
	}
	limitsPath := "/api/v1/people/" + strconv.FormatInt(userID(t, mia), 10) + "/limits"
	if r := a.key(owner, "admin").send("PUT", limitsPath, map[string]any{"hdPerWeek": 1}); r.Status != 403 || r.errCode(t) != "insufficient_scope" {
		t.Errorf("admin key changing limits: %d %s", r.Status, r.Body)
	}
	if r := a.key(owner, "settings").send("PUT", limitsPath, map[string]any{"hdPerWeek": 1}); r.Status != 200 {
		t.Errorf("settings key changing limits: %d %s", r.Status, r.Body)
	}
	// Revoking is not needed: a key never outlives what its person may do.
	if r := a.key(admin, "admin").send("GET", "/api/v1/people", nil); r.Status != 200 {
		t.Errorf("admin's admin key reading people: %d", r.Status)
	}
	if r := a.key(admin, "admin").send("PUT", limitsPath, map[string]any{"hdPerWeek": 1}); r.Status != 403 {
		t.Errorf("an admin changing limits: %d", r.Status)
	}
}

// ---- people and limits -----------------------------------------------------

func TestPeopleAndLimitsOverHTTP(t *testing.T) {
	a := newReqApp(t)
	owner := a.owner()
	admin := a.admin(owner, "Alice")
	mia := a.member(owner, "Mia")
	miaID := userID(t, mia)
	limits := "/api/v1/people/" + strconv.FormatInt(miaID, 10) + "/limits"
	a.mustAsk(mia, movieAsk(1, "hd"))

	// Who may read.
	if r := mia.do("GET", "/api/v1/people", nil); r.Status != 403 || r.errCode(t) != "forbidden" {
		t.Errorf("a member reading people: %d %s", r.Status, r.Body)
	}
	r := admin.do("GET", "/api/v1/people", nil)
	if r.Status != 200 {
		t.Fatalf("admin reading people: %d %s", r.Status, r.Body)
	}
	body := r.JSON(t)
	defaults := obj(t, body, "defaults")
	if defaults["hdPerWeek"] != float64(10) || defaults["uhdPerWeek"] != float64(2) ||
		defaults["autoApproveHD"] != true || defaults["autoApproveUHD"] != false {
		t.Errorf("defaults = %v", defaults)
	}
	var miaRow map[string]any
	for _, p := range body["people"].([]any) {
		if row := p.(map[string]any); row["id"] == float64(miaID) {
			miaRow = row
		}
	}
	if miaRow == nil || miaRow["name"] != "Mia" || miaRow["role"] != "member" || miaRow["disabled"] != false {
		t.Fatalf("Mia's row = %v", miaRow)
	}
	eff := obj(t, miaRow, "limits", "effective")
	if eff["hdPerWeek"] != float64(10) || eff["uhdPerWeek"] != float64(2) || eff["autoApproveHD"] != true || eff["autoApproveUHD"] != false {
		t.Errorf("effective = %v", eff)
	}
	over := obj(t, miaRow, "limits", "overrides")
	for _, k := range []string{"hdPerWeek", "uhdPerWeek", "autoApproveHD", "autoApproveUHD"} {
		if v, ok := over[k]; !ok || v != nil {
			t.Errorf("override %s = %v (present %v), want null", k, v, ok)
		}
	}
	if used := obj(t, miaRow, "usage", "hd"); used["used"] != float64(1) || used["limit"] != float64(10) {
		t.Errorf("usage.hd = %v", used)
	}
	if aa := obj(t, miaRow, "autoApprove"); aa["hd"] != true || aa["uhd"] != false {
		t.Errorf("autoApprove = %v", aa)
	}

	// Who may change.
	for name, c := range map[string]*client{"admin": admin, "member": mia} {
		if r := c.do("PUT", limits, map[string]any{"hdPerWeek": 3}); r.Status != 403 || r.errCode(t) != "forbidden" {
			t.Errorf("%s changing limits: %d %s", name, r.Status, r.Body)
		}
	}
	// Set, leave alone, clear.
	r = owner.do("PUT", limits, map[string]any{"hdPerWeek": 3, "autoApproveHD": false, "uhdPerWeek": 0})
	if r.Status != 200 {
		t.Fatalf("PUT limits = %d %s", r.Status, r.Body)
	}
	person := obj(t, r.JSON(t), "person")
	over = obj(t, person, "limits", "overrides")
	if over["hdPerWeek"] != float64(3) || over["uhdPerWeek"] != float64(0) || over["autoApproveHD"] != false || over["autoApproveUHD"] != nil {
		t.Errorf("overrides = %v", over)
	}
	if eff := obj(t, person, "limits", "effective"); eff["hdPerWeek"] != float64(3) || eff["autoApproveHD"] != false {
		t.Errorf("effective = %v", eff)
	}
	r = owner.do("PUT", limits, map[string]any{"autoApproveUHD": true})
	over = obj(t, r.JSON(t), "person", "limits", "overrides")
	if over["hdPerWeek"] != float64(3) || over["autoApproveUHD"] != true {
		t.Errorf("a field left out changed: %v", over)
	}
	r = owner.send("PUT", limits, nil, "X-Marquee", "1", "Content-Type", "application/json") // no body at all
	if r.Status != 400 {
		t.Errorf("PUT with no body: %d %s", r.Status, r.Body)
	}
	raw := func(body string) reply {
		req, _ := http.NewRequest("PUT", a.srv.URL+limits, bytes.NewReader([]byte(body)))
		req.Header.Set("X-Marquee", "1")
		req.Header.Set("Content-Type", "application/json")
		resp, err := owner.hc.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		defer resp.Body.Close()
		b, _ := io.ReadAll(resp.Body)
		return reply{Status: resp.StatusCode, Header: resp.Header, Body: b}
	}
	r = raw(`{"hdPerWeek":null,"autoApproveHD":null}`)
	over = obj(t, r.JSON(t), "person", "limits", "overrides")
	if r.Status != 200 || over["hdPerWeek"] != nil || over["autoApproveHD"] != nil || over["uhdPerWeek"] != float64(0) || over["autoApproveUHD"] != true {
		t.Errorf("a null clears: %d %v", r.Status, over)
	}
	if eff := obj(t, r.JSON(t), "person", "limits", "effective"); eff["hdPerWeek"] != float64(10) || eff["autoApproveHD"] != true {
		t.Errorf("effective after the clear = %v", eff)
	}
	// An empty object changes nothing.
	if r = raw(`{}`); r.Status != 200 {
		t.Errorf("empty patch: %d %s", r.Status, r.Body)
	}

	// Mistakes.
	for name, c := range map[string]struct {
		path, body string
		status     int
		code       string
	}{
		"unknown field":   {limits, `{"weeklyLimit":3}`, 400, "invalid_request"},
		"text for number": {limits, `{"hdPerWeek":"lots"}`, 400, "invalid_request"},
		"fraction":        {limits, `{"hdPerWeek":2.5}`, 400, "invalid_request"},
		"text for flag":   {limits, `{"autoApproveHD":"yes"}`, 400, "invalid_request"},
		"negative":        {limits, `{"hdPerWeek":-1}`, 400, "invalid_limit"},
		"huge":            {limits, `{"uhdPerWeek":100000}`, 400, "invalid_limit"},
		"not an object":   {limits, `[1]`, 400, "invalid_json"},
		"no such person":  {"/api/v1/people/9999/limits", `{"hdPerWeek":1}`, 404, "not_found"},
		"bad person id":   {"/api/v1/people/abc/limits", `{"hdPerWeek":1}`, 404, "not_found"},
	} {
		req, _ := http.NewRequest("PUT", a.srv.URL+c.path, bytes.NewReader([]byte(c.body)))
		req.Header.Set("X-Marquee", "1")
		resp, err := owner.hc.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		b, _ := io.ReadAll(resp.Body)
		resp.Body.Close()
		rr := reply{Status: resp.StatusCode, Body: b}
		if rr.Status != c.status || rr.errCode(t) != c.code {
			t.Errorf("%s: %d %s, want %d %s", name, rr.Status, rr.Body, c.status, c.code)
		}
	}

	// The limits are enforced: no 4K asks at all for Mia now (limit 0).
	if r := mia.do("POST", "/api/v1/requests", movieAsk(2, "uhd")); r.Status != 429 || r.errCode(t) != "limit_reached" {
		t.Errorf("a limit of 0: %d %s", r.Status, r.Body)
	} else if e := obj(t, r.JSON(t), "error"); e["limit"] != float64(0) || e["resetsAt"] != nil {
		t.Errorf("error = %v", e)
	}
}

// ---- no folder, events, wiring ---------------------------------------------

func TestOwnerAskWithNoLibraryFolderIsAConflict(t *testing.T) {
	a := newReqApp(t)
	owner := a.owner()
	// Take the show folder out of the library.
	folders := owner.do("GET", "/api/v1/library/folders", nil).JSON(t)["folders"].([]any)
	for _, f := range folders {
		fm := f.(map[string]any)
		if fm["kind"] == "show" {
			if d := owner.do("DELETE", "/api/v1/library/folders/"+strconv.Itoa(int(fm["id"].(float64))), nil); d.Status != 204 {
				t.Fatalf("delete folder = %d", d.Status)
			}
		}
	}
	r := owner.do("POST", "/api/v1/requests", map[string]any{"kind": "show", "provider": "tvmaze", "providerId": 3, "quality": "hd"})
	if r.Status != 409 || r.errCode(t) != "no_library_folder" {
		t.Errorf("owner ask with no show folder: %d %s", r.Status, r.Body)
	}
	mia := a.member(owner, "Mia")
	r = mia.do("POST", "/api/v1/requests", map[string]any{"kind": "show", "provider": "tvmaze", "providerId": 4, "quality": "hd"})
	if r.Status != 201 || r.JSON(t)["autoApproved"] != false {
		t.Fatalf("a member's ask waits: %d %s", r.Status, r.Body)
	}
	if ap := owner.do("POST", reqPath(requestID64(t, r))+"/approve", nil); ap.Status != 409 || ap.errCode(t) != "no_library_folder" {
		t.Errorf("approving with no folder: %d %s", ap.Status, ap.Body)
	}
}

func TestRequestEventsReachTheStream(t *testing.T) {
	a := newReqApp(t)
	owner := a.owner()
	mia := a.member(owner, "Mia")
	sub, unsub := a.hub.Subscribe("requests")
	defer unsub()

	next := func(want string) map[string]any {
		t.Helper()
		select {
		case m := <-sub.C():
			var got map[string]any
			if err := json.Unmarshal(m.Data, &got); err != nil {
				t.Fatalf("event %s: %v", m.Data, err)
			}
			if got["event"] != want {
				t.Fatalf("event = %v, want %s", got, want)
			}
			if len(got) != 3 {
				t.Errorf("event %s carries more than event, id and state", m.Data)
			}
			return got
		case <-time.After(2 * time.Second):
			t.Fatalf("no %q event", want)
			return nil
		}
	}
	id := a.mustAsk(mia, movieAsk(1, "uhd"))
	if got := next("created"); got["id"] != float64(id) || got["state"] != "pending" {
		t.Errorf("created = %v", got)
	}
	if r := owner.do("POST", reqPath(id)+"/approve", nil); r.Status != 200 {
		t.Fatal(r.Status, string(r.Body))
	}
	if got := next("approved"); got["id"] != float64(id) || got["state"] != "approved" {
		t.Errorf("approved = %v", got)
	}
	id2 := a.mustAsk(mia, movieAsk(2, "uhd"))
	next("created")
	owner.do("POST", reqPath(id2)+"/decline", map[string]any{"reason": "no"})
	next("declined")
	id3 := a.mustAsk(mia, movieAsk(3, "uhd"))
	next("created")
	mia.do("DELETE", reqPath(id3), nil)
	next("cancelled")
	a.mustAsk(owner, movieAsk(4, "hd")) // approved by the rules: only "approved" is sent
	next("approved")

	// And it is on the wire for a signed-in browser.
	req, _ := http.NewRequest("GET", a.srv.URL+"/api/v1/events?topic=requests", nil)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	resp, err := mia.hc.Do(req.WithContext(ctx))
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	deadline := time.Now().Add(2 * time.Second)
	for a.hub.Len() < 2 && time.Now().Before(deadline) {
		time.Sleep(5 * time.Millisecond)
	}
	a.mustAsk(owner, movieAsk(5, "hd"))
	buf := make([]byte, 0, 512)
	tmp := make([]byte, 256)
	for !bytes.Contains(buf, []byte(`"event":"approved"`)) {
		n, err := resp.Body.Read(tmp)
		buf = append(buf, tmp[:n]...)
		if err != nil {
			t.Fatalf("stream ended: %v; got %q", err, buf)
		}
	}
	if !bytes.Contains(buf, []byte("event: requests")) {
		t.Errorf("stream = %q", buf)
	}
}

func TestRequestRoutesAreAbsentWithoutTheService(t *testing.T) {
	a := newApp(t, nil) // no requests service
	owner := a.owner()
	for _, c := range []struct{ method, path string }{
		{"GET", "/api/v1/requests"}, {"POST", "/api/v1/requests"}, {"GET", "/api/v1/requests/summary"},
		{"GET", "/api/v1/requests/1"}, {"GET", "/api/v1/people"},
	} {
		if r := owner.do(c.method, c.path, nil); r.Status != 404 || r.errCode(t) != "not_found" {
			t.Errorf("%s %s = %d %s", c.method, c.path, r.Status, r.Body)
		}
	}
}
