package db

import (
	"context"
	"database/sql"
	"embed"
	"fmt"
	"path"
	"regexp"
	"sort"
	"strconv"
	"time"
)

//go:embed migrations/main/*.sql migrations/events/*.sql
var migrationFS embed.FS

// Migration sets.
const (
	SetMain   = "main"
	SetEvents = "events"
)

// Migration is one numbered SQL file.
type Migration struct {
	Version int
	Name    string
	SQL     string
}

var migrationName = regexp.MustCompile(`^(\d+)_[A-Za-z0-9_\-]+\.sql$`)

// Migrations returns the embedded migrations of a set in version order.
func Migrations(set string) ([]Migration, error) {
	dir := path.Join("migrations", set)
	entries, err := migrationFS.ReadDir(dir)
	if err != nil {
		return nil, fmt.Errorf("db: unknown migration set %q: %w", set, err)
	}
	var out []Migration
	seen := map[int]string{}
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		m := migrationName.FindStringSubmatch(e.Name())
		if m == nil {
			return nil, fmt.Errorf("db: migration file %q must be named NNNN_name.sql", e.Name())
		}
		v, _ := strconv.Atoi(m[1])
		if prev, dup := seen[v]; dup {
			return nil, fmt.Errorf("db: migrations %q and %q share version %d", prev, e.Name(), v)
		}
		seen[v] = e.Name()
		body, err := migrationFS.ReadFile(path.Join(dir, e.Name()))
		if err != nil {
			return nil, err
		}
		out = append(out, Migration{Version: v, Name: e.Name(), SQL: string(body)})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Version < out[j].Version })
	return out, nil
}

// Migrate applies every migration of the set that has not run yet. Each one
// runs in its own transaction together with its schema_migrations row, so a
// failure leaves no half-applied file. Running it again is a no-op. It returns
// the versions applied by this call.
func Migrate(ctx context.Context, d *sql.DB, set string) ([]int, error) {
	migs, err := Migrations(set)
	if err != nil {
		return nil, err
	}
	if _, err := d.ExecContext(ctx, `CREATE TABLE IF NOT EXISTS schema_migrations (
		version    INTEGER PRIMARY KEY,
		applied_at TEXT NOT NULL
	)`); err != nil {
		return nil, fmt.Errorf("db: create schema_migrations: %w", err)
	}
	var applied []int
	for _, m := range migs {
		ok, err := applyOne(ctx, d, m)
		if err != nil {
			return applied, fmt.Errorf("db: migration %s/%s: %w", set, m.Name, err)
		}
		if ok {
			applied = append(applied, m.Version)
		}
	}
	return applied, nil
}

func applyOne(ctx context.Context, d *sql.DB, m Migration) (bool, error) {
	tx, err := d.BeginTx(ctx, nil)
	if err != nil {
		return false, err
	}
	defer func() { _ = tx.Rollback() }()

	// Checked inside the transaction (which already holds the write lock) so
	// two processes starting together cannot both apply the same file.
	var one int
	switch err := tx.QueryRowContext(ctx, `SELECT 1 FROM schema_migrations WHERE version = ?`, m.Version).Scan(&one); {
	case err == nil:
		return false, nil
	case err != sql.ErrNoRows:
		return false, err
	}
	if _, err := tx.ExecContext(ctx, m.SQL); err != nil {
		return false, err
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO schema_migrations (version, applied_at) VALUES (?, ?)`,
		m.Version, TS(time.Now())); err != nil {
		return false, err
	}
	return true, tx.Commit()
}
