package metadata

import (
	"bytes"
	"context"
	"database/sql"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/BaileyPillon/marquee/internal/core/db"
	"github.com/BaileyPillon/marquee/internal/core/outbound"
)

// fakeClock drives the cache TTLs and the outbound rate limiter. Sleeping on it
// moves time forward instead of waiting, so rate-limit tests run instantly.
type fakeClock struct {
	mu     sync.Mutex
	t      time.Time
	sleeps []time.Duration
}

func newFakeClock() *fakeClock {
	return &fakeClock{t: time.Date(2026, 11, 20, 12, 0, 0, 0, time.UTC)}
}

func (c *fakeClock) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.t
}

func (c *fakeClock) Advance(d time.Duration) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.t = c.t.Add(d)
}

func (c *fakeClock) Sleep(_ context.Context, d time.Duration) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.sleeps = append(c.sleeps, d)
	c.t = c.t.Add(d)
	return nil
}

func (c *fakeClock) Sleeps() []time.Duration {
	c.mu.Lock()
	defer c.mu.Unlock()
	return append([]time.Duration(nil), c.sleeps...)
}

// env is a metadata Service on a temp database with a debug log you can read.
type env struct {
	t     *testing.T
	db    *sql.DB
	clock *fakeClock
	svc   *Service
	logs  *syncBuffer
	dir   string // artwork cache dir
}

type syncBuffer struct {
	mu sync.Mutex
	b  bytes.Buffer
}

func (s *syncBuffer) Write(p []byte) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.b.Write(p)
}

func (s *syncBuffer) String() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.b.String()
}

func newTestDB(t *testing.T) *sql.DB {
	t.Helper()
	d, err := db.Open(filepath.Join(t.TempDir(), "marquee.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { d.Close() })
	if _, err := db.Migrate(context.Background(), d, db.SetMain); err != nil {
		t.Fatal(err)
	}
	return d
}

// newEnv builds a Service. Callers fill in the base URLs they need; the
// artwork directory, clock and logger are set here.
func newEnv(t *testing.T, o Options) *env {
	t.Helper()
	e := &env{t: t, db: newTestDB(t), clock: newFakeClock(), logs: &syncBuffer{}, dir: filepath.Join(t.TempDir(), "artwork")}
	log := slog.New(slog.NewTextHandler(e.logs, &slog.HandlerOptions{Level: slog.LevelDebug}))
	hc := outbound.New("1.2.3", outbound.Options{
		Logger:     log,
		MaxRetries: -1, // a failing fake should fail at once
		Now:        e.clock.Now,
		Sleep:      e.clock.Sleep,
	})
	o.ArtworkDir = e.dir
	o.Now = e.clock.Now
	o.Logger = log
	if o.TMDBURL == "" {
		o.TMDBURL = "http://tmdb.invalid"
	}
	if o.TVmazeURL == "" {
		o.TVmazeURL = "http://tvmaze.invalid"
	}
	if o.MusicBrainzURL == "" {
		o.MusicBrainzURL = "http://musicbrainz.invalid"
	}
	svc, err := New(e.db, hc, o)
	if err != nil {
		t.Fatal(err)
	}
	e.svc = svc
	return e
}

func (e *env) cacheRows() int {
	e.t.Helper()
	n, err := e.svc.cache.store.count(context.Background())
	if err != nil {
		e.t.Fatal(err)
	}
	return n
}

// fakeAPI is an httptest server with per-path handlers and a call log.
type fakeAPI struct {
	t   *testing.T
	srv *httptest.Server

	mu       sync.Mutex
	handlers map[string]http.HandlerFunc
	calls    []apiCall
}

type apiCall struct {
	Path, RawQuery string
	Header         http.Header
}

func newFakeAPI(t *testing.T, routes map[string]http.HandlerFunc) *fakeAPI {
	t.Helper()
	f := &fakeAPI{t: t, handlers: routes}
	if f.handlers == nil {
		f.handlers = map[string]http.HandlerFunc{}
	}
	f.srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		f.mu.Lock()
		f.calls = append(f.calls, apiCall{Path: r.URL.Path, RawQuery: r.URL.RawQuery, Header: r.Header.Clone()})
		h := f.handlers[r.URL.Path]
		f.mu.Unlock()
		if h == nil {
			http.NotFound(w, r)
			return
		}
		h(w, r)
	}))
	t.Cleanup(f.srv.Close)
	return f
}

func (f *fakeAPI) URL() string { return f.srv.URL }

func (f *fakeAPI) Set(path string, h http.HandlerFunc) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.handlers[path] = h
}

// Count is how many requests reached path.
func (f *fakeAPI) Count(path string) int {
	f.mu.Lock()
	defer f.mu.Unlock()
	n := 0
	for _, c := range f.calls {
		if c.Path == path {
			n++
		}
	}
	return n
}

func (f *fakeAPI) Total() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.calls)
}

func (f *fakeAPI) Calls() []apiCall {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]apiCall(nil), f.calls...)
}

// Last returns the most recent call to path.
func (f *fakeAPI) Last(path string) apiCall {
	f.t.Helper()
	f.mu.Lock()
	defer f.mu.Unlock()
	for i := len(f.calls) - 1; i >= 0; i-- {
		if f.calls[i].Path == path {
			return f.calls[i]
		}
	}
	f.t.Fatalf("no request reached %s", path)
	return apiCall{}
}

func readFixture(t *testing.T, name string) []byte {
	t.Helper()
	b, err := os.ReadFile(filepath.Join("testdata", name))
	if err != nil {
		t.Fatal(err)
	}
	return b
}

// fixture serves a testdata file as JSON.
func fixture(t *testing.T, name string) http.HandlerFunc {
	body := readFixture(t, name)
	return func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write(body)
	}
}

func httpStatus(code int) http.HandlerFunc {
	return func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(code) }
}

func bg() context.Context { return context.Background() }
