package decide

import (
	"strings"
	"testing"
	"time"

	"github.com/BaileyPillon/marquee/internal/grab"
	"github.com/BaileyPillon/marquee/internal/parse"
	"github.com/BaileyPillon/marquee/internal/titles"
)

var testNow = time.Date(2026, 11, 20, 12, 0, 0, 0, time.UTC)

func ip(n int) *int { return &n }

const hashA = "0123456789abcdef0123456789abcdef01234567"

func parseOf(name string) parse.Release { return parse.ParseRelease(name) }

const (
	mbSize = int64(1 << 20)
	gbSize = int64(1 << 30)
)

// The seeded profiles, as the migration writes them.
func movieHD() Profile {
	return Profile{ID: 1, Name: "HD", Kind: KindMovie, Quality: grab.QualityHD, AllowedRes: []int{1080, 720}, PreferredRes: 1080,
		AllowedSources: []string{"Remux", "BluRay", "WEB-DL", "WEBRip", "HDTV"}, MaxSizeMB: ip(20480), MinSeeders: 1}
}

func movie4K() Profile {
	return Profile{ID: 2, Name: "4K", Kind: KindMovie, Quality: grab.QualityUHD, AllowedRes: []int{2160}, PreferredRes: 2160,
		AllowedSources: []string{"Remux", "BluRay", "WEB-DL", "WEBRip", "HDTV"}, MaxSizeMB: ip(61440), PreferHDR: true, MinSeeders: 1}
}

func showHD() Profile {
	return Profile{ID: 3, Name: "HD", Kind: KindShow, Quality: grab.QualityHD, AllowedRes: []int{1080, 720}, PreferredRes: 1080,
		AllowedSources: []string{"Remux", "BluRay", "WEB-DL", "WEBRip", "HDTV"}, MaxSizePerHourMB: ip(4096), MinSeeders: 1}
}

func lossless() Profile {
	return Profile{ID: 5, Name: "Lossless", Kind: KindArtist, Quality: grab.QualityHD, AllowedSources: []string{"FLAC", "MP3-320"}, MinSeeders: 1}
}

var (
	metropolis = titles.Title{ID: 1, Kind: titles.KindMovie, Name: "Metropolis", Year: 1927, IMDbID: "tt0017136", TMDBID: 19, RuntimeMin: 153}
	sintel     = titles.Title{ID: 2, Kind: titles.KindMovie, Name: "Sintel", Year: 2010, IMDbID: "tt1727587", TMDBID: 45745, RuntimeMin: 15}
	notld      = titles.Title{ID: 3, Kind: titles.KindMovie, Name: "Night of the Living Dead", Year: 1968, IMDbID: "tt0063350", TMDBID: 10331,
		AltNames: []string{"Night of the Living Dead (1968)"}}
	pioneer = titles.Title{ID: 20, Kind: titles.KindShow, Name: "Pioneer One", Year: 2010, IMDbID: "tt9900001", TVmazeID: 99001, RuntimeMin: 47}
	chopin  = titles.Title{ID: 30, Kind: titles.KindArtist, Name: "Frédéric Chopin", AltNames: []string{"Chopin"}}
)

// rel is a healthy torrent release: 4 GB, 20 seeders, a day old.
func rel(name string) Release {
	return Release{GUID: "g:" + name, Title: name, Protocol: grab.ProtocolTorrent, Size: 4 * gbSize, Seeders: 20,
		PublishedAt: testNow.Add(-24 * time.Hour)}
}

func score(p Profile, t titles.Title, w Want, r Release) Verdict {
	w.Now = testNow
	return Score(p, t, w, parse.ParseRelease(r.Title), r)
}

func ep(season int, eps ...int) Want { return Want{Season: season, Episodes: eps, SeasonEpisodes: 8} }

type scoreCase struct {
	name       string
	profile    Profile
	title      titles.Title
	want       Want
	release    Release
	accepted   bool
	rejection  string   // the exact first rejection, when rejected
	contains   []string // each must appear in some reason
	notContain []string
}

