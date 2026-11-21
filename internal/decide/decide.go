// Package decide holds the quality profiles, the blocklist, and the rules that
// turn a release name into a decision: is this the thing we want, how good is
// it, and why. Every verdict carries reasons in plain words, because the owner
// reads them on the "Releases found" table.
//
// Scoring is pure: Score takes a profile, a title and one parsed release and
// returns a verdict. The search module does the fetching and the grabbing.
package decide

import (
	"context"
	"errors"
	"strings"
	"time"
	"unicode/utf8"
)

// Errors the web layer maps to HTTP statuses.
var (
	ErrNotFound      = errors.New("decide: not found")
	ErrDuplicateName = errors.New("decide: another profile of that kind already has that name")
)

// InvalidError is a request the caller can fix; Message is plain words.
type InvalidError struct{ Message string }

func (e *InvalidError) Error() string { return e.Message }

func invalid(msg string) error { return &InvalidError{Message: msg} }

// Options configure the service.
type Options struct {
	Now func() time.Time
}

// Service reads and changes profiles and the blocklist.
type Service struct {
	store *Store
	now   func() time.Time
}

// New builds the service.
func New(store *Store, o Options) *Service {
	if o.Now == nil {
		o.Now = time.Now
	}
	return &Service{store: store, now: o.Now}
}

// ---- profiles ----------------------------------------------------------------

// Profiles lists every profile.
func (s *Service) Profiles(ctx context.Context) ([]Profile, error) { return s.store.Profiles(ctx) }

// Profile returns the profile for a kind and quality ("movie", grab.QualityUHD).
func (s *Service) Profile(ctx context.Context, kind, quality string) (Profile, error) {
	return s.store.ProfileFor(ctx, kind, quality)
}

// ProfileInput is everything about a profile the owner can change. Kind and
// quality are fixed when the profile is created.
type ProfileInput struct {
	Name             string   `json:"name"`
	AllowedRes       []int    `json:"allowedRes"`
	PreferredRes     int      `json:"preferredRes"`
	AllowedSources   []string `json:"allowedSources"`
	MinSizeMB        *int     `json:"minSizeMb"`
	MaxSizeMB        *int     `json:"maxSizeMb"`
	MaxSizePerHourMB *int     `json:"maxSizePerHourMb"`
	PreferHDR        bool     `json:"preferHdr"`
	PreferredWords   []string `json:"preferredWords"`
	RejectedWords    []string `json:"rejectedWords"`
	MinSeeders       int      `json:"minSeeders"`
	UpgradeUntil     *string  `json:"upgradeUntil"`
}

// InputOf is the editable side of a profile, to be changed and sent back.
func InputOf(p Profile) ProfileInput {
	return ProfileInput{
		Name: p.Name, AllowedRes: p.AllowedRes, PreferredRes: p.PreferredRes, AllowedSources: p.AllowedSources,
		MinSizeMB: p.MinSizeMB, MaxSizeMB: p.MaxSizeMB, MaxSizePerHourMB: p.MaxSizePerHourMB,
		PreferHDR: p.PreferHDR, PreferredWords: p.PreferredWords, RejectedWords: p.RejectedWords,
		MinSeeders: p.MinSeeders, UpgradeUntil: p.UpgradeUntil,
	}
}

// ProfileByID returns one profile.
func (s *Service) ProfileByID(ctx context.Context, id int64) (Profile, error) {
	return s.store.ProfileByID(ctx, id)
}

// UpdateProfile replaces the editable fields of a profile.
func (s *Service) UpdateProfile(ctx context.Context, id int64, in ProfileInput) (Profile, error) {
	p, err := s.store.ProfileByID(ctx, id)
	if err != nil {
		return Profile{}, err
	}
	p.Name, p.AllowedRes, p.PreferredRes, p.AllowedSources = in.Name, in.AllowedRes, in.PreferredRes, in.AllowedSources
	p.MinSizeMB, p.MaxSizeMB, p.MaxSizePerHourMB = in.MinSizeMB, in.MaxSizeMB, in.MaxSizePerHourMB
	p.PreferHDR, p.PreferredWords, p.RejectedWords = in.PreferHDR, in.PreferredWords, in.RejectedWords
	p.MinSeeders, p.UpgradeUntil = in.MinSeeders, in.UpgradeUntil
	if err := p.Validate(); err != nil {
		return Profile{}, err
	}
	if err := s.store.UpdateProfile(ctx, p); err != nil {
		return Profile{}, err
	}
	return s.store.ProfileByID(ctx, id)
}

// ---- blocklist ---------------------------------------------------------------

const maxReasonRunes = 300

// Block turns a release down for a title for good. The importer calls it when a
// download turns out bad; the owner can undo it from the blocklist.
func (s *Service) Block(ctx context.Context, e BlockEntry) (BlockEntry, error) {
	if e.TitleID <= 0 {
		return BlockEntry{}, invalid("A blocked release belongs to a title.")
	}
	e.GUID = strings.TrimSpace(e.GUID)
	e.InfoHash = strings.ToLower(strings.TrimSpace(e.InfoHash))
	e.ReleaseTitle = strings.TrimSpace(e.ReleaseTitle)
	if e.GUID == "" && e.InfoHash == "" && e.ReleaseTitle == "" {
		return BlockEntry{}, invalid("Say which release to block.")
	}
	e.Reason = strings.TrimSpace(e.Reason)
	if utf8.RuneCountInString(e.Reason) > maxReasonRunes {
		e.Reason = string([]rune(e.Reason)[:maxReasonRunes])
	}
	e.CreatedAt = s.now()
	return s.store.AddBlock(ctx, e)
}

// Blocklist lists entries, newest first.
func (s *Service) Blocklist(ctx context.Context, limit, offset int) ([]BlockEntry, int, error) {
	if limit <= 0 || limit > 500 {
		limit = 100
	}
	return s.store.Blocklist(ctx, limit, max(offset, 0))
}

// Unblock removes an entry.
func (s *Service) Unblock(ctx context.Context, id int64) error { return s.store.DeleteBlock(ctx, id) }

// BlockIndex answers "is this release blocked?" for one title without a query
// per release.
type BlockIndex struct {
	entries []BlockEntry
}

// BlocksFor loads the blocklist entries of one title.
func (s *Service) BlocksFor(ctx context.Context, titleID int64) (*BlockIndex, error) {
	list, err := s.store.BlocksForTitle(ctx, titleID)
	if err != nil {
		return nil, err
	}
	return &BlockIndex{entries: list}, nil
}

// NewBlockIndex builds an index from entries (for tests and callers that
// already hold them).
func NewBlockIndex(entries []BlockEntry) *BlockIndex { return &BlockIndex{entries: entries} }

// Reason reports whether a release is blocked and why. A release matches by
// its feed id, by its info hash, or by its exact title.
func (b *BlockIndex) Reason(guid, infoHash, title string) (string, bool) {
	if b == nil {
		return "", false
	}
	for _, e := range b.entries {
		hit := (e.GUID != "" && e.GUID == guid) ||
			(e.InfoHash != "" && infoHash != "" && strings.EqualFold(e.InfoHash, infoHash)) ||
			(e.ReleaseTitle != "" && e.GUID == "" && e.InfoHash == "" && strings.EqualFold(e.ReleaseTitle, title))
		if hit {
			if e.Reason == "" {
				return "no reason was given", true
			}
			return e.Reason, true
		}
	}
	return "", false
}
