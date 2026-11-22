package subtitles

import (
	"fmt"
	"slices"
	"sort"
	"strings"

	"github.com/BaileyPillon/marquee/internal/parse"
)

// How a subtitle is scored, from 0 (not this video at all) to 100 (the exact
// file). The points are:
//
//	the exact file, by its hash                        100, and nothing else is looked at
//	the right film (30) or show (15)                   30 or 15
//	a film's year                                       5 (not stated: 2)
//	an episode: the right one                          20 (not stated: 10; the wrong one: 0 for all)
//	the same release group as the video                20 (not stated on one side: 10)
//	the same source: BluRay, WEB, TV, DVD              20 (not stated: 10)
//	the same resolution                                15 (not stated: 7)
//	the same video codec                                5 (not stated: 2)
//
// A subtitle for another film, another episode, or one that is machine-made is
// 0. The hearing-impaired preference adds or takes off 5, and only a hash match
// reaches 100. With the default minimum of 60, a film's subtitle that the
// provider matched by id and that says nothing about its release scores 61 and
// is taken; an episode's needs its episode number stated (64); one that names a
// different group, source and resolution scores 37 and is not.
//
// A video the importer renamed has no group in its name, so the group counts
// for 10 either way and the rest decides: for a film that agrees on title, year,
// source and resolution that is 82.
const (
	pointsTitle      = 30
	pointsYear       = 5
	pointsShowTitle  = 15
	pointsEpisode    = 20
	pointsGroup      = 20
	pointsSource     = 20
	pointsResolution = 15
	pointsCodec      = 5
	pointsHashMatch  = 100
	pointsHINudge    = 5
	maxScoreNoHash   = 99
)

// Score rates how well a subtitle fits a video. hiPref is the owner's
// hearing-impaired preference. The reasons are short plain-words lines.
func Score(v VideoInfo, c Candidate, hiPref string) (int, []string) {
	if c.Machine {
		return 0, []string{"Machine-made or machine-translated"}
	}
	if hiPref == HIOnly && !c.HI {
		return 0, []string{"Not for the hearing impaired, which is all you take"}
	}
	if c.HashMatch {
		return pointsHashMatch, []string{"It is made for this exact file (the hashes match)"}
	}

	names := c.Releases
	if len(names) == 0 {
		names = []string{""}
	}
	best, bestReasons := 0, []string(nil)
	var rejection string
	for _, name := range names {
		score, reasons, reject := scoreName(v, c, name)
		if reject != "" {
			rejection = reject
			continue
		}
		if score > best {
			best, bestReasons = score, reasons
		}
	}
	if best == 0 {
		if rejection == "" {
			rejection = "It does not look like this video's subtitle"
		}
		return 0, []string{rejection}
	}
	switch {
	case hiPref == HIPrefer && c.HI:
		best += pointsHINudge
		bestReasons = append(bestReasons, "For the hearing impaired, which you prefer")
	case hiPref == HIPreferNot && c.HI:
		best -= pointsHINudge
		bestReasons = append(bestReasons, "For the hearing impaired, which you would rather not")
	}
	return min(max(best, 1), maxScoreNoHash), bestReasons
}

