package library

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
type Store struct {
	db *sql.DB
}

// NewStore wraps the main database.
func NewStore(d *sql.DB) *Store { return &Store{db: d} }

// writeBatch is how many rows go into one write transaction, so a big scan
// never holds the database lock for long.
const writeBatch = 500

type scanner interface{ Scan(dest ...any) error }

// ---- folders -----------------------------------------------------------------

const folderCols = `id, kind, path, label, created_at`

func scanFolder(s scanner) (Folder, error) {
	var (
		f       Folder
		created string
	)
	if err := s.Scan(&f.ID, &f.Kind, &f.Path, &f.Label, &created); err != nil {
		return Folder{}, err
	}
	t, err := db.ParseTS(created)
	if err != nil {
		return Folder{}, err
	}
	f.CreatedAt = t
	return f, nil
}

// InsertFolder adds a folder row. A path that is already there gives ErrDuplicate.
func (s *Store) InsertFolder(ctx context.Context, kind, path, label string, now time.Time) (Folder, error) {
	res, err := s.db.ExecContext(ctx,
		`INSERT INTO library_folders (kind, path, label, created_at) VALUES (?, ?, ?, ?)`,
		kind, path, label, db.TS(now))
	if err != nil {
		if db.IsUniqueViolation(err) {
			return Folder{}, ErrDuplicate
		}
		return Folder{}, err
	}
	id, err := res.LastInsertId()
	if err != nil {
		return Folder{}, err
	}
	return s.GetFolder(ctx, id)
}

// GetFolder returns one folder, or ErrNotFound.
func (s *Store) GetFolder(ctx context.Context, id int64) (Folder, error) {
	f, err := scanFolder(s.db.QueryRowContext(ctx, `SELECT `+folderCols+` FROM library_folders WHERE id = ?`, id))
	if errors.Is(err, sql.ErrNoRows) {
		return Folder{}, ErrNotFound
	}
	return f, err
}

// ListFolders returns every folder, oldest first.
func (s *Store) ListFolders(ctx context.Context) ([]Folder, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT `+folderCols+` FROM library_folders ORDER BY id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Folder
	for rows.Next() {
		f, err := scanFolder(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, f)
	}
	return out, rows.Err()
}

// DeleteFolder removes a folder row; its file rows go with it (ON DELETE
// CASCADE). Nothing on disk is touched.
func (s *Store) DeleteFolder(ctx context.Context, id int64) error {
	res, err := s.db.ExecContext(ctx, `DELETE FROM library_folders WHERE id = ?`, id)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrNotFound
	}
	return nil
}

// FolderCounts returns the per-folder file counts, keyed by folder id. A
// folder with no files has no entry (the zero value is right).
func (s *Store) FolderCounts(ctx context.Context) (map[int64]FolderCounts, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT folder_id,
		       COALESCE(SUM(missing_since IS NULL), 0),
		       COALESCE(SUM(CASE WHEN missing_since IS NULL THEN size END), 0),
		       COALESCE(SUM(missing_since IS NULL AND match_state = 'matched'), 0),
		       COALESCE(SUM(missing_since IS NULL AND match_state = 'unmatched'), 0),
		       COALESCE(SUM(missing_since IS NULL AND match_state = 'ignored'), 0),
		       COALESCE(SUM(missing_since IS NOT NULL), 0)
		FROM files GROUP BY folder_id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[int64]FolderCounts{}
	for rows.Next() {
		var (
			id int64
			c  FolderCounts
		)
		if err := rows.Scan(&id, &c.Files, &c.Bytes, &c.Matched, &c.Unmatched, &c.Ignored, &c.Missing); err != nil {
			return nil, err
		}
		out[id] = c
	}
	return out, rows.Err()
}

// ---- files -------------------------------------------------------------------

// indexRow is what a scan needs to know about a file it already has.
type indexRow struct {
	ID      int64
	Size    int64
	MTime   int64
	State   string
	Missing bool
}

