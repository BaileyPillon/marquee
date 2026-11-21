package search

import (
	"errors"
	"reflect"
	"strings"
	"testing"

	"github.com/BaileyPillon/marquee/internal/decide"
	"github.com/BaileyPillon/marquee/internal/grab"
	"github.com/BaileyPillon/marquee/internal/sources"
	"github.com/BaileyPillon/marquee/internal/sources/sourcestest"
)

// metropolisFeed is a typical set of answers for Metropolis (1927).
func metropolisFeed() []sourcestest.Item {
	best := tor("Metropolis.1927.1080p.BluRay.x264-GRP", 8*gbSize)
	best.GUID, best.IMDb = "metropolis-1080-bluray", "tt0017136"
	return []sourcestest.Item{
		tor("Metropolis.1927.720p.WEB-DL.x264-GRP", 2*gbSize),
		best,
		tor("Metropolis.1927.2160p.UHD.BluRay.REMUX.HEVC-GRP", 40*gbSize), // wrong resolution for HD
		tor("Metropolis.1927.CAM.x264-GRP", 1*gbSize),
		tor("Metropolis.2001.1080p.BluRay.x264-GRP", 8*gbSize), // the anime
		torSeeded("Metropolis.1927.1080p.WEB-DL.x264-DEAD", 6*gbSize, 0),
	}
}

func TestSearchCopyGrabsTheBestAcceptedRelease(t *testing.T) {
	r := newRig(t)
	id := r.addMovie("Metropolis", 1927, "tt0017136", 19, 153)
	cp := r.addCopy(1, id, "movie", grab.QualityHD)
	r.always(metropolisFeed()...)

	if err := r.svc.SearchCopy(bg(), cp.ID); err != nil {
		t.Fatal(err)
	}
	calls := r.grabber.all()
	if len(calls) != 1 {
		t.Fatalf("%d grabs, want 1: %v", len(calls), r.grabber.titlesGrabbed())
	}
	c := calls[0]
	if c.Release.Title != "Metropolis.1927.1080p.BluRay.x264-GRP" || c.Release.GUID != "metropolis-1080-bluray" {
		t.Errorf("grabbed %q", c.Release.Title)
	}
	wantTarget := grab.Target{CopyID: cp.ID, TitleID: id, Kind: "movie", Quality: grab.QualityHD}
	if !reflect.DeepEqual(c.Target, wantTarget) {
		t.Errorf("target = %+v, want %+v", c.Target, wantTarget)
	}
	rel := c.Release
	if rel.IndexerID != r.ix.ID || rel.Protocol != grab.ProtocolTorrent || rel.Size != 8*gbSize || rel.Seeders != 20 {
		t.Errorf("release = %+v", rel)
	}
	if rel.Quality != "Bluray-1080p" || rel.Score <= 0 || rel.PublishedAt.IsZero() {
		t.Errorf("release quality %q score %d published %v", rel.Quality, rel.Score, rel.PublishedAt)
	}
	if !strings.Contains(rel.DownloadURL, "/download/metropolis-1080-bluray") {
		t.Errorf("the grabber needs the download link: %q", rel.DownloadURL)
	}
	if len(rel.Reasons) == 0 || rel.Reasons[0] != "Best match: 1080p BluRay encode under your size limit" {
		t.Errorf("reasons = %q", rel.Reasons)
	}

	// The search asked by id first (the source lists imdbid and tmdbid).
	q := r.srv.Requests(false)[0].Query
	if q.Get("t") != "movie" || q.Get("imdbid") != "0017136" || q.Get("tmdbid") != "19" || q.Has("q") {
		t.Errorf("search call = %v", q)
	}

	h := r.history(cp.ID)
	if len(h) != 1 {
		t.Fatalf("%d history rows", len(h))
	}
	if h[0].IndexersOK != 1 || h[0].IndexersFailed != 0 || h[0].Results != 6 || h[0].Grabbed != 1 ||
		h[0].BestGUID != "metropolis-1080-bluray" || !reflect.DeepEqual(h[0].GrabbedFor, []string{"movie"}) {
		t.Errorf("history = %+v", h[0])
	}
	if !strings.HasPrefix(h[0].Note, "Sent 1 release for download: Metropolis.1927.1080p.BluRay") {
		t.Errorf("note = %q", h[0].Note)
	}
	noKey(t, "the log", r.logs.String())
}

