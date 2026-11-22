package importer

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"time"
)

// partialSuffix marks a file that is still being written. The library scanner
// and the media servers do not read it (it is not a media extension), and the
// final name only appears once the whole file is there.
const partialSuffix = ".marquee-partial"

// How a file reached its place.
const (
	ModeHardlink = "hardlink" // a second name for the same data; the download keeps seeding
	ModeCopy     = "copy"
	ModeMove     = "move"
)

// Method is what the importer may do with a source file.
type Method int

const (
	// MethodLink keeps the source where it is, as a torrent client needs: a
	// hardlink when the destination is on the same volume, a copy when not.
	// The source is never moved.
	MethodLink Method = iota
	// MethodMove takes the source away: a rename when possible, otherwise a
	// copy and then a delete. It is for Usenet downloads and for files the
	// importer unpacked itself.
	MethodMove
)

// linkFile makes a hardlink. Tests replace it to stand in for a destination on
// another volume, where the link fails.
var linkFile = os.Link

// transfer puts the source file at dest and returns how it got there. The
// destination folder is made if needed. dest is written under a partial name
// first and renamed once complete; if dest already exists it is replaced, and
// on any failure the partial file is removed and dest is left as it was.
func transfer(ctx context.Context, src, dest string, m Method) (mode string, err error) {
	if err := os.MkdirAll(filepath.Dir(dest), 0o755); err != nil {
		return "", fmt.Errorf("cannot make the folder for %s: %w", filepath.Base(dest), err)
	}
	tmp := dest + partialSuffix
	_ = os.Remove(tmp)
	defer func() {
		if err != nil {
			_ = os.Remove(tmp)
		}
	}()

	if m == MethodMove {
		// dest may already be a hard link of src (an earlier import linked
		// it). POSIX rename does nothing in that case and still reports
		// success, which would leave the source behind, so drop the
		// source name ourselves: the data is already at dest.
		if si, err := os.Stat(src); err == nil {
			if di, err := os.Stat(dest); err == nil && os.SameFile(si, di) {
				if err := os.Remove(src); err != nil {
					return "", fmt.Errorf("cannot remove %s: %w", filepath.Base(src), err)
				}
				return ModeMove, nil
			}
		}
		if err := os.Rename(src, dest); err == nil {
			return ModeMove, nil
		}
		// Another volume, or a name in use: copy it, then delete the original.
		if err := copyFile(ctx, src, tmp); err != nil {
			return "", err
		}
		if err := os.Rename(tmp, dest); err != nil {
			return "", fmt.Errorf("cannot put %s in place: %w", filepath.Base(dest), err)
		}
		if err := os.Remove(src); err != nil {
			// The data is where it belongs; only the leftover stays.
			return ModeCopy, nil
		}
		return ModeMove, nil
	}

	// A hardlink is tried first. Whether source and destination are on the same
	// volume is not asked: the link either works or it does not.
	if err := linkFile(src, tmp); err == nil {
		if err := os.Rename(tmp, dest); err != nil {
			return "", fmt.Errorf("cannot put %s in place: %w", filepath.Base(dest), err)
		}
		return ModeHardlink, nil
	}
	if err := copyFile(ctx, src, tmp); err != nil {
		return "", err
	}
	if err := os.Rename(tmp, dest); err != nil {
		return "", fmt.Errorf("cannot put %s in place: %w", filepath.Base(dest), err)
	}
	return ModeCopy, nil
}

// ctxReader stops a long copy when the importer is shut down.
type ctxReader struct {
	ctx context.Context
	r   io.Reader
}

func (c ctxReader) Read(p []byte) (int, error) {
	if err := c.ctx.Err(); err != nil {
		return 0, err
	}
	return c.r.Read(p)
}

// copyFile copies src to dst, keeps its modified time, and checks the size.
// A cancelled context stops it and leaves nothing behind.
func copyFile(ctx context.Context, src, dst string) (err error) {
	in, err := os.Open(src)
	if err != nil {
		return fmt.Errorf("cannot read %s: %w", filepath.Base(src), err)
	}
	defer in.Close()
	st, err := in.Stat()
	if err != nil {
		return err
	}
	out, err := os.OpenFile(dst, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o644)
	if err != nil {
		return fmt.Errorf("cannot write %s: %w", filepath.Base(dst), err)
	}
	defer func() {
		if cerr := out.Close(); err == nil && cerr != nil {
			err = cerr
		}
		if err != nil {
			_ = os.Remove(dst)
		}
	}()
	n, err := io.CopyBuffer(out, ctxReader{ctx: ctx, r: in}, make([]byte, 1<<20))
	if err != nil {
		return fmt.Errorf("copying %s failed: %w", filepath.Base(src), err)
	}
	if n != st.Size() {
		return fmt.Errorf("copying %s stopped early (%d of %d bytes)", filepath.Base(src), n, st.Size())
	}
	if err := out.Sync(); err != nil {
		return err
	}
	_ = os.Chtimes(dst, time.Now(), st.ModTime())
	return nil
}

// removeEmptyDirs removes empty folders under root, deepest first, and root
// itself if it ends up empty. Anything with a file in it is left alone.
func removeEmptyDirs(root string) {
	var dirs []string
	_ = filepath.WalkDir(root, func(p string, d os.DirEntry, err error) error {
		if err == nil && d.IsDir() {
			dirs = append(dirs, p)
		}
		return nil
	})
	for i := len(dirs) - 1; i >= 0; i-- {
		_ = os.Remove(dirs[i]) // fails, harmlessly, when the folder is not empty
	}
}

// isNotExist reports a missing file or folder.
func isNotExist(err error) bool { return errors.Is(err, os.ErrNotExist) }
