package requests

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"strings"
	"time"

	"github.com/BaileyPillon/marquee/internal/core/db"
)

// Store is the only place in this package that talks SQL.
//
// It reads two tables it does not own, only to show a request: titles (name,
// year, kind, poster) and users (name, role). It never writes them.
type Store struct {
	db *sql.DB
}

// NewStore wraps the main database.
func NewStore(d *sql.DB) *Store { return &Store{db: d} }

type dbx interface {
	ExecContext(ctx context.Context, query string, args ...any) (sql.Result, error)
	QueryContext(ctx context.Context, query string, args ...any) (*sql.Rows, error)
	QueryRowContext(ctx context.Context, query string, args ...any) *sql.Row
}

// queries run on the database or inside a transaction, depending on x.
type queries struct{ x dbx }

func (s *Store) q() queries { return queries{x: s.db} }

// inTx runs fn in one write transaction. The database is opened with
// BEGIN IMMEDIATE, so two writers queue: a check and the insert that follows it
// inside fn cannot be split by another ask.
func (s *Store) inTx(ctx context.Context, fn func(q queries) error) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	if err := fn(queries{x: tx}); err != nil {
		return err
	}
	return tx.Commit()
}

type scanner interface{ Scan(dest ...any) error }

func b2i(b bool) int {
	if b {
		return 1
	}
	return 0
}

func nullInt(n int64) any {
	if n == 0 {
		return nil
	}
	return n
}

func nullTime(t *time.Time) any {
	if t == nil {
		return nil
	}
	return db.TS(*t)
}

// ---- requests --------------------------------------------------------------

const itemSelect = `
	SELECT r.id, r.user_id, u.name, r.title_id, t.name, t.year, t.kind, t.poster_ref,
	       r.quality, r.scope, r.note, r.state, r.source, r.created_at, r.updated_at,
	       r.decided_by, d.name, r.decided_at, r.decline_reason, c.folder_id
	  FROM requests r
	  JOIN users  u ON u.id = r.user_id
	  JOIN titles t ON t.id = r.title_id
	  LEFT JOIN users  d ON d.id = r.decided_by
	  LEFT JOIN copies c ON c.title_id = r.title_id AND c.quality = r.quality`

func scanItem(s scanner) (Item, error) {
	var (
		it                     Item
		userName               string
		scope                  string
		created, updated       string
		decidedBy              sql.NullInt64
		decidedName, decidedAt sql.NullString
		folder                 sql.NullInt64
	)
	if err := s.Scan(&it.ID, &it.userID, &userName, &it.Title.ID, &it.Title.Name, &it.Title.Year, &it.Title.Kind,
		&it.Title.PosterRef, &it.Quality, &scope, &it.Note, &it.State, &it.Source, &created, &updated,
		&decidedBy, &decidedName, &decidedAt, &it.DeclineReason, &folder); err != nil {
		return Item{}, err
	}
	it.User = &UserRef{ID: it.userID, Name: userName}
	if err := json.Unmarshal([]byte(scope), &it.Scope); err != nil {
		return Item{}, err
	}
	var err error
	if it.CreatedAt, err = db.ParseTS(created); err != nil {
		return Item{}, err
	}
	if it.UpdatedAt, err = db.ParseTS(updated); err != nil {
		return Item{}, err
	}
	if decidedBy.Valid {
		it.DecidedBy = &UserRef{ID: decidedBy.Int64, Name: decidedName.String}
	}
	if decidedAt.Valid {
		t, err := db.ParseTS(decidedAt.String)
		if err != nil {
			return Item{}, err
		}
		it.DecidedAt = &t
	}
	it.folderID = folder.Int64
	return it, nil
}

func (q queries) item(ctx context.Context, id int64) (Item, error) {
	it, err := scanItem(q.x.QueryRowContext(ctx, itemSelect+` WHERE r.id = ?`, id))
	if errors.Is(err, sql.ErrNoRows) {
		return Item{}, ErrNotFound
	}
	return it, err
}

