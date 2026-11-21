package accounts

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/BaileyPillon/marquee/internal/core/db"
)

// Store is the only place in this package that talks SQL.
type Store struct {
	db *sql.DB
}

// NewStore wraps the main database.
func NewStore(d *sql.DB) *Store { return &Store{db: d} }

// userRow is a user plus the password hash, which never leaves this package.
type userRow struct {
	User
	PasswordHash string
}

const userCols = `id, name, role, password_hash, created_at, disabled`

type scanner interface{ Scan(dest ...any) error }

func scanUser(s scanner) (userRow, error) {
	var (
		u        userRow
		role     string
		created  string
		disabled int
	)
	if err := s.Scan(&u.ID, &u.Name, &role, &u.PasswordHash, &created, &disabled); err != nil {
		return userRow{}, err
	}
	t, err := db.ParseTS(created)
	if err != nil {
		return userRow{}, err
	}
	u.Role, u.CreatedAt, u.Disabled = Role(role), t, disabled != 0
	return u, nil
}

// ---- users -----------------------------------------------------------------

// CountUsers returns how many users exist.
func (s *Store) CountUsers(ctx context.Context) (int, error) {
	var n int
	err := s.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM users`).Scan(&n)
	return n, err
}

// CreateOwnerIfNone inserts the owner only if the users table is empty. The
// check and the insert share one write transaction, so of several concurrent
// callers exactly one succeeds and the rest get ErrOwnerExists.
func (s *Store) CreateOwnerIfNone(ctx context.Context, name, hash string, now time.Time) (User, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return User{}, err
	}
	defer func() { _ = tx.Rollback() }()

	var n int
	if err := tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM users`).Scan(&n); err != nil {
		return User{}, err
	}
	if n > 0 {
		return User{}, ErrOwnerExists
	}
	u, err := insertUser(ctx, tx, name, RoleOwner, hash, now)
	if err != nil {
		if db.IsUniqueViolation(err) {
			return User{}, ErrOwnerExists // lost a race the count could not see
		}
		return User{}, err
	}
	return u, tx.Commit()
}

type execer interface {
	ExecContext(ctx context.Context, query string, args ...any) (sql.Result, error)
}

func insertUser(ctx context.Context, tx execer, name string, role Role, hash string, now time.Time) (User, error) {
	res, err := tx.ExecContext(ctx,
		`INSERT INTO users (name, role, password_hash, created_at, disabled) VALUES (?, ?, ?, ?, 0)`,
		name, string(role), hash, db.TS(now))
	if err != nil {
		return User{}, err
	}
	id, err := res.LastInsertId()
	if err != nil {
		return User{}, err
	}
	return User{ID: id, Name: name, Role: role, CreatedAt: now.UTC().Truncate(time.Millisecond)}, nil
}

// UserByName finds a user by name, ignoring case.
func (s *Store) UserByName(ctx context.Context, name string) (userRow, error) {
	u, err := scanUser(s.db.QueryRowContext(ctx, `SELECT `+userCols+` FROM users WHERE name = ?`, name))
	if errors.Is(err, sql.ErrNoRows) {
		return userRow{}, ErrNotFound
	}
	return u, err
}

// UserByID finds a user by id.
func (s *Store) UserByID(ctx context.Context, id int64) (User, error) {
	u, err := scanUser(s.db.QueryRowContext(ctx, `SELECT `+userCols+` FROM users WHERE id = ?`, id))
	if errors.Is(err, sql.ErrNoRows) {
		return User{}, ErrNotFound
	}
	return u.User, err
}

// ---- sessions --------------------------------------------------------------

type sessionRecord struct {
	ID         string
	User       User
	CreatedAt  time.Time
	ExpiresAt  time.Time
	LastSeenAt time.Time
}

// InsertSession stores a new session. id is the sha256 hex of the cookie token.
func (s *Store) InsertSession(ctx context.Context, id string, userID int64, now, expires time.Time, userAgent, ip string) error {
	_, err := s.db.ExecContext(ctx,
		`INSERT INTO sessions (id, user_id, created_at, expires_at, last_seen_at, user_agent, ip)
		 VALUES (?, ?, ?, ?, ?, ?, ?)`,
		id, userID, db.TS(now), db.TS(expires), db.TS(now), userAgent, ip)
	return err
}

// SessionByID loads a session with its user.
func (s *Store) SessionByID(ctx context.Context, id string) (sessionRecord, error) {
	var (
		rec                    sessionRecord
		created, expires, seen string
		uCreated, role         string
		disabled               int
	)
	err := s.db.QueryRowContext(ctx,
		`SELECT s.created_at, s.expires_at, s.last_seen_at,
		        u.id, u.name, u.role, u.created_at, u.disabled
		   FROM sessions s JOIN users u ON u.id = s.user_id
		  WHERE s.id = ?`, id).
		Scan(&created, &expires, &seen, &rec.User.ID, &rec.User.Name, &role, &uCreated, &disabled)
	if errors.Is(err, sql.ErrNoRows) {
		return sessionRecord{}, ErrNotFound
	}
	if err != nil {
		return sessionRecord{}, err
	}
	rec.ID = id
	rec.User.Role, rec.User.Disabled = Role(role), disabled != 0
	for _, f := range []struct {
		src string
		dst *time.Time
	}{{created, &rec.CreatedAt}, {expires, &rec.ExpiresAt}, {seen, &rec.LastSeenAt}, {uCreated, &rec.User.CreatedAt}} {
		if *f.dst, err = db.ParseTS(f.src); err != nil {
			return sessionRecord{}, err
		}
	}
	return rec, nil
}

