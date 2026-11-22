package parse

import (
	"regexp"
	"strings"
)

// MusicRelease is what ParseMusic could read from a track path.
type MusicRelease struct {
	Artist      string `json:"artist,omitempty"`
	Album       string `json:"album,omitempty"`
	Year        int    `json:"year,omitempty"`
	Format      string `json:"format,omitempty"`  // FLAC, MP3, AAC, ALAC, Opus, Vorbis, WAV, ...
	Bitrate     string `json:"bitrate,omitempty"` // "320kbps", "V0", or "24bit" for hi-res files
	Disc        int    `json:"disc,omitempty"`
	Track       int    `json:"track,omitempty"`
	Title       string `json:"title,omitempty"`
	TrackArtist string `json:"trackArtist,omitempty"` // only when it differs from Artist (compilations)

	// Read by ParseMusicRelease from a release name (not from a track path).
	Source      string `json:"source,omitempty"`      // CD, WEB, Vinyl, SACD, ...
	Group       string `json:"group,omitempty"`       // the scene group after the last hyphen
	Discography bool   `json:"discography,omitempty"` // a many-album pack, not one album
}

// QualityName is the short quality label: "FLAC", "FLAC-24bit", "MP3-320kbps".
func (m MusicRelease) QualityName() string {
	if m.Format == "" {
		return "Unknown"
	}
	if m.Bitrate == "" {
		return m.Format
	}
	return m.Format + "-" + m.Bitrate
}

var musicFormat = map[string]string{
	"flac": "FLAC", "mp3": "MP3", "m4a": "AAC", "aac": "AAC", "alac": "ALAC", "ogg": "Vorbis",
	"opus": "Opus", "wav": "WAV", "wma": "WMA", "ape": "APE", "wv": "WavPack", "aiff": "AIFF", "aif": "AIFF",
}

var (
	reDiscDir    = regexp.MustCompile(`(?i)^(?:cd|dis[ck])[ ._-]*(\d{1,2})(?:[^0-9].*)?$`)
	reTrackDisc  = regexp.MustCompile(`(?i)^(?:cd|dis[ck])[ ._-]*(\d{1,2})[ ._-]+(\d{1,3})(?:[ ._)-]+(.*))?$`)
	reTrackDash  = regexp.MustCompile(`^(\d{1,2})[-.](\d{2,3})(?:[ ._)-]+(.*))?$`)
	reTrackSep   = regexp.MustCompile(`^(\d{1,3})([ ._)-]+)(.*)$`)
	reTrackOnly  = regexp.MustCompile(`^(\d{1,3})$`)
	reArtistTrck = regexp.MustCompile(`^(.+?) - (\d{2,3})(?:[ ._)-]+(.*))?$`)

	reBrackets  = regexp.MustCompile(`\[[^\]]*\]|\([^)]*\)|\{[^}]*\}`)
	reYearOnly  = regexp.MustCompile(`^(?:19|20)\d\d$`)
	reYearLead  = regexp.MustCompile(`^((?:19|20)\d\d)\s*[-–._]\s*(\S.*)$`)
	reYearTrail = regexp.MustCompile(`^(.*\S)\s*[-–]\s*((?:19|20)\d\d)$`)
	reArtistAlb = regexp.MustCompile(`^(.+?)\s+[-–]\s+(\S.*)$`)
	reTagToken  = regexp.MustCompile(`[a-z][a-z0-9]*|\d+(?:\.\d+)?`)
)

var (
	tagFormats = map[string]string{"flac": "FLAC", "alac": "ALAC", "mp3": "MP3", "aac": "AAC", "m4a": "AAC",
		"opus": "Opus", "ogg": "Vorbis", "wav": "WAV", "ape": "APE"}
	tagWords    = map[string]bool{"web": true, "cd": true, "vinyl": true, "lossless": true, "cbr": true, "vbr": true, "kbps": true, "k": true, "khz": true, "hz": true, "bit": true, "hires": true, "hi": true, "res": true, "cdda": true}
	sampleRates = map[string]bool{"44": true, "44.1": true, "48": true, "88.2": true, "96": true, "176.4": true, "192": true}
	bitrates    = map[string]bool{"128": true, "160": true, "192": true, "224": true, "256": true, "320": true}
)

// parseTags reads the text of a bracket as quality tags. It is a tag group
// only when every word in it is a known tag.
func parseTags(content string) (ok bool, format, bitrate string) {
	toks := reTagToken.FindAllString(strings.ToLower(content), -1)
	if len(toks) == 0 {
		return false, "", ""
	}
	depth := ""
	for _, t := range toks {
		switch {
		case tagFormats[t] != "":
			format = tagFormats[t]
		case tagWords[t]:
		case t == "16" || t == "24" || t == "32":
			depth = t
		case sampleRates[t] && depth != "":
			// the 96 of "24-96"
		case bitrates[t]:
			bitrate = t + "kbps"
		case sampleRates[t]:
		case len(t) == 2 && t[0] == 'v' && t[1] >= '0' && t[1] <= '2':
			bitrate = strings.ToUpper(t)
		default:
			return false, "", ""
		}
	}
	if depth == "24" || depth == "32" {
		bitrate = depth + "bit"
	}
	return true, format, bitrate
}

