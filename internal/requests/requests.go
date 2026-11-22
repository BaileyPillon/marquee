// Package requests is how people ask for things: an ask names a title and a
// quality (HD or 4K), waits for the owner's yes unless the rules let it
// through, and once approved becomes a wanted copy of the title. A 4K copy is a
// separate copy from the HD one and goes to its own library folder.
//
// Service.Ask is the single way in. The HTTP API calls it today; the MCP server
// and the watchlist sync call it later, as the person the ask is for.
//
// The rules, in short:
//
//   - The owner's and the admins' own asks are approved at once.
//   - A member's HD ask is approved at once when auto-approve for HD is on for
//     them (their own setting, else the default), and it fits their weekly limit.
//   - A member's 4K ask waits for the owner or an admin, unless auto-approve for
//     4K is on for them.
//   - Weekly limits count over a rolling 7 days, per quality, every ask that was
//     not declined or cancelled. They apply to members; the owner and admins
//     have none unless one was set for them by name.
//   - One open (pending, approved or available) ask per title and quality.
//
// SQL lives in store.go. The package reads the titles and users tables for
// display only (joins in store.go) and talks to the catalog, the library and
// the settings through their services.
package requests

import (
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/BaileyPillon/marquee/internal/grab"
	"github.com/BaileyPillon/marquee/internal/titles"
)

// Kinds of title (the catalog's own words).
const (
	KindMovie  = titles.KindMovie
	KindShow   = titles.KindShow
	KindArtist = titles.KindArtist
)

// Qualities (see grab).
const (
	QualityHD  = grab.QualityHD
	QualityUHD = grab.QualityUHD
)

// Request states.
const (
	StatePending   = "pending"
	StateApproved  = "approved"
	StateDeclined  = "declined"
	StateAvailable = "available"
	StatePartial   = "partial"
	StateFailed    = "failed"
	StateCancelled = "cancelled"
)

// States lists every state.
var States = []string{StatePending, StateApproved, StateDeclined, StateAvailable, StatePartial, StateFailed, StateCancelled}

// Where an ask came from.
const (
	SourceManual    = "manual"
	SourceWatchlist = "watchlist"
	SourceMCP       = "mcp"
	SourceList      = "list"
)

// Event topic on the hub. A payload is {"event", "id", "state"}; event is
// created, approved, declined, cancelled or state. It carries no names or
// titles, because every signed-in person can read the stream.
const Topic = "requests"

// SettingsSection is the settings section that holds the defaults.
const SettingsSection = "requests"

// Window is how far back the weekly limits look.
const Window = 7 * 24 * time.Hour

// Limits of what a person may type.
const (
	maxNoteRunes   = 500
	maxReasonRunes = 500
	maxPerWeek     = 10000
)

// Errors the web layer maps to HTTP statuses.
var (
	ErrNotFound       = errors.New("requests: no such request")
	ErrCopyNotFound   = errors.New("requests: no such copy")
	ErrUserNotFound   = errors.New("requests: no such person")
	ErrForbidden      = errors.New("requests: not allowed")
	ErrNotPending     = errors.New("requests: that request is not waiting for an answer")
	ErrNotCancellable = errors.New("requests: that request can no longer be cancelled")
	ErrNoFolder       = errors.New("requests: no library folder for this kind of title")
	ErrBadState       = errors.New("requests: that state change is not allowed")
)

// InvalidError is an ask the caller can fix. Code is stable (the UI keys off
// it); Message is plain words.
type InvalidError struct{ Code, Message string }

func (e *InvalidError) Error() string { return e.Message }

func invalid(code, msg string) error { return &InvalidError{Code: code, Message: msg} }

// LimitError means the person has used up this week's asks of one quality.
// ResetsAt is when a place frees up again; nil when no wait will help (the
// limit is 0).
type LimitError struct {
	Quality  string
	Used     int
	Limit    int
	ResetsAt *time.Time
}

func (e *LimitError) Error() string {
	return fmt.Sprintf("requests: weekly %s limit reached (%d of %d)", e.Quality, e.Used, e.Limit)
}

// DuplicateError means the title is already asked for at that quality. Existing
// is that ask, as the caller may see it (someone else's ask is shown without
// the person's name, note or answer to a member).
type DuplicateError struct{ Existing Item }

func (e *DuplicateError) Error() string { return "requests: already requested" }

// duplicateID is the store's answer; the service turns it into a DuplicateError.
type duplicateID int64

func (d duplicateID) Error() string { return "requests: already requested" }

// ---- what an ask is --------------------------------------------------------

// Scope says how much of a title was asked for: {} for a movie,
// {"seasons":[1,2]} or {"all":true} for a show, {"albums":[ids]} or
// {"all":true} for an artist.
type Scope struct {
	All     bool    `json:"all,omitempty"`
	Seasons []int   `json:"seasons,omitempty"`
	Albums  []int64 `json:"albums,omitempty"`
}

