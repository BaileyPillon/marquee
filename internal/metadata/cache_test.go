package metadata

import (
	"errors"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/BaileyPillon/marquee/internal/core/outbound"
)

func TestCacheHitSkipsTheNetwork(t *testing.T) {
	e, api := tvmazeEnv(t)
	for i := 0; i < 3; i++ {
		hits, err := e.svc.TVmaze.SearchShows(bg(), "Pioneer One")
		if err != nil || len(hits) != 2 {
			t.Fatalf("call %d: %v %v", i, hits, err)
		}
	}
	if n := api.Count("/search/shows"); n != 1 {
		t.Errorf("provider was asked %d times, want 1", n)
	}
	// The cache key ignores case and surrounding spaces.
	if _, err := e.svc.TVmaze.SearchShows(bg(), "  PIONEER one "); err != nil {
		t.Fatal(err)
	}
	if n := api.Count("/search/shows"); n != 1 {
		t.Errorf("normalised repeat went out: %d", n)
	}
}

func TestCacheSurvivesARestart(t *testing.T) {
	e, api := tvmazeEnv(t)
	if _, err := e.svc.TVmaze.Show(bg(), "99001"); err != nil {
		t.Fatal(err)
	}
	// A second service on the same database is a restarted program.
	hc := outbound.New("1.2.3", outbound.Options{MaxRetries: -1})
	svc2, err := New(e.db, hc, Options{TVmazeURL: api.URL(), ArtworkDir: t.TempDir(), Now: e.clock.Now})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := svc2.TVmaze.Show(bg(), "99001"); err != nil {
		t.Fatal(err)
	}
	if n := api.Count("/shows/99001"); n != 1 {
		t.Errorf("provider asked %d times across a restart, want 1", n)
	}
}

func TestCacheExpiryFollowsTTL(t *testing.T) {
	e, api := tvmazeEnv(t)
	search := func() { e.svc.TVmaze.SearchShows(bg(), "pioneer") }
	show := func() { e.svc.TVmaze.Show(bg(), "99001") }
	search()
	show()

	e.clock.Advance(SearchTTL - time.Minute)
	search()
	show()
	if api.Count("/search/shows") != 1 || api.Count("/shows/99001") != 1 {
		t.Fatalf("inside the TTL: search=%d show=%d, want 1 and 1", api.Count("/search/shows"), api.Count("/shows/99001"))
	}

	e.clock.Advance(2 * time.Minute) // 6h01m: search expired, details (24h) not
	search()
	show()
	if api.Count("/search/shows") != 2 || api.Count("/shows/99001") != 1 {
		t.Fatalf("after 6h: search=%d show=%d, want 2 and 1", api.Count("/search/shows"), api.Count("/shows/99001"))
	}

	e.clock.Advance(DetailsTTL)
	show()
	if api.Count("/shows/99001") != 2 {
		t.Errorf("after 24h the details were not refetched: %d", api.Count("/shows/99001"))
	}
	if SearchTTL != 6*time.Hour || DetailsTTL != 24*time.Hour {
		t.Errorf("TTLs changed: search %v details %v", SearchTTL, DetailsTTL)
	}
}

func TestStaleCopyIsServedWhileTheProviderIsDown(t *testing.T) {
	e, api := tvmazeEnv(t)
	first, err := e.svc.TVmaze.SearchShows(bg(), "pioneer")
	if err != nil {
		t.Fatal(err)
	}

	e.clock.Advance(SearchTTL + time.Hour)
	api.Set("/search/shows", httpStatus(http.StatusServiceUnavailable))
	again, err := e.svc.TVmaze.SearchShows(bg(), "pioneer")
	if err != nil {
		t.Fatalf("an outage with a stale copy must not be an error: %v", err)
	}
	if len(again) != len(first) || again[0] != first[0] {
		t.Errorf("stale answer differs: %+v", again)
	}
	if api.Count("/search/shows") != 2 {
		t.Errorf("the provider should have been tried once more: %d", api.Count("/search/shows"))
	}
	st := e.svc.Status(bg()).Providers[ProviderTVmaze]
	if !strings.Contains(st.LastError, "upstream_error") || st.LastErrorAt == nil {
		t.Errorf("status after serving stale = %+v, want the error recorded", st)
	}
	if !strings.Contains(e.logs.String(), "serving the stale copy") {
		t.Error("serving a stale copy should leave a log line")
	}

	// Provider recovers: fresh data, error cleared.
	api.Set("/search/shows", fixture(t, "tvmaze_search_pioneer.json"))
	if _, err := e.svc.TVmaze.SearchShows(bg(), "pioneer"); err != nil {
		t.Fatal(err)
	}
	st = e.svc.Status(bg()).Providers[ProviderTVmaze]
	if st.LastError != "" || st.LastOK == nil {
		t.Errorf("status after recovery = %+v", st)
	}
}

