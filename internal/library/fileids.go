package library

import "context"

// FileIDs looks up the ids of the file rows of one folder by relative path (with
// "/"), for a caller that has just imported them. A path with no row is left out
// of the answer.
func (s *Service) FileIDs(ctx context.Context, folderID int64, relPaths []string) (map[string]int64, error) {
	var rels []string
	for _, r := range relPaths {
		if rel, err := cleanRel(r); err == nil {
			rels = append(rels, rel)
		}
	}
	return s.store.fileIDsByPath(ctx, folderID, rels)
}

// fileIDsByPath returns the ids of a folder's file rows, keyed by relative path.
func (s *Store) fileIDsByPath(ctx context.Context, folderID int64, rels []string) (map[string]int64, error) {
	out := map[string]int64{}
	for len(rels) > 0 {
		n := min(len(rels), writeBatch)
		args := []any{folderID}
		for _, r := range rels[:n] {
			args = append(args, r)
		}
		rows, err := s.db.QueryContext(ctx,
			`SELECT rel_path, id FROM files WHERE folder_id = ? AND rel_path IN (`+placeholderList(n)+`)`, args...)
		if err != nil {
			return nil, err
		}
		for rows.Next() {
			var (
				rel string
				id  int64
			)
			if err := rows.Scan(&rel, &id); err != nil {
				rows.Close()
				return nil, err
			}
			out[rel] = id
		}
		if err := rows.Err(); err != nil {
			rows.Close()
			return nil, err
		}
		rows.Close()
		rels = rels[n:]
	}
	return out, nil
}