// FileIndex loads every file row of a folder, keyed by relative path.
func (s *Store) FileIndex(ctx context.Context, folderID int64) (map[string]indexRow, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT id, rel_path, size, mtime, match_state, missing_since IS NOT NULL FROM files WHERE folder_id = ?`, folderID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[string]indexRow{}
	for rows.Next() {
		var (
			r   indexRow
			rel string
		)
		if err := rows.Scan(&r.ID, &rel, &r.Size, &r.MTime, &r.State, &r.Missing); err != nil {
			return nil, err
		}
		out[rel] = r
	}
	return out, rows.Err()
}

// fileWrite is one file row to insert or refresh.
type fileWrite struct {
	Rel        string
	Size       int64
	MTime      int64 // unix nanoseconds
	Kind       string
	Parsed     string // JSON
	Quality    string
	State      string
	TitleID    *int64
	EpisodeIDs []int64 // nil stores NULL
	TrackID    *int64
}

// UpsertFiles inserts new rows and refreshes existing ones (matched on folder
// and path), clearing missing_since, in batches of writeBatch per transaction.
func (s *Store) UpsertFiles(ctx context.Context, folderID int64, now time.Time, ws []fileWrite) error {
	for len(ws) > 0 {
		n := min(len(ws), writeBatch)
		if err := s.upsertBatch(ctx, folderID, now, ws[:n]); err != nil {
			return err
		}
		ws = ws[n:]
	}
	return nil
}

func (s *Store) upsertBatch(ctx context.Context, folderID int64, now time.Time, ws []fileWrite) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	stmt, err := tx.PrepareContext(ctx, `
		INSERT INTO files (folder_id, rel_path, size, mtime, kind, parsed, quality,
		                   title_id, episode_ids, track_id, match_state, added_at, missing_since)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, NULL)
		ON CONFLICT (folder_id, rel_path) DO UPDATE SET
		    size = excluded.size, mtime = excluded.mtime, kind = excluded.kind,
		    parsed = excluded.parsed, quality = excluded.quality,
		    title_id = excluded.title_id, episode_ids = excluded.episode_ids,
		    track_id = excluded.track_id, match_state = excluded.match_state,
		    missing_since = NULL`)
	if err != nil {
		return err
	}
	defer stmt.Close()
	added := db.TS(now)
	for _, w := range ws {
		var eps any
		if w.EpisodeIDs != nil {
			b, err := json.Marshal(w.EpisodeIDs)
			if err != nil {
				return err
			}
			eps = string(b)
		}
		if _, err := stmt.ExecContext(ctx, folderID, w.Rel, w.Size, w.MTime, w.Kind, w.Parsed, w.Quality,
			nullInt(w.TitleID), eps, nullInt(w.TrackID), w.State, added); err != nil {
			return err
		}
	}
	return tx.Commit()
}

func nullInt(p *int64) any {
	if p == nil {
		return nil
	}
	return *p
}

// MarkMissing stamps files that are no longer on disk.
func (s *Store) MarkMissing(ctx context.Context, ids []int64, now time.Time) error {
	return s.updateIDs(ctx, ids, `UPDATE files SET missing_since = ? WHERE missing_since IS NULL AND id IN (`, []any{db.TS(now)})
}

// ClearMissing clears the stamp on files that are back.
func (s *Store) ClearMissing(ctx context.Context, ids []int64) error {
	return s.updateIDs(ctx, ids, `UPDATE files SET missing_since = NULL WHERE id IN (`, nil)
}

func (s *Store) updateIDs(ctx context.Context, ids []int64, prefix string, lead []any) error {
	for len(ids) > 0 {
		n := min(len(ids), writeBatch)
		args := append([]any{}, lead...)
		for _, id := range ids[:n] {
			args = append(args, id)
		}
		q := prefix + strings.TrimSuffix(strings.Repeat("?,", n), ",") + `)`
		if _, err := s.db.ExecContext(ctx, q, args...); err != nil {
			return err
		}
		ids = ids[n:]
	}
	return nil
}

const fileCols = `id, folder_id, rel_path, size, mtime, kind, parsed, quality, title_id, episode_ids,
	track_id, match_state, added_at, missing_since`

func scanFile(s scanner) (File, error) {
	var (
		f       File
		mtime   int64
		parsed  string
		title   sql.NullInt64
		eps     sql.NullString
		track   sql.NullInt64
		added   string
		missing sql.NullString
	)
	if err := s.Scan(&f.ID, &f.FolderID, &f.RelPath, &f.Size, &mtime, &f.Kind, &parsed, &f.Quality,
		&title, &eps, &track, &f.State, &added, &missing); err != nil {
		return File{}, err
	}
	f.MTime = time.Unix(0, mtime).UTC()
	f.Parsed = json.RawMessage(parsed)
	if title.Valid {
		f.TitleID = &title.Int64
	}
	if track.Valid {
		f.TrackID = &track.Int64
	}
	if eps.Valid {
		if err := json.Unmarshal([]byte(eps.String), &f.EpisodeIDs); err != nil {
			return File{}, err
		}
	}
	t, err := db.ParseTS(added)
	if err != nil {
		return File{}, err
	}
	f.AddedAt = t
	if missing.Valid {
		mt, err := db.ParseTS(missing.String)
		if err != nil {
			return File{}, err
		}
		f.MissingSince = &mt
	}
	return f, nil
}

// ListFiles returns one page of file rows (ordered by folder and path) and the
// total number of rows matching the filter.
func (s *Store) ListFiles(ctx context.Context, f FileFilter) ([]File, int, error) {
	var (
		where []string
		args  []any
	)
	if f.FolderID != 0 {
		where = append(where, `folder_id = ?`)
		args = append(args, f.FolderID)
	}
	if f.TitleID != 0 {
		where = append(where, `title_id = ?`)
		args = append(args, f.TitleID)
	}
	switch f.State {
	case "":
	case "missing":
		where = append(where, `missing_since IS NOT NULL`)
	default:
		where = append(where, `match_state = ?`, `missing_since IS NULL`)
		args = append(args, f.State)
	}
	cond := ""
	if len(where) > 0 {
		cond = ` WHERE ` + strings.Join(where, ` AND `)
	}

	var total int
	if err := s.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM files`+cond, args...).Scan(&total); err != nil {
		return nil, 0, err
	}
	limit := f.Limit
	if limit <= 0 {
		limit = 100
	}
	rows, err := s.db.QueryContext(ctx,
		`SELECT `+fileCols+` FROM files`+cond+` ORDER BY folder_id, rel_path LIMIT ? OFFSET ?`,
		append(args, limit, max(f.Offset, 0))...)
	if err != nil {
		return nil, 0, err
	}
	defer rows.Close()
	out := []File{}
	for rows.Next() {
		file, err := scanFile(rows)
		if err != nil {
			return nil, 0, err
		}
		out = append(out, file)
	}
	return out, total, rows.Err()
}

