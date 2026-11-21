package decide

import (
	"fmt"
	"slices"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/BaileyPillon/marquee/internal/parse"
)

// Kinds a profile is for (the same words the catalog uses).
const (
	KindMovie  = "movie"
	KindShow   = "show"
	KindArtist = "artist"
)

// Profile says what a good release looks like for one kind and quality. There
// is exactly one profile per kind and quality; the owner edits them.
type Profile struct {
	ID      int64  `json:"id"`
	Name    string `json:"name"`
	Kind    string `json:"kind"`    // KindMovie | KindShow | KindArtist
	Quality string `json:"quality"` // grab.QualityHD | grab.QualityUHD

	// Video: the resolutions accepted, and the one that ranks first.
	AllowedRes   []int `json:"allowedRes"`
	PreferredRes int   `json:"preferredRes"`

	// Video: the sources accepted (parse.Source names; empty means any).
	// Music: the formats accepted, best first (FLAC, MP3-320, ...).
	AllowedSources []string `json:"allowedSources"`

	MinSizeMB        *int `json:"minSizeMb"`
	MaxSizeMB        *int `json:"maxSizeMb"`
	MaxSizePerHourMB *int `json:"maxSizePerHourMb"` // episodes: against the show's runtime

	PreferHDR      bool     `json:"preferHdr"`
	PreferredWords []string `json:"preferredWords"`
	RejectedWords  []string `json:"rejectedWords"`
	MinSeeders     int      `json:"minSeeders"`

	// UpgradeUntil is the quality name (for example "Bluray-1080p") up to which
	// a better release replaces a copy already in the library. Nil means never
	// upgrade. Picking does not use it yet; the importer will.
	UpgradeUntil *string `json:"upgradeUntil"`

	CreatedAt time.Time `json:"createdAt"`
}

// Resolutions the parser can report, in order.
var resolutionLadder = []int{480, 576, 720, 1080, 2160}

// videoSources are the names allowed_sources may hold for movies and shows.
var videoSources = []string{
	string(parse.SourceRemux), string(parse.SourceBluray), string(parse.SourceWEBDL), string(parse.SourceWEBRip),
	string(parse.SourceHDTV), string(parse.SourceSDTV), string(parse.SourceDVD), string(parse.SourceRawHD), "Unknown",
}

// musicFormats are the names allowed_sources may hold for music.
var musicFormats = []string{"FLAC", "MP3-320", "MP3-V0", "MP3-256", "MP3-192", "MP3-128", "AAC", "Opus"}

const (
	maxWords   = 50
	maxWordLen = 40
)

// Validate checks a profile before it is stored and tidies its lists. Kind and
// quality are not checked here: they never change after the profile exists.
func (p *Profile) Validate() error {
	p.Name = strings.TrimSpace(p.Name)
	if p.Name == "" {
		return invalid("Give the profile a name.")
	}
	if utf8.RuneCountInString(p.Name) > 60 {
		return invalid("That name is too long.")
	}
	if p.Kind == KindArtist {
		p.AllowedRes, p.PreferredRes = []int{}, 0
		var err error
		if p.AllowedSources, err = cleanChoices(p.AllowedSources, musicFormats, "format"); err != nil {
			return err
		}
		if len(p.AllowedSources) == 0 {
			return invalid("Pick at least one music format.")
		}
	} else {
		res := slices.Clone(p.AllowedRes)
		slices.Sort(res)
		res = slices.Compact(res)
		if len(res) == 0 {
			return invalid("Pick at least one resolution.")
		}
		for _, r := range res {
			if !slices.Contains(resolutionLadder, r) {
				return invalid(fmt.Sprintf("%dp is not a resolution Marquee knows (480, 576, 720, 1080 or 2160).", r))
			}
		}
		p.AllowedRes = res
		if p.PreferredRes != 0 && !slices.Contains(res, p.PreferredRes) {
			return invalid("The preferred resolution must be one of the allowed ones.")
		}
		var err error
		if p.AllowedSources, err = cleanChoices(p.AllowedSources, videoSources, "source"); err != nil {
			return err
		}
	}
	for _, f := range []struct {
		label string
		v     *int
	}{{"minimum size", p.MinSizeMB}, {"maximum size", p.MaxSizeMB}, {"size per hour", p.MaxSizePerHourMB}} {
		if f.v != nil && (*f.v < 0 || *f.v > 10_000_000) {
			return invalid("The " + f.label + " must be a sensible number of megabytes.")
		}
	}
	if p.MinSizeMB != nil && p.MaxSizeMB != nil && *p.MaxSizeMB > 0 && *p.MinSizeMB > *p.MaxSizeMB {
		return invalid("The minimum size is bigger than the maximum size.")
	}
	if p.MinSeeders < 0 || p.MinSeeders > 1000 {
		return invalid("The fewest seeders must be a number from 0 to 1000.")
	}
	var err error
	if p.PreferredWords, err = cleanWords(p.PreferredWords); err != nil {
		return err
	}
	if p.RejectedWords, err = cleanWords(p.RejectedWords); err != nil {
		return err
	}
	if p.UpgradeUntil != nil {
		v := strings.TrimSpace(*p.UpgradeUntil)
		if v == "" {
			p.UpgradeUntil = nil
		} else {
			p.UpgradeUntil = &v
		}
	}
	return nil
}

func cleanChoices(have, allowed []string, what string) ([]string, error) {
	out := []string{}
	for _, v := range have {
		v = strings.TrimSpace(v)
		var match string
		for _, a := range allowed {
			if strings.EqualFold(a, v) {
				match = a
			}
		}
		if match == "" {
			return nil, invalid(fmt.Sprintf("%q is not a %s Marquee knows.", v, what))
		}
		if !slices.Contains(out, match) {
			out = append(out, match)
		}
	}
	return out, nil
}

func cleanWords(in []string) ([]string, error) {
	out := []string{}
	for _, w := range in {
		w = strings.TrimSpace(w)
		if w == "" {
			continue
		}
		if utf8.RuneCountInString(w) > maxWordLen {
			return nil, invalid("A word in the lists is too long.")
		}
		if !slices.ContainsFunc(out, func(o string) bool { return strings.EqualFold(o, w) }) {
			out = append(out, w)
		}
	}
	if len(out) > maxWords {
		return nil, invalid(fmt.Sprintf("At most %d words in a list.", maxWords))
	}
	return out, nil
}

// maxBytes is the biggest size the profile allows for one release of a title
// with the given runtime in minutes (0: unknown, taken as an hour) that covers
// the given number of episodes, or 0 for no limit.
func (p Profile) maxBytes(runtimeMin, episodes int) int64 {
	var limit int64
	if p.MaxSizeMB != nil && *p.MaxSizeMB > 0 {
		limit = int64(*p.MaxSizeMB) * mb
	}
	if p.MaxSizePerHourMB != nil && *p.MaxSizePerHourMB > 0 {
		if runtimeMin <= 0 {
			runtimeMin = 60
		}
		per := int64(*p.MaxSizePerHourMB) * mb * int64(runtimeMin) / 60
		per *= int64(max(episodes, 1))
		if limit == 0 || per < limit {
			limit = per
		}
	}
	return limit
}

func (p Profile) minBytes() int64 {
	if p.MinSizeMB != nil && *p.MinSizeMB > 0 {
		return int64(*p.MinSizeMB) * mb
	}
	return 0
}

const mb = 1 << 20