// ParseMusic reads a track path such as
//
//	Frédéric Chopin/Nocturnes (2012) [FLAC]/CD1/01 - Nocturne in E-flat major, Op. 9 No. 2.flac
//
// The path should be relative to the library folder: the last directory is the
// album (a CD1 or Disc 2 directory is read as the disc and skipped), the one
// above it the artist. A bare file name works too and gives track, title and
// format. The album directory may say "Artist - Album (Year) [FLAC]".
func ParseMusic(path string) MusicRelease {
	var m MusicRelease
	base, dirs := splitPath(path)
	stem, ext := base, ""
	if i := strings.LastIndexByte(base, '.'); i > 0 {
		if _, ok := musicFormat[strings.ToLower(base[i+1:])]; ok {
			stem, ext = base[:i], strings.ToLower(base[i+1:])
		}
	}
	m.Format = musicFormat[ext]

	discFromDir := 0
	if n := len(dirs); n > 0 {
		if g := reDiscDir.FindStringSubmatch(strings.TrimSpace(dirs[n-1])); g != nil {
			discFromDir = atoi(g[1])
			dirs = dirs[:n-1]
		}
	}
	m.Disc = discFromDir

	var artistDir string
	if n := len(dirs); n > 0 {
		album := strings.TrimSpace(dirs[n-1])
		if n > 1 {
			artistDir = strings.TrimSpace(dirs[n-2])
		}
		m.parseAlbumDir(album, artistDir)
	}
	m.parseFile(stem, discFromDir)
	return m
}

func (m *MusicRelease) parseAlbumDir(name, artistDir string) {
	var format, bitrate string
	rest := reBrackets.ReplaceAllStringFunc(name, func(g string) string {
		c := strings.TrimSpace(g[1 : len(g)-1])
		if reYearOnly.MatchString(c) {
			if m.Year == 0 {
				m.Year = atoi(c)
			}
			return ""
		}
		if ok, f, b := parseTags(c); ok {
			if f != "" {
				format = f
			}
			if b != "" {
				bitrate = b
			}
			return ""
		}
		return g
	})
	rest = foldSpace(rest)
	rest = strings.Trim(rest, " -–._")
	if g := reYearLead.FindStringSubmatch(rest); g != nil && m.Year == 0 {
		m.Year, rest = atoi(g[1]), g[2]
	} else if g := reYearTrail.FindStringSubmatch(rest); g != nil && m.Year == 0 {
		m.Year, rest = atoi(g[2]), g[1]
	}
	rest = strings.TrimSpace(rest)

	m.Artist, m.Album = artistDir, rest
	if g := reArtistAlb.FindStringSubmatch(rest); g != nil {
		left, right := strings.TrimSpace(g[1]), strings.TrimSpace(g[2])
		switch {
		case artistDir == "":
			m.Artist, m.Album = left, right
		case Normalize(left) == Normalize(artistDir):
			m.Album = right
		}
	}
	if format != "" {
		m.Format = format // "[ALAC]" outranks the ambiguous .m4a
	}
	m.Bitrate = bitrate
}

func (m *MusicRelease) parseFile(stem string, discFromDir int) {
	s := foldSpace(strings.ReplaceAll(stem, "_", " "))
	rest, hasTrack := s, false
	switch {
	case reTrackDisc.MatchString(s):
		g := reTrackDisc.FindStringSubmatch(s)
		m.Disc, m.Track, rest, hasTrack = atoi(g[1]), atoi(g[2]), g[3], true
	case reTrackDash.MatchString(s):
		g := reTrackDash.FindStringSubmatch(s)
		m.Disc, m.Track, rest, hasTrack = atoi(g[1]), atoi(g[2]), g[3], true
	case reTrackSep.MatchString(s):
		g := reTrackSep.FindStringSubmatch(s)
		// "7 Rings" is a title; "07 Rings", "7 - Rings" and "7. Rings" are tracks.
		if !(len(g[1]) == 1 && strings.TrimSpace(g[2]) == "") {
			m.Track, rest, hasTrack = atoi(g[1]), g[3], true
		}
	case reTrackOnly.MatchString(s):
		m.Track, rest, hasTrack = atoi(s), "", true
	case reArtistTrck.MatchString(s):
		g := reArtistTrck.FindStringSubmatch(s)
		m.Track, rest, hasTrack = atoi(g[2]), g[3], true
		left := strings.TrimSpace(g[1])
		switch {
		case m.Artist == "":
			m.Artist = left
		case Normalize(left) != Normalize(m.Artist):
			m.TrackArtist = left
		}
	}
	// Disc and track glued together: 201 is disc 2, track 1 when the folder says disc 2.
	if hasTrack && m.Track >= 100 && discFromDir > 0 && m.Track/100 == discFromDir {
		m.Track %= 100
	}
	rest = strings.Trim(rest, " -–._")
	m.Title = m.trackTitle(rest, hasTrack)
}

// trackTitle drops a leading "Artist - " when it repeats the album artist, and
// keeps it as the track artist on a compilation. A bare "Artist - Title" file
// with nothing else to go on names its own artist.
func (m *MusicRelease) trackTitle(s string, hasTrack bool) string {
	g := reArtistAlb.FindStringSubmatch(s)
	if g == nil {
		return s
	}
	left, right := strings.TrimSpace(g[1]), strings.TrimSpace(g[2])
	if m.Artist == "" {
		if !hasTrack {
			m.Artist = left
			return right
		}
		return s
	}
	switch Normalize(m.Artist) {
	case Normalize(left):
		return right
	case "various artists", "various", "va":
		m.TrackArtist = left
		return right
	}
	return s
}
