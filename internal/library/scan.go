package library

import (
	"context"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/BaileyPillon/marquee/internal/parse"
)

// Extensions the scanner reads.
var (
	videoExt = setOf("mkv", "mp4", "m4v", "avi", "mov", "ts", "wmv", "webm")
	audioExt = setOf("flac", "mp3", "m4a", "aac", "ogg", "opus", "wav", "alac")
)

func setOf(items ...string) map[string]bool {
	m := make(map[string]bool, len(items))
	for _, i := range items {
		m[i] = true
	}
	return m
}

// skipDirs are folders the scanner never enters (compared in lower case): NAS
// and Windows housekeeping, sample clips, and Plex-style extras.
var skipDirs = setOf(
	"@eadir", "#recycle", "$recycle.bin", "system volume information", "lost+found",
	"sample", "samples",
	"extras", "featurettes", "behind the scenes", "deleted scenes", "interviews", "trailers",
)

// progressEvery is how many files a walk finds between progress reports.
const progressEvery = 500

func skipDir(name string) bool {
	return strings.HasPrefix(name, ".") || skipDirs[strings.ToLower(name)]
}

// wantFile decides whether a file name is media of the folder's kind. Hidden
// files, partial downloads and sample clips are not.
func wantFile(name, kind string) bool {
	if strings.HasPrefix(name, ".") {
		return false // hidden, and macOS "._" shadow files
	}
	lower := strings.ToLower(name)
	if strings.HasSuffix(lower, ".part") || strings.HasSuffix(lower, ".!qb") {
		return false
	}
	ext := strings.TrimPrefix(filepath.Ext(lower), ".")
	if kind == KindMusic {
		return audioExt[ext]
	}
	if !videoExt[ext] {
		return false
	}
	// Parsing every name would be wasteful; only look at ones that say "sample".
	if strings.Contains(lower, "sample") && parse.ParseRelease(name).IsSample {
		return false
	}
	return true
}

// foundFile is one media file on disk.
type foundFile struct {
	Rel   string // relative to the folder, with "/"
	Size  int64
	MTime int64 // unix nanoseconds
}

// walkResult is what one walk of a folder found. Unreadable lists relative
// paths the walk could not read; files under them must not be called missing.
type walkResult struct {
	Files      []foundFile
	Unreadable []string
}

func relSlash(root, p string) string {
	rel, err := filepath.Rel(root, p)
	if err != nil {
		rel = p
	}
	return filepath.ToSlash(rel)
}

// walkFolder lists the media files under root. It does not follow symlinked
// folders (which could loop) but does follow symlinked files. It stops with
// ctx's error as soon as ctx is cancelled, and fails if root itself cannot be
// read (so a missing drive never makes every file look missing).
func walkFolder(ctx context.Context, root, kind string, progress func(seen int)) (walkResult, error) {
	var res walkResult
	err := filepath.WalkDir(root, func(p string, d fs.DirEntry, err error) error {
		if cerr := ctx.Err(); cerr != nil {
			return cerr
		}
		if err != nil {
			if p == root {
				return err
			}
			res.Unreadable = append(res.Unreadable, relSlash(root, p))
			if d != nil && d.IsDir() {
				return filepath.SkipDir
			}
			return nil
		}
		if d.IsDir() {
			if p != root && skipDir(d.Name()) {
				return filepath.SkipDir
			}
			return nil
		}
		if !wantFile(d.Name(), kind) {
			return nil
		}
		var info fs.FileInfo
		switch {
		case d.Type()&fs.ModeSymlink != 0:
			info, err = os.Stat(p)
			if err != nil || !info.Mode().IsRegular() {
				return nil
			}
		case d.Type().IsRegular():
			info, err = d.Info()
			if err != nil {
				return nil // vanished while we walked
			}
		default:
			return nil
		}
		res.Files = append(res.Files, foundFile{Rel: relSlash(root, p), Size: info.Size(), MTime: info.ModTime().UnixNano()})
		if progress != nil && len(res.Files)%progressEvery == 0 {
			progress(len(res.Files))
		}
		return nil
	})
	return res, err
}

// ---- scans -------------------------------------------------------------------

