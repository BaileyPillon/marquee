package parse

import (
	"regexp"
	"strings"
)

// ParseMusicRelease reads the name of an album release, as a torrent or an NZB
// is called, not the path of one track (that is ParseMusic). It copes with
//
//	Artist - Album (2012) [FLAC 24bit]
//	Artist - Album [2012, MP3, 320 kbps]
//	Artist - 2012 - Album [WEB FLAC]
//	Artist - Album - 2012 - FLAC
//	Artist-Album-CD-FLAC-2012-GRP            (the scene way: "_" is a space, "-" splits)
//
// and gives the artist, the album (an edition note such as "(Deluxe Edition)"
// stays with it), the year, the format and bitrate ("FLAC", "24bit"; "MP3",
// "320kbps"), the source (CD, WEB, Vinyl) and the scene group. Discography is
// set for a pack of many albums. Anything the name does not say is left empty.
func ParseMusicRelease(name string) MusicRelease {
	var m MusicRelease
	s := strings.TrimSpace(name)
	if s == "" {
		return m
	}
	base, _ := splitPath(s)
	s = base
	ext := ""
	if g := reMusicExt.FindStringSubmatch(s); g != nil {
		s = s[:len(s)-len(g[0])]
		ext = strings.ToLower(g[1])
	}
	m.Discography = reDiscography.MatchString(s)
	if !parseSceneMusic(s, &m) {
		parseLooseMusic(s, &m)
	}
	if m.Format == "" && ext != "" {
		m.Format = musicFormat[ext]
	}
	return m
}

var (
	reMusicExt = regexp.MustCompile(`(?i)\.(nzb|torrent|zip|rar|7z|flac|mp3|m4a|opus|ogg|wav)$`)
	// A pack of albums: "Discography", "Complete Studio Albums", or a span of years.
	reDiscography = regexp.MustCompile(`(?i)\bdiscograph(?:y|ie)\b|\bcomplete (?:studio )?(?:albums|collection|works|recordings)\b|\b(?:19|20)\d\d\s*[-–]\s*(?:19|20)\d\d\b`)

	reSceneGroup   = regexp.MustCompile(`^[A-Za-z0-9_]{2,}$`)
	reCatalogParen = regexp.MustCompile(`^\([A-Za-z]{1,8}[-_.]?\d{1,6}[A-Za-z]?\)$`)
	reDashSplit    = regexp.MustCompile(`\s+[-–—]\s+`)
	reTagPieces    = regexp.MustCompile(`[,;/|]`)
	reMusicToken   = regexp.MustCompile(`\d+(?:\.\d+)?[a-z]*|[a-z][a-z0-9]*`)
	reKbpsToken    = regexp.MustCompile(`^(\d{3})(?:k|kbps|kb)$`)
	reBitToken     = regexp.MustCompile(`^(16|24|32)bits?$`)
	reRateToken    = regexp.MustCompile(`^\d+(?:\.\d+)?(?:khz|hz)$`)
	reDiscCount    = regexp.MustCompile(`^\d{1,2}cd$`)
)

// tagSources are the words that say where the audio came from.
var tagSources = map[string]string{
	"cd": "CD", "cdda": "CD", "hdcd": "CD", "web": "WEB", "vinyl": "Vinyl", "lp": "Vinyl",
	"sacd": "SACD", "cassette": "Cassette", "dvda": "DVD-A",
}

// plainTagWords are words that are tags but say nothing we keep.
var plainTagWords = map[string]bool{
	"lossless": true, "cbr": true, "vbr": true, "kbps": true, "bit": true, "bits": true,
	"hires": true, "hi": true, "res": true, "khz": true, "hz": true,
}

type musicTags struct{ format, bitrate, source string }

