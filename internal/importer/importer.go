// Package importer takes a finished download and makes it part of the library.
//
// When a download client says a download is done, the importer finds the files,
// checks each one is really the title (and, for a show, which episodes), unpacks
// rar, 7z and zip archives when that is all there is, names the files the way
// the library wants, and puts them in the copy's folder: a hardlink for a
// torrent (which has to keep seeding, so its files are never moved), a move for
// Usenet. It records the new files in the library, marks the request available
// (or partial), tells the media servers, and writes the title's history.
//
// A release that turns out bad (a fake, a sample, the wrong film) is blocklisted
// with the reason, the download is marked failed, and the copy is searched again
// so the next best release is tried. A problem that is not the release's fault
// (the library folder is not mounted, the disk is full) fails the import without
// blocklisting; the job tries once more later.
//
// SQL lives in store.go. The importer meets the other modules through the small
// interfaces below, so each can be replaced in a test.
package importer

import (
	"context"
	"database/sql"
	"errors"
	"io"
	"log/slog"
	"strings"
	"sync"
	"time"

	"github.com/BaileyPillon/marquee/internal/core/events"
	"github.com/BaileyPillon/marquee/internal/decide"
	"github.com/BaileyPillon/marquee/internal/downloads"
	"github.com/BaileyPillon/marquee/internal/grab"
	"github.com/BaileyPillon/marquee/internal/history"
	"github.com/BaileyPillon/marquee/internal/library"
	"github.com/BaileyPillon/marquee/internal/mediaservers"
	"github.com/BaileyPillon/marquee/internal/titles"
)

// Job and event names, timings.
const (
	// JobPending is the scheduler job that picks up finished downloads no one
	// has imported yet (the handler is not told about ones that were already
	// finished when Marquee started) and retries failed imports once.
	JobPending      = "import.pending"
	PendingInterval = 2 * time.Minute

	// LibraryTopic is the SSE topic that says new files arrived.
	LibraryTopic = "library"

	// FailPrefix starts the error text of a download whose import failed.
	FailPrefix = "Import failed: "

	startupDelay      = 5 * time.Second
	retryWindow       = 24 * time.Hour
	retryAfter        = time.Minute
	staleImporting    = 30 * time.Minute
	defaultSearchWait = 20 * time.Second
	maxSearchesPerDay = 5
	maxReasonRunes    = 280
)

// Outcomes of one import.
const (
	OutcomeRunning  = "running"
	OutcomeImported = "imported"
	OutcomeSkipped  = "skipped" // nothing to add: the library already had it
	OutcomeFailed   = "failed"
)

// Errors the web layer maps to HTTP statuses.
var (
	// ErrWrongState: the download is not in a state an import can start from.
	ErrWrongState = errors.New("importer: that download cannot be imported in its current state")
	// ErrBusy: the download is being imported right now.
	ErrBusy = errors.New("importer: that download is being imported right now")
	// ErrClosed: the importer has been shut down.
	ErrClosed = errors.New("importer: closed")
)

// ---- what the importer needs from the other modules -----------------------------

// Downloads is the downloads service.
type Downloads interface {
	Download(ctx context.Context, id int64) (downloads.Download, error)
	SetState(ctx context.Context, id int64, state, errText string) error
	Remove(ctx context.Context, id int64, deleteData bool) error
	ClientPaths(ctx context.Context, clientID int64) (downloads.ClientPaths, error)
	List(ctx context.Context, f downloads.ListFilter) ([]downloads.View, int, error)
}

// Catalog is the titles service.
type Catalog interface {
	Get(ctx context.Context, id int64) (*titles.Detail, error)
}

// Library is the library service.
type Library interface {
	Folder(ctx context.Context, id int64) (library.Folder, error)
	Files(ctx context.Context, f library.FileFilter) ([]library.File, int, error)
	ImportFiles(ctx context.Context, folderID int64, files []library.ImportedFile) error
	ForgetFiles(ctx context.Context, folderID int64, relPaths []string) error
}

// Profiles is the decide service's quality profiles.
type Profiles interface {
	Profile(ctx context.Context, kind, quality string) (decide.Profile, error)
}

// Blocklist is the decide service's blocklist.
type Blocklist interface {
	Block(ctx context.Context, e decide.BlockEntry) (decide.BlockEntry, error)
}

// Requests is the requests service.
type Requests interface {
	// SettleCopy moves the requests behind a copy to available or partial.
	SettleCopy(ctx context.Context, copyID int64) (string, error)
}

// Servers is the media-servers service.
type Servers interface {
	Notify(ctx context.Context, ev mediaservers.Event) []mediaservers.NotifyResult
}

