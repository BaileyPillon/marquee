package library

import (
	"context"
	"slices"
	"testing"
)

func movieTitles() *fakeTitles {
	ts := newFakeTitles()
	ts.cands[CandidateMovie] = []Candidate{
		{ID: 1, Kind: "movie", Name: "Metropolis", Year: 1927},
		{ID: 2, Kind: "movie", Name: "Nosferatu", Year: 1922, AltNames: []string{"Nosferatu, eine Symphonie des Grauens"}},
		{ID: 3, Kind: "movie", Name: "Nosferatu", Year: 2024},
		{ID: 4, Kind: "movie", Name: "Sherlock Jr.", Year: 1924},
		{ID: 5, Kind: "movie", Name: "Das Cabinet des Dr. Caligari", Year: 1920, AltNames: []string{"The Cabinet of Dr. Caligari"}},
		{ID: 6, Kind: "movie", Name: "Sintel"}, // year not known
		{ID: 7, Kind: "movie", Name: "Remake", Year: 2019},
		{ID: 8, Kind: "movie", Name: "Remake", Year: 2020},
		{ID: 9, Kind: "movie", Name: "Tears of Steel", Year: 2012},
		{ID: 10, Kind: "movie", Name: "Fröhliche Weihnachten", Year: 2019},
		{ID: 11, Kind: "movie", Name: "2001: A Space Odyssey", Year: 1968},
		{ID: 12, Kind: "movie", Name: "Night of the Living Dead", Year: 1968},
		{ID: 13, Kind: "movie", Name: "Blade Runner 2049", Year: 2017},
		{ID: 14, Kind: "movie", Name: "Blade Runner", Year: 1982},
	}
	return ts
}

func TestMovieMatchRule(t *testing.T) {
	m := newMatcher(context.Background(), movieTitles(), quietLog())
	cases := []struct {
		rel  string
		want int64 // 0 = unmatched
	}{
		{"Metropolis (1927)/Metropolis (1927).mkv", 1},
		{"Metropolis.1927.1080p.BluRay.x264-GRP.mkv", 1},
		{"Metropolis (1928).mkv", 1},     // a year off is fine
		{"Metropolis (1926).mkv", 1},     // either way
		{"Metropolis (1930).mkv", 0},     // three years off is not
		{"Metropolis.mkv", 1},            // no year, one candidate with the name
		{"metropolis 1927.mkv", 1},       // case
		{"The Metropolis (1927).mkv", 1}, // leading article
		{"Nosferatu.mkv", 0},             // no year, two candidates: ambiguous
		{"Nosferatu (1922).mkv", 2},      // the year tells them apart
		{"Nosferatu (2024).mkv", 3},
		{"Nosferatu.1922.720p.BluRay.x264-GRP.mkv", 2},
		{"Nosferatu (1999).mkv", 0},                             // neither
		{"Nosferatu, eine Symphonie des Grauens (1922).mkv", 2}, // an alternate name
		{"Sherlock, Jr. (1924).mkv", 4},                         // punctuation
		{"Sherlock.Jr.1924.720p.WEB-DL.AAC2.0.H.264-GRP.mp4", 4},
		{"The.Cabinet.of.Dr.Caligari.1920.720p.mkv", 5}, // matches the alt name
		{"Das Cabinet des Dr Caligari (1920).mkv", 5},
		{"Sintel (2010).mkv", 6}, // a candidate without a year fits any year
		{"Sintel.mkv", 6},
		{"Remake (2019).mkv", 7}, // closer year wins
		{"Remake (2020).mkv", 8},
		{"Remake (2021).mkv", 8}, // 2019 is two years off, 2020 one
		{"Remake (2018).mkv", 7},
		{"Remake.mkv", 0}, // no year, two candidates
		{"Remake (2025).mkv", 0},
		{"Tears of Steel (2012)/Tears.of.Steel.2012.1080p.mkv", 9},
		{"Frohliche Weihnachten (2019).mkv", 10}, // accents folded
		{"Fröhliche Weihnachten.mkv", 10},
		{"2001 A Space Odyssey (1968).mkv", 11}, // number in the title
		{"2001.A.Space.Odyssey.1968.1080p.BluRay.x264-GRP.mkv", 11},
		{"Night of the Living Dead (1968)/Night.of.the.Living.Dead.1968.mov", 12},
		{"Unknown Film (2001).mkv", 0},
		// A title that ends in a year-like number.
		{"Blade.Runner.2049.1080p.BluRay.x264-GRP.mkv", 13},
		{"Blade Runner 2049 (2017).mkv", 13},
		{"Blade.Runner.2049.2017.1080p.BluRay.x264-GRP.mkv", 13},
		{"Blade Runner (1982)/Blade.Runner.1982.720p.mkv", 14},
		// The file name says nothing: the folder does.
		{"Metropolis (1927)/video.mkv", 1},
		{"Nosferatu (1922)/movie.mkv", 2},
		{"Nosferatu (2024)/movie.mkv", 3},
		{"Metropolis (1927)/1080p.mkv", 1},
		// A file that names one thing in a folder that names another: the file wins.
		{"Metropolis (1927)/Sintel (2010).mkv", 6},
		// Years can come from either place.
		{"Nosferatu (1922)/Nosferatu.mkv", 2},
		{"Nosferatu/Nosferatu (2024).mkv", 3},
		// Nothing usable at all.
		{"1080p.mkv", 0},
		{"01.mkv", 0},
		{"Extras Collection/clip.mkv", 0},
	}
	for _, c := range cases {
		t.Run(c.rel, func(t *testing.T) {
			w := m.classify(KindMovie, c.rel)
			var got int64
			if w.TitleID != nil {
				got = *w.TitleID
			}
			if got != c.want {
				t.Errorf("%s matched title %d, want %d", c.rel, got, c.want)
			}
			if (got != 0) != (w.State == StateMatched) {
				t.Errorf("state %q for title %d", w.State, got)
			}
			if w.EpisodeIDs != nil || w.TrackID != nil {
				t.Errorf("a movie must not carry episodes or a track: %+v", w)
			}
		})
	}
}