func (c scoreCase) run(t *testing.T) {
	t.Helper()
	v := score(c.profile, c.title, c.want, c.release)
	if v.Accepted != c.accepted {
		t.Errorf("%s: accepted = %v, want %v; reasons %q", c.name, v.Accepted, c.accepted, v.Reasons)
		return
	}
	if c.accepted {
		if v.Rejection != "" || v.Score <= 0 || len(v.Reasons) == 0 || !strings.HasPrefix(v.Reasons[0], "Good fit: ") {
			t.Errorf("%s: an accepted verdict should have a score, no rejection and a headline: %+v", c.name, v)
		}
	} else {
		if v.Score != 0 || v.Rejection == "" || len(v.Reasons) == 0 || v.Reasons[0] != v.Rejection {
			t.Errorf("%s: a rejected verdict should have score 0 and its first reason as the rejection: %+v", c.name, v)
		}
		if c.rejection != "" && v.Rejection != c.rejection {
			t.Errorf("%s: rejection = %q, want %q", c.name, v.Rejection, c.rejection)
		}
	}
	all := strings.Join(v.Reasons, " | ")
	for _, want := range c.contains {
		if !strings.Contains(all, want) {
			t.Errorf("%s: reasons %q lack %q", c.name, all, want)
		}
	}
	for _, bad := range c.notContain {
		if strings.Contains(all, bad) {
			t.Errorf("%s: reasons %q should not contain %q", c.name, all, bad)
		}
	}
}

func with(r Release, f func(*Release)) Release { f(&r); return r }

