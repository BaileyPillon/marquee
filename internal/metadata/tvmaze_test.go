package metadata

import (
	"errors"
	"net/http"
	"slices"
	"strings"
	"testing"
	"time"
)

func tvmazeEnv(t *testing.T) (*env, *fakeAPI) {
	t.Helper()
	api := newFakeAPI(t, map[string]http.HandlerFunc{
		"/search/shows": fixture(t, "tvmaze_search_pioneer.json"),
		"/shows/99001":  fixture(t, "tvmaze_show_pioneer.json"),
	})
	return newEnv(t, Options{TVmazeURL: api.URL()}), api
}

func TestTVmazeSearchMapsHits(t *testing.T) {
	e, api := tvmazeEnv(t)
	hits, err := e.svc.TVmaze.SearchShows(bg(), "Pioneer One")
	if err != nil {
		t.Fatal(err)
	}
	if len(hits) != 2 {
		t.Fatalf("hits = %d, want 2", len(hits))
	}
	first := hits[0]
	want := SearchHit{
		Kind: KindShow, Provider: ProviderTVmaze, ProviderID: "99001", Name: "Pioneer One", Year: 2010,
		Overview: "When a mysterious object crashes in the Canadian wilderness, a team of agents is sent to find out why & what it means.",
		Poster:   Image{Ref: "tvmaze/uploads/images/original_untouched/99/99001.jpg", Kind: "poster"},
		IMDbID:   "tt9900001", Detail: "VODO",
	}
	if first != want {
		t.Errorf("first hit =\n%+v\nwant\n%+v", first, want)
	}
	second := hits[1]
	if second.Year != 0 || !second.Poster.IsZero() || second.Overview != "" || second.Detail != "CBC" || second.IMDbID != "" {
		t.Errorf("second hit (no premiere, no image, no summary) = %+v", second)
	}
	if got := api.Last("/search/shows"); got.RawQuery != "q=Pioneer+One" {
		t.Errorf("query = %q", got.RawQuery)
	}
	if ua := api.Last("/search/shows").Header.Get("User-Agent"); !strings.HasPrefix(ua, "Marquee/1.2.3 (+https://") {
		t.Errorf("User-Agent = %q, want the outbound client's Marquee agent with contact URL", ua)
	}
}

func TestTVmazeSearchBlankQueryMakesNoRequest(t *testing.T) {
	e, api := tvmazeEnv(t)
	hits, err := e.svc.TVmaze.SearchShows(bg(), "   ")
	if err != nil || len(hits) != 0 {
		t.Fatalf("hits=%v err=%v", hits, err)
	}
	if api.Total() != 0 {
		t.Errorf("%d requests for a blank query", api.Total())
	}
}

func TestTVmazeShowMapsSeasonsEpisodesCast(t *testing.T) {
	e, api := tvmazeEnv(t)
	show, err := e.svc.TVmaze.Show(bg(), "99001")
	if err != nil {
		t.Fatal(err)
	}

	// The three embeds are asked for in one call.
	embeds := api.Last("/shows/99001").RawQuery
	for _, want := range []string{"embed[]=seasons", "embed[]=episodes", "embed[]=cast"} {
		if !strings.Contains(embeds, want) {
			t.Errorf("query %q lacks %q", embeds, want)
		}
	}

	if show.TVmazeID != 99001 || show.Name != "Pioneer One" || show.Year != 2010 || show.Premiered != "2010-06-19" ||
		show.IMDbID != "tt9900001" || show.TheTVDBID != 99991 || show.Status != "ended" ||
		show.Network != "VODO" || show.Language != "English" || show.RuntimeMin != 47 {
		t.Errorf("show = %+v", show)
	}
	if !slices.Equal(show.Genres, []string{"Drama", "Science-Fiction"}) {
		t.Errorf("genres = %v", show.Genres)
	}
	if show.Poster.Ref != "tvmaze/uploads/images/original_untouched/99/99001.jpg" {
		t.Errorf("poster = %+v", show.Poster)
	}
	if strings.ContainsAny(show.Overview, "<>") || !strings.HasPrefix(show.Overview, "When a mysterious object") {
		t.Errorf("overview not plain text: %q", show.Overview)
	}

	type ep struct {
		season, number int
		name, air      string
		runtime        int
		special        bool
	}
	var got []ep
	for _, x := range show.Episodes {
		got = append(got, ep{x.Season, x.Number, x.Name, x.AirDate, x.RuntimeMin, x.Special})
	}
	want := []ep{
		{0, 1, "Behind the Scenes", "2010-07-10", 12, true}, // a special with no number becomes season 0
		{1, 1, "Earthfall", "2010-06-19", 45, false},
		{1, 2, "Anomalies", "2010-07-03", 0, false},
		{2, 1, "Landing Zone", "2011-01-30", 47, false}, // the duplicate listing is dropped
	}
	if !slices.Equal(got, want) {
		t.Errorf("episodes =\n%v\nwant\n%v", got, want)
	}
	if show.Episodes[1].Overview != "An object falls to Earth." {
		t.Errorf("episode overview = %q", show.Episodes[1].Overview)
	}

	type sn struct {
		number, count int
		name, start   string
	}
	var seasons []sn
	for _, s := range show.Seasons {
		seasons = append(seasons, sn{s.Number, s.EpisodeCount, s.Name, s.PremiereDate})
	}
	wantSeasons := []sn{
		{0, 1, "", ""}, // derived: TVmaze does not list season 0
		{1, 2, "", "2010-06-19"},
		{2, 1, "Second Wave", "2011-01-30"},
	}
	if !slices.Equal(seasons, wantSeasons) {
		t.Errorf("seasons = %v, want %v", seasons, wantSeasons)
	}
	if show.Seasons[1].Poster.Ref != "tvmaze/uploads/images/original_untouched/99/s1.jpg" {
		t.Errorf("season poster = %+v", show.Seasons[1].Poster)
	}

	if len(show.People) != 2 {
		t.Fatalf("people = %+v", show.People)
	}
	if p := show.People[0]; p.Name != "Joanna Gaskell" || p.Role != "actor" || p.Job != "Agent Taylor" ||
		p.Image.Ref != "tvmaze/uploads/images/original_untouched/1/p1.jpg" {
		t.Errorf("first cast = %+v", p)
	}
	if !show.People[1].Image.IsZero() {
		t.Errorf("a cast member with no photo has image %+v", show.People[1].Image)
	}
}