func TestSearchCopyDoesNothingWhenNothingIsAcceptable(t *testing.T) {
	r := newRig(t)
	id := r.addMovie("Metropolis", 1927, "tt0017136", 19, 153)
	cp := r.addCopy(1, id, "movie", grab.QualityHD)
	r.always(
		tor("Metropolis.1927.CAM.x264-GRP", 1*gbSize),
		tor("Metropolis.1927.2160p.UHD.BluRay.REMUX.HEVC-GRP", 40*gbSize),
		tor("Sintel.2010.1080p.BluRay.x264-GRP", 3*gbSize),
	)
	if err := r.svc.SearchCopy(bg(), cp.ID); err != nil {
		t.Fatalf("a search that finds nothing good is not an error: %v", err)
	}
	if n := len(r.grabber.all()); n != 0 {
		t.Errorf("%d grabs, want none", n)
	}
	h := r.history(cp.ID)
	if len(h) != 1 || h[0].Grabbed != 0 || h[0].Results != 3 || h[0].BestGUID != "" {
		t.Fatalf("history = %+v", h)
	}
	if !strings.HasPrefix(h[0].Note, "Found 3 releases, none acceptable. Closest: ") {
		t.Errorf("note = %q", h[0].Note)
	}
}

func TestSearchCopyWithNoResults(t *testing.T) {
	r := newRig(t)
	id := r.addMovie("Metropolis", 1927, "tt0017136", 19, 153)
	cp := r.addCopy(1, id, "movie", grab.QualityHD)
	r.always()
	if err := r.svc.SearchCopy(bg(), cp.ID); err != nil {
		t.Fatal(err)
	}
	// The id search found nothing, so a text search followed.
	if n := r.srv.Searches(); n != 2 {
		t.Errorf("%d search calls, want 2 (ids, then text)", n)
	}
	if h := r.history(cp.ID); len(h) != 1 || h[0].Note != "Nothing found" || h[0].Grabbed != 0 {
		t.Errorf("history = %+v", h)
	}
}

func TestSearchCopyUsesTheCopysQuality(t *testing.T) {
	r := newRig(t)
	id := r.addMovie("Metropolis", 1927, "tt0017136", 19, 153)
	r.addCopy(1, id, "movie", grab.QualityHD)
	cp4k := r.addCopy(2, id, "movie", grab.QualityUHD)
	r.always(
		tor("Metropolis.1927.1080p.BluRay.x264-GRP", 8*gbSize),
		tor("Metropolis.1927.2160p.BluRay.HDR.x265-GRP", 30*gbSize),
		tor("Metropolis.1927.2160p.WEB-DL.x265-GRP", 15*gbSize),
	)
	if err := r.svc.SearchCopy(bg(), cp4k.ID); err != nil {
		t.Fatal(err)
	}
	calls := r.grabber.all()
	if len(calls) != 1 || calls[0].Release.Title != "Metropolis.1927.2160p.BluRay.HDR.x265-GRP" || calls[0].Target.Quality != grab.QualityUHD {
		t.Fatalf("grabs = %v", r.grabber.titlesGrabbed())
	}
}

func TestSearchCopyKeepsGoingWhenASourceFails(t *testing.T) {
	r := newRig(t)
	id := r.addMovie("Metropolis", 1927, "tt0017136", 19, 153)
	cp := r.addCopy(1, id, "movie", grab.QualityHD)
	r.always(metropolisFeed()...)
	bad := sourcestest.NewServer(t, testKey)
	bad.Fail = 500
	if _, err := r.src.Create(bg(), sources.Input{Name: "Broken", Kind: "torznab", URL: bad.Feed(), APIKey: testKey}); err != nil {
		t.Fatal(err)
	}
	if err := r.svc.SearchCopy(bg(), cp.ID); err != nil {
		t.Fatal(err)
	}
	if len(r.grabber.all()) != 1 {
		t.Fatalf("grabs = %v", r.grabber.titlesGrabbed())
	}
	h := r.history(cp.ID)
	if h[0].IndexersOK != 1 || h[0].IndexersFailed != 1 {
		t.Errorf("history = %+v, want 1 ok and 1 failed", h[0])
	}
}

