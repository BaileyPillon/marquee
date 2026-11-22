package parse

import "strings"

// CatalogTrack is one track of an album as the catalog lists it. It is what
// MatchTrack matches a file against; the parse package knows nothing else about
// the catalog.
type CatalogTrack struct {
	ID       int64
	Disc     int
	Position int
	Name     string
}

// TrackMatch says how sure MatchTrack is.
type TrackMatch int

const (
	// NoTrack: nothing in the album fits the file.
	NoTrack TrackMatch = iota
	// WeakTrack: the number fits but the title does not (or the other way
	// round, with a number that is no help): probably the track, not certain.
	WeakTrack
	// SureTrack: the title fits, and so does the number when the file has one.
	SureTrack
)

// editionWords may follow an album's name in a release without making it
// another album ("Abbey Road (Remastered)").
var editionWords = map[string]bool{
	"deluxe": true, "edition": true, "expanded": true, "remastered": true, "remaster": true, "remasters": true,
	"anniversary": true, "bonus": true, "tracks": true, "track": true, "version": true, "special": true,
	"limited": true, "collector": true, "collectors": true, "legacy": true, "super": true, "original": true,
	"stereo": true, "mono": true, "reissue": true, "digital": true, "explicit": true, "clean": true,
	"the": true, "and": true, "with": true, "plus": true, "disc": true, "cd": true, "complete": true,
	"classic": true, "ultimate": true, "platinum": true, "gold": true, "japan": true, "japanese": true,
	"uk": true, "us": true, "eu": true, "mix": true, "mixed": true,
}

// AlbumNameMatches reports whether a release's album is the catalog's album:
// the names are the same once folded, or one is the other followed only by
// edition words ("Abbey Road" and "Abbey Road Remastered 2019"). A different
// volume or part ("Nocturnes Vol 2") is another album.
func AlbumNameMatches(catalog, found string) bool {
	a, b := Normalize(catalog), Normalize(found)
	if a == "" || b == "" {
		return false
	}
	if a == b {
		return true
	}
	return onlyEditionWords(a, b) || onlyEditionWords(b, a)
}

// onlyEditionWords reports whether long is short followed by edition words
// (and years).
func onlyEditionWords(short, long string) bool {
	if !strings.HasPrefix(long, short+" ") {
		return false
	}
	for _, w := range strings.Fields(long[len(short)+1:]) {
		if editionWords[w] {
			continue
		}
		if reYearOnly.MatchString(w) {
			continue
		}
		return false
	}
	return true
}

// TitleSimilarity says how alike two track titles are, from 0 (nothing shared)
// to 1 (the same once folded). A title that holds the other as whole words
// ("Nocturne in B-flat minor" and "Nocturne in B-flat minor, Op. 9 No. 1")
// counts as 0.9.
func TitleSimilarity(a, b string) float64 {
	na, nb := Normalize(stripParens(a)), Normalize(stripParens(b))
	if na == "" || nb == "" {
		return 0
	}
	if na == nb {
		return 1
	}
	wa, wb := strings.Fields(na), strings.Fields(nb)
	inA := map[string]bool{}
	for _, w := range wa {
		inA[w] = true
	}
	shared := 0
	seen := map[string]bool{}
	for _, w := range wb {
		if inA[w] && !seen[w] {
			shared++
		}
		seen[w] = true
	}
	union := len(inA) + len(seen) - shared
	score := float64(shared) / float64(union)
	if shared == len(inA) || shared == len(seen) {
		// One title is made of the other's words.
		if short := min(len(inA), len(seen)); short >= 2 {
			score = max(score, 0.9)
		}
	}
	return score
}

// stripParens drops a trailing "(feat. X)" or "[Remastered]" note.
func stripParens(s string) string {
	return strings.TrimSpace(reBrackets.ReplaceAllString(s, " "))
}

// MatchTrack finds which track of an album a file is. disc may be 0 when the
// file does not say, number 0 when it has no number, title "" when it has no
// title. The title decides when it names a track exactly; otherwise the
// position does, if the title does not contradict it.
func MatchTrack(tracks []CatalogTrack, disc, number int, title string) (CatalogTrack, TrackMatch) {
	if len(tracks) == 0 {
		return CatalogTrack{}, NoTrack
	}
	title = strings.TrimSpace(title)

	var byNumber []CatalogTrack
	for _, t := range tracks {
		if number > 0 && t.Position == number && (disc == 0 || t.Disc == 0 || t.Disc == disc) {
			byNumber = append(byNumber, t)
		}
	}
	// With no disc in the name and several discs holding that number, the
	// title has to choose; failing that, the first disc.
	best := func(list []CatalogTrack) (CatalogTrack, bool) {
		if len(list) == 0 {
			return CatalogTrack{}, false
		}
		if len(list) == 1 || title == "" {
			return list[0], len(list) == 1 || disc != 0
		}
		top, topScore := list[0], -1.0
		for _, t := range list {
			if s := TitleSimilarity(t.Name, title); s > topScore {
				top, topScore = t, s
			}
		}
		return top, true
	}

	var exact *CatalogTrack
	if title != "" {
		nt := Normalize(title)
		for i := range tracks {
			if Normalize(tracks[i].Name) == nt {
				// Several tracks with the same title (a hidden reprise): the
				// number picks, else the first.
				if exact == nil || (number > 0 && tracks[i].Position == number) {
					exact = &tracks[i]
				}
			}
		}
	}
	num, numOK := best(byNumber)

	switch {
	case exact != nil:
		// The title names a track exactly. When the number points elsewhere (a
		// release that skips a pregap track shifts every number), the title wins.
		return *exact, SureTrack
	case numOK && (title == "" || TitleSimilarity(num.Name, title) >= 0.5):
		return num, SureTrack
	case numOK:
		return num, WeakTrack
	}
	return CatalogTrack{}, NoTrack
}
