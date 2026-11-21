package alerts

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

// channelRow is a channel as the database keeps it: the settings as text, the
// secrets among them sealed.
type channelRow struct {
	ID        int64
	Name      string
	Kind      string
	Config    map[string]string
	Events    []string
	Enabled   bool
	LastOKAt  *time.Time
	LastError string
	CreatedAt time.Time
}

const channelCols = `id, name, kind, config_sealed, events, enabled, last_ok_at, last_error, created_at`

type scanner interface{ Scan(dest ...any) error }

func scanChannel(s scanner) (channelRow, error) {
	var (
		r             channelRow
		cfg, evs      string
		enabled       int
		lastOK        sql.NullString
		createdAtText string
	)
	if err := s.Scan(&r.ID, &r.Name, &r.Kind, &cfg, &evs, &enabled, &lastOK, &r.LastError, &createdAtText); err != nil {
		return channelRow{}, err
	}
	r.Enabled = enabled != 0
	if err := json.Unmarshal([]byte(cfg), &r.Config); err != nil || r.Config == nil {
		r.Config = map[string]string{}
	}
	if err := json.Unmarshal([]byte(evs), &r.Events); err != nil || r.Events == nil {
		r.Events = []string{}
	}
	var err error
	if r.CreatedAt, err = db.ParseTS(createdAtText); err != nil {
		return channelRow{}, err
	}
	if lastOK.Valid {
		t, err := db.ParseTS(lastOK.String)
		if err != nil {
			return channelRow{}, err
		}
		r.LastOKAt = &t
	}
	return r, nil
}

func b2i(b bool) int {
	if b {
		return 1
	}
	return 0
}

func (s *Store) get(ctx context.Context, id int64) (channelRow, error) {
	r, err := scanChannel(s.db.QueryRowContext(ctx, `SELECT `+channelCols+` FROM alert_channels WHERE id = ?`, id))
	if errors.Is(err, sql.ErrNoRows) {
		return channelRow{}, ErrNotFound
	}
	return r, err
}

func (s *Store) list(ctx context.Context) ([]channelRow, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT `+channelCols+` FROM alert_channels ORDER BY id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []channelRow
	for rows.Next() {
		r, err := scanChannel(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

func (s *Store) insert(ctx context.Context, r channelRow, now time.Time) (int64, error) {
	cfg, _ := json.Marshal(r.Config)
	evs, _ := json.Marshal(append([]string{}, r.Events...))
	res, err := s.db.ExecContext(ctx,
		`INSERT INTO alert_channels (name, kind, config_sealed, events, enabled, last_error, created_at) VALUES (?, ?, ?, ?, ?, '', ?)`,
		r.Name, r.Kind, string(cfg), string(evs), b2i(r.Enabled), db.TS(now))
	if db.IsUniqueViolation(err) {
		return 0, ErrNameTaken
	}
	if err != nil {
		return 0, err
	}
	return res.LastInsertId()
}

func (s *Store) update(ctx context.Context, r channelRow) error {
	cfg, _ := json.Marshal(r.Config)
	evs, _ := json.Marshal(append([]string{}, r.Events...))
	res, err := s.db.ExecContext(ctx,
		`UPDATE alert_channels SET name = ?, config_sealed = ?, events = ?, enabled = ? WHERE id = ?`,
		r.Name, string(cfg), string(evs), b2i(r.Enabled), r.ID)
	if db.IsUniqueViolation(err) {
		return ErrNameTaken
	}
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrNotFound
	}
	return nil
}

func (s *Store) delete(ctx context.Context, id int64) error {
	res, err := s.db.ExecContext(ctx, `DELETE FROM alert_channels WHERE id = ?`, id)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrNotFound
	}
	return nil
}

// markResult records how the last send went. A failure keeps the time of the
// last success.
func (s *Store) markResult(ctx context.Context, id int64, sendErr string, at time.Time) error {
	if sendErr == "" {
		_, err := s.db.ExecContext(ctx, `UPDATE alert_channels SET last_ok_at = ?, last_error = '' WHERE id = ?`, db.TS(at), id)
		return err
	}
	_, err := s.db.ExecContext(ctx, `UPDATE alert_channels SET last_error = ? WHERE id = ?`, sendErr, id)
	return err
}