// itemFilter selects requests for list.
type itemFilter struct {
	UserID int64 // 0 = everyone
	State  string
	Limit  int
	Offset int
}

func (q queries) list(ctx context.Context, f itemFilter) ([]Item, int, error) {
	var (
		where []string
		args  []any
	)
	if f.UserID != 0 {
		where = append(where, `r.user_id = ?`)
		args = append(args, f.UserID)
	}
	if f.State != "" {
		where = append(where, `r.state = ?`)
		args = append(args, f.State)
	}
	cond := ""
	if len(where) > 0 {
		cond = ` WHERE ` + strings.Join(where, ` AND `)
	}
	var total int
	if err := q.x.QueryRowContext(ctx, `SELECT COUNT(*) FROM requests r`+cond, args...).Scan(&total); err != nil {
		return nil, 0, err
	}
	rows, err := q.x.QueryContext(ctx,
		itemSelect+cond+` ORDER BY r.created_at DESC, r.id DESC LIMIT ? OFFSET ?`,
		append(args, f.Limit, f.Offset)...)
	if err != nil {
		return nil, 0, err
	}
	defer rows.Close()
	out := []Item{}
	for rows.Next() {
		it, err := scanItem(rows)
		if err != nil {
			return nil, 0, err
		}
		out = append(out, it)
	}
	return out, total, rows.Err()
}

// openFor finds the open (pending, approved or available) request for a title
// and quality.
func (q queries) openFor(ctx context.Context, titleID int64, quality string) (int64, bool, error) {
	var id int64
	err := q.x.QueryRowContext(ctx,
		`SELECT id FROM requests WHERE title_id = ? AND quality = ? AND state IN ('pending', 'approved', 'available')
		 ORDER BY id LIMIT 1`, titleID, quality).Scan(&id)
	if errors.Is(err, sql.ErrNoRows) {
		return 0, false, nil
	}
	return id, err == nil, err
}

// usageTimes lists, oldest first, when the person's asks of a quality in the
// window were made. Declined and cancelled asks do not count.
func (q queries) usageTimes(ctx context.Context, userID int64, quality string, since time.Time) ([]time.Time, error) {
	rows, err := q.x.QueryContext(ctx,
		`SELECT created_at FROM requests
		  WHERE user_id = ? AND quality = ? AND state NOT IN ('declined', 'cancelled') AND created_at > ?
		  ORDER BY created_at, id`, userID, quality, db.TS(since))
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []time.Time
	for rows.Next() {
		var s string
		if err := rows.Scan(&s); err != nil {
			return nil, err
		}
		t, err := db.ParseTS(s)
		if err != nil {
			return nil, err
		}
		out = append(out, t)
	}
	return out, rows.Err()
}

// usageKey names one person's one quality.
type usageKey struct {
	user    int64
	quality string
}

// usageAll is usageTimes for everyone at once.
func (q queries) usageAll(ctx context.Context, since time.Time) (map[usageKey][]time.Time, error) {
	rows, err := q.x.QueryContext(ctx,
		`SELECT user_id, quality, created_at FROM requests
		  WHERE state NOT IN ('declined', 'cancelled') AND created_at > ?
		  ORDER BY created_at, id`, db.TS(since))
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[usageKey][]time.Time{}
	for rows.Next() {
		var (
			k usageKey
			s string
		)
		if err := rows.Scan(&k.user, &k.quality, &s); err != nil {
			return nil, err
		}
		t, err := db.ParseTS(s)
		if err != nil {
			return nil, err
		}
		out[k] = append(out[k], t)
	}
	return out, rows.Err()
}

// newRequest is an ask about to be stored.
type newRequest struct {
	UserID    int64
	TitleID   int64
	Kind      string
	Quality   string
	Scope     Scope
	Note      string
	Source    string
	SourceRef string
	Now       time.Time
}