func showTitles() *fakeTitles {
	ts := newFakeTitles()
	ts.cands[CandidateShow] = []Candidate{
		{ID: 20, Kind: "show", Name: "Pioneer One", Year: 2010},
		{ID: 21, Kind: "show", Name: "Undersea Kingdom", Year: 1936},
		{ID: 22, Kind: "show", Name: "The Daily Show", Year: 1996, AltNames: []string{"Daily Show"}},
		{ID: 23, Kind: "show", Name: "Remake", Year: 2011},
		{ID: 24, Kind: "show", Name: "Remake", Year: 2019},
		{ID: 25, Kind: "show", Name: "Agents of S.H.I.E.L.D.", Year: 2013},
	}
	for ep := 1; ep <= 6; ep++ {
		ts.eps[epKey{20, 1, ep}] = int64(200 + ep)
	}
	ts.eps[epKey{20, 0, 1}] = 200
	ts.eps[epKey{21, 1, 1}] = 211
	ts.eps[epKey{25, 2, 1}] = 251
	ts.eps[epKey{23, 1, 1}] = 231
	ts.eps[epKey{24, 1, 1}] = 241
	return ts
}

func TestShowMatchRule(t *testing.T) {
	ts := showTitles()
	m := newMatcher(context.Background(), ts, quietLog())
	cases := []struct {
		rel      string
		wantID   int64
		wantEps  []int64
		wantName string
	}{
		{"Pioneer One (2010)/Season 01/Pioneer One (2010) S01E01.mkv", 20, []int64{201}, "series folder and file"},
		{"Pioneer One (2010)/Season 01/Pioneer One - S01E02 - Earthfall.mkv", 20, []int64{202}, ""},
		{"Pioneer One (2010)/Season 01/Pioneer.One.S01E03E04.720p.HDTV.x264-GRP.mkv", 20, []int64{203, 204}, "multi-episode file"},
		{"Pioneer One (2010)/Season 1/Pioneer One - 1x05.avi", 20, []int64{205}, "1x05 style"},
		{"Pioneer One (2010)/Specials/Pioneer One - S00E01 - Recap.mkv", 20, []int64{200}, "specials"},
		{"Pioneer.One.S01E06.720p.mkv", 20, []int64{206}, "loose file: the parsed title"},
		{"Pioneer One/Season 1/S01E01.mkv", 20, []int64{201}, "no title in the file, no year in the folder"},
		{"Pioneer One (2010)/Season 1/S01E02.mkv", 20, []int64{202}, "folder gives the series"},
		{"Shows/Pioneer.One.S01E01.mkv", 20, []int64{201}, "a folder that is not a series is skipped"},
		{"Pioneer One (2013)/Season 1/Pioneer One - S01E01.mkv", 20, []int64{201}, "year off, but only one show has the name"},
		{"Undersea Kingdom (1936)/Season 01/Undersea Kingdom - S01E01 - Chapter 1.mp4", 21, []int64{211}, ""},
		{"Remake (2011)/Season 1/Remake - S01E01.mkv", 23, []int64{231}, "the year tells two shows apart"},
		{"Remake (2019)/Season 1/Remake - S01E01.mkv", 24, []int64{241}, ""},
		{"Remake/Season 1/Remake - S01E01.mkv", 0, nil, "no year, two shows with the name"},
		{"Agents of SHIELD/Season 2/Agents.of.S.H.I.E.L.D.S02E01.mkv", 25, []int64{251}, "dotted acronym"},
		{"The Daily Show/The.Daily.Show.2019.03.14.mkv", 22, nil, "daily show: the series matches, there is no episode to map"},
		{"Daily Show/2019-03-14 - Guest.mkv", 22, nil, "alternate name"},
		{"[Group] Pioneer One - 07 [1080p].mkv", 20, nil, "absolute numbering: the series only"},
		// Episodes the show does not have.
		{"Pioneer One (2010)/Season 01/Pioneer One - S01E09.mkv", 0, nil, "unknown episode"},
		{"Pioneer One (2010)/Season 02/Pioneer One - S02E01.mkv", 0, nil, "unknown season"},
		{"Pioneer One (2010)/Season 01/Pioneer.One.S01E05E07.mkv", 0, nil, "every episode in the name must exist"},
		// Not a known show.
		{"Unknown Show/Season 1/Unknown Show - S01E01.mkv", 0, nil, ""},
		{"S01E01.mkv", 0, nil, "nothing to go on"},
	}
	for _, c := range cases {
		t.Run(c.rel, func(t *testing.T) {
			w := m.classify(KindShow, c.rel)
			var got int64
			if w.TitleID != nil {
				got = *w.TitleID
			}
			if got != c.wantID || !slices.Equal(w.EpisodeIDs, c.wantEps) {
				t.Errorf("%s (%s): title %d episodes %v, want %d %v", c.rel, c.wantName, got, w.EpisodeIDs, c.wantID, c.wantEps)
			}
			if (got != 0) != (w.State == StateMatched) {
				t.Errorf("state %q for title %d", w.State, got)
			}
			if got == 0 && w.EpisodeIDs != nil {
				t.Errorf("an unmatched file must not carry episodes: %v", w.EpisodeIDs)
			}
		})
	}
}

