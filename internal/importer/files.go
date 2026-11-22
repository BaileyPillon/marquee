package importer

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/BaileyPillon/marquee/internal/history"
	"github.com/BaileyPillon/marquee/internal/library"
	"github.com/BaileyPillon/marquee/internal/mediaservers"
	"github.com/BaileyPillon/marquee/internal/titles"
)

// Errors of the two file actions. The web layer maps them to HTTP statuses.
var (
	// ErrNotMatched: the file is not matched to a title (or to its episode or
	// track), so there is no name to give it.
	ErrNotMatched = errors.New("importer: that file is not matched to a title")
	// ErrFileMissing: the file is not on disk.
	ErrFileMissing = errors.New("importer: that file is not on disk")
	// ErrNameTaken: a different file already has the name.
	ErrNameTaken = errors.New("importer: a different file already has that name")
	// ErrOutsideLibrary: the path leaves the library folder it belongs to. Nothing
	// is done to a path like that, whatever the database says.
	ErrOutsideLibrary = errors.New("importer: that path is not inside its library folder")
	// ErrImporting: an import is moving files right now. Renaming and deleting
	// wait for it, because both are disk work on the same folders.
	ErrImporting = errors.New("importer: an import is running right now; try again in a moment")
)

// RenameResult is what RenameFile did.
type RenameResult struct {
	// Renamed is false when the file already had the right name.
	Renamed bool         `json:"renamed"`
	From    string       `json:"from"` // the old path under the library folder, with "/"
	To      string       `json:"to"`   // the new one
	File    library.File `json:"file"` // the row as it is now
	// Servers is how each media server took the news; empty when nothing moved.
	Servers []mediaservers.NotifyResult `json:"servers"`
	// Moved are the subtitle and note files that went along with the video, as full
	// paths. Whoever recorded a path to one of them (a subtitles list) can follow.
	Moved []FileMove `json:"-"`
}

// FileMove is one file the importer moved.
type FileMove struct{ From, To string }

// DeleteResult is what DeleteFile did.
type DeleteResult struct {
	Path string `json:"path"` // under the library folder, with "/"
	// FromDisk says the file itself was removed (or was already gone) as asked.
	FromDisk bool `json:"fromDisk"`
	// WasMissing: the file was not on disk any more, so only the row went.
	WasMissing bool                        `json:"wasMissing"`
	Servers    []mediaservers.NotifyResult `json:"servers"`
}