// Stats counts files by kind and state (leaving out files that are missing
// from disk, which are counted on their own).
func (s *Store) Stats(ctx context.Context) (Stats, error) {
	st := Stats{
		States: map[string]int{StateMatched: 0, StateUnmatched: 0, StateIgnored: 0},
		Kinds:  map[string]KindStats{KindMovie: {}, KindShow: {}, KindMusic: {}},
	}
	rows, err := s.db.QueryContext(ctx, `
		SELECT kind, match_state, COUNT(*), COALESCE(SUM(size), 0)
		FROM files WHERE missing_since IS NULL GROUP BY kind, match_state`)
	if err != nil {
		return Stats{}, err
	}
	defer rows.Close()
	for rows.Next() {
		var (
			kind, state string
			n           int
			bytes       int64
		)
		if err := rows.Scan(&kind, &state, &n, &bytes); err != nil {
			return Stats{}, err
		}
		ks := st.Kinds[kind]
		ks.Files += n
		ks.Bytes += bytes
		switch state {
		case StateMatched:
			ks.Matched += n
		case StateUnmatched:
			ks.Unmatched += n
		case StateIgnored:
			ks.Ignored += n
		}
		st.Kinds[kind] = ks
		st.States[state] += n
		st.Files += n
		st.Bytes += bytes
	}
	if err := rows.Err(); err != nil {
		return Stats{}, err
	}
	rows.Close()

	mrows, err := s.db.QueryContext(ctx, `SELECT kind, COUNT(*) FROM files WHERE missing_since IS NOT NULL GROUP BY kind`)
	if err != nil {
		return Stats{}, err
	}
	defer mrows.Close()
	for mrows.Next() {
		var (
			kind string
			n    int
		)
		if err := mrows.Scan(&kind, &n); err != nil {
			return Stats{}, err
		}
		ks := st.Kinds[kind]
		ks.Missing = n
		st.Kinds[kind] = ks
		st.Missing += n
	}
	return st, mrows.Err()
}
