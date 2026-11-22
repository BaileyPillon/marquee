package library

import (
	"context"
	"database/sql"
	"errors"

	"github.com/BaileyPillon/marquee/internal/core/db"
)

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

// GetFile returns one file row, or ErrNotFound.
func (s *Store) GetFile(ctx context.Context, id int64) (File, error) {
	f, err := scanFile(s.db.QueryRowContext(ctx, `SELECT `+fileCols+` FROM files WHERE id = ?`, id))
	if errors.Is(err, sql.ErrNoRows) {
		return File{}, ErrNotFound
	}
	return f, err
}

// RenameFile gives a file row a new path inside its folder, keeping everything
// else the scan or the importer knew about it. A path another row already has
// gives ErrNameInUse.
func (s *Store) RenameFile(ctx context.Context, id int64, newRel string, mtime int64) error {
	res, err := s.db.ExecContext(ctx, `UPDATE files SET rel_path = ?, mtime = ?, missing_since = NULL WHERE id = ?`, newRel, mtime, id)
	if err != nil {
		if db.IsUniqueViolation(err) {
			return ErrNameInUse
		}
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrNotFound
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
