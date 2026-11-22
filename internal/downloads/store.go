package downloads

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"strings"
	"time"

	"github.com/BaileyPillon/marquee/internal/core/db"
	"github.com/BaileyPillon/marquee/internal/core/pathmap"
)

// Store is the only place in this package that talks SQL.
type Store struct {
	db *sql.DB
}

// NewStore wraps the main database.
func NewStore(d *sql.DB) *Store { return &Store{db: d} }

type scanner interface{ Scan(dest ...any) error }

func placeholders(n int) string {
	if n <= 0 {
		return ""
	}
	return strings.TrimSuffix(strings.Repeat("?,", n), ",")
}

func anyList(in []string) []any {
	out := make([]any, len(in))
	for i, v := range in {
		out[i] = v
	}
	return out
}

func parseOptTS(s sql.NullString) (*time.Time, error) {
	if !s.Valid || s.String == "" {
		return nil, nil
	}
	t, err := db.ParseTS(s.String)
	if err != nil {
		return nil, err
	}
	return &t, nil
}

func optTS(t *time.Time) any {
	if t == nil {
		return nil
	}
	return db.TS(*t)
}

// ---- clients -----------------------------------------------------------------

// clientRow is a row of the clients table, secrets still sealed.
type clientRow struct {
	ID             int64
	Name           string
	Kind           string
	URL            string
	Username       string
	PasswordSealed string
	APIKeySealed   string
	Category       string
	SavePath       string // "" = the client's own default
	Enabled        bool
	Priority       int
	LastOKAt       *time.Time
	LastError      string
	LastErrorAt    *time.Time
	CreatedAt      time.Time
	// PathMap says how the client's folders look from Marquee's side (the
	// client's From becomes Marquee's To).
	PathMap []pathmap.Rule
}

const clientCols = `id, name, kind, url, username, password_sealed, api_key_sealed, category, save_path,
	enabled, priority, last_ok_at, last_error, last_error_at, created_at, path_map`

func scanClient(s scanner) (clientRow, error) {
	var (
		c                 clientRow
		savePath          sql.NullString
		enabled           int
		lastOK, lastErrAt sql.NullString
		created           string
		pathMap           string
	)
	if err := s.Scan(&c.ID, &c.Name, &c.Kind, &c.URL, &c.Username, &c.PasswordSealed, &c.APIKeySealed, &c.Category,
		&savePath, &enabled, &c.Priority, &lastOK, &c.LastError, &lastErrAt, &created, &pathMap); err != nil {
		return clientRow{}, err
	}
	c.SavePath = savePath.String
	if rules, err := pathmap.Parse(pathMap); err == nil {
		c.PathMap = rules
	} else {
		c.PathMap = []pathmap.Rule{}
	}
	c.Enabled = enabled == 1
	var err error
	if c.LastOKAt, err = parseOptTS(lastOK); err != nil {
		return clientRow{}, err
	}
	if c.LastErrorAt, err = parseOptTS(lastErrAt); err != nil {
		return clientRow{}, err
	}
	if c.CreatedAt, err = db.ParseTS(created); err != nil {
		return clientRow{}, err
	}
	return c, nil
}

func nullable(s string) any {
	if s == "" {
		return nil
	}
	return s
}

func boolInt(b bool) int {
	if b {
		return 1
	}
	return 0
}

