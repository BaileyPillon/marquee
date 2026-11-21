package calendar

import (
	"context"
	"database/sql"
	"encoding/json"
	"strings"
)

// Store is the only place in this package that talks SQL. It only reads, and it
// reads tables other modules own (titles, episodes, files, downloads,
// requests), for display.
type Store struct {
	db *sql.DB
}

// NewStore wraps the main database.
func NewStore(d *sql.DB) *Store { return &Store{db: d} }

// batch is how many ids go into one IN (...) list.
const batch = 500

func placeholders(n int) string { return strings.TrimSuffix(strings.Repeat("?,", n), ",") }

// forIDs runs fn over the ids in batches.
func forIDs(ids []int64, fn func(args []any, in string) error) error {
	for len(ids) > 0 {
		n := min(len(ids), batch)
		args := make([]any, n)
		for i, id := range ids[:n] {
			args[i] = id
		}
		if err := fn(args, placeholders(n)); err != nil {
			return err
		}
		ids = ids[n:]
	}
	return nil
}

type episodeRow struct {
	ID        int64
	TitleID   int64
	TitleName string
	Season    int
	Number    int
	Name      string
	AirDate   string
}

// episodes lists the episodes of monitored shows that air from one day to
// another (YYYY-MM-DD, both included). The episode itself must be monitored,
// which leaves out the specials and the seasons nobody asked for.
func (s *Store) episodes(ctx context.Context, from, to string) ([]episodeRow, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT e.id, t.id, t.name, e.season, e.number, e.name, e.air_date
		 FROM episodes e JOIN titles t ON t.id = e.title_id
		 WHERE t.kind = 'show' AND t.monitored = 1 AND e.monitored = 1 AND e.air_date >= ? AND e.air_date <= ?
		 ORDER BY e.air_date, t.sort_name, e.season, e.number`, from, to)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []episodeRow
	for rows.Next() {
		var r episodeRow
		if err := rows.Scan(&r.ID, &r.TitleID, &r.TitleName, &r.Season, &r.Number, &r.Name, &r.AirDate); err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

type movieRow struct {
	TitleID     int64
	Name        string
	ReleaseDate string
}

// movies lists the monitored movies released from one day to another. A release
// date that is only a year cannot be put on a day and is left out.
func (s *Store) movies(ctx context.Context, from, to string) ([]movieRow, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT id, name, release_date FROM titles
		 WHERE kind = 'movie' AND monitored = 1 AND length(release_date) = 10 AND release_date >= ? AND release_date <= ?
		 ORDER BY release_date, sort_name`, from, to)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []movieRow
	for rows.Next() {
		var r movieRow
		if err := rows.Scan(&r.TitleID, &r.Name, &r.ReleaseDate); err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

// liveFile is the library's idea of a file that counts: matched and still on disk.
const liveFile = `f.match_state = 'matched' AND f.missing_since IS NULL`

// episodeFiles says which episodes of the titles have a file. A file that holds
// several episodes counts for each.
func (s *Store) episodeFiles(ctx context.Context, titleIDs []int64) (map[fileKey]bool, error) {
	out := map[fileKey]bool{}
	err := forIDs(titleIDs, func(args []any, in string) error {
		rows, err := s.db.QueryContext(ctx,
			`SELECT DISTINCT f.title_id, j.value FROM files f, json_each(f.episode_ids) j
			 WHERE `+liveFile+` AND f.episode_ids IS NOT NULL AND f.title_id IN (`+in+`)`, args...)
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			var title, episode int64
			if err := rows.Scan(&title, &episode); err != nil {
				return err
			}
			out[fileKey{title, episode}] = true
		}
		return rows.Err()
	})
	return out, err
}

// movieFiles says which of the titles have a file.
func (s *Store) movieFiles(ctx context.Context, titleIDs []int64) (map[int64]bool, error) {
	out := map[int64]bool{}
	err := forIDs(titleIDs, func(args []any, in string) error {
		rows, err := s.db.QueryContext(ctx,
			`SELECT DISTINCT f.title_id FROM files f WHERE `+liveFile+` AND f.title_id IN (`+in+`)`, args...)
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			var id int64
			if err := rows.Scan(&id); err != nil {
				return err
			}
			out[id] = true
		}
		return rows.Err()
	})
	return out, err
}

// openStates are the downloads that are not over: still in the client, or
// finished there and waiting for the importer.
const openStates = `'queued', 'downloading', 'paused', 'stalled', 'completed', 'importing'`

// openDownloads lists the open downloads of the titles.
func (s *Store) openDownloads(ctx context.Context, titleIDs []int64) (openDownloads, error) {
	var out openDownloads
	err := forIDs(titleIDs, func(args []any, in string) error {
		rows, err := s.db.QueryContext(ctx,
			`SELECT title_id, kind, episode_ids, progress FROM downloads
			 WHERE state IN (`+openStates+`) AND title_id IN (`+in+`)`, args...)
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			var (
				d   download
				eps string
			)
			if err := rows.Scan(&d.TitleID, &d.Kind, &eps, &d.Progress); err != nil {
				return err
			}
			if err := json.Unmarshal([]byte(eps), &d.EpisodeIDs); err != nil {
				d.EpisodeIDs = nil
			}
			out = append(out, d)
		}
		return rows.Err()
	})
	return out, err
}

// requestedBy lists the titles a person has an ask for that was not declined or
// cancelled.
func (s *Store) requestedBy(ctx context.Context, userID int64) (map[int64]bool, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT DISTINCT title_id FROM requests WHERE user_id = ? AND state NOT IN ('declined', 'cancelled')`, userID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[int64]bool{}
	for rows.Next() {
		var id int64
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		out[id] = true
	}
	return out, rows.Err()
}
