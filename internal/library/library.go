// Package library knows which folders Marquee watches, scans them for media
// files, reads each file's name with the parse module, and matches the files
// to titles. It only ever writes rows: nothing here moves, renames or deletes
// a file on disk.
//
// SQL lives in store.go. The titles module is not imported: matching goes
// through the TitleSource interface, wired in with SetTitleSource.
package library

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"time"
	"unicode/utf8"

	"github.com/BaileyPillon/marquee/internal/core/events"
	"github.com/BaileyPillon/marquee/internal/parse"
)

// Folder kinds (also the kind recorded on every file in the folder).
const (
	KindMovie = "movie"
	KindShow  = "show"
	KindMusic = "music"
)

// File match states.
const (
	StateMatched   = "matched"
	StateUnmatched = "unmatched"
	StateIgnored   = "ignored"
)

// Kinds of candidate the scanner asks the TitleSource for.
const (
	CandidateMovie  = "movie"
	CandidateShow   = "show"
	CandidateArtist = "artist"
)

// Scheduling and events.
const (
	JobScan      = "library.scan"
	ScanInterval = 6 * time.Hour
	ScanTopic    = "library.scan"

	// maxWalks is how many folder walks may run at the same time.
	maxWalks = 2

	maxLabelRunes = 100
)

// Errors the web layer maps to HTTP statuses.
var (
	ErrNotFound       = errors.New("library: not found")
	ErrInvalidKind    = errors.New("library: kind must be movie, show or music")
	ErrInvalidPath    = errors.New("library: the path must be a full (absolute) path")
	ErrPathNotFound   = errors.New("library: that folder does not exist")
	ErrNotDirectory   = errors.New("library: that path is not a folder")
	ErrNested         = errors.New("library: that folder is inside another library folder")
	ErrContainsFolder = errors.New("library: that folder contains another library folder")
	ErrDuplicate      = errors.New("library: that folder is already in the library")
	ErrScanRunning    = errors.New("library: that folder is already being scanned")
	ErrClosed         = errors.New("library: service is closed")
)

// Candidate is one title the matcher may pick, as the titles service offers it.
type Candidate struct {
	ID       int64
	Kind     string
	Name     string
	Year     int
	AltNames []string
}

// TitleSource is what the library needs from the titles service.
//
// Candidates lists every title of a kind: kind is "movie", "show" or "artist"
// (the CandidateMovie, CandidateShow and CandidateArtist constants). EpisodeID
// finds the episode of a show; it reports false, not an error, when the show
// has no such episode.
type TitleSource interface {
	Candidates(ctx context.Context, kind string) ([]Candidate, error)
	EpisodeID(ctx context.Context, titleID int64, season, number int) (int64, bool, error)
}

// AlbumInfo is one album of an artist with its tracks, as the catalog lists them.
type AlbumInfo struct {
	ID     int64
	Name   string
	Year   int
	Tracks []parse.CatalogTrack
}

// AlbumSource is an optional extra of a TitleSource: the albums and tracks of an
// artist. With it a scanned music file is matched to its track (files.track_id)
// as well as to its artist.
type AlbumSource interface {
	Albums(ctx context.Context, titleID int64) ([]AlbumInfo, error)
}

// Folder is one watched folder.
type Folder struct {
	ID        int64     `json:"id"`
	Kind      string    `json:"kind"`
	Path      string    `json:"path"`
	Label     string    `json:"label"`
	CreatedAt time.Time `json:"createdAt"`
}

// FolderCounts is what the scanner knows about the files of one folder.
// Missing files are not counted in the other numbers.
type FolderCounts struct {
	Files     int   `json:"files"`
	Bytes     int64 `json:"bytes"`
	Matched   int   `json:"matched"`
	Unmatched int   `json:"unmatched"`
	Ignored   int   `json:"ignored"`
	Missing   int   `json:"missing"`
}

// FolderInfo is a folder with its counts, its free disk space and whether a
// scan is running.
type FolderInfo struct {
	Folder
	FolderCounts
	FreeBytes  *uint64 `json:"freeBytes"`  // nil when the disk cannot be read
	TotalBytes *uint64 `json:"totalBytes"` // nil when the disk cannot be read
	Scanning   bool    `json:"scanning"`
}

