package mcp

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"strconv"
	"strings"
)

// Store is the only place in this package that talks SQL. It only reads, and
// only small facts that no other module offers as a method: the next episodes
// to air, the files behind one title, and whether an ask is already open. (The
// downloads module reads the titles table the same way for its list.)
type Store struct {
	db *sql.DB
}

// NewStore wraps the main database.
func NewStore(d *sql.DB) *Store { return &Store{db: d} }

// Ping reports whether the main database answers.
func (s *Store) Ping(ctx context.Context) error { return s.db.PingContext(ctx) }

// Airing is one episode that has not aired yet (or airs today).
type Airing struct {
	TitleID int64
	Show    string
	Season  int
	Number  int
	Name    string
	AirDate string // YYYY-MM-DD
}

// Upcoming lists the episodes of monitored shows that air from "from" to "to"
// (both YYYY-MM-DD, inclusive), soonest first. An episode counts only while it
// is monitored itself, so a show asked for by season does not list the seasons
// nobody asked for.
func (s *Store) Upcoming(ctx context.Context, from, to string) ([]Airing, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT t.id, t.name, e.season, e.number, e.name, e.air_date
		FROM episodes e
		JOIN titles t ON t.id = e.title_id
		WHERE t.kind = 'show' AND t.monitored = 1 AND e.monitored = 1
		  AND e.air_date <> '' AND e.air_date >= ? AND e.air_date <= ?
		ORDER BY e.air_date, t.sort_name, e.season, e.number
		LIMIT 500`, from, to)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Airing
	for rows.Next() {
		var a Airing
		if err := rows.Scan(&a.TitleID, &a.Show, &a.Season, &a.Number, &a.Name, &a.AirDate); err != nil {
			return nil, err
		}
		out = append(out, a)
	}
	return out, rows.Err()
}

// FileRow is one file of a title that is on disk.
type FileRow struct {
	RelPath    string
	Size       int64
	Quality    string // the label the scanner read, such as "Bluray-1080p"
	EpisodeIDs []int64
}

// TitleFiles lists the matched files of a title that are still on disk.
func (s *Store) TitleFiles(ctx context.Context, titleID int64) ([]FileRow, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT rel_path, size, quality, episode_ids
		FROM files
		WHERE title_id = ? AND match_state = 'matched' AND missing_since IS NULL
		ORDER BY rel_path`, titleID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []FileRow
	for rows.Next() {
		var (
			f   FileRow
			eps sql.NullString
		)
		if err := rows.Scan(&f.RelPath, &f.Size, &f.Quality, &eps); err != nil {
			return nil, err
		}
		if eps.Valid {
			if err := json.Unmarshal([]byte(eps.String), &f.EpisodeIDs); err != nil {
				return nil, err
			}
		}
		out = append(out, f)
	}
	return out, rows.Err()
}

// OnDisk says which of the titles have at least one matched file on disk.
func (s *Store) OnDisk(ctx context.Context, ids []int64) (map[int64]bool, error) {
	out := map[int64]bool{}
	if len(ids) == 0 {
		return out, nil
	}
	args := make([]any, len(ids))
	for i, id := range ids {
		args[i] = id
	}
	rows, err := s.db.QueryContext(ctx,
		`SELECT DISTINCT title_id FROM files
		 WHERE match_state = 'matched' AND missing_since IS NULL
		   AND title_id IN (`+strings.TrimSuffix(strings.Repeat("?,", len(ids)), ",")+`)`, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var id int64
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		out[id] = true
	}
	return out, rows.Err()
}

// FindTitle finds a catalog title by the id its provider gives it (TMDB for a
// movie, TVmaze for a show, MusicBrainz for an artist).
func (s *Store) FindTitle(ctx context.Context, kind, providerID string) (int64, bool, error) {
	providerID = strings.TrimSpace(providerID)
	var (
		query string
		arg   any
	)
	switch kind {
	case "movie", "show":
		n, err := strconv.ParseInt(providerID, 10, 64)
		if err != nil || n <= 0 {
			return 0, false, nil
		}
		arg = n
		query = `SELECT id FROM titles WHERE kind = 'movie' AND tmdb_id = ? ORDER BY id LIMIT 1`
		if kind == "show" {
			query = `SELECT id FROM titles WHERE kind = 'show' AND tvmaze_id = ? ORDER BY id LIMIT 1`
		}
	case "artist":
		arg = strings.ToLower(providerID)
		query = `SELECT id FROM titles WHERE kind = 'artist' AND mbid = ? ORDER BY id LIMIT 1`
	default:
		return 0, false, nil
	}
	var id int64
	err := s.db.QueryRowContext(ctx, query, arg).Scan(&id)
	if errors.Is(err, sql.ErrNoRows) {
		return 0, false, nil
	}
	return id, err == nil, err
}

// OpenAsk is an ask for a title at a quality that is still open.
type OpenAsk struct {
	ID     int64
	UserID int64
	State  string
}

// OpenAsk finds the open (pending, approved or available) ask for a title at a
// quality, if there is one. Only one can exist.
func (s *Store) OpenAsk(ctx context.Context, titleID int64, quality string) (*OpenAsk, error) {
	var a OpenAsk
	err := s.db.QueryRowContext(ctx,
		`SELECT id, user_id, state FROM requests
		 WHERE title_id = ? AND quality = ? AND state IN ('pending', 'approved', 'available')
		 ORDER BY id LIMIT 1`, titleID, quality).Scan(&a.ID, &a.UserID, &a.State)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &a, nil
}
