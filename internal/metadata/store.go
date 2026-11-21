package metadata

import (
	"context"
	"database/sql"
	"errors"
	"time"

	"github.com/BaileyPillon/marquee/internal/core/db"
)

// store is the only place in this package that talks SQL.
type store struct {
	db *sql.DB
}

// get returns a cached body and when it expires.
func (s *store) get(ctx context.Context, provider, key string) (body []byte, expires time.Time, found bool, err error) {
	var exp string
	err = s.db.QueryRowContext(ctx,
		`SELECT body, expires_at FROM metadata_cache WHERE provider = ? AND key = ?`, provider, key).Scan(&body, &exp)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, time.Time{}, false, nil
	}
	if err != nil {
		return nil, time.Time{}, false, err
	}
	expires, err = db.ParseTS(exp)
	if err != nil {
		return nil, time.Time{}, false, err
	}
	return body, expires, true, nil
}

// put stores or replaces one entry.
func (s *store) put(ctx context.Context, provider, key string, body []byte, fetched, expires time.Time) error {
	_, err := s.db.ExecContext(ctx,
		`INSERT INTO metadata_cache (provider, key, body, fetched_at, expires_at) VALUES (?, ?, ?, ?, ?)
		 ON CONFLICT(provider, key) DO UPDATE SET
		   body = excluded.body, fetched_at = excluded.fetched_at, expires_at = excluded.expires_at`,
		provider, key, body, db.TS(fetched), db.TS(expires))
	return err
}

// prune deletes entries that expired before cutoff. Entries that expired
// recently stay: they are the stale copies an outage falls back to.
func (s *store) prune(ctx context.Context, cutoff time.Time) (int64, error) {
	res, err := s.db.ExecContext(ctx, `DELETE FROM metadata_cache WHERE expires_at < ?`, db.TS(cutoff))
	if err != nil {
		return 0, err
	}
	return res.RowsAffected()
}

// count returns how many entries are cached.
func (s *store) count(ctx context.Context) (int, error) {
	var n int
	err := s.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM metadata_cache`).Scan(&n)
	return n, err
}
