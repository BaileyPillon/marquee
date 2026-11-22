package search

import (
	"context"
	"time"

	"github.com/BaileyPillon/marquee/internal/core/db"
)

// AlbumSearched returns when each album of a copy was last searched for, keyed
// by album id.
func (s *Store) AlbumSearched(ctx context.Context, copyID int64) (map[int64]time.Time, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT album_id, at FROM album_searches WHERE copy_id = ?`, copyID)
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

// NoteAlbumSearch records that an album of a copy was searched for just now.
func (s *Store) NoteAlbumSearch(ctx context.Context, copyID, albumID int64, at time.Time, grabbed bool) error {
	g := 0
	if grabbed {
		g = 1
	}
	_, err := s.db.ExecContext(ctx,
		`INSERT INTO album_searches (copy_id, album_id, at, grabbed) VALUES (?, ?, ?, ?)
		 ON CONFLICT (copy_id, album_id) DO UPDATE SET at = excluded.at, grabbed = excluded.grabbed`,
		copyID, albumID, db.TS(at), g)
	return err
}