func TestSearchCopyNeverPicksABlocklistedRelease(t *testing.T) {
	r := newRig(t)
	id := r.addMovie("Metropolis", 1927, "tt0017136", 19, 153)
	cp := r.addCopy(1, id, "movie", grab.QualityHD)
	r.always(metropolisFeed()...)
	if _, err := r.dec.Block(bg(), decide.BlockEntry{TitleID: id, GUID: "metropolis-1080-bluray", Reason: "bad audio"}); err != nil {
		t.Fatal(err)
	}
	if err := r.svc.SearchCopy(bg(), cp.ID); err != nil {
		t.Fatal(err)
	}
	calls := r.grabber.all()
	if len(calls) != 1 || calls[0].Release.Title != "Metropolis.1927.720p.WEB-DL.x264-GRP" {
		t.Fatalf("grabs = %v, want the 720p one now", r.grabber.titlesGrabbed())
	}
}

func TestSearchCopyStaysQuietWhenThereIsNothingToLookFor(t *testing.T) {
	r := newRig(t)
	id := r.addMovie("Metropolis", 1927, "tt0017136", 19, 153)
	cp := r.addCopy(1, id, "movie", grab.QualityHD)
	r.always(metropolisFeed()...)
	r.shelf.have[cp.ID] = Have{Movie: true}
	if err := r.svc.SearchCopy(bg(), cp.ID); err != nil {
		t.Fatal(err)
	}
	if r.srv.Searches() != 0 || len(r.grabber.all()) != 0 {
		t.Errorf("searched %d times and grabbed %d for a film already on the shelf", r.srv.Searches(), len(r.grabber.all()))
	}
	if h := r.history(cp.ID); len(h) != 1 || !strings.HasPrefix(h[0].Note, "Nothing to look for") {
		t.Errorf("history = %+v", h)
	}
}

func TestAGrabbedFilmIsNotGrabbedAgainTheNextDay(t *testing.T) {
	r := newRig(t)
	id := r.addMovie("Metropolis", 1927, "tt0017136", 19, 153)
	cp := r.addCopy(1, id, "movie", grab.QualityHD)
	r.always(metropolisFeed()...)
	if err := r.svc.SearchCopy(bg(), cp.ID); err != nil {
		t.Fatal(err)
	}
	// The download is under way (the file is not on the shelf yet): another search, even with
	// a better release on offer, must not send a second one.
	r.always(append(metropolisFeed(), tor("Metropolis.1927.1080p.BluRay.PROPER.x264-GRP", 8*gbSize))...)
	if err := r.svc.SearchCopy(bg(), cp.ID); err != nil {
		t.Fatal(err)
	}
	if n := len(r.grabber.all()); n != 1 {
		t.Fatalf("%d grabs, want 1", n)
	}
	// After the memory runs out it would search again (the download must have failed).
	r.skew = 25 * 60 * 60 * 1e9
	if err := r.svc.SearchCopy(bg(), cp.ID); err != nil {
		t.Fatal(err)
	}
	if n := len(r.grabber.all()); n != 2 {
		t.Errorf("%d grabs after a day, want 2", n)
	}
}

func TestSearchCopyErrors(t *testing.T) {
	t.Run("no download client", func(t *testing.T) {
		r := newRig(t)
		id := r.addMovie("Metropolis", 1927, "tt0017136", 19, 153)
		cp := r.addCopy(1, id, "movie", grab.QualityHD)
		r.svc.SetGrabber(nil)
		if err := r.svc.SearchCopy(bg(), cp.ID); !errors.Is(err, ErrNoGrabber) {
			t.Errorf("err = %v, want ErrNoGrabber", err)
		}
		if r.srv.Searches() != 0 || len(r.history(cp.ID)) != 0 {
			t.Error("with nowhere to send it, nothing should be searched or recorded")
		}
	})
	t.Run("no sources", func(t *testing.T) {
		r := newRig(t)
		id := r.addMovie("Metropolis", 1927, "tt0017136", 19, 153)
		cp := r.addCopy(1, id, "movie", grab.QualityHD)
		if err := r.src.Delete(bg(), r.ix.ID); err != nil {
			t.Fatal(err)
		}
		if err := r.svc.SearchCopy(bg(), cp.ID); err != nil {
			t.Fatal(err)
		}
		if h := r.history(cp.ID); len(h) != 1 || h[0].Note != "No sources are set up yet" {
			t.Errorf("history = %+v", h)
		}
	})
	t.Run("unknown copy", func(t *testing.T) {
		r := newRig(t)
		if err := r.svc.SearchCopy(bg(), 99); err == nil {
			t.Error("an unknown copy should be an error")
		}
	})
	t.Run("the grab fails", func(t *testing.T) {
		r := newRig(t)
		id := r.addMovie("Metropolis", 1927, "tt0017136", 19, 153)
		cp := r.addCopy(1, id, "movie", grab.QualityHD)
		r.always(metropolisFeed()...)
		r.grabber.err = errors.New("client unreachable")
		if err := r.svc.SearchCopy(bg(), cp.ID); err == nil || !strings.Contains(err.Error(), "client unreachable") {
			t.Errorf("err = %v, want the grabber's error", err)
		}
		h := r.history(cp.ID)
		if len(h) != 1 || h[0].Grabbed != 0 || !strings.Contains(h[0].Note, "stopped early") {
			t.Errorf("history = %+v", h)
		}
		// Nothing was sent, so a later search tries again.
		r.grabber.err = nil
		if err := r.svc.SearchCopy(bg(), cp.ID); err != nil || len(r.grabber.all()) != 1 {
			t.Errorf("retry: %v, %d grabs", err, len(r.grabber.all()))
		}
	})
	t.Run("music is not searched yet", func(t *testing.T) {
		r := newRig(t)
		id := r.addArtist("Frédéric Chopin")
		cp := r.addCopy(1, id, "artist", grab.QualityHD)
		r.always(tor("Chopin - Nocturnes (2011) [FLAC]", 400<<20))
		if err := r.svc.SearchCopy(bg(), cp.ID); err != nil {
			t.Fatal(err)
		}
		if r.srv.Searches() != 0 || len(r.grabber.all()) != 0 {
			t.Error("music must not be searched in this milestone")
		}
		if h := r.history(cp.ID); len(h) != 1 || h[0].Note != "Music is not searched yet" {
			t.Errorf("history = %+v", h)
		}
	})
}

