package mediaservers

import (
	"context"
	"database/sql"
	"errors"
	"time"

	"github.com/BaileyPillon/marquee/internal/core/db"
	"github.com/BaileyPillon/marquee/internal/core/pathmap"
)

// Store is the only place in this package that talks SQL.
type Store struct {
	db *sql.DB
}

// NewStore wraps the main database.
func NewStore(d *sql.DB) *Store { return &Store{db: d} }

type scanner interface{ Scan(dest ...any) error }

// serverRow is a row of media_servers, the token still sealed.
type serverRow struct {
	ID          int64
	Name        string
	Kind        string
	URL         string
	TokenSealed string
	Enabled     bool
	PathMap     []pathmap.Rule
	LastOKAt    *time.Time
	LastError   string
	CreatedAt   time.Time
}

const serverCols = `id, name, kind, url, token_sealed, enabled, path_map, last_ok_at, last_error, created_at`

func scanServer(s scanner) (serverRow, error) {
	var (
		r       serverRow
		enabled int
		pm      string
		lastOK  sql.NullString
		created string
	)
	if err := s.Scan(&r.ID, &r.Name, &r.Kind, &r.URL, &r.TokenSealed, &enabled, &pm, &lastOK, &r.LastError, &created); err != nil {
		return serverRow{}, err
	}
	r.Enabled = enabled == 1
	rules, err := pathmap.Parse(pm)
	if err != nil {
		rules = []pathmap.Rule{}
	}
	r.PathMap = rules
	if lastOK.Valid && lastOK.String != "" {
		t, err := db.ParseTS(lastOK.String)
		if err != nil {
			return serverRow{}, err
		}
		r.LastOKAt = &t
	}
	t, err := db.ParseTS(created)
	if err != nil {
		return serverRow{}, err
	}
	r.CreatedAt = t
	return r, nil
}

func boolInt(b bool) int {
	if b {
		return 1
	}
	return 0
}

func (s *Store) insert(ctx context.Context, r serverRow, now time.Time) (serverRow, error) {
	res, err := s.db.ExecContext(ctx,
		`INSERT INTO media_servers (name, kind, url, token_sealed, enabled, path_map, created_at) VALUES (?, ?, ?, ?, ?, ?, ?)`,
		r.Name, r.Kind, r.URL, r.TokenSealed, boolInt(r.Enabled), pathmap.Encode(r.PathMap), db.TS(now))
	if err != nil {
		if db.IsUniqueViolation(err) {
			return serverRow{}, ErrNameTaken
		}
		return serverRow{}, err
	}
	id, err := res.LastInsertId()
	if err != nil {
		return serverRow{}, err
	}
	return s.get(ctx, id)
}

func (s *Store) get(ctx context.Context, id int64) (serverRow, error) {
	r, err := scanServer(s.db.QueryRowContext(ctx, `SELECT `+serverCols+` FROM media_servers WHERE id = ?`, id))
	if errors.Is(err, sql.ErrNoRows) {
		return serverRow{}, ErrNotFound
	}
	return r, err
}

func (s *Store) list(ctx context.Context, onlyEnabled bool) ([]serverRow, error) {
	q := `SELECT ` + serverCols + ` FROM media_servers`
	if onlyEnabled {
		q += ` WHERE enabled = 1`
	}
	rows, err := s.db.QueryContext(ctx, q+` ORDER BY id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []serverRow
	for rows.Next() {
		r, err := scanServer(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

func (s *Store) update(ctx context.Context, r serverRow) error {
	res, err := s.db.ExecContext(ctx,
		`UPDATE media_servers SET name = ?, url = ?, token_sealed = ?, enabled = ?, path_map = ? WHERE id = ?`,
		r.Name, r.URL, r.TokenSealed, boolInt(r.Enabled), pathmap.Encode(r.PathMap), r.ID)
	if err != nil {
		if db.IsUniqueViolation(err) {
			return ErrNameTaken
		}
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrNotFound
	}
	return nil
}

func (s *Store) delete(ctx context.Context, id int64) error {
	res, err := s.db.ExecContext(ctx, `DELETE FROM media_servers WHERE id = ?`, id)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrNotFound
	}
	return nil
}

func (s *Store) ok(ctx context.Context, id int64, now time.Time) error {
	_, err := s.db.ExecContext(ctx, `UPDATE media_servers SET last_ok_at = ?, last_error = '' WHERE id = ?`, db.TS(now), id)
	return err
}

func (s *Store) failed(ctx context.Context, id int64, msg string) error {
	_, err := s.db.ExecContext(ctx, `UPDATE media_servers SET last_error = ? WHERE id = ?`, msg, id)
	return err
}
