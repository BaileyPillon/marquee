// Package downloads sends releases to the owner's download clients (qBittorrent,
// Deluge, SABnzbd) and follows them until they finish.
//
// It implements grab.Grabber: a release comes in, one enabled client of the
// right protocol is picked (lowest priority number first), the release is added
// there under Marquee's category, and a row in the downloads table tracks it. A
// poll job then reads each client's category every few seconds, moves the rows
// along, and tells the importer (grab.CompletedHandler) once when one finishes.
//
// SQL lives in store.go. Every call to a client or an indexer goes through
// core/outbound; the clients' passwords and keys are sealed with core/secrets
// and are never logged, returned, or put in an error.
package downloads

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/BaileyPillon/marquee/internal/core/outbound"
)

// Client kinds.
const (
	KindQBittorrent  = "qbittorrent"
	KindDeluge       = "deluge"
	KindSABnzbd      = "sabnzbd"
	KindTransmission = "transmission" // reserved: not supported yet
	KindNZBGet       = "nzbget"       // reserved: not supported yet
)

// Download states. A download moves queued -> downloading (or paused,
// stalled) -> completed -> importing -> imported, or ends failed or removed.
const (
	StateQueued      = "queued"
	StateDownloading = "downloading"
	StatePaused      = "paused"
	StateStalled     = "stalled"
	StateCompleted   = "completed" // finished in the client, waiting for the importer
	StateImporting   = "importing"
	StateImported    = "imported"
	StateFailed      = "failed"
	StateRemoved     = "removed" // gone from the client (or removed by the owner)
)

// Scheduling and events.
const (
	// Topic is the SSE topic every download event goes out on.
	Topic = "downloads"
	// JobPoll is the scheduler job name. Register it with PollTick as its
	// interval; the job itself decides whether this tick is due.
	JobPoll  = "downloads.poll"
	PollTick = 5 * time.Second

	activeInterval = 5 * time.Second
	idleInterval   = 60 * time.Second
	// dueSlack lets a tick that lands a hair early still count as due.
	dueSlack = 500 * time.Millisecond

	clientTimeout = 25 * time.Second
	// missesToRemove is how many polls in a row a download must be missing from
	// its client's list before it is marked removed.
	missesToRemove = 2

	// DefaultCategory is the label Marquee's downloads carry in the client.
	DefaultCategory = "marquee"
)

// activeStates are the states a poll tracks in the client.
var activeStates = []string{StateQueued, StateDownloading, StatePaused, StateStalled}

// busyStates are the ones that make the poll run every few seconds: a paused
// download is not changing, so it can wait for the slow pace.
var busyStates = []string{StateQueued, StateDownloading, StateStalled}

// openStates are everything that is not over yet (shown in the "active" list).
var openStates = []string{StateQueued, StateDownloading, StatePaused, StateStalled, StateCompleted, StateImporting}

// terminalStates end a download's life.
var terminalStates = []string{StateImported, StateFailed, StateRemoved}

func isTerminal(state string) bool { return inList(terminalStates, state) }

func inList(list []string, s string) bool {
	for _, v := range list {
		if v == s {
			return true
		}
	}
	return false
}

// ---- adapter contract --------------------------------------------------------

// AddInput is one thing to add to a client.
type AddInput struct {
	TorrentFile []byte // the .torrent file, fetched by Marquee
	MagnetURL   string
	NZBURL      string
	Name        string // a readable name for clients that take one (SABnzbd)
	Category    string
	SavePath    string // empty = the client's default
	Paused      bool
}

// Item is one download as its client reports it, already translated.
type Item struct {
	Ref      string // the client's own id
	Name     string
	State    string  // StateQueued, StateDownloading, StatePaused, StateStalled or StateFailed
	Progress float64 // 0 to 1
	Size     int64   // bytes
	Done     int64   // bytes
	SpeedBps int64
	ETA      int // seconds, -1 when unknown
	// SavePath is the folder the client saves into; ContentPath is the file or
	// folder it produced (both as the client sees them).
	SavePath    string
	ContentPath string
	Finished    bool   // the download is complete (the client may still be seeding)
	Error       string // plain words, set when State is StateFailed
}

// Adapter talks to one download client. Refs are the client's own ids:
// info-hash for qBittorrent, torrent id for Deluge, nzo_id for SABnzbd.
type Adapter interface {
	// Test signs in and returns the client's version.
	Test(ctx context.Context) (version string, err error)
	// Add sends a release and returns its ref.
	Add(ctx context.Context, in AddInput) (ref string, err error)
	// List returns every download in the category.
	List(ctx context.Context, category string) ([]Item, error)
	// Remove deletes a download (and its data, if asked). A download the client
	// does not have is not an error.
	Remove(ctx context.Context, ref string, deleteData bool) error
	Pause(ctx context.Context, ref string) error
	Resume(ctx context.Context, ref string) error
}