// Actor is who an ask is for and how it arrived.
type Actor struct {
	UserID int64
	Role   string // owner, admin or member (accounts.Role)
	Source string // manual, watchlist, mcp or list; empty means manual
}

// AskInput is one ask. Name the title by TitleID, or by Kind, Provider and
// ProviderID (a title that is not in the catalog yet is added, unmonitored,
// until the ask is approved).
type AskInput struct {
	TitleID    int64
	Kind       string
	Provider   string
	ProviderID string
	Quality    string
	Scope      Scope
	Note       string
	SourceRef  string // an id from where the ask came from (a watchlist item, say)
}

// UserRef is a person as named on a request.
type UserRef struct {
	ID   int64  `json:"id"`
	Name string `json:"name"`
}

// TitleBrief is the little of a title a request list shows.
type TitleBrief struct {
	ID        int64  `json:"id"`
	Name      string `json:"name"`
	Year      int    `json:"year"`
	Kind      string `json:"kind"`
	PosterRef string `json:"posterRef"`
}

// Facts are details about where a request stands on disk.
type Facts struct {
	// SizeEstimate is the expected size in bytes; null until sources can say.
	SizeEstimate *int64 `json:"sizeEstimate"`
	// FolderPath is where the copy goes (or would go, while the ask waits).
	FolderPath string `json:"folderPath"`
}

// Item is one request as people see it.
type Item struct {
	ID            int64      `json:"id"`
	Title         TitleBrief `json:"title"`
	User          *UserRef   `json:"user"`
	Quality       string     `json:"quality"`
	Scope         Scope      `json:"scope"`
	Note          string     `json:"note"`
	State         string     `json:"state"`
	Source        string     `json:"source"`
	CreatedAt     time.Time  `json:"createdAt"`
	UpdatedAt     time.Time  `json:"updatedAt"`
	DecidedBy     *UserRef   `json:"decidedBy"` // null while waiting, and when the rules approved it
	DecidedAt     *time.Time `json:"decidedAt"`
	DeclineReason string     `json:"declineReason"`
	Facts         Facts      `json:"facts"`

	userID   int64 // the person who asked
	folderID int64 // the copy's folder, 0 when there is no copy yet
}

// ListFilter selects requests for List.
type ListFilter struct {
	State   string // "" means every state
	Mine    bool   // only the caller's own (members always get only their own)
	TitleID int64  // only requests for this title; 0 means every title
	Limit   int    // default 50, at most 200
	Offset  int
}

// List limits.
const (
	DefaultLimit = 50
	MaxLimit     = 200
)

// ListResult is one page of requests and how many match in all.
type ListResult struct {
	Items  []Item `json:"items"`
	Total  int    `json:"total"`
	Limit  int    `json:"limit"`
	Offset int    `json:"offset"`
}

// ---- usage and limits ------------------------------------------------------

// Unlimited is the number that stands for "no weekly limit" wherever a limit is
// stored: a person's own setting (a person the owner lets ask for as much as they
// like, whatever the default is) and the defaults. The API never shows it: a limit
// that is not there is null, with "unlimited": true beside it.
const Unlimited = -1

// QualityUsage is one quality's asks over the last 7 days.
type QualityUsage struct {
	Used int `json:"used"`
	// Limit is null when there is none; Unlimited says so in words.
	Limit     *int `json:"limit"`
	Unlimited bool `json:"unlimited"`
	// ResetsAt is when the next ask drops out of the window (and a place frees
	// up, if the limit is used up); null when nothing was asked.
	ResetsAt *time.Time `json:"resetsAt"`
}

// Usage is a person's asks over the last 7 days.
type Usage struct {
	HD  QualityUsage `json:"hd"`
	UHD QualityUsage `json:"uhd"`
}

// AskResult is what Ask returns.
type AskResult struct {
	Request      Item  `json:"request"`
	AutoApproved bool  `json:"autoApproved"`
	Usage        Usage `json:"usage"`
}

// Summary is the numbers for the home screen.
type Summary struct {
	// WaitingForYou counts asks waiting for an answer; only the owner and
	// admins have any (it is 0 for a member).
	WaitingForYou int `json:"waitingForYou"`
	Mine          struct {
		Pending  int `json:"pending"`
		Approved int `json:"approved"`
	} `json:"mine"`
	Usage Usage `json:"usage"`
	// Owner names the owner, so a member's screen can say who has to say yes. It is
	// left out only when there is no owner yet.
	Owner *OwnerRef `json:"owner,omitempty"`
}

// OwnerRef is the owner as a summary names them.
type OwnerRef struct {
	Name string `json:"name"`
}

