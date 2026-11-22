package db

import (
	"context"
	"testing"
)

// 0013_polish.sql rebuilds user_limits so a limit of -1 ("unlimited") fits, and adds
// the import columns to downloads. Rows that were there before must survive.
func TestPolishMigrationKeepsLimitsAndAllowsUnlimited(t *testing.T) {
	ctx := context.Background()
	d := openTemp(t, "main.db")
	if _, err := d.ExecContext(ctx, `CREATE TABLE IF NOT EXISTS schema_migrations (version INTEGER PRIMARY KEY, applied_at TEXT NOT NULL)`); err != nil {
		t.Fatal(err)
	}
	migs, err := Migrations(SetMain)
	if err != nil {
		t.Fatal(err)
	}
	var polish *Migration
	for i := range migs {
		if migs[i].Name == "0013_polish.sql" {
			polish = &migs[i]
			continue
		}
		if migs[i].Version < 13 {
			if _, err := applyOne(ctx, d, migs[i]); err != nil {
				t.Fatalf("%s: %v", migs[i].Name, err)
			}
		}
	}
	if polish == nil {
		t.Fatal("0013_polish.sql is missing")
	}

	// A person with limits set the old way, one with only a flag, and a download.
	mustExec := func(q string, args ...any) {
		t.Helper()
		if _, err := d.ExecContext(ctx, q, args...); err != nil {
			t.Fatalf("%s: %v", q, err)
		}
	}
	mustExec(`INSERT INTO users (id, name, role, password_hash, created_at) VALUES (1, 'Mia', 'member', 'x', '2026-11-20T00:00:00.000Z'), (2, 'Noah', 'member', 'x', '2026-11-20T00:00:00.000Z')`)
	mustExec(`INSERT INTO user_limits (user_id, hd_per_week, uhd_per_week, auto_approve_hd, auto_approve_uhd) VALUES (1, 3, 0, 1, NULL)`)
	mustExec(`INSERT INTO user_limits (user_id, auto_approve_uhd) VALUES (2, 1)`)
	mustExec(`INSERT INTO clients (id, name, kind, url, created_at) VALUES (1, 'qb', 'qbittorrent', 'http://qb', '2026-11-20T00:00:00.000Z')`)
	mustExec(`INSERT INTO downloads (client_id, client_ref, protocol, title_id, created_at, updated_at) VALUES (1, 'abc', 'torrent', 5, '2026-11-20T00:00:00.000Z', '2026-11-20T00:00:00.000Z')`)

	if ok, err := applyOne(ctx, d, *polish); err != nil || !ok {
		t.Fatalf("applying 0013: %v %v", ok, err)
	}

	type row struct {
		hd, uhd, autoHD, autoUHD *int64
	}
	read := func(id int64) row {
		t.Helper()
		var r row
		if err := d.QueryRowContext(ctx, `SELECT hd_per_week, uhd_per_week, auto_approve_hd, auto_approve_uhd FROM user_limits WHERE user_id = ?`, id).
			Scan(&r.hd, &r.uhd, &r.autoHD, &r.autoUHD); err != nil {
			t.Fatal(err)
		}
		return r
	}
	mia, noah := read(1), read(2)
	if mia.hd == nil || *mia.hd != 3 || mia.uhd == nil || *mia.uhd != 0 || mia.autoHD == nil || *mia.autoHD != 1 || mia.autoUHD != nil {
		t.Errorf("Mia's limits changed: %+v", mia)
	}
	if noah.hd != nil || noah.uhd != nil || noah.autoHD != nil || noah.autoUHD == nil || *noah.autoUHD != 1 {
		t.Errorf("Noah's limits changed: %+v", noah)
	}

	// -1 now fits, below it does not, and the other checks are still there.
	mustExec(`UPDATE user_limits SET hd_per_week = -1 WHERE user_id = 1`)
	if got := read(1); got.hd == nil || *got.hd != -1 {
		t.Errorf("unlimited was not stored: %+v", got)
	}
	for _, bad := range []string{
		`UPDATE user_limits SET hd_per_week = -2 WHERE user_id = 1`,
		`UPDATE user_limits SET uhd_per_week = -2 WHERE user_id = 1`,
		`UPDATE user_limits SET auto_approve_hd = 2 WHERE user_id = 1`,
		`INSERT INTO user_limits (user_id) VALUES (99)`, // no such user
	} {
		if _, err := d.ExecContext(ctx, bad); err == nil {
			t.Errorf("the database accepted %q", bad)
		}
	}
	// Deleting a person still takes their limits along.
	mustExec(`DELETE FROM users WHERE id = 2`)
	var n int
	if err := d.QueryRowContext(ctx, `SELECT COUNT(*) FROM user_limits WHERE user_id = 2`).Scan(&n); err != nil || n != 0 {
		t.Errorf("limits of a deleted person: %d, %v", n, err)
	}

	// The download has its two new columns, empty, and the step is checked.
	var step, target string
	if err := d.QueryRowContext(ctx, `SELECT import_step, import_target FROM downloads WHERE client_ref = 'abc'`).Scan(&step, &target); err != nil || step != "" || target != "" {
		t.Errorf("import columns = %q %q, %v", step, target, err)
	}
	for _, ok := range []string{"checking", "renaming", "moving", "rescanning", "done", ""} {
		if _, err := d.ExecContext(ctx, `UPDATE downloads SET import_step = ?`, ok); err != nil {
			t.Errorf("step %q refused: %v", ok, err)
		}
	}
	if _, err := d.ExecContext(ctx, `UPDATE downloads SET import_step = 'flying'`); err == nil {
		t.Error("an unknown import step was accepted")
	}
}