// InsertClient adds a client. A name that is taken gives ErrNameTaken.
func (s *Store) insertClient(ctx context.Context, c clientRow, now time.Time) (clientRow, error) {
	res, err := s.db.ExecContext(ctx, `
		INSERT INTO clients (name, kind, url, username, password_sealed, api_key_sealed, category, save_path,
		                     enabled, priority, created_at, path_map)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		c.Name, c.Kind, c.URL, c.Username, c.PasswordSealed, c.APIKeySealed, c.Category, nullable(c.SavePath),
		boolInt(c.Enabled), c.Priority, db.TS(now), pathmap.Encode(c.PathMap))
	if err != nil {
		if db.IsUniqueViolation(err) {
			return clientRow{}, ErrNameTaken
		}
		return clientRow{}, err
	}
	id, err := res.LastInsertId()
	if err != nil {
		return clientRow{}, err
	}
	return s.getClient(ctx, id)
}

func (s *Store) getClient(ctx context.Context, id int64) (clientRow, error) {
	c, err := scanClient(s.db.QueryRowContext(ctx, `SELECT `+clientCols+` FROM clients WHERE id = ?`, id))
	if errors.Is(err, sql.ErrNoRows) {
		return clientRow{}, ErrClientNotFound
	}
	return c, err
}

func (s *Store) listClients(ctx context.Context) ([]clientRow, error) {
	return s.queryClients(ctx, `SELECT `+clientCols+` FROM clients ORDER BY priority, id`)
}

// enabledClients lists the enabled clients of the given kinds (all kinds if
// none are given), lowest priority number first.
func (s *Store) enabledClients(ctx context.Context, kinds ...string) ([]clientRow, error) {
	q := `SELECT ` + clientCols + ` FROM clients WHERE enabled = 1`
	var args []any
	if len(kinds) > 0 {
		q += ` AND kind IN (` + placeholders(len(kinds)) + `)`
		args = anyList(kinds)
	}
	return s.queryClients(ctx, q+` ORDER BY priority, id`, args...)
}

func (s *Store) queryClients(ctx context.Context, q string, args ...any) ([]clientRow, error) {
	rows, err := s.db.QueryContext(ctx, q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []clientRow
	for rows.Next() {
		c, err := scanClient(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, c)
	}
	return out, rows.Err()
}

// updateClient replaces everything the owner can edit.
func (s *Store) updateClient(ctx context.Context, c clientRow) error {
	res, err := s.db.ExecContext(ctx, `
		UPDATE clients SET name = ?, url = ?, username = ?, password_sealed = ?, api_key_sealed = ?,
		                   category = ?, save_path = ?, enabled = ?, priority = ?, path_map = ?
		WHERE id = ?`,
		c.Name, c.URL, c.Username, c.PasswordSealed, c.APIKeySealed, c.Category, nullable(c.SavePath),
		boolInt(c.Enabled), c.Priority, pathmap.Encode(c.PathMap), c.ID)
	if err != nil {
		if db.IsUniqueViolation(err) {
			return ErrNameTaken
		}
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrClientNotFound
	}
	return nil
}

func (s *Store) deleteClient(ctx context.Context, id int64) error {
	res, err := s.db.ExecContext(ctx, `DELETE FROM clients WHERE id = ?`, id)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrClientNotFound
	}
	return nil
}

// countLive counts a client's downloads that are not over yet.
func (s *Store) countLive(ctx context.Context, clientID int64) (int, error) {
	var n int
	err := s.db.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM downloads WHERE client_id = ? AND state IN (`+placeholders(len(openStates))+`)`,
		append([]any{clientID}, anyList(openStates)...)...).Scan(&n)
	return n, err
}

func (s *Store) clientOK(ctx context.Context, id int64, now time.Time) error {
	_, err := s.db.ExecContext(ctx,
		`UPDATE clients SET last_ok_at = ?, last_error = '', last_error_at = NULL WHERE id = ?`, db.TS(now), id)
	return err
}

func (s *Store) clientFailed(ctx context.Context, id int64, msg string, now time.Time) error {
	_, err := s.db.ExecContext(ctx,
		`UPDATE clients SET last_error = ?, last_error_at = ? WHERE id = ?`, msg, db.TS(now), id)
	return err
}

// ---- downloads ---------------------------------------------------------------

