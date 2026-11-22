// Package metadata looks titles up: TMDB (the owner's own key) for movies,
// TVmaze for shows and MusicBrainz for artists, with Cover Art Archive
// covers. Every call goes through core/outbound, every answer is cached in the
// database, and artwork reaches the browser only through the artwork proxy.
//
// The package returns normalised types (MovieInfo, ShowInfo, ArtistInfo) so
// the rest of Marquee never sees a provider's own JSON.
package metadata

import (
	"context"
	"errors"
	"html"
	"strings"
)

// Provider names. They are also the first word of an artwork ref.
const (
	ProviderTMDB        = "tmdb"
	ProviderTVmaze      = "tvmaze"
	ProviderMusicBrainz = "musicbrainz"
	ProviderArtwork     = "artwork" // the image proxy, for the status report
)

// Kinds of title.
const (
	KindMovie  = "movie"
	KindShow   = "show"
	KindArtist = "artist"
)

// ValidKind reports whether k is a known kind.
func ValidKind(k string) bool { return k == KindMovie || k == KindShow || k == KindArtist }

// ProviderFor returns the provider that serves a kind.
func ProviderFor(kind string) string {
	switch kind {
	case KindMovie:
		return ProviderTMDB
	case KindShow:
		return ProviderTVmaze
	case KindArtist:
		return ProviderMusicBrainz
	}
	return ""
}

// ---- errors ----------------------------------------------------------------

// Error is a failure with a stable code the API can show. Two Errors are the
// same error (errors.Is) when their codes match.
type Error struct {
	Code    string
	Message string // plain words for the person using Marquee
	Err     error  // the cause, never containing a key or a query string
}

func (e *Error) Error() string {
	if e.Err != nil {
		return e.Code + ": " + e.Err.Error()
	}
	return e.Code + ": " + e.Message
}

func (e *Error) Unwrap() error { return e.Err }

// Is matches by code.
func (e *Error) Is(target error) bool {
	t, ok := target.(*Error)
	return ok && t.Code == e.Code
}

// Errors callers match with errors.Is.
var (
	ErrTMDBKeyMissing = &Error{Code: "tmdb_key_missing", Message: "Movie search needs your TMDB key. Add it in Settings, then try again."}
	ErrTMDBKeyInvalid = &Error{Code: "tmdb_key_invalid", Message: "TMDB did not accept the key in Settings."}
	ErrNotFound       = &Error{Code: "not_found", Message: "The provider has no such title."}
	ErrUpstream       = &Error{Code: "upstream_error", Message: "The metadata provider did not answer."}
)

func notFound(provider string) error {
	return &Error{Code: ErrNotFound.Code, Message: "That title was not found at " + provider + "."}
}

func upstream(provider string, cause error) error {
	return &Error{Code: ErrUpstream.Code, Message: provider + " did not answer. Try again in a minute.", Err: cause}
}

// CodeOf returns the stable code of err, or "" if it has none.
func CodeOf(err error) string {
	var e *Error
	if errors.As(err, &e) {
		return e.Code
	}
	return ""
}

// MessageOf returns the plain-words message of err, or its text.
func MessageOf(err error) string {
	var e *Error
	if errors.As(err, &e) {
		return e.Message
	}
	return err.Error()
}

// ---- normalised results ----------------------------------------------------

// Image is a reference to artwork, resolved through the artwork proxy:
// Ref is "tmdb/w500/abc.jpg", "tvmaze/uploads/images/..." or "caa/...". The
// browser loads it from /api/v1/artwork/<Ref>.
type Image struct {
	Ref  string `json:"ref"`
	Kind string `json:"kind,omitempty"` // poster, backdrop, cover or profile
}

// IsZero reports whether there is no image.
func (i Image) IsZero() bool { return i.Ref == "" }

// Person is someone credited on a title.
type Person struct {
	Name  string `json:"name"`
	Role  string `json:"role"` // actor, director, writer or creator
	Job   string `json:"job"`  // the character an actor plays, or the crew job
	Image Image  `json:"image"`
}

// SearchHit is one candidate from a provider's search. Nothing is saved.
type SearchHit struct {
	Kind       string `json:"kind"`
	Provider   string `json:"provider"`
	ProviderID string `json:"providerId"`
	Name       string `json:"name"`
	Year       int    `json:"year"`
	Overview   string `json:"overview"`
	Poster     Image  `json:"poster"`
	IMDbID     string `json:"imdbId,omitempty"`
	// Detail is a short hint to tell namesakes apart: an artist's type and
	// country, a show's network.
	Detail string `json:"detail,omitempty"`
}

// MovieInfo is a movie's full details.
type MovieInfo struct {
	TMDBID        int64    `json:"tmdbId"`
	IMDbID        string   `json:"imdbId"`
	Title         string   `json:"title"`
	OriginalTitle string   `json:"originalTitle"`
	Year          int      `json:"year"`        // the year of the first release (a premiere abroad counts), not the primary date's
	ReleaseDate   string   `json:"releaseDate"` // the provider's primary release date; the calendar uses it
	Overview      string   `json:"overview"`
	Tagline       string   `json:"tagline"`
	RuntimeMin    int      `json:"runtimeMin"`
	Genres        []string `json:"genres"`
	Status        string   `json:"status"`
	Certification string   `json:"certification"` // US theatrical rating, when TMDB has one
	TrailerKey    string   `json:"trailerKey"`    // YouTube video key
	Poster        Image    `json:"poster"`
	Backdrop      Image    `json:"backdrop"`
	People        []Person `json:"people"`
}

