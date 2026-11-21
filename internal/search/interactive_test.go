package search

import (
	"context"
	"errors"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/BaileyPillon/marquee/internal/grab"
	"github.com/BaileyPillon/marquee/internal/sources"
	"github.com/BaileyPillon/marquee/internal/sources/sourcestest"
	"github.com/BaileyPillon/marquee/internal/titles"
)

func TestInteractiveReturnsEveryResultWithItsVerdict(t *testing.T) {
	r := newRig(t)
	id := r.addMovie("Metropolis", 1927, "tt0017136", 19, 153)
	r.addCopy(1, id, "movie", grab.QualityHD)
	r.always(metropolisFeed()...)
	bad := sourcestest.NewServer(t, testKey)
	bad.Fail = 503
	if _, err := r.src.Create(bg(), sources.Input{Name: "Broken", Kind: "torznab", URL: bad.Feed(), APIKey: testKey}); err != nil {
		t.Fatal(err)
	}

	res, err := r.svc.Interactive(bg(), id, grab.QualityHD, 0, 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Results) != 6 {
		t.Fatalf("%d results, want all 6", len(res.Results))
	}
	if res.IndexersSearched != 2 || len(res.IndexersFailed) != 1 || res.IndexersFailed[0].Name != "Broken" || res.IndexersFailed[0].Error == "" {
		t.Errorf("searched %d failed %+v", res.IndexersSearched, res.IndexersFailed)
	}
	if time.Since(res.SearchedAt) > time.Minute {
		t.Errorf("searchedAt = %v", res.SearchedAt)
	}

	// Accepted ones first, best first, with the headline the table shows.
	first := res.Results[0]
	if !first.Accepted || first.Title != "Metropolis.1927.1080p.BluRay.x264-GRP" || first.Reasons[0] != "Best match: 1080p BluRay encode under your size limit" {
		t.Errorf("first = %+v", first)
	}
	if first.IndexerID != r.ix.ID || first.IndexerName != "Alpha" || first.GUID != "metropolis-1080-bluray" || first.Quality != "Bluray-1080p" ||
		first.Size != 8*gbSize || first.Seeders != 20 || first.Protocol != grab.ProtocolTorrent || first.PublishedAt.IsZero() || first.Score <= 0 || first.Rejection != "" {
		t.Errorf("first = %+v", first)
	}
	if second := res.Results[1]; !second.Accepted || !strings.HasPrefix(second.Reasons[0], "Good fit: 720p WEB-DL") {
		t.Errorf("second = %+v", second)
	}
	rejected := map[string]string{}
	for _, x := range res.Results {
		if !x.Accepted {
			rejected[x.Title] = x.Rejection
			if x.Score != 0 || len(x.Reasons) == 0 || x.Rejection == "" {
				t.Errorf("a rejected result needs a reason: %+v", x)
			}
		}
	}
	want := map[string]string{
		"Metropolis.1927.2160p.UHD.BluRay.REMUX.HEVC-GRP": "Resolution not allowed: this is 2160p, your profile takes 720p or 1080p",
		"Metropolis.1927.CAM.x264-GRP":                    "Rejected: this is a camera recording (CAM) from a cinema",
		"Metropolis.2001.1080p.BluRay.x264-GRP":           "Wrong year: this is from 2001, you want the 1927 one",
		"Metropolis.1927.1080p.WEB-DL.x264-DEAD":          "No one is sharing it (0 seeders)",
	}
	for title, rej := range want {
		if got := rejected[title]; got != rej {
			t.Errorf("%s: rejection = %q, want %q", title, got, rej)
		}
	}

	// The result type has no field that could carry a download link.
	rt := reflect.TypeOf(Result{})
	for i := 0; i < rt.NumField(); i++ {
		name := strings.ToLower(rt.Field(i).Name)
		for _, bad := range []string{"url", "link", "magnet", "download", "hash"} {
			if strings.Contains(name, bad) {
				t.Errorf("Result has a field %q that could carry a link", rt.Field(i).Name)
			}
		}
	}
}