// readMusicTags reads the text of a bracket, or the end of a name, as quality
// tags. It is a tag group only when every word in it is a known tag and at
// least one says something real (a format, a source, a unit): a bare "320" or
// "24" at the end of an album name is not a tag.
func readMusicTags(text string) (musicTags, bool) {
	toks := reMusicToken.FindAllString(strings.ToLower(text), -1)
	if len(toks) == 0 {
		return musicTags{}, false
	}
	var (
		t       musicTags
		strong  bool
		depth   string
		numbers []string
	)
	for _, tok := range toks {
		switch {
		case tagFormats[tok] != "":
			t.format, strong = tagFormats[tok], true
		case tagSources[tok] != "":
			t.source, strong = tagSources[tok], true
		case reDiscCount.MatchString(tok):
			t.source, strong = "CD", true
		case plainTagWords[tok]:
			strong = true
		case reBitToken.MatchString(tok):
			depth, strong = reBitToken.FindStringSubmatch(tok)[1], true
		case reRateToken.MatchString(tok):
			strong = true
		case reKbpsToken.MatchString(tok):
			n := reKbpsToken.FindStringSubmatch(tok)[1]
			if !bitrates[n] {
				return musicTags{}, false
			}
			t.bitrate, strong = n+"kbps", true
		case len(tok) == 2 && tok[0] == 'v' && tok[1] >= '0' && tok[1] <= '2':
			t.bitrate, strong = strings.ToUpper(tok), true
		case isPlainNumber(tok):
			numbers = append(numbers, tok)
		default:
			return musicTags{}, false
		}
	}
	for _, n := range numbers {
		switch {
		case n == "16" || n == "24" || n == "32":
			depth = n
		case sampleRates[n] && depth != "":
			// the 96 of "24-96": a rate, not a bitrate
		case bitrates[n] && t.bitrate == "":
			t.bitrate = n + "kbps"
		case sampleRates[n]:
			// a sample rate by itself
		default:
			return musicTags{}, false
		}
	}
	if depth == "24" || depth == "32" {
		t.bitrate = depth + "bit"
	}
	if !strong {
		return musicTags{}, false
	}
	return t, true
}

func isPlainNumber(s string) bool {
	if s == "" {
		return false
	}
	for _, r := range s {
		if (r < '0' || r > '9') && r != '.' {
			return false
		}
	}
	return true
}

func (m *MusicRelease) applyTags(t musicTags) {
	if t.format != "" {
		m.Format = t.format
	}
	if t.bitrate != "" {
		m.Bitrate = t.bitrate
	}
	if t.source != "" {
		m.Source = t.source
	}
}

// yearOnly reports whether s is exactly a year from 1900 to 2099.
func yearOnly(s string) (int, bool) {
	s = strings.TrimSpace(s)
	if reYearOnly.MatchString(s) {
		return atoi(s), true
	}
	return 0, false
}

// yearOrTags reports whether a piece of a name is a year or a group of quality tags.
func yearOrTags(s string) bool {
	if _, ok := yearOnly(s); ok {
		return true
	}
	_, ok := readMusicTags(strings.ReplaceAll(s, "_", " "))
	return ok
}

// parseSceneMusic reads "Artist-Album-CD-FLAC-2012-GRP". It reports false for a
// name that is not written that way.
func parseSceneMusic(s string, m *MusicRelease) bool {
	if strings.ContainsAny(s, " \t") {
		return false
	}
	parts := strings.Split(s, "-")
	if len(parts) < 3 {
		return false
	}
	// The last piece is the scene group unless it is itself a year or a tag
	// ("Artist-Album-2012-FLAC" has no group).
	group := ""
	mid := parts[1:]
	if last := parts[len(parts)-1]; reSceneGroup.MatchString(last) && !yearOrTags(last) {
		group, mid = last, parts[1:len(parts)-1]
	}
	if len(mid) < 2 {
		return false
	}
	known := false
	for _, p := range mid {
		if _, ok := yearOnly(p); ok {
			known = true
		} else if _, ok := readMusicTags(strings.ReplaceAll(p, "_", " ")); ok {
			known = true
		}
	}
	if !known {
		return false
	}
	m.Group = group
	m.Artist = foldSpace(strings.ReplaceAll(parts[0], "_", " "))
	var album []string
	stopped := false
	for _, p := range mid {
		q := foldSpace(strings.ReplaceAll(p, "_", " "))
		if y, ok := yearOnly(q); ok {
			if m.Year == 0 {
				m.Year = y
			}
			stopped = true
			continue
		}
		if t, ok := readMusicTags(q); ok {
			m.applyTags(t)
			stopped = true
			continue
		}
		if stopped && bitrates[q] && m.Bitrate == "" && m.Format != "FLAC" && m.Format != "ALAC" {
			m.Bitrate = q + "kbps" // "MP3-320-2012": the bitrate after the format
			continue
		}
		if stopped || reCatalogParen.MatchString(p) || q == "" {
			continue
		}
		album = append(album, q)
	}
	m.Album = strings.Join(album, " - ")
	return true
}