// scoreName scores one release name an uploader gave. A non-empty rejection
// says the subtitle is for something else.
func scoreName(v VideoInfo, c Candidate, name string) (score int, reasons []string, rejection string) {
	var r parse.Release
	if name != "" {
		r = parse.ParseRelease(name)
	}
	titleOK := name != "" && r.Title != "" && sameTitle(v.Title, r.Title)

	// Which film or show. The provider's own id match vouches for a release name
	// that spells the title differently ("NOTLD"), but not for one that names
	// another title and another year as well.
	if titleBad := name != "" && r.Title != "" && !titleOK; titleBad {
		yearBad := !v.IsEpisode() && r.Year != 0 && v.Year != 0 && abs(r.Year-v.Year) > 1
		if !c.IDMatch || yearBad {
			return 0, nil, fmt.Sprintf("It is for %q, not %q", r.Title, v.Title)
		}
	}
	if v.IsEpisode() {
		score += pointsShowTitle
		season, episodes := r.Season, r.Episodes
		if len(episodes) == 0 && c.Episode > 0 {
			season, episodes = c.Season, []int{c.Episode}
		}
		switch {
		case len(episodes) == 0:
			score += pointsEpisode / 2
		case season == v.Season && slices.Contains(episodes, v.Episode):
			score += pointsEpisode
			reasons = append(reasons, fmt.Sprintf("The right episode (S%02dE%02d)", v.Season, v.Episode))
		default:
			return 0, nil, fmt.Sprintf("It is for S%02dE%02d, not S%02dE%02d", season, episodes[0], v.Season, v.Episode)
		}
	} else {
		score += pointsTitle
		year := r.Year
		if year == 0 {
			year = c.Year
		}
		switch {
		case year == 0 || v.Year == 0:
			score += pointsYear / 2
		case year == v.Year:
			score += pointsYear
		case abs(year-v.Year) > 1 && !c.IDMatch:
			return 0, nil, fmt.Sprintf("It is for the %d film, not the %d one", year, v.Year)
		}
	}

	// How the video was made.
	score += half(pointsGroup, v.Parsed.Group != "" && r.Group != "", strings.EqualFold(v.Parsed.Group, r.Group))
	if v.Parsed.Group != "" && r.Group != "" {
		if strings.EqualFold(v.Parsed.Group, r.Group) {
			reasons = append(reasons, "The same release group ("+r.Group+")")
		} else {
			reasons = append(reasons, "Another release group ("+r.Group+")")
		}
	}
	srcStated := v.Parsed.Source != parse.SourceUnknown && r.Source != parse.SourceUnknown
	score += half(pointsSource, srcStated, sourceFamily(v.Parsed.Source) == sourceFamily(r.Source))
	if srcStated && sourceFamily(v.Parsed.Source) == sourceFamily(r.Source) {
		reasons = append(reasons, "The same kind of source ("+string(r.Source)+")")
	}
	resStated := v.Parsed.Resolution > 0 && r.Resolution > 0
	score += half(pointsResolution, resStated, v.Parsed.Resolution == r.Resolution)
	if resStated && v.Parsed.Resolution == r.Resolution {
		reasons = append(reasons, fmt.Sprintf("The same resolution (%dp)", r.Resolution))
	}
	codecStated := v.Parsed.Codec != "" && r.Codec != ""
	switch {
	case codecStated && v.Parsed.Codec == r.Codec:
		score += pointsCodec
	case !codecStated:
		score += pointsCodec * 2 / 5
	}
	if c.IDMatch && len(reasons) == 0 {
		reasons = append(reasons, "Found by the video's id")
	}
	return score, reasons, ""
}

// half is the points of a field: all when both sides state it and agree, none
// when both state it and differ, and half when one side does not say.
func half(points int, stated, same bool) int {
	switch {
	case !stated:
		return points / 2
	case same:
		return points
	}
	return 0
}

func abs(n int) int {
	if n < 0 {
		return -n
	}
	return n
}

// sameTitle reports whether a release's title is the video's title, folded.
func sameTitle(want, got string) bool {
	w, g := parse.Normalize(want), parse.Normalize(got)
	if w == "" || g == "" {
		return false
	}
	return w == g
}

// sourceFamily groups the sources that time alike: a WEB-DL subtitle fits a
// WEBRip, and a BluRay one fits a Remux.
func sourceFamily(s parse.Source) string {
	switch s {
	case parse.SourceWEBDL, parse.SourceWEBRip:
		return "web"
	case parse.SourceBluray, parse.SourceRemux:
		return "disc"
	case parse.SourceHDTV, parse.SourceSDTV, parse.SourceRawHD:
		return "tv"
	case parse.SourceDVD:
		return "dvd"
	}
	return string(s)
}

// Rank scores every candidate and sorts them best first: by score, then by how
// many times it has been downloaded (the tiebreak), then by name so the order
// is stable. Candidates for a language that was not asked for are dropped.
func Rank(v VideoInfo, list []Candidate, languages []string, hiPref string) []Candidate {
	out := make([]Candidate, 0, len(list))
	for _, c := range list {
		if len(languages) > 0 && !slices.Contains(languages, c.Language) {
			continue
		}
		c.Score, c.Reasons = Score(v, c, hiPref)
		out = append(out, c)
	}
	sort.SliceStable(out, func(i, j int) bool {
		a, b := out[i], out[j]
		switch {
		case a.Score != b.Score:
			return a.Score > b.Score
		case a.Downloads != b.Downloads:
			return a.Downloads > b.Downloads
		case a.Provider != b.Provider:
			return a.Provider < b.Provider
		}
		return a.ProviderID < b.ProviderID
	})
	return out
}
