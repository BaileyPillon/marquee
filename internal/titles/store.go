package titles

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"strconv"
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

// dbx is what *sql.DB and *sql.Tx share.
type dbx interface {
	ExecContext(ctx context.Context, query string, args ...any) (sql.Result, error)
	QueryContext(ctx context.Context, query string, args ...any) (*sql.Rows, error)
	QueryRowContext(ctx context.Context, query string, args ...any) *sql.Row
}

// queries are the statements; they run on the database or inside a
// transaction, depending on x.
type queries struct{ x dbx }

func (s *Store) q() queries { return queries{x: s.db} }

// inTx runs fn in one write transaction.
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

const titleCols = `id, kind, tmdb_id, tvmaze_id, imdb_id, mbid, name, sort_name, alt_names, year, overview, tagline,
	certification, trailer_key, runtime_min, genres, poster_ref, backdrop_ref, status, monitored, root_folder,
	added_at, updated_at, refreshed_at, release_date, life_begin, life_end`

func scanTitle(s scanner) (Title, error) {
	var (
		t                         Title
		tmdb, tvmaze              sql.NullInt64
		imdb, mbid                sql.NullString
		alt, genres               string
		mon                       int
		added, updated, refreshed string
		lifeBegin, lifeEnd        string
	)
	if err := s.Scan(&t.ID, &t.Kind, &tmdb, &tvmaze, &imdb, &mbid, &t.Name, &t.SortName, &alt, &t.Year,
		&t.Overview, &t.Tagline, &t.Certification, &t.TrailerKey, &t.RuntimeMin, &genres, &t.PosterRef,
		&t.BackdropRef, &t.Status, &mon, &t.RootFolder, &added, &updated, &refreshed,
		&t.ReleaseDate, &lifeBegin, &lifeEnd); err != nil {
		return Title{}, err
	}
	t.TMDBID, t.TVmazeID, t.IMDbID, t.MBID = tmdb.Int64, tvmaze.Int64, imdb.String, mbid.String
	t.Monitored = mon != 0
	if t.Kind == KindArtist {
		t.LifeSpan = &LifeSpan{Begin: lifeBegin, End: lifeEnd}
	}
	if err := json.Unmarshal([]byte(alt), &t.AltNames); err != nil {
		return Title{}, err
	}
	if err := json.Unmarshal([]byte(genres), &t.Genres); err != nil {
		return Title{}, err
	}
	if t.AltNames == nil {
		t.AltNames = []string{}
	}
	if t.Genres == nil {
		t.Genres = []string{}
	}
	var err error
	if t.AddedAt, err = db.ParseTS(added); err != nil {
		return Title{}, err
	}
	if t.UpdatedAt, err = db.ParseTS(updated); err != nil {
		return Title{}, err
	}
	if t.RefreshedAt, err = db.ParseTS(refreshed); err != nil {
		return Title{}, err
	}
	return t, nil
}

func nullInt(n int64) any {
	if n == 0 {
		return nil
	}
	return n
}

func nullText(s string) any {
	if s == "" {
		return nil
	}
	return s
}

func jsonText(v []string) string {
	if v == nil {
		v = []string{}
	}
	b, _ := json.Marshal(v)
	return string(b)
}

func b2i(b bool) int {
	if b {
		return 1
	}
	return 0
}

func lifeBegin(d *Detail) string {
	if d.LifeSpan == nil {
		return ""
	}
	return d.LifeSpan.Begin
}

func lifeEnd(d *Detail) string {
	if d.LifeSpan == nil {
		return ""
	}
	return d.LifeSpan.End
}

// ---- identity --------------------------------------------------------------

// ids are a title's identities at the providers.
type ids struct {
	TMDB, TVmaze int64
	IMDb, MBID   string
}

func (d *Detail) ids() ids {
	return ids{TMDB: d.TMDBID, TVmaze: d.TVmazeID, IMDb: d.IMDbID, MBID: d.MBID}
}

// findExisting returns the title of kind that has any of the given ids.
func (q queries) findExisting(ctx context.Context, kind string, id ids) (int64, bool, error) {
	var found int64
	err := q.x.QueryRowContext(ctx,
		`SELECT id FROM titles WHERE kind = ? AND (
			(? <> 0  AND tmdb_id   = ?) OR
			(? <> 0  AND tvmaze_id = ?) OR
			(? <> '' AND imdb_id   = ?) OR
			(? <> '' AND mbid      = ?))
		 ORDER BY id LIMIT 1`,
		kind, id.TMDB, id.TMDB, id.TVmaze, id.TVmaze, id.IMDb, id.IMDb, id.MBID, id.MBID).Scan(&found)
	if errors.Is(err, sql.ErrNoRows) {
		return 0, false, nil
	}
	return found, err == nil, err
}