// ---- shows ---------------------------------------------------------------------------

func pioneerEps(n int) []epRow {
	var eps []epRow
	for i := 1; i <= n; i++ {
		eps = append(eps, epRow{Season: 1, Number: i})
	}
	return eps
}

func TestSearchCopyShowOneMissingEpisode(t *testing.T) {
	r := newRig(t)
	id, eps := r.addShow("Pioneer One", 2010, 99001, "tt9900001", 47, pioneerEps(4)...)
	cp := r.addCopy(1, id, "show", grab.QualityHD)
	r.shelf.have[cp.ID] = Have{EpisodeIDs: []int64{eps["1:1"], eps["1:2"], eps["1:3"]}}
	r.always(
		tor("Pioneer.One.S01E03.720p.WEB-DL.x264-GRP", 1*gbSize),
		tor("Pioneer.One.S01E04.720p.WEB-DL.x264-GRP", 1*gbSize),
		tor("Pioneer.One.S01E04.1080p.WEB-DL.x264-GRP", 2*gbSize),
		tor("Pioneer.One.S01.720p.WEB-DL.x264-GRP", 6*gbSize),
	)
	if err := r.svc.SearchCopy(bg(), cp.ID); err != nil {
		t.Fatal(err)
	}
	calls := r.grabber.all()
	if len(calls) != 1 || calls[0].Release.Title != "Pioneer.One.S01E04.1080p.WEB-DL.x264-GRP" {
		t.Fatalf("grabs = %v", r.grabber.titlesGrabbed())
	}
	if !reflect.DeepEqual(calls[0].Target.EpisodeIDs, []int64{eps["1:4"]}) || calls[0].Target.Kind != "show" {
		t.Errorf("target = %+v", calls[0].Target)
	}
	q := r.srv.Requests(false)[0].Query
	if q.Get("t") != "tvsearch" || q.Get("season") != "1" || q.Get("ep") != "4" || q.Get("tvmazeid") != "99001" || q.Has("q") {
		t.Errorf("search call = %v", q)
	}
	if h := r.history(cp.ID); h[0].Grabbed != 1 || !reflect.DeepEqual(h[0].GrabbedFor, []string{"s01e04"}) {
		t.Errorf("history = %+v", h[0])
	}
}

