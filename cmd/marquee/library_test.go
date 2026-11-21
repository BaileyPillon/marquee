package main

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"path/filepath"
	"regexp"
	"slices"
	"testing"
	"time"

	"github.com/BaileyPillon/marquee/internal/core/db"
	"github.com/BaileyPillon/marquee/internal/core/events"
	"github.com/BaileyPillon/marquee/internal/core/jobs"
	"github.com/BaileyPillon/marquee/internal/library"
	"github.com/BaileyPillon/marquee/internal/titles"
)

type fakeCatalog struct {
	cands   []titles.Candidate
	candErr error
	gotKind string
	epID    int64
	epOK    bool
	epErr   error
	gotEp   [3]int64
}

func (f *fakeCatalog) Candidates(_ context.Context, kind string) ([]titles.Candidate, error) {
	f.gotKind = kind
	return f.cands, f.candErr
}

func (f *fakeCatalog) EpisodeID(_ context.Context, titleID int64, season, number int) (int64, bool, error) {
	f.gotEp = [3]int64{titleID, int64(season), int64(number)}
	return f.epID, f.epOK, f.epErr
}

func TestTitleSourceConvertsCatalogCandidates(t *testing.T) {
	cat := &fakeCatalog{
		cands: []titles.Candidate{
			{ID: 7, Kind: "movie", Name: "Metropolis", Year: 1927, AltNames: []string{"Metropolis (1927 film)"}},
			{ID: 8, Kind: "movie", Name: "Nosferatu", Year: 1922},
		},
		epID: 42, epOK: true,
	}
	var src library.TitleSource = titleSource{cat: cat}
	got, err := src.Candidates(context.Background(), library.CandidateMovie)
	if err != nil {
		t.Fatal(err)
	}
	want := []library.Candidate{
		{ID: 7, Kind: "movie", Name: "Metropolis", Year: 1927, AltNames: []string{"Metropolis (1927 film)"}},
		{ID: 8, Kind: "movie", Name: "Nosferatu", Year: 1922},
	}
	if len(got) != 2 || got[0].ID != want[0].ID || got[0].Name != want[0].Name || got[0].Year != want[0].Year ||
		!slices.Equal(got[0].AltNames, want[0].AltNames) || got[1].ID != 8 || got[1].Kind != "movie" {
		t.Errorf("Candidates = %+v, want %+v", got, want)
	}
	if cat.gotKind != "movie" {
		t.Errorf("kind passed on = %q", cat.gotKind)
	}
	// The library asks for artists by the same word the catalog uses.
	if library.CandidateArtist != titles.KindArtist || library.CandidateMovie != titles.KindMovie || library.CandidateShow != titles.KindShow {
		t.Errorf("library kinds %q %q %q differ from the catalog's %q %q %q",
			library.CandidateMovie, library.CandidateShow, library.CandidateArtist, titles.KindMovie, titles.KindShow, titles.KindArtist)
	}

	id, ok, err := src.EpisodeID(context.Background(), 20, 1, 5)
	if id != 42 || !ok || err != nil || cat.gotEp != [3]int64{20, 1, 5} {
		t.Errorf("EpisodeID = %d %v %v, asked %v", id, ok, err, cat.gotEp)
	}
	cat.candErr, cat.epErr = errors.New("boom"), errors.New("boom")
	if _, err := src.Candidates(context.Background(), "show"); err == nil {
		t.Error("a catalog error must come back")
	}
	if _, _, err := src.EpisodeID(context.Background(), 1, 1, 1); err == nil {
		t.Error("a catalog error must come back")
	}
}

func TestLibraryJobIsRegistered(t *testing.T) {
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
	lib := library.NewService(d, library.Options{Logger: quiet, Hub: events.NewHub(quiet)})
	defer lib.Close()

	sched := jobs.New(quiet)
	if err := registerLibraryJobs(sched, lib); err != nil {
		t.Fatal(err)
	}
	st := sched.Status()
	if len(st) != 1 || st[0].Name != "library.scan" || st[0].Interval != 6*time.Hour {
		t.Fatalf("jobs = %+v, want one library.scan every 6h", st)
	}
	// With no folders yet the job runs clean.
	if err := sched.RunNow("library.scan"); err != nil {
		t.Errorf("library.scan: %v", err)
	}
}

func TestServeExposesTheLibraryAPI(t *testing.T) {
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

	// Every library route exists and wants a sign-in: 401, not the JSON 404 an
	// unwired route would give.
	for _, path := range []string{
		"/api/v1/library/folders", "/api/v1/library/files", "/api/v1/library/files?state=unmatched", "/api/v1/library/stats",
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
