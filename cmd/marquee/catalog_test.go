package main

import (
	"bytes"
	"context"
	"io"
	"log/slog"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/BaileyPillon/marquee/internal/core/db"
	"github.com/BaileyPillon/marquee/internal/core/jobs"
	"github.com/BaileyPillon/marquee/internal/core/outbound"
	"github.com/BaileyPillon/marquee/internal/core/secrets"
	"github.com/BaileyPillon/marquee/internal/settings"
)

func TestTMDBKeyComesFromTheTMDBSettingsSection(t *testing.T) {
	dir := t.TempDir()
	quiet := slog.New(slog.NewTextHandler(io.Discard, nil))
	d, err := db.Open(filepath.Join(dir, "marquee.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer d.Close()
	if _, err := db.Migrate(context.Background(), d, db.SetMain); err != nil {
		t.Fatal(err)
	}
	box, err := secrets.LoadOrCreate(filepath.Join(dir, "a.key"), quiet)
	if err != nil {
		t.Fatal(err)
	}
	cfg := settings.New(d, box)
	var logs bytes.Buffer
	key := tmdbKey(cfg, slog.New(slog.NewTextHandler(&logs, nil)))
	ctx := context.Background()

	if got, err := key(ctx); got != "" || err != nil {
		t.Errorf("never set: %q %v", got, err)
	}
	if _, err := cfg.Put(ctx, "tmdb", map[string]any{"apiKey": "  0123456789abcdef0123456789abcdef \n"}); err != nil {
		t.Fatal(err)
	}
	if got, err := key(ctx); got != "0123456789abcdef0123456789abcdef" || err != nil {
		t.Errorf("after saving: %q %v", got, err)
	}
	// Another field in the section does not matter, and clearing the field clears the key.
	if _, err := cfg.Put(ctx, "tmdb", map[string]any{"apiKey": "", "note": "x"}); err != nil {
		t.Fatal(err)
	}
	if got, _ := key(ctx); got != "" {
		t.Errorf("after clearing: %q", got)
	}

	// A stored key that the current master key cannot open counts as no key,
	// with a warning, so the owner is simply asked again.
	if _, err := cfg.Put(ctx, "tmdb", map[string]any{"apiKey": "0123456789abcdef0123456789abcdef"}); err != nil {
		t.Fatal(err)
	}
	other, err := secrets.LoadOrCreate(filepath.Join(dir, "b.key"), quiet)
	if err != nil {
		t.Fatal(err)
	}
	broken := tmdbKey(settings.New(d, other), slog.New(slog.NewTextHandler(&logs, nil)))
	if got, err := broken(ctx); got != "" || err != nil {
		t.Errorf("unreadable key: %q %v", got, err)
	}
	if !strings.Contains(logs.String(), "cannot be read") || strings.Contains(logs.String(), "0123456789abcdef") {
		t.Errorf("log = %q", logs.String())
	}
}

func TestCatalogJobsAreRegistered(t *testing.T) {
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
	box, _ := secrets.LoadOrCreate(filepath.Join(dir, "a.key"), quiet)
	meta, cat, err := newCatalog(d, outbound.New("test", outbound.Options{Logger: quiet}), settings.New(d, box), dir, quiet)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(dir, "cache", "artwork")); err != nil {
		t.Errorf("artwork cache dir not created under the config dir: %v", err)
	}

	sched := jobs.New(quiet)
	if err := registerCatalogJobs(sched, meta, cat); err != nil {
		t.Fatal(err)
	}
	got := map[string]time.Duration{}
	for _, s := range sched.Status() {
		got[s.Name] = s.Interval
	}
	if got["titles.refresh"] != 12*time.Hour || got["metadata.prune"] != 24*time.Hour || len(got) != 2 {
		t.Errorf("jobs = %v", got)
	}
	// With an empty catalog both jobs run clean.
	for _, name := range []string{"titles.refresh", "metadata.prune"} {
		if err := sched.RunNow(name); err != nil {
			t.Errorf("%s: %v", name, err)
		}
	}
}

func TestServeExposesTheCatalogAPI(t *testing.T) {
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

	// Each catalog route exists and wants a sign-in: 401, not the JSON 404 an
	// unwired route would give.
	for _, path := range []string{
		"/api/v1/titles", "/api/v1/titles/1", "/api/v1/search?q=x", "/api/v1/metadata/status",
		"/api/v1/artwork/tmdb/w500/abc.jpg",
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
	if _, err := os.Stat(filepath.Join(cfg, "cache", "artwork")); err != nil {
		t.Errorf("artwork cache dir missing: %v", err)
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
