package parse

import "testing"

func TestParseMusicRelease(t *testing.T) {
	cases := []struct {
		name string
		want MusicRelease
	}{
		// ---- the usual spelling ----
		{"Frédéric Chopin - Nocturnes (1998) [FLAC]",
			MusicRelease{Artist: "Frédéric Chopin", Album: "Nocturnes", Year: 1998, Format: "FLAC"}},
		{"Artist - Album (2012) [FLAC 24bit]",
			MusicRelease{Artist: "Artist", Album: "Album", Year: 2012, Format: "FLAC", Bitrate: "24bit"}},
		{"Artist - Album (2012) [FLAC 24-96]",
			MusicRelease{Artist: "Artist", Album: "Album", Year: 2012, Format: "FLAC", Bitrate: "24bit"}},
		{"Artist - Album (2012) FLAC",
			MusicRelease{Artist: "Artist", Album: "Album", Year: 2012, Format: "FLAC"}},
		{"Artist - Album 2012 FLAC",
			MusicRelease{Artist: "Artist", Album: "Album", Year: 2012, Format: "FLAC"}},
		{"Artist - Album [FLAC]",
			MusicRelease{Artist: "Artist", Album: "Album", Format: "FLAC"}},
		{"Artist - Album",
			MusicRelease{Artist: "Artist", Album: "Album"}},
		{"Artist - Album (1994) [MP3 320]",
			MusicRelease{Artist: "Artist", Album: "Album", Year: 1994, Format: "MP3", Bitrate: "320kbps"}},
		{"Artist - Album (1994) [MP3 V0]",
			MusicRelease{Artist: "Artist", Album: "Album", Year: 1994, Format: "MP3", Bitrate: "V0"}},
		{"Artist - Album (1994) [MP3 V2 VBR]",
			MusicRelease{Artist: "Artist", Album: "Album", Year: 1994, Format: "MP3", Bitrate: "V2"}},
		{"Artist - Album (2001) [AAC 256kbps]",
			MusicRelease{Artist: "Artist", Album: "Album", Year: 2001, Format: "AAC", Bitrate: "256kbps"}},
		{"Artist - Album (2001) [ALAC]",
			MusicRelease{Artist: "Artist", Album: "Album", Year: 2001, Format: "ALAC"}},
		{"Artist - Album (2019) [OPUS]",
			MusicRelease{Artist: "Artist", Album: "Album", Year: 2019, Format: "Opus"}},
		// ---- bracket styles ----
		{"Artist - Album [2012, MP3, 320 kbps]",
			MusicRelease{Artist: "Artist", Album: "Album", Year: 2012, Format: "MP3", Bitrate: "320kbps"}},
		{"Artist - Album (2012) [WEB FLAC]",
			MusicRelease{Artist: "Artist", Album: "Album", Year: 2012, Format: "FLAC", Source: "WEB"}},
		{"Artist - Album (2012) [CD] [FLAC]",
			MusicRelease{Artist: "Artist", Album: "Album", Year: 2012, Format: "FLAC", Source: "CD"}},
		{"Artist - Album (2012) {FLAC}",
			MusicRelease{Artist: "Artist", Album: "Album", Year: 2012, Format: "FLAC"}},
		{"Artist - Album (Vinyl) (1977) [FLAC 24bit 96kHz]",
			MusicRelease{Artist: "Artist", Album: "Album", Year: 1977, Format: "FLAC", Bitrate: "24bit", Source: "Vinyl"}},
		{"Artist - (2012) Album [FLAC]",
			MusicRelease{Artist: "Artist", Album: "Album", Year: 2012, Format: "FLAC"}},
		// ---- edition notes stay with the album ----
		{"Artist - Album (Deluxe Edition) (2012) [FLAC]",
			MusicRelease{Artist: "Artist", Album: "Album (Deluxe Edition)", Year: 2012, Format: "FLAC"}},
		{"Artist - Album [Remastered] 2011 FLAC",
			MusicRelease{Artist: "Artist", Album: "Album [Remastered]", Year: 2011, Format: "FLAC"}},
		// ---- dashes and years in other places ----
		{"Artist - 2012 - Album [FLAC]",
			MusicRelease{Artist: "Artist", Album: "Album", Year: 2012, Format: "FLAC"}},
		{"Artist - Album - 2012 - FLAC",
			MusicRelease{Artist: "Artist", Album: "Album", Year: 2012, Format: "FLAC"}},
		{"Artist - Album - FLAC",
			MusicRelease{Artist: "Artist", Album: "Album", Format: "FLAC"}},
		{"Artist - Album – 2012 – MP3 – 320",
			MusicRelease{Artist: "Artist", Album: "Album", Year: 2012, Format: "MP3", Bitrate: "320kbps"}},
		{"Artist_-_Album_(2012)_[MP3_V0]",
			MusicRelease{Artist: "Artist", Album: "Album", Year: 2012, Format: "MP3", Bitrate: "V0"}},
		// ---- names that are numbers ----
		{"Taylor Swift - 1989 (2014) [FLAC]",
			MusicRelease{Artist: "Taylor Swift", Album: "1989", Year: 2014, Format: "FLAC"}},
		{"Taylor Swift - 1989 FLAC",
			MusicRelease{Artist: "Taylor Swift", Album: "1989", Format: "FLAC"}},
		{"Rush - 2112 [FLAC]",
			MusicRelease{Artist: "Rush", Album: "2112", Format: "FLAC"}},
		{"Artist - Album 24 [FLAC]",
			MusicRelease{Artist: "Artist", Album: "Album 24", Format: "FLAC"}},
		// ---- the scene way ----
		{"Daft_Punk-Random_Access_Memories-WEB-2013-ENTiTLED",
			MusicRelease{Artist: "Daft Punk", Album: "Random Access Memories", Year: 2013, Source: "WEB", Group: "ENTiTLED"}},
		{"Pink_Floyd-The_Wall-2CD-FLAC-1994-GRP",
			MusicRelease{Artist: "Pink Floyd", Album: "The Wall", Year: 1994, Format: "FLAC", Source: "CD", Group: "GRP"}},
		{"Artist-Album-CD-FLAC-2012-FNT",
			MusicRelease{Artist: "Artist", Album: "Album", Year: 2012, Format: "FLAC", Source: "CD", Group: "FNT"}},
		{"VA-Cafe_del_Mar-(CM001)-WEB-2013-PMEDIA",
			MusicRelease{Artist: "VA", Album: "Cafe del Mar", Year: 2013, Source: "WEB", Group: "PMEDIA"}},
		{"Artist-Album-24BIT-WEB-FLAC-2019-GRP",
			MusicRelease{Artist: "Artist", Album: "Album", Year: 2019, Format: "FLAC", Bitrate: "24bit", Source: "WEB", Group: "GRP"}},
		{"Artist-Album_(Deluxe_Edition)-CD-FLAC-2012-GRP",
			MusicRelease{Artist: "Artist", Album: "Album (Deluxe Edition)", Year: 2012, Format: "FLAC", Source: "CD", Group: "GRP"}},
		{"Artist-Album-2012-FLAC",
			MusicRelease{Artist: "Artist", Album: "Album", Year: 2012, Format: "FLAC"}},
		{"Artist-Album-WEB-2019-320-GRP",
			MusicRelease{Artist: "Artist", Album: "Album", Year: 2019, Bitrate: "320kbps", Source: "WEB", Group: "GRP"}},
		// ---- packs ----
		{"Artist - Discography (1990-2015) [FLAC]",
			MusicRelease{Artist: "Artist", Album: "Discography (1990-2015)", Format: "FLAC", Discography: true}},
		{"Artist - Complete Studio Albums [MP3 320]",
			MusicRelease{Artist: "Artist", Album: "Complete Studio Albums", Format: "MP3", Bitrate: "320kbps", Discography: true}},
		// ---- single files and wrappers ----
		{"Artist - Album (2012).nzb",
			MusicRelease{Artist: "Artist", Album: "Album", Year: 2012}},
		{"Artist - Album (2012) [FLAC].torrent",
			MusicRelease{Artist: "Artist", Album: "Album", Year: 2012, Format: "FLAC"}},
		{"Artist - Album.flac",
			MusicRelease{Artist: "Artist", Album: "Album", Format: "FLAC"}},
		{"Downloads/Artist - Album (2012) [FLAC]",
			MusicRelease{Artist: "Artist", Album: "Album", Year: 2012, Format: "FLAC"}},
		// ---- nothing useful ----
		{"", MusicRelease{}},
		{"Album Only (2012)", MusicRelease{Album: "Album Only", Year: 2012}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := ParseMusicRelease(tc.name)
			if got != tc.want {
				t.Errorf("ParseMusicRelease(%q)\n got %+v\nwant %+v", tc.name, got, tc.want)
			}
		})
	}
	if len(cases) < 30 {
		t.Errorf("only %d cases", len(cases))
	}
}

