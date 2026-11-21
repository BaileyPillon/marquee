package decide

import (
	"fmt"
	"regexp"
	"slices"
	"strings"
	"time"

	"github.com/BaileyPillon/marquee/internal/grab"
	"github.com/BaileyPillon/marquee/internal/parse"
	"github.com/BaileyPillon/marquee/internal/titles"
)

var (
	reFLAC = regexp.MustCompile(`(?i)(?:^|[^a-z0-9])flac(?:[^a-z0-9]|$)`)
	reALAC = regexp.MustCompile(`(?i)(?:^|[^a-z0-9])alac(?:[^a-z0-9]|$)`)
	reMP3  = regexp.MustCompile(`(?i)(?:^|[^a-z0-9])mp3(?:[^a-z0-9]|$)`)
	reAAC  = regexp.MustCompile(`(?i)(?:^|[^a-z0-9])(?:aac|m4a)(?:[^a-z0-9]|$)`)
	reOpus = regexp.MustCompile(`(?i)(?:^|[^a-z0-9])opus(?:[^a-z0-9]|$)`)
	reV0   = regexp.MustCompile(`(?i)(?:^|[^a-z0-9])v0(?:[^a-z0-9]|$)`)
	reKbps = regexp.MustCompile(`(?i)(?:^|[^0-9])(320|256|224|192|160|128)\s*(?:k|kbps|kb/s)?(?:[^0-9a-z]|$)`)
)

// musicFormat reads the format out of an album release name: "FLAC",
// "MP3-320", "MP3-V0", ... or "" when the name does not say.
func musicFormat(title string) string {
	switch {
	case reFLAC.MatchString(title):
		return "FLAC"
	case reALAC.MatchString(title):
		return "ALAC"
	case reAAC.MatchString(title):
		return "AAC"
	case reOpus.MatchString(title):
		return "Opus"
	case reMP3.MatchString(title) || reV0.MatchString(title) || reKbps.MatchString(title):
		if reV0.MatchString(title) {
			return "MP3-V0"
		}
		if m := reKbps.FindStringSubmatch(title); m != nil {
			return "MP3-" + m[1]
		}
		return "MP3"
	}
	return ""
}

// artistMatches checks that an album release is by the artist: the release
// name, folded to plain lower case, must start with the artist's name or one of
// the other names the catalog knows.
func artistMatches(t titles.Title, release string) (bool, string) {
	got := parse.Normalize(release)
	for _, n := range append([]string{t.Name}, t.AltNames...) {
		name := parse.Normalize(n)
		if name != "" && (got == name || strings.HasPrefix(got, name+" ")) {
			return true, ""
		}
	}
	return false, fmt.Sprintf("Wrong artist: this release is not by %q", t.Name)
}

// scoreMusic is Score for an album release. Music is not searched for yet (it
// arrives with the music milestone); the rules are here so the seeded
// "Lossless" profile already means something.
func scoreMusic(p Profile, t titles.Title, w Want, r Release) Verdict {
	reject := func(msgs ...string) Verdict { return Verdict{Reasons: msgs, Rejection: msgs[0]} }
	if ok, msg := artistMatches(t, r.Title); !ok {
		return reject(msg)
	}
	var bad []string
	format := musicFormat(r.Title)
	switch {
	case format == "":
		bad = append(bad, "Format not stated: the name does not say if it is FLAC or MP3")
	case len(p.AllowedSources) > 0 && !slices.Contains(p.AllowedSources, format):
		bad = append(bad, fmt.Sprintf("Format not allowed: %s (your profile takes %s)", format, strings.Join(p.AllowedSources, ", ")))
	}
	if word, ok := firstWord(p.RejectedWords, r.Title); ok {
		bad = append(bad, fmt.Sprintf("Has a word you rejected: %q", word))
	}
	limit := p.maxBytes(0, 1)
	if r.Size > 0 {
		if limit > 0 && r.Size > limit {
			bad = append(bad, tooBig(p, r.Size, limit, scope{sizeEpisodes: 1}))
		}
		if lo := p.minBytes(); lo > 0 && r.Size < lo {
			bad = append(bad, fmt.Sprintf("Too small: %s is under your %s minimum", fmtSize(r.Size), fmtLimit(lo)))
		}
	}
	torrent := r.Protocol != grab.ProtocolUsenet
	if torrent && r.Seeders >= 0 && r.Seeders < p.MinSeeders {
		if r.Seeders == 0 {
			bad = append(bad, "No one is sharing it (0 seeders)")
		} else {
			bad = append(bad, fmt.Sprintf("Too few people sharing it (%d seeders, your profile wants at least %d)", r.Seeders, p.MinSeeders))
		}
	}
	if len(bad) > 0 {
		return reject(bad...)
	}

	now := w.Now
	if now.IsZero() {
		now = time.Now()
	}
	v := Verdict{Accepted: true, size: r.Size, source: format}
	// The format list is in order of preference.
	v.parts[partRes] = max(9-2*slices.Index(p.AllowedSources, format), 1)
	liked := matchingWords(p.PreferredWords, r.Title)
	v.parts[partWords] = min(len(liked), 9)
	v.parts[partSize] = sizeFit(p, r.Size, limit) / 10
	v.parts[partSeeders] = seedersDigit(r, torrent)
	v.parts[partAge] = ageDigit(now, r.PublishedAt)
	v.Score = 1
	for i, d := range v.parts {
		v.Score += d * partScale[i]
	}
	kind := "lossless"
	if strings.HasPrefix(format, "MP3") || format == "AAC" || format == "Opus" {
		kind = "lossy"
	}
	v.headline = format + ", " + kind
	v.Reasons = []string{"Good fit: " + v.headline}
	if len(liked) > 0 {
		v.Reasons = append(v.Reasons, "Has "+quoteList(liked, "a word", "words")+" you prefer")
	}
	return v
}
