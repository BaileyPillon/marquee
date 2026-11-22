package importer

import (
	"path/filepath"
	"strings"
	"testing"

	"github.com/BaileyPillon/marquee/internal/titles"
)

func movieDetail(name string, year int, alts ...string) *titles.Detail {
	return &titles.Detail{Title: titles.Title{ID: 7, Kind: titles.KindMovie, Name: name, Year: year, AltNames: alts}}
}

// verifyOne runs the verifier on one file. folder is the download's own folder
// ("" when the download is one loose file) and file is the path under it with
// "/" (it may be in a sub folder).
func verifyOne(t *testing.T, d *titles.Detail, folder, file, release string) ([]plan, []skip) {
	t.Helper()
	base := t.TempDir()
	root := ""
	path := filepath.Join(base, filepath.FromSlash(file))
	if folder != "" {
		root = filepath.Join(base, folder)
		path = filepath.Join(root, filepath.FromSlash(file))
	}
	v := newVerifier(d, root, release, nil)
	return v.verify([]srcFile{{Path: path, Rel: file, Size: 10}})
}

func TestMovieNameCheckReadsTheFirstNameThatIsTheFilm(t *testing.T) {
	spring := movieDetail("Spring", 2019)
	sprite := movieDetail("Sprite Fright", 2021)
	charge := movieDetail("Charge", 2022)
	up := movieDetail("Up", 2009)

	const (
		springRel = "Spring.2019.1080p.WEB-DL.x264-Blender"
		spriteRel = "Sprite.Fright.2021.1080p.WEB-DL.x264-Blender"
		chargeRel = "Charge.2022.2160p.WEB-DL.x265-Blender"
	)
	tests := []struct {
		name    string
		movie   *titles.Detail
		folder  string // the download's own folder; "" for one loose file
		file    string
		release string
		want    string // where the file goes; "" when it is turned down
		reason  string // a part of the reason when it is turned down
	}{
		// The three real downloads: a file name with extra words or mashed together,
		// a release name and folder that are right.
		{name: "mashed file name", movie: spring, folder: springRel, file: "springopenmovie.mp4", release: springRel,
			want: "Spring (2019)/Spring (2019) WEBDL-1080p.mp4"},
		{name: "extra words and a bare resolution", movie: sprite, folder: spriteRel,
			file: "Sprite Fright - Open Movie by Blender Studio-804p.mp4", release: spriteRel,
			want: "Sprite Fright (2021)/Sprite Fright (2021) WEBDL-1080p.mp4"},
		{name: "extra words and an odd resolution", movie: charge, folder: chargeRel,
			file: "Charge - Blender Open Movie 1608p.mp4", release: chargeRel,
			want: "Charge (2022)/Charge (2022) WEBDL-2160p.mp4"},
		// The same files as loose single-file downloads: only the release name helps.
		{name: "mashed file name, loose file", movie: spring, file: "springopenmovie.mp4", release: springRel,
			want: "Spring (2019)/Spring (2019) WEBDL-1080p.mp4"},
		{name: "extra words, loose file", movie: sprite, file: "Sprite Fright - Open Movie by Blender Studio-804p.mp4", release: spriteRel,
			want: "Sprite Fright (2021)/Sprite Fright (2021) WEBDL-1080p.mp4"},
		{name: "odd resolution, loose file", movie: charge, file: "Charge - Blender Open Movie 1608p.mp4", release: chargeRel,
			want: "Charge (2022)/Charge (2022) WEBDL-2160p.mp4"},
		// More forms of the same idea.
		{name: "official, full hd", movie: spring, folder: springRel, file: "Spring - Official Blender Open Movie - Full HD.mp4", release: springRel,
			want: "Spring (2019)/Spring (2019) WEBDL-1080p.mp4"},
		{name: "spaced noise after a short title", movie: up, file: "Up - Open Movie.mkv", release: "Up.2009.1080p.BluRay.x264-GRP",
			want: "Up (2009)/Up (2009) Bluray-1080p.mkv"},
		{name: "alternative name plus noise", movie: movieDetail("Spirited Away", 2001, "Sen to Chihiro no Kamikakushi"),
			file: "Sen to Chihiro no Kamikakushi - Official Movie.mkv", release: "Spirited.Away.2001.1080p.BluRay.x264-GRP",
			want: "Spirited Away (2001)/Spirited Away (2001) Bluray-1080p.mkv"},
		{name: "a year that is part of the title still works", movie: movieDetail("Blade Runner 2049", 2017),
			file: "Blade.Runner.2049.2017.1080p.BluRay.x264-GRP.mkv", release: "Blade.Runner.2049.2017.1080p.BluRay.x264-GRP",
			want: "Blade Runner 2049 (2017)/Blade Runner 2049 (2017) Bluray-1080p.mkv"},
		{name: "a file that says nothing is read from the folder", movie: spring, folder: springRel, file: "movie.mkv", release: springRel,
			want: "Spring (2019)/Spring (2019) WEBDL-1080p.mkv"},
		// A folder that is not the film is passed over; it never rejects on its own.
		{name: "a folder that is not the film is passed over", movie: charge, folder: "Charge Blender Open Movie Extras",
			file: "movie.mkv", release: chargeRel, want: "Charge (2022)/Charge (2022) WEBDL-2160p.mkv"},

		// Another film is still another film.
		{name: "a longer title that starts with the film's name", movie: charge, folder: chargeRel,
			file: "Charge of the Light Brigade.mkv", release: chargeRel, reason: `"Charge of the Light Brigade"`},
		{name: "a sequel number is not noise", movie: charge, file: "Charge 2.mkv", release: chargeRel, reason: `"Charge 2"`},
		{name: "another film in a right release", movie: movieDetail("Metropolis", 1927), folder: "Metropolis.1927.1080p.BluRay.x264-GRP",
			file: "Nosferatu.mkv", release: "Metropolis.1927.1080p.BluRay.x264-GRP", reason: `"Nosferatu"`},
		{name: "another film in a right release, loose file", movie: movieDetail("Metropolis", 1927),
			file: "Nosferatu.mkv", release: "Metropolis.1927.1080p.BluRay.x264-GRP", reason: `"Nosferatu"`},
		{name: "another film that only starts like the film", movie: spring, folder: springRel,
			file: "Springfield.mkv", release: springRel, reason: `"Springfield"`},
		{name: "a mashed form needs a title of four characters", movie: up, file: "upopenmovie.mkv",
			release: "Up.2009.1080p.BluRay.x264-GRP", reason: `"upopenmovie"`},
		{name: "six characters are enough to name another film", movie: up, file: "Casino.mkv",
			release: "Up.2009.1080p.BluRay.x264-GRP", reason: `"Casino"`},
		// What a short single word cannot do: it does not count against the film, so
		// the release decides.
		{name: "a short single word is no contradiction", movie: up, file: "Cars.mkv",
			release: "Up.2009.1080p.BluRay.x264-GRP", want: "Up (2009)/Up (2009) Bluray-1080p.mkv"},
		// Nothing names the film: the reason is the first useful reading.
		{name: "no name is the film", movie: movieDetail("Night of the Living Dead", 1968), folder: "The.Matrix.1999.1080p.BluRay.x264-GRP",
			file: "The.Matrix.1999.1080p.BluRay.x264-GRP.mkv", release: "The.Matrix.1999.1080p.BluRay.x264-GRP", reason: `"The Matrix"`},
		{name: "a sequel that starts with the film's name", movie: movieDetail("Blade Runner", 1982),
			file: "Blade.Runner.2049.2017.1080p.BluRay.x264-GRP.mkv", release: "Blade.Runner.1982.1080p.BluRay.x264-GRP", reason: `"Blade Runner 2049"`},
		{name: "a TV episode", movie: spring, file: "Some.Show.S01E01.1080p.WEB-DL.mkv", release: springRel, reason: "TV episode"},
		{name: "nothing says what it is", movie: spring, file: "movie.mkv", release: "", reason: "does not say"},

		// The year check.
		{name: "the right file in a release of the wrong year", movie: spring, folder: "Spring.1999.1080p.WEB-DL.x264-Blender",
			file: "springopenmovie.mp4", release: "Spring.1999.1080p.WEB-DL.x264-Blender", reason: "Wrong year: this is from 1999"},
		{name: "a year one off is fine", movie: spring, folder: "Spring.2020.1080p.WEB-DL.x264-Blender",
			file: "springopenmovie.mp4", release: "Spring.2020.1080p.WEB-DL.x264-Blender", want: "Spring (2019)/Spring (2019) WEBDL-1080p.mp4"},
		{name: "a folder that names another title does not give the year", movie: charge, folder: "Extras 2005",
			file: "Charge - Blender Open Movie 1608p.mp4", release: chargeRel, want: "Charge (2022)/Charge (2022) WEBDL-2160p.mp4"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			plans, skips := verifyOne(t, tc.movie, tc.folder, tc.file, tc.release)
			if tc.want == "" {
				if len(plans) != 0 || len(skips) != 1 {
					t.Fatalf("expected the file to be turned down: plans %+v, skips %+v", plans, skips)
				}
				if !strings.Contains(skips[0].Reason, tc.reason) {
					t.Errorf("reason = %q, want it to contain %q", skips[0].Reason, tc.reason)
				}
				return
			}
			if len(plans) != 1 || len(skips) != 0 {
				t.Fatalf("expected the file to be imported: plans %+v, skips %+v", plans, skips)
			}
			if p := plans[0]; p.dest != tc.want || p.titleID != tc.movie.ID {
				t.Errorf("dest = %q (title %d), want %q (title %d)", p.dest, p.titleID, tc.want, tc.movie.ID)
			}
		})
	}
}