func TestScoreMovies(t *testing.T) {
	hd, k4 := movieHD(), movie4K()
	big := func(size int64) func(*Release) { return func(r *Release) { r.Size = size } }
	seeders := func(n int) func(*Release) { return func(r *Release) { r.Seeders = n } }
	usenet := func(r *Release) { r.Protocol = grab.ProtocolUsenet; r.Seeders = 0 }

	cases := []scoreCase{
		// Size.
		{name: "1080p BluRay inside the limit", profile: hd, title: metropolis, release: with(rel("Metropolis.1927.1080p.BluRay.x264-GRP"), big(8*gbSize)),
			accepted: true, contains: []string{"Good fit: 1080p BluRay encode under your size limit"}},
		{name: "720p WEB-DL is allowed", profile: hd, title: metropolis, release: rel("Metropolis.1927.720p.WEB-DL.x264-GRP"),
			accepted: true, contains: []string{"Good fit: 720p WEB-DL under your size limit"}},
		{name: "over the size limit", profile: hd, title: metropolis, release: with(rel("Metropolis.1927.1080p.BluRay.x264-GRP"), big(25*gbSize)),
			rejection: "Too big: 25.0 GB is over your 20 GB limit"},
		{name: "exactly at the size limit", profile: hd, title: metropolis, release: with(rel("Metropolis.1927.1080p.BluRay.x264-GRP"), big(20*gbSize)),
			accepted: true},
		{name: "under the minimum size", profile: with2(hd, func(p *Profile) { p.MinSizeMB = ip(500) }), title: metropolis,
			release:   with(rel("Metropolis.1927.1080p.BluRay.x264-GRP"), big(300*mbSize)),
			rejection: "Too small: 300 MB is under your 500 MB minimum"},
		{name: "an unknown size is not judged", profile: hd, title: metropolis, release: with(rel("Metropolis.1927.1080p.BluRay.x264-GRP"), big(0)),
			accepted: true, notContain: []string{"size limit"}},
		{name: "the 4K limit, in the owner's words", profile: k4, title: metropolis,
			release:   with(rel("Metropolis.1927.2160p.BluRay.x265-GRP"), func(r *Release) { r.Size = 681 * gbSize / 10 }),
			rejection: "Too big: 68.1 GB is over your 60 GB limit"},

		// Resolution.
		{name: "2160p is not an HD resolution", profile: hd, title: metropolis, release: rel("Metropolis.1927.2160p.BluRay.x265-GRP"),
			rejection: "Resolution not allowed: this is 2160p, your profile takes 720p or 1080p"},
		{name: "480p DVD rip", profile: hd, title: metropolis, release: rel("Metropolis.1927.DVDRip.x264-GRP"),
			contains: []string{"Resolution not allowed: this is 480p", "Source not allowed: a DVD rip"}},
		{name: "1080p is not a 4K resolution", profile: k4, title: metropolis, release: rel("Metropolis.1927.1080p.BluRay.x264-GRP"),
			rejection: "Resolution not allowed: this is 1080p, your profile takes 2160p"},
		{name: "a bare BluRay counts as 1080p", profile: hd, title: metropolis, release: rel("Metropolis.1927.BluRay.x264-GRP"), accepted: true},
		{name: "a name with no resolution and no source", profile: with2(hd, func(p *Profile) { p.AllowedSources = nil }), title: metropolis,
			release: rel("Metropolis.1927.x264-GRP"), rejection: "Resolution not stated: the name does not say how sharp the picture is"},
		{name: "4K remux accepted", profile: k4, title: metropolis, release: with(rel("Metropolis.1927.2160p.UHD.BluRay.REMUX.HDR.HEVC-GRP"), big(40*gbSize)),
			accepted: true, contains: []string{"2160p remux with HDR, under your size limit"}},

		// Sources.
		{name: "HDTV not on the profile's list", profile: with2(hd, func(p *Profile) { p.AllowedSources = []string{"BluRay", "WEB-DL"} }), title: metropolis,
			release: rel("Metropolis.1927.720p.HDTV.x264-GRP"), rejection: "Source not allowed: an HDTV capture is not one your profile takes"},
		{name: "no source stated, list is strict", profile: hd, title: metropolis, release: rel("Metropolis.1927.1080p.x264-GRP"),
			rejection: "Source not stated: the name does not say if it is a BluRay, WEB or TV capture"},
		{name: "no source stated, Unknown ticked", profile: with2(hd, func(p *Profile) { p.AllowedSources = []string{"BluRay", "Unknown"} }), title: metropolis,
			release: rel("Metropolis.1927.1080p.x264-GRP"), accepted: true},
		{name: "any source when the list is empty", profile: with2(hd, func(p *Profile) { p.AllowedSources = nil }), title: metropolis,
			release: rel("Metropolis.1927.720p.HDTV.x264-GRP"), accepted: true},

		// Camera copies are never taken.
		{name: "CAM", profile: hd, title: metropolis, release: rel("Metropolis.1927.CAM.x264-GRP"),
			rejection: "Rejected: this is a camera recording (CAM) from a cinema"},
		{name: "HDCAM", profile: hd, title: metropolis, release: rel("Metropolis.1927.1080p.HDCAM.x264-GRP"),
			rejection: "Rejected: this is a camera recording (CAM) from a cinema"},
		{name: "telesync", profile: hd, title: metropolis, release: rel("Metropolis.1927.TELESYNC.x264-GRP"),
			rejection: "Rejected: this is a telesync (TS), a cinema recording with separate sound"},
		{name: "telecine", profile: hd, title: metropolis, release: rel("Metropolis.1927.TELECINE.x264-GRP"),
			rejection: "Rejected: this is a telecine (TC) copy"},
		{name: "screener", profile: hd, title: metropolis, release: rel("Metropolis.1927.DVDSCR.x264-GRP"),
			rejection: "Rejected: this is a screener, a pre-release review copy"},
		{name: "a CAM is refused even in a profile that lists every source", profile: with2(hd, func(p *Profile) { p.AllowedSources = nil }), title: metropolis,
			release: rel("Metropolis.1927.CAM.x264-GRP"), rejection: "Rejected: this is a camera recording (CAM) from a cinema"},
		{name: "a sample file", profile: hd, title: metropolis, release: rel("Metropolis.1927.1080p.BluRay.x264-GRP-sample"),
			rejection: "This is a sample file, not the full release"},

		// Seeders.
		{name: "nobody is sharing it", profile: hd, title: metropolis, release: with(rel("Metropolis.1927.1080p.BluRay.x264-GRP"), seeders(0)),
			rejection: "No one is sharing it (0 seeders)"},
		{name: "fewer seeders than asked", profile: with2(hd, func(p *Profile) { p.MinSeeders = 3 }), title: metropolis,
			release:   with(rel("Metropolis.1927.1080p.BluRay.x264-GRP"), seeders(2)),
			rejection: "Too few people sharing it (2 seeders, your profile wants at least 3)"},
		{name: "enough seeders", profile: with2(hd, func(p *Profile) { p.MinSeeders = 3 }), title: metropolis,
			release: with(rel("Metropolis.1927.1080p.BluRay.x264-GRP"), seeders(3)), accepted: true},
		{name: "seeders not stated by the feed", profile: hd, title: metropolis, release: with(rel("Metropolis.1927.1080p.BluRay.x264-GRP"), seeders(-1)),
			accepted: true},
		{name: "usenet has no seeders", profile: hd, title: metropolis, release: with(rel("Metropolis.1927.1080p.BluRay.x264-GRP"), usenet),
			accepted: true},
		{name: "a profile that takes zero seeders", profile: with2(hd, func(p *Profile) { p.MinSeeders = 0 }), title: metropolis,
			release: with(rel("Metropolis.1927.1080p.BluRay.x264-GRP"), seeders(0)), accepted: true},
		{name: "well shared", profile: hd, title: metropolis, release: with(rel("Metropolis.1927.1080p.BluRay.x264-GRP"), seeders(120)),
			accepted: true, contains: []string{"Well shared (120 seeders)"}},

		// Words.
		{name: "a rejected word", profile: with2(hd, func(p *Profile) { p.RejectedWords = []string{"HC", "Korsub"} }), title: metropolis,
			release: rel("Metropolis.1927.1080p.BluRay.x264.HC-GRP"), rejection: `Has a word you rejected: "HC"`},
		{name: "a rejected word is matched whole and in any case", profile: with2(hd, func(p *Profile) { p.RejectedWords = []string{"hc"} }), title: metropolis,
			release: rel("Metropolis.1927.1080p.BluRay.x264-GRP"), accepted: true},
		{name: "a rejected phrase", profile: with2(hd, func(p *Profile) { p.RejectedWords = []string{"hard coded"} }), title: metropolis,
			release: rel("Metropolis.1927.1080p.BluRay.x264.Hard.Coded-GRP"), rejection: `Has a word you rejected: "hard coded"`},
		{name: "a preferred word", profile: with2(hd, func(p *Profile) { p.PreferredWords = []string{"Criterion"} }), title: metropolis,
			release: rel("Metropolis.1927.Criterion.1080p.BluRay.x264-GRP"), accepted: true, contains: []string{`Has a word "Criterion" you prefer`}},

		// Title and year.
		{name: "another film of the same name", profile: hd, title: metropolis, release: rel("Metropolis.2001.1080p.BluRay.x264-GRP"),
			rejection: "Wrong year: this is from 2001, you want the 1927 one"},
		{name: "a year out by one is fine", profile: hd, title: metropolis, release: rel("Metropolis.1928.1080p.BluRay.x264-GRP"), accepted: true},
		{name: "a year out by two is not", profile: hd, title: metropolis, release: rel("Metropolis.1929.1080p.BluRay.x264-GRP"),
			rejection: "Wrong year: this is from 1929, you want the 1927 one"},
		{name: "no year in the name", profile: hd, title: metropolis, release: rel("Metropolis.1080p.BluRay.x264-GRP"),
			rejection: `No year in the name, so it cannot be told apart from other films called "Metropolis"`},
		{name: "a different film", profile: hd, title: metropolis, release: rel("Sintel.2010.1080p.BluRay.x264-GRP"),
			rejection: `Wrong title: this is "Sintel", you want "Metropolis"`},
		{name: "case and dots do not matter", profile: hd, title: sintel, release: rel("SINTEL.2010.1080P.BluRay.x264-GRP"), accepted: true},
		{name: "an alternative name", profile: hd, title: notld, release: rel("Night.of.the.Living.Dead.1968.1080p.BluRay.x264-GRP"), accepted: true},
		{name: "a name the catalog lists as another", profile: hd, title: with3(metropolis, func(t *titles.Title) { t.AltNames = []string{"Fritz Lang's Metropolis"} }),
			release: rel("Fritz.Langs.Metropolis.1927.1080p.BluRay.x264-GRP"), accepted: true},
		{name: "a TV episode is not a film", profile: hd, title: metropolis, release: rel("Metropolis.S01E01.1080p.WEB-DL.x264-GRP"),
			rejection: "This looks like a TV episode, not a movie"},

		// Ids from the source.
		{name: "an IMDb id settles the title whatever the name says", profile: hd, title: metropolis,
			release: with(rel("Fritz.Lang.Silent.Classic.1080p.BluRay.x264-GRP"), func(r *Release) { r.IMDb = "tt0017136" }), accepted: true},
		{name: "a TMDB id settles the title too", profile: hd, title: metropolis,
			release: with(rel("Silent.Classic.1080p.BluRay.x264-GRP"), func(r *Release) { r.TMDb = 19 }), accepted: true},
		{name: "a different IMDb id is a different film", profile: hd, title: metropolis,
			release:   with(rel("Metropolis.1927.1080p.BluRay.x264-GRP"), func(r *Release) { r.IMDb = "tt0123456" }),
			rejection: "Wrong title: the source says this is IMDb tt0123456, not tt0017136"},
		{name: "a different TMDB id is a different film", profile: hd, title: metropolis,
			release:   with(rel("Metropolis.1927.1080p.BluRay.x264-GRP"), func(r *Release) { r.TMDb = 99 }),
			rejection: "Wrong title: the source says this is TMDB 99, not 19"},

		// Blocklist.
		{name: "a blocked release", profile: hd, title: metropolis,
			release:   with(rel("Metropolis.1927.1080p.BluRay.x264-GRP"), func(r *Release) { r.Blocked = "the audio was out of sync" }),
			rejection: "You blocked this release before: the audio was out of sync"},
		{name: "a blocked release that is also wrong in other ways says only the block", profile: hd, title: metropolis,
			release:   with(rel("Metropolis.1927.2160p.BluRay.x265-GRP"), func(r *Release) { r.Blocked = "bad" }),
			rejection: "You blocked this release before: bad"},
	}
	for _, c := range cases {
		c.run(t)
	}
}

