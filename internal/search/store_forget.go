package search

import (
	"context"
	"encoding/json"
	"time"

	"github.com/BaileyPillon/marquee/internal/core/db"
)

// ForgetGrabs removes keys from the grabbed_for list of a copy's searches since
// the given time. A search left with nothing grabbed is marked as having grabbed
// nothing. It returns how many rows changed. Keys are compared in lower case.
func (s *Store) ForgetGrabs(ctx context.Context, copyID int64, since time.Time, keys map[string]bool) (int, error) {
	type row struct {
		id   int64
		keys []string
	}
	rows, err := s.db.QueryContext(ctx,
		`SELECT id, grabbed_for FROM search_history WHERE copy_id = ? AND grabbed > 0 AND at >= ?`, copyID, db.TS(since))
	if err != nil {
		return 0, err
	}
	var changed []row
	for rows.Next() {
		var (
			id   int64
			text string
		)
		if err := rows.Scan(&id, &text); err != nil {
			rows.Close()
			return 0, err
		}
		var list []string
		if json.Unmarshal([]byte(text), &list) != nil {
			continue
		}
		kept := make([]string, 0, len(list))
		for _, k := range list {
			if !keys[k] {
				kept = append(kept, k)
			}
		}
		if len(kept) != len(list) {
			changed = append(changed, row{id: id, keys: kept})
		}
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return 0, err
	}
	rows.Close()
	for _, r := range changed {
		text, _ := json.Marshal(r.keys)
		grabbed := 0
		if len(r.keys) > 0 {
			grabbed = len(r.keys)
		}
		if _, err := s.db.ExecContext(ctx,
			`UPDATE search_history SET grabbed_for = ?, grabbed = ? WHERE id = ?`, string(text), grabbed, r.id); err != nil {
			return 0, err
		}
	}
	return len(changed), nil
}