// The reason a film that is not there is turned down names the first useful
// reading and the film that was wanted.
func TestMovieMismatchReasonNamesBothTitles(t *testing.T) {
	_, skips := verifyOne(t, movieDetail("Metropolis", 1927), "", "Nosferatu.mkv", "Metropolis.1927.1080p.BluRay.x264-GRP")
	if len(skips) != 1 || skips[0].Reason != `This is "Nosferatu", not "Metropolis"` {
		t.Errorf("skips = %+v", skips)
	}
}

func TestTitleVariant(t *testing.T) {
	tests := []struct {
		name, want string
		ok         bool
	}{
		{"sprite fright", "sprite fright", true},
		{"sprite fright open movie by blender studio", "sprite fright", true},
		{"sprite fright open movie by blender studio 804p", "sprite fright", true},
		{"charge blender open movie 1608p", "charge", true},
		{"charge 2160p", "charge", true},
		{"charge hd 4k", "charge", true},
		{"charge of the light brigade", "charge", false},
		{"charge 2", "charge", false},
		{"charge the movie", "charge", false}, // "the" is not noise
		{"sprite", "sprite fright", false},
		{"springopenmovie", "spring", true},
		{"spring", "spring", true},
		{"springopenmoviex", "spring", false},
		{"springfield", "spring", false},
		{"spiderman", "spider man", true}, // written together
		{"spiderman official", "spider man", true},
		{"upopenmovie", "up", false}, // the title is too short for a mashed match
		{"hermovie", "her", false},
		{"", "spring", false},
		{"spring", "", false},
	}
	for _, tc := range tests {
		if got := titleVariant(tc.name, tc.want); got != tc.ok {
			t.Errorf("titleVariant(%q, %q) = %v, want %v", tc.name, tc.want, got, tc.ok)
		}
	}
}