func TestTVmazeShowErrors(t *testing.T) {
	e, api := tvmazeEnv(t)
	api.Set("/shows/5000", httpStatus(500))

	for _, tc := range []struct {
		name     string
		id       string
		want     error
		requests int
	}{
		{"unknown show", "123456", ErrNotFound, 1},
		{"not a number", "abc", ErrNotFound, 0},
		{"negative", "-4", ErrNotFound, 0},
		{"server error", "5000", ErrUpstream, 1},
	} {
		before := api.Total()
		_, err := e.svc.TVmaze.Show(bg(), tc.id)
		if !errors.Is(err, tc.want) {
			t.Errorf("%s: err = %v, want %v", tc.name, err, tc.want)
		}
		if got := api.Total() - before; got != tc.requests {
			t.Errorf("%s: %d requests, want %d", tc.name, got, tc.requests)
		}
	}
}

func TestTVmazeLookupByIMDbFollowsRedirect(t *testing.T) {
	e, api := tvmazeEnv(t)
	api.Set("/lookup/shows", func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Query().Get("imdb") != "tt9900001" {
			http.NotFound(w, r)
			return
		}
		http.Redirect(w, r, "/shows/99001", http.StatusMovedPermanently)
	})

	hit, err := e.svc.TVmaze.ShowByIMDb(bg(), " TT9900001 ")
	if err != nil {
		t.Fatal(err)
	}
	if hit.ProviderID != "99001" || hit.Name != "Pioneer One" || hit.IMDbID != "tt9900001" || hit.Provider != ProviderTVmaze {
		t.Errorf("hit = %+v", hit)
	}

	before := api.Total()
	for _, bad := range []string{"", "9900001", "tt12", "tt9900001; drop"} {
		if _, err := e.svc.TVmaze.ShowByIMDb(bg(), bad); !errors.Is(err, ErrNotFound) {
			t.Errorf("ShowByIMDb(%q) = %v, want not found", bad, err)
		}
	}
	if api.Total() != before {
		t.Error("a malformed IMDb id reached the network")
	}
}

func TestTVmazeRateLimitBucket(t *testing.T) {
	e, api := tvmazeEnv(t)
	// Distinct queries so the cache cannot answer. The bucket allows a burst
	// of 5, then refills at 2 per second: the sixth call waits half a second.
	for i := 0; i < 8; i++ {
		if _, err := e.svc.TVmaze.SearchShows(bg(), "query "+string(rune('a'+i))); err != nil {
			t.Fatal(err)
		}
	}
	if api.Count("/search/shows") != 8 {
		t.Fatalf("calls = %d", api.Count("/search/shows"))
	}
	sleeps := e.clock.Sleeps()
	if len(sleeps) != 3 {
		t.Fatalf("sleeps = %v, want 3 waits (calls 6, 7 and 8)", sleeps)
	}
	for _, d := range sleeps {
		if d < 400*time.Millisecond || d > 600*time.Millisecond {
			t.Errorf("wait = %v, want about 500ms", d)
		}
	}
}
