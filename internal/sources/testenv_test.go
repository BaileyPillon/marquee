package sources

import (
	"bytes"
	"context"
	"database/sql"
	"log/slog"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/BaileyPillon/marquee/internal/core/db"
	"github.com/BaileyPillon/marquee/internal/core/outbound"
	"github.com/BaileyPillon/marquee/internal/core/secrets"
)

func bg() context.Context { return context.Background() }

// testKey is a plausible Torznab key, long enough to be masked as ••••b1a0.
const testKey = "9f8e7d6c5b4a39281706f5e4d3c2b1a0"

// syncBuffer is a log sink that is safe to write from many goroutines.
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

// env is a service on a temp database, with every log line (debug included)
// captured so tests can check that no key ever reaches them.
type env struct {
	t    *testing.T
	db   *sql.DB
	box  *secrets.Box
	svc  *Service
	logs *syncBuffer
	now  time.Time
}

func newEnv(t *testing.T) *env {
	t.Helper()
	d, err := db.Open(filepath.Join(t.TempDir(), "marquee.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { d.Close() })
	if _, err := db.Migrate(bg(), d, db.SetMain); err != nil {
		t.Fatal(err)
	}
	box, err := secrets.New([]byte(strings.Repeat("k", secrets.KeySize)))
	if err != nil {
		t.Fatal(err)
	}
	e := &env{t: t, db: d, box: box, logs: &syncBuffer{}, now: time.Date(2026, 11, 20, 12, 0, 0, 0, time.UTC)}
	log := slog.New(slog.NewTextHandler(e.logs, &slog.HandlerOptions{Level: slog.LevelDebug}))
	hc := outbound.New("test", outbound.Options{
		Logger:  log,
		Backoff: func(int) time.Duration { return 0 },
		HostRPS: 1000, HostBurst: 1000,
	})
	e.svc = New(NewStore(d), box, hc, Options{Logger: log, Now: func() time.Time { return e.now }})
	return e
}

// add creates a source and fails the test if that does not work.
func (e *env) add(name, kind, url, key string, cats ...int) Indexer {
	e.t.Helper()
	ix, err := e.svc.Create(bg(), Input{Name: name, Kind: kind, URL: url, APIKey: key, Categories: cats})
	if err != nil {
		e.t.Fatalf("Create %s: %v", name, err)
	}
	return ix
}

// count runs a COUNT(*) for a test's own checks.
func (e *env) count(query string, args ...any) int {
	e.t.Helper()
	var n int
	if err := e.db.QueryRow(query, args...).Scan(&n); err != nil {
		e.t.Fatal(err)
	}
	return n
}

// noKey fails the test if text contains the key.
func noKey(t *testing.T, what, text, key string) {
	t.Helper()
	if strings.Contains(text, key) {
		t.Errorf("%s contains the API key: %.300q", what, text)
	}
}

// recordingMatcher remembers what it was told.
type recordingMatcher struct {
	mu    sync.Mutex
	calls [][]Item
}

func (m *recordingMatcher) MatchNew(_ context.Context, items []Item) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.calls = append(m.calls, items)
}

func (m *recordingMatcher) total() int {
	m.mu.Lock()
	defer m.mu.Unlock()
	n := 0
	for _, c := range m.calls {
		n += len(c)
	}
	return n
}