func TestInteractiveForAShow(t *testing.T) {
	r := newRig(t)
	id, _ := r.addShow("Pioneer One", 2010, 99001, "tt9900001", 47, pioneerEps(4)...)
	r.addCopy(1, id, "show", grab.QualityHD)
	r.always(
		tor("Pioneer.One.S01E04.720p.WEB-DL.x264-GRP", 1*gbSize),
		tor("Pioneer.One.S01E03.720p.WEB-DL.x264-GRP", 1*gbSize),
		tor("Pioneer.One.S01.720p.WEB-DL.x264-GRP", 4*gbSize),
	)
	accepted := func(res InteractiveResult) []string {
		var out []string
		for _, x := range res.Results {
			if x.Accepted {
				out = append(out, x.Title)
			}
		}
		return out
	}

	// One episode: the pack is turned down, the other episode too, with the owner's wording.
	res, err := r.svc.Interactive(bg(), id, grab.QualityHD, 1, 4)
	if err != nil {
		t.Fatal(err)
	}
	if got := accepted(res); len(got) != 1 || got[0] != "Pioneer.One.S01E04.720p.WEB-DL.x264-GRP" {
		t.Errorf("accepted for S01E04 = %v", got)
	}
	var sawWrong, sawPack bool
	for _, x := range res.Results {
		sawWrong = sawWrong || x.Rejection == "Wrong episode: this is S01E03, you need S01E04"
		sawPack = sawPack || x.Rejection == "Season pack, but you only need S01E04"
	}
	if !sawWrong || !sawPack {
		t.Errorf("rejections: wrong episode %v, pack %v", sawWrong, sawPack)
	}
	q := r.srv.Requests(false)[0].Query
	if q.Get("season") != "1" || q.Get("ep") != "4" {
		t.Errorf("search call = %v", q)
	}

	// The whole season: the pack and every episode are acceptable.
	res, err = r.svc.Interactive(bg(), id, grab.QualityHD, 1, 0)
	if err != nil {
		t.Fatal(err)
	}
	if got := accepted(res); len(got) != 3 {
		t.Errorf("accepted for season 1 = %v", got)
	}
	if reqs := r.srv.Requests(false); reqs[len(reqs)-1].Query.Has("ep") {
		t.Errorf("a season search must not send ep: %v", reqs[len(reqs)-1].Query)
	}

	// No season: every release is judged on its own merits.
	res, err = r.svc.Interactive(bg(), id, grab.QualityHD, 0, 0)
	if err != nil || len(accepted(res)) != 3 {
		t.Errorf("no scope: %d accepted, err %v", len(accepted(res)), err)
	}
	if reqs := r.srv.Requests(false); reqs[len(reqs)-1].Query.Has("season") {
		t.Errorf("a whole-show search must not send season: %v", reqs[len(reqs)-1].Query)
	}
}

func TestInteractiveInputChecks(t *testing.T) {
	r := newRig(t)
	id := r.addMovie("Metropolis", 1927, "tt0017136", 19, 153)
	for name, c := range map[string]struct {
		quality string
		season  int
		episode int
	}{
		"bad quality":        {"sd", 0, 0},
		"negative season":    {"hd", -1, 0},
		"episode no season":  {"hd", 0, 3},
		"negative episode":   {"hd", 1, -2},
		"empty quality":      {"", 0, 0},
		"uppercase quality":  {"HD", 0, 0},
		"quality with space": {"hd ", 0, 0},
	} {
		_, err := r.svc.Interactive(bg(), id, c.quality, c.season, c.episode)
		var ie *InvalidError
		if !errors.As(err, &ie) {
			t.Errorf("%s: err = %v, want an InvalidError", name, err)
		}
	}
	if _, err := r.svc.Interactive(bg(), 999, "hd", 0, 0); !errors.Is(err, titles.ErrNotFound) {
		t.Errorf("unknown title: %v", err)
	}
	if r.srv.Searches() != 0 {
		t.Error("an invalid request must not search")
	}
}

