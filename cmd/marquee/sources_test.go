package main

import (
	"context"
	"database/sql"
	"errors"
	"io"
	"log/slog"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/BaileyPillon/marquee/internal/core/db"
	"github.com/BaileyPillon/marquee/internal/core/jobs"
	"github.com/BaileyPillon/marquee/internal/core/outbound"
	"github.com/BaileyPillon/marquee/internal/core/secrets"
	"github.com/BaileyPillon/marquee/internal/downloads"
	"github.com/BaileyPillon/marquee/internal/grab"
	"github.com/BaileyPillon/marquee/internal/library"
	"github.com/BaileyPillon/marquee/internal/search"
	"github.com/BaileyPillon/marquee/internal/sources"
	"github.com/BaileyPillon/marquee/internal/titles"
)

type fakeGrabber struct {
	err   error
	calls int
}

func (f *fakeGrabber) Grab(context.Context, grab.Target, grab.Release) (int64, error) {
	f.calls++
	return 7, f.err
}

func TestNoDownloadClientBecomesNoGrabber(t *testing.T) {
	inner := &fakeGrabber{err: downloads.ErrNoClient}
	g := noClientAsNoGrabber{inner}
	_, err := g.Grab(context.Background(), grab.Target{}, grab.Release{})
	if !errors.Is(err, search.ErrNoGrabber) || !errors.Is(err, downloads.ErrNoClient) {
		t.Errorf("err = %v, want both search.ErrNoGrabber and the downloads error", err)
	}

	// Any other failure passes through untouched, and a success keeps its id.
	boom := errors.New("client unreachable")
	inner.err = boom
	if _, err := g.Grab(context.Background(), grab.Target{}, grab.Release{}); !errors.Is(err, boom) || errors.Is(err, search.ErrNoGrabber) {
		t.Errorf("err = %v", err)
	}
	inner.err = nil
	if id, err := g.Grab(context.Background(), grab.Target{}, grab.Release{}); err != nil || id != 7 {
		t.Errorf("Grab = %d %v", id, err)
	}
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

// addFile puts a matched file row in the library, the way a scan would.
func addFile(t *testing.T, d *sql.DB, rel string, kind string, titleID int64, episodes string, quality string, missing bool) {
	t.Helper()
	ts := db.TS(time.Now())
	var eps any
	if episodes != "" {
		eps = episodes
	}
	var miss any
	if missing {
		miss = ts
	}
	if _, err := d.Exec(`INSERT INTO files (folder_id, rel_path, size, mtime, kind, parsed, quality, title_id, episode_ids, match_state, added_at, missing_since)
		VALUES (1, ?, 1000, 0, ?, '{}', ?, ?, ?, 'matched', ?, ?)`, rel, kind, quality, titleID, eps, ts, miss); err != nil {
		t.Fatal(err)
	}
}

func TestLibraryShelfSaysWhatACopyAlreadyHas(t *testing.T) {
	d := newTestDB(t)
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	lib := library.NewService(d, library.Options{Logger: log})
	t.Cleanup(lib.Close)
	ts := db.TS(time.Now())
	if _, err := d.Exec(`INSERT INTO library_folders (id, kind, path, label, created_at) VALUES (1, 'movie', '/media/movies', 'Movies', ?)`, ts); err != nil {
		t.Fatal(err)
	}
	addFile(t, d, "Metropolis (1927)/Metropolis.1927.1080p.BluRay.mkv", "movie", 5, "", "Bluray-1080p", false)
	addFile(t, d, "Sintel (2010)/Sintel.2010.2160p.WEB-DL.mkv", "movie", 6, "", "WEBDL-2160p", false)
	addFile(t, d, "Gone/Gone.1999.1080p.mkv", "movie", 7, "", "Bluray-1080p", true) // missing from disk
	addFile(t, d, "Pioneer One/S01E01.mkv", "movie", 20, "[201]", "WEBDL-720p", false)
	addFile(t, d, "Pioneer One/S01E02E03.mkv", "movie", 20, "[202,203]", "WEBDL-720p", false)
	addFile(t, d, "Pioneer One/S01E04.2160p.mkv", "movie", 20, "[204]", "WEBDL-2160p", false)

	shelf := &libraryShelf{lib: lib}
	have := func(titleID int64, quality string) search.Have {
		t.Helper()
		h, err := shelf.Have(context.Background(), grab.Copy{TitleID: titleID, Quality: quality})
		if err != nil {
			t.Fatal(err)
		}
		slices.Sort(h.EpisodeIDs)
		return h
	}
	if !have(5, grab.QualityHD).Movie || have(5, grab.QualityUHD).Movie {
		t.Error("a 1080p file is in the HD copy, not the 4K one")
	}
	if have(6, grab.QualityHD).Movie || !have(6, grab.QualityUHD).Movie {
		t.Error("a 2160p file is in the 4K copy, not the HD one")
	}
	if have(7, grab.QualityHD).Movie {
		t.Error("a file that vanished from disk is not on the shelf")
	}
	if have(8, grab.QualityHD).Movie {
		t.Error("an unknown title has nothing")
	}
	hd := have(20, grab.QualityHD)
	if !slices.Equal(hd.EpisodeIDs, []int64{201, 202, 203}) {
		t.Errorf("HD episodes = %v", hd.EpisodeIDs)
	}
	if uhd := have(20, grab.QualityUHD); !slices.Equal(uhd.EpisodeIDs, []int64{204}) {
		t.Errorf("4K episodes = %v", uhd.EpisodeIDs)
	}
}

func TestReleaseFindingIsWiredTogether(t *testing.T) {
	d := newTestDB(t)
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	box, err := secrets.New([]byte(strings.Repeat("k", secrets.KeySize)))
	if err != nil {
		t.Fatal(err)
	}
	hc := outbound.New("test", outbound.Options{Logger: log})
	lib := library.NewService(d, library.Options{Logger: log})
	t.Cleanup(lib.Close)
	cat := titles.NewService(d, titles.Providers{}, titles.Options{Logger: log})
	g := &fakeGrabber{}

	rf := newReleaseFinding(d, hc, box, cat, lib, nil, g, log)
	if rf.Sources == nil || rf.Decide == nil || rf.Search == nil {
		t.Fatalf("services = %+v", rf)
	}
	var _ grab.Searcher = rf.Search // what the requests module is given
	if !rf.Search.HasGrabber() {
		t.Error("the download side should be wired in")
	}
	if profiles, err := rf.Decide.Profiles(context.Background()); err != nil || len(profiles) != 5 {
		t.Errorf("profiles = %d %v", len(profiles), err)
	}

	sched := jobs.New(log)
	if err := registerReleaseJobs(sched, rf); err != nil {
		t.Fatal(err)
	}
	got := map[string]time.Duration{}
	for _, s := range sched.Status() {
		got[s.Name] = s.Interval
	}
	if got[sources.RSSJob] != 15*time.Minute || got[search.MissingJob] != 6*time.Hour || len(got) != 2 {
		t.Errorf("jobs = %v", got)
	}
	// Both jobs are harmless with nothing set up.
	if err := sched.RunNow(sources.RSSJob); err != nil {
		t.Errorf("sources.rss: %v", err)
	}
	if err := sched.RunNow(search.MissingJob); err != nil {
		t.Errorf("search.missing: %v", err)
	}
}
