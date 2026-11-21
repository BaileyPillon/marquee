package importer

import (
	"archive/zip"
	"context"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/bodgit/sevenzip"
	"github.com/nwaples/rardecode/v2"

	"github.com/BaileyPillon/marquee/internal/library"
)

// maxUnpacked bounds what one download may unpack to, so a decompression bomb
// cannot fill the disk.
const maxUnpacked int64 = 1 << 40

var (
	reRarPart  = regexp.MustCompile(`(?i)\.part0*(\d+)\.rar$`)
	re7zVolume = regexp.MustCompile(`(?i)\.7z\.001$`)
)

// archiveKind says what an archive file is, from its name, for the first
// volume of a set only: "rar", "7z" or "zip". Later volumes (.r00, .part02.rar,
// .7z.002) are read through the first and are not archives of their own.
func archiveKind(name string) string {
	lower := strings.ToLower(name)
	switch {
	case strings.HasSuffix(lower, ".zip"):
		return "zip"
	case strings.HasSuffix(lower, ".7z"), re7zVolume.MatchString(lower):
		return "7z"
	case strings.HasSuffix(lower, ".rar"):
		if m := reRarPart.FindStringSubmatch(lower); m != nil && m[1] != "1" {
			return "" // part02 and up
		}
		return "rar"
	}
	return ""
}

// findArchives lists the archives under root (root itself, if it is a file),
// skipping the folders the scanner skips.
func findArchives(root string) []string {
	var out []string
	st, err := os.Stat(root)
	if err != nil {
		return nil
	}
	if !st.IsDir() {
		if archiveKind(filepath.Base(root)) != "" {
			out = append(out, root)
		}
		return out
	}
	_ = filepath.WalkDir(root, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		if d.IsDir() {
			if p != root && library.SkipFolderName(d.Name()) {
				return filepath.SkipDir
			}
			return nil
		}
		if !strings.HasPrefix(d.Name(), ".") && archiveKind(d.Name()) != "" {
			out = append(out, p)
		}
		return nil
	})
	return out
}

