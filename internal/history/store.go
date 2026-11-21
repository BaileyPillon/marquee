package history

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

func nullInt(n int64) any {
	if n == 0 {
		return nil
	}
	return n
}

// Add inserts one row.
func (s *Store) Add(ctx context.Context, at time.Time, e Entry, detail string) error {
	_, err := s.db.ExecContext(ctx,
		`INSERT INTO history (at, title_id, copy_id, download_id, event, detail) VALUES (?, ?, ?, ?, ?, ?)`,
		db.TS(at), e.TitleID, nullInt(e.CopyID), nullInt(e.DownloadID), e.Event, detail)
	return err
}

// ForTitle returns a title's rows, newest first.
func (s *Store) ForTitle(ctx context.Context, titleID int64, limit int) ([]Item, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT id, at, title_id, copy_id, download_id, event, detail FROM history
		  WHERE title_id = ? ORDER BY at DESC, id DESC LIMIT ?`, titleID, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Item{}
	for rows.Next() {
		var (
			it       Item
			at       string
			copyID   sql.NullInt64
			download sql.NullInt64
			detail   string
		)
		if err := rows.Scan(&it.ID, &at, &it.TitleID, &copyID, &download, &it.Event, &detail); err != nil {
			return nil, err
		}
		if it.At, err = db.ParseTS(at); err != nil {
			return nil, err
		}
		if copyID.Valid {
			it.CopyID = &copyID.Int64
		}
		if download.Valid {
			it.DownloadID = &download.Int64
		}
		it.Detail = json.RawMessage(detail)
		out = append(out, it)
	}
	return out, rows.Err()
}