func TestSearchCopyShowPrefersASeasonPackWhenTwoOrMoreAreMissing(t *testing.T) {
	r := newRig(t)
	id, _ := r.addShow("Pioneer One", 2010, 99001, "tt9900001", 47, pioneerEps(4)...)
	cp := r.addCopy(1, id, "show", grab.QualityHD)
	r.always(
		tor("Pioneer.One.S01E01.720p.WEB-DL.x264-GRP", 1*gbSize),
		tor("Pioneer.One.S01E02.720p.WEB-DL.x264-GRP", 1*gbSize),
		tor("Pioneer.One.S01.720p.WEB-DL.x264-GRP", 4*gbSize),
		tor("Pioneer.One.S01.1080p.WEB-DL.x264-HUGE", 40*gbSize), // over the pack limit
		tor("Pioneer.One.S02.720p.WEB-DL.x264-GRP", 4*gbSize),
	)
	if err := r.svc.SearchCopy(bg(), cp.ID); err != nil {
		t.Fatal(err)
	}
	calls := r.grabber.all()
	if len(calls) != 1 || calls[0].Release.Title != "Pioneer.One.S01.720p.WEB-DL.x264-GRP" {
		t.Fatalf("grabs = %v", r.grabber.titlesGrabbed())
	}
	if len(calls[0].Target.EpisodeIDs) != 0 {
		t.Errorf("a whole-season pack is resolved later: %v", calls[0].Target.EpisodeIDs)
	}
	reasons := strings.Join(calls[0].Release.Reasons, "|")
	if !strings.Contains(reasons, "Covers all 4 episodes you need in one download") || !strings.HasPrefix(reasons, "Best match: 720p WEB-DL season pack") {
		t.Errorf("reasons = %q", reasons)
	}
	// One season query, no ep parameter.
	reqs := r.srv.Requests(false)
	if len(reqs) != 1 || reqs[0].Query.Get("season") != "1" || reqs[0].Query.Has("ep") {
		t.Errorf("search calls = %v", reqs)
	}
	if h := r.history(cp.ID); !reflect.DeepEqual(h[0].GrabbedFor, []string{"s01"}) {
		t.Errorf("history = %+v", h[0])
	}
	// The pack covers the season: the next search has nothing to look for.
	r.grabber.mu.Lock()
	n := len(r.grabber.calls)
	r.grabber.mu.Unlock()
	if err := r.svc.SearchCopy(bg(), cp.ID); err != nil {
		t.Fatal(err)
	}
	if len(r.grabber.all()) != n {
		t.Error("the same season was grabbed twice")
	}
}

func TestSearchCopyShowTakesSinglesWhenThereIsNoPack(t *testing.T) {
	r := newRig(t)
	id, eps := r.addShow("Pioneer One", 2010, 99001, "tt9900001", 47, pioneerEps(5)...)
	cp := r.addCopy(1, id, "show", grab.QualityHD)
	r.always(
		tor("Pioneer.One.S01E01.720p.WEB-DL.x264-GRP", 1*gbSize),
		tor("Pioneer.One.S01E01.1080p.WEB-DL.x264-GRP", 2*gbSize), // better
		tor("Pioneer.One.S01E02E03.720p.WEB-DL.x264-GRP", 2*gbSize),
		tor("Pioneer.One.S01E03.720p.WEB-DL.x264-GRP", 1*gbSize), // already covered by the double
		tor("Pioneer.One.S01E05.1080p.HDTV.x264-GRP", 2*gbSize),
	)
	if err := r.svc.SearchCopy(bg(), cp.ID); err != nil {
		t.Fatal(err)
	}
	got := map[string]grabCall{}
	for _, c := range r.grabber.all() {
		got[c.Release.Title] = c
	}
	if len(got) != 3 {
		t.Fatalf("grabs = %v, want three (E01, the E02+E03 double, E05)", r.grabber.titlesGrabbed())
	}
	if c, ok := got["Pioneer.One.S01E01.1080p.WEB-DL.x264-GRP"]; !ok || !reflect.DeepEqual(c.Target.EpisodeIDs, []int64{eps["1:1"]}) {
		t.Errorf("E01: %+v", c)
	}
	if c, ok := got["Pioneer.One.S01E02E03.720p.WEB-DL.x264-GRP"]; !ok || !reflect.DeepEqual(c.Target.EpisodeIDs, []int64{eps["1:2"], eps["1:3"]}) {
		t.Errorf("E02E03: %+v", c)
	}
	if _, ok := got["Pioneer.One.S01E05.1080p.HDTV.x264-GRP"]; !ok {
		t.Error("E05 was not grabbed")
	}
	h := r.history(cp.ID)[0]
	if h.Grabbed != 3 || !strings.Contains(h.Note, "Nothing acceptable yet for S01E04") || !strings.HasPrefix(h.Note, "Sent 3 releases for download") {
		t.Errorf("history = %+v", h)
	}
}