// entryPath makes an archive entry's name safe to put under a folder: forward
// slashes, no "..", no absolute path, no drive letter or stream name. ok is
// false for a name that tries to escape.
func entryPath(name string) (string, bool) {
	name = strings.ReplaceAll(name, `\`, "/")
	if name == "" || strings.HasPrefix(name, "/") || strings.Contains(name, ":") {
		return "", false
	}
	clean := path.Clean(name)
	if clean == "." || clean == ".." || strings.HasPrefix(clean, "../") {
		return "", false
	}
	return clean, true
}

// wantEntry says whether an archive entry is media worth unpacking: the same
// test the scanner uses on files, and none of its skipped folders on the way.
func wantEntry(clean, kind string) bool {
	parts := strings.Split(clean, "/")
	for _, d := range parts[:len(parts)-1] {
		if library.SkipFolderName(d) {
			return false
		}
	}
	return library.WantMedia(parts[len(parts)-1], kind)
}

// unpacker collects what one run of extraction wrote.
type unpacker struct {
	dest    string
	kind    string // library folder kind: movie, show or music
	written int64
	files   []string
}

// save writes one entry's data to dest/<prefix>/<clean>.
func (u *unpacker) save(prefix, clean string, r io.Reader) error {
	target := filepath.Join(u.dest, prefix, filepath.FromSlash(clean))
	if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
		return err
	}
	out, err := os.OpenFile(target, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o644)
	if err != nil {
		return err
	}
	left := maxUnpacked - u.written
	n, err := io.Copy(out, io.LimitReader(r, left+1))
	if cerr := out.Close(); err == nil {
		err = cerr
	}
	u.written += n
	if err == nil && n > left {
		err = errors.New("the archive unpacks to more than the importer allows")
	}
	if err != nil {
		_ = os.Remove(target)
		return err
	}
	u.files = append(u.files, target)
	return nil
}

// extractArchives unpacks the media files from every archive under root into
// dest and returns their paths. A password-protected or damaged archive is an
// error that names the archive.
func extractArchives(ctx context.Context, root, dest, kind string) ([]string, error) {
	archives := findArchives(root)
	if len(archives) == 0 {
		return nil, nil
	}
	u := &unpacker{dest: dest, kind: kind}
	for i, a := range archives {
		if err := ctx.Err(); err != nil {
			return u.files, err
		}
		prefix := fmt.Sprintf("a%02d", i)
		var err error
		switch archiveKind(filepath.Base(a)) {
		case "zip":
			err = u.fromZip(ctx, a, prefix)
		case "7z":
			err = u.from7z(ctx, a, prefix)
		case "rar":
			err = u.fromRar(ctx, a, prefix)
		}
		if err != nil {
			return u.files, fmt.Errorf("%s: %w", filepath.Base(a), err)
		}
	}
	return u.files, nil
}

func (u *unpacker) fromZip(ctx context.Context, name, prefix string) error {
	zr, err := zip.OpenReader(name)
	if zr != nil && errors.Is(err, zip.ErrInsecurePath) {
		err = nil // an entry with an odd name: entryPath leaves such entries out below
	}
	if err != nil {
		return fmt.Errorf("cannot open the zip file: %w", err)
	}
	defer zr.Close()
	for _, f := range zr.File {
		if err := ctx.Err(); err != nil {
			return err
		}
		if f.FileInfo().IsDir() {
			continue
		}
		clean, ok := entryPath(f.Name)
		if !ok || !wantEntry(clean, u.kind) {
			continue
		}
		if f.Flags&0x1 != 0 {
			return errors.New("the zip file is password protected")
		}
		rc, err := f.Open()
		if err != nil {
			return fmt.Errorf("cannot read %s: %w", path.Base(clean), err)
		}
		err = u.save(prefix, clean, rc)
		rc.Close()
		if err != nil {
			return fmt.Errorf("cannot unpack %s: %w", path.Base(clean), err)
		}
	}
	return nil
}

func (u *unpacker) from7z(ctx context.Context, name, prefix string) error {
	zr, err := sevenzip.OpenReader(name)
	if err != nil {
		return fmt.Errorf("cannot open the 7z file: %w", err)
	}
	defer zr.Close()
	for _, f := range zr.File {
		if err := ctx.Err(); err != nil {
			return err
		}
		if f.FileInfo().IsDir() {
			continue
		}
		clean, ok := entryPath(f.Name)
		if !ok || !wantEntry(clean, u.kind) {
			continue
		}
		rc, err := f.Open()
		if err != nil {
			return fmt.Errorf("cannot read %s: %w", path.Base(clean), err)
		}
		err = u.save(prefix, clean, rc)
		rc.Close()
		if err != nil {
			return fmt.Errorf("cannot unpack %s: %w", path.Base(clean), err)
		}
	}
	return nil
}

func (u *unpacker) fromRar(ctx context.Context, name, prefix string) error {
	rr, err := rardecode.OpenReader(name)
	if err != nil {
		return fmt.Errorf("cannot open the rar file: %w", err)
	}
	defer rr.Close()
	for {
		if err := ctx.Err(); err != nil {
			return err
		}
		h, err := rr.Next()
		if errors.Is(err, io.EOF) {
			return nil
		}
		if err != nil {
			return fmt.Errorf("cannot read the rar file: %w", err)
		}
		if h.IsDir || h.Encrypted {
			if h.Encrypted {
				return errors.New("the rar file is password protected")
			}
			continue
		}
		clean, ok := entryPath(h.Name)
		if !ok || !wantEntry(clean, u.kind) {
			continue
		}
		if err := u.save(prefix, clean, rr); err != nil {
			return fmt.Errorf("cannot unpack %s: %w", path.Base(clean), err)
		}
	}
}
