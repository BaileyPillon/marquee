package importer

import (
	"fmt"
	"path"
	"strings"
	"unicode"
)

// File and folder names. They are constants for now; the owner will be able to
// change them later. Every name is made safe for Windows (and so for every
// other system): the characters < > : " / \ | ? * are removed, and so are
// trailing dots and spaces, which Windows will not keep.

// maxPart is the longest a single folder or file name may be, in characters.
// Windows allows 255 per name but only 260 for a whole path.
const maxPart = 150

// reservedNames are the device names Windows will not take as a file name.
var reservedNames = map[string]bool{
	"CON": true, "PRN": true, "AUX": true, "NUL": true,
	"COM1": true, "COM2": true, "COM3": true, "COM4": true, "COM5": true, "COM6": true, "COM7": true, "COM8": true, "COM9": true,
	"LPT1": true, "LPT2": true, "LPT3": true, "LPT4": true, "LPT5": true, "LPT6": true, "LPT7": true, "LPT8": true, "LPT9": true,
}

// stripUnsafe removes the characters a name may not have (< > : " / \ | ? * and
// control characters) and turns every run of white space into one space. It
// leaves dots alone: a dot in the middle of a name is fine; finishName deals
// with one at the end.
func stripUnsafe(s string) string {
	var b strings.Builder
	space := false
	for _, r := range s {
		switch {
		case unicode.IsSpace(r):
			space = true
			continue
		case strings.ContainsRune(`<>:"/\|?*`, r), unicode.IsControl(r):
			continue
		}
		if space && b.Len() > 0 {
			b.WriteByte(' ')
		}
		space = false
		b.WriteRune(r)
	}
	return b.String()
}

// finishName turns a cleaned name plus an extension into one path element:
// trailing dots and spaces are trimmed, the name is cut to maxPart characters
// (keeping the extension), an empty name is "_", and a Windows device name
// (CON, NUL, COM1...) gets an underscore added.
func finishName(stem, ext string) string {
	stem = strings.TrimRight(stem, ". ")
	if r := []rune(stem); len(r) > maxPart-len([]rune(ext)) {
		stem = strings.TrimRight(string(r[:max(maxPart-len([]rune(ext)), 1)]), ". ")
	}
	if stem == "" {
		stem = "_"
	}
	first := stem
	if i := strings.IndexByte(first, '.'); i >= 0 {
		first = first[:i]
	}
	if reservedNames[strings.ToUpper(first)] {
		stem += "_"
	}
	return stem + ext
}

// SafeName makes one piece of a file or folder name safe: forbidden characters
// and control characters are removed, runs of spaces become one, and trailing
// dots and spaces are trimmed. A name that comes out empty is "_", and a
// Windows device name (CON, NUL, COM1...) gets an underscore added.
func SafeName(s string) string { return finishName(stripUnsafe(s), "") }

// yearSuffix is " (1968)", or nothing when the year is not known.
func yearSuffix(year int) string {
	if year > 0 {
		return fmt.Sprintf(" (%d)", year)
	}
	return ""
}

// ext returns a file extension as ".mkv": lower case, with one dot, and nothing
// that could change the path.
func ext(e string) string {
	e = strings.ToLower(strings.TrimSpace(e))
	e = strings.TrimLeft(e, ".")
	var b strings.Builder
	for _, r := range e {
		if unicode.IsLetter(r) || unicode.IsDigit(r) {
			b.WriteRune(r)
		}
	}
	if b.Len() == 0 {
		return ""
	}
	return "." + b.String()
}

// qualityTail is " Bluray-1080p", or nothing when the quality is not known.
func qualityTail(q string) string {
	q = stripUnsafe(q)
	if q == "" || q == "Unknown" {
		return ""
	}
	return " " + q
}

// MovieName is what the film's path is made of.
type MovieName struct {
	Name    string
	Year    int
	Quality string // "Bluray-1080p"
	Ext     string // ".mkv" or "mkv"
}