// ScanSummary says what one scan of one folder found.
type ScanSummary struct {
	FolderID  int64 `json:"folderId"`
	Seen      int   `json:"seen"`      // media files on disk
	Added     int   `json:"added"`     // new rows
	Updated   int   `json:"updated"`   // size or modified time changed
	Missing   int   `json:"missing"`   // newly gone from disk
	Returned  int   `json:"returned"`  // were missing, are back unchanged
	Unchanged int   `json:"unchanged"` // seen again, nothing to do
	Matched   int   `json:"matched"`   // became matched during this scan
	Millis    int64 `json:"millis"`
}

// ScanEvent is the payload published on the "library.scan" topic. State is
// "started", "progress", "done" or "failed".
type ScanEvent struct {
	FolderID int64        `json:"folderId"`
	State    string       `json:"state"`
	Seen     int          `json:"seen,omitempty"`
	Summary  *ScanSummary `json:"summary,omitempty"`
	Error    string       `json:"error,omitempty"`
}

func (s *Service) publish(ev ScanEvent) {
	if s.hub != nil {
		s.hub.Publish(ScanTopic, ev)
	}
}

// begin claims a folder for scanning; false means a scan of it is running.
func (s *Service) begin(id int64) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.running[id] {
		return false
	}
	s.running[id] = true
	return true
}

func (s *Service) end(id int64) {
	s.mu.Lock()
	delete(s.running, id)
	s.mu.Unlock()
}

// ScanNow scans one folder and waits for it. It returns ErrScanRunning if that
// folder is already being scanned.
func (s *Service) ScanNow(ctx context.Context, folderID int64) (ScanSummary, error) {
	f, err := s.store.GetFolder(ctx, folderID)
	if err != nil {
		return ScanSummary{}, err
	}
	if !s.begin(f.ID) {
		return ScanSummary{FolderID: f.ID}, ErrScanRunning
	}
	defer s.end(f.ID)
	return s.scanLocked(ctx, f)
}

// ScanAll scans every folder, at most two walks at a time, and returns the
// summary of each folder that was scanned. A folder already being scanned is
// skipped; other failures are joined into the returned error.
func (s *Service) ScanAll(ctx context.Context) ([]ScanSummary, error) {
	folders, err := s.store.ListFolders(ctx)
	if err != nil {
		return nil, err
	}
	sums := make([]ScanSummary, len(folders))
	errs := make([]error, len(folders))
	var wg sync.WaitGroup
	for i, f := range folders {
		if !s.begin(f.ID) {
			errs[i] = ErrScanRunning
			continue
		}
		wg.Add(1)
		go func() {
			defer wg.Done()
			defer s.end(f.ID)
			sums[i], errs[i] = s.scanLocked(ctx, f)
		}()
	}
	wg.Wait()
	var out []ScanSummary
	var failed []error
	for i := range folders {
		switch {
		case errs[i] == nil:
			out = append(out, sums[i])
		case errors.Is(errs[i], ErrScanRunning):
		default:
			failed = append(failed, errs[i])
		}
	}
	return out, errors.Join(failed...)
}

// ScanJob is the scheduled job: scan every folder.
func (s *Service) ScanJob(ctx context.Context) error {
	_, err := s.ScanAll(ctx)
	return err
}

// StartScan begins scanning in the background and returns at once: one folder,
// or every folder when folderID is 0. It reports which folders it started and
// which were already being scanned. ErrNotFound means the folder is unknown.
func (s *Service) StartScan(ctx context.Context, folderID int64) (started, running []int64, err error) {
	var folders []Folder
	if folderID != 0 {
		f, err := s.store.GetFolder(ctx, folderID)
		if err != nil {
			return nil, nil, err
		}
		folders = []Folder{f}
	} else if folders, err = s.store.ListFolders(ctx); err != nil {
		return nil, nil, err
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return nil, nil, ErrClosed
	}
	for _, f := range folders {
		if s.running[f.ID] {
			running = append(running, f.ID)
			continue
		}
		s.running[f.ID] = true
		started = append(started, f.ID)
		s.wg.Add(1)
		go func() {
			defer s.wg.Done()
			defer s.end(f.ID)
			// scanLocked reports its own failure through the log and the hub.
			_, _ = s.scanLocked(s.baseCtx, f)
		}()
	}
	return started, running, nil
}

// walkLimited runs a walk once one of the two walk slots is free.
func (s *Service) walkLimited(ctx context.Context, f Folder, progress func(int)) (walkResult, error) {
	select {
	case s.walkSem <- struct{}{}:
	case <-ctx.Done():
		return walkResult{}, ctx.Err()
	}
	defer func() { <-s.walkSem }()
	return s.walk(ctx, f.Path, f.Kind, progress)
}

