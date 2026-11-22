package sources

import (
	"testing"

	"github.com/BaileyPillon/marquee/internal/sources/sourcestest"
)

func TestBuildSearchMusicStructured(t *testing.T) {
	c := capsFrom(t, sourcestest.Opts{}) // its music search lists q, artist and album
	mode, p, used := BuildSearch(c, Query{Media: MediaArtist, Text: "Frédéric Chopin Nocturnes", Artist: "Frédéric Chopin", Album: "Nocturnes"}, []int{3000})
	if mode != ModeMusic || get(p, "t") != "music" || get(p, "artist") != "Frédéric Chopin" || get(p, "album") != "Nocturnes" ||
		p.Has("q") || get(p, "cat") != "3000" || !used {
		t.Errorf("structured music search: mode %s used %v params %v", mode, used, p)
	}

	// The retry after an empty answer asks with text.
	_, p, used = BuildSearch(c, Query{Media: MediaArtist, Text: "Frédéric Chopin Nocturnes", Artist: "Frédéric Chopin", Album: "Nocturnes", NoIDs: true}, []int{3000})
	if used || p.Has("artist") || p.Has("album") || get(p, "q") != "Frédéric Chopin Nocturnes" {
		t.Errorf("text retry: used %v params %v", used, p)
	}

	// The artist alone, with no album named.
	_, p, _ = BuildSearch(c, Query{Media: MediaArtist, Text: "Frédéric Chopin", Artist: "Frédéric Chopin"}, nil)
	if get(p, "artist") != "Frédéric Chopin" || p.Has("album") || p.Has("q") {
		t.Errorf("artist only: %v", p)
	}
}

func TestBuildSearchMusicNeedsSupportedParams(t *testing.T) {
	// A source that only takes q in its music search gets the text.
	c := capsFrom(t, sourcestest.Opts{})
	c.Modes[ModeMusic] = Mode{Available: true, Params: []string{"q"}}
	mode, p, used := BuildSearch(c, Query{Media: MediaArtist, Text: "Chopin Nocturnes", Artist: "Chopin", Album: "Nocturnes"}, []int{3000})
	if mode != ModeMusic || used || p.Has("artist") || p.Has("album") || get(p, "q") != "Chopin Nocturnes" {
		t.Errorf("q-only music search: mode %s used %v params %v", mode, used, p)
	}

	// A source with the artist parameter but not the album one: the album is left out.
	c.Modes[ModeMusic] = Mode{Available: true, Params: []string{"q", "artist"}}
	_, p, _ = BuildSearch(c, Query{Media: MediaArtist, Text: "Chopin Nocturnes", Artist: "Chopin", Album: "Nocturnes"}, []int{3000})
	if get(p, "artist") != "Chopin" || p.Has("album") || p.Has("q") {
		t.Errorf("artist-only music search: %v", p)
	}

	// A source with no music search at all: plain search with the text.
	c.Modes[ModeMusic] = Mode{}
	mode, p, used = BuildSearch(c, Query{Media: MediaArtist, Text: "Chopin Nocturnes", Artist: "Chopin", Album: "Nocturnes"}, []int{3000})
	if mode != ModeSearch || used || get(p, "q") != "Chopin Nocturnes" {
		t.Errorf("no music search: mode %s used %v params %v", mode, used, p)
	}
}