func TestAlbumNameMatches(t *testing.T) {
	cases := []struct {
		catalog, found string
		want           bool
	}{
		{"Nocturnes", "Nocturnes", true},
		{"Nocturnes", "nocturnes", true},
		{"Études", "Etudes", true},
		{"Abbey Road", "Abbey Road (Remastered)", true},
		{"Abbey Road", "Abbey Road Remastered 2019", true},
		{"Abbey Road (Remastered)", "Abbey Road", true},
		{"The Wall", "Wall", true}, // "the" is dropped
		{"Nocturnes", "Nocturnes Vol 2", false},
		{"Nocturnes", "Preludes", false},
		{"Greatest Hits", "Greatest Hits II", false},
		{"", "Nocturnes", false},
		{"Nocturnes", "", false},
		{"Rumours", "Rumours Deluxe Edition", true},
		{"Rumours", "Rumours Live", false},
	}
	for _, tc := range cases {
		if got := AlbumNameMatches(tc.catalog, tc.found); got != tc.want {
			t.Errorf("AlbumNameMatches(%q, %q) = %v, want %v", tc.catalog, tc.found, got, tc.want)
		}
	}
}

func TestTitleSimilarity(t *testing.T) {
	cases := []struct {
		a, b   string
		lo, hi float64
	}{
		{"Nocturne in E-flat major, Op. 9 No. 2", "Nocturne in E-flat major, Op. 9 No. 2", 1, 1},
		{"Nocturne in E-flat major, Op. 9 No. 2", "nocturne in e flat major op 9 no 2", 1, 1},
		{"Nocturne in B-flat minor", "Nocturne in B-flat minor, Op. 9 No. 1", 0.9, 1},
		{"Song (feat. Someone)", "Song", 1, 1},
		{"Song One", "Totally Different", 0, 0.01},
		{"Song One", "Song Two", 0.3, 0.4},
		{"", "Song", 0, 0},
	}
	for _, tc := range cases {
		got := TitleSimilarity(tc.a, tc.b)
		if got < tc.lo || got > tc.hi {
			t.Errorf("TitleSimilarity(%q, %q) = %.2f, want %.2f to %.2f", tc.a, tc.b, got, tc.lo, tc.hi)
		}
	}
}