func TestCleanTitle(t *testing.T) {
	for title, want := range map[string]bool{
		"Nosferatu": true, "Casino": true, "Charge of the Light Brigade": true, "Up Close": true,
		"Cars": false, "Up": false, "Alien": false, "Her": false, "": false,
	} {
		if got := cleanTitle(title); got != want {
			t.Errorf("cleanTitle(%q) = %v, want %v", title, got, want)
		}
	}
}

// A show is still checked against the first useful reading by equal names: a
// file that names another show is not turned into this one by a right release
// name.
func TestShowNameCheckIsUnchanged(t *testing.T) {
	glee := &titles.Detail{
		Title:    titles.Title{ID: 9, Kind: titles.KindShow, Name: "Glee"},
		Episodes: []titles.Episode{{ID: 1, Season: 1, Number: 1, Name: "Pilot"}},
	}
	const rel = "Glee.S01E01.1080p.WEB-DL.x264-GRP"
	tests := []struct {
		name, folder, file, release string
		ok                          bool
	}{
		{"the show's own file", rel, "Glee.S01E01.1080p.WEB-DL.x264-GRP.mkv", rel, true},
		{"a file with no title", rel, "S01E01.mkv", rel, true},
		{"another show's file in a right release", rel, "Lost.S01E01.mkv", rel, false},
		{"extra words after the show's name", rel, "Glee Open Movie.S01E01.mkv", rel, false},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			plans, skips := verifyOne(t, glee, tc.folder, tc.file, tc.release)
			if tc.ok && (len(plans) != 1 || len(skips) != 0) {
				t.Fatalf("expected an import: plans %+v, skips %+v", plans, skips)
			}
			if !tc.ok && (len(plans) != 0 || len(skips) != 1 || !strings.HasPrefix(skips[0].Reason, "This is ")) {
				t.Fatalf("expected it to be turned down: plans %+v, skips %+v", plans, skips)
			}
		})
	}
}
