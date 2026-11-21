package library

import (
	"context"
	"strings"
)

// The queries behind the file summaries (summary.go): what is on disk for a
// title, an episode or a track. They count matched files that are still there.

// fileGroup is the files of one thing that share a quality name.
type fileGroup struct {
	ID      int64
	Quality string
	Count   int
	Bytes   int64
}

const liveMatched = `f.match_state = 'matched' AND f.missing_since IS NULL`

// titleFileGroups groups the matched files of the titles by title and quality,
// in one query.
func (s *Store) titleFileGroups(ctx context.Context, ids []int64) ([]fileGroup, error) {
	var out []fileGroup
	for len(ids) > 0 {
		n := min(len(ids), writeBatch)
		args := make([]any, n)
		for i, id := range ids[:n] {
			args[i] = id
		}
		rows, err := s.db.QueryContext(ctx,
			`SELECT f.title_id, f.quality, COUNT(*), COALESCE(SUM(f.size), 0)
			 FROM files f
			 WHERE `+liveMatched+` AND f.title_id IN (`+strings.TrimSuffix(strings.Repeat("?,", n), ",")+`)
			 GROUP BY f.title_id, f.quality`, args...)
		if err != nil {
			return nil, err
		}
		for rows.Next() {
			var g fileGroup
			if err := rows.Scan(&g.ID, &g.Quality, &g.Count, &g.Bytes); err != nil {
				rows.Close()
				return nil, err
			}
			out = append(out, g)
		}
		if err := rows.Err(); err != nil {
			rows.Close()
			return nil, err
		}
		rows.Close()
		ids = ids[n:]
	}
	return out, nil
}

// episodeFileGroups groups the matched files of one show by episode and
// quality. A file that holds several episodes counts for each of them.
func (s *Store) episodeFileGroups(ctx context.Context, titleID int64) ([]fileGroup, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT j.value, f.quality, COUNT(*), COALESCE(SUM(f.size), 0)
		 FROM files f, json_each(f.episode_ids) j
		 WHERE `+liveMatched+` AND f.title_id = ? AND f.episode_ids IS NOT NULL
		 GROUP BY j.value, f.quality`, titleID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []fileGroup
	for rows.Next() {
		var g fileGroup
		if err := rows.Scan(&g.ID, &g.Quality, &g.Count, &g.Bytes); err != nil {
			return nil, err
		}
		out = append(out, g)
	}
	return out, rows.Err()
}

// trackFileGroups groups the matched files of one artist by track and quality.
func (s *Store) trackFileGroups(ctx context.Context, titleID int64) ([]fileGroup, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT f.track_id, f.quality, COUNT(*), COALESCE(SUM(f.size), 0)
		 FROM files f
		 WHERE `+liveMatched+` AND f.title_id = ? AND f.track_id IS NOT NULL
		 GROUP BY f.track_id, f.quality`, titleID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []fileGroup
	for rows.Next() {
		var g fileGroup
		if err := rows.Scan(&g.ID, &g.Quality, &g.Count, &g.Bytes); err != nil {
			return nil, err
		}
		out = append(out, g)
	}
	return out, rows.Err()
}