func TestInteractiveWithNoSources(t *testing.T) {
	r := newRig(t)
	id := r.addMovie("Metropolis", 1927, "tt0017136", 19, 153)
	if err := r.src.Delete(bg(), r.ix.ID); err != nil {
		t.Fatal(err)
	}
	res, err := r.svc.Interactive(bg(), id, "hd", 0, 0)
	if err != nil || len(res.Results) != 0 || res.IndexersSearched != 0 {
		t.Errorf("res = %+v err %v", res, err)
	}
}

// ---- grabbing by hand ------------------------------------------------------------------

func TestGrabReleasePicksFromTheLastManualSearch(t *testing.T) {
	r := newRig(t)
	id := r.addMovie("Metropolis", 1927, "tt0017136", 19, 153)
	cp := r.addCopy(1, id, "movie", grab.QualityHD)
	r.always(metropolisFeed()...)
	res, err := r.svc.Interactive(bg(), id, grab.QualityHD, 0, 0)
	if err != nil {
		t.Fatal(err)
	}
	pick := res.Results[1] // the 720p one, not the best
	out, err := r.svc.GrabRelease(bg(), id, pick.IndexerID, pick.GUID, grab.QualityHD)
	if err != nil {
		t.Fatal(err)
	}
	if out.DownloadID != 101 || out.Title != pick.Title || out.Quality != "WEBDL-720p" || out.Score != pick.Score {
		t.Errorf("result = %+v", out)
	}
	calls := r.grabber.all()
	if len(calls) != 1 {
		t.Fatalf("%d grabs", len(calls))
	}
	c := calls[0]
	if c.Release.Title != pick.Title || c.Target.CopyID != cp.ID || c.Target.TitleID != id || c.Target.Quality != grab.QualityHD {
		t.Errorf("grab = %+v", c)
	}
	if !strings.Contains(c.Release.DownloadURL, "/download/") {
		t.Errorf("the download link must reach the grabber: %q", c.Release.DownloadURL)
	}
	if c.Release.Reasons[0] != "Picked by hand from the releases found" || !strings.HasPrefix(c.Release.Reasons[1], "Good fit: 720p WEB-DL") {
		t.Errorf("reasons = %q", c.Release.Reasons)
	}
	if h := r.history(cp.ID); len(h) != 1 || h[0].Grabbed != 1 || h[0].Note != "Picked by hand: "+pick.Title || !reflect.DeepEqual(h[0].GrabbedFor, []string{"movie"}) {
		t.Errorf("history = %+v", h)
	}
	noKey(t, "the log", r.logs.String())
}

func TestGrabReleaseHonoursTheOwnersChoiceEvenForARejectedRelease(t *testing.T) {
	r := newRig(t)
	id := r.addMovie("Metropolis", 1927, "tt0017136", 19, 153)
	r.addCopy(1, id, "movie", grab.QualityHD)
	r.always(metropolisFeed()...)
	res, _ := r.svc.Interactive(bg(), id, grab.QualityHD, 0, 0)
	var dead Result
	for _, x := range res.Results {
		if strings.HasSuffix(x.Title, "-DEAD") {
			dead = x
		}
	}
	if _, err := r.svc.GrabRelease(bg(), id, dead.IndexerID, dead.GUID, grab.QualityHD); err != nil {
		t.Fatal(err)
	}
	reasons := r.grabber.all()[0].Release.Reasons
	if reasons[0] != "Picked by hand from the releases found" || reasons[1] != "No one is sharing it (0 seeders)" {
		t.Errorf("reasons = %q: the grab should say what the profile thought of it", reasons)
	}
}

