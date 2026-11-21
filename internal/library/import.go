package library

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path"
	"path/filepath"
	"strings"
)

// WantMedia reports whether a file name is media of a folder kind ("movie",
// "show" or "music") the way the scanner decides it: video or audio by
// extension, not hidden, not a partial download, not a sample clip. The
// importer uses the same test on a finished download, so both agree on what
// counts.
func WantMedia(name, kind string) bool { return wantFile(name, kind) }

// SkipFolderName reports whether the scanner never enters a folder of this
// name (sample clips, extras, NAS and Windows housekeeping, hidden folders).
func SkipFolderName(name string) bool { return skipDir(name) }

// ImportedFile is a file the importer just put in a library folder. The
// importer already knows what it is, so it is recorded as matched, without
// waiting for a scan.
type ImportedFile struct {
	// RelPath is under the folder, written with "/".
	RelPath string
	// Size is the file's size in bytes; 0 reads it from the disk.
	Size       int64
	TitleID    int64
	EpisodeIDs []int64 // empty for a movie
	// Quality is the quality name (for example "Bluray-1080p"); empty keeps what
	// the file's own name says.
	Quality string
}

// ErrBadRelPath is returned for a relative path that is empty, absolute or
// leaves the folder.
var ErrBadRelPath = errors.New("library: that file path is not inside the folder")

// cleanRel validates a relative path and returns it with "/" separators.
func cleanRel(rel string) (string, error) {
	rel = strings.ReplaceAll(strings.TrimSpace(rel), `\`, "/")
	if rel == "" || strings.HasPrefix(rel, "/") {
		return "", ErrBadRelPath
	}
	clean := path.Clean(rel)
	if clean == "." || clean == ".." || strings.HasPrefix(clean, "../") {
		return "", ErrBadRelPath
	}
	if len(clean) >= 2 && clean[1] == ':' { // a drive letter
		return "", ErrBadRelPath
	}
	return clean, nil
}

// ImportFiles records files the importer put into a folder as matched to a
// title. Each file is looked at on disk for its size and modified time, so a
// later scan sees nothing changed and leaves the row alone. A file that is not
// there is an error; the others are still recorded.
func (s *Service) ImportFiles(ctx context.Context, folderID int64, files []ImportedFile) error {
	f, err := s.store.GetFolder(ctx, folderID)
	if err != nil {
		return err
	}
	m := newMatcher(ctx, nil, s.log)
	var (
		ws   []fileWrite
		errs []error
	)
	for _, in := range files {
		rel, err := cleanRel(in.RelPath)
		if err != nil {
			errs = append(errs, fmt.Errorf("%q: %w", in.RelPath, err))
			continue
		}
		if in.TitleID <= 0 {
			errs = append(errs, fmt.Errorf("%q: a file needs a title", in.RelPath))
			continue
		}
		st, err := os.Stat(filepath.Join(f.Path, filepath.FromSlash(rel)))
		if err != nil || !st.Mode().IsRegular() {
			errs = append(errs, fmt.Errorf("%q: not found in the folder", rel))
			continue
		}
		w := m.classify(f.Kind, rel)
		w.Size, w.MTime = st.Size(), st.ModTime().UnixNano()
		w.State = StateMatched
		id := in.TitleID
		w.TitleID = &id
		w.TrackID = nil
		w.EpisodeIDs = nil
		if len(in.EpisodeIDs) > 0 {
			w.EpisodeIDs = append([]int64{}, in.EpisodeIDs...)
		}
		if in.Quality != "" {
			w.Quality = in.Quality
		}
		ws = append(ws, w)
	}
	if len(ws) > 0 {
		if err := s.store.UpsertFiles(ctx, f.ID, s.now(), ws); err != nil {
			return err
		}
	}
	return errors.Join(errs...)
}

// ForgetFiles drops the rows of files the importer removed from a folder (an
// upgrade replaced them). Nothing on disk is touched.
func (s *Service) ForgetFiles(ctx context.Context, folderID int64, relPaths []string) error {
	var rels []string
	for _, r := range relPaths {
		rel, err := cleanRel(r)
		if err != nil {
			continue
		}
		rels = append(rels, rel)
	}
	return s.store.DeleteFiles(ctx, folderID, rels)
}
