package search

import (
	"errors"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/BaileyPillon/marquee/internal/grab"
	"github.com/BaileyPillon/marquee/internal/sources"
)

// ---- the feed matcher ----------------------------------------------------------------

func TestFeedItemsThatMatchAWantedCopyAreGrabbed(t *testing.T) {
	r := newRig(t)
	id := r.addMovie("Metropolis", 1927, "tt0017136", 19, 153)
	cp := r.addCopy(1, id, "movie", grab.QualityHD)
	other := r.addMovie("Sintel", 2010, "tt1727587", 45745, 15)
	_ = other // in the catalog, but no copy is wanted

	// The latest feed: the film we want, one we do not, a bad copy of ours, and a TV episode.
	r.always(
		tor("Sintel.2010.1080p.BluRay.x264-GRP", 3*gbSize),
		tor("Metropolis.1927.CAM.x264-GRP", 1*gbSize),
		tor("Metropolis.1927.1080p.BluRay.x264-GRP", 8*gbSize),
		tor("Pioneer.One.S01E04.720p.WEB-DL.x264-GRP", 1*gbSize),
	)
	// The matcher is wired in by newRig: a sync is all it takes.
	if err := r.src.SyncRSS(bg()); err != nil {
		t.Fatal(err)
	}
	calls := r.grabber.all()
	if len(calls) != 1 || calls[0].Release.Title != "Metropolis.1927.1080p.BluRay.x264-GRP" {
		t.Fatalf("grabs = %v", r.grabber.titlesGrabbed())
	}
	if calls[0].Target.CopyID != cp.ID || calls[0].Target.TitleID != id || calls[0].Release.Quality != "Bluray-1080p" {
		t.Errorf("grab = %+v", calls[0])
	}
	if !strings.Contains(calls[0].Release.DownloadURL, "/download/") {
		t.Errorf("download link = %q", calls[0].Release.DownloadURL)
	}
	if len(calls[0].Release.Reasons) == 0 || !strings.HasPrefix(calls[0].Release.Reasons[0], "Best match: 1080p BluRay encode") {
		t.Errorf("reasons = %q", calls[0].Release.Reasons)
	}
	h := r.history(cp.ID)
	if len(h) != 1 || h[0].Grabbed != 1 || !strings.HasPrefix(h[0].Note, "Sent 1 release for download") || !strings.Contains(h[0].Note, "Found in the latest feeds") {
		t.Errorf("history = %+v", h)
	}

	// The same items are not new on the next sync, and a better release of a film that is already
	// on its way is not grabbed either.
	r.always(
		tor("Metropolis.1927.1080p.BluRay.x264-GRP", 8*gbSize),
		tor("Metropolis.1927.1080p.BluRay.PROPER.x264-GRP", 8*gbSize),
	)
	if err := r.src.SyncRSS(bg()); err != nil {
		t.Fatal(err)
	}
	if n := len(r.grabber.all()); n != 1 {
		t.Errorf("%d grabs after the second sync, want 1", n)
	}
}

func TestFeedItemsAreIgnoredWhenNothingMatches(t *testing.T) {
	r := newRig(t)
	id := r.addMovie("Metropolis", 1927, "tt0017136", 19, 153)
	cp := r.addCopy(1, id, "movie", grab.QualityHD)
	r.always(
		tor("Metropolis.2001.1080p.BluRay.x264-GRP", 8*gbSize),           // another film
		tor("Metropolis.1927.2160p.BluRay.x265-GRP", 30*gbSize),          // wrong quality for an HD copy
		torSeeded("Metropolis.1927.1080p.WEB-DL.x264-DEAD", 6*gbSize, 0), // nobody sharing it
	)
	if err := r.src.SyncRSS(bg()); err != nil {
		t.Fatal(err)
	}
	if n := len(r.grabber.all()); n != 0 {
		t.Errorf("%d grabs, want none", n)
	}
	if h := r.history(cp.ID); len(h) != 0 {
		t.Errorf("a feed that only mentions the title must not count as a search: %+v", h)
	}
}

func TestFeedMatchingNeedsAWantedCopyAndAGrabber(t *testing.T) {
	r := newRig(t)
	id := r.addMovie("Metropolis", 1927, "tt0017136", 19, 153)
	r.always(tor("Metropolis.1927.1080p.BluRay.x264-GRP", 8*gbSize))

	// No copy is wanted yet.
	if err := r.src.SyncRSS(bg()); err != nil {
		t.Fatal(err)
	}
	if len(r.grabber.all()) != 0 {
		t.Fatal("grabbed something nobody wanted")
	}
	// A copy that is already on the shelf is not wanted either: the matcher is given the
	// wanted list by the CopySource, so mimic requests by leaving it out.
	r.copies.wantedErr = errors.New("db down")
	r.addCopy(1, id, "movie", grab.QualityHD)
	r.always(tor("Metropolis.1927.1080p.WEB-DL.x264-GRP", 8*gbSize))
	if err := r.src.SyncRSS(bg()); err != nil {
		t.Fatalf("a failing CopySource must not fail the sync: %v", err)
	}
	r.copies.wantedErr = nil

	// No download client: the matcher stays out of it.
	r.svc.SetGrabber(nil)
	r.always(tor("Metropolis.1927.1080p.WEB-DL.x264-NEW", 8*gbSize))
	if err := r.src.SyncRSS(bg()); err != nil {
		t.Fatal(err)
	}
	if len(r.grabber.all()) != 0 {
		t.Error("grabbed with no grabber")
	}
}