func TestGrabReleaseOfAnEpisodeKnowsTheEpisode(t *testing.T) {
	r := newRig(t)
	id, eps := r.addShow("Pioneer One", 2010, 99001, "tt9900001", 47, pioneerEps(4)...)
	r.addCopy(1, id, "show", grab.QualityHD)
	r.always(
		tor("Pioneer.One.S01E04.720p.WEB-DL.x264-GRP", 1*gbSize),
		tor("Pioneer.One.S01E02E03.720p.WEB-DL.x264-GRP", 2*gbSize),
		tor("Pioneer.One.S01.720p.WEB-DL.x264-GRP", 4*gbSize),
	)
	res, _ := r.svc.Interactive(bg(), id, grab.QualityHD, 1, 0)
	byTitle := map[string]Result{}
	for _, x := range res.Results {
		byTitle[x.Title] = x
	}
	grabTitle := func(title string) grab.Target {
		x := byTitle[title]
		if _, err := r.svc.GrabRelease(bg(), id, x.IndexerID, x.GUID, grab.QualityHD); err != nil {
			t.Fatal(err)
		}
		calls := r.grabber.all()
		return calls[len(calls)-1].Target
	}
	if tg := grabTitle("Pioneer.One.S01E04.720p.WEB-DL.x264-GRP"); !reflect.DeepEqual(tg.EpisodeIDs, []int64{eps["1:4"]}) || tg.Kind != "show" {
		t.Errorf("single: %+v", tg)
	}
	if tg := grabTitle("Pioneer.One.S01E02E03.720p.WEB-DL.x264-GRP"); !reflect.DeepEqual(tg.EpisodeIDs, []int64{eps["1:2"], eps["1:3"]}) {
		t.Errorf("double: %+v", tg)
	}
	if tg := grabTitle("Pioneer.One.S01.720p.WEB-DL.x264-GRP"); len(tg.EpisodeIDs) != 0 {
		t.Errorf("pack: %+v", tg)
	}
}

func TestGrabReleaseFallsBackToWhatTheFeedsShowed(t *testing.T) {
	r := newRig(t)
	id := r.addMovie("Metropolis", 1927, "tt0017136", 19, 153)
	r.addCopy(1, id, "movie", grab.QualityHD)
	r.always(metropolisFeed()...)
	// No manual search was made, but the feed sync remembered the releases (and, with a copy
	// already on its way, grabs nothing more).
	if err := r.src.SyncRSS(bg()); err != nil {
		t.Fatal(err)
	}
	if n := len(r.grabber.all()); n != 1 {
		t.Fatalf("setup: %d grabs", n)
	}
	out, err := r.svc.GrabRelease(bg(), id, r.ix.ID, "Metropolis.1927.720p.WEB-DL.x264-GRP", grab.QualityHD)
	if err != nil {
		t.Fatal(err)
	}
	last := r.grabber.all()[1]
	if out.Title != "Metropolis.1927.720p.WEB-DL.x264-GRP" || !strings.Contains(last.Release.DownloadURL, "/download/") {
		t.Errorf("grab = %+v / %+v: the sealed link should come back open", out, last.Release)
	}
}

func TestGrabReleaseCacheExpiresAfterTenMinutes(t *testing.T) {
	r := newRig(t)
	id := r.addMovie("Metropolis", 1927, "tt0017136", 19, 153)
	r.addCopy(1, id, "movie", grab.QualityHD)
	r.always(metropolisFeed()...)
	res, _ := r.svc.Interactive(bg(), id, grab.QualityHD, 0, 0)
	pick := res.Results[0]

	r.skew = 11 * time.Minute
	if _, err := r.svc.GrabRelease(bg(), id, pick.IndexerID, pick.GUID, grab.QualityHD); !errors.Is(err, ErrReleaseGone) {
		t.Errorf("after 11 minutes: err = %v, want ErrReleaseGone (a manual search is not stored)", err)
	}
	// Just inside the window it still works.
	r.skew = 9 * time.Minute
	if _, err := r.svc.GrabRelease(bg(), id, pick.IndexerID, pick.GUID, grab.QualityHD); err != nil {
		t.Errorf("after 9 minutes: %v", err)
	}
}