func TestSearchCopyShowSearchesEachSeasonAndSkipsWhatIsNotWanted(t *testing.T) {
	r := newRig(t)
	eps := []epRow{
		{Season: 0, Number: 1}, // a special: never wanted
		{Season: 1, Number: 1}, {Season: 1, Number: 2, Unmonitored: true},
		{Season: 2, Number: 1}, {Season: 2, Number: 2, AirDate: "2999-01-01"}, // not aired yet
		{Season: 3, Number: 1, AirDate: "none"}, // no air date: not aired as far as we know
	}
	id, _ := r.addShow("Pioneer One", 2010, 99001, "tt9900001", 47, eps...)
	cp := r.addCopy(1, id, "show", grab.QualityHD)
	r.always(
		tor("Pioneer.One.S01E01.720p.WEB-DL.x264-GRP", 1*gbSize),
		tor("Pioneer.One.S02E01.720p.WEB-DL.x264-GRP", 1*gbSize),
		tor("Pioneer.One.S01E02.720p.WEB-DL.x264-GRP", 1*gbSize),
		tor("Pioneer.One.S00E01.720p.WEB-DL.x264-GRP", 1*gbSize),
	)
	if err := r.svc.SearchCopy(bg(), cp.ID); err != nil {
		t.Fatal(err)
	}
	got := r.grabber.titlesGrabbed()
	if len(got) != 2 || got[0] != "Pioneer.One.S01E01.720p.WEB-DL.x264-GRP" || got[1] != "Pioneer.One.S02E01.720p.WEB-DL.x264-GRP" {
		t.Fatalf("grabs = %v, want S01E01 then S02E01 only", got)
	}
	reqs := r.srv.Requests(false)
	if len(reqs) != 2 || reqs[0].Query.Get("season") != "1" || reqs[1].Query.Get("season") != "2" {
		t.Errorf("search calls = %v", reqs)
	}
}

func TestSearchCopyShowWithNothingMissing(t *testing.T) {
	r := newRig(t)
	id, eps := r.addShow("Pioneer One", 2010, 99001, "tt9900001", 47, pioneerEps(2)...)
	cp := r.addCopy(1, id, "show", grab.QualityHD)
	r.shelf.have[cp.ID] = Have{EpisodeIDs: []int64{eps["1:1"], eps["1:2"]}}
	r.always(tor("Pioneer.One.S01E01.720p.WEB-DL.x264-GRP", 1*gbSize))
	if err := r.svc.SearchCopy(bg(), cp.ID); err != nil {
		t.Fatal(err)
	}
	if r.srv.Searches() != 0 || len(r.grabber.all()) != 0 {
		t.Error("everything is on the shelf: nothing to search")
	}
	if h := r.history(cp.ID); len(h) != 1 || !strings.HasPrefix(h[0].Note, "Nothing to look for") {
		t.Errorf("history = %+v", h)
	}
}

func TestSearchCopyOnlySearchesTheSeasonsAskedFor(t *testing.T) {
	r := newRig(t)
	id, _ := r.addShow("Pioneer One", 2010, 99001, "tt9900001", 47,
		epRow{Season: 1, Number: 1}, epRow{Season: 2, Number: 1}, epRow{Season: 3, Number: 1})
	cp := r.addCopy(1, id, "show", grab.QualityHD)
	r.svc.SetCopySource(scopedCopies{fakeCopies: r.copies, seasons: map[int64][]int{1: {2}}})
	r.always(
		tor("Pioneer.One.S01E01.720p.WEB-DL.x264-GRP", 1*gbSize),
		tor("Pioneer.One.S02E01.720p.WEB-DL.x264-GRP", 1*gbSize),
		tor("Pioneer.One.S03E01.720p.WEB-DL.x264-GRP", 1*gbSize),
	)
	if err := r.svc.SearchCopy(bg(), cp.ID); err != nil {
		t.Fatal(err)
	}
	if got := r.grabber.titlesGrabbed(); len(got) != 1 || got[0] != "Pioneer.One.S02E01.720p.WEB-DL.x264-GRP" {
		t.Errorf("grabs = %v, want only season 2", got)
	}
}

func TestShowWithoutAShelfCountsEverythingAiredAsMissing(t *testing.T) {
	r := newRig(t)
	id, _ := r.addShow("Pioneer One", 2010, 99001, "tt9900001", 47, pioneerEps(1)...)
	cp := r.addCopy(1, id, "show", grab.QualityHD)
	r.svc.SetShelf(nil)
	r.always(tor("Pioneer.One.S01E01.720p.WEB-DL.x264-GRP", 1*gbSize))
	if err := r.svc.SearchCopy(bg(), cp.ID); err != nil || len(r.grabber.all()) != 1 {
		t.Errorf("err %v grabs %d", err, len(r.grabber.all()))
	}
}
