// Package db opens Marquee's two SQLite files and applies embedded migrations.
//
// The pure-Go modernc.org/sqlite driver is imported here and nowhere else in
// the program, so the rest of the code only sees database/sql.
package db

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"

	"modernc.org/sqlite"
	sqlitelib "modernc.org/sqlite/lib"
)

// MainFile and EventsFile are the two database file names inside the config dir.
const (
	MainFile   = "marquee.db"
	EventsFile = "marquee-events.db"
)

// Open opens (creating if needed) one SQLite database file with the pragmas
// Marquee relies on: WAL, a 5 second busy timeout, foreign keys, and
// synchronous=NORMAL. Write transactions start with BEGIN IMMEDIATE, so two
// writers queue behind each other instead of failing on a read-to-write upgrade.
func Open(path string) (*sql.DB, error) {
	if strings.ContainsAny(path, "?#") {
		return nil, fmt.Errorf("db: unsupported character in path %q", path)
	}
	dsn := path + "?_pragma=journal_mode(WAL)" +
		"&_pragma=busy_timeout(5000)" +
		"&_pragma=foreign_keys(ON)" +
		"&_pragma=synchronous(NORMAL)" +
		"&_txlock=immediate"
	d, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, fmt.Errorf("db: open %s: %w", path, err)
	}
	d.SetMaxOpenConns(8)
	d.SetMaxIdleConns(8)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := d.PingContext(ctx); err != nil {
		_ = d.Close()
		return nil, fmt.Errorf("db: open %s: %w", path, err)
	}
	return d, nil
}

// Timestamps are stored as fixed-width UTC text so they sort and compare
// correctly in SQL.
const tsLayout = "2006-01-02T15:04:05.000Z"

// TS formats t for storage.
func TS(t time.Time) string { return t.UTC().Format(tsLayout) }

// ParseTS reads a value written by TS.
func ParseTS(s string) (time.Time, error) {
	t, err := time.ParseInLocation(tsLayout, s, time.UTC)
	if err != nil {
		return time.Time{}, fmt.Errorf("db: bad timestamp %q: %w", s, err)
	}
	return t, nil
}

// IsUniqueViolation reports whether err is a UNIQUE or PRIMARY KEY constraint
// failure.
func IsUniqueViolation(err error) bool {
	var se *sqlite.Error
	if errors.As(err, &se) {
		c := se.Code()
		return c == sqlitelib.SQLITE_CONSTRAINT_UNIQUE || c == sqlitelib.SQLITE_CONSTRAINT_PRIMARYKEY
	}
	return false
}