func TestGrabReleaseErrors(t *testing.T) {
	t.Run("no download client", func(t *testing.T) {
		r := newRig(t)
		id := r.addMovie("Metropolis", 1927, "tt0017136", 19, 153)
		r.addCopy(1, id, "movie", grab.QualityHD)
		r.svc.SetGrabber(nil)
		if _, err := r.svc.GrabRelease(bg(), id, r.ix.ID, "g", "hd"); !errors.Is(err, ErrNoGrabber) {
			t.Errorf("err = %v, want ErrNoGrabber", err)
		}
	})
	t.Run("the downloads module says there is no client", func(t *testing.T) {
		r := newRig(t)
		id := r.addMovie("Metropolis", 1927, "tt0017136", 19, 153)
		r.addCopy(1, id, "movie", grab.QualityHD)
		r.always(metropolisFeed()...)
		res, _ := r.svc.Interactive(bg(), id, "hd", 0, 0)
		r.grabber.err = errors.Join(ErrNoGrabber, errors.New("wrapped"))
		if _, err := r.svc.GrabRelease(bg(), id, res.Results[0].IndexerID, res.Results[0].GUID, "hd"); !errors.Is(err, ErrNoGrabber) {
			t.Errorf("err = %v", err)
		}
	})
	t.Run("a release nobody remembers", func(t *testing.T) {
		r := newRig(t)
		id := r.addMovie("Metropolis", 1927, "tt0017136", 19, 153)
		r.addCopy(1, id, "movie", grab.QualityHD)
		if _, err := r.svc.GrabRelease(bg(), id, r.ix.ID, "never-seen", "hd"); !errors.Is(err, ErrReleaseGone) {
			t.Errorf("err = %v, want ErrReleaseGone", err)
		}
	})
	t.Run("no copy of that quality", func(t *testing.T) {
		r := newRig(t)
		id := r.addMovie("Metropolis", 1927, "tt0017136", 19, 153)
		r.addCopy(1, id, "movie", grab.QualityHD)
		r.always(metropolisFeed()...)
		res, _ := r.svc.Interactive(bg(), id, "uhd", 0, 0)
		if _, err := r.svc.GrabRelease(bg(), id, res.Results[0].IndexerID, res.Results[0].GUID, "uhd"); !errors.Is(err, ErrNoCopy) {
			t.Errorf("err = %v, want ErrNoCopy", err)
		}
		if len(r.grabber.all()) != 0 {
			t.Error("grabbed without a copy")
		}
	})
	t.Run("input checks", func(t *testing.T) {
		r := newRig(t)
		id := r.addMovie("Metropolis", 1927, "tt0017136", 19, 153)
		var ie *InvalidError
		if _, err := r.svc.GrabRelease(bg(), id, r.ix.ID, "g", "8k"); !errors.As(err, &ie) {
			t.Errorf("bad quality: %v", err)
		}
		if _, err := r.svc.GrabRelease(bg(), id, 0, "g", "hd"); !errors.As(err, &ie) {
			t.Errorf("no indexer: %v", err)
		}
		if _, err := r.svc.GrabRelease(bg(), id, r.ix.ID, "", "hd"); !errors.As(err, &ie) {
			t.Errorf("no guid: %v", err)
		}
		if _, err := r.svc.GrabRelease(bg(), 999, r.ix.ID, "g", "hd"); !errors.Is(err, titles.ErrNotFound) {
			t.Errorf("unknown title: %v", err)
		}
	})
	t.Run("the grab itself fails", func(t *testing.T) {
		r := newRig(t)
		id := r.addMovie("Metropolis", 1927, "tt0017136", 19, 153)
		cp := r.addCopy(1, id, "movie", grab.QualityHD)
		r.always(metropolisFeed()...)
		res, _ := r.svc.Interactive(bg(), id, "hd", 0, 0)
		r.grabber.err = errors.New("client rejected it")
		if _, err := r.svc.GrabRelease(bg(), id, res.Results[0].IndexerID, res.Results[0].GUID, "hd"); err == nil {
			t.Error("the grabber's error should come back")
		}
		if h := r.history(cp.ID); len(h) != 0 {
			t.Errorf("a failed grab must not be recorded as sent: %+v", h)
		}
	})
}

