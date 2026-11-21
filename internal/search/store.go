package search

import (
	"context"
	"database/sql"
	"encoding/json"
	"time"

	"github.com/BaileyPillon/marquee/internal/core/db"
)

// Store is the only place in this package that talks SQL.
type Store struct {
	db *sql.DB
}

// NewStore wraps the main database.
func NewStore(d *sql.DB) *Store { return &Store{db: d} }

// History is one row of search_history.
type History struct {
	ID             int64     `json:"id"`
	CopyID         int64     `json:"copyId"`
	At             time.Time `json:"at"`
	IndexersOK     int       `json:"indexersOk"`
	IndexersFailed int       `json:"indexersFailed"`
	Results        int       `json:"results"`
	BestGUID       string    `json:"bestGuid"`
	Grabbed        int       `json:"grabbed"`
	GrabbedFor     []string  `json:"grabbedFor"`
	Note           string    `json:"note"`
}

// AddHistory records one search.
func (s *Store) AddHistory(ctx context.Context, h History) error {
	if h.GrabbedFor == nil {
		h.GrabbedFor = []string{}
	}
	keys, _ := json.Marshal(h.GrabbedFor)
	_, err := s.db.ExecContext(ctx,
		`INSERT INTO search_history (copy_id, at, indexers_ok, indexers_failed, results, best_guid, grabbed, grabbed_for, note)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		h.CopyID, db.TS(h.At), h.IndexersOK, h.IndexersFailed, h.Results, h.BestGUID, h.Grabbed, string(keys), h.Note)
	return err
}

// History lists the latest searches of a copy, newest first.
func (s *Store) History(ctx context.Context, copyID int64, limit int) ([]History, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT id, copy_id, at, indexers_ok, indexers_failed, results, best_guid, grabbed, grabbed_for, note
		 FROM search_history WHERE copy_id = ? ORDER BY at DESC, id DESC LIMIT ?`, copyID, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []History
	for rows.Next() {
		var (
			h    History
			at   string
			keys string
		)
		if err := rows.Scan(&h.ID, &h.CopyID, &at, &h.IndexersOK, &h.IndexersFailed, &h.Results, &h.BestGUID,
			&h.Grabbed, &keys, &h.Note); err != nil {
			return nil, err
		}
		if h.At, err = db.ParseTS(at); err != nil {
			return nil, err
		}
		_ = json.Unmarshal([]byte(keys), &h.GrabbedFor)
		out = append(out, h)
	}
	return out, rows.Err()
}

// LastSearched returns when each copy was last searched.
func (s *Store) LastSearched(ctx context.Context) (map[int64]time.Time, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT copy_id, MAX(at) FROM search_history GROUP BY copy_id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[int64]time.Time{}
	for rows.Next() {
		var (
			id int64
			at string
		)
		if err := rows.Scan(&id, &at); err != nil {
			return nil, err
		}
		t, err := db.ParseTS(at)
		if err != nil {
			return nil, err
		}
		out[id] = t
	}
	return out, rows.Err()
}

// RecentlyGrabbed returns the keys ("movie", "s01e04", "s01") of what was sent
// for download for a copy since the given time.
func (s *Store) RecentlyGrabbed(ctx context.Context, copyID int64, since time.Time) (map[string]bool, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT grabbed_for FROM search_history WHERE copy_id = ? AND grabbed > 0 AND at >= ?`, copyID, db.TS(since))
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[string]bool{}
	for rows.Next() {
		var text string
		if err := rows.Scan(&text); err != nil {
			return nil, err
		}
		var keys []string
		if json.Unmarshal([]byte(text), &keys) == nil {
			for _, k := range keys {
				out[k] = true
			}
		}
	}
	return out, rows.Err()
}

// PruneHistory forgets searches older than the cutoff.
func (s *Store) PruneHistory(ctx context.Context, before time.Time) (int64, error) {
	res, err := s.db.ExecContext(ctx, `DELETE FROM search_history WHERE at < ?`, db.TS(before))
	if err != nil {
		return 0, err
	}
	return res.RowsAffected()
}
