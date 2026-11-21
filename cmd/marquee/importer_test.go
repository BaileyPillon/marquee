package main

import (
	"context"
	"database/sql"
	"errors"
	"net/http"
	"path/filepath"
	"regexp"
	"sync"
	"testing"
	"time"

	"github.com/BaileyPillon/marquee/internal/core/db"
	"github.com/BaileyPillon/marquee/internal/grab"
	"github.com/BaileyPillon/marquee/internal/history"
)

type stubGrabber struct {
	id  int64
	err error
}

func (f stubGrabber) Grab(context.Context, grab.Target, grab.Release) (int64, error) {
	return f.id, f.err
}

type recordedEntries struct {
	mu      sync.Mutex
	entries []history.Entry
}

func (r *recordedEntries) Record(_ context.Context, e history.Entry) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.entries = append(r.entries, e)
	return nil
}

func TestRecordedGrabberWritesTheGrabbedEvent(t *testing.T) {
	rec := &recordedEntries{}
	g := recordedGrabber(stubGrabber{id: 77}, rec)
	id, err := g.Grab(context.Background(),
		grab.Target{CopyID: 4, TitleID: 9, EpisodeIDs: []int64{1, 2}},
		grab.Release{Title: "Film.2020.1080p.BluRay.x264-GRP", Protocol: grab.ProtocolTorrent, Size: 1234, Quality: "Bluray-1080p",
			IndexerID: 3, Reasons: []string{"Best match: 1080p BluRay encode"}})
	if err != nil || id != 77 {
		t.Fatalf("Grab = %d, %v", id, err)
	}
	if len(rec.entries) != 1 {
		t.Fatalf("entries = %+v", rec.entries)
	}
	e := rec.entries[0]
	d, _ := e.Detail.(map[string]any)
	if e.Event != history.Grabbed || e.TitleID != 9 || e.CopyID != 4 || e.DownloadID != 77 ||
		d["release"] != "Film.2020.1080p.BluRay.x264-GRP" || d["quality"] != "Bluray-1080p" || d["why"] != "Best match: 1080p BluRay encode" || d["size"] != int64(1234) {
		t.Errorf("entry = %+v", e)
	}

	// A grab that fails writes nothing, and the error still reaches the caller.
	rec.entries = nil
	boom := errors.New("no client")
	if _, err := recordedGrabber(stubGrabber{err: boom}, rec).Grab(context.Background(), grab.Target{TitleID: 9}, grab.Release{}); !errors.Is(err, boom) {
		t.Errorf("err = %v", err)
	}
	if len(rec.entries) != 0 {
		t.Errorf("a failed grab is not history: %+v", rec.entries)
	}
	// And no recorder is fine.
	if _, err := recordedGrabber(stubGrabber{id: 1}, nil).Grab(context.Background(), grab.Target{TitleID: 9}, grab.Release{}); err != nil {
		t.Errorf("no recorder: %v", err)
	}
}

func TestGrabbedHistoryReachesTheDatabase(t *testing.T) {
	d, err := db.Open(filepath.Join(t.TempDir(), "marquee.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer d.Close()
	if _, err := db.Migrate(context.Background(), d, db.SetMain); err != nil {
		t.Fatal(err)
	}
	h := history.New(d, history.Options{})
	if _, err := recordedGrabber(stubGrabber{id: 5}, h).Grab(context.Background(), grab.Target{CopyID: 2, TitleID: 8}, grab.Release{Title: "X"}); err != nil {
		t.Fatal(err)
	}
	items, err := h.ForTitle(context.Background(), 8, 10)
	if err != nil || len(items) != 1 || items[0].Event != "grabbed" || items[0].DownloadID == nil || *items[0].DownloadID != 5 {
		t.Errorf("history = %+v, %v", items, err)
	}
	var _ *sql.DB = d
}

func TestServeWiresUpTheImportSide(t *testing.T) {
	t.Setenv("MARQUEE_MASTER_KEY_FILE", "")
	cfg := filepath.Join(t.TempDir(), "config")
	logs := &lockedBuffer{}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- serve(ctx, newLogger(logs, "info"), "127.0.0.1:0", cfg) }()

	started := regexp.MustCompile(`msg="marquee started" version=\S+ addr=(127\.0\.0\.1:\d+)`)
	var m []string
	for deadline := time.Now().Add(10 * time.Second); m == nil && time.Now().Before(deadline); {
		m = started.FindStringSubmatch(logs.String())
		select {
		case err := <-done:
			t.Fatalf("serve returned early: %v", err)
		case <-time.After(20 * time.Millisecond):
		}
	}
	if m == nil {
		t.Fatalf("no startup line:\n%s", logs.String())
	}

	// Every route exists and wants a sign-in (401), not the JSON 404 an unwired
	// route would give.
	for _, rt := range []struct{ method, path string }{
		{"GET", "/api/v1/mediaservers"}, {"GET", "/api/v1/import/status"},
		{"GET", "/api/v1/titles/1/history"}, {"GET", "/api/v1/titles/1/open"},
		{"POST", "/api/v1/mediaservers"}, {"POST", "/api/v1/mediaservers/1/test"}, {"POST", "/api/v1/downloads/1/import"},
	} {
		req, _ := http.NewRequest(rt.method, "http://"+m[1]+rt.path, nil)
		req.Header.Set("X-Marquee", "1")
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		resp.Body.Close()
		if resp.StatusCode != http.StatusUnauthorized {
			t.Errorf("%s %s = %d, want 401", rt.method, rt.path, resp.StatusCode)
		}
	}

	cancel()
	select {
	case err := <-done:
		if err != nil {
			t.Errorf("serve returned %v", err)
		}
	case <-time.After(15 * time.Second):
		t.Fatal("serve did not return after cancel")
	}
}