// File is one media file the scanner found.
type File struct {
	ID           int64           `json:"id"`
	FolderID     int64           `json:"folderId"`
	RelPath      string          `json:"relPath"` // under the folder, always with "/"
	Size         int64           `json:"size"`
	MTime        time.Time       `json:"mtime"`
	Kind         string          `json:"kind"`
	Parsed       json.RawMessage `json:"parsed"`
	Quality      string          `json:"quality"`
	TitleID      *int64          `json:"titleId"`
	EpisodeIDs   []int64         `json:"episodeIds"`
	TrackID      *int64          `json:"trackId"`
	State        string          `json:"state"`
	AddedAt      time.Time       `json:"addedAt"`
	MissingSince *time.Time      `json:"missingSince"`
}

// KindStats are the file counts of one kind.
type KindStats struct {
	Files     int   `json:"files"`
	Bytes     int64 `json:"bytes"`
	Matched   int   `json:"matched"`
	Unmatched int   `json:"unmatched"`
	Ignored   int   `json:"ignored"`
	Missing   int   `json:"missing"`
}

// Stats is the whole library in numbers. Files and Bytes leave out missing files.
type Stats struct {
	Files   int                  `json:"files"`
	Bytes   int64                `json:"bytes"`
	Missing int                  `json:"missing"`
	States  map[string]int       `json:"states"`
	Kinds   map[string]KindStats `json:"kinds"`
}

// FileFilter narrows ListFiles. State is matched, unmatched, ignored (files
// still on disk in that state) or "missing"; empty means every row.
type FileFilter struct {
	FolderID int64
	// TitleID, when not 0, keeps only the files matched to that title.
	TitleID int64
	State   string
	Limit   int
	Offset  int
}

// Options configures a Service.
type Options struct {
	Logger *slog.Logger
	// Hub, when set, gets a "library.scan" event as each scan starts, makes
	// progress and ends.
	Hub *events.Hub
}

// Service is the library.
type Service struct {
	store *Store
	log   *slog.Logger
	hub   *events.Hub
	now   func() time.Time

	ts atomic.Pointer[titleSourceBox]

	// walkSem limits how many folder walks run at once.
	walkSem chan struct{}
	// walk reads one folder; tests replace it.
	walk func(ctx context.Context, root, kind string, progress func(seen int)) (walkResult, error)

	folderMu sync.Mutex // serialises adding folders so the nesting check holds

	mu      sync.Mutex
	running map[int64]bool
	closed  bool
	baseCtx context.Context
	cancel  context.CancelFunc
	wg      sync.WaitGroup
}

type titleSourceBox struct{ ts TitleSource }

// NewService builds the library on the main database.
func NewService(d *sql.DB, o Options) *Service {
	log := o.Logger
	if log == nil {
		log = slog.Default()
	}
	ctx, cancel := context.WithCancel(context.Background())
	return &Service{
		store:   NewStore(d),
		log:     log,
		hub:     o.Hub,
		now:     time.Now,
		walkSem: make(chan struct{}, maxWalks),
		walk:    walkFolder,
		running: map[int64]bool{},
		baseCtx: ctx,
		cancel:  cancel,
	}
}

// SetTitleSource wires in the titles service. Until then scans still record
// every file, as unmatched.
func (s *Service) SetTitleSource(ts TitleSource) {
	if ts == nil {
		s.ts.Store(nil)
		return
	}
	s.ts.Store(&titleSourceBox{ts: ts})
}

func (s *Service) titleSource() TitleSource {
	if b := s.ts.Load(); b != nil {
		return b.ts
	}
	return nil
}

// Close stops background scans and waits for them to end.
func (s *Service) Close() {
	s.mu.Lock()
	s.closed = true
	s.mu.Unlock()
	s.cancel()
	s.wg.Wait()
}

// ---- folders -----------------------------------------------------------------

func validKind(k string) bool { return k == KindMovie || k == KindShow || k == KindMusic }

// AddFolder puts a folder in the library. The path must be absolute and exist,
// and must not be inside another library folder or contain one. The files in
// it are not scanned until the next scan.
func (s *Service) AddFolder(ctx context.Context, kind, path, label string) (Folder, error) {
	if !validKind(kind) {
		return Folder{}, ErrInvalidKind
	}
	clean, err := checkDir(path)
	if err != nil {
		return Folder{}, err
	}
	label = strings.TrimSpace(label)
	if label == "" {
		label = filepath.Base(clean)
		if label == "" || label == string(filepath.Separator) || label == "." {
			label = clean
		}
	}
	if utf8.RuneCountInString(label) > maxLabelRunes {
		label = string([]rune(label)[:maxLabelRunes])
	}

	s.folderMu.Lock()
	defer s.folderMu.Unlock()
	existing, err := s.store.ListFolders(ctx)
	if err != nil {
		return Folder{}, err
	}
	if err := checkNesting(clean, existing); err != nil {
		return Folder{}, err
	}
	return s.store.InsertFolder(ctx, kind, clean, label, s.now())
}

