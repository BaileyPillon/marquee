package db

import (
	"context"
	"testing"
)

// 0014_stalls.sql adds downloads.progress_changed_at. Downloads that were there
// before count from their last update, so none is given up on at once by a
// clock that never ran.
func TestStallsMigrationStartsTheClockAtTheLastUpdate(t *testing.T) {
	ctx := context.Background()
	d := openTemp(t, "main.db")
	if _, err := d.ExecContext(ctx, `CREATE TABLE IF NOT EXISTS schema_migrations (version INTEGER PRIMARY KEY, applied_at TEXT NOT NULL)`); err != nil {
		t.Fatal(err)
	}
	migs, err := Migrations(SetMain)
	if err != nil {
		t.Fatal(err)
	}
	var stalls *Migration
	for i := range migs {
		switch {
		case migs[i].Name == "0014_stalls.sql":
			stalls = &migs[i]
		case migs[i].Version < 14:
			if _, err := applyOne(ctx, d, migs[i]); err != nil {
				t.Fatalf("%s: %v", migs[i].Name, err)
			}
		}
	}
	if stalls == nil {
		t.Fatal("0014_stalls.sql is missing")
	}

	mustExec := func(q string, args ...any) {
		t.Helper()
		if _, err := d.ExecContext(ctx, q, args...); err != nil {
			t.Fatalf("%s: %v", q, err)
		}
	}
	mustExec(`INSERT INTO clients (id, name, kind, url, created_at) VALUES (1, 'qb', 'qbittorrent', 'http://qb', '2026-11-20T00:00:00.000Z')`)
	mustExec(`INSERT INTO downloads (client_id, client_ref, protocol, title_id, state, created_at, updated_at)
		VALUES (1, 'abc', 'torrent', 5, 'stalled', '2026-11-19T10:00:00.000Z', '2026-11-20T08:30:00.000Z')`)

	if ok, err := applyOne(ctx, d, *stalls); err != nil || !ok {
		t.Fatalf("applying 0014: %v %v", ok, err)
	}

	var at string
	if err := d.QueryRowContext(ctx, `SELECT progress_changed_at FROM downloads WHERE client_ref = 'abc'`).Scan(&at); err != nil {
		t.Fatal(err)
	}
	if at != "2026-11-20T08:30:00.000Z" {
		t.Errorf("progress_changed_at = %q, want the last update", at)
	}
	// A row added after the migration that says nothing gets an empty value, which
	// the downloads module reads as "the last update".
	mustExec(`INSERT INTO downloads (client_id, client_ref, protocol, title_id, created_at, updated_at)
		VALUES (1, 'def', 'torrent', 5, '2026-11-21T00:00:00.000Z', '2026-11-21T00:00:00.000Z')`)
	if err := d.QueryRowContext(ctx, `SELECT progress_changed_at FROM downloads WHERE client_ref = 'def'`).Scan(&at); err != nil || at != "" {
		t.Errorf("default = %q, %v", at, err)
	}
}