func TestFeedMatchingShowEpisodesAndPacks(t *testing.T) {
	r := newRig(t)
	id, eps := r.addShow("Pioneer One", 2010, 99001, "tt9900001", 47, pioneerEps(4)...)
	cp := r.addCopy(1, id, "show", grab.QualityHD)
	r.shelf.have[cp.ID] = Have{EpisodeIDs: []int64{eps["1:1"], eps["1:2"], eps["1:3"]}}
	r.always(
		tor("Pioneer.One.S01E02.720p.WEB-DL.x264-GRP", 1*gbSize), // already on the shelf
		tor("Pioneer.One.S01E04.720p.WEB-DL.x264-GRP", 1*gbSize), // the one that is missing
		tor("Pioneer.One.S01.720p.WEB-DL.x264-GRP", 4*gbSize),    // a pack, when only one episode is missing
		tor("Pioneer.One.S02E01.720p.WEB-DL.x264-GRP", 1*gbSize), // a season we have no episodes of
	)
	if err := r.src.SyncRSS(bg()); err != nil {
		t.Fatal(err)
	}
	calls := r.grabber.all()
	if len(calls) != 1 || calls[0].Release.Title != "Pioneer.One.S01E04.720p.WEB-DL.x264-GRP" {
		t.Fatalf("grabs = %v", r.grabber.titlesGrabbed())
	}
	if !reflect.DeepEqual(calls[0].Target.EpisodeIDs, []int64{eps["1:4"]}) {
		t.Errorf("target = %+v", calls[0].Target)
	}
}

func TestFeedMatchingTakesAPackWhenSeveralEpisodesAreMissing(t *testing.T) {
	r := newRig(t)
	id, _ := r.addShow("Pioneer One", 2010, 99001, "tt9900001", 47, pioneerEps(3)...)
	r.addCopy(1, id, "show", grab.QualityHD)
	r.always(
		tor("Pioneer.One.S01E01.720p.WEB-DL.x264-GRP", 1*gbSize),
		tor("Pioneer.One.S01.720p.WEB-DL.x264-GRP", 3*gbSize),
	)
	if err := r.src.SyncRSS(bg()); err != nil {
		t.Fatal(err)
	}
	if got := r.grabber.titlesGrabbed(); len(got) != 1 || got[0] != "Pioneer.One.S01.720p.WEB-DL.x264-GRP" {
		t.Fatalf("grabs = %v", got)
	}
}

func TestFeedMatchingUsesTheSourcesOwnIDs(t *testing.T) {
	r := newRig(t)
	id := r.addMovie("Metropolis", 1927, "tt0017136", 19, 153)
	r.addCopy(1, id, "movie", grab.QualityHD)
	// The name is not one the catalog knows, but the source says which film it is.
	it := tor("Fritz.Lang.Silent.Epic.1080p.BluRay.x264-GRP", 8*gbSize)
	it.IMDb = "tt0017136"
	r.always(it)
	if err := r.src.SyncRSS(bg()); err != nil {
		t.Fatal(err)
	}
	if got := r.grabber.titlesGrabbed(); len(got) != 1 {
		t.Errorf("grabs = %v", got)
	}
}

func TestMatchNewIgnoresItemsOfADisabledSource(t *testing.T) {
	r := newRig(t)
	id := r.addMovie("Metropolis", 1927, "tt0017136", 19, 153)
	r.addCopy(1, id, "movie", grab.QualityHD)
	off := false
	if _, err := r.src.Update(bg(), r.ix.ID, sources.Patch{Enabled: &off}); err != nil {
		t.Fatal(err)
	}
	it := sources.Item{IndexerID: r.ix.ID, GUID: "g", Title: "Metropolis.1927.1080p.BluRay.x264-GRP", Protocol: grab.ProtocolTorrent,
		Size: 8 * gbSize, Seeders: 20, DownloadURL: "http://127.0.0.1:1/x"}
	r.svc.MatchNew(bg(), []sources.Item{it})
	if len(r.grabber.all()) != 0 {
		t.Error("an item of a disabled source was grabbed")
	}
}

// ---- the scheduled search --------------------------------------------------------------