func with2(p Profile, f func(*Profile)) Profile { f(&p); return p }

func with3(t titles.Title, f func(*titles.Title)) titles.Title { f(&t); return t }

func TestScoreReportsEveryQualityProblem(t *testing.T) {
	// 720p is not allowed in 4K, and it is also too big and not shared.
	r := rel("Metropolis.1927.720p.WEB-DL.x264-GRP")
	r.Size, r.Seeders = 70*gbSize, 0
	v := score(movie4K(), metropolis, Want{}, r)
	if v.Accepted || len(v.Reasons) != 3 {
		t.Fatalf("verdict = %+v", v)
	}
	for _, want := range []string{"Resolution not allowed", "Too big: 70.0 GB is over your 60 GB limit", "No one is sharing it (0 seeders)"} {
		if !strings.Contains(strings.Join(v.Reasons, "|"), want) {
			t.Errorf("reasons %q lack %q", v.Reasons, want)
		}
	}
}

func TestScoreShows(t *testing.T) {
	hd := showHD()
	size := func(n int64) func(*Release) { return func(r *Release) { r.Size = n } }
	pioneerEp := func(name string) Release { return with(rel(name), size(1*gbSize)) }

	cases := []scoreCase{
		{name: "the episode that is wanted", profile: hd, title: pioneer, want: ep(1, 4), release: pioneerEp("Pioneer.One.S01E04.720p.WEB-DL.x264-GRP"),
			accepted: true, contains: []string{"Good fit: 720p WEB-DL under your size limit"}},
		{name: "another episode, in the owner's words", profile: hd, title: pioneer, want: ep(1, 4), release: pioneerEp("Pioneer.One.S01E05.720p.WEB-DL.x264-GRP"),
			rejection: "Wrong episode: this is S01E05, you need S01E04"},
		{name: "the right episode of another season", profile: hd, title: pioneer, want: ep(1, 4), release: pioneerEp("Pioneer.One.S02E04.720p.WEB-DL.x264-GRP"),
			rejection: "Wrong episode: this is S02E04, you need S01E04"},
		{name: "one of several wanted", profile: hd, title: pioneer, want: ep(1, 3, 4, 5), release: pioneerEp("Pioneer.One.S01E04.720p.WEB-DL.x264-GRP"),
			accepted: true},
		{name: "none of several wanted", profile: hd, title: pioneer, want: ep(1, 3, 4, 5), release: pioneerEp("Pioneer.One.S01E06.720p.WEB-DL.x264-GRP"),
			rejection: "Wrong episode: this is S01E06, you need S01E03, S01E04 or S01E05"},
		{name: "a season pack for one wanted episode", profile: hd, title: pioneer, want: ep(1, 4), release: with(rel("Pioneer.One.S01.720p.WEB-DL.x264-GRP"), size(5*gbSize)),
			rejection: "Season pack, but you only need S01E04"},
		{name: "a season pack when two are wanted", profile: hd, title: pioneer, want: ep(1, 3, 4), release: with(rel("Pioneer.One.S01.720p.WEB-DL.x264-GRP"), size(5*gbSize)),
			accepted: true, contains: []string{"720p WEB-DL season pack under your size limit", "Covers all 2 episodes you need in one download"}},
		{name: "a season pack of the wrong season", profile: hd, title: pioneer, want: ep(1, 3, 4), release: with(rel("Pioneer.One.S02.720p.WEB-DL.x264-GRP"), size(5*gbSize)),
			rejection: "Wrong season: this pack is season 2, you need season 1"},
		{name: "a pack of several seasons", profile: hd, title: pioneer, want: ep(1, 3, 4), release: with(rel("Pioneer.One.S01-S03.720p.WEB-DL.x264-GRP"), size(9*gbSize)),
			rejection: "Multi-season pack (seasons 1 to 3): Marquee picks one season at a time"},
		{name: "a complete series", profile: hd, title: pioneer, want: ep(1, 3, 4), release: with(rel("Pioneer.One.COMPLETE.720p.WEB-DL.x264-GRP"), size(9*gbSize)),
			rejection: "Whole-series pack: Marquee picks one season at a time"},
		{name: "a double episode with one wanted", profile: hd, title: pioneer, want: ep(1, 4), release: with(rel("Pioneer.One.S01E04E05.720p.WEB-DL.x264-GRP"), size(2*gbSize)),
			accepted: true, contains: []string{"Also contains S01E05"}},
		{name: "a double episode with neither wanted", profile: hd, title: pioneer, want: ep(1, 2), release: with(rel("Pioneer.One.S01E04E05.720p.WEB-DL.x264-GRP"), size(2*gbSize)),
			rejection: "Wrong episode: this is S01E04 and S01E05, you need S01E02"},
		{name: "a name with no episode in it", profile: hd, title: pioneer, want: ep(1, 4), release: pioneerEp("Pioneer.One.2010.720p.WEB-DL.x264-GRP"),
			rejection: "No season or episode in the name, so it cannot be matched to your show"},
		{name: "a daily-style name with a date only", profile: hd, title: pioneer, want: ep(1, 4), release: pioneerEp("Pioneer.One.2010.06.19.720p.WEB-DL.x264-GRP"),
			rejection: "Cannot tell which episode this is: the name has no season and episode numbers"},
		{name: "no scope accepts any episode", profile: hd, title: pioneer, release: pioneerEp("Pioneer.One.S02E04.720p.WEB-DL.x264-GRP"), accepted: true},
		{name: "no scope accepts a pack", profile: hd, title: pioneer, release: with(rel("Pioneer.One.S01.720p.WEB-DL.x264-GRP"), size(5*gbSize)), accepted: true},
		{name: "the show's year in the name", profile: hd, title: pioneer, want: ep(1, 4), release: pioneerEp("Pioneer.One.2010.S01E04.720p.WEB-DL.x264-GRP"), accepted: true},
		{name: "another show of the same name", profile: hd, title: pioneer, want: ep(1, 4), release: pioneerEp("Pioneer.One.2019.S01E04.720p.WEB-DL.x264-GRP"),
			rejection: "Wrong year: this is the 2019 version, you want the one from 2010"},
		{name: "a different show", profile: hd, title: pioneer, want: ep(1, 4), release: pioneerEp("Sintel.S01E04.720p.WEB-DL.x264-GRP"),
			rejection: `Wrong title: this is "Sintel", you want "Pioneer One"`},
		{name: "a show's TVmaze id settles it", profile: hd, title: pioneer, want: ep(1, 4),
			release: with(pioneerEp("Something.Else.S01E04.720p.WEB-DL.x264-GRP"), func(r *Release) { r.TVMaze = 99001 }), accepted: true},
		{name: "a show's other TVmaze id does not", profile: hd, title: pioneer, want: ep(1, 4),
			release:   with(pioneerEp("Pioneer.One.S01E04.720p.WEB-DL.x264-GRP"), func(r *Release) { r.TVMaze = 5 }),
			rejection: "Wrong title: the source says this is TVmaze 5, not 99001"},

		// Per-hour size limit: 4 GB an hour of a 47-minute show is about 3.1 GB.
		{name: "an episode over the hourly limit", profile: hd, title: pioneer, want: ep(1, 4), release: with(rel("Pioneer.One.S01E04.1080p.WEB-DL.x264-GRP"), size(6*gbSize)),
			rejection: "Too big: 6.0 GB is over your limit of 3.1 GB for this episode (4 GB an hour)"},
		{name: "an episode under the hourly limit", profile: hd, title: pioneer, want: ep(1, 4), release: with(rel("Pioneer.One.S01E04.1080p.WEB-DL.x264-GRP"), size(3*gbSize)),
			accepted: true},
		{name: "a pack's limit grows with the season", profile: hd, title: pioneer, want: ep(1, 1, 2, 3, 4, 5, 6, 7, 8), release: with(rel("Pioneer.One.S01.1080p.WEB-DL.x264-GRP"), size(20*gbSize)),
			accepted: true},
		{name: "a pack over its limit", profile: hd, title: pioneer, want: ep(1, 1, 2, 3, 4, 5, 6, 7, 8), release: with(rel("Pioneer.One.S01.1080p.WEB-DL.x264-GRP"), size(30*gbSize)),
			rejection: "Too big: 30.0 GB is over your limit of 25.1 GB for a season of 8 episodes (4 GB an hour)"},
		{name: "a double episode gets two episodes of room", profile: hd, title: pioneer, want: ep(1, 4, 5), release: with(rel("Pioneer.One.S01E04E05.1080p.WEB-DL.x264-GRP"), size(5*gbSize)),
			accepted: true},
		{name: "the hourly limit with a runtime the catalog lacks assumes an hour", profile: hd, title: with3(pioneer, func(t *titles.Title) { t.RuntimeMin = 0 }), want: ep(1, 4),
			release: with(rel("Pioneer.One.S01E04.1080p.WEB-DL.x264-GRP"), size(5*gbSize)), rejection: "Too big: 5.0 GB is over your limit of 4 GB for this episode (4 GB an hour)"},
		{name: "a movie profile never sees episodes", profile: movieHD(), title: pioneer, want: ep(1, 4), release: pioneerEp("Pioneer.One.S01E04.720p.WEB-DL.x264-GRP"), accepted: true},
	}
	for _, c := range cases {
		c.run(t)
	}
}