// MoviePath is where a film goes inside its library folder, with "/" between
// the parts:
//
//	<Name> (<Year>)/<Name> (<Year>) <Quality>.<ext>
func MoviePath(n MovieName) string {
	base := stripUnsafe(n.Name) + yearSuffix(n.Year)
	return path.Join(finishName(base, ""), finishName(base+qualityTail(n.Quality), ext(n.Ext)))
}

// EpisodeName is what an episode file's path is made of. Episodes lists the
// episode numbers the file holds (more than one for a double episode), and
// EpisodeNames their names in the same order (any may be empty).
type EpisodeName struct {
	Show         string
	Year         int
	Season       int
	Episodes     []int
	EpisodeNames []string
	Quality      string
	Ext          string
}

// EpisodePath is where an episode goes inside its library folder:
//
//	<Show> (<Year>)/Season <NN>/<Show> - S<NN>E<NN>[-E<NN>] - <Episode Name> <Quality>.<ext>
//
// Season 0 (specials) is "Season 00". A double episode is S01E01-E02; episodes
// that are not next to each other are written one after another, S01E01E03.
func EpisodePath(n EpisodeName) string {
	show := stripUnsafe(n.Show)
	dir := path.Join(finishName(show+yearSuffix(n.Year), ""), fmt.Sprintf("Season %02d", max(n.Season, 0)))
	file := show + " - " + episodeCode(n.Season, n.Episodes)
	if name := joinEpisodeNames(n.EpisodeNames); name != "" {
		file += " - " + name
	}
	return path.Join(dir, finishName(file+qualityTail(n.Quality), ext(n.Ext)))
}

// episodeCode is "S01E04", "S01E01-E02" or "S01E01E03".
func episodeCode(season int, eps []int) string {
	if len(eps) == 0 {
		return fmt.Sprintf("S%02d", max(season, 0))
	}
	code := fmt.Sprintf("S%02dE%02d", max(season, 0), eps[0])
	if len(eps) == 1 {
		return code
	}
	consecutive := true
	for i := 1; i < len(eps); i++ {
		if eps[i] != eps[i-1]+1 {
			consecutive = false
		}
	}
	if consecutive {
		return code + fmt.Sprintf("-E%02d", eps[len(eps)-1])
	}
	for _, e := range eps[1:] {
		code += fmt.Sprintf("E%02d", e)
	}
	return code
}

// joinEpisodeNames joins the names of a double episode with " + ", dropping
// empty and repeated ones, and keeps the result a sensible length.
func joinEpisodeNames(names []string) string {
	var parts []string
	seen := map[string]bool{}
	for _, n := range names {
		n = strings.TrimRight(stripUnsafe(n), ". ")
		if n == "" || seen[n] {
			continue
		}
		seen[n] = true
		parts = append(parts, n)
	}
	out := strings.Join(parts, " + ")
	if r := []rune(out); len(r) > 100 {
		out = strings.TrimRight(string(r[:100]), ". ")
	}
	return out
}

// TrackName is what a music file's path is made of. Disc is 0 when the album
// has one disc; MultiDisc says the album has several, which puts the disc in
// the file name.
type TrackName struct {
	Artist    string
	Album     string
	Year      int
	Disc      int
	MultiDisc bool
	Track     int
	Title     string
	Ext       string
}

// TrackPath is where a track goes inside its library folder:
//
//	<Artist>/<Album> (<Year>)/<NN> - <Track>.<ext>
//
// On an album with several discs the number is <D>-<NN>.
func TrackPath(n TrackName) string {
	num := fmt.Sprintf("%02d", max(n.Track, 0))
	if n.MultiDisc || n.Disc > 1 {
		num = fmt.Sprintf("%d-%s", max(n.Disc, 1), num)
	}
	file := num
	if title := stripUnsafe(n.Title); title != "" {
		file += " - " + title
	}
	return path.Join(SafeName(n.Artist), finishName(stripUnsafe(n.Album)+yearSuffix(n.Year), ""), finishName(file, ext(n.Ext)))
}