// AdapterConfig is everything needed to build an adapter. The secrets are in
// plain text here; they live only as long as the adapter does.
type AdapterConfig struct {
	Kind     string
	URL      string
	Username string
	Password string
	APIKey   string
	HTTP     *outbound.Client
}

// AdapterFactory builds an adapter. Tests replace the default.
type AdapterFactory func(AdapterConfig) (Adapter, error)

// NewAdapter builds the adapter for a client kind.
func NewAdapter(cfg AdapterConfig) (Adapter, error) {
	switch cfg.Kind {
	case KindQBittorrent:
		return newQBittorrent(cfg)
	case KindDeluge:
		return newDeluge(cfg)
	case KindSABnzbd:
		return newSABnzbd(cfg)
	}
	return nil, &InvalidError{Message: "Marquee cannot talk to " + displayKind(cfg.Kind) + " yet."}
}

// SupportedKind reports whether there is an adapter for the kind.
func SupportedKind(kind string) bool {
	switch kind {
	case KindQBittorrent, KindDeluge, KindSABnzbd:
		return true
	}
	return false
}

// ProtocolOf is the protocol a client kind downloads.
func ProtocolOf(kind string) string {
	switch kind {
	case KindSABnzbd, KindNZBGet:
		return "usenet"
	}
	return "torrent"
}

func displayKind(kind string) string {
	switch kind {
	case KindQBittorrent:
		return "qBittorrent"
	case KindDeluge:
		return "Deluge"
	case KindSABnzbd:
		return "SABnzbd"
	case KindTransmission:
		return "Transmission"
	case KindNZBGet:
		return "NZBGet"
	}
	return "that kind of client"
}

// ---- errors ------------------------------------------------------------------

// Adapter errors. Adapters wrap them, so callers use errors.Is.
var (
	// ErrUnreachable: the client did not answer (down, wrong address, timeout).
	ErrUnreachable = errors.New("the download client did not answer")
	// ErrAuth: the client answered but refused the sign-in or the key.
	ErrAuth = errors.New("the download client refused the sign-in")
	// ErrRejected: the client is fine but would not take this download.
	ErrRejected = errors.New("the download client refused this download")
)

// Error is what Grab and the owner's actions return. Code is stable (the UI and
// other modules key off it); Message is plain words for people.
type Error struct {
	Code    string
	Message string
	Err     error // the cause, never containing a key or a query string
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
	ErrNoClient           = &Error{Code: "no_client_for_protocol", Message: "There is no enabled download client for that kind of release. Add one in Settings."}
	ErrClientUnreachable  = &Error{Code: "client_unreachable", Message: "Marquee could not reach the download client."}
	ErrClientRejected     = &Error{Code: "client_rejected", Message: "The download client would not take that release."}
	ErrReleaseUnavailable = &Error{Code: "release_unavailable", Message: "Marquee could not fetch the release from its source."}
	ErrInvalidState       = &Error{Code: "invalid_state", Message: "That cannot be done to a download in its current state."}
	ErrClientInUse        = &Error{Code: "client_in_use", Message: "That client still has downloads in progress."}
	ErrNameTaken          = &Error{Code: "name_taken", Message: "Another download client already has that name."}
)

// Lookup errors.
var (
	ErrNotFound       = errors.New("downloads: no such download")
	ErrClientNotFound = errors.New("downloads: no such client")
)

// InvalidError is a bad value in what the owner sent; Message says what to fix.
type InvalidError struct{ Message string }

func (e *InvalidError) Error() string { return "downloads: " + e.Message }

// wrap builds an *Error from a base error and a cause.
func wrap(base *Error, cause error) *Error {
	return &Error{Code: base.Code, Message: base.Message, Err: cause}
}

// PlainError says what went wrong with a client in words the owner can act on.
// It never contains a key or a query string (adapters and outbound see to that).
func PlainError(err error) string {
	switch {
	case err == nil:
		return ""
	case errors.Is(err, ErrAuth):
		return "The client refused the sign-in. Check the username, password or API key."
	case errors.Is(err, ErrUnreachable):
		detail := strings.TrimPrefix(err.Error(), ErrUnreachable.Error()+": ")
		return "Marquee could not reach the client. " + shorten(detail, 200)
	case errors.Is(err, ErrRejected):
		return shorten(err.Error(), 300)
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