func TestMatchTrack(t *testing.T) {
	tracks := []CatalogTrack{
		{ID: 1, Disc: 1, Position: 1, Name: "Nocturne in B-flat minor, Op. 9 No. 1"},
		{ID: 2, Disc: 1, Position: 2, Name: "Nocturne in E-flat major, Op. 9 No. 2"},
		{ID: 3, Disc: 1, Position: 3, Name: "Nocturne in B major, Op. 9 No. 3"},
		{ID: 11, Disc: 2, Position: 1, Name: "Waltz in D-flat major, Op. 64 No. 1"},
		{ID: 12, Disc: 2, Position: 2, Name: "Waltz in C-sharp minor, Op. 64 No. 2"},
	}
	cases := []struct {
		label  string
		disc   int
		number int
		title  string
		wantID int64
		wantM  TrackMatch
	}{
		{"number and title agree", 0, 2, "Nocturne in E-flat major, Op. 9 No. 2", 2, SureTrack},
		{"number agrees, title a little different", 0, 2, "Nocturne in E-flat major", 2, SureTrack},
		{"number only", 0, 3, "", 3, SureTrack},
		{"disc 2 by disc number", 2, 2, "", 12, SureTrack},
		{"disc 2 by title when the disc is not said", 0, 1, "Waltz in D-flat major, Op. 64 No. 1", 11, SureTrack},
		{"title wins over a shifted number", 0, 1, "Nocturne in B major, Op. 9 No. 3", 3, SureTrack},
		{"title only", 0, 0, "Waltz in C-sharp minor, Op. 64 No. 2", 12, SureTrack},
		{"number fits, title does not", 0, 2, "A Bonus Track Nobody Listed", 2, WeakTrack},
		{"number past the end", 0, 9, "", 0, NoTrack},
		{"nothing to go on", 0, 0, "", 0, NoTrack},
		{"unknown title, no number", 0, 0, "Mystery", 0, NoTrack},
		{"several discs, no disc, no title", 0, 1, "", 0, NoTrack},
	}
	for _, tc := range cases {
		got, m := MatchTrack(tracks, tc.disc, tc.number, tc.title)
		if m != tc.wantM || got.ID != tc.wantID {
			t.Errorf("%s: MatchTrack = track %d (%v), want track %d (%v)", tc.label, got.ID, m, tc.wantID, tc.wantM)
		}
	}
	if _, m := MatchTrack(nil, 1, 1, "x"); m != NoTrack {
		t.Error("an album with no tracks matched")
	}
}