// inLibrary maps provider ids (as the provider writes them) to title ids, for
// the candidates of one search. provider is one of the fixed provider names.
func (q queries) inLibrary(ctx context.Context, kind, column string, ints []int64, texts []string) (map[string]int64, error) {
	out := map[string]int64{}
	var (
		query string
		args  []any
	)
	switch {
	case len(ints) > 0:
		query = `SELECT id, ` + column + ` FROM titles WHERE kind = ? AND ` + column + ` IN (` + placeholders(len(ints)) + `)`
		args = append(args, kind)
		for _, n := range ints {
			args = append(args, n)
		}
	case len(texts) > 0:
		query = `SELECT id, ` + column + ` FROM titles WHERE kind = ? AND ` + column + ` IN (` + placeholders(len(texts)) + `)`
		args = append(args, kind)
		for _, s := range texts {
			args = append(args, s)
		}
	default:
		return out, nil
	}
	rows, err := q.x.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var id int64
		var key any
		if err := rows.Scan(&id, &key); err != nil {
			return nil, err
		}
		switch k := key.(type) {
		case int64:
			out[strconv.FormatInt(k, 10)] = id
		case string:
			out[k] = id
		}
	}
	return out, rows.Err()
}

func placeholders(n int) string {
	if n <= 0 {
		return ""
	}
	return strings.TrimSuffix(strings.Repeat("?,", n), ",")
}

// ---- reads -----------------------------------------------------------------

func (q queries) title(ctx context.Context, id int64) (Title, error) {
	t, err := scanTitle(q.x.QueryRowContext(ctx, `SELECT `+titleCols+` FROM titles WHERE id = ?`, id))
	if errors.Is(err, sql.ErrNoRows) {
		return Title{}, ErrNotFound
	}
	return t, err
}

// detail loads a title and everything under it.
func (q queries) detail(ctx context.Context, id int64) (*Detail, error) {
	t, err := q.title(ctx, id)
	if err != nil {
		return nil, err
	}
	d := &Detail{Title: t, Seasons: []Season{}, Episodes: []Episode{}, Albums: []Album{}, People: []Person{}}

	rows, err := q.x.QueryContext(ctx,
		`SELECT s.number, s.monitored,
		        (SELECT COUNT(*) FROM episodes e WHERE e.title_id = s.title_id AND e.season = s.number)
		 FROM seasons s WHERE s.title_id = ? ORDER BY s.number`, id)
	if err != nil {
		return nil, err
	}
	for rows.Next() {
		var s Season
		var mon int
		if err := rows.Scan(&s.Number, &mon, &s.EpisodeCount); err != nil {
			rows.Close()
			return nil, err
		}
		s.Monitored = mon != 0
		d.Seasons = append(d.Seasons, s)
	}
	if err := closeRows(rows); err != nil {
		return nil, err
	}

	rows, err = q.x.QueryContext(ctx,
		`SELECT id, season, number, name, air_date, overview, runtime_min, monitored
		 FROM episodes WHERE title_id = ? ORDER BY season, number`, id)
	if err != nil {
		return nil, err
	}
	for rows.Next() {
		var e Episode
		var mon int
		if err := rows.Scan(&e.ID, &e.Season, &e.Number, &e.Name, &e.AirDate, &e.Overview, &e.RuntimeMin, &mon); err != nil {
			rows.Close()
			return nil, err
		}
		e.Monitored = mon != 0
		d.Episodes = append(d.Episodes, e)
	}
	if err := closeRows(rows); err != nil {
		return nil, err
	}

	rows, err = q.x.QueryContext(ctx,
		`SELECT id, mbid, name, year, type, studio, monitored, cover_ref FROM albums WHERE title_id = ? ORDER BY year, name, id`, id)
	if err != nil {
		return nil, err
	}
	byID := map[int64]int{}
	for rows.Next() {
		var a Album
		var mon, studio int
		if err := rows.Scan(&a.ID, &a.MBID, &a.Name, &a.Year, &a.Type, &studio, &mon, &a.CoverRef); err != nil {
			rows.Close()
			return nil, err
		}
		a.Studio, a.Monitored, a.Tracks = studio != 0, mon != 0, []Track{}
		byID[a.ID] = len(d.Albums)
		d.Albums = append(d.Albums, a)
	}
	if err := closeRows(rows); err != nil {
		return nil, err
	}
	if len(d.Albums) > 0 {
		rows, err = q.x.QueryContext(ctx,
			`SELECT t.id, t.album_id, t.disc, t.position, t.name, t.length_ms, t.mbid
			 FROM tracks t JOIN albums a ON a.id = t.album_id
			 WHERE a.title_id = ? ORDER BY t.album_id, t.disc, t.position`, id)
		if err != nil {
			return nil, err
		}
		for rows.Next() {
			var t Track
			var albumID int64
			if err := rows.Scan(&t.ID, &albumID, &t.Disc, &t.Position, &t.Name, &t.LengthMS, &t.MBID); err != nil {
				rows.Close()
				return nil, err
			}
			if i, ok := byID[albumID]; ok {
				d.Albums[i].Tracks = append(d.Albums[i].Tracks, t)
			}
		}
		if err := closeRows(rows); err != nil {
			return nil, err
		}
	}

	rows, err = q.x.QueryContext(ctx,
		`SELECT name, role, job, image_ref FROM people WHERE title_id = ? ORDER BY ord`, id)
	if err != nil {
		return nil, err
	}
	for rows.Next() {
		var p Person
		if err := rows.Scan(&p.Name, &p.Role, &p.Job, &p.ImageRef); err != nil {
			rows.Close()
			return nil, err
		}
		d.People = append(d.People, p)
	}
	if err := closeRows(rows); err != nil {
		return nil, err
	}
	return d, nil
}