// TouchSession slides the expiry forward.
func (s *Store) TouchSession(ctx context.Context, id string, seen, expires time.Time) error {
	_, err := s.db.ExecContext(ctx, `UPDATE sessions SET last_seen_at = ?, expires_at = ? WHERE id = ?`,
		db.TS(seen), db.TS(expires), id)
	return err
}

// DeleteSession removes one session (sign out).
func (s *Store) DeleteSession(ctx context.Context, id string) error {
	_, err := s.db.ExecContext(ctx, `DELETE FROM sessions WHERE id = ?`, id)
	return err
}

// PurgeExpired deletes expired sessions and long-expired unused invites.
func (s *Store) PurgeExpired(ctx context.Context, now time.Time) (sessions, invites int64, err error) {
	res, err := s.db.ExecContext(ctx, `DELETE FROM sessions WHERE expires_at <= ?`, db.TS(now))
	if err != nil {
		return 0, 0, err
	}
	sessions, _ = res.RowsAffected()
	res, err = s.db.ExecContext(ctx, `DELETE FROM invites WHERE used_at IS NULL AND expires_at <= ?`,
		db.TS(now.Add(-7*24*time.Hour)))
	if err != nil {
		return sessions, 0, err
	}
	invites, _ = res.RowsAffected()
	return sessions, invites, nil
}

// ---- invites ---------------------------------------------------------------

// InsertInvite stores an invite by token hash.
func (s *Store) InsertInvite(ctx context.Context, tokenHash string, createdBy int64, role Role, now, expires time.Time) error {
	_, err := s.db.ExecContext(ctx,
		`INSERT INTO invites (token_hash, created_by, role, created_at, expires_at) VALUES (?, ?, ?, ?, ?)`,
		tokenHash, createdBy, string(role), db.TS(now), db.TS(expires))
	return err
}

// AcceptInvite checks the invite, creates the user and marks the invite used,
// all in one transaction.
func (s *Store) AcceptInvite(ctx context.Context, tokenHash, name, passwordHash string, now time.Time) (User, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return User{}, err
	}
	defer func() { _ = tx.Rollback() }()

	var (
		role, expires string
		usedAt        sql.NullString
	)
	err = tx.QueryRowContext(ctx, `SELECT role, expires_at, used_at FROM invites WHERE token_hash = ?`, tokenHash).
		Scan(&role, &expires, &usedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return User{}, ErrInvalidInvite
	}
	if err != nil {
		return User{}, err
	}
	if usedAt.Valid || expires <= db.TS(now) {
		return User{}, ErrInvalidInvite
	}
	var one int
	switch err := tx.QueryRowContext(ctx, `SELECT 1 FROM users WHERE name = ?`, name).Scan(&one); {
	case err == nil:
		return User{}, ErrNameTaken
	case !errors.Is(err, sql.ErrNoRows):
		return User{}, err
	}
	u, err := insertUser(ctx, tx, name, Role(role), passwordHash, now)
	if err != nil {
		if db.IsUniqueViolation(err) {
			return User{}, ErrNameTaken
		}
		return User{}, err
	}
	res, err := tx.ExecContext(ctx, `UPDATE invites SET used_by = ?, used_at = ? WHERE token_hash = ? AND used_at IS NULL`,
		u.ID, db.TS(now), tokenHash)
	if err != nil {
		return User{}, err
	}
	if n, _ := res.RowsAffected(); n != 1 {
		return User{}, ErrInvalidInvite
	}
	return u, tx.Commit()
}

// ---- API keys --------------------------------------------------------------

type apiKeyRecord struct {
	APIKey
	Hash string
	User User
}

func scanAPIKey(s scanner) (APIKey, error) {
	var (
		k                 APIKey
		scopes, created   string
		lastUsed, revoked sql.NullString
	)
	if err := s.Scan(&k.ID, &k.Name, &k.Prefix, &scopes, &created, &lastUsed, &revoked); err != nil {
		return APIKey{}, err
	}
	if err := json.Unmarshal([]byte(scopes), &k.Scopes); err != nil {
		return APIKey{}, fmt.Errorf("accounts: api key %d has bad scopes: %w", k.ID, err)
	}
	var err error
	if k.CreatedAt, err = db.ParseTS(created); err != nil {
		return APIKey{}, err
	}
	if lastUsed.Valid {
		t, err := db.ParseTS(lastUsed.String)
		if err != nil {
			return APIKey{}, err
		}
		k.LastUsedAt = &t
	}
	if revoked.Valid {
		t, err := db.ParseTS(revoked.String)
		if err != nil {
			return APIKey{}, err
		}
		k.RevokedAt = &t
	}
	return k, nil
}

