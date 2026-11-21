package importer

import (
	"strings"
	"testing"
)

func TestSafeName(t *testing.T) {
	cases := map[string]string{
		"Star Trek: Voyager":                "Star Trek Voyager",
		`AC/DC`:                             "ACDC",
		`What If...?`:                       "What If",
		"Who?":                              "Who",
		`He said "Hi" <loudly> | * ` + "\\": "He said Hi loudly",
		"  Spaced   out  ":                  "Spaced out",
		"Ends with dot. ":                   "Ends with dot",
		"...":                               "_",
		"":                                  "_",
		`<>:"/\|?*`:                         "_",
		"CON":                               "CON_",
		"nul":                               "nul_",
		"Aux.Movie":                         "Aux.Movie_",
		"Console":                           "Console",
		"Tab\tand\nnewline":                 "Tab and newline",
		"Zero\x00width\x07":                 "Zerowidth",
		"Amélie":                            "Amélie",
	}
	for in, want := range cases {
		if got := SafeName(in); got != want {
			t.Errorf("SafeName(%q) = %q, want %q", in, got, want)
		}
	}
	long := SafeName(strings.Repeat("a", 400))
	if len([]rune(long)) != maxPart {
		t.Errorf("a long name is cut to %d characters, got %d", maxPart, len([]rune(long)))
	}
}

func TestMoviePath(t *testing.T) {
	got := MoviePath(MovieName{Name: "Night of the Living Dead", Year: 1968, Quality: "Bluray-1080p", Ext: ".mkv"})
	want := "Night of the Living Dead (1968)/Night of the Living Dead (1968) Bluray-1080p.mkv"
	if got != want {
		t.Errorf("got  %q\nwant %q", got, want)
	}
	// Windows-unsafe characters in the name, no year, no known quality, no dot in the extension.
	got = MoviePath(MovieName{Name: "Who Framed Roger Rabbit?: The <Cut>", Quality: "Unknown", Ext: "MKV"})
	want = "Who Framed Roger Rabbit The Cut/Who Framed Roger Rabbit The Cut.mkv"
	if got != want {
		t.Errorf("got  %q\nwant %q", got, want)
	}
	if got := MoviePath(MovieName{Name: "Up", Year: 2009, Quality: "WEBDL-2160p", Ext: ".mp4"}); got != "Up (2009)/Up (2009) WEBDL-2160p.mp4" {
		t.Errorf("got %q", got)
	}
}

func TestEpisodePath(t *testing.T) {
	cases := []struct {
		name EpisodeName
		want string
	}{
		{
			EpisodeName{Show: "Pioneer One", Year: 2010, Season: 1, Episodes: []int{1}, EpisodeNames: []string{"Earthfall"}, Quality: "Bluray-1080p", Ext: ".mkv"},
			"Pioneer One (2010)/Season 01/Pioneer One - S01E01 - Earthfall Bluray-1080p.mkv",
		},
		{ // a double episode
			EpisodeName{Show: "Pioneer One", Year: 2010, Season: 1, Episodes: []int{1, 2}, EpisodeNames: []string{"Earthfall", "Anomalies"}, Quality: "WEBDL-720p", Ext: ".mkv"},
			"Pioneer One (2010)/Season 01/Pioneer One - S01E01-E02 - Earthfall + Anomalies WEBDL-720p.mkv",
		},
		{ // three in a row
			EpisodeName{Show: "X", Season: 2, Episodes: []int{4, 5, 6}, EpisodeNames: []string{"A", "B", "C"}, Ext: ".mkv"},
			"X/Season 02/X - S02E04-E06 - A + B + C.mkv",
		},
		{ // not next to each other
			EpisodeName{Show: "X", Season: 2, Episodes: []int{4, 6}, Ext: ".mkv"},
			"X/Season 02/X - S02E04E06.mkv",
		},
		{ // specials are season 0
			EpisodeName{Show: "Pioneer One", Year: 2010, Season: 0, Episodes: []int{1}, EpisodeNames: []string{"Behind the Scenes"}, Quality: "HDTV-720p", Ext: ".mp4"},
			"Pioneer One (2010)/Season 00/Pioneer One - S00E01 - Behind the Scenes HDTV-720p.mp4",
		},
		{ // unsafe characters in the show and the episode name; no episode name
			EpisodeName{Show: "Marvel's Agents of S.H.I.E.L.D.", Year: 2013, Season: 3, Episodes: []int{10}, EpisodeNames: []string{"Maveth: Part 1?"}, Quality: "WEBDL-1080p", Ext: ".mkv"},
			"Marvel's Agents of S.H.I.E.L.D. (2013)/Season 03/Marvel's Agents of S.H.I.E.L.D. - S03E10 - Maveth Part 1 WEBDL-1080p.mkv",
		},
		{ // a show whose name ends in a dot, with no year: the folder cannot end in one, the file name can have it inside
			EpisodeName{Show: "S.H.I.E.L.D.", Season: 1, Episodes: []int{1}, Ext: ".mkv"},
			"S.H.I.E.L.D/Season 01/S.H.I.E.L.D. - S01E01.mkv",
		},
		{
			EpisodeName{Show: "Show", Season: 12, Episodes: []int{101}, Ext: ".mkv"},
			"Show/Season 12/Show - S12E101.mkv",
		},
	}
	for _, c := range cases {
		if got := EpisodePath(c.name); got != c.want {
			t.Errorf("got  %q\nwant %q", got, c.want)
		}
	}
}

func TestTrackPath(t *testing.T) {
	got := TrackPath(TrackName{Artist: "Frédéric Chopin", Album: "Nocturnes", Year: 1998, Track: 3, Title: "Nocturne in E-flat, Op. 9 No. 2", Ext: ".flac"})
	want := "Frédéric Chopin/Nocturnes (1998)/03 - Nocturne in E-flat, Op. 9 No. 2.flac"
	if got != want {
		t.Errorf("got  %q\nwant %q", got, want)
	}
	got = TrackPath(TrackName{Artist: "AC/DC", Album: "Back in Black", Year: 1980, Disc: 2, MultiDisc: true, Track: 7, Title: "What?", Ext: ".mp3"})
	want = "ACDC/Back in Black (1980)/2-07 - What.mp3"
	if got != want {
		t.Errorf("got  %q\nwant %q", got, want)
	}
	if got := TrackPath(TrackName{Artist: "A", Album: "B", Track: 1, Ext: ".flac"}); got != "A/B/01.flac" {
		t.Errorf("a track with no title: got %q", got)
	}
}