func closeRows(rows *sql.Rows) error {
	if err := rows.Err(); err != nil {
		rows.Close()
		return err
	}
	return rows.Close()
}

// list returns one page of titles and the total that match.
func (q queries) list(ctx context.Context, f ListFilter) ([]Title, int, error) {
	var (
		where []string
		args  []any
	)
	if f.Kind != "" {
		where = append(where, `kind = ?`)
		args = append(args, f.Kind)
	}
	if f.Monitored != nil {
		where = append(where, `monitored = ?`)
		args = append(args, b2i(*f.Monitored))
	}
	if f.Q != "" {
		like := "%" + escapeLike(strings.ToLower(f.Q)) + "%"
		where = append(where, `(LOWER(name) LIKE ? ESCAPE '\' OR LOWER(alt_names) LIKE ? ESCAPE '\')`)
		args = append(args, like, like)
	}
	cond := ""
	if len(where) > 0 {
		cond = ` WHERE ` + strings.Join(where, ` AND `)
	}

	var total int
	if err := q.x.QueryRowContext(ctx, `SELECT COUNT(*) FROM titles`+cond, args...).Scan(&total); err != nil {
		return nil, 0, err
	}

	order := `added_at DESC, id DESC`
	switch f.Sort {
	case SortName:
		order = `sort_name, id`
	case SortYear:
		order = `year DESC, sort_name, id`
	}
	rows, err := q.x.QueryContext(ctx,
		`SELECT `+titleCols+` FROM titles`+cond+` ORDER BY `+order+` LIMIT ? OFFSET ?`,
		append(args, f.Limit, f.Offset)...)
	if err != nil {
		return nil, 0, err
	}
	defer rows.Close()
	out := []Title{}
	for rows.Next() {
		t, err := scanTitle(rows)
		if err != nil {
			return nil, 0, err
		}
		out = append(out, t)
	}
	return out, total, rows.Err()
}

