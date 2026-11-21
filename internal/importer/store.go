package importer

import (
	"context"
	"database/sql"
	"encoding/json"
	"time"

	"github.com/BaileyPillon/marquee/internal/core/db"
)

// Store is the only place in this package that talks SQL.
//
// It reads the titles table only to show a name next to an import (a join); it
// never writes it.
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

func boolInt(b bool) int {
	if b {
		return 1
	}
	return 0
}

// importStart is what is known when an import begins.
type importStart struct {
	DownloadID   int64
	TitleID      int64
	CopyID       int64
	ReleaseTitle string
	Manual       bool
	At           time.Time
}

// begin records the start of an import and returns its id.
func (s *Store) begin(ctx context.Context, in importStart) (int64, error) {
	res, err := s.db.ExecContext(ctx,
		`INSERT INTO imports (download_id, title_id, copy_id, release_title, manual, started_at) VALUES (?, ?, ?, ?, ?, ?)`,
		in.DownloadID, in.TitleID, nullInt(in.CopyID), in.ReleaseTitle, boolInt(in.Manual), db.TS(in.At))
	if err != nil {
		return 0, err
	}
	return res.LastInsertId()
}

// importEnd is how an import ended.
type importEnd struct {
	Outcome   string
	Imported  int
	Skipped   int
	Reason    string
	Retryable bool
	Detail    any
	At        time.Time
}

func (s *Store) finish(ctx context.Context, id int64, e importEnd) error {
	detail, err := json.Marshal(e.Detail)
	if err != nil || e.Detail == nil {
		detail = []byte("{}")
	}
	_, err = s.db.ExecContext(ctx,
		`UPDATE imports SET finished_at = ?, outcome = ?, files_imported = ?, files_skipped = ?, reason = ?, retryable = ?, detail = ?
		  WHERE id = ?`,
		db.TS(e.At), e.Outcome, e.Imported, e.Skipped, e.Reason, boolInt(e.Retryable), string(detail), id)
	return err
}

// interrupted ends the imports a stopped process left "running".
func (s *Store) interrupted(ctx context.Context, now time.Time) error {
	_, err := s.db.ExecContext(ctx,
		`UPDATE imports SET outcome = 'failed', finished_at = ?, reason = 'Marquee stopped before this import finished.'
		  WHERE outcome = 'running'`, db.TS(now))
	return err
}

// attempts says how many imports were tried for a download and whether the last
// one failed in a way worth another go.
func (s *Store) attempts(ctx context.Context, downloadID int64) (n int, lastRetryable bool, err error) {
	if err = s.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM imports WHERE download_id = ?`, downloadID).Scan(&n); err != nil {
		return 0, false, err
	}
	var r sql.NullInt64
	err = s.db.QueryRowContext(ctx,
		`SELECT retryable FROM imports WHERE download_id = ? ORDER BY id DESC LIMIT 1`, downloadID).Scan(&r)
	if err == sql.ErrNoRows {
		return n, false, nil
	}
	return n, r.Valid && r.Int64 == 1, err
}

// TitleRef is the small piece of a title an import shows.
type TitleRef struct {
	ID   int64  `json:"id"`
	Name string `json:"name"`
	Year int    `json:"year"`
	Kind string `json:"kind"`
}

// Record is one import as the API shows it.
type Record struct {
	ID            int64           `json:"id"`
	DownloadID    int64           `json:"downloadId"`
	Title         *TitleRef       `json:"title"` // null when the title has since been deleted
	ReleaseTitle  string          `json:"releaseTitle"`
	Manual        bool            `json:"manual"`
	Outcome       string          `json:"outcome"`
	FilesImported int             `json:"filesImported"`
	FilesSkipped  int             `json:"filesSkipped"`
	Reason        string          `json:"reason"`
	Detail        json.RawMessage `json:"detail"`
	StartedAt     time.Time       `json:"startedAt"`
	FinishedAt    *time.Time      `json:"finishedAt"`
}

func (s *Store) recent(ctx context.Context, limit int) ([]Record, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT i.id, i.download_id, i.release_title, i.manual, i.outcome, i.files_imported, i.files_skipped, i.reason,
		       i.detail, i.started_at, i.finished_at, t.id, t.name, t.year, t.kind
		  FROM imports i LEFT JOIN titles t ON t.id = i.title_id
		 ORDER BY i.id DESC LIMIT ?`, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Record{}
	for rows.Next() {
		var (
			r            Record
			manual       int
			detail       string
			started      string
			finished     sql.NullString
			tid, tyear   sql.NullInt64
			tname, tkind sql.NullString
		)
		if err := rows.Scan(&r.ID, &r.DownloadID, &r.ReleaseTitle, &manual, &r.Outcome, &r.FilesImported, &r.FilesSkipped,
			&r.Reason, &detail, &started, &finished, &tid, &tname, &tyear, &tkind); err != nil {
			return nil, err
		}
		r.Manual = manual == 1
		r.Detail = json.RawMessage(detail)
		if r.StartedAt, err = db.ParseTS(started); err != nil {
			return nil, err
		}
		if finished.Valid {
			t, err := db.ParseTS(finished.String)
			if err != nil {
				return nil, err
			}
			r.FinishedAt = &t
		}
		if tid.Valid {
			r.Title = &TitleRef{ID: tid.Int64, Name: tname.String, Year: int(tyear.Int64), Kind: tkind.String}
		}
		out = append(out, r)
	}
	return out, rows.Err()
}