// RemoveFolder forgets a folder and its file rows. The files on disk stay.
func (s *Service) RemoveFolder(ctx context.Context, id int64) error {
	return s.store.DeleteFolder(ctx, id)
}

// Folder returns one folder.
func (s *Service) Folder(ctx context.Context, id int64) (Folder, error) {
	return s.store.GetFolder(ctx, id)
}

// ListFolders lists every folder, oldest first, without counts or disk space. It
// is the cheap way to find a folder by kind or path.
func (s *Service) ListFolders(ctx context.Context) ([]Folder, error) {
	return s.store.ListFolders(ctx)
}

// Folders lists every folder with its counts and free disk space.
func (s *Service) Folders(ctx context.Context) ([]FolderInfo, error) {
	folders, err := s.store.ListFolders(ctx)
	if err != nil {
		return nil, err
	}
	counts, err := s.store.FolderCounts(ctx)
	if err != nil {
		return nil, err
	}
	out := make([]FolderInfo, 0, len(folders))
	for _, f := range folders {
		info := FolderInfo{Folder: f, FolderCounts: counts[f.ID], Scanning: s.isRunning(f.ID)}
		if sp, err := DiskSpace(f.Path); err == nil {
			free, total := sp.Free, sp.Total
			info.FreeBytes, info.TotalBytes = &free, &total
		}
		out = append(out, info)
	}
	return out, nil
}

// FolderInfo returns one folder with its counts and free disk space.
func (s *Service) FolderInfo(ctx context.Context, id int64) (FolderInfo, error) {
	infos, err := s.Folders(ctx)
	if err != nil {
		return FolderInfo{}, err
	}
	for _, info := range infos {
		if info.ID == id {
			return info, nil
		}
	}
	return FolderInfo{}, ErrNotFound
}

// Files lists file rows and the total number matching the filter.
func (s *Service) Files(ctx context.Context, f FileFilter) ([]File, int, error) {
	return s.store.ListFiles(ctx, f)
}

// Stats counts the whole library.
func (s *Service) Stats(ctx context.Context) (Stats, error) {
	return s.store.Stats(ctx)
}

func (s *Service) isRunning(id int64) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.running[id]
}

// ---- path checks -------------------------------------------------------------

// checkDir validates a folder path and returns it cleaned.
func checkDir(path string) (string, error) {
	path = strings.TrimSpace(path)
	if path == "" || !filepath.IsAbs(path) {
		return "", ErrInvalidPath
	}
	clean := filepath.Clean(path)
	st, err := os.Stat(clean)
	switch {
	case errors.Is(err, os.ErrNotExist):
		return "", ErrPathNotFound
	case err != nil:
		return "", fmt.Errorf("library: reading %s: %w", clean, err)
	case !st.IsDir():
		return "", ErrNotDirectory
	}
	return clean, nil
}

// pathForms is a path as written plus, when it differs, with symlinks resolved,
// so a folder reached through a link still counts as the folder it points to.
func pathForms(p string) []string {
	forms := []string{filepath.Clean(p)}
	if r, err := filepath.EvalSymlinks(p); err == nil && filepath.Clean(r) != forms[0] {
		forms = append(forms, filepath.Clean(r))
	}
	return forms
}

// within reports whether child is parent itself or somewhere below it.
func within(parent, child string) bool {
	rel, err := filepath.Rel(parent, child)
	if err != nil {
		return false
	}
	return rel == "." || (rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator)))
}

func checkNesting(path string, existing []Folder) error {
	mine := pathForms(path)
	for _, f := range existing {
		for _, theirs := range pathForms(f.Path) {
			for _, m := range mine {
				switch {
				case within(theirs, m) && within(m, theirs):
					return ErrDuplicate
				case within(theirs, m):
					return ErrNested
				case within(m, theirs):
					return ErrContainsFolder
				}
			}
		}
	}
	return nil
}