// Searcher is the search service: look for a copy again, and forget what was
// sent for a bad release so the look is not skipped as "just grabbed".
type Searcher interface {
	grab.Searcher
	ForgetGrab(ctx context.Context, copyID int64, keys []string) error
}

// Deps are the other modules. Downloads, Copies, Titles and Library are needed;
// the rest may be nil (the importer then skips that step).
type Deps struct {
	Downloads Downloads
	Copies    grab.CopySource
	Titles    Catalog
	Library   Library
	Profiles  Profiles
	Blocklist Blocklist
	Requests  Requests
	Servers   Servers
	Search    Searcher
	History   history.Recorder
}

// Options configure the service.
type Options struct {
	Logger *slog.Logger
	// Hub gets a "library" event when files arrive.
	Hub *events.Hub
	Now func() time.Time
	// SearchDelay is how long a failed import waits before the copy is searched
	// again (default 20 s). Failures close together share one search.
	SearchDelay time.Duration
}

// Service is the importer.
type Service struct {
	store *Store
	deps  Deps
	log   *slog.Logger
	hub   *events.Hub
	now   func() time.Time

	searchDelay time.Duration

	baseCtx context.Context
	cancel  context.CancelFunc

	mu       sync.Mutex
	inflight map[int64]bool
	closed   bool
	firstRun bool
	wg       sync.WaitGroup
	work     sync.Mutex // one import at a time: they are all disk work

	searchMu     sync.Mutex
	searchTimers map[int64]*time.Timer
	searchLog    map[int64][]time.Time
}

var _ grab.CompletedHandler = (*Service)(nil)

// New builds the importer on the main database.
func New(d *sql.DB, deps Deps, o Options) *Service {
	if o.Logger == nil {
		o.Logger = slog.New(slog.NewTextHandler(io.Discard, nil))
	}
	if o.Now == nil {
		o.Now = time.Now
	}
	if o.SearchDelay <= 0 {
		o.SearchDelay = defaultSearchWait
	}
	ctx, cancel := context.WithCancel(context.Background())
	return &Service{
		store: NewStore(d), deps: deps, log: o.Logger, hub: o.Hub, now: o.Now, searchDelay: o.SearchDelay,
		baseCtx: ctx, cancel: cancel,
		inflight: map[int64]bool{}, firstRun: true,
		searchTimers: map[int64]*time.Timer{}, searchLog: map[int64][]time.Time{},
	}
}

// Close stops starting imports, cancels the ones running and waits for them.
func (s *Service) Close() {
	s.mu.Lock()
	s.closed = true
	s.mu.Unlock()
	s.cancel()
	s.wg.Wait()
	s.searchMu.Lock()
	for id, t := range s.searchTimers {
		t.Stop()
		delete(s.searchTimers, id)
	}
	s.searchMu.Unlock()
}

// Wait blocks until every import that was started has ended (tests use it).
func (s *Service) Wait() { s.wg.Wait() }

// StartupPass runs one Pending pass a few seconds from now, in the background,
// so downloads that finished while Marquee was off (or before the importer was
// wired in) are imported without waiting for the job's first tick.
func (s *Service) StartupPass() {
	s.wg.Add(1)
	go func() {
		defer s.wg.Done()
		select {
		case <-time.After(startupDelay):
		case <-s.baseCtx.Done():
			return
		}
		if err := s.Pending(s.baseCtx); err != nil && s.baseCtx.Err() == nil {
			s.log.Warn("importer: the startup pass failed", "err", err)
		}
	}()
}

// ---- starting an import ---------------------------------------------------------

// OnCompleted implements grab.CompletedHandler: the download client says a
// download is done. It returns at once; the import runs in the background, one
// at a time. (The downloads poll calls this while it holds its own lock, so it
// must never wait for disk work.)
func (s *Service) OnCompleted(_ context.Context, downloadID int64) {
	s.enqueue(downloadID, false, false)
}

// enqueue starts an import in the background. retry lets it start from a
// failed or abandoned state, not just a completed one; manual records that the
// owner asked. It reports whether anything was started (false: already
// running, or closed).
func (s *Service) enqueue(id int64, retry, manual bool) bool {
	s.mu.Lock()
	if s.closed || s.inflight[id] {
		s.mu.Unlock()
		return false
	}
	s.inflight[id] = true
	s.wg.Add(1)
	s.mu.Unlock()
	go func() {
		defer s.wg.Done()
		defer func() {
			s.mu.Lock()
			delete(s.inflight, id)
			s.mu.Unlock()
		}()
		defer func() {
			if r := recover(); r != nil {
				s.log.Error("the importer panicked", "download", id, "panic", r)
			}
		}()
		s.work.Lock()
		defer s.work.Unlock()
		if _, err := s.process(s.baseCtx, id, retry, manual); err != nil && !errors.Is(err, ErrWrongState) && s.baseCtx.Err() == nil {
			s.log.Warn("import did not run", "download", id, "err", err)
		}
	}()
	return true
}