// ShowInfo is a show's full details with seasons and episodes.
type ShowInfo struct {
	TVmazeID   int64         `json:"tvmazeId"`
	IMDbID     string        `json:"imdbId"`
	TheTVDBID  int64         `json:"thetvdbId"`
	Name       string        `json:"name"`
	Year       int           `json:"year"`
	Premiered  string        `json:"premiered"`
	Overview   string        `json:"overview"`
	RuntimeMin int           `json:"runtimeMin"`
	Genres     []string      `json:"genres"`
	Status     string        `json:"status"`
	Network    string        `json:"network"`
	Language   string        `json:"language"`
	Poster     Image         `json:"poster"`
	Backdrop   Image         `json:"backdrop"`
	Seasons    []SeasonInfo  `json:"seasons"`
	Episodes   []EpisodeInfo `json:"episodes"`
	People     []Person      `json:"people"`
}

// SeasonInfo is one season. Season 0 holds specials.
type SeasonInfo struct {
	Number       int    `json:"number"`
	Name         string `json:"name"`
	EpisodeCount int    `json:"episodeCount"`
	PremiereDate string `json:"premiereDate"`
	EndDate      string `json:"endDate"`
	Poster       Image  `json:"poster"`
}

// EpisodeInfo is one episode.
type EpisodeInfo struct {
	Season     int    `json:"season"`
	Number     int    `json:"number"`
	Name       string `json:"name"`
	AirDate    string `json:"airDate"` // YYYY-MM-DD, empty if unknown
	Overview   string `json:"overview"`
	RuntimeMin int    `json:"runtimeMin"`
	Special    bool   `json:"special"`
}

// ArtistInfo is an artist's full details with albums and tracks.
type ArtistInfo struct {
	MBID           string      `json:"mbid"`
	Name           string      `json:"name"`
	SortName       string      `json:"sortName"`
	Type           string      `json:"type"` // Person, Group, ...
	Country        string      `json:"country"`
	Disambiguation string      `json:"disambiguation"`
	Year           int         `json:"year"`  // when the artist began
	Begin          string      `json:"begin"` // life-span begin as MusicBrainz writes it (1810-03-01, 1998, or empty)
	End            string      `json:"end"`   // life-span end; empty while it goes on
	Ended          bool        `json:"ended"`
	Genres         []string    `json:"genres"`
	AltNames       []string    `json:"altNames"`
	Albums         []AlbumInfo `json:"albums"`
}

// AlbumInfo is a release group (an album or an EP) with the track list of its
// earliest official release.
type AlbumInfo struct {
	MBID        string `json:"mbid"` // the release group
	ReleaseID   string `json:"releaseId"`
	Title       string `json:"title"`
	Year        int    `json:"year"`
	ReleaseDate string `json:"releaseDate"`
	Type        string `json:"type"` // album or ep
	// SecondaryTypes are MusicBrainz's secondary types ("Soundtrack", "Live",
	// ...) as it writes them. An album with none is a studio album.
	SecondaryTypes []string    `json:"secondaryTypes"`
	Cover          Image       `json:"cover"`
	Tracks         []TrackInfo `json:"tracks"`
}

// TrackInfo is one track on an album.
type TrackInfo struct {
	Disc     int    `json:"disc"`
	Position int    `json:"position"`
	Title    string `json:"title"`
	LengthMS int    `json:"lengthMs"`
	MBID     string `json:"mbid"` // the recording
}

// ExternalIDs are the other databases' ids for a TMDB title.
type ExternalIDs struct {
	IMDbID string `json:"imdbId"`
	TVDBID int64  `json:"tvdbId"`
}

// ---- provider interfaces ---------------------------------------------------

// MovieProvider finds movies.
type MovieProvider interface {
	SearchMovies(ctx context.Context, query string, year int) ([]SearchHit, error)
	Movie(ctx context.Context, id string) (*MovieInfo, error)
}

// ShowProvider finds shows.
type ShowProvider interface {
	SearchShows(ctx context.Context, query string) ([]SearchHit, error)
	Show(ctx context.Context, id string) (*ShowInfo, error)
	// ShowByIMDb finds a show from its IMDb id (tt1234567).
	ShowByIMDb(ctx context.Context, imdbID string) (*SearchHit, error)
}

// ArtistProvider finds artists.
type ArtistProvider interface {
	SearchArtists(ctx context.Context, query string) ([]SearchHit, error)
	Artist(ctx context.Context, id string) (*ArtistInfo, error)
}

// ArtistBaseProvider is an ArtistProvider that can also answer quickly with the
// artist alone (one request, no albums or tracks). The catalog uses it to store
// a new artist at once and read the albums in the background.
type ArtistBaseProvider interface {
	ArtistBase(ctx context.Context, id string) (*ArtistInfo, error)
}

// ---- small helpers shared by the providers ---------------------------------

// year returns the leading four-digit year of a date such as "1927-01-10".
func year(date string) int {
	if len(date) < 4 {
		return 0
	}
	n := 0
	for _, c := range date[:4] {
		if c < '0' || c > '9' {
			return 0
		}
		n = n*10 + int(c-'0')
	}
	return n
}

// cleanText trims and collapses whitespace.
func cleanText(s string) string { return strings.Join(strings.Fields(s), " ") }

// stripHTML turns the small HTML fragments TVmaze sends into plain text.
func stripHTML(s string) string {
	if s == "" {
		return ""
	}
	var b strings.Builder
	inTag := false
	for _, r := range s {
		switch {
		case r == '<':
			inTag = true
			b.WriteByte(' ')
		case r == '>' && inTag:
			inTag = false
		case !inTag:
			b.WriteRune(r)
		}
	}
	return cleanText(html.UnescapeString(b.String()))
}
