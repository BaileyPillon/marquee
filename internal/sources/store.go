package sources

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"time"

	"github.com/BaileyPillon/marquee/internal/core/db"
)

// Store is the only place in this package that talks SQL.
type Store struct {
	db *sql.DB
}

// NewStore wraps the main database.
func NewStore(d *sql.DB) *Store { return &Store{db: d} }

// record is an indexers row, with the key still sealed.
type record struct {
	ID          int64
	Name        string
	Kind        string
	URL         string
	KeySealed   string
	Categories  []int
	Enabled     bool
	Priority    int
	Caps        *Caps
	LastOKAt    *time.Time
	LastError   string
	LastErrorAt *time.Time
	CreatedAt   time.Time
}

type scanner interface{ Scan(dest ...any) error }

const recordCols = `id, name, kind, url, api_key_sealed, categories, enabled, priority, caps,
	last_ok_at, last_error, last_error_at, created_at`

func scanRecord(s scanner) (record, error) {
	var (
		r               record
		cats            string
		enabled         int
		caps            sql.NullString
		lastOK, lastErr sql.NullString
		created         string
	)
	if err := s.Scan(&r.ID, &r.Name, &r.Kind, &r.URL, &r.KeySealed, &cats, &enabled, &r.Priority, &caps,
		&lastOK, &r.LastError, &lastErr, &created); err != nil {
		return record{}, err
	}
	r.Enabled = enabled != 0
	if err := json.Unmarshal([]byte(cats), &r.Categories); err != nil {
		return record{}, err
	}
	if r.Categories == nil {
		r.Categories = []int{}
	}
	if caps.Valid {
		var c Caps
		if err := json.Unmarshal([]byte(caps.String), &c); err == nil {
			r.Caps = &c
		}
	}
	var err error
	if r.LastOKAt, err = optTime(lastOK); err != nil {
		return record{}, err
	}
	if r.LastErrorAt, err = optTime(lastErr); err != nil {
		return record{}, err
	}
	if r.CreatedAt, err = db.ParseTS(created); err != nil {
		return record{}, err
	}
	return r, nil
}

func optTime(s sql.NullString) (*time.Time, error) {
	if !s.Valid || s.String == "" {
		return nil, nil
	}
	t, err := db.ParseTS(s.String)
	if err != nil {
		return nil, err
	}
	return &t, nil
}

func tsOrNil(t *time.Time) any {
	if t == nil {
		return nil
	}
	return db.TS(*t)
}

func b2i(b bool) int {
	if b {
		return 1
	}
	return 0
}