func TestScoreMusic(t *testing.T) {
	p := lossless()
	album := func(name string) Release { return with(rel(name), func(r *Release) { r.Size = 400 * mbSize }) }
	cases := []scoreCase{
		{name: "FLAC", profile: p, title: chopin, release: album("Frederic Chopin - Nocturnes (2011) [FLAC]"), accepted: true,
			contains: []string{"Good fit: FLAC, lossless"}},
		{name: "MP3 320 is allowed", profile: p, title: chopin, release: album("Chopin - Ballades (1998) MP3 320kbps"), accepted: true,
			contains: []string{"Good fit: MP3-320, lossy"}},
		{name: "MP3 128 is not", profile: p, title: chopin, release: album("Chopin - Etudes (1985) MP3 128kbps"),
			rejection: "Format not allowed: MP3-128 (your profile takes FLAC, MP3-320)"},
		{name: "V0 is not on the list", profile: p, title: chopin, release: album("Chopin - Etudes (1985) MP3 V0"),
			rejection: "Format not allowed: MP3-V0 (your profile takes FLAC, MP3-320)"},
		{name: "no format in the name", profile: p, title: chopin, release: album("Chopin - Etudes (1985)"),
			rejection: "Format not stated: the name does not say if it is FLAC or MP3"},
		{name: "another artist", profile: p, title: chopin, release: album("Bach - Cello Suites (2000) [FLAC]"),
			rejection: `Wrong artist: this release is not by "Frédéric Chopin"`},
		{name: "a sloppy spelling of the artist", profile: p, title: chopin, release: album("frederic.chopin-nocturnes-2011-FLAC"), accepted: true},
		{name: "nobody sharing a lossless album", profile: p, title: chopin, release: with(album("Chopin - Etudes (1985) [FLAC]"), func(r *Release) { r.Seeders = 0 }),
			rejection: "No one is sharing it (0 seeders)"},
	}
	for _, c := range cases {
		c.run(t)
	}
}

