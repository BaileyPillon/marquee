// Package history is the story of each title: when it was asked for, approved,
// grabbed, imported, upgraded or failed. Other modules call Record as things
// happen; the title page reads the list back.
//
// It sits just above the core (it only needs the database) so every layer can
// write to it. The table is in the main database, not the events one, so a
// title's history is one plain query away from the rest of the title.
package history

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"log/slog"
	"time"
)

// Events a row can hold.
const (
	Grabbed   = "grabbed"
	Imported  = "imported"
	Upgraded  = "upgraded"
	Failed    = "failed"
	Deleted   = "deleted"
	Renamed   = "renamed"
	Requested = "requested"
	Approved  = "approved"
	Declined  = "declined"
)

var validEvents = map[string]bool{
	Grabbed: true, Imported: true, Upgraded: true, Failed: true, Deleted: true,
	Renamed: true, Requested: true, Approved: true, Declined: true,
}

// ErrInvalid is returned for an entry the table would refuse.
var ErrInvalid = errors.New("history: an entry needs a title and a known event")

// Entry is one thing that happened to a title. CopyID and DownloadID are 0 when
// they do not apply. Detail is anything that marshals to a JSON object.
type Entry struct {
	TitleID    int64
	CopyID     int64
	DownloadID int64
	Event      string
	Detail     any
}

// Item is one row as the API shows it.
type Item struct {
	ID         int64           `json:"id"`
	At         time.Time       `json:"at"`
	TitleID    int64           `json:"titleId"`
	CopyID     *int64          `json:"copyId"`
	DownloadID *int64          `json:"downloadId"`
	Event      string          `json:"event"`
	Detail     json.RawMessage `json:"detail"`
}

// Recorder is the part of the service other modules need.
type Recorder interface {
	Record(ctx context.Context, e Entry) error
}

// Options configure a Service.
type Options struct {
	Logger *slog.Logger
	Now    func() time.Time
}

// Service reads and writes the history.
type Service struct {
	store *Store
	log   *slog.Logger
	now   func() time.Time
}

var _ Recorder = (*Service)(nil)

// New builds the service on the main database.
func New(d *sql.DB, o Options) *Service {
	if o.Logger == nil {
		o.Logger = slog.Default()
	}
	if o.Now == nil {
		o.Now = time.Now
	}
	return &Service{store: NewStore(d), log: o.Logger, now: o.Now}
}

// Record stores one entry.
func (s *Service) Record(ctx context.Context, e Entry) error {
	if e.TitleID <= 0 || !validEvents[e.Event] {
		return ErrInvalid
	}
	detail := "{}"
	if e.Detail != nil {
		b, err := json.Marshal(e.Detail)
		if err != nil {
			return err
		}
		if len(b) > 0 && string(b) != "null" {
			detail = string(b)
		}
	}
	return s.store.Add(ctx, s.now(), e, detail)
}

// Note records an entry and logs, instead of returning, a failure: history is a
// record of what happened, never a reason for something else to stop.
func (s *Service) Note(ctx context.Context, e Entry) {
	if s == nil {
		return
	}
	if err := s.Record(context.WithoutCancel(ctx), e); err != nil {
		s.log.Warn("could not record history", "event", e.Event, "title", e.TitleID, "err", err)
	}
}

// Limits of ForTitle.
const (
	DefaultLimit = 50
	MaxLimit     = 500
)

// ForTitle lists a title's history, newest first.
func (s *Service) ForTitle(ctx context.Context, titleID int64, limit int) ([]Item, error) {
	if limit <= 0 {
		limit = DefaultLimit
	}
	limit = min(limit, MaxLimit)
	return s.store.ForTitle(ctx, titleID, limit)
}