// List returns every indexer, lowest priority number first.
func (s *Store) List(ctx context.Context) ([]record, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT `+recordCols+` FROM indexers ORDER BY priority, id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []record{}
	for rows.Next() {
		r, err := scanRecord(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

// Get returns one indexer, or ErrNotFound.
func (s *Store) Get(ctx context.Context, id int64) (record, error) {
	r, err := scanRecord(s.db.QueryRowContext(ctx, `SELECT `+recordCols+` FROM indexers WHERE id = ?`, id))
	if errors.Is(err, sql.ErrNoRows) {
		return record{}, ErrNotFound
	}
	return r, err
}

// Insert adds an indexer. An address already in use gives ErrDuplicate.
func (s *Store) Insert(ctx context.Context, r record) (int64, error) {
	cats, _ := json.Marshal(nonNilInts(r.Categories))
	res, err := s.db.ExecContext(ctx,
		`INSERT INTO indexers (name, kind, url, api_key_sealed, categories, enabled, priority, created_at)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?)`,
		r.Name, r.Kind, r.URL, r.KeySealed, string(cats), b2i(r.Enabled), r.Priority, db.TS(r.CreatedAt))
	if err != nil {
		if db.IsUniqueViolation(err) {
			return 0, ErrDuplicate
		}
		return 0, err
	}
	return res.LastInsertId()
}

func nonNilInts(v []int) []int {
	if v == nil {
		return []int{}
	}
	return v
}

// Update writes every editable column and the learned state of an indexer.
func (s *Store) Update(ctx context.Context, r record) error {
	cats, _ := json.Marshal(nonNilInts(r.Categories))
	var caps any
	if r.Caps != nil {
		b, _ := json.Marshal(r.Caps)
		caps = string(b)
	}
	res, err := s.db.ExecContext(ctx,
		`UPDATE indexers SET name = ?, kind = ?, url = ?, api_key_sealed = ?, categories = ?, enabled = ?,
		        priority = ?, caps = ?, last_ok_at = ?, last_error = ?, last_error_at = ?
		 WHERE id = ?`,
		r.Name, r.Kind, r.URL, r.KeySealed, string(cats), b2i(r.Enabled), r.Priority, caps,
		tsOrNil(r.LastOKAt), r.LastError, tsOrNil(r.LastErrorAt), r.ID)
	if err != nil {
		if db.IsUniqueViolation(err) {
			return ErrDuplicate
		}
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrNotFound
	}
	return nil
}

// Delete removes an indexer; its releases_seen rows go with it.
func (s *Store) Delete(ctx context.Context, id int64) error {
	res, err := s.db.ExecContext(ctx, `DELETE FROM indexers WHERE id = ?`, id)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrNotFound
	}
	return nil
}

// SaveCaps stores the capabilities learned from t=caps.
func (s *Store) SaveCaps(ctx context.Context, id int64, c *Caps) error {
	b, err := json.Marshal(c)
	if err != nil {
		return err
	}
	_, err = s.db.ExecContext(ctx, `UPDATE indexers SET caps = ? WHERE id = ?`, string(b), id)
	return err
}

// MarkOK records a good answer and clears the last error.
func (s *Store) MarkOK(ctx context.Context, id int64, at time.Time) error {
	_, err := s.db.ExecContext(ctx,
		`UPDATE indexers SET last_ok_at = ?, last_error = '', last_error_at = NULL WHERE id = ?`, db.TS(at), id)
	return err
}

// MarkError records a failed call.
func (s *Store) MarkError(ctx context.Context, id int64, at time.Time, msg string) error {
	_, err := s.db.ExecContext(ctx,
		`UPDATE indexers SET last_error = ?, last_error_at = ? WHERE id = ?`, msg, db.TS(at), id)
	return err
}

// ---- releases_seen -----------------------------------------------------------

// seenRow is one releases_seen row, links still sealed.
type seenRow struct {
	IndexerID    int64
	GUID         string
	Title        string
	Size         int64
	PublishedAt  time.Time // zero: unknown
	Protocol     string
	LinkSealed   string
	MagnetSealed string
	InfoHash     string
	Seeders      int
	RawAttrs     string // JSON
	SeenAt       time.Time
}

// InsertSeen adds rows and returns the indexes (into rows) of the ones that
// were new; a row that is already remembered is left alone.
func (s *Store) InsertSeen(ctx context.Context, rows []seenRow) ([]int, error) {
	if len(rows) == 0 {
		return nil, nil
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer func() { _ = tx.Rollback() }()
	stmt, err := tx.PrepareContext(ctx,
		`INSERT INTO releases_seen (indexer_id, guid, title, size, published_at, protocol, link_sealed, magnet,
		                            infohash, seeders, raw_attrs, seen_at)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
		 ON CONFLICT (indexer_id, guid) DO NOTHING`)
	if err != nil {
		return nil, err
	}
	defer stmt.Close()
	var fresh []int
	for i, r := range rows {
		var pub any
		if !r.PublishedAt.IsZero() {
			pub = db.TS(r.PublishedAt)
		}
		var link any
		if r.LinkSealed != "" {
			link = r.LinkSealed
		}
		res, err := stmt.ExecContext(ctx, r.IndexerID, r.GUID, r.Title, r.Size, pub, r.Protocol, link,
			r.MagnetSealed, r.InfoHash, r.Seeders, r.RawAttrs, db.TS(r.SeenAt))
		if err != nil {
			return nil, err
		}
		if n, _ := res.RowsAffected(); n > 0 {
			fresh = append(fresh, i)
		}
	}
	return fresh, tx.Commit()
}

// GetSeen returns one remembered release.
func (s *Store) GetSeen(ctx context.Context, indexerID int64, guid string) (seenRow, bool, error) {
	var (
		r          seenRow
		pub, link  sql.NullString
		seenAtText string
	)
	err := s.db.QueryRowContext(ctx,
		`SELECT indexer_id, guid, title, size, published_at, protocol, link_sealed, magnet, infohash, seeders,
		        raw_attrs, seen_at
		 FROM releases_seen WHERE indexer_id = ? AND guid = ?`, indexerID, guid).
		Scan(&r.IndexerID, &r.GUID, &r.Title, &r.Size, &pub, &r.Protocol, &link, &r.MagnetSealed, &r.InfoHash,
			&r.Seeders, &r.RawAttrs, &seenAtText)
	if errors.Is(err, sql.ErrNoRows) {
		return seenRow{}, false, nil
	}
	if err != nil {
		return seenRow{}, false, err
	}
	r.LinkSealed = link.String
	if pub.Valid {
		if r.PublishedAt, err = db.ParseTS(pub.String); err != nil {
			return seenRow{}, false, err
		}
	}
	if r.SeenAt, err = db.ParseTS(seenAtText); err != nil {
		return seenRow{}, false, err
	}
	return r, true, nil
}

// PruneSeen forgets releases first seen before the cutoff and returns how many.
func (s *Store) PruneSeen(ctx context.Context, before time.Time) (int64, error) {
	res, err := s.db.ExecContext(ctx, `DELETE FROM releases_seen WHERE seen_at < ?`, db.TS(before))
	if err != nil {
		return 0, err
	}
	return res.RowsAffected()
}

// CountSeen counts remembered releases (for tests and status).
func (s *Store) CountSeen(ctx context.Context) (int, error) {
	var n int
	err := s.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM releases_seen`).Scan(&n)
	return n, err
}
