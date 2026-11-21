package library

import "context"

// DeleteFiles removes file rows of one folder by relative path.
func (s *Store) DeleteFiles(ctx context.Context, folderID int64, rels []string) error {
	for len(rels) > 0 {
		n := min(len(rels), writeBatch)
		args := []any{folderID}
		for _, r := range rels[:n] {
			args = append(args, r)
		}
		q := `DELETE FROM files WHERE folder_id = ? AND rel_path IN (` + placeholderList(n) + `)`
		if _, err := s.db.ExecContext(ctx, q, args...); err != nil {
			return err
		}
		rels = rels[n:]
	}
	return nil
}

func placeholderList(n int) string {
	b := make([]byte, 0, 2*n)
	for i := 0; i < n; i++ {
		if i > 0 {
			b = append(b, ',')
		}
		b = append(b, '?')
	}
	return string(b)
}
