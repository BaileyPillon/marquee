// Package watchlists turns Plex watchlists into asks. The owner links their Plex
// account (the PIN flow); from then on a check every 15 minutes reads the
// owner's watchlist and, if the owner allows it, the watchlists of their Plex
// friends. An item becomes an ask through the one entry point the requests
// module has, as the person it belongs to: the owner's own items as the owner,
// a friend's items as the Marquee person the friend is linked to. Weekly limits
// and auto-approve rules therefore apply as for any other ask.
//
// Each item is looked at once. It ends as an ask (request_id), or as a skip
// that names the reason: some reasons are final (the item has no TMDB id,
// somebody already asked for it), and some can pass and are tried again at the
// next check (the person's weekly limit is used up, the owner has no TMDB key
// yet, the friend is not linked yet).
//
// The package imports only layers below it. The requests module (the same layer)
// is reached through the Asker interface, which the program wires in.
//
// Which Plex endpoints this code uses is written down in plex.go. They are
// built from Plex's public documentation and community knowledge, and have not
// been run against the real service: the client is tolerant of missing fields
// and of the answer's shape, and every call goes through an interface so a
// change on Plex's side stays inside plex.go.
package watchlists

import (
	"context"
	"errors"
	"time"

	"github.com/BaileyPillon/marquee/internal/metadata"
)

// Settings section and job.
const (
	SettingsSection = "watchlists"
	// ClientSection holds the stable identifier Marquee tells Plex it is.
	ClientSection = "watchlists-client"
	JobSync       = "watchlists.sync"
	SyncInterval  = 15 * time.Minute
	// Source is the value of watchlist_items.source for Plex.
	SourcePlex = "plex"
)

// Why an item did not become an ask. The codes are stable (the UI shows them).
const (
	SkipUnlinkedFriend  = "unlinked_friend"   // the friend is not linked to a Marquee person (can pass)
	SkipLimitReached    = "limit_reached"     // the person used up this week's asks (can pass)
	SkipTMDBKeyMissing  = "tmdb_key_missing"  // a movie needs the owner's TMDB key (can pass)
	SkipNoLibraryFolder = "no_library_folder" // there is no library folder to put it in (can pass)
	SkipUserDisabled    = "user_disabled"     // the linked person is switched off (can pass)
	SkipLookupFailed    = "lookup_failed"     // Plex or the metadata provider did not answer (can pass)
	SkipAskFailed       = "ask_failed"        // the ask failed for a reason that may pass (can pass)

	SkipNoTMDBID        = "no_tmdb_id"       // a movie with no TMDB id on Plex
	SkipNoMatch         = "no_match"         // a show that TVmaze could not find
	SkipNotFound        = "not_found"        // the provider has no such title
	SkipAlreadyAsked    = "already_asked"    // somebody already asked for it at that quality
	SkipUnsupportedType = "unsupported_type" // neither a movie nor a show
)

// retryable are the reasons that can pass; an item skipped for one is looked at
// again at the next check.
var retryable = map[string]bool{
	SkipUnlinkedFriend: true, SkipLimitReached: true, SkipTMDBKeyMissing: true, SkipNoLibraryFolder: true,
	SkipUserDisabled: true, SkipLookupFailed: true, SkipAskFailed: true,
}

// Errors the web layer maps to HTTP statuses.
var (
	ErrNotLinked         = errors.New("watchlists: no Plex account is linked")
	ErrDisabled          = errors.New("watchlists: Plex watchlists are switched off")
	ErrRunning           = errors.New("watchlists: a check is already running")
	ErrPinExpired        = errors.New("watchlists: that Plex link request has expired; start again")
	ErrUnknownFriend     = errors.New("watchlists: no such Plex friend")
	ErrUserNotFound      = errors.New("watchlists: no such person")
	ErrUserAlreadyLinked = errors.New("watchlists: that person is already linked to another Plex friend")
	ErrPlexAuth          = errors.New("watchlists: Plex did not accept the link; link the account again")
)

// Errors the Asker and Members adapters report in the package's own words.
var (
	ErrLimitReached = errors.New("watchlists: weekly limit reached")
	ErrAlreadyAsked = errors.New("watchlists: already asked for")
	ErrNoFolder     = errors.New("watchlists: no library folder for this kind of title")
	ErrNameTaken    = errors.New("watchlists: that name is taken")
)

// ---- what the package needs from the rest of the program -------------------

// AskInput is one ask for a person, made from a watchlist item.
type AskInput struct {
	UserID     int64
	Role       string // owner, admin or member
	Kind       string // movie or show
	Provider   string // tmdb for a movie, tvmaze for a show
	ProviderID string
	Quality    string // hd or uhd
	SourceRef  string
}

// AskResult is what an ask became.
type AskResult struct {
	RequestID    int64
	AutoApproved bool
}

// Asker makes an ask, as the person AskInput names and with the source
// "watchlist". It reports a used-up weekly limit as ErrLimitReached, a title
// that is already asked for as ErrAlreadyAsked and a missing library folder as
// ErrNoFolder; everything else comes back as it is (the catalog's own errors
// included).
type Asker interface {
	Ask(ctx context.Context, in AskInput) (AskResult, error)
}

// Members makes a Marquee person for a Plex friend. It reports a name that is
// already taken as ErrNameTaken.
type Members interface {
	CreateMember(ctx context.Context, name string) (id int64, err error)
}

// ShowFinder is the part of the metadata module that finds a show by its IMDb id
// or by name. metadata.ShowProvider satisfies it.
type ShowFinder interface {
	ShowByIMDb(ctx context.Context, imdbID string) (*metadata.SearchHit, error)
	SearchShows(ctx context.Context, query string) ([]metadata.SearchHit, error)
}

// ---- what the package reports ----------------------------------------------

// Item is one entry on a watchlist.
type Item struct {
	Key   string // the item's id at Plex
	Title string
	Year  int
	Type  string   // movie or show
	Guids []string // tmdb://603, imdb://tt0133093, tvdb://81189
}

// PlexUser is a Plex account: the owner, or one of their friends.
type PlexUser struct {
	ID          string
	Username    string
	DisplayName string
}

// Friend is a Plex friend as the owner sees them, with the Marquee person they
// are linked to.
type Friend struct {
	PlexUserID     string  `json:"plexUserId"`
	Username       string  `json:"username"`
	LinkedUserID   *int64  `json:"linkedUserId"`
	LinkedUserName *string `json:"linkedUserName"`
}

// Result is what one check did: asks made, and items skipped by reason. Items
// that were dealt with at an earlier check are not counted again.
type Result struct {
	Asked   int            `json:"asked"`
	Skipped map[string]int `json:"skipped"`
}

func newResult() Result { return Result{Skipped: map[string]int{}} }

// Status is the state of the Plex link and of the last check.
type Status struct {
	Linked            bool       `json:"linked"`
	User              *string    `json:"user"` // the linked Plex account's name
	Enabled           bool       `json:"enabled"`
	IncludeFriends    bool       `json:"includeFriends"`
	AutoCreateMembers bool       `json:"autoCreateMembers"`
	Quality           string     `json:"quality"`
	Friends           []Friend   `json:"friends"`
	LastCheckedAt     *time.Time `json:"lastCheckedAt"`
	LastError         string     `json:"lastError"`
	LastResult        Result     `json:"lastResult"`
}

// Link is a Plex link request: send the owner to AuthURL, then poll CheckLink
// with PinID.
type Link struct {
	PinID   int64  `json:"pinId"`
	Code    string `json:"code"`
	AuthURL string `json:"authUrl"`
}