// parseLooseMusic reads the usual "Artist - Album (Year) [tags]" spellings.
func parseLooseMusic(s string, m *MusicRelease) {
	s = foldSpace(strings.ReplaceAll(s, "_", " "))

	// Brackets that hold only a year and tags are quality notes; the others
	// (an edition name) stay in the album.
	rest := reBrackets.ReplaceAllStringFunc(s, func(g string) string {
		content := strings.TrimSpace(g[1 : len(g)-1])
		var (
			tags  musicTags
			year  int
			found bool
		)
		for _, piece := range reTagPieces.Split(content, -1) {
			piece = strings.TrimSpace(piece)
			if piece == "" {
				continue
			}
			if y, ok := yearOnly(piece); ok {
				if year == 0 {
					year = y
				}
				found = true
				continue
			}
			t, ok := readMusicTags(piece)
			if !ok {
				return g
			}
			mergeTags(&tags, t)
			found = true
		}
		if !found {
			return g
		}
		if year != 0 && m.Year == 0 {
			m.Year = year
		}
		m.applyTags(tags)
		return " "
	})
	rest = strings.Trim(foldSpace(rest), " -–—._")

	parts := reDashSplit.Split(rest, -1)
	for i := range parts {
		parts[i] = strings.TrimSpace(parts[i])
	}
	// Quality notes that follow the album after a dash: "Album - 2012 - FLAC".
	// At least an artist and an album stay.
	for len(parts) >= 3 {
		n := len(parts)
		last := parts[n-1]
		if y, ok := yearOnly(last); ok {
			if m.Year == 0 {
				m.Year = y
			}
			parts = parts[:n-1]
		} else if t, ok := readMusicTags(last); ok {
			m.applyTags(t)
			parts = parts[:n-1]
		} else if t, ok := readMusicTags(parts[n-2] + " " + last); ok && n >= 4 {
			// "MP3 - 320": a format and its bitrate, set apart by a dash.
			m.applyTags(t)
			parts = parts[:n-2]
		} else {
			break
		}
	}
	// A year between the artist and the album: "Artist - 2012 - Album".
	if len(parts) >= 3 {
		if y, ok := yearOnly(parts[1]); ok {
			if m.Year == 0 {
				m.Year = y
			}
			parts = append(parts[:1:1], parts[2:]...)
		}
	}
	// Quality words stuck on the end of the album without a dash.
	if n := len(parts); n > 0 {
		parts[n-1] = stripTrailingTags(parts[n-1], m)
	}
	switch len(parts) {
	case 0:
	case 1:
		m.Album = parts[0]
	default:
		m.Artist = parts[0]
		m.Album = strings.Join(parts[1:], " - ")
	}
	m.Artist = strings.Trim(m.Artist, " -–—._")
	m.Album = strings.Trim(m.Album, " -–—._")
}

func mergeTags(dst *musicTags, t musicTags) {
	if t.format != "" {
		dst.format = t.format
	}
	if t.bitrate != "" {
		dst.bitrate = t.bitrate
	}
	if t.source != "" {
		dst.source = t.source
	}
}

// stripTrailingTags takes year and quality words off the end of an album text
// ("Album 2012 FLAC") and records them. A year is only taken when something else
// is left, so an album called "1989" keeps its name.
func stripTrailingTags(text string, m *MusicRelease) string {
	toks := strings.Fields(text)
loop:
	for len(toks) > 1 {
		for n := min(3, len(toks)-1); n >= 1; n-- {
			chunk := strings.Join(toks[len(toks)-n:], " ")
			if y, ok := yearOnly(chunk); ok {
				if m.Year == 0 {
					m.Year = y
				}
				toks = toks[:len(toks)-n]
				continue loop
			}
			if t, ok := readMusicTags(chunk); ok {
				m.applyTags(t)
				toks = toks[:len(toks)-n]
				continue loop
			}
		}
		break
	}
	return strings.Join(toks, " ")
}