// InsertAPIKey stores a new key and returns it with its id.
func (s *Store) InsertAPIKey(ctx context.Context, userID int64, name, prefix, hash string, scopes []string, now time.Time) (APIKey, error) {
	js, err := json.Marshal(scopes)
	if err != nil {
		return APIKey{}, err
	}
	res, err := s.db.ExecContext(ctx,
		`INSERT INTO api_keys (user_id, name, prefix, hash, scopes, created_at) VALUES (?, ?, ?, ?, ?, ?)`,
		userID, name, prefix, hash, string(js), db.TS(now))
	if err != nil {
		return APIKey{}, err
	}
	id, err := res.LastInsertId()
	if err != nil {
		return APIKey{}, err
	}
	return APIKey{ID: id, Name: name, Prefix: prefix, Scopes: scopes, CreatedAt: now.UTC().Truncate(time.Millisecond)}, nil
}

const apiKeyCols = `id, name, prefix, scopes, created_at, last_used_at, revoked_at`

// ListAPIKeys returns a user's keys, newest first, revoked ones included.
func (s *Store) ListAPIKeys(ctx context.Context, userID int64) ([]APIKey, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT `+apiKeyCols+` FROM api_keys WHERE user_id = ? ORDER BY id DESC`, userID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []APIKey{}
	for rows.Next() {
		k, err := scanAPIKey(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, k)
	}
	return out, rows.Err()
}

// ActiveAPIKeyByPrefix finds a non-revoked key and its owner by public prefix.
func (s *Store) ActiveAPIKeyByPrefix(ctx context.Context, prefix string) (apiKeyRecord, error) {
	var (
		rec               apiKeyRecord
		scopes, created   string
		lastUsed, revoked sql.NullString
		role, uCreated    string
		disabled          int
	)
	err := s.db.QueryRowContext(ctx,
		`SELECT k.id, k.name, k.prefix, k.hash, k.scopes, k.created_at, k.last_used_at, k.revoked_at,
		        u.id, u.name, u.role, u.created_at, u.disabled
		   FROM api_keys k JOIN users u ON u.id = k.user_id
		  WHERE k.prefix = ? AND k.revoked_at IS NULL`, prefix).
		Scan(&rec.ID, &rec.Name, &rec.Prefix, &rec.Hash, &scopes, &created, &lastUsed, &revoked,
			&rec.User.ID, &rec.User.Name, &role, &uCreated, &disabled)
	if errors.Is(err, sql.ErrNoRows) {
		return apiKeyRecord{}, ErrNotFound
	}
	if err != nil {
		return apiKeyRecord{}, err
	}
	if err := json.Unmarshal([]byte(scopes), &rec.Scopes); err != nil {
		return apiKeyRecord{}, fmt.Errorf("accounts: api key %d has bad scopes: %w", rec.ID, err)
	}
	rec.User.Role, rec.User.Disabled = Role(role), disabled != 0
	if rec.User.CreatedAt, err = db.ParseTS(uCreated); err != nil {
		return apiKeyRecord{}, err
	}
	if rec.CreatedAt, err = db.ParseTS(created); err != nil {
		return apiKeyRecord{}, err
	}
	return rec, nil
}

// TouchAPIKey records a use, at most once per throttle period.
func (s *Store) TouchAPIKey(ctx context.Context, id int64, now time.Time, throttle time.Duration) error {
	_, err := s.db.ExecContext(ctx,
		`UPDATE api_keys SET last_used_at = ? WHERE id = ? AND (last_used_at IS NULL OR last_used_at < ?)`,
		db.TS(now), id, db.TS(now.Add(-throttle)))
	return err
}

// RevokeAPIKey revokes one of the user's keys. Revoking an already revoked key
// succeeds; a key that is not theirs is ErrNotFound.
func (s *Store) RevokeAPIKey(ctx context.Context, userID, id int64, now time.Time) error {
	res, err := s.db.ExecContext(ctx,
		`UPDATE api_keys SET revoked_at = ? WHERE id = ? AND user_id = ? AND revoked_at IS NULL`,
		db.TS(now), id, userID)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n > 0 {
		return nil
	}
	var one int
	err = s.db.QueryRowContext(ctx, `SELECT 1 FROM api_keys WHERE id = ? AND user_id = ?`, id, userID).Scan(&one)
	if errors.Is(err, sql.ErrNoRows) {
		return ErrNotFound
	}
	return err
}

// InsertMember adds a member. A name that is taken gives ErrNameTaken.
func (s *Store) InsertMember(ctx context.Context, name, passwordHash string, now time.Time) (User, error) {
	u, err := insertUser(ctx, s.db, name, RoleMember, passwordHash, now)
	if err != nil {
		if db.IsUniqueViolation(err) {
			return User{}, ErrNameTaken
		}
		return User{}, err
	}
	return u, nil
}
