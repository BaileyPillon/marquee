package subtitles

import (
	"strings"
	"testing"

	"github.com/BaileyPillon/marquee/internal/parse"
)

func movieVideo(name string) VideoInfo {
	return VideoInfo{Kind: "movie", Title: "Night of the Living Dead", Year: 1968, FileName: name, Parsed: parse.ParseRelease(name)}
}

func episodeVideo(name string, season, episode int) VideoInfo {
	return VideoInfo{Kind: "show", Title: "Pioneer One", Year: 2010, Season: season, Episode: episode, FileName: name, Parsed: parse.ParseRelease(name)}
}

func TestScore(t *testing.T) {
	const nol = "Night.of.the.Living.Dead.1968.1080p.BluRay.x264-GRP.mkv"
	movie := movieVideo(nol)
	cases := []struct {
		name     string
		video    VideoInfo
		cand     Candidate
		hi       string
		min, max int
		contains string
	}{
		{"the exact file", movie, Candidate{HashMatch: true, Releases: []string{"Something Else Entirely"}}, HIPreferNot, 100, 100, "exact file"},
		{"the exact file, even for the hearing impaired you rather not have", movie, Candidate{HashMatch: true, HI: true}, HIPreferNot, 100, 100, ""},
		{"same group, source, resolution, codec", movie,
			Candidate{Releases: []string{"Night.of.the.Living.Dead.1968.1080p.BluRay.x264-GRP"}}, HIPreferNot, 95, 95, "same release group (GRP)"},
		{"same everything but the group", movie,
			Candidate{Releases: []string{"Night.of.the.Living.Dead.1968.1080p.BluRay.x264-OTHER"}}, HIPreferNot, 75, 75, "Another release group"},
		{"a WEB-DL subtitle for a WEBRip video counts as the same source", episodeVideo("Pioneer.One.S01E02.720p.WEBRip.x264-GRP.mkv", 1, 2),
			Candidate{Releases: []string{"Pioneer.One.S01E02.720p.WEB-DL.x264-GRP"}}, HIPreferNot, 95, 95, "The right episode (S01E02)"},
		{"a DVD subtitle for a BluRay video is not the same source", movie,
			Candidate{Releases: []string{"Night.of.the.Living.Dead.1968.DVDRip.x264-GRP"}}, HIPreferNot, 67, 67, ""},
		{"nothing said about the release, found by id", movie, Candidate{IDMatch: true, Year: 1968}, HIPreferNot, 61 - 2 + 5 - 0, 64, "Found by the video's id"},
		{"nothing said, no year", movie, Candidate{IDMatch: true}, HIPreferNot, 59 + 2, 61, ""},
		{"another film by name", movie, Candidate{Releases: []string{"Dawn.of.the.Dead.1978.1080p.BluRay-GRP"}}, HIPreferNot, 0, 0, `not "Night of the Living Dead"`},
		{"another film by name, but the provider vouches for the id", movie,
			Candidate{IDMatch: true, Releases: []string{"NOTLD.1968.1080p.BluRay-GRP"}}, HIPreferNot, 50, 99, ""},
		{"another year", movie, Candidate{Releases: []string{"Night.of.the.Living.Dead.1990.1080p.BluRay-GRP"}}, HIPreferNot, 0, 0, "1990 film"},
		{"a year one off is fine", movie, Candidate{Releases: []string{"Night.of.the.Living.Dead.1969.1080p.BluRay.x264-GRP"}}, HIPreferNot, 90, 99, ""},
		{"the wrong episode", episodeVideo("Pioneer.One.S01E02.720p.WEB-DL.x264-GRP.mkv", 1, 2),
			Candidate{Releases: []string{"Pioneer.One.S01E03.720p.WEB-DL.x264-GRP"}}, HIPreferNot, 0, 0, "S01E03, not S01E02"},
		{"the wrong season", episodeVideo("Pioneer.One.S01E02.720p.WEB-DL.x264-GRP.mkv", 1, 2),
			Candidate{Releases: []string{"Pioneer.One.S02E02.720p.WEB-DL.x264-GRP"}}, HIPreferNot, 0, 0, "S02E02, not S01E02"},
		{"the episode from the provider's own fields", episodeVideo("Pioneer.One.S01E02.720p.WEB-DL.x264-GRP.mkv", 1, 2),
			Candidate{IDMatch: true, Season: 1, Episode: 2, Releases: []string{"Pioneer One"}}, HIPreferNot, 60, 99, "The right episode"},
		{"the wrong episode from the provider's own fields", episodeVideo("Pioneer.One.S01E02.720p.WEB-DL.x264-GRP.mkv", 1, 2),
			Candidate{IDMatch: true, Season: 1, Episode: 5, Releases: []string{"Pioneer One"}}, HIPreferNot, 0, 0, "not S01E02"},
		{"an episode with no episode stated falls under the default minimum", episodeVideo("Pioneer.One.S01E02.720p.WEB-DL.x264-GRP.mkv", 1, 2),
			Candidate{IDMatch: true}, HIPreferNot, 50, 59, ""},
		{"machine-made", movie, Candidate{Machine: true, IDMatch: true, Releases: []string{"Night.of.the.Living.Dead.1968.1080p.BluRay.x264-GRP"}}, HIPreferNot, 0, 0, "Machine"},
		{"hearing impaired when you prefer not", movie,
			Candidate{HI: true, Releases: []string{"Night.of.the.Living.Dead.1968.1080p.BluRay.x264-GRP"}}, HIPreferNot, 90, 90, "would rather not"},
		{"hearing impaired when you prefer it", movie,
			Candidate{HI: true, Releases: []string{"Night.of.the.Living.Dead.1968.1080p.BluRay.x264-GRP"}}, HIPrefer, 99, 99, "which you prefer"},
		{"not hearing impaired when you prefer it", movie,
			Candidate{Releases: []string{"Night.of.the.Living.Dead.1968.1080p.BluRay.x264-GRP"}}, HIPrefer, 95, 95, ""},
		{"only hearing impaired: the plain one is out", movie,
			Candidate{Releases: []string{"Night.of.the.Living.Dead.1968.1080p.BluRay.x264-GRP"}}, HIOnly, 0, 0, "hearing impaired"},
		{"only hearing impaired: the right one is in", movie,
			Candidate{HI: true, Releases: []string{"Night.of.the.Living.Dead.1968.1080p.BluRay.x264-GRP"}}, HIOnly, 95, 99, ""},
		{"several release names: the best one counts", movie,
			Candidate{Releases: []string{"Night.of.the.Living.Dead.1968.480p.DVDRip-OTHER", "Night.of.the.Living.Dead.1968.1080p.BluRay.x264-GRP"}}, HIPreferNot, 95, 95, ""},
	}
	for _, tc := range cases {
		got, reasons := Score(tc.video, tc.cand, tc.hi)
		if got < tc.min || got > tc.max {
			t.Errorf("%s: score = %d (%q), want %d to %d", tc.name, got, reasons, tc.min, tc.max)
		}
		if tc.contains != "" && !strings.Contains(strings.Join(reasons, " | "), tc.contains) {
			t.Errorf("%s: reasons %q should mention %q", tc.name, reasons, tc.contains)
		}
		if got == 0 && len(reasons) != 1 {
			t.Errorf("%s: a rejected subtitle needs its one reason, got %q", tc.name, reasons)
		}
	}
	// Only the hash reaches 100.
	if s, _ := Score(movie, Candidate{HI: true, Releases: []string{"Night.of.the.Living.Dead.1968.1080p.BluRay.x264-GRP"}}, HIPrefer); s >= 100 {
		t.Errorf("a name match reached %d", s)
	}
}

