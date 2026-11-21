package sources

import (
	"net/url"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/BaileyPillon/marquee/internal/sources/sourcestest"
)

func capsFrom(t *testing.T, o sourcestest.Opts) *Caps {
	t.Helper()
	c, err := ParseCaps(strings.NewReader(sourcestest.CapsXML(o)), time.Date(2026, 11, 20, 0, 0, 0, 0, time.UTC))
	if err != nil {
		t.Fatalf("ParseCaps: %v", err)
	}
	return c
}

func TestParseCaps(t *testing.T) {
	c := capsFrom(t, sourcestest.Opts{})
	if c.Title != "Fake Indexer" || c.LimitMax != 100 || c.LimitDefault != 50 {
		t.Errorf("caps header = %+v", c)
	}
	for _, mode := range []string{ModeSearch, ModeTV, ModeMovie, ModeMusic} {
		if !c.ModeAvailable(mode) {
			t.Errorf("mode %s should be available", mode)
		}
	}
	if !c.Supports(ModeMovie, "imdbid") || !c.Supports(ModeMovie, "tmdbid") || c.Supports(ModeMovie, "tvmazeid") {
		t.Errorf("movie params = %v", c.Modes[ModeMovie].Params)
	}
	if !c.Supports(ModeTV, "season") || !c.Supports(ModeTV, "ep") || !c.Supports(ModeTV, "tvmazeid") || c.Supports(ModeTV, "tvdbid") {
		t.Errorf("tv params = %v", c.Modes[ModeTV].Params)
	}
	want := []int{2000, 2040, 2045, 5000, 5040, 3000}
	var got []int
	for _, cat := range c.Categories {
		got = append(got, cat.ID)
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("categories = %v, want %v (sub-categories flattened)", got, want)
	}
}

func TestCapsWithModesOff(t *testing.T) {
	c := capsFrom(t, sourcestest.Opts{NoMovie: true, NoTV: true})
	if c.ModeAvailable(ModeMovie) || c.ModeAvailable(ModeTV) || c.Supports(ModeMovie, "imdbid") {
		t.Errorf("modes that say available=no must not count: %+v", c.Modes)
	}
	// A nil Caps (never fetched) only offers the plain search.
	var nilCaps *Caps
	if !nilCaps.ModeAvailable(ModeSearch) || nilCaps.ModeAvailable(ModeMovie) || nilCaps.Supports(ModeMovie, "q") {
		t.Error("nil caps should offer the plain search and nothing else")
	}
}

func TestCapsLimit(t *testing.T) {
	c := capsFrom(t, sourcestest.Opts{Max: 60})
	if c.Limit(0) != 60 || c.Limit(30) != 30 || c.Limit(500) != 60 {
		t.Errorf("limits: %d %d %d", c.Limit(0), c.Limit(30), c.Limit(500))
	}
	var nilCaps *Caps
	if nilCaps.Limit(0) != DefaultLimit {
		t.Errorf("nil caps limit = %d", nilCaps.Limit(0))
	}
}

func get(p url.Values, k string) string { return p.Get(k) }

func TestBuildSearchMovieUsesIDsWhenSupported(t *testing.T) {
	c := capsFrom(t, sourcestest.Opts{})
	mode, p, ids := BuildSearch(c, Query{Media: MediaMovie, Text: "Metropolis", Year: 1927, IMDb: "tt0017136", TMDb: 19}, []int{2000})
	if mode != ModeMovie || !ids {
		t.Fatalf("mode %s ids %v", mode, ids)
	}
	if get(p, "t") != "movie" || get(p, "imdbid") != "0017136" || get(p, "tmdbid") != "19" {
		t.Errorf("params = %v", p)
	}
	if p.Has("q") {
		t.Errorf("an id search must not also send q: %v", p)
	}
	if get(p, "cat") != "2000" || get(p, "limit") != "100" || get(p, "extended") != "1" {
		t.Errorf("params = %v", p)
	}
}

func TestBuildSearchMovieSkipsUnsupportedIDs(t *testing.T) {
	// This source takes imdbid only: tmdbid must not be sent.
	c := capsFrom(t, sourcestest.Opts{MovieIDs: "q,imdbid"})
	_, p, ids := BuildSearch(c, Query{Media: MediaMovie, Text: "Metropolis", IMDb: "tt0017136", TMDb: 19}, nil)
	if !ids || p.Has("tmdbid") || get(p, "imdbid") != "0017136" {
		t.Errorf("params = %v", p)
	}
}

func TestBuildSearchMovieFallsBackToText(t *testing.T) {
	t.Run("ids not listed", func(t *testing.T) {
		c := capsFrom(t, sourcestest.Opts{MovieIDs: "q"})
		mode, p, ids := BuildSearch(c, Query{Media: MediaMovie, Text: "Metropolis", Year: 1927, IMDb: "tt0017136", TMDb: 19}, nil)
		if mode != ModeMovie || ids || get(p, "q") != "Metropolis 1927" || p.Has("imdbid") || p.Has("tmdbid") {
			t.Errorf("mode %s ids %v params %v", mode, ids, p)
		}
	})
	t.Run("no movie mode at all", func(t *testing.T) {
		c := capsFrom(t, sourcestest.Opts{SearchOnly: true})
		mode, p, ids := BuildSearch(c, Query{Media: MediaMovie, Text: "Sintel", Year: 2010, IMDb: "tt1727587"}, nil)
		if mode != ModeSearch || ids || get(p, "t") != "search" || get(p, "q") != "Sintel 2010" {
			t.Errorf("mode %s ids %v params %v", mode, ids, p)
		}
	})
	t.Run("caps never fetched", func(t *testing.T) {
		mode, p, ids := BuildSearch(nil, Query{Media: MediaMovie, Text: "Sintel", IMDb: "tt1727587"}, nil)
		if mode != ModeSearch || ids || get(p, "q") != "Sintel" {
			t.Errorf("mode %s ids %v params %v", mode, ids, p)
		}
	})
	t.Run("NoIDs forces text", func(t *testing.T) {
		c := capsFrom(t, sourcestest.Opts{})
		_, p, ids := BuildSearch(c, Query{Media: MediaMovie, Text: "Metropolis", Year: 1927, IMDb: "tt0017136", NoIDs: true}, nil)
		if ids || get(p, "q") != "Metropolis 1927" || p.Has("imdbid") {
			t.Errorf("params = %v", p)
		}
	})
}