// limitCheck makes create refuse when the window already holds Limit asks.
type limitCheck struct{ Limit int }

// autoApproval makes create store the ask already approved, with its copy.
type autoApproval struct {
	DecidedBy int64 // 0 when the rules approved it
	FolderID  int64
}

// create checks for an open ask and for the weekly limit, then stores the ask
// (and, for an auto-approval, its copy), all in one transaction. It returns the
// new id and the copy id (0 when the ask is pending).
//
// A duplicate comes back as duplicateID, a used-up limit as *LimitError.
func (s *Store) create(ctx context.Context, nr newRequest, lim *limitCheck, auto *autoApproval) (id, copyID int64, err error) {
	scope, err := json.Marshal(nr.Scope)
	if err != nil {
		return 0, 0, err
	}
	err = s.inTx(ctx, func(q queries) error {
		if existing, ok, err := q.openFor(ctx, nr.TitleID, nr.Quality); err != nil {
			return err
		} else if ok {
			return duplicateID(existing)
		}
		if lim != nil {
			times, err := q.usageTimes(ctx, nr.UserID, nr.Quality, nr.Now.Add(-Window))
			if err != nil {
				return err
			}
			if len(times) >= lim.Limit {
				l := lim.Limit
				return &LimitError{Quality: nr.Quality, Used: len(times), Limit: l, ResetsAt: resetsAt(times, &l)}
			}
		}
		ts := db.TS(nr.Now)
		state, decidedBy, decidedAt := StatePending, any(nil), any(nil)
		if auto != nil {
			state, decidedBy, decidedAt = StateApproved, nullInt(auto.DecidedBy), ts
		}
		res, err := q.x.ExecContext(ctx,
			`INSERT INTO requests (user_id, title_id, kind, quality, scope, note, state, decided_by, decided_at,
			                       source, source_ref, created_at, updated_at)
			 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
			nr.UserID, nr.TitleID, nr.Kind, nr.Quality, string(scope), nr.Note, state, decidedBy, decidedAt,
			nr.Source, nr.SourceRef, ts, ts)
		if err != nil {
			return err
		}
		if id, err = res.LastInsertId(); err != nil {
			return err
		}
		if auto != nil {
			copyID, err = q.ensureCopy(ctx, nr.TitleID, nr.Quality, auto.FolderID, nr.Now)
		}
		return err
	})
	if db.IsUniqueViolation(err) {
		// Two asks for the same thing at the same moment: the index caught the
		// second.
		if existing, ok, ferr := s.q().openFor(ctx, nr.TitleID, nr.Quality); ferr == nil && ok {
			return 0, 0, duplicateID(existing)
		}
	}
	return id, copyID, err
}

// approve moves a waiting request to approved, makes sure its copy exists (in
// folderID) and, if asked, turns on 4K auto-approve for the person who asked.
func (s *Store) approve(ctx context.Context, id, decidedBy, folderID int64, now time.Time, alwaysAllowUHD bool) (copyID int64, err error) {
	err = s.inTx(ctx, func(q queries) error {
		ts := db.TS(now)
		res, err := q.x.ExecContext(ctx,
			`UPDATE requests SET state = 'approved', decided_by = ?, decided_at = ?, updated_at = ?
			  WHERE id = ? AND state = 'pending'`, nullInt(decidedBy), ts, ts, id)
		if err != nil {
			return err
		}
		if n, _ := res.RowsAffected(); n == 0 {
			return q.whyNotUpdated(ctx, id)
		}
		var (
			titleID, userID int64
			quality         string
		)
		if err := q.x.QueryRowContext(ctx, `SELECT title_id, user_id, quality FROM requests WHERE id = ?`, id).
			Scan(&titleID, &userID, &quality); err != nil {
			return err
		}
		if copyID, err = q.ensureCopy(ctx, titleID, quality, folderID, now); err != nil {
			return err
		}
		if alwaysAllowUHD {
			if _, err := q.x.ExecContext(ctx,
				`INSERT INTO user_limits (user_id, auto_approve_uhd) VALUES (?, 1)
				 ON CONFLICT (user_id) DO UPDATE SET auto_approve_uhd = 1`, userID); err != nil {
				return err
			}
		}
		return nil
	})
	return copyID, err
}

// whyNotUpdated explains an UPDATE that touched no row: no such request, or one
// that is not in the state the change needs.
func (q queries) whyNotUpdated(ctx context.Context, id int64) error {
	var one int
	err := q.x.QueryRowContext(ctx, `SELECT 1 FROM requests WHERE id = ?`, id).Scan(&one)
	if errors.Is(err, sql.ErrNoRows) {
		return ErrNotFound
	}
	if err != nil {
		return err
	}
	return ErrNotPending
}

// decline moves a waiting request to declined.
func (s *Store) decline(ctx context.Context, id, decidedBy int64, reason string, now time.Time) error {
	return s.inTx(ctx, func(q queries) error {
		ts := db.TS(now)
		res, err := q.x.ExecContext(ctx,
			`UPDATE requests SET state = 'declined', decided_by = ?, decided_at = ?, decline_reason = ?, updated_at = ?
			  WHERE id = ? AND state = 'pending'`, nullInt(decidedBy), ts, reason, ts, id)
		if err != nil {
			return err
		}
		if n, _ := res.RowsAffected(); n == 0 {
			return q.whyNotUpdated(ctx, id)
		}
		return nil
	})
}

// errWrongState is transition's answer for a request in another state.
var errWrongState = errors.New("requests: wrong state")

// transition changes a request's state, but only from one of the states in
// from. It reports ErrNotFound for no such request and errWrongState for one
// in another state.
func (s *Store) transition(ctx context.Context, id int64, from []string, to string, now time.Time) error {
	marks := strings.TrimSuffix(strings.Repeat("?,", len(from)), ",")
	args := []any{to, db.TS(now), id}
	for _, f := range from {
		args = append(args, f)
	}
	res, err := s.db.ExecContext(ctx,
		`UPDATE requests SET state = ?, updated_at = ? WHERE id = ? AND state IN (`+marks+`)`, args...)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n > 0 {
		return nil
	}
	var one int
	err = s.db.QueryRowContext(ctx, `SELECT 1 FROM requests WHERE id = ?`, id).Scan(&one)
	if errors.Is(err, sql.ErrNoRows) {
		return ErrNotFound
	}
	if err != nil {
		return err
	}
	return errWrongState
}

// stateCounts counts requests by state; userID 0 means everyone's.
func (q queries) stateCounts(ctx context.Context, userID int64) (map[string]int, error) {
	rows, err := q.x.QueryContext(ctx,
		`SELECT state, COUNT(*) FROM requests WHERE (? = 0 OR user_id = ?) GROUP BY state`, userID, userID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[string]int{}
	for rows.Next() {
		var (
			state string
			n     int
		)
		if err := rows.Scan(&state, &n); err != nil {
			return nil, err
		}
		out[state] = n
	}
	return out, rows.Err()
}

// ---- copies ----------------------------------------------------------------

// copyRow is one copy with its title's kind.
type copyRow struct {
	ID        int64
	TitleID   int64
	Kind      string
	Quality   string
	FolderID  int64 // 0 when none
	Monitored bool
}

// ensureCopy makes the copy of a title at a quality exist and be monitored. A
// folder the copy already has is kept.
func (q queries) ensureCopy(ctx context.Context, titleID int64, quality string, folderID int64, now time.Time) (int64, error) {
	if _, err := q.x.ExecContext(ctx,
		`INSERT INTO copies (title_id, quality, folder_id, monitored, created_at) VALUES (?, ?, ?, 1, ?)
		 ON CONFLICT (title_id, quality) DO UPDATE SET monitored = 1,
		   folder_id = COALESCE(copies.folder_id, excluded.folder_id)`,
		titleID, quality, nullInt(folderID), db.TS(now)); err != nil {
		return 0, err
	}
	var id int64
	err := q.x.QueryRowContext(ctx, `SELECT id FROM copies WHERE title_id = ? AND quality = ?`, titleID, quality).Scan(&id)
	return id, err
}

const copySelect = `
	SELECT c.id, c.title_id, t.kind, c.quality, c.folder_id, c.monitored
	  FROM copies c JOIN titles t ON t.id = c.title_id`

func scanCopy(s scanner) (copyRow, error) {
	var (
		c      copyRow
		folder sql.NullInt64
		mon    int
	)
	if err := s.Scan(&c.ID, &c.TitleID, &c.Kind, &c.Quality, &folder, &mon); err != nil {
		return copyRow{}, err
	}
	c.FolderID, c.Monitored = folder.Int64, mon != 0
	return c, nil
}

func (q queries) copyByID(ctx context.Context, id int64) (copyRow, error) {
	c, err := scanCopy(q.x.QueryRowContext(ctx, copySelect+` WHERE c.id = ?`, id))
	if errors.Is(err, sql.ErrNoRows) {
		return copyRow{}, ErrCopyNotFound
	}
	return c, err
}

// wantedCopies lists the copies that are monitored, whose title is monitored
// too.
func (q queries) wantedCopies(ctx context.Context) ([]copyRow, error) {
	rows, err := q.x.QueryContext(ctx, copySelect+` WHERE c.monitored = 1 AND t.monitored = 1 ORDER BY c.id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []copyRow
	for rows.Next() {
		c, err := scanCopy(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, c)
	}
	return out, rows.Err()
}

func (q queries) setCopyFolder(ctx context.Context, id, folderID int64) error {
	_, err := q.x.ExecContext(ctx, `UPDATE copies SET folder_id = ? WHERE id = ? AND folder_id IS NULL`, nullInt(folderID), id)
	return err
}

// copyKey names one copy.
type copyKey struct {
	title   int64
	quality string
}

// liveScopes lists the scopes of the requests that stand behind each copy (the
// approved ones and those that went on from there).
func (q queries) liveScopes(ctx context.Context) (map[copyKey][]Scope, error) {
	rows, err := q.x.QueryContext(ctx,
		`SELECT title_id, quality, scope FROM requests WHERE state IN ('approved', 'available', 'partial', 'failed')`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[copyKey][]Scope{}
	for rows.Next() {
		var (
			k  copyKey
			sc string
		)
		if err := rows.Scan(&k.title, &k.quality, &sc); err != nil {
			return nil, err
		}
		var s Scope
		if err := json.Unmarshal([]byte(sc), &s); err != nil {
			return nil, err
		}
		out[k] = append(out[k], s)
	}
	return out, rows.Err()
}

// ---- people and limits -----------------------------------------------------

type userRow struct {
	ID       int64
	Name     string
	Role     string
	Disabled bool
}

func (q queries) users(ctx context.Context) ([]userRow, error) {
	rows, err := q.x.QueryContext(ctx, `SELECT id, name, role, disabled FROM users ORDER BY id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []userRow
	for rows.Next() {
		var (
			u   userRow
			dis int
		)
		if err := rows.Scan(&u.ID, &u.Name, &u.Role, &dis); err != nil {
			return nil, err
		}
		u.Disabled = dis != 0
		out = append(out, u)
	}
	return out, rows.Err()
}

func (q queries) user(ctx context.Context, id int64) (userRow, error) {
	var (
		u   userRow
		dis int
	)
	err := q.x.QueryRowContext(ctx, `SELECT id, name, role, disabled FROM users WHERE id = ?`, id).
		Scan(&u.ID, &u.Name, &u.Role, &dis)
	if errors.Is(err, sql.ErrNoRows) {
		return userRow{}, ErrUserNotFound
	}
	u.Disabled = dis != 0
	return u, err
}

// fromNulls turns the four nullable columns of user_limits into overrides.
func fromNulls(hd, uhd, autoHD, autoUHD sql.NullInt64) LimitOverrides {
	var o LimitOverrides
	if hd.Valid {
		v := int(hd.Int64)
		o.HDPerWeek = &v
	}
	if uhd.Valid {
		v := int(uhd.Int64)
		o.UHDPerWeek = &v
	}
	if autoHD.Valid {
		v := autoHD.Int64 != 0
		o.AutoApproveHD = &v
	}
	if autoUHD.Valid {
		v := autoUHD.Int64 != 0
		o.AutoApproveUHD = &v
	}
	return o
}

// overrides returns one person's overrides (all nil when they have none).
func (q queries) overrides(ctx context.Context, userID int64) (LimitOverrides, error) {
	var hd, uhd, autoHD, autoUHD sql.NullInt64
	err := q.x.QueryRowContext(ctx,
		`SELECT hd_per_week, uhd_per_week, auto_approve_hd, auto_approve_uhd FROM user_limits WHERE user_id = ?`, userID).
		Scan(&hd, &uhd, &autoHD, &autoUHD)
	if errors.Is(err, sql.ErrNoRows) {
		return LimitOverrides{}, nil
	}
	if err != nil {
		return LimitOverrides{}, err
	}
	return fromNulls(hd, uhd, autoHD, autoUHD), nil
}

// allOverrides returns every person's overrides by user id.
func (q queries) allOverrides(ctx context.Context) (map[int64]LimitOverrides, error) {
	rows, err := q.x.QueryContext(ctx,
		`SELECT user_id, hd_per_week, uhd_per_week, auto_approve_hd, auto_approve_uhd FROM user_limits`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[int64]LimitOverrides{}
	for rows.Next() {
		var (
			id                       int64
			hd, uhd, autoHD, autoUHD sql.NullInt64
		)
		if err := rows.Scan(&id, &hd, &uhd, &autoHD, &autoUHD); err != nil {
			return nil, err
		}
		out[id] = fromNulls(hd, uhd, autoHD, autoUHD)
	}
	return out, rows.Err()
}

// setOverrides applies a patch to one person's overrides and returns the result.
func (s *Store) setOverrides(ctx context.Context, userID int64, p LimitsPatch) (LimitOverrides, error) {
	var out LimitOverrides
	err := s.inTx(ctx, func(q queries) error {
		if _, err := q.user(ctx, userID); err != nil {
			return err
		}
		if _, err := q.x.ExecContext(ctx, `INSERT OR IGNORE INTO user_limits (user_id) VALUES (?)`, userID); err != nil {
			return err
		}
		set := func(column string, isSet bool, v any) error {
			if !isSet {
				return nil
			}
			_, err := q.x.ExecContext(ctx, `UPDATE user_limits SET `+column+` = ? WHERE user_id = ?`, v, userID)
			return err
		}
		intVal := func(p *int) any {
			if p == nil {
				return nil
			}
			return *p
		}
		boolVal := func(p *bool) any {
			if p == nil {
				return nil
			}
			return b2i(*p)
		}
		for _, step := range []error{
			set("hd_per_week", p.HDPerWeek.Set, intVal(p.HDPerWeek.Value)),
			set("uhd_per_week", p.UHDPerWeek.Set, intVal(p.UHDPerWeek.Value)),
			set("auto_approve_hd", p.AutoApproveHD.Set, boolVal(p.AutoApproveHD.Value)),
			set("auto_approve_uhd", p.AutoApproveUHD.Set, boolVal(p.AutoApproveUHD.Value)),
		} {
			if step != nil {
				return step
			}
		}
		var err error
		out, err = q.overrides(ctx, userID)
		return err
	})
	return out, err
}