// scanLocked scans one folder; the caller holds its running flag.
func (s *Service) scanLocked(ctx context.Context, f Folder) (sum ScanSummary, err error) {
	start := time.Now()
	sum.FolderID = f.ID
	s.publish(ScanEvent{FolderID: f.ID, State: "started"})
	defer func() {
		sum.Millis = time.Since(start).Milliseconds()
		if err != nil {
			s.log.Warn("library scan failed", "folder", f.ID, "path", f.Path, "err", err)
			s.publish(ScanEvent{FolderID: f.ID, State: "failed", Error: err.Error()})
			return
		}
		s.log.Info("library scan finished", "folder", f.ID, "path", f.Path, "seen", sum.Seen, "added", sum.Added,
			"updated", sum.Updated, "missing", sum.Missing, "returned", sum.Returned, "unchanged", sum.Unchanged,
			"matched", sum.Matched, "ms", sum.Millis)
		s.publish(ScanEvent{FolderID: f.ID, State: "done", Seen: sum.Seen, Summary: &sum})
	}()

	index, err := s.store.FileIndex(ctx, f.ID)
	if err != nil {
		return sum, err
	}
	res, err := s.walkLimited(ctx, f, func(seen int) {
		s.publish(ScanEvent{FolderID: f.ID, State: "progress", Seen: seen})
	})
	if err != nil {
		return sum, err
	}
	if err := ctx.Err(); err != nil {
		return sum, err
	}
	sum.Seen = len(res.Files)

	m := newMatcher(ctx, s.titleSource(), s.log)
	var (
		writes   []fileWrite
		returned []int64
	)
	seen := make(map[string]struct{}, len(res.Files))
	for _, ff := range res.Files {
		seen[ff.Rel] = struct{}{}
		row, known := index[ff.Rel]
		switch {
		case !known:
			sum.Added++
			w := s.classify(m, f.Kind, ff, nil)
			if w.State == StateMatched {
				sum.Matched++
			}
			writes = append(writes, w)
		case row.Size != ff.Size || row.MTime != ff.MTime:
			sum.Updated++
			w := s.classify(m, f.Kind, ff, &row)
			if w.State == StateMatched {
				sum.Matched++
			}
			writes = append(writes, w)
		default:
			if row.Missing {
				sum.Returned++
				returned = append(returned, row.ID)
			} else {
				sum.Unchanged++
			}
			// A file that did not match last time may match now that titles
			// have been added.
			if row.State == StateUnmatched && m.enabled() {
				if w := s.classify(m, f.Kind, ff, &row); w.State == StateMatched {
					sum.Matched++
					writes = append(writes, w)
				}
			} else if row.State == StateMatched && row.NoTrack && f.Kind == KindMusic && m.enabled() {
				// A track scanned before the albums were known (or loaded) gets
				// its track now.
				if w := s.classify(m, f.Kind, ff, &row); w.State == StateMatched && w.TrackID != nil {
					writes = append(writes, w)
				}
			}
		}
	}

	var gone []int64
	for rel, row := range index {
		if _, ok := seen[rel]; ok || row.Missing || underAny(rel, res.Unreadable) {
			continue
		}
		gone = append(gone, row.ID)
	}
	sum.Missing = len(gone)

	if err := ctx.Err(); err != nil {
		return sum, err
	}
	now := s.now()
	if err := s.store.UpsertFiles(ctx, f.ID, now, writes); err != nil {
		return sum, err
	}
	if err := s.store.ClearMissing(ctx, returned); err != nil {
		return sum, err
	}
	if err := s.store.MarkMissing(ctx, gone, now); err != nil {
		return sum, err
	}
	return sum, nil
}

// classify builds the row for one file. A file the owner marked ignored stays
// ignored whatever its name says.
func (s *Service) classify(m *matcher, kind string, ff foundFile, existing *indexRow) fileWrite {
	w := m.classify(kind, ff.Rel)
	w.Size, w.MTime = ff.Size, ff.MTime
	if existing != nil && existing.State == StateIgnored {
		w.State, w.TitleID, w.EpisodeIDs, w.TrackID = StateIgnored, nil, nil, nil
	}
	return w
}

// underAny reports whether rel is one of the paths or below one of them.
func underAny(rel string, prefixes []string) bool {
	for _, p := range prefixes {
		if rel == p || strings.HasPrefix(rel, p+"/") {
			return true
		}
	}
	return false
}