// Download is one tracked download: a row of the downloads table.
type Download struct {
	ID           int64
	ClientID     int64
	ClientRef    string
	Protocol     string
	TitleID      int64
	CopyID       int64 // 0 = none
	Kind         string
	Quality      string
	EpisodeIDs   []int64
	ReleaseTitle string
	IndexerID    int64 // 0 = none
	GUID         string
	Size         int64
	State        string
	Progress     float64
	SpeedBps     int64
	ETA          int // seconds, -1 = unknown
	SavePath     string
	ContentPath  string
	Error        string
	CreatedAt    time.Time
	UpdatedAt    time.Time
	CompletedAt  *time.Time
	// ImportStep is which step of its import the download is on (one of the Step
	// constants), empty when it has not started one. ImportTarget is where the
	// import is putting the files, set once the destination is known.
	ImportStep   string
	ImportTarget string
}

const downloadCols = `d.id, d.client_id, d.client_ref, d.protocol, d.title_id, d.copy_id, d.kind, d.quality,
	d.episode_ids, d.release_title, d.indexer_id, d.guid, d.size, d.state, d.progress, d.speed_bps, d.eta_seconds,
	d.save_path, d.content_path, d.error, d.created_at, d.updated_at, d.completed_at, d.import_step, d.import_target`

func scanDownload(s scanner, extra ...any) (Download, error) {
	var (
		d                 Download
		copyID, indexerID sql.NullInt64
		episodes          string
		created, updated  string
		completed         sql.NullString
	)
	dest := append([]any{&d.ID, &d.ClientID, &d.ClientRef, &d.Protocol, &d.TitleID, &copyID, &d.Kind, &d.Quality,
		&episodes, &d.ReleaseTitle, &indexerID, &d.GUID, &d.Size, &d.State, &d.Progress, &d.SpeedBps, &d.ETA,
		&d.SavePath, &d.ContentPath, &d.Error, &created, &updated, &completed, &d.ImportStep, &d.ImportTarget}, extra...)
	if err := s.Scan(dest...); err != nil {
		return Download{}, err
	}
	d.CopyID, d.IndexerID = copyID.Int64, indexerID.Int64
	if err := json.Unmarshal([]byte(episodes), &d.EpisodeIDs); err != nil {
		d.EpisodeIDs = nil
	}
	var err error
	if d.CreatedAt, err = db.ParseTS(created); err != nil {
		return Download{}, err
	}
	if d.UpdatedAt, err = db.ParseTS(updated); err != nil {
		return Download{}, err
	}
	if d.CompletedAt, err = parseOptTS(completed); err != nil {
		return Download{}, err
	}
	return d, nil
}