func TestBuildSearchShowNative(t *testing.T) {
	c := capsFrom(t, sourcestest.Opts{})
	mode, p, ids := BuildSearch(c, Query{Media: MediaShow, Text: "Pioneer One", IMDb: "tt9900001", TVMaze: 99001, TVDb: 99991, Season: 1, Episode: 4}, nil)
	if mode != ModeTV || !ids {
		t.Fatalf("mode %s ids %v", mode, ids)
	}
	if get(p, "season") != "1" || get(p, "ep") != "4" || get(p, "tvmazeid") != "99001" || get(p, "imdbid") != "9900001" {
		t.Errorf("params = %v", p)
	}
	if p.Has("tvdbid") {
		t.Errorf("tvdbid is not in this source's list: %v", p)
	}
	if p.Has("q") {
		t.Errorf("an id search must not send q: %v", p)
	}
}

func TestBuildSearchShowWithoutIDsKeepsSeasonParams(t *testing.T) {
	c := capsFrom(t, sourcestest.Opts{TVParams: "q,season,ep"})
	_, p, ids := BuildSearch(c, Query{Media: MediaShow, Text: "Pioneer One", TVMaze: 99001, Season: 1}, nil)
	if ids || get(p, "q") != "Pioneer One" || get(p, "season") != "1" || p.Has("ep") || p.Has("tvmazeid") {
		t.Errorf("params = %v", p)
	}
}

func TestBuildSearchShowWritesSeasonIntoTextWhenSourceCannotTakeIt(t *testing.T) {
	t.Run("tv-search lacks ep", func(t *testing.T) {
		c := capsFrom(t, sourcestest.Opts{TVParams: "q,season,tvmazeid"})
		mode, p, ids := BuildSearch(c, Query{Media: MediaShow, Text: "Pioneer One", TVMaze: 99001, Season: 1, Episode: 4}, nil)
		if mode != ModeTV || ids || get(p, "q") != "Pioneer One S01E04" || p.Has("season") || p.Has("tvmazeid") {
			t.Errorf("mode %s ids %v params %v", mode, ids, p)
		}
	})
	t.Run("only the plain search", func(t *testing.T) {
		c := capsFrom(t, sourcestest.Opts{SearchOnly: true})
		mode, p, _ := BuildSearch(c, Query{Media: MediaShow, Text: "Pioneer One", Season: 1}, nil)
		if mode != ModeSearch || get(p, "q") != "Pioneer One S01" {
			t.Errorf("mode %s params %v", mode, p)
		}
	})
}

func TestBuildSearchMusicAndLatest(t *testing.T) {
	c := capsFrom(t, sourcestest.Opts{})
	mode, p, _ := BuildSearch(c, Query{Media: MediaArtist, Text: "Frédéric Chopin"}, []int{3000})
	if mode != ModeMusic || get(p, "q") != "Frédéric Chopin" || get(p, "cat") != "3000" {
		t.Errorf("mode %s params %v", mode, p)
	}
	mode, p, ids := BuildSearch(c, Query{Limit: 40}, []int{2000, 5000})
	if mode != ModeSearch || ids || p.Has("q") || get(p, "cat") != "2000,5000" || get(p, "limit") != "40" {
		t.Errorf("latest feed: mode %s params %v", mode, p)
	}
	if p.Has("apikey") {
		t.Error("BuildSearch must not add the key; the client does")
	}
}

func TestCategoriesForKind(t *testing.T) {
	tests := []struct {
		name       string
		configured []int
		media      string
		want       []int
		ok         bool
	}{
		{"nothing set, movie", nil, MediaMovie, []int{2000}, true},
		{"nothing set, show", nil, MediaShow, []int{5000}, true},
		{"nothing set, music", nil, MediaArtist, []int{3000}, true},
		{"mixed, movie", []int{2000, 2040, 5000}, MediaMovie, []int{2000, 2040}, true},
		{"mixed, show", []int{2000, 2040, 5000}, MediaShow, []int{5000}, true},
		{"tv only source asked for a movie", []int{5000, 5040}, MediaMovie, nil, false},
		{"custom category goes everywhere", []int{100123}, MediaMovie, []int{100123}, true},
		{"latest feed sends exactly what was set", []int{2000, 5000}, "", []int{2000, 5000}, true},
		{"latest feed with nothing set sends none", nil, "", nil, true},
	}
	for _, tt := range tests {
		got, ok := categoriesFor(tt.configured, tt.media)
		if ok != tt.ok || !reflect.DeepEqual(got, tt.want) {
			t.Errorf("%s: got %v %v, want %v %v", tt.name, got, ok, tt.want, tt.ok)
		}
	}
}
