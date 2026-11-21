package settings

import (
	"context"
	"database/sql"
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

// Get returns the stored JSON text of a section.
func (s *Store) Get(ctx context.Context, key string) (value string, updated time.Time, found bool, err error) {
	var ts string
	err = s.db.QueryRowContext(ctx, `SELECT value, updated_at FROM settings WHERE key = ?`, key).Scan(&value, &ts)
	if errors.Is(err, sql.ErrNoRows) {
		return "", time.Time{}, false, nil
	}
	if err != nil {
		return "", time.Time{}, false, err
	}
	updated, err = db.ParseTS(ts)
	return value, updated, err == nil, err
}

// Update reads the current value (empty string and false if unset), lets fn
// compute the new one, and stores it, all in one write transaction so two
// concurrent updates cannot lose each other's changes.
func (s *Store) Update(ctx context.Context, key string, now time.Time,
	fn func(old string, found bool) (string, error)) (string, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return "", err
	}
	defer func() { _ = tx.Rollback() }()

	var old string
	found := true
	switch err := tx.QueryRowContext(ctx, `SELECT value FROM settings WHERE key = ?`, key).Scan(&old); {
	case errors.Is(err, sql.ErrNoRows):
		found = false
	case err != nil:
		return "", err
	}
	next, err := fn(old, found)
	if err != nil {
		return "", err
	}
	if _, err := tx.ExecContext(ctx,
		`INSERT INTO settings (key, value, updated_at) VALUES (?, ?, ?)
		 ON CONFLICT(key) DO UPDATE SET value = excluded.value, updated_at = excluded.updated_at`,
		key, next, db.TS(now)); err != nil {
		return "", err
	}
	return next, tx.Commit()
}
