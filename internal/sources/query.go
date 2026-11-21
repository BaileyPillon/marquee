package sources

import (
	"fmt"
	"net/url"
	"strconv"
	"strings"
)

// Media kinds a search is for. "" is the latest-items feed (RSS).
const (
	MediaMovie  = "movie"
	MediaShow   = "show"
	MediaArtist = "artist"
)

// Query is one search, before it is shaped for a particular source.
type Query struct {
	Media   string // MediaMovie, MediaShow, MediaArtist, or "" for the latest items
	Text    string // the title, used when no id can be sent
	Year    int    // movies: added to Text when ids are not used
	IMDb    string // "tt0017136" or just the digits
	TMDb    int64
	TVDb    int64
	TVMaze  int64
	Season  int // shows: 0 means not set
	Episode int // shows: 0 means not set
	Limit   int

	// NoIDs forces a text search. The search service sets it for the second
	// try when an id search found nothing.
	NoIDs bool
}

// imdbDigits drops the "tt" so a source that wants bare digits is happy; the
// bridges accept either spelling.
func imdbDigits(id string) string {
	id = strings.TrimSpace(strings.ToLower(id))
	return strings.TrimPrefix(id, "tt")
}

// BuildSearch shapes a query for one source using what its capabilities say.
// It never sends an id parameter the source did not list, and it falls back to
// a text search (q) when it has none to send. usedIDs says whether the answer
// is an id search, so the caller can retry with text when it finds nothing.
// The API key is not part of the result; the client adds it.
func BuildSearch(caps *Caps, q Query, cats []int) (mode string, params url.Values, usedIDs bool) {
	params = url.Values{}
	mode = ModeSearch
	switch q.Media {
	case MediaMovie:
		if caps.ModeAvailable(ModeMovie) {
			mode = ModeMovie
		}
	case MediaShow:
		if caps.ModeAvailable(ModeTV) {
			mode = ModeTV
		}
	case MediaArtist:
		if caps.ModeAvailable(ModeMusic) {
			mode = ModeMusic
		}
	}
	text := strings.TrimSpace(q.Text)

	switch q.Media {
	case MediaMovie:
		if !q.NoIDs {
			if d := imdbDigits(q.IMDb); d != "" && caps.Supports(mode, "imdbid") {
				params.Set("imdbid", d)
				usedIDs = true
			}
			if q.TMDb > 0 && caps.Supports(mode, "tmdbid") {
				params.Set("tmdbid", strconv.FormatInt(q.TMDb, 10))
				usedIDs = true
			}
		}
		if !usedIDs && text != "" {
			if q.Year > 0 {
				text += " " + strconv.Itoa(q.Year)
			}
			params.Set("q", text)
		}

	case MediaShow:
		// Season and episode go in their own parameters when the source takes
		// them; otherwise they are written into the text as S01E04.
		native := mode == ModeTV && (q.Season == 0 || caps.Supports(mode, "season")) &&
			(q.Episode == 0 || caps.Supports(mode, "ep"))
		if native && q.Season > 0 {
			params.Set("season", strconv.Itoa(q.Season))
			if q.Episode > 0 {
				params.Set("ep", strconv.Itoa(q.Episode))
			}
		}
		if native && !q.NoIDs {
			if q.TVMaze > 0 && caps.Supports(mode, "tvmazeid") {
				params.Set("tvmazeid", strconv.FormatInt(q.TVMaze, 10))
				usedIDs = true
			}
			if d := imdbDigits(q.IMDb); d != "" && caps.Supports(mode, "imdbid") {
				params.Set("imdbid", d)
				usedIDs = true
			}
			if q.TVDb > 0 && caps.Supports(mode, "tvdbid") {
				params.Set("tvdbid", strconv.FormatInt(q.TVDb, 10))
				usedIDs = true
			}
			if q.TMDb > 0 && caps.Supports(mode, "tmdbid") {
				params.Set("tmdbid", strconv.FormatInt(q.TMDb, 10))
				usedIDs = true
			}
		}
		if !usedIDs && text != "" {
			if !native && q.Season > 0 {
				if q.Episode > 0 {
					text += fmt.Sprintf(" S%02dE%02d", q.Season, q.Episode)
				} else {
					text += fmt.Sprintf(" S%02d", q.Season)
				}
			}
			params.Set("q", text)
		}

	case MediaArtist:
		if text != "" {
			params.Set("q", text)
		}

	default: // the latest items
	}

	params.Set("t", mode)
	if len(cats) > 0 {
		parts := make([]string, len(cats))
		for i, c := range cats {
			parts[i] = strconv.Itoa(c)
		}
		params.Set("cat", strings.Join(parts, ","))
	}
	params.Set("limit", strconv.Itoa(caps.Limit(q.Limit)))
	params.Set("extended", "1")
	return mode, params, usedIDs
}

// categoriesFor picks the categories to send for a kind of search. A source the
// owner limited to TV categories is not asked for movies (ok is false then).
// With nothing configured the kind's main category is used. Source-specific
// categories (numbers from 100000) are always passed along.
func categoriesFor(configured []int, media string) (cats []int, ok bool) {
	var lo, hi int
	switch media {
	case MediaMovie:
		lo, hi = 2000, 3000
	case MediaArtist:
		lo, hi = 3000, 4000
	case MediaShow:
		lo, hi = 5000, 6000
	default:
		return configured, true // the latest-items feed: exactly what the owner chose
	}
	if len(configured) == 0 {
		return []int{lo}, true
	}
	for _, c := range configured {
		if (c >= lo && c < hi) || c >= 100000 {
			cats = append(cats, c)
		}
	}
	return cats, len(cats) > 0
}