// insertDownload records a new download. If the client already has a row for
// that ref: one that is over (imported, failed, removed) is reused for the new
// grab; one still running is left alone and its id is returned with
// existing=true.
func (s *Store) insertDownload(ctx context.Context, d Download, now time.Time) (id int64, existing bool, err error) {
	episodes, err := json.Marshal(append([]int64{}, d.EpisodeIDs...))
	if err != nil {
		return 0, false, err
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return 0, false, err
	}
	defer func() { _ = tx.Rollback() }()

	var (
		oldID    int64
		oldState string
	)
	switch err := tx.QueryRowContext(ctx,
		`SELECT id, state FROM downloads WHERE client_id = ? AND client_ref = ?`, d.ClientID, d.ClientRef).
		Scan(&oldID, &oldState); {
	case err == nil:
		if !isTerminal(oldState) {
			return oldID, true, tx.Commit()
		}
		_, err = tx.ExecContext(ctx, `
			UPDATE downloads SET protocol = ?, title_id = ?, copy_id = ?, kind = ?, quality = ?, episode_ids = ?,
			       release_title = ?, indexer_id = ?, guid = ?, size = ?, state = ?, progress = 0, speed_bps = 0,
			       eta_seconds = -1, save_path = '', content_path = '', error = '', created_at = ?, updated_at = ?,
			       completed_at = NULL, import_step = '', import_target = ''
			WHERE id = ?`,
			d.Protocol, d.TitleID, nullInt(d.CopyID), d.Kind, d.Quality, string(episodes),
			d.ReleaseTitle, nullInt(d.IndexerID), d.GUID, d.Size, d.State, db.TS(now), db.TS(now), oldID)
		if err != nil {
			return 0, false, err
		}
		return oldID, false, tx.Commit()
	case !errors.Is(err, sql.ErrNoRows):
		return 0, false, err
	}

	res, err := tx.ExecContext(ctx, `
		INSERT INTO downloads (client_id, client_ref, protocol, title_id, copy_id, kind, quality, episode_ids,
		                       release_title, indexer_id, guid, size, state, created_at, updated_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		d.ClientID, d.ClientRef, d.Protocol, d.TitleID, nullInt(d.CopyID), d.Kind, d.Quality, string(episodes),
		d.ReleaseTitle, nullInt(d.IndexerID), d.GUID, d.Size, d.State, db.TS(now), db.TS(now))
	if err != nil {
		return 0, false, err
	}
	id, err = res.LastInsertId()
	if err != nil {
		return 0, false, err
	}
	return id, false, tx.Commit()
}

func nullInt(n int64) any {
	if n == 0 {
		return nil
	}
	return n
}

func (s *Store) getDownload(ctx context.Context, id int64) (Download, error) {
	d, err := scanDownload(s.db.QueryRowContext(ctx, `SELECT `+downloadCols+` FROM downloads d WHERE d.id = ?`, id))
	if errors.Is(err, sql.ErrNoRows) {
		return Download{}, ErrNotFound
	}
	return d, err
}

// trackedFor lists a client's downloads a poll still has to follow.
func (s *Store) trackedFor(ctx context.Context, clientID int64) ([]Download, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT `+downloadCols+` FROM downloads d WHERE d.client_id = ? AND d.state IN (`+placeholders(len(activeStates))+`) ORDER BY d.id`,
		append([]any{clientID}, anyList(activeStates)...)...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Download
	for rows.Next() {
		d, err := scanDownload(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, d)
	}
	return out, rows.Err()
}

// anyBusy reports whether any download is moving, so the poll should keep its
// fast pace.
func (s *Store) anyBusy(ctx context.Context) (bool, error) {
	var one int
	err := s.db.QueryRowContext(ctx,
		`SELECT 1 FROM downloads WHERE state IN (`+placeholders(len(busyStates))+`) LIMIT 1`, anyList(busyStates)...).Scan(&one)
	if errors.Is(err, sql.ErrNoRows) {
		return false, nil
	}
	return err == nil, err
}

// dlUpdate is what a poll writes to a download.
type dlUpdate struct {
	State       string
	Progress    float64
	SpeedBps    int64
	ETA         int
	Size        int64  // 0 keeps the stored size
	SavePath    string // "" keeps what is stored
	ContentPath string // "" keeps what is stored
	Error       string
	Completed   bool // stamp completed_at
}

// update writes u to the download, but only while it is still in one of the
// states in from. It reports whether a row changed, which is how a transition
// is claimed exactly once even if two polls overlap.
func (s *Store) update(ctx context.Context, id int64, from []string, u dlUpdate, now time.Time) (bool, error) {
	completedAt := any(nil)
	if u.Completed {
		completedAt = db.TS(now)
	}
	res, err := s.db.ExecContext(ctx, `
		UPDATE downloads SET state = ?, progress = ?, speed_bps = ?, eta_seconds = ?,
		       size = CASE WHEN ? > 0 THEN ? ELSE size END,
		       save_path = CASE WHEN ? != '' THEN ? ELSE save_path END,
		       content_path = CASE WHEN ? != '' THEN ? ELSE content_path END,
		       error = ?, updated_at = ?, completed_at = COALESCE(?, completed_at)
		WHERE id = ? AND state IN (`+placeholders(len(from))+`)`,
		append([]any{u.State, u.Progress, u.SpeedBps, u.ETA, u.Size, u.Size, u.SavePath, u.SavePath,
			u.ContentPath, u.ContentPath, u.Error, db.TS(now), completedAt, id}, anyList(from)...)...)
	if err != nil {
		return false, err
	}
	n, _ := res.RowsAffected()
	return n == 1, nil
}

// setState moves a download to a state, whatever state it is in.
//
// The import step follows the state: a finished import is "done", an import that
// starts (or is started again) has no step yet, and one that failed keeps the
// step it was on, so the screen can say where it stopped.
func (s *Store) setState(ctx context.Context, id int64, state, errText string, now time.Time) error {
	res, err := s.db.ExecContext(ctx, `
		UPDATE downloads SET state = ?, error = ?, speed_bps = 0, eta_seconds = -1, updated_at = ?,
		       import_step = CASE WHEN ? = 'imported' THEN 'done' WHEN ? IN ('importing', 'completed') THEN '' ELSE import_step END,
		       import_target = CASE WHEN ? IN ('importing', 'completed') THEN '' ELSE import_target END
		WHERE id = ?`,
		state, errText, db.TS(now), state, state, state, id)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrNotFound
	}
	return nil
}