func TestOutageWithNoStaleCopyIsAnError(t *testing.T) {
	e, api := tvmazeEnv(t)
	api.Set("/search/shows", httpStatus(500))
	_, err := e.svc.TVmaze.SearchShows(bg(), "pioneer")
	if !errors.Is(err, ErrUpstream) {
		t.Fatalf("err = %v", err)
	}
	if CodeOf(err) != "upstream_error" || MessageOf(err) == "" {
		t.Errorf("code %q message %q", CodeOf(err), MessageOf(err))
	}
}

func TestNotFoundIsNotPaperedOverWithStaleData(t *testing.T) {
	e, api := tvmazeEnv(t)
	if _, err := e.svc.TVmaze.Show(bg(), "99001"); err != nil {
		t.Fatal(err)
	}
	e.clock.Advance(DetailsTTL + time.Hour)
	api.Set("/shows/99001", httpStatus(http.StatusNotFound))
	if _, err := e.svc.TVmaze.Show(bg(), "99001"); !errors.Is(err, ErrNotFound) {
		t.Errorf("a 404 after expiry must be reported, got %v", err)
	}
	if st := e.svc.Status(bg()).Providers[ProviderTVmaze]; st.LastError != "" {
		t.Errorf("a 404 is an answer, not an outage: %+v", st)
	}
}

func TestBypassCacheForcesARefetchAndStoresIt(t *testing.T) {
	e, api := tvmazeEnv(t)
	if _, err := e.svc.TVmaze.Show(bg(), "99001"); err != nil {
		t.Fatal(err)
	}
	if _, err := e.svc.TVmaze.Show(BypassCache(bg()), "99001"); err != nil {
		t.Fatal(err)
	}
	if api.Count("/shows/99001") != 2 {
		t.Fatalf("bypass did not refetch: %d", api.Count("/shows/99001"))
	}
	if _, err := e.svc.TVmaze.Show(bg(), "99001"); err != nil {
		t.Fatal(err)
	}
	if api.Count("/shows/99001") != 2 {
		t.Errorf("an ordinary call after a refresh should hit the refreshed cache: %d", api.Count("/shows/99001"))
	}
}

func TestBypassCacheSparesTrackLists(t *testing.T) {
	e, api := mbEnv(t)
	if _, err := e.svc.MusicBrainz.Artist(bg(), chopinMBID); err != nil {
		t.Fatal(err)
	}
	before := api.Total() // 8
	if _, err := e.svc.MusicBrainz.Artist(BypassCache(bg()), chopinMBID); err != nil {
		t.Fatal(err)
	}
	// A refresh asks again for the artist and the album list, not for the
	// track list of every album.
	if extra := api.Total() - before; extra != 2 {
		t.Errorf("refresh made %d requests, want 2 (artist and release groups)", extra)
	}
}

func TestPruneKeepsRecentlyExpiredEntries(t *testing.T) {
	e, _ := tvmazeEnv(t)
	e.svc.TVmaze.SearchShows(bg(), "a")
	e.clock.Advance(10 * 24 * time.Hour)
	e.svc.TVmaze.SearchShows(bg(), "b")
	if e.cacheRows() != 2 {
		t.Fatalf("rows = %d", e.cacheRows())
	}
	if err := e.svc.PruneCache(bg()); err != nil {
		t.Fatal(err)
	}
	if e.cacheRows() != 2 {
		t.Errorf("an entry expired 10 days ago is still a usable stale copy, rows = %d", e.cacheRows())
	}
	e.clock.Advance(25 * 24 * time.Hour) // "a" is now over 30 days past expiry
	if err := e.svc.PruneCache(bg()); err != nil {
		t.Fatal(err)
	}
	if e.cacheRows() != 1 {
		t.Errorf("rows = %d, want only the newer entry", e.cacheRows())
	}
}

func TestBrokenCacheDoesNotStopLookups(t *testing.T) {
	e, api := tvmazeEnv(t)
	if _, err := e.db.Exec(`DROP TABLE metadata_cache`); err != nil {
		t.Fatal(err)
	}
	hits, err := e.svc.TVmaze.SearchShows(bg(), "pioneer")
	if err != nil || len(hits) != 2 {
		t.Fatalf("hits=%v err=%v", hits, err)
	}
	if api.Count("/search/shows") != 1 {
		t.Error("lookup did not reach the provider")
	}
	if !strings.Contains(e.logs.String(), "metadata cache") {
		t.Error("a broken cache should be logged")
	}
}

func TestStatusStartsClean(t *testing.T) {
	e := newEnv(t, Options{})
	rep := e.svc.Status(bg())
	for _, p := range []string{ProviderTMDB, ProviderTVmaze, ProviderMusicBrainz, ProviderArtwork} {
		s, ok := rep.Providers[p]
		if !ok || s.LastError != "" || s.LastOK != nil {
			t.Errorf("%s status = %+v ok=%v", p, s, ok)
		}
	}
	if rep.HasKey {
		t.Error("HasKey with no key function")
	}
}