func TestSearchMissingSkipsCopiesSearchedLately(t *testing.T) {
	r := newRig(t)
	m1 := r.addMovie("Metropolis", 1927, "tt0017136", 19, 153)
	m2 := r.addMovie("Sintel", 2010, "tt1727587", 45745, 15)
	m3 := r.addMovie("Night of the Living Dead", 1968, "tt0063350", 10331, 96)
	c1 := r.addCopy(1, m1, "movie", grab.QualityHD)
	c2 := r.addCopy(2, m2, "movie", grab.QualityHD)
	c3 := r.addCopy(3, m3, "movie", grab.QualityHD)
	r.always(
		tor("Metropolis.1927.1080p.BluRay.x264-GRP", 8*gbSize),
		tor("Sintel.2010.1080p.BluRay.x264-GRP", 3*gbSize),
		tor("Night.of.the.Living.Dead.1968.1080p.BluRay.x264-GRP", 4*gbSize),
	)
	// Copy 2 was searched an hour ago, copy 3 six hours ago.
	for _, h := range []struct {
		copyID int64
		ago    time.Duration
	}{{c2.ID, time.Hour}, {c3.ID, 6 * time.Hour}} {
		if err := r.svc.store.AddHistory(bg(), History{CopyID: h.copyID, At: r.now().Add(-h.ago), Note: "earlier"}); err != nil {
			t.Fatal(err)
		}
	}
	if err := r.svc.SearchMissing(bg()); err != nil {
		t.Fatal(err)
	}
	got := map[int64]bool{}
	for _, c := range r.grabber.all() {
		got[c.Target.CopyID] = true
	}
	if !got[c1.ID] || got[c2.ID] || !got[c3.ID] || len(got) != 2 {
		t.Errorf("searched copies = %v, want 1 and 3", got)
	}
	// The run itself is recorded, so a second run straight away does nothing.
	before := len(r.grabber.all())
	if err := r.svc.SearchMissing(bg()); err != nil {
		t.Fatal(err)
	}
	if len(r.grabber.all()) != before {
		t.Error("a second run searched the same copies again")
	}
}

func TestSearchMissingSpacesTheSearchesOut(t *testing.T) {
	r := newRig(t)
	r.svc.pace = 40 * time.Millisecond
	for i := int64(1); i <= 3; i++ {
		name := []string{"", "Metropolis", "Sintel", "Night of the Living Dead"}[i]
		id := r.addMovie(name, 1927+int(i), "", 0, 90)
		r.addCopy(i, id, "movie", grab.QualityHD)
	}
	r.always()
	start := time.Now()
	if err := r.svc.SearchMissing(bg()); err != nil {
		t.Fatal(err)
	}
	if took := time.Since(start); took < 80*time.Millisecond {
		t.Errorf("three searches took %v, want at least two pauses of 40ms", took)
	}
	for i := int64(1); i <= 3; i++ {
		if len(r.history(i)) != 1 {
			t.Errorf("copy %d was not searched", i)
		}
	}
}

func TestSearchMissingStopsWithoutADownloadClient(t *testing.T) {
	r := newRig(t)
	id := r.addMovie("Metropolis", 1927, "tt0017136", 19, 153)
	r.addCopy(1, id, "movie", grab.QualityHD)
	r.svc.SetGrabber(nil)
	if err := r.svc.SearchMissing(bg()); err != nil {
		t.Errorf("no download client is not a job failure: %v", err)
	}
	if r.srv.Searches() != 0 {
		t.Error("searched with nowhere to send the result")
	}
}

func TestSearchMissingReportsWhenEverySearchFails(t *testing.T) {
	r := newRig(t)
	id := r.addMovie("Metropolis", 1927, "tt0017136", 19, 153)
	r.addCopy(1, id, "movie", grab.QualityHD)
	r.always(metropolisFeed()...)
	r.grabber.err = errors.New("client unreachable")
	err := r.svc.SearchMissing(bg())
	if err == nil || !strings.Contains(err.Error(), "every search failed") {
		t.Errorf("err = %v", err)
	}
	r.copies.wantedErr = errors.New("requests down")
	if err := r.svc.SearchMissing(bg()); err == nil {
		t.Error("a failing CopySource should be reported")
	}
}

func TestSearchMissingWithoutRequestsWired(t *testing.T) {
	r := newRig(t)
	r.svc.SetCopySource(nil)
	if err := r.svc.SearchMissing(bg()); err != nil {
		t.Errorf("err = %v", err)
	}
	r.svc.MatchNew(bg(), []sources.Item{{Title: "Metropolis.1927.1080p.BluRay.x264-GRP"}}) // must not panic
}

// A thin guard that the job names and intervals are the ones the brief fixed.
func TestJobSettings(t *testing.T) {
	if MissingJob != "search.missing" || MissingInterval != 6*time.Hour || sources.RSSJob != "sources.rss" || sources.RSSInterval != 15*time.Minute {
		t.Errorf("jobs: %s %v %s %v", MissingJob, MissingInterval, sources.RSSJob, sources.RSSInterval)
	}
	if sources.SeenKeep != 7*24*time.Hour || sources.SearchConcurrency != 8 || sources.SearchTimeout != 30*time.Second {
		t.Errorf("limits: %v %d %v", sources.SeenKeep, sources.SearchConcurrency, sources.SearchTimeout)
	}
	if defaultPace != 2*time.Second {
		t.Errorf("pace = %v", defaultPace)
	}
}
