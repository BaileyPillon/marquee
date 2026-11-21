// Package mediaservers tells the owner's media servers (Jellyfin, Emby, Plex)
// about changes in the library, so a film that was just imported shows up
// without anyone pressing "scan". It can also work out where a title lives on a
// server, for the "Open in Jellyfin" button.
//
// Every call to a server goes through core/outbound. Tokens are sealed with the
// master key (core/secrets) and are never logged, returned or put in an error.
// A server that does not answer is never fatal: the problem is logged and
// recorded on the server's row, where the owner can see it.
package mediaservers

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/BaileyPillon/marquee/internal/titles"
)

// Server kinds.
const (
	KindJellyfin = "jellyfin"
	KindEmby     = "emby"
	KindPlex     = "plex"
)

func validKind(k string) bool { return k == KindJellyfin || k == KindEmby || k == KindPlex }

func displayKind(k string) string {
	switch k {
	case KindJellyfin:
		return "Jellyfin"
	case KindEmby:
		return "Emby"
	case KindPlex:
		return "Plex"
	}
	return k
}

// Event kinds Notify understands.
const (
	EventCreated = "created" // files were added
	EventDeleted = "deleted" // files were removed
)

// Event says what changed. Paths are Marquee's own paths (files or folders);
// each server's path map turns them into what that server sees.
type Event struct {
	Kind  string
	Paths []string
}

// Timeouts.
const (
	callTimeout = 20 * time.Second
	openTimeout = 12 * time.Second
	maxReply    = 16 << 20
)

// Errors.
var (
	ErrNotFound  = errors.New("mediaservers: no such media server")
	ErrNameTaken = errors.New("mediaservers: another media server already has that name")
	// ErrNotOnServer means no enabled server has the title.
	ErrNotOnServer = errors.New("mediaservers: the title is not on any media server")
	// ErrAuth: the server answered but refused the token.
	ErrAuth = errors.New("the media server refused the token")
	// ErrUnreachable: the server did not answer.
	ErrUnreachable = errors.New("the media server did not answer")
)

// InvalidError is a bad value in what the owner sent; Message says what to fix.
type InvalidError struct{ Message string }

func (e *InvalidError) Error() string { return "mediaservers: " + e.Message }

// Library is one library (a Jellyfin virtual folder or a Plex section) and the
// folders it reads, as the server sees them.
type Library struct {
	ID    string   `json:"id"`
	Name  string   `json:"name"`
	Paths []string `json:"paths"`
}

// TestResult is the outcome of testing a server. A server that does not answer
// is not an error: the result says so.
type TestResult struct {
	OK        bool      `json:"ok"`
	Version   string    `json:"version"`
	Error     string    `json:"error"`
	Libraries []Library `json:"libraries"`
}

// ServerRef names a server on an OpenResult.
type ServerRef struct {
	ID   int64  `json:"id"`
	Name string `json:"name"`
	Kind string `json:"kind"`
}

// OpenResult is where a title can be opened.
type OpenResult struct {
	URL    string    `json:"url"`
	Server ServerRef `json:"server"`
}

// NotifyResult says how one server took an event.
type NotifyResult struct {
	Server ServerRef `json:"server"`
	OK     bool      `json:"ok"`
	Error  string    `json:"error,omitempty"`
	// Sent is the paths the server was given, as it sees them.
	Sent []string `json:"sent,omitempty"`
}

// titleQuery is what an adapter needs to find a title.
type titleQuery struct {
	Kind    string // titles.KindMovie | KindShow | KindArtist
	Name    string
	Names   []string // the name and the alternative names
	Year    int
	TMDB    int64
	TVmaze  int64
	IMDb    string
	MBID    string
	Version string // filled by the caller when known
}

func queryOf(t titles.Title) titleQuery {
	q := titleQuery{Kind: t.Kind, Name: t.Name, Year: t.Year, TMDB: t.TMDBID, TVmaze: t.TVmazeID,
		IMDb: strings.ToLower(t.IMDbID), MBID: strings.ToLower(t.MBID)}
	q.Names = append([]string{t.Name}, t.AltNames...)
	return q
}

// adapter talks to one server. Paths given to notify are already mapped.
type adapter interface {
	test(ctx context.Context) (version string, libs []Library, err error)
	notify(ctx context.Context, ev Event) error
	// open returns the page of the title on the server, ok=false if the server
	// does not have it.
	open(ctx context.Context, q titleQuery) (url string, ok bool, err error)
}

// plain says what went wrong with a server in words the owner can act on.
func plain(err error) string {
	switch {
	case err == nil:
		return ""
	case errors.Is(err, ErrAuth):
		return "The server refused the token. Check the API key (Jellyfin and Emby) or the token (Plex)."
	case errors.Is(err, ErrUnreachable):
		detail := strings.TrimPrefix(err.Error(), ErrUnreachable.Error()+": ")
		return "Marquee could not reach the server. " + shorten(detail, 200)
	}
	var ie *InvalidError
	if errors.As(err, &ie) {
		return ie.Message
	}
	return shorten(err.Error(), 300)
}

func shorten(s string, n int) string {
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	return string(r[:n]) + "..."
}

func unreachable(err error) error { return fmt.Errorf("%w: %w", ErrUnreachable, err) }