func TestShowMatchWhenEpisodeLookupFails(t *testing.T) {
	ts := showTitles()
	ts.epErr = errTest
	m := newMatcher(context.Background(), ts, quietLog())
	w := m.classify(KindShow, "Pioneer One (2010)/Season 01/Pioneer One - S01E01.mkv")
	if w.State != StateUnmatched || w.TitleID != nil {
		t.Errorf("a failed lookup must leave the file unmatched: %+v", w)
	}
}

func TestMusicMatchRule(t *testing.T) {
	ts := newFakeTitles()
	ts.cands[CandidateArtist] = []Candidate{
		{ID: 30, Kind: "artist", Name: "Frédéric Chopin", AltNames: []string{"Chopin"}},
		{ID: 31, Kind: "artist", Name: "Johann Sebastian Bach", AltNames: []string{"J.S. Bach"}},
		{ID: 32, Kind: "artist", Name: "Twin"},
		{ID: 33, Kind: "artist", Name: "Twin"},
	}
	m := newMatcher(context.Background(), ts, quietLog())
	cases := []struct {
		rel  string
		want int64
	}{
		{"Frédéric Chopin/Nocturnes (2012) [FLAC]/CD1/01 - Nocturne in E-flat major, Op. 9 No. 2.flac", 30},
		{"Frederic Chopin/Nocturnes/01 - Nocturne.flac", 30}, // accents folded
		{"Chopin/Préludes (1999) [MP3 320]/03 - Prélude No. 3.mp3", 30},
		{"chopin/Ballades/01 Ballade No. 1.m4a", 30},
		{"J.S. Bach/Cello Suites (1990)/01 - Prelude.flac", 31},
		{"Bach - Goldberg Variations (1981) [FLAC]/01 - Aria.flac", 0}, // "Bach" is not a known name
		{"Twin/Album/01 - Song.flac", 0},                               // two artists share the name
		{"Unknown Artist/Album/01 - Song.flac", 0},
		{"01 - Song.flac", 0},
		{"Chopin - Nocturne.flac", 30}, // a bare "Artist - Title" file
	}
	for _, c := range cases {
		t.Run(c.rel, func(t *testing.T) {
			w := m.classify(KindMusic, c.rel)
			var got int64
			if w.TitleID != nil {
				got = *w.TitleID
			}
			if got != c.want || w.TrackID != nil || w.EpisodeIDs != nil {
				t.Errorf("%s matched %d (%+v), want %d", c.rel, got, w, c.want)
			}
		})
	}
}