// Retry is the owner's "import this again": a completed, failed or abandoned
// import starts again in the background. A download already imported, or one
// that is still being fetched, is refused.
func (s *Service) Retry(ctx context.Context, id int64) error {
	d, err := s.deps.Downloads.Download(ctx, id)
	if err != nil {
		return err
	}
	switch d.State {
	case downloads.StateCompleted, downloads.StateFailed, downloads.StateImporting:
	default:
		return ErrWrongState
	}
	s.mu.Lock()
	closed, busy := s.closed, s.inflight[id]
	s.mu.Unlock()
	switch {
	case closed:
		return ErrClosed
	case busy:
		return ErrBusy
	}
	if !s.enqueue(id, true, true) {
		return ErrBusy
	}
	return nil
}

// ImportNow imports a download and waits for it (tests and the "import now"
// paths use it). It returns the result; ErrBusy if it is already running.
func (s *Service) ImportNow(ctx context.Context, id int64) (Result, error) {
	s.mu.Lock()
	if s.closed {
		s.mu.Unlock()
		return Result{}, ErrClosed
	}
	if s.inflight[id] {
		s.mu.Unlock()
		return Result{}, ErrBusy
	}
	s.inflight[id] = true
	s.mu.Unlock()
	defer func() {
		s.mu.Lock()
		delete(s.inflight, id)
		s.mu.Unlock()
	}()
	s.work.Lock()
	defer s.work.Unlock()
	return s.process(ctx, id, true, true)
}

// ---- the job --------------------------------------------------------------------

// Pending is the "import.pending" job. It starts an import for every download
// that finished in its client and has not been imported (the downloads module
// only tells the importer about downloads that finish while Marquee runs, so
// ones that finished while it was off, or before the importer was wired in, are
// found here), for importing runs that were left behind, and, once, for
// imports that failed for a reason that was not the release's fault within the
// last day.
func (s *Service) Pending(ctx context.Context) error {
	s.mu.Lock()
	first := s.firstRun
	s.firstRun = false
	s.mu.Unlock()
	if first {
		// Nothing is running at startup, so a "running" row is a leftover.
		if err := s.store.interrupted(ctx, s.now()); err != nil {
			s.log.Warn("importer: could not tidy old imports", "err", err)
		}
	}
	now := s.now()
	started := 0

	for off := 0; ; off += downloads.MaxLimit {
		views, total, err := s.deps.Downloads.List(ctx, downloads.ListFilter{State: downloads.FilterActive, Limit: downloads.MaxLimit, Offset: off})
		if err != nil {
			return err
		}
		for _, v := range views {
			switch v.State {
			case downloads.StateCompleted:
				if s.enqueue(v.ID, false, false) {
					started++
				}
			case downloads.StateImporting:
				if (first || now.Sub(v.UpdatedAt) > staleImporting) && s.enqueue(v.ID, true, false) {
					started++
				}
			}
		}
		if len(views) == 0 || off+len(views) >= total {
			break
		}
	}

	cutoff := now.Add(-retryWindow)
retry:
	for off := 0; ; off += downloads.MaxLimit {
		views, total, err := s.deps.Downloads.List(ctx, downloads.ListFilter{State: downloads.FilterHistory, Limit: downloads.MaxLimit, Offset: off})
		if err != nil {
			return err
		}
		for _, v := range views {
			if v.UpdatedAt.Before(cutoff) {
				break retry // newest first: everything after is older
			}
			if v.State != downloads.StateFailed || !strings.HasPrefix(v.Error, FailPrefix) || now.Sub(v.UpdatedAt) < retryAfter {
				continue
			}
			tries, retryable, err := s.store.attempts(ctx, v.ID)
			if err != nil {
				return err
			}
			if tries == 1 && retryable && s.enqueue(v.ID, true, false) {
				started++
			}
		}
		if len(views) == 0 || off+len(views) >= total {
			break
		}
	}
	if started > 0 {
		s.log.Info("importer picked up downloads", "count", started)
	}
	return nil
}

// ---- reading ---------------------------------------------------------------------

// Status returns the latest imports, newest first.
func (s *Service) Status(ctx context.Context, limit int) ([]Record, error) {
	if limit <= 0 || limit > 200 {
		limit = 20
	}
	return s.store.recent(ctx, limit)
}

// Running lists the downloads being imported right now.
func (s *Service) Running() []int64 {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]int64, 0, len(s.inflight))
	for id := range s.inflight {
		out = append(out, id)
	}
	return out
}