func TestFMTSizeAndLimitWords(t *testing.T) {
	for in, want := range map[int64]string{
		68*gbSize + gbSize/10 + 1: "68.1 GB", gbSize: "1.0 GB", 850 * mbSize: "850 MB", 100 * 1024: "100 KB",
	} {
		if got := fmtSize(in); got != want {
			t.Errorf("fmtSize(%d) = %q, want %q", in, got, want)
		}
	}
	for in, want := range map[int64]string{60 * gbSize: "60 GB", 5 * gbSize / 2: "2.5 GB", 800 * mbSize: "800 MB"} {
		if got := fmtLimit(in); got != want {
			t.Errorf("fmtLimit(%d) = %q, want %q", in, got, want)
		}
	}
	if got := resList([]int{720, 1080, 2160}); got != "720p, 1080p or 2160p" {
		t.Errorf("resList = %q", got)
	}
}

func TestTitleMatchesIsTheCheapPreCheck(t *testing.T) {
	ok, why := TitleMatches(metropolis, parse.ParseRelease("Metropolis.1927.1080p.BluRay.x264-GRP"), Release{Title: "x"})
	if !ok || why != "" {
		t.Errorf("match = %v %q", ok, why)
	}
	ok, why = TitleMatches(metropolis, parse.ParseRelease("Sintel.2010.1080p.BluRay.x264-GRP"), Release{})
	if ok || !strings.Contains(why, "Wrong title") {
		t.Errorf("mismatch = %v %q", ok, why)
	}
	ok, _ = TitleMatches(chopin, parse.Release{}, Release{Title: "Chopin - Nocturnes (2011) [FLAC]"})
	if !ok {
		t.Error("an album by the artist should match")
	}
}
