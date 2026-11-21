// Package titles is the catalog: the movies, shows and artists Marquee knows
// about, with their seasons and episodes, albums and tracks, and whether each
// is monitored (wanted). Metadata comes from the metadata module; nothing in
// this package talks to the outside world itself.
//
// Looking a title up does not save it. Add saves it, with everything the
// provider knows, in one transaction.
package titles

import (
	"errors"
	"time"

	"github.com/BaileyPillon/marquee/internal/metadata"
)

// Kinds of title.
const (
	KindMovie  = metadata.KindMovie
	KindShow   = metadata.KindShow
	KindArtist = metadata.KindArtist
)

// Errors the web layer maps to HTTP statuses.
var ErrNotFound = errors.New("titles: no such title")

// InvalidError is a request the caller can fix; Message is plain words.
type InvalidError struct{ Message string }

func (e *InvalidError) Error() string { return e.Message }

func invalid(msg string) error { return &InvalidError{Message: msg} }

// Title is one catalog row.
type Title struct {
	ID            int64     `json:"id"`
	Kind          string    `json:"kind"`
	TMDBID        int64     `json:"tmdbId,omitempty"`
	TVmazeID      int64     `json:"tvmazeId,omitempty"`
	IMDbID        string    `json:"imdbId,omitempty"`
	MBID          string    `json:"mbid,omitempty"`
	Name          string    `json:"name"`
	SortName      string    `json:"sortName"`
	AltNames      []string  `json:"altNames"`
	Year          int       `json:"year"`
	Overview      string    `json:"overview"`
	Tagline       string    `json:"tagline"`
	Certification string    `json:"certification"`
	TrailerKey    string    `json:"trailerKey"` // a YouTube video key
	RuntimeMin    int       `json:"runtimeMin"`
	Genres        []string  `json:"genres"`
	PosterRef     string    `json:"posterRef"`   // artwork-proxy ref, loaded from /api/v1/artwork/<ref>
	BackdropRef   string    `json:"backdropRef"` // same
	Status        string    `json:"status"`
	Monitored     bool      `json:"monitored"`
	RootFolder    string    `json:"rootFolder"`
	AddedAt       time.Time `json:"addedAt"`
	UpdatedAt     time.Time `json:"updatedAt"`
	RefreshedAt   time.Time `json:"refreshedAt"`
}

// Season is one season of a show; season 0 holds specials.
type Season struct {
	Number       int  `json:"number"`
	Monitored    bool `json:"monitored"`
	EpisodeCount int  `json:"episodeCount"`
}

// Episode is one episode of a show.
type Episode struct {
	ID         int64  `json:"id"`
	Season     int    `json:"season"`
	Number     int    `json:"number"`
	Name       string `json:"name"`
	AirDate    string `json:"airDate"` // YYYY-MM-DD, empty if unknown
	Overview   string `json:"overview"`
	RuntimeMin int    `json:"runtimeMin"`
	Monitored  bool   `json:"monitored"`
}

// Album is an album or EP of an artist.
type Album struct {
	ID        int64   `json:"id"`
	MBID      string  `json:"mbid"`
	Name      string  `json:"name"`
	Year      int     `json:"year"`
	Type      string  `json:"type"` // album or ep
	Monitored bool    `json:"monitored"`
	CoverRef  string  `json:"coverRef"`
	Tracks    []Track `json:"tracks"`
}

// Track is one track of an album.
type Track struct {
	ID       int64  `json:"id"`
	Disc     int    `json:"disc"`
	Position int    `json:"position"`
	Name     string `json:"name"`
	LengthMS int    `json:"lengthMs"`
	MBID     string `json:"mbid"`
}

// Person is someone credited on a title.
type Person struct {
	Name     string `json:"name"`
	Role     string `json:"role"` // actor, director, writer or creator
	Job      string `json:"job"`  // the character, or the crew job
	ImageRef string `json:"imageRef"`
}

// Detail is a title with everything under it.
type Detail struct {
	Title
	Seasons  []Season  `json:"seasons"`
	Episodes []Episode `json:"episodes"`
	Albums   []Album   `json:"albums"`
	People   []Person  `json:"people"`
}

// AddOptions say how a new title is kept.
type AddOptions struct {
	Monitored  bool
	RootFolder string
}

// LookupResult is a provider candidate that is not saved, and whether it is
// already in the library.
type LookupResult struct {
	metadata.SearchHit
	InLibrary bool  `json:"inLibrary"`
	TitleID   int64 `json:"titleId,omitempty"`
}

// ListFilter selects titles for List. Zero values mean "any".
type ListFilter struct {
	Kind      string
	Monitored *bool
	Q         string // matches the name or any alternative name
	Sort      string // added (newest first), name or year (newest first)
	Limit     int    // default 50, at most 200
	Offset    int
}

// Sort orders.
const (
	SortAdded = "added"
	SortName  = "name"
	SortYear  = "year"
)

// List limits.
const (
	DefaultLimit = 50
	MaxLimit     = 200
)

// ListResult is one page of titles and how many match in all.
type ListResult struct {
	Items  []Title `json:"items"`
	Total  int     `json:"total"`
	Limit  int     `json:"limit"`
	Offset int     `json:"offset"`
}

// Candidate is what the library matcher needs to match a file to a title.
type Candidate struct {
	ID       int64
	Kind     string
	Name     string
	Year     int
	AltNames []string
}