func TestPickIsDeterministicAboutTies(t *testing.T) {
	cands := []candIdx{
		{Candidate: Candidate{ID: 1, Name: "Dup", Year: 2000}, names: []string{"dup"}},
		{Candidate: Candidate{ID: 2, Name: "Dup", Year: 2000}, names: []string{"dup"}},
	}
	if _, ok := pick(cands, "Dup", 2000); ok {
		t.Error("two identical candidates are ambiguous")
	}
	if c, ok := pick(cands[:1], "Dup", 0); !ok || c.ID != 1 {
		t.Error("a single candidate with no year must match")
	}
	if _, ok := pick(nil, "Dup", 2000); ok {
		t.Error("no candidates, no match")
	}
	if _, ok := pick(cands, "!!!", 2000); ok {
		t.Error("an empty normalized title never matches")
	}
}

func TestSeasonFolders(t *testing.T) {
	for name, want := range map[string]bool{
		"Season 1": true, "Season 01": true, "season 12": true, "Season.2": true, "Season 1 (2010)": true,
		"Series 3": true, "Staffel 2": true, "Saison 4": true, "S01": true, "S1": true, "Specials": true, "Special": true,
		"Pioneer One": false, "Seasons": false, "Season": false, "Sesame Street": false, "S": false, "Shows": false,
	} {
		if got := isSeasonDir(name); got != want {
			t.Errorf("isSeasonDir(%q) = %v, want %v", name, got, want)
		}
	}
}

// ---- matching during a scan ---------------------------------------------------

func TestScanMatchesWithATitleSource(t *testing.T) {
	e := newEnv(t)
	e.svc.SetTitleSource(movieTitles())
	f, _ := e.addFolder(KindMovie,
		"Metropolis (1927)/Metropolis (1927).mkv",
		"Nosferatu (1922)/Nosferatu.1922.1080p.BluRay.x264-GRP.mkv",
		"Nosferatu (2024)/Nosferatu (2024).mkv",
		"Unknown Film (2001)/Unknown Film (2001).mkv",
	)
	sum := e.scan(f)
	if sum.Added != 4 || sum.Matched != 3 {
		t.Fatalf("summary = %+v", sum)
	}
	rows := e.files(f)
	check := func(rel string, state string, title int64) {
		t.Helper()
		r := rows[rel]
		var got int64
		if r.TitleID != nil {
			got = *r.TitleID
		}
		if r.State != state || got != title {
			t.Errorf("%s: state %q title %d, want %q %d", rel, r.State, got, state, title)
		}
	}
	check("Metropolis (1927)/Metropolis (1927).mkv", StateMatched, 1)
	check("Nosferatu (1922)/Nosferatu.1922.1080p.BluRay.x264-GRP.mkv", StateMatched, 2)
	check("Nosferatu (2024)/Nosferatu (2024).mkv", StateMatched, 3)
	check("Unknown Film (2001)/Unknown Film (2001).mkv", StateUnmatched, 0)
}

func TestCandidatesAreFetchedOncePerScan(t *testing.T) {
	e := newEnv(t)
	ts := movieTitles()
	e.svc.SetTitleSource(ts)
	f, _ := e.addFolder(KindMovie,
		"Metropolis (1927)/Metropolis (1927).mkv", "Sintel (2010)/Sintel (2010).mkv", "Nobody (2001)/Nobody (2001).mkv")
	e.scan(f)
	if n := ts.calls[CandidateMovie]; n != 1 {
		t.Errorf("Candidates called %d times for one scan, want 1", n)
	}
	e.scan(f) // one unmatched file is tried again
	if n := ts.calls[CandidateMovie]; n != 2 {
		t.Errorf("Candidates called %d times after two scans, want 2", n)
	}
}