func TestGrabReleaseFindsTheCopyThroughTheOptionalFinder(t *testing.T) {
	r := newRig(t)
	id := r.addMovie("Metropolis", 1927, "tt0017136", 19, 153)
	cp := r.addCopy(1, id, "movie", grab.QualityUHD)
	// The 4K copy is fully satisfied (so Wanted would not list it), but the finder still knows it.
	r.copies.mu.Lock()
	c := r.copies.copies[1]
	c.Monitored = false
	r.copies.copies[1] = c
	r.copies.mu.Unlock()
	r.svc.SetCopySource(finderCopies{r.copies})
	r.always(tor("Metropolis.1927.2160p.BluRay.HDR.x265-GRP", 30*gbSize))
	res, _ := r.svc.Interactive(bg(), id, "uhd", 0, 0)
	if _, err := r.svc.GrabRelease(bg(), id, res.Results[0].IndexerID, res.Results[0].GUID, "uhd"); err != nil {
		t.Fatal(err)
	}
	if got := r.grabber.all(); len(got) != 1 || got[0].Target.CopyID != cp.ID || got[0].Target.Quality != grab.QualityUHD {
		t.Errorf("grabs = %+v", got)
	}
}

// ensurerCopies can make a copy on demand, the way the requests module can.
type ensurerCopies struct {
	*fakeCopies
	err error
}

func (e ensurerCopies) EnsureCopy(_ context.Context, titleID int64, quality string) (grab.Copy, error) {
	if e.err != nil {
		return grab.Copy{}, e.err
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	c := grab.Copy{ID: int64(len(e.copies) + 1), TitleID: titleID, Kind: "movie", Quality: quality, Monitored: true}
	e.copies[c.ID] = c
	return c, nil
}

func TestGrabReleaseMakesTheCopyWhenTheCopySourceCan(t *testing.T) {
	r := newRig(t)
	id := r.addMovie("Metropolis", 1927, "tt0017136", 19, 153)
	r.addCopy(1, id, "movie", grab.QualityHD)
	r.svc.SetCopySource(ensurerCopies{fakeCopies: r.copies})
	r.always(tor("Metropolis.1927.2160p.BluRay.HDR.x265-GRP", 30*gbSize))
	res, _ := r.svc.Interactive(bg(), id, "uhd", 0, 0)
	if _, err := r.svc.GrabRelease(bg(), id, res.Results[0].IndexerID, res.Results[0].GUID, "uhd"); err != nil {
		t.Fatal(err)
	}
	got := r.grabber.all()
	if len(got) != 1 || got[0].Target.CopyID != 2 || got[0].Target.Quality != grab.QualityUHD {
		t.Errorf("grabs = %+v, want the new 4K copy (id 2)", got)
	}

	// When the copy cannot be made (no folder for it, say) the answer is the no-copy one.
	r.svc.SetCopySource(ensurerCopies{fakeCopies: r.copies, err: errors.New("no library folder")})
	if _, err := r.svc.GrabRelease(bg(), id, res.Results[0].IndexerID, res.Results[0].GUID, "uhd"); !errors.Is(err, ErrNoCopy) {
		t.Errorf("err = %v, want ErrNoCopy", err)
	}
}

func TestInteractiveMemoryStaysSmall(t *testing.T) {
	r := newRig(t)
	r.always(tor("Metropolis.1927.1080p.BluRay.x264-GRP", 8*gbSize))
	for i := 0; i < maxCachedTitles+10; i++ {
		id := r.addMovie("Metropolis", 1927+i, "", 0, 153)
		if _, err := r.svc.Interactive(bg(), id, "hd", 0, 0); err != nil {
			t.Fatal(err)
		}
	}
	r.svc.cacheMu.Lock()
	n := len(r.svc.cache)
	r.svc.cacheMu.Unlock()
	if n > maxCachedTitles {
		t.Errorf("%d titles remembered, want at most %d", n, maxCachedTitles)
	}
}