// setImportStep records which step an import is on and, when it is not empty,
// where it is putting the files. It writes only while the download is importing,
// so a late step cannot undo the final state.
func (s *Store) setImportStep(ctx context.Context, id int64, step, target string, now time.Time) (bool, error) {
	res, err := s.db.ExecContext(ctx, `
		UPDATE downloads SET import_step = ?, import_target = CASE WHEN ? != '' THEN ? ELSE import_target END, updated_at = ?
		WHERE id = ? AND state = 'importing'`,
		step, target, target, db.TS(now), id)
	if err != nil {
		return false, err
	}
	n, _ := res.RowsAffected()
	return n == 1, nil
}

// ---- views (downloads joined to the title and the client) --------------------

// TitleRef is the small piece of a title a download shows.
type TitleRef struct {
	ID        int64  `json:"id"`
	Name      string `json:"name"`
	Year      int    `json:"year"`
	Kind      string `json:"kind"`
	PosterRef string `json:"posterRef"`
}

// ClientRef is the small piece of a client a download shows.
type ClientRef struct {
	ID   int64  `json:"id"`
	Name string `json:"name"`
	Kind string `json:"kind"`
}

// View is a download as the API shows it.
type View struct {
	ID           int64      `json:"id"`
	Title        *TitleRef  `json:"title"` // null when the title has since been deleted
	Client       ClientRef  `json:"client"`
	CopyID       *int64     `json:"copyId"`
	Protocol     string     `json:"protocol"`
	Kind         string     `json:"kind"`
	Quality      string     `json:"quality"`
	EpisodeIDs   []int64    `json:"episodeIds"`
	ReleaseTitle string     `json:"releaseTitle"`
	State        string     `json:"state"`
	Progress     float64    `json:"progress"` // 0 to 1
	SpeedBps     int64      `json:"speedBps"`
	ETASeconds   *int64     `json:"etaSeconds"` // null when unknown
	Size         int64      `json:"size"`
	Error        string     `json:"error"`
	CreatedAt    time.Time  `json:"createdAt"`
	UpdatedAt    time.Time  `json:"updatedAt"`
	CompletedAt  *time.Time `json:"completedAt"`
	// ImportStep is where the download's import is up to: checking, renaming,
	// moving, rescanning or done; empty before an import starts.
	ImportStep string `json:"importStep"`
	// ImportTarget is the path the import is putting the files in. It is the
	// owner's own folder layout, so it is only for the owner and admins; every
	// route that returns a View is guarded that way.
	ImportTarget string `json:"importTarget"`
	// Outcome is how a finished download ended, for the history list: imported,
	// upgraded (its import replaced a worse file), failed or removed. It is empty
	// while the download is still going.
	Outcome string `json:"outcome"`
}

// Outcomes of a download that is over.
const (
	OutcomeImported = "imported"
	OutcomeUpgraded = "upgraded"
	OutcomeFailed   = "failed"
	OutcomeRemoved  = "removed"
)