func TestScanMatchesShowsAndMusic(t *testing.T) {
	e := newEnv(t)
	e.svc.SetTitleSource(showTitles())
	f, _ := e.addFolder(KindShow,
		"Pioneer One (2010)/Season 01/Pioneer One - S01E01.mkv",
		"Pioneer One (2010)/Season 01/Pioneer.One.S01E03E04.720p.mkv",
		"Pioneer One (2010)/Season 01/Pioneer One - S01E09.mkv",
	)
	sum := e.scan(f)
	if sum.Added != 3 || sum.Matched != 2 {
		t.Fatalf("summary = %+v", sum)
	}
	rows := e.files(f)
	multi := rows["Pioneer One (2010)/Season 01/Pioneer.One.S01E03E04.720p.mkv"]
	if multi.State != StateMatched || multi.TitleID == nil || *multi.TitleID != 20 || !slices.Equal(multi.EpisodeIDs, []int64{203, 204}) {
		t.Errorf("multi-episode row = %+v", multi)
	}
	if r := rows["Pioneer One (2010)/Season 01/Pioneer One - S01E09.mkv"]; r.State != StateUnmatched || r.EpisodeIDs != nil {
		t.Errorf("unknown episode row = %+v", r)
	}

	mts := newFakeTitles()
	mts.cands[CandidateArtist] = []Candidate{{ID: 30, Kind: "artist", Name: "Frédéric Chopin", AltNames: []string{"Chopin"}}}
	e2 := newEnv(t)
	e2.svc.SetTitleSource(mts)
	mf, _ := e2.addFolder(KindMusic,
		"Frederic Chopin/Nocturnes (2012) [FLAC]/CD1/01 - Nocturne.flac", "Somebody Else/Album/01 - Song.flac")
	sum = e2.scan(mf)
	if sum.Added != 2 || sum.Matched != 1 {
		t.Fatalf("music summary = %+v", sum)
	}
	if r := e2.files(mf)["Frederic Chopin/Nocturnes (2012) [FLAC]/CD1/01 - Nocturne.flac"]; r.State != StateMatched ||
		r.TitleID == nil || *r.TitleID != 30 || r.TrackID != nil {
		t.Errorf("music row = %+v", r)
	}
}

func TestUnmatchedFilesAreRetriedWhenATitleSourceAppears(t *testing.T) {
	e := newEnv(t)
	f, _ := e.addFolder(KindMovie,
		"Metropolis (1927)/Metropolis (1927).mkv", "Unknown Film (2001)/Unknown Film (2001).mkv")
	// No source yet: recorded, unmatched.
	if sum := e.scan(f); sum.Added != 2 || sum.Matched != 0 {
		t.Fatalf("first scan = %+v", sum)
	}
	// The titles service arrives; nothing changed on disk.
	e.svc.SetTitleSource(movieTitles())
	sum := e.scan(f)
	if sum.Unchanged != 2 || sum.Matched != 1 || sum.Added+sum.Updated != 0 {
		t.Fatalf("second scan = %+v", sum)
	}
	rows := e.files(f)
	if r := rows["Metropolis (1927)/Metropolis (1927).mkv"]; r.State != StateMatched || r.TitleID == nil || *r.TitleID != 1 {
		t.Errorf("row = %+v", r)
	}
	// A matched file is left alone on later scans.
	if sum := e.scan(f); sum.Matched != 0 || sum.Unchanged != 2 {
		t.Errorf("third scan = %+v", sum)
	}
	// And the source can be taken away again without losing a match.
	e.svc.SetTitleSource(nil)
	e.scan(f)
	if r := e.files(f)["Metropolis (1927)/Metropolis (1927).mkv"]; r.State != StateMatched {
		t.Errorf("a match was lost when the source went away: %+v", r)
	}
}

func TestScanSurvivesATitleSourceThatFails(t *testing.T) {
	e := newEnv(t)
	ts := movieTitles()
	ts.candErr = errTest
	e.svc.SetTitleSource(ts)
	f, _ := e.addFolder(KindMovie, "Metropolis (1927)/Metropolis (1927).mkv")
	sum := e.scan(f)
	if sum.Added != 1 || sum.Matched != 0 {
		t.Fatalf("summary = %+v", sum)
	}
	if r := e.files(f)["Metropolis (1927)/Metropolis (1927).mkv"]; r.State != StateUnmatched {
		t.Errorf("row = %+v", r)
	}
	// It heals on the next scan.
	ts.candErr = nil
	if sum := e.scan(f); sum.Matched != 1 {
		t.Errorf("after the source recovers: %+v", sum)
	}
}