// safeJoin puts a path written under a library folder (with "/") onto the folder
// and checks the result is still inside it. A path that is empty, absolute,
// names a drive, has a ".." in it, or comes out at the folder itself is refused.
func safeJoin(root, rel string) (string, error) {
	rel = strings.ReplaceAll(strings.TrimSpace(rel), `\`, "/")
	if rel == "" || strings.HasPrefix(rel, "/") || (len(rel) >= 2 && rel[1] == ':') {
		return "", ErrOutsideLibrary
	}
	clean := path.Clean(rel)
	if clean == "." || clean == ".." || strings.HasPrefix(clean, "../") {
		return "", ErrOutsideLibrary
	}
	abs := filepath.Join(root, filepath.FromSlash(clean))
	if !below(root, abs) {
		return "", ErrOutsideLibrary
	}
	return abs, nil
}

// below reports whether p is strictly inside root (not root itself).
func below(root, p string) bool {
	r, err := filepath.Rel(root, p)
	if err != nil || r == "." || r == ".." || strings.HasPrefix(r, ".."+string(filepath.Separator)) || filepath.IsAbs(r) {
		return false
	}
	return true
}

// realInside reports whether the folder a file is in, with every link followed,
// is inside the library folder with its links followed: a folder inside the
// library that is really a link to somewhere else does not let a file there be
// touched. A folder that does not exist is an error (there is nothing to touch).
func realInside(root, file string) (bool, error) {
	realRoot, err := filepath.EvalSymlinks(root)
	if err != nil {
		return false, err
	}
	realDir, err := filepath.EvalSymlinks(filepath.Dir(file))
	if err != nil {
		return false, err
	}
	if sameDir(realDir, realRoot) {
		return true, nil
	}
	return below(realRoot, realDir), nil
}

// fileContext is a file row with its folder and title.
type fileContext struct {
	file   library.File
	folder library.Folder
	abs    string
}

func (s *Service) fileContext(ctx context.Context, id int64) (fileContext, error) {
	f, err := s.deps.Library.File(ctx, id)
	if err != nil {
		return fileContext{}, err
	}
	folder, err := s.deps.Library.Folder(ctx, f.FolderID)
	if err != nil {
		return fileContext{}, err
	}
	abs, err := safeJoin(folder.Path, f.RelPath)
	if err != nil {
		return fileContext{}, err
	}
	return fileContext{file: f, folder: folder, abs: abs}, nil
}

// ---- naming an existing file ------------------------------------------------------

// NameFor is the path (under the library folder, with "/") the importer would
// give a file it put in the library today: the same naming it used when the
// file arrived, worked out again from the title's catalog entry and the file's
// quality and extension.
func NameFor(det *titles.Detail, f library.File) (string, error) {
	ext := filepath.Ext(f.RelPath)
	switch det.Kind {
	case titles.KindMovie:
		return MoviePath(MovieName{Name: det.Name, Year: det.Year, Quality: f.Quality, Ext: ext}), nil
	case titles.KindShow:
		want := map[int64]bool{}
		for _, id := range f.EpisodeIDs {
			want[id] = true
		}
		var eps []titles.Episode
		for _, e := range det.Episodes {
			if want[e.ID] {
				eps = append(eps, e)
			}
		}
		if len(eps) == 0 {
			return "", ErrNotMatched
		}
		sort.Slice(eps, func(i, j int) bool {
			if eps[i].Season != eps[j].Season {
				return eps[i].Season < eps[j].Season
			}
			return eps[i].Number < eps[j].Number
		})
		season := eps[0].Season
		var nums []int
		var names []string
		for _, e := range eps {
			if e.Season != season {
				continue
			}
			nums = append(nums, e.Number)
			names = append(names, e.Name)
		}
		return EpisodePath(EpisodeName{Show: det.Name, Year: det.Year, Season: season, Episodes: nums, EpisodeNames: names,
			Quality: f.Quality, Ext: ext}), nil
	case titles.KindArtist:
		if f.TrackID == nil {
			return "", ErrNotMatched
		}
		for _, al := range det.Albums {
			for _, tr := range al.Tracks {
				if tr.ID != *f.TrackID {
					continue
				}
				multi := false
				for _, t := range al.Tracks {
					if t.Disc > 1 {
						multi = true
					}
				}
				return TrackPath(TrackName{Artist: det.Name, Album: al.Name, Year: al.Year, Disc: tr.Disc, MultiDisc: multi,
					Track: tr.Position, Title: tr.Name, Ext: ext}), nil
			}
		}
		return "", ErrNotMatched
	}
	return "", ErrNotMatched
}

// sidecarExts are the files that travel with a video: subtitles and notes with
// the same name.
var sidecarExts = map[string]bool{
	".srt": true, ".ass": true, ".ssa": true, ".sub": true, ".idx": true, ".vtt": true, ".sup": true, ".nfo": true,
}

// sidecars lists the files next to a media file that share its name (movie.mkv,
// movie.en.srt, movie.nfo), as the part after the stem ("" + ".en.srt").
func sidecars(abs string) []string {
	dir, base := filepath.Dir(abs), filepath.Base(abs)
	stem := strings.TrimSuffix(base, filepath.Ext(base))
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil
	}
	var out []string
	for _, e := range entries {
		name := e.Name()
		if e.IsDir() || len(name) <= len(stem) || !strings.EqualFold(name[:len(stem)], stem) || name[len(stem)] != '.' {
			continue
		}
		if sidecarExts[strings.ToLower(filepath.Ext(name))] {
			out = append(out, name[len(stem):])
		}
	}
	return out
}

// RenameFile gives an existing library file the name the importer would give it
// today, moving it (inside its library folder) when the name or the folder
// changes. The file's row, the title's history and the media servers are told.
//
// A file already named right is left alone (Renamed is false). Subtitle and
// note files next to it that share its name move with it. ErrNameTaken is
// returned when a different file is already at the new path; the file is never
// overwritten.
func (s *Service) RenameFile(ctx context.Context, fileID int64) (RenameResult, error) {
	if !s.work.TryLock() {
		return RenameResult{}, ErrImporting
	}
	defer s.work.Unlock()
	fc, err := s.fileContext(ctx, fileID)
	if err != nil {
		return RenameResult{}, err
	}
	f := fc.file
	if f.TitleID == nil || f.State != library.StateMatched {
		return RenameResult{}, ErrNotMatched
	}
	det, err := s.deps.Titles.Get(ctx, *f.TitleID)
	if err != nil {
		return RenameResult{}, err
	}
	newRel, err := NameFor(det, f)
	if err != nil {
		return RenameResult{}, err
	}
	newAbs, err := safeJoin(fc.folder.Path, newRel)
	if err != nil {
		return RenameResult{}, err
	}
	res := RenameResult{From: f.RelPath, To: newRel, File: f, Servers: []mediaservers.NotifyResult{}}

	oldSt, err := os.Stat(fc.abs)
	switch {
	case isNotExist(err):
		return res, ErrFileMissing
	case err != nil:
		return res, err
	}
	if ok, err := realInside(fc.folder.Path, fc.abs); err != nil || !ok {
		return res, ErrOutsideLibrary
	}
	if f.RelPath == newRel {
		return res, nil
	}
	if newSt, err := os.Stat(newAbs); err == nil && !os.SameFile(oldSt, newSt) {
		return res, ErrNameTaken
	}
	if err := os.MkdirAll(filepath.Dir(newAbs), 0o755); err != nil {
		return res, fmt.Errorf("Marquee cannot make the folder for %s: %w", filepath.Base(newAbs), err)
	}
	if ok, err := realInside(fc.folder.Path, newAbs); err != nil || !ok {
		return res, ErrOutsideLibrary
	}

	// The related files first would leave a video without its subtitles if the
	// video's move then failed; move the video first and bring the others after.
	related := sidecars(fc.abs)
	if err := os.Rename(fc.abs, newAbs); err != nil {
		return res, fmt.Errorf("Marquee could not rename the file: %w", err)
	}
	mtime := oldSt.ModTime()
	if st, err := os.Stat(newAbs); err == nil {
		mtime = st.ModTime()
	}
	if err := s.deps.Library.RenameFile(ctx, f.ID, newRel, mtime); err != nil {
		_ = os.Rename(newAbs, fc.abs) // the row did not follow: put the file back
		if errors.Is(err, library.ErrNameInUse) {
			return res, ErrNameTaken
		}
		return res, err
	}
	oldStem := strings.TrimSuffix(fc.abs, filepath.Ext(fc.abs))
	newStem := strings.TrimSuffix(newAbs, filepath.Ext(newAbs))
	for _, tail := range related {
		to := newStem + tail
		if _, err := os.Stat(to); err == nil {
			continue // never overwrite one that is already there
		}
		if err := os.Rename(oldStem+tail, to); err != nil {
			s.log.Warn("rename: could not move a file that goes with the video", "file", filepath.Base(oldStem+tail), "err", err)
			continue
		}
		res.Moved = append(res.Moved, FileMove{From: oldStem + tail, To: to})
	}
	pruneDir(fc.folder.Path, filepath.Dir(fc.abs))

	updated, err := s.deps.Library.File(ctx, f.ID)
	if err != nil {
		updated = f
		updated.RelPath = newRel
	}
	res.Renamed, res.File = true, updated

	s.note(ctx, history.Entry{TitleID: *f.TitleID, Event: history.Renamed, Detail: map[string]any{
		"file": path.Base(newRel), "from": path.Base(f.RelPath), "to": path.Base(newRel),
		"fromPath": f.RelPath, "toPath": newRel, "quality": f.Quality,
	}})
	if s.deps.Servers != nil {
		// Only the video is announced; the media server finds its subtitles when it
		// looks at the folder.
		res.Servers = s.deps.Servers.NotifyRenamed(ctx, []string{fc.abs}, []string{newAbs})
		if res.Servers == nil {
			res.Servers = []mediaservers.NotifyResult{}
		}
	}
	s.publishLibrary("renamed", *f.TitleID)
	s.log.Info("file renamed", "file", f.ID, "from", f.RelPath, "to", newRel)
	return res, nil
}

// ---- deleting ------------------------------------------------------------------------

// DeleteFile takes a file out of the library: its row always, and the file on
// disk when fromDisk is set. It never touches a path outside the file's library
// folder (ErrOutsideLibrary), even if the row says otherwise or a folder in the
// way is a link to somewhere else. A file that is already gone from the disk is
// not an error: the row goes and WasMissing says so.
//
// Without fromDisk only the row goes, and the next scan finds the file again.
// The title's history and, when the file itself went, the media servers are told.
func (s *Service) DeleteFile(ctx context.Context, fileID int64, fromDisk bool) (DeleteResult, error) {
	if !s.work.TryLock() {
		return DeleteResult{}, ErrImporting
	}
	defer s.work.Unlock()
	fc, err := s.fileContext(ctx, fileID)
	if err != nil {
		return DeleteResult{}, err
	}
	f := fc.file
	res := DeleteResult{Path: f.RelPath, Servers: []mediaservers.NotifyResult{}}

	if fromDisk {
		st, err := os.Lstat(fc.abs)
		switch {
		case isNotExist(err):
			res.WasMissing = true
		case err != nil:
			return res, fmt.Errorf("Marquee could not look at the file: %w", err)
		case st.IsDir():
			return res, ErrOutsideLibrary // a row that names a folder: never remove one
		default:
			ok, err := realInside(fc.folder.Path, fc.abs)
			if err != nil || !ok {
				return res, ErrOutsideLibrary
			}
			if err := os.Remove(fc.abs); err != nil && !isNotExist(err) {
				return res, fmt.Errorf("Marquee could not delete the file: %w", err)
			}
		}
		res.FromDisk = true
		pruneDir(fc.folder.Path, filepath.Dir(fc.abs))
	}

	if err := s.deps.Library.ForgetFiles(ctx, f.FolderID, []string{f.RelPath}); err != nil {
		return res, err
	}
	if f.TitleID != nil {
		s.note(ctx, history.Entry{TitleID: *f.TitleID, Event: history.Deleted, Detail: map[string]any{
			"file": path.Base(f.RelPath), "path": f.RelPath, "fromDisk": res.FromDisk, "wasMissing": res.WasMissing,
			"quality": f.Quality, "size": f.Size,
		}})
	}
	if res.FromDisk && s.deps.Servers != nil {
		res.Servers = s.deps.Servers.Notify(ctx, mediaservers.Event{Kind: mediaservers.EventDeleted, Paths: []string{fc.abs}})
		if res.Servers == nil {
			res.Servers = []mediaservers.NotifyResult{}
		}
	}
	if f.TitleID != nil {
		s.publishLibrary("deleted", *f.TitleID)
	}
	s.log.Info("library file deleted", "file", f.ID, "path", f.RelPath, "fromDisk", res.FromDisk)
	return res, nil
}

func (s *Service) publishLibrary(event string, titleID int64) {
	if s.hub == nil {
		return
	}
	s.hub.Publish(LibraryTopic, map[string]any{"event": event, "titleId": titleID, "files": 1, "at": time.Now().UTC()})
}