// The join reads the titles table directly: a download shows its title's name,
// year and poster, and a lightweight join is cheaper than asking the titles
// module once per row. Nothing is written there. The same goes for the history
// table, read only to see whether the download's import was an upgrade.
const viewSelect = `SELECT ` + downloadCols + `,
	c.name, c.kind, t.id, t.name, t.year, t.kind, t.poster_ref,
	d.state = 'imported' AND EXISTS (SELECT 1 FROM history h WHERE h.download_id = d.id AND h.event = 'upgraded')
	FROM downloads d
	JOIN clients c ON c.id = d.client_id
	LEFT JOIN titles t ON t.id = d.title_id`

func scanView(s scanner) (View, error) {
	var (
		cname, ckind sql.NullString
		tid, tyear   sql.NullInt64
		tname, tkind sql.NullString
		tposter      sql.NullString
		upgraded     bool
	)
	d, err := scanDownload(s, &cname, &ckind, &tid, &tname, &tyear, &tkind, &tposter, &upgraded)
	if err != nil {
		return View{}, err
	}
	v := View{
		ID:           d.ID,
		Client:       ClientRef{ID: d.ClientID, Name: cname.String, Kind: ckind.String},
		Protocol:     d.Protocol,
		Kind:         d.Kind,
		Quality:      d.Quality,
		EpisodeIDs:   d.EpisodeIDs,
		ReleaseTitle: d.ReleaseTitle,
		State:        d.State,
		Progress:     d.Progress,
		SpeedBps:     d.SpeedBps,
		Size:         d.Size,
		Error:        d.Error,
		CreatedAt:    d.CreatedAt,
		UpdatedAt:    d.UpdatedAt,
		CompletedAt:  d.CompletedAt,
		ImportStep:   d.ImportStep,
		ImportTarget: d.ImportTarget,
		Outcome:      outcomeOf(d.State, upgraded),
	}
	if v.EpisodeIDs == nil {
		v.EpisodeIDs = []int64{}
	}
	if d.CopyID != 0 {
		id := d.CopyID
		v.CopyID = &id
	}
	if d.ETA >= 0 {
		eta := int64(d.ETA)
		v.ETASeconds = &eta
	}
	if tid.Valid {
		v.Title = &TitleRef{ID: tid.Int64, Name: tname.String, Year: int(tyear.Int64), Kind: tkind.String, PosterRef: tposter.String}
	}
	return v, nil
}

// outcomeOf says how a download ended; "" while it has not.
func outcomeOf(state string, upgraded bool) string {
	switch state {
	case StateImported:
		if upgraded {
			return OutcomeUpgraded
		}
		return OutcomeImported
	case StateFailed:
		return OutcomeFailed
	case StateRemoved:
		return OutcomeRemoved
	}
	return ""
}

func (s *Store) getView(ctx context.Context, id int64) (View, error) {
	v, err := scanView(s.db.QueryRowContext(ctx, viewSelect+` WHERE d.id = ?`, id))
	if errors.Is(err, sql.ErrNoRows) {
		return View{}, ErrNotFound
	}
	return v, err
}

func (s *Store) listViews(ctx context.Context, states []string, order string, limit, offset int) ([]View, int, error) {
	where, args := "", []any(nil)
	if len(states) > 0 {
		where = ` WHERE d.state IN (` + placeholders(len(states)) + `)`
		args = anyList(states)
	}
	var total int
	if err := s.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM downloads d`+where, args...).Scan(&total); err != nil {
		return nil, 0, err
	}
	rows, err := s.db.QueryContext(ctx, viewSelect+where+` ORDER BY `+order+` LIMIT ? OFFSET ?`,
		append(append([]any{}, args...), limit, offset)...)
	if err != nil {
		return nil, 0, err
	}
	defer rows.Close()
	out := []View{}
	for rows.Next() {
		v, err := scanView(rows)
		if err != nil {
			return nil, 0, err
		}
		out = append(out, v)
	}
	return out, total, rows.Err()
}

// stateCounts returns how many downloads are in each state.
func (s *Store) stateCounts(ctx context.Context) (map[string]int, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT state, COUNT(*) FROM downloads GROUP BY state`)
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
