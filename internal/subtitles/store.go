package subtitles

import (
	"context"
	"database/sql"
	"errors"
	"strings"
	"time"

	"github.com/BaileyPillon/marquee/internal/core/db"
)

// Store is the only place in this package that talks SQL.
type Store struct {
	db *sql.DB
}

// NewStore wraps the main database.
func NewStore(d *sql.DB) *Store { return &Store{db: d} }

// Subtitle is one subtitle file Marquee wrote.
type Subtitle struct {
	ID         int64     `json:"id"`
	FileID     int64     `json:"fileId"`
	Language   string    `json:"language"`
	HI         bool      `json:"hi"`
	Provider   string    `json:"provider"`
	ProviderID string    `json:"providerId"`
	Path       string    `json:"-"` // never shown: it is a path on the server
	Score      int       `json:"score"`
	CreatedAt  time.Time `json:"createdAt"`
}

const subtitleCols = `id, file_id, language, hi, provider, provider_id, path, score, created_at`

type scanner interface{ Scan(dest ...any) error }

func scanSubtitle(s scanner) (Subtitle, error) {
	var (
		x       Subtitle
		hi      int
		created string
	)
	if err := s.Scan(&x.ID, &x.FileID, &x.Language, &hi, &x.Provider, &x.ProviderID, &x.Path, &x.Score, &created); err != nil {
		return Subtitle{}, err
	}
	x.HI = hi != 0
	t, err := db.ParseTS(created)
	if err != nil {
		return Subtitle{}, err
	}
	x.CreatedAt = t
	return x, nil
}

func b2i(b bool) int {
	if b {
		return 1
	}
	return 0
}

// List returns the subtitles Marquee wrote for a file.
func (s *Store) List(ctx context.Context, fileID int64) ([]Subtitle, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT `+subtitleCols+` FROM subtitles WHERE file_id = ? ORDER BY language, hi`, fileID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Subtitle
	for rows.Next() {
		x, err := scanSubtitle(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, x)
	}
	return out, rows.Err()
}

// ForFiles returns the subtitles of many files at once, keyed by file id.
func (s *Store) ForFiles(ctx context.Context, ids []int64) (map[int64][]Subtitle, error) {
	out := map[int64][]Subtitle{}
	for len(ids) > 0 {
		n := min(len(ids), 400)
		args := make([]any, n)
		for i, id := range ids[:n] {
			args[i] = id
		}
		rows, err := s.db.QueryContext(ctx,
			`SELECT `+subtitleCols+` FROM subtitles WHERE file_id IN (`+strings.TrimSuffix(strings.Repeat("?,", n), ",")+`)`, args...)
		if err != nil {
			return nil, err
		}
		for rows.Next() {
			x, err := scanSubtitle(rows)
			if err != nil {
				rows.Close()
				return nil, err
			}
			out[x.FileID] = append(out[x.FileID], x)
		}
		if err := rows.Err(); err != nil {
			rows.Close()
			return nil, err
		}
		rows.Close()
		ids = ids[n:]
	}
	return out, nil
}

// Get returns one subtitle, or ErrNotFound.
func (s *Store) Get(ctx context.Context, id int64) (Subtitle, error) {
	x, err := scanSubtitle(s.db.QueryRowContext(ctx, `SELECT `+subtitleCols+` FROM subtitles WHERE id = ?`, id))
	if errors.Is(err, sql.ErrNoRows) {
		return Subtitle{}, ErrNotFound
	}
	return x, err
}

// Upsert records a subtitle Marquee wrote, replacing the row for the same file,
// language and hearing-impaired flag.
func (s *Store) Upsert(ctx context.Context, x Subtitle) (Subtitle, error) {
	_, err := s.db.ExecContext(ctx,
		`INSERT INTO subtitles (file_id, language, hi, provider, provider_id, path, score, created_at)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?)
		 ON CONFLICT (file_id, language, hi) DO UPDATE SET
		   provider = excluded.provider, provider_id = excluded.provider_id, path = excluded.path,
		   score = excluded.score, created_at = excluded.created_at`,
		x.FileID, x.Language, b2i(x.HI), x.Provider, x.ProviderID, x.Path, x.Score, db.TS(x.CreatedAt))
	if err != nil {
		return Subtitle{}, err
	}
	return scanSubtitle(s.db.QueryRowContext(ctx,
		`SELECT `+subtitleCols+` FROM subtitles WHERE file_id = ? AND language = ? AND hi = ?`, x.FileID, x.Language, b2i(x.HI)))
}

// Delete removes the row of a subtitle.
func (s *Store) Delete(ctx context.Context, id int64) error {
	res, err := s.db.ExecContext(ctx, `DELETE FROM subtitles WHERE id = ?`, id)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrNotFound
	}
	return nil
}

// OwnsPath reports whether Marquee wrote the subtitle file at path: some row
// names it.
func (s *Store) OwnsPath(ctx context.Context, path string) (bool, error) {
	var n int
	if err := s.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM subtitles WHERE path = ?`, path).Scan(&n); err != nil {
		return false, err
	}
	return n > 0, nil
}

// ---- searches ---------------------------------------------------------------------

// AddSearch records one look for a subtitle in one language.
func (s *Store) AddSearch(ctx context.Context, fileID int64, language string, at time.Time, result string) error {
	_, err := s.db.ExecContext(ctx,
		`INSERT INTO subtitle_searches (file_id, language, at, result) VALUES (?, ?, ?, ?)`, fileID, language, db.TS(at), result)
	return err
}

// SearchState reads the log of one file and language, newest first, and says how
// many misses ("none") in a row end it and when the last one was. An "error"
// newest-first, or a "found", means no misses: the next look is not held back.
func (s *Store) SearchState(ctx context.Context, fileID int64, language string) (misses int, last time.Time, err error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT result, at FROM subtitle_searches WHERE file_id = ? AND language = ? ORDER BY at DESC, rowid DESC LIMIT 30`, fileID, language)
	if err != nil {
		return 0, time.Time{}, err
	}
	defer rows.Close()
	first := true
	for rows.Next() {
		var result, at string
		if err := rows.Scan(&result, &at); err != nil {
			return 0, time.Time{}, err
		}
		if result != ResultNone {
			break
		}
		if first {
			if last, err = db.ParseTS(at); err != nil {
				return 0, time.Time{}, err
			}
			first = false
		}
		misses++
	}
	return misses, last, rows.Err()
}

// PruneSearches forgets looks older than the cutoff, but keeps the newest 30 of
// each file and language so the backoff stays right.
func (s *Store) PruneSearches(ctx context.Context, before time.Time) error {
	_, err := s.db.ExecContext(ctx,
		`DELETE FROM subtitle_searches WHERE at < ? AND rowid NOT IN (
		   SELECT rowid FROM (
		     SELECT rowid, ROW_NUMBER() OVER (PARTITION BY file_id, language ORDER BY at DESC, rowid DESC) AS n
		     FROM subtitle_searches) WHERE n <= 30)`, db.TS(before))
	return err
}