func escapeLike(s string) string {
	return strings.NewReplacer(`\`, `\\`, `%`, `\%`, `_`, `\_`).Replace(s)
}

// candidates lists titles for the library matcher; kind "" means all kinds.
func (q queries) candidates(ctx context.Context, kind string) ([]Candidate, error) {
	query := `SELECT id, kind, name, year, alt_names FROM titles`
	var args []any
	if kind != "" {
		query += ` WHERE kind = ?`
		args = append(args, kind)
	}
	rows, err := q.x.QueryContext(ctx, query+` ORDER BY id`, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Candidate{}
	for rows.Next() {
		var c Candidate
		var alt string
		if err := rows.Scan(&c.ID, &c.Kind, &c.Name, &c.Year, &alt); err != nil {
			return nil, err
		}
		if err := json.Unmarshal([]byte(alt), &c.AltNames); err != nil {
			return nil, err
		}
		out = append(out, c)
	}
	return out, rows.Err()
}

func (q queries) episodeID(ctx context.Context, titleID int64, season, number int) (int64, bool, error) {
	var id int64
	err := q.x.QueryRowContext(ctx,
		`SELECT id FROM episodes WHERE title_id = ? AND season = ? AND number = ?`, titleID, season, number).Scan(&id)
	if errors.Is(err, sql.ErrNoRows) {
		return 0, false, nil
	}
	return id, err == nil, err
}

// monitoredIDs lists the monitored titles, least recently refreshed first.
func (q queries) monitoredIDs(ctx context.Context) ([]int64, error) {
	rows, err := q.x.QueryContext(ctx, `SELECT id FROM titles WHERE monitored = 1 ORDER BY refreshed_at, id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []int64
	for rows.Next() {
		var id int64
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		out = append(out, id)
	}
	return out, rows.Err()
}

// ---- writes ----------------------------------------------------------------

// insert saves a new title with everything under it and returns its id.
func (q queries) insert(ctx context.Context, d *Detail, now time.Time) (int64, error) {
	ts := db.TS(now)
	res, err := q.x.ExecContext(ctx,
		`INSERT INTO titles (kind, tmdb_id, tvmaze_id, imdb_id, mbid, name, sort_name, alt_names, year, overview,
			tagline, certification, trailer_key, runtime_min, genres, poster_ref, backdrop_ref, status, monitored,
			root_folder, added_at, updated_at, refreshed_at, release_date, life_begin, life_end)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		d.Kind, nullInt(d.TMDBID), nullInt(d.TVmazeID), nullText(d.IMDbID), nullText(d.MBID),
		d.Name, d.SortName, jsonText(d.AltNames), d.Year, d.Overview, d.Tagline, d.Certification, d.TrailerKey,
		d.RuntimeMin, jsonText(d.Genres), d.PosterRef, d.BackdropRef, d.Status, b2i(d.Monitored),
		d.RootFolder, ts, ts, ts, d.ReleaseDate, lifeBegin(d), lifeEnd(d))
	if err != nil {
		return 0, err
	}
	id, err := res.LastInsertId()
	if err != nil {
		return 0, err
	}
	for _, s := range d.Seasons {
		if _, err := q.x.ExecContext(ctx,
			`INSERT INTO seasons (title_id, number, monitored) VALUES (?, ?, ?)`, id, s.Number, b2i(s.Monitored)); err != nil {
			return 0, err
		}
	}
	for _, e := range d.Episodes {
		if _, err := q.x.ExecContext(ctx,
			`INSERT INTO episodes (title_id, season, number, name, air_date, overview, runtime_min, monitored)
			 VALUES (?, ?, ?, ?, ?, ?, ?, ?)`,
			id, e.Season, e.Number, e.Name, e.AirDate, e.Overview, e.RuntimeMin, b2i(e.Monitored)); err != nil {
			return 0, err
		}
	}
	for _, a := range d.Albums {
		albumID, err := q.upsertAlbum(ctx, id, a)
		if err != nil {
			return 0, err
		}
		if err := q.syncTracks(ctx, albumID, a.Tracks); err != nil {
			return 0, err
		}
	}
	return id, q.replacePeople(ctx, id, d.People)
}

// refresh updates a title from fresh provider data. Rows the user can change
// (monitored flags, root folder) are kept; episodes and albums are added or
// updated and never deleted, because files may hang off them. Tracks and
// people follow the provider, but an empty answer is taken as "unknown", not
// as "none".
func (q queries) refresh(ctx context.Context, id int64, d *Detail, now time.Time) error {
	ts := db.TS(now)
	if _, err := q.x.ExecContext(ctx,
		`UPDATE titles SET name = ?, sort_name = ?, alt_names = ?, year = ?, overview = ?, tagline = ?,
			certification = ?, trailer_key = ?, runtime_min = ?, genres = ?, poster_ref = ?, backdrop_ref = ?,
			status = ?, updated_at = ?, refreshed_at = ?,
			release_date = CASE WHEN ? <> '' THEN ? ELSE release_date END,
			life_begin = CASE WHEN ? <> '' THEN ? ELSE life_begin END,
			life_end = ?
		 WHERE id = ?`,
		d.Name, d.SortName, jsonText(d.AltNames), d.Year, d.Overview, d.Tagline, d.Certification, d.TrailerKey,
		d.RuntimeMin, jsonText(d.Genres), d.PosterRef, d.BackdropRef, d.Status, ts, ts,
		d.ReleaseDate, d.ReleaseDate, lifeBegin(d), lifeBegin(d), lifeEnd(d), id); err != nil {
		return err
	}
	// Fill an IMDb id we did not have, unless another title already holds it.
	if d.IMDbID != "" {
		if _, err := q.x.ExecContext(ctx,
			`UPDATE titles SET imdb_id = ? WHERE id = ? AND imdb_id IS NULL
			   AND NOT EXISTS (SELECT 1 FROM titles o WHERE o.kind = titles.kind AND o.imdb_id = ?)`,
			d.IMDbID, id, d.IMDbID); err != nil {
			return err
		}
	}

	for _, s := range d.Seasons {
		if _, err := q.x.ExecContext(ctx,
			`INSERT OR IGNORE INTO seasons (title_id, number, monitored) VALUES (?, ?, ?)`,
			id, s.Number, b2i(s.Monitored)); err != nil {
			return err
		}
	}
	for _, e := range d.Episodes {
		// A new episode follows its season's flag; an existing one keeps the
		// flag the user set.
		if _, err := q.x.ExecContext(ctx,
			`INSERT INTO episodes (title_id, season, number, name, air_date, overview, runtime_min, monitored)
			 VALUES (?, ?, ?, ?, ?, ?, ?, COALESCE((SELECT monitored FROM seasons WHERE title_id = ? AND number = ?), ?))
			 ON CONFLICT (title_id, season, number) DO UPDATE SET
			   name = excluded.name, air_date = excluded.air_date, overview = excluded.overview,
			   runtime_min = excluded.runtime_min`,
			id, e.Season, e.Number, e.Name, e.AirDate, e.Overview, e.RuntimeMin,
			id, e.Season, b2i(e.Monitored)); err != nil {
			return err
		}
	}
	for _, a := range d.Albums {
		albumID, err := q.upsertAlbum(ctx, id, a)
		if err != nil {
			return err
		}
		if len(a.Tracks) > 0 {
			if err := q.syncTracks(ctx, albumID, a.Tracks); err != nil {
				return err
			}
		}
	}
	if len(d.People) > 0 {
		return q.replacePeople(ctx, id, d.People)
	}
	return nil
}

// upsertAlbum inserts an album or updates its descriptive fields; monitored is
// only set when the album is new.
func (q queries) upsertAlbum(ctx context.Context, titleID int64, a Album) (int64, error) {
	if _, err := q.x.ExecContext(ctx,
		`INSERT INTO albums (title_id, mbid, name, year, type, studio, monitored, cover_ref) VALUES (?, ?, ?, ?, ?, ?, ?, ?)
		 ON CONFLICT (mbid) DO UPDATE SET name = excluded.name, year = excluded.year, type = excluded.type,
		   studio = excluded.studio, cover_ref = excluded.cover_ref`,
		titleID, a.MBID, a.Name, a.Year, a.Type, b2i(a.Studio), b2i(a.Monitored), a.CoverRef); err != nil {
		return 0, err
	}
	var id int64
	err := q.x.QueryRowContext(ctx, `SELECT id FROM albums WHERE mbid = ?`, a.MBID).Scan(&id)
	return id, err
}

// syncTracks makes an album's tracks match the list: upsert by position, then
// delete the ones that are no longer listed.
func (q queries) syncTracks(ctx context.Context, albumID int64, tracks []Track) error {
	keep := make([]any, 0, len(tracks)*2+1)
	keep = append(keep, albumID)
	for _, t := range tracks {
		if _, err := q.x.ExecContext(ctx,
			`INSERT INTO tracks (album_id, disc, position, name, length_ms, mbid) VALUES (?, ?, ?, ?, ?, ?)
			 ON CONFLICT (album_id, disc, position) DO UPDATE SET
			   name = excluded.name, length_ms = excluded.length_ms, mbid = excluded.mbid`,
			albumID, t.Disc, t.Position, t.Name, t.LengthMS, t.MBID); err != nil {
			return err
		}
		keep = append(keep, t.Disc, t.Position)
	}
	if len(tracks) == 0 {
		return nil
	}
	cond := strings.TrimSuffix(strings.Repeat("(disc = ? AND position = ?) OR ", len(tracks)), " OR ")
	_, err := q.x.ExecContext(ctx, `DELETE FROM tracks WHERE album_id = ? AND NOT (`+cond+`)`, keep...)
	return err
}

func (q queries) replacePeople(ctx context.Context, titleID int64, people []Person) error {
	if _, err := q.x.ExecContext(ctx, `DELETE FROM people WHERE title_id = ?`, titleID); err != nil {
		return err
	}
	for i, p := range people {
		if _, err := q.x.ExecContext(ctx,
			`INSERT INTO people (title_id, name, role, job, ord, image_ref) VALUES (?, ?, ?, ?, ?, ?)`,
			titleID, p.Name, p.Role, p.Job, i, p.ImageRef); err != nil {
			return err
		}
	}
	return nil
}

// setMonitored sets the flag on a title and on everything under it. Turning
// monitoring on leaves specials (season 0) off.
func (q queries) setMonitored(ctx context.Context, id int64, on bool) error {
	res, err := q.x.ExecContext(ctx, `UPDATE titles SET monitored = ? WHERE id = ?`, b2i(on), id)
	if err != nil {
		return err
	}
	if n, err := res.RowsAffected(); err != nil {
		return err
	} else if n == 0 {
		return ErrNotFound
	}
	if _, err := q.x.ExecContext(ctx,
		`UPDATE seasons SET monitored = ? WHERE title_id = ? AND (? = 0 OR number > 0)`, b2i(on), id, b2i(on)); err != nil {
		return err
	}
	if _, err := q.x.ExecContext(ctx,
		`UPDATE episodes SET monitored = ? WHERE title_id = ? AND (? = 0 OR season > 0)`, b2i(on), id, b2i(on)); err != nil {
		return err
	}
	// Turning an artist on wants the studio albums; EPs, soundtracks and the rest
	// stay as they are (MonitorAlbums turns those on by name). Off turns every
	// album off.
	_, err = q.x.ExecContext(ctx,
		`UPDATE albums SET monitored = ? WHERE title_id = ? AND (? = 0 OR studio = 1)`, b2i(on), id, b2i(on))
	return err
}

// monitorTitle turns the flag on for the title row itself.
func (q queries) monitorTitle(ctx context.Context, id int64) error {
	res, err := q.x.ExecContext(ctx, `UPDATE titles SET monitored = 1 WHERE id = ?`, id)
	if err != nil {
		return err
	}
	if n, err := res.RowsAffected(); err != nil {
		return err
	} else if n == 0 {
		return ErrNotFound
	}
	return nil
}

// monitorSeasons turns monitoring on for a title and for some of its seasons
// (and their episodes). The other seasons are left as they are.
func (q queries) monitorSeasons(ctx context.Context, id int64, seasons []int) error {
	if err := q.monitorTitle(ctx, id); err != nil {
		return err
	}
	if len(seasons) == 0 {
		return nil
	}
	args := []any{id}
	for _, n := range seasons {
		args = append(args, n)
	}
	in := placeholders(len(seasons))
	if _, err := q.x.ExecContext(ctx,
		`UPDATE seasons SET monitored = 1 WHERE title_id = ? AND number IN (`+in+`)`, args...); err != nil {
		return err
	}
	_, err := q.x.ExecContext(ctx,
		`UPDATE episodes SET monitored = 1 WHERE title_id = ? AND season IN (`+in+`)`, args...)
	return err
}

// monitorAlbums turns monitoring on for a title and for some of its albums.
func (q queries) monitorAlbums(ctx context.Context, id int64, albums []int64) error {
	if err := q.monitorTitle(ctx, id); err != nil {
		return err
	}
	if len(albums) == 0 {
		return nil
	}
	args := []any{id}
	for _, n := range albums {
		args = append(args, n)
	}
	_, err := q.x.ExecContext(ctx,
		`UPDATE albums SET monitored = 1 WHERE title_id = ? AND id IN (`+placeholders(len(albums))+`)`, args...)
	return err
}

// delete removes a title; everything under it goes with it (foreign keys).
func (q queries) delete(ctx context.Context, id int64) error {
	res, err := q.x.ExecContext(ctx, `DELETE FROM titles WHERE id = ?`, id)
	if err != nil {
		return err
	}
	if n, err := res.RowsAffected(); err != nil {
		return err
	} else if n == 0 {
		return ErrNotFound
	}
	return nil
}
