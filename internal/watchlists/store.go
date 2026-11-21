package watchlists

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"time"

	"github.com/BaileyPillon/marquee/internal/core/db"
)

// Store is the only place in this package that talks SQL. It reads the users
// table for display and for the role an ask is made with (the accounts module
// owns that table; nothing here writes to it).
type Store struct {
	db *sql.DB
}

// NewStore wraps the main database.
func NewStore(d *sql.DB) *Store { return &Store{db: d} }

// ---- the linked account -----------------------------------------------------

type account struct {
	UserID        int64 // the person whose asks the owner's own watchlist makes; 0 if gone
	PlexUserID    string
	PlexUsername  string
	TokenSealed   string
	LinkedAt      time.Time
	LastCheckedAt *time.Time
	LastError     string
	LastResult    Result
}

func (s *Store) account(ctx context.Context) (account, bool, error) {
	var (
		a             account
		userID        sql.NullInt64
		linked        string
		checked       sql.NullString
		lastResultRaw string
	)
	err := s.db.QueryRowContext(ctx,
		`SELECT user_id, plex_user_id, plex_username, token_sealed, linked_at, last_checked_at, last_error, last_result
		 FROM plex_account WHERE id = 1`).
		Scan(&userID, &a.PlexUserID, &a.PlexUsername, &a.TokenSealed, &linked, &checked, &a.LastError, &lastResultRaw)
	if errors.Is(err, sql.ErrNoRows) {
		return account{}, false, nil
	}
	if err != nil {
		return account{}, false, err
	}
	a.UserID = userID.Int64
	if a.LinkedAt, err = db.ParseTS(linked); err != nil {
		return account{}, false, err
	}
	if checked.Valid {
		t, err := db.ParseTS(checked.String)
		if err != nil {
			return account{}, false, err
		}
		a.LastCheckedAt = &t
	}
	a.LastResult = newResult()
	_ = json.Unmarshal([]byte(lastResultRaw), &a.LastResult) // a result that cannot be read is an empty one
	if a.LastResult.Skipped == nil {
		a.LastResult.Skipped = map[string]int{}
	}
	return a, true, nil
}

// saveAccount links (or relinks) the account. The last check is forgotten.
func (s *Store) saveAccount(ctx context.Context, a account) error {
	_, err := s.db.ExecContext(ctx,
		`INSERT INTO plex_account (id, user_id, plex_user_id, plex_username, token_sealed, linked_at, last_checked_at, last_error, last_result)
		 VALUES (1, ?, ?, ?, ?, ?, NULL, '', '{}')
		 ON CONFLICT (id) DO UPDATE SET user_id = excluded.user_id, plex_user_id = excluded.plex_user_id,
		   plex_username = excluded.plex_username, token_sealed = excluded.token_sealed, linked_at = excluded.linked_at,
		   last_checked_at = NULL, last_error = '', last_result = '{}'`,
		nullID(a.UserID), a.PlexUserID, a.PlexUsername, a.TokenSealed, db.TS(a.LinkedAt))
	return err
}

func nullID(id int64) any {
	if id == 0 {
		return nil
	}
	return id
}