func TestRankOrdersByScoreThenDownloads(t *testing.T) {
	v := movieVideo("Night.of.the.Living.Dead.1968.1080p.BluRay.x264-GRP.mkv")
	list := []Candidate{
		{Provider: "a", ProviderID: "1", Language: "en", Downloads: 5, Releases: []string{"Night.of.the.Living.Dead.1968.1080p.BluRay.x264-GRP"}},
		{Provider: "a", ProviderID: "2", Language: "en", Downloads: 900, Releases: []string{"Night.of.the.Living.Dead.1968.1080p.BluRay.x264-GRP"}},
		{Provider: "a", ProviderID: "3", Language: "en", Downloads: 99999, Releases: []string{"Night.of.the.Living.Dead.1968.720p.WEB-DL.x264-OTHER"}},
		{Provider: "a", ProviderID: "4", Language: "fr", Downloads: 99999, HashMatch: true},
		{Provider: "b", ProviderID: "5", Language: "en", Downloads: 1, HashMatch: true},
	}
	got := Rank(v, list, []string{"en"}, HIPreferNot)
	var ids []string
	for _, c := range got {
		ids = append(ids, c.ProviderID)
	}
	// The hash match first; the two equal scores by downloads; the weaker one last
	// however popular; the French one is not asked for.
	if strings.Join(ids, ",") != "5,2,1,3" {
		t.Errorf("order = %v, want 5,2,1,3", ids)
	}
	if got[0].Score != 100 || got[1].Score != got[2].Score || got[3].Score >= got[2].Score {
		t.Errorf("scores = %d %d %d %d", got[0].Score, got[1].Score, got[2].Score, got[3].Score)
	}
	// With no language list every language is kept.
	if n := len(Rank(v, list, nil, HIPreferNot)); n != 5 {
		t.Errorf("no language filter: %d results", n)
	}
}

func TestConfigNormalizeAndDefaults(t *testing.T) {
	c := DefaultConfig()
	if len(c.Languages) != 1 || c.Languages[0] != "en" || !c.Providers.Podnapisi.Enabled || c.Providers.OpenSubtitles.Enabled ||
		c.HearingImpaired != HIPreferNot || c.MinScore != 60 || !c.SearchAfterImport || c.RescanEveryHours != 24 {
		t.Errorf("defaults = %+v", c)
	}
	c.Languages = []string{" EN ", "pt_BR", "eng", "xx-", "", "fr", "fr", "not a code", "deu"}
	c.HearingImpaired = "sometimes"
	c.MinScore = 250
	c.RescanEveryHours = -3
	c.Providers.OpenSubtitles.APIKey = "  key  "
	n := c.normalize()
	if strings.Join(n.Languages, ",") != "en,pt-br,fr,de" || n.HearingImpaired != HIPreferNot || n.MinScore != 100 || n.RescanEveryHours != 24 ||
		n.Providers.OpenSubtitles.APIKey != "key" {
		t.Errorf("normalized = %+v", n)
	}
	var many Config
	for i := 0; i < 30; i++ {
		many.Languages = append(many.Languages, string(rune('a'+i%26))+string(rune('a'+i/26)))
	}
	if got := many.normalize().Languages; len(got) != maxLanguages {
		t.Errorf("%d languages kept, want %d", len(got), maxLanguages)
	}
}
