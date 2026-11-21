package main

import (
	"context"
	"io"
	"log/slog"
	"net/http"
	"path/filepath"
	"regexp"
	"testing"
	"time"

	"github.com/BaileyPillon/marquee/internal/core/db"
	"github.com/BaileyPillon/marquee/internal/core/events"
	"github.com/BaileyPillon/marquee/internal/core/jobs"
	"github.com/BaileyPillon/marquee/internal/core/outbound"
	"github.com/BaileyPillon/marquee/internal/core/secrets"
	"github.com/BaileyPillon/marquee/internal/downloads"
	"github.com/BaileyPillon/marquee/internal/grab"
)

func TestDownloadsPollJobIsRegisteredAndIsAGrabber(t *testing.T) {
	dir := t.TempDir()
	d, err := db.Open(filepath.Join(dir, "marquee.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer d.Close()
	if _, err := db.Migrate(context.Background(), d, db.SetMain); err != nil {
		t.Fatal(err)
	}
	quiet := slog.New(slog.NewTextHandler(io.Discard, nil))
	box, err := secrets.LoadOrCreate(filepath.Join(dir, "marquee.key"), quiet)
	if err != nil {
		t.Fatal(err)
	}
	svc := newDownloads(d, events.NewHub(quiet), outbound.New("test", outbound.Options{Logger: quiet}), box, quiet)

	// What the search service will be handed is a grab.Grabber backed by the service.
	var g grab.Grabber = downloadGrabber(svc)
	if _, err := g.Grab(context.Background(), grab.Target{}, grab.Release{Protocol: grab.ProtocolTorrent}); err == nil {
		t.Error("grabbing with no client configured should fail")
	}

	sched := jobs.New(quiet)
	if err := registerDownloadsJobs(sched, svc); err != nil {
		t.Fatal(err)
	}
	st := sched.Status()
	if len(st) != 1 || st[0].Name != downloads.JobPoll || st[0].Interval != 5*time.Second {
		t.Fatalf("jobs = %+v, want one downloads.poll every 5s", st)
	}
	// With no clients yet the job runs clean.
	if err := sched.RunNow(downloads.JobPoll); err != nil {
		t.Errorf("downloads.poll: %v", err)
	}
}

func TestServeExposesTheDownloadsAPI(t *testing.T) {
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

	// Every route exists and wants a sign-in: 401, not the JSON 404 an unwired
	// route would give.
	for _, path := range []string{
		"/api/v1/downloads", "/api/v1/downloads?state=history", "/api/v1/downloads/summary", "/api/v1/clients",
	} {
		resp, err := http.Get("http://" + m[1] + path)
		if err != nil {
			t.Fatal(err)
		}
		resp.Body.Close()
		if resp.StatusCode != http.StatusUnauthorized {
			t.Errorf("GET %s = %d, want 401", path, resp.StatusCode)
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