// EffectiveLimits are the limits and flags that apply to a person right now. A
// limit that is nil means there is none; the Unlimited flags say so in words for
// the API.
type EffectiveLimits struct {
	HDPerWeek      *int `json:"hdPerWeek"` // null: no limit
	HDUnlimited    bool `json:"hdUnlimited"`
	UHDPerWeek     *int `json:"uhdPerWeek"`
	UHDUnlimited   bool `json:"uhdUnlimited"`
	AutoApproveHD  bool `json:"autoApproveHD"`
	AutoApproveUHD bool `json:"autoApproveUHD"`
}

// LimitOverrides are what the owner set for one person; nil means "use the
// default". A limit holding Unlimited (-1) is "no limit for this person"; the API
// shows that as null with the matching Unlimited flag true (see MarshalJSON), so
// null with the flag false still means "use the default".
type LimitOverrides struct {
	HDPerWeek      *int
	UHDPerWeek     *int
	AutoApproveHD  *bool
	AutoApproveUHD *bool
}

// MarshalJSON writes the overrides the way the API shows them: a limit that is
// Unlimited becomes null plus hdUnlimited/uhdUnlimited true.
func (o LimitOverrides) MarshalJSON() ([]byte, error) {
	split := func(p *int) (*int, bool) {
		if p != nil && *p == Unlimited {
			return nil, true
		}
		return p, false
	}
	hd, hdU := split(o.HDPerWeek)
	uhd, uhdU := split(o.UHDPerWeek)
	return json.Marshal(struct {
		HDPerWeek      *int  `json:"hdPerWeek"`
		HDUnlimited    bool  `json:"hdUnlimited"`
		UHDPerWeek     *int  `json:"uhdPerWeek"`
		UHDUnlimited   bool  `json:"uhdUnlimited"`
		AutoApproveHD  *bool `json:"autoApproveHD"`
		AutoApproveUHD *bool `json:"autoApproveUHD"`
	}{hd, hdU, uhd, uhdU, o.AutoApproveHD, o.AutoApproveUHD})
}

// PersonLimits groups both.
type PersonLimits struct {
	Effective EffectiveLimits `json:"effective"`
	Overrides LimitOverrides  `json:"overrides"`
}

// AutoApprove says which of a person's asks are approved without waiting.
type AutoApprove struct {
	HD  bool `json:"hd"`
	UHD bool `json:"uhd"`
}

// PersonInfo is one row of the People & limits screen.
type PersonInfo struct {
	ID          int64        `json:"id"`
	Name        string       `json:"name"`
	Role        string       `json:"role"`
	Disabled    bool         `json:"disabled"`
	Limits      PersonLimits `json:"limits"`
	Usage       Usage        `json:"usage"`
	AutoApprove AutoApprove  `json:"autoApprove"`
}

// PeopleResult is everyone, with the defaults the overrides sit on.
type PeopleResult struct {
	People   []PersonInfo `json:"people"`
	Defaults Config       `json:"defaults"`
}

// Patch is one field of a limits change: not Set means leave it, Set with a nil
// Value clears the override, Set with a Value sets it.
type Patch[T any] struct {
	Set   bool
	Value *T
}

// LimitsPatch is a change to one person's overrides.
type LimitsPatch struct {
	HDPerWeek      Patch[int]
	UHDPerWeek     Patch[int]
	AutoApproveHD  Patch[bool]
	AutoApproveUHD Patch[bool]
}

// ApproveOptions are the choices that come with approving.
type ApproveOptions struct {
	// AlwaysAllowUHD turns on auto-approve for 4K for the person who asked.
	AlwaysAllowUHD bool
}

// ---- helpers ---------------------------------------------------------------

func isStaff(role string) bool { return role == "owner" || role == "admin" }

func validQuality(q string) bool { return q == QualityHD || q == QualityUHD }

func validState(s string) bool {
	for _, v := range States {
		if v == s {
			return true
		}
	}
	return false
}

func validSource(s string) bool {
	switch s {
	case SourceManual, SourceWatchlist, SourceMCP, SourceList:
		return true
	}
	return false
}

// libraryKind is the library folder kind that holds a title kind.
func libraryKind(titleKind string) string {
	if titleKind == KindArtist {
		return "music"
	}
	return titleKind
}

// resetsAt works out when the next place frees up from the creation times of
// the asks in the window (oldest first). With a limit that is used up it is
// when enough asks have aged out to get below it; otherwise when the oldest ask
// ages out. It is nil when there is nothing to wait for.
func resetsAt(times []time.Time, limit *int) *time.Time {
	if len(times) == 0 {
		return nil
	}
	i := 0
	if limit != nil {
		if *limit <= 0 {
			return nil
		}
		if len(times) >= *limit {
			i = len(times) - *limit
		}
	}
	t := times[i].Add(Window)
	return &t
}
