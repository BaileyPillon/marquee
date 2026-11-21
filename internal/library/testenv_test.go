package library

import (
	"context"
	"database/sql"
	"errors"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/BaileyPillon/marquee/internal/core/db"
	"github.com/BaileyPillon/marquee/internal/core/events"
)

// fixedTime is the modified time every test file gets, so a file that goes
// away and comes back looks exactly the same.
var fixedTime = time.Date(2020, 1, 2, 3, 4, 5, 0, time.UTC)

func quietLog() *slog.Logger { return slog.New(slog.NewTextHandler(io.Discard, nil)) }

// testEnv is a library service on a migrated temp database.
type testEnv struct {
	t   *testing.T
	svc *Service
	db  *sql.DB
	hub *events.Hub
}

func newEnv(t *testing.T) *testEnv {
	t.Helper()
	d, err := db.Open(filepath.Join(t.TempDir(), db.MainFile))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { d.Close() })
	if _, err := db.Migrate(context.Background(), d, db.SetMain); err != nil {
		t.Fatal(err)
	}
	hub := events.NewHub(quietLog())
	t.Cleanup(hub.Close)
	svc := NewService(d, Options{Logger: quietLog(), Hub: hub})
	t.Cleanup(svc.Close)
	return &testEnv{t: t, svc: svc, db: d, hub: hub}
}

// mkFiles creates empty files (and their folders) under root, each with the
// fixed modified time.
func mkFiles(t *testing.T, root string, rels ...string) {
	t.Helper()
	for _, rel := range rels {
		p := filepath.Join(root, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, nil, 0o644); err != nil {
			t.Fatal(err)
		}
		if err := os.Chtimes(p, fixedTime, fixedTime); err != nil {
			t.Fatal(err)
		}
	}
}

// addFolder creates a folder on disk with files in it and adds it to the library.
func (e *testEnv) addFolder(kind string, rels ...string) (Folder, string) {
	e.t.Helper()
	root := filepath.Join(e.t.TempDir(), kind)
	if err := os.MkdirAll(root, 0o755); err != nil {
		e.t.Fatal(err)
	}
	mkFiles(e.t, root, rels...)
	f, err := e.svc.AddFolder(context.Background(), kind, root, "")
	if err != nil {
		e.t.Fatal(err)
	}
	return f, root
}

func (e *testEnv) scan(f Folder) ScanSummary {
	e.t.Helper()
	sum, err := e.svc.ScanNow(context.Background(), f.ID)
	if err != nil {
		e.t.Fatalf("ScanNow: %v", err)
	}
	return sum
}

// files returns every row of a folder keyed by relative path.
func (e *testEnv) files(f Folder) map[string]File {
	e.t.Helper()
	list, _, err := e.svc.Files(context.Background(), FileFilter{FolderID: f.ID, Limit: 10000})
	if err != nil {
		e.t.Fatal(err)
	}
	out := map[string]File{}
	for _, file := range list {
		out[file.RelPath] = file
	}
	return out
}

func keys[V any](m map[string]V) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	return out
}

// fakeTitles is a TitleSource with fixed answers.
type fakeTitles struct {
	mu       sync.Mutex
	cands    map[string][]Candidate
	eps      map[epKey]int64
	candErr  error
	epErr    error
	calls    map[string]int
	epLookup int
}

type epKey struct {
	title          int64
	season, number int
}

func newFakeTitles() *fakeTitles {
	return &fakeTitles{cands: map[string][]Candidate{}, eps: map[epKey]int64{}, calls: map[string]int{}}
}

func (f *fakeTitles) Candidates(_ context.Context, kind string) ([]Candidate, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls[kind]++
	if f.candErr != nil {
		return nil, f.candErr
	}
	return f.cands[kind], nil
}

func (f *fakeTitles) EpisodeID(_ context.Context, titleID int64, season, number int) (int64, bool, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.epLookup++
	if f.epErr != nil {
		return 0, false, f.epErr
	}
	id, ok := f.eps[epKey{titleID, season, number}]
	return id, ok, nil
}

var errTest = errors.New("test failure")
