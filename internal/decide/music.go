package decide

import (
	"fmt"
	"regexp"
	"slices"
	"strconv"
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

// artistRest checks that an album release is by the artist: the release name,
// folded to plain lower case, must start with the artist's name or one of the
// other names the catalog knows. It returns what follows the name (folded).
func artistRest(t titles.Title, release string) (rest string, ok bool) {
	got := parse.Normalize(release)
	for _, n := range append([]string{t.Name}, t.AltNames...) {
		name := parse.Normalize(n)
		switch {
		case name == "":
		case got == name:
			return "", true
		case strings.HasPrefix(got, name+" "):
			return got[len(name)+1:], true
		}
	}
	return "", false
}

func artistMatches(t titles.Title, release string) (bool, string) {
	if _, ok := artistRest(t, release); ok {
		return true, ""
	}
	return false, fmt.Sprintf("Wrong artist: this release is not by %q", t.Name)
}

// artistIs reports whether a name is the artist's (or one of the other names).
func artistIs(t titles.Title, name string) bool {
	n := parse.Normalize(name)
	if n == "" {
		return false
	}
	for _, c := range append([]string{t.Name}, t.AltNames...) {
		if parse.Normalize(c) == n {
			return true
		}
	}
	return false
}

// otherAlbumWords, straight after an album's name, say it is another album
// ("Nocturnes Vol 2", "Nocturnes Live").
var otherAlbumWords = map[string]bool{
	"vol": true, "volume": true, "part": true, "pt": true, "ii": true, "iii": true, "iv": true, "vi": true,
	"live": true, "unplugged": true, "remixes": true, "remix": true, "acoustic": true, "instrumental": true,
	"sessions": true, "singles": true, "ep": true, "demos": true,
}

// albumMismatch is "" when the release is the album asked for, else the reason
// it is not. The release's album has to be the album's name, possibly followed
// by notes (an edition, a format, a year): anything after the name is let
// through unless it starts another title (a volume, a part, "Live").
func albumMismatch(t titles.Title, album, release string, pr parse.MusicRelease) string {
	if pr.Discography {
		return fmt.Sprintf("This is a pack of many albums, not just %q", album)
	}
	want := parse.Normalize(album)
	if want == "" {
		return ""
	}
	// What the release calls the album: the parsed album when the name split
	// cleanly after the artist's, else everything after the artist's name.
	found, label := "", ""
	if pr.Album != "" && artistIs(t, pr.Artist) {
		found, label = parse.Normalize(pr.Album), pr.Album
	} else {
		found, _ = artistRest(t, release)
	}
	var after []string
	switch {
	case found == want:
	case strings.HasPrefix(found, want+" "):
		after = strings.Fields(found[len(want)+1:])
	case label == "":
		padded := " " + found + " "
		i := strings.Index(padded, " "+want+" ")
		if i < 0 {
			return fmt.Sprintf("Wrong album: the name does not say %q", album)
		}
		after = strings.Fields(padded[i+len(want)+2:])
	default:
		return fmt.Sprintf("Wrong album: this is %q, not %q", label, album)
	}
	if len(after) > 0 && startsAnotherTitle(want, after[0]) {
		if label != "" {
			return fmt.Sprintf("Wrong album: this is %q, not %q", label, album)
		}
		return fmt.Sprintf("Wrong album: this looks like another one named %q", strings.TrimSpace(want+" "+after[0]))
	}
	return ""
}

// startsAnotherTitle reports whether a word straight after an album's name
// makes it another album: a volume or part, "Live", or a small number.
func startsAnotherTitle(want, next string) bool {
	if strings.Contains(" "+want+" ", " "+next+" ") {
		return false
	}
	if otherAlbumWords[next] {
		return true
	}
	if !isDigits(next) {
		return false
	}
	// A year (1998) is a note on the release; any other number is a volume.
	return !(len(next) == 4 && (strings.HasPrefix(next, "19") || strings.HasPrefix(next, "20")))
}
func isDigits(s string) bool {
	if s == "" {
		return false
	}
	for _, r := range s {
		if r < '0' || r > '9' {
			return false
		}
	}
	return true
}

// lossyKbps is the bitrate of an "MP3-320" style format name, 0 for anything
// else (lossless, V0, a name without a number).
func lossyKbps(format string) int {
	n, ok := strings.CutPrefix(format, "MP3-")
	if !ok {
		return 0
	}
	k, err := strconv.Atoi(n)
	if err != nil {
		return 0
	}
	return k
}

const (
	// Size sanity per track (the album's track count is known): under the floor
	// it is a stub or a fake, over the ceiling it is not this album.
	losslessMinPerTrack = 5 << 20
	losslessMaxPerTrack = 400 << 20
	lossyMinPerTrack    = 3 << 19 // 1.5 MiB
	lossyMaxPerTrack    = 80 << 20

	// minLossyKbps is the floor for lossy music: under it the release is
	// rejected unless the owner's profile lists the format on purpose.
	minLossyKbps = 192
)

// trackSizeProblem is "" when the size fits the number of tracks.
func trackSizeProblem(size int64, tracks int, lossless bool) string {
	if size <= 0 || tracks <= 0 {
		return ""
	}
	per := size / int64(tracks)
	lo, hi := int64(lossyMinPerTrack), int64(lossyMaxPerTrack)
	kind := "lossy music"
	if lossless {
		lo, hi, kind = losslessMinPerTrack, losslessMaxPerTrack, "lossless music"
	}
	switch {
	case per < lo:
		return fmt.Sprintf("Too small for %d tracks: %s is about %s a track, too little for %s", tracks, fmtSize(size), fmtSize(per), kind)
	case per > hi:
		return fmt.Sprintf("Too big for %d tracks: %s is about %s a track, more than one album holds", tracks, fmtSize(size), fmtSize(per))
	}
	return ""
}

// scoreMusic is Score for an album release. With a Want that names the album,
// the release must be that album (an edition note is fine, another volume is
// not) and a pack of many albums is turned down; without one, only the artist
// is checked. FLAC ranks first and the profile's format list sets the rest.
func scoreMusic(p Profile, t titles.Title, w Want, r Release) Verdict {
	reject := func(msgs ...string) Verdict { return Verdict{Reasons: msgs, Rejection: msgs[0]} }
	if ok, msg := artistMatches(t, r.Title); !ok {
		return reject(msg)
	}
	pr := parse.ParseMusicRelease(r.Title)
	var bad []string
	if w.Album != "" {
		if msg := albumMismatch(t, w.Album, r.Title, pr); msg != "" {
			return reject(msg)
		}
	}
	format := musicFormat(r.Title)
	switch {
	case format == "":
		bad = append(bad, "Format not stated: the name does not say if it is FLAC or MP3")
	case lossyKbps(format) > 0 && lossyKbps(format) < minLossyKbps && !slices.Contains(p.AllowedSources, format):
		bad = append(bad, fmt.Sprintf("Too low a quality: %s is lossy and under %d kbps", format, minLossyKbps))
	case len(p.AllowedSources) > 0 && !slices.Contains(p.AllowedSources, format):
		bad = append(bad, fmt.Sprintf("Format not allowed: %s (your profile takes %s)", format, strings.Join(p.AllowedSources, ", ")))
	}
	if word, ok := firstWord(p.RejectedWords, r.Title); ok {
		bad = append(bad, fmt.Sprintf("Has a word you rejected: %q", word))
	}
	lossless := format == "FLAC" || format == "ALAC"
	if msg := trackSizeProblem(r.Size, w.Tracks, lossless); msg != "" && format != "" {
		bad = append(bad, msg)
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
	v.parts[partRes] = min(max(9-2*slices.Index(p.AllowedSources, format), 1), 9)
	if w.AlbumYear > 0 && pr.Year == w.AlbumYear {
		v.parts[partSource] = 1 // the original year, not a reissue
	}
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
	if w.Album != "" {
		v.Reasons = append(v.Reasons, fmt.Sprintf("It is the album %q", w.Album))
	}
	if len(liked) > 0 {
		v.Reasons = append(v.Reasons, "Has "+quoteList(liked, "a word", "words")+" you prefer")
	}
	return v
}