// deleteAccount unlinks the Plex account and forgets the friends list. Links
// between friends and people, and the items already looked at, stay.
func (s *Store) deleteAccount(ctx context.Context) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	if _, err := tx.ExecContext(ctx, `DELETE FROM plex_account`); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM plex_friends`); err != nil {
		return err
	}
	return tx.Commit()
}

func (s *Store) saveCheck(ctx context.Context, at time.Time, res Result, errMsg string) error {
	raw, err := json.Marshal(res)
	if err != nil {
		return err
	}
	_, err = s.db.ExecContext(ctx,
		`UPDATE plex_account SET last_checked_at = ?, last_error = ?, last_result = ? WHERE id = 1`,
		db.TS(at), errMsg, string(raw))
	return err
}

// ---- friends and links ------------------------------------------------------

// replaceFriends makes the friends list match what Plex answered.
func (s *Store) replaceFriends(ctx context.Context, friends []PlexUser, now time.Time) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	if _, err := tx.ExecContext(ctx, `DELETE FROM plex_friends`); err != nil {
		return err
	}
	for _, f := range friends {
		if _, err := tx.ExecContext(ctx,
			`INSERT INTO plex_friends (plex_user_id, username, display_name, seen_at) VALUES (?, ?, ?, ?)`,
			f.ID, f.Username, f.DisplayName, db.TS(now)); err != nil {
			return err
		}
	}
	return tx.Commit()
}

// friends lists the friends known from the last check, with their links.
func (s *Store) friends(ctx context.Context) ([]Friend, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT f.plex_user_id, COALESCE(NULLIF(f.username, ''), f.display_name), l.user_id, u.name
		 FROM plex_friends f
		 LEFT JOIN plex_links l ON l.plex_user_id = f.plex_user_id
		 LEFT JOIN users u ON u.id = l.user_id
		 ORDER BY LOWER(COALESCE(NULLIF(f.username, ''), f.display_name)), f.plex_user_id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Friend{}
	for rows.Next() {
		var (
			f    Friend
			uid  sql.NullInt64
			name sql.NullString
		)
		if err := rows.Scan(&f.PlexUserID, &f.Username, &uid, &name); err != nil {
			return nil, err
		}
		if uid.Valid {
			f.LinkedUserID = &uid.Int64
			n := name.String
			f.LinkedUserName = &n
		}
		out = append(out, f)
	}
	return out, rows.Err()
}

func (s *Store) friend(ctx context.Context, plexUserID string) (PlexUser, bool, error) {
	var f PlexUser
	err := s.db.QueryRowContext(ctx,
		`SELECT plex_user_id, username, display_name FROM plex_friends WHERE plex_user_id = ?`, plexUserID).
		Scan(&f.ID, &f.Username, &f.DisplayName)
	if errors.Is(err, sql.ErrNoRows) {
		return PlexUser{}, false, nil
	}
	return f, err == nil, err
}

// userRef is a Marquee person as the watchlists need them.
type userRef struct {
	ID       int64
	Name     string
	Role     string
	Disabled bool
}

func (s *Store) user(ctx context.Context, id int64) (userRef, bool, error) {
	var (
		u   userRef
		dis int
	)
	err := s.db.QueryRowContext(ctx, `SELECT id, name, role, disabled FROM users WHERE id = ?`, id).Scan(&u.ID, &u.Name, &u.Role, &dis)
	if errors.Is(err, sql.ErrNoRows) {
		return userRef{}, false, nil
	}
	u.Disabled = dis != 0
	return u, err == nil, err
}

// link finds the person a friend is linked to.
func (s *Store) link(ctx context.Context, plexUserID string) (userRef, bool, error) {
	var (
		u   userRef
		dis int
	)
	err := s.db.QueryRowContext(ctx,
		`SELECT u.id, u.name, u.role, u.disabled FROM plex_links l JOIN users u ON u.id = l.user_id WHERE l.plex_user_id = ?`,
		plexUserID).Scan(&u.ID, &u.Name, &u.Role, &dis)
	if errors.Is(err, sql.ErrNoRows) {
		return userRef{}, false, nil
	}
	u.Disabled = dis != 0
	return u, err == nil, err
}

// setLink links a friend to a person, replacing the friend's earlier link. A
// person who is linked to another friend gives ErrUserAlreadyLinked.
func (s *Store) setLink(ctx context.Context, plexUserID string, userID int64, plexUsername string, now time.Time) error {
	_, err := s.db.ExecContext(ctx,
		`INSERT INTO plex_links (plex_user_id, user_id, plex_username, linked_at) VALUES (?, ?, ?, ?)
		 ON CONFLICT (plex_user_id) DO UPDATE SET user_id = excluded.user_id, plex_username = excluded.plex_username,
		   linked_at = excluded.linked_at`,
		plexUserID, userID, plexUsername, db.TS(now))
	if db.IsUniqueViolation(err) {
		return ErrUserAlreadyLinked
	}
	return err
}

func (s *Store) deleteLink(ctx context.Context, plexUserID string) error {
	_, err := s.db.ExecContext(ctx, `DELETE FROM plex_links WHERE plex_user_id = ?`, plexUserID)
	return err
}

// ---- items ------------------------------------------------------------------

type itemRow struct {
	Source        string
	PlexUserID    string
	Key           string
	Title         string
	Year          int
	Type          string
	Guids         []string
	FirstSeenAt   time.Time
	RequestID     int64 // 0 while there is no ask
	SkippedReason string
}

// done says an item has been dealt with for good: it became an ask, or it was
// skipped for a reason that will not pass.
func (r itemRow) done() bool {
	return r.RequestID != 0 || (r.SkippedReason != "" && !retryable[r.SkippedReason])
}

func (s *Store) item(ctx context.Context, source, plexUserID, key string) (itemRow, bool, error) {
	var (
		r     itemRow
		guids string
		first string
		req   sql.NullInt64
	)
	err := s.db.QueryRowContext(ctx,
		`SELECT source, plex_user_id, item_key, title, year, type, guids, first_seen_at, request_id, skipped_reason
		 FROM watchlist_items WHERE source = ? AND plex_user_id = ? AND item_key = ?`, source, plexUserID, key).
		Scan(&r.Source, &r.PlexUserID, &r.Key, &r.Title, &r.Year, &r.Type, &guids, &first, &req, &r.SkippedReason)
	if errors.Is(err, sql.ErrNoRows) {
		return itemRow{}, false, nil
	}
	if err != nil {
		return itemRow{}, false, err
	}
	r.RequestID = req.Int64
	if r.FirstSeenAt, err = db.ParseTS(first); err != nil {
		return itemRow{}, false, err
	}
	if err := json.Unmarshal([]byte(guids), &r.Guids); err != nil {
		r.Guids = nil
	}
	return r, true, nil
}

// putItem inserts an item, or updates what Marquee has learnt about it. The
// time it was first seen never changes.
func (s *Store) putItem(ctx context.Context, r itemRow) error {
	guids, err := json.Marshal(append([]string{}, r.Guids...))
	if err != nil {
		return err
	}
	_, err = s.db.ExecContext(ctx,
		`INSERT INTO watchlist_items (source, plex_user_id, item_key, title, year, type, guids, first_seen_at, request_id, skipped_reason)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
		 ON CONFLICT (source, plex_user_id, item_key) DO UPDATE SET title = excluded.title, year = excluded.year,
		   type = excluded.type, guids = excluded.guids, request_id = excluded.request_id,
		   skipped_reason = excluded.skipped_reason`,
		r.Source, r.PlexUserID, r.Key, r.Title, r.Year, r.Type, string(guids), db.TS(r.FirstSeenAt),
		nullID(r.RequestID), r.SkippedReason)
	return err
}
