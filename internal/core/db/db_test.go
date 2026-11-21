package db

import (
	"context"
	"database/sql"
	"path/filepath"
	"slices"
	"testing"
	"time"
)

func openTemp(t *testing.T, name string) *sql.DB {
	t.Helper()
	d, err := Open(filepath.Join(t.TempDir(), name))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { d.Close() })
	return d
}

func TestMigrateTwiceIsIdempotent(t *testing.T) {
	ctx := context.Background()
	for _, set := range []string{SetMain, SetEvents} {
		t.Run(set, func(t *testing.T) {
			d := openTemp(t, set+".db")
			migs, err := Migrations(set)
			if err != nil || len(migs) == 0 {
				t.Fatalf("Migrations: %v, %d files", err, len(migs))
			}

			first, err := Migrate(ctx, d, set)
			if err != nil {
				t.Fatalf("first run: %v", err)
			}
			if len(first) != len(migs) {
				t.Fatalf("first run applied %v, want %d migrations", first, len(migs))
			}
			second, err := Migrate(ctx, d, set)
			if err != nil {
				t.Fatalf("second run: %v", err)
			}
			if len(second) != 0 {
				t.Fatalf("second run applied %v, want nothing", second)
			}

			var n int
			if err := d.QueryRow(`SELECT COUNT(*) FROM schema_migrations`).Scan(&n); err != nil {
				t.Fatal(err)
			}
			if n != len(migs) {
				t.Fatalf("schema_migrations has %d rows, want %d", n, len(migs))
			}
		})
	}
}

func TestMigrateCreatesTables(t *testing.T) {
	ctx := context.Background()
	d := openTemp(t, "main.db")
	if _, err := Migrate(ctx, d, SetMain); err != nil {
		t.Fatal(err)
	}
	for _, table := range []string{"users", "sessions", "invites", "api_keys", "settings", "schema_migrations"} {
		var name string
		err := d.QueryRow(`SELECT name FROM sqlite_master WHERE type = 'table' AND name = ?`, table).Scan(&name)
		if err != nil {
			t.Errorf("table %s missing: %v", table, err)
		}
	}
}

func TestMigrationFilesAreNumbered(t *testing.T) {
	for _, set := range []string{SetMain, SetEvents} {
		migs, err := Migrations(set)
		if err != nil {
			t.Fatal(err)
		}
		var versions []int
		for _, m := range migs {
			versions = append(versions, m.Version)
		}
		if !slices.IsSorted(versions) || versions[0] != 1 {
			t.Errorf("%s versions = %v, want ascending from 1", set, versions)
		}
	}
	if _, err := Migrations("nope"); err == nil {
		t.Error("unknown set should fail")
	}
}

func TestFailedMigrationRollsBack(t *testing.T) {
	ctx := context.Background()
	d := openTemp(t, "x.db")
	if _, err := d.Exec(`CREATE TABLE schema_migrations (version INTEGER PRIMARY KEY, applied_at TEXT NOT NULL)`); err != nil {
		t.Fatal(err)
	}
	bad := Migration{Version: 9, Name: "0009_bad.sql", SQL: `CREATE TABLE half (id INTEGER); INSERT INTO nope VALUES (1);`}
	if _, err := applyOne(ctx, d, bad); err == nil {
		t.Fatal("expected the bad migration to fail")
	}
	var n int
	_ = d.QueryRow(`SELECT COUNT(*) FROM sqlite_master WHERE name = 'half'`).Scan(&n)
	if n != 0 {
		t.Error("half-applied migration left a table behind")
	}
	_ = d.QueryRow(`SELECT COUNT(*) FROM schema_migrations`).Scan(&n)
	if n != 0 {
		t.Error("failed migration was recorded as applied")
	}
}

func TestPragmas(t *testing.T) {
	d := openTemp(t, "p.db")
	for pragma, want := range map[string]string{
		"journal_mode": "wal",
		"busy_timeout": "5000",
		"foreign_keys": "1",
		"synchronous":  "1", // NORMAL
	} {
		var got string
		if err := d.QueryRow("PRAGMA " + pragma).Scan(&got); err != nil {
			t.Fatal(err)
		}
		if got != want {
			t.Errorf("PRAGMA %s = %q, want %q", pragma, got, want)
		}
	}
}

func TestPragmasApplyToEveryConnection(t *testing.T) {
	d := openTemp(t, "p.db")
	ctx := context.Background()
	var conns []*sql.Conn
	for range 4 {
		c, err := d.Conn(ctx)
		if err != nil {
			t.Fatal(err)
		}
		conns = append(conns, c)
		var fk int
		if err := c.QueryRowContext(ctx, "PRAGMA foreign_keys").Scan(&fk); err != nil || fk != 1 {
			t.Fatalf("connection %d: foreign_keys = %d, %v", len(conns), fk, err)
		}
	}
	for _, c := range conns {
		c.Close()
	}
}

func TestTimestampRoundTrip(t *testing.T) {
	now := time.Date(2026, 11, 20, 13, 4, 5, 123_000_000, time.UTC)
	s := TS(now)
	if s != "2026-11-20T13:04:05.123Z" {
		t.Fatalf("TS = %q", s)
	}
	back, err := ParseTS(s)
	if err != nil || !back.Equal(now) {
		t.Fatalf("ParseTS = %v, %v", back, err)
	}
	if _, err := ParseTS("yesterday"); err == nil {
		t.Error("ParseTS accepted garbage")
	}
}

func TestIsUniqueViolation(t *testing.T) {
	d := openTemp(t, "u.db")
	if _, err := d.Exec(`CREATE TABLE t (name TEXT UNIQUE)`); err != nil {
		t.Fatal(err)
	}
	if _, err := d.Exec(`INSERT INTO t VALUES ('a')`); err != nil {
		t.Fatal(err)
	}
	_, err := d.Exec(`INSERT INTO t VALUES ('a')`)
	if !IsUniqueViolation(err) {
		t.Fatalf("IsUniqueViolation(%v) = false", err)
	}
	if IsUniqueViolation(sql.ErrNoRows) {
		t.Error("ErrNoRows is not a unique violation")
	}
}
