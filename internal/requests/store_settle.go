package requests

import "context"

// settleTargets lists the requests behind a copy that an import can move on:
// the ones for the same title and quality that are approved, partial, available
// or failed.
func (q queries) settleTargets(ctx context.Context, titleID int64, quality string) ([]int64, error) {
	rows, err := q.x.QueryContext(ctx,
		`SELECT id FROM requests
		  WHERE title_id = ? AND quality = ? AND state IN ('approved', 'partial', 'available', 'failed')
		  ORDER BY id`, titleID, quality)
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
