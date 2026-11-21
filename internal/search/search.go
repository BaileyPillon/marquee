// Package search finds releases for what the library is missing and sends the
// best acceptable one to be downloaded. It sits on top of sources (which fetch
// and parse feeds) and decide (which judges releases), and meets the requests
// and downloads modules only through the small interfaces in the grab package:
// a CopySource says what is wanted, a Grabber takes the pick.
//
// Three things start a search: a person asking for the releases of a title
// (Interactive, then GrabRelease), the six-hourly SearchMissing job, and new
// items showing up in the feeds (MatchNew). All three go through the same
// scoring, so the reasons a person sees are the reasons the machine used.
package search

import (
	"context"
	"database/sql"
	"errors"
	"io"
	"log/slog"
	"sync"
	"sync/atomic"
	"time"

	"github.com/BaileyPillon/marquee/internal/decide"
	"github.com/BaileyPillon/marquee/internal/grab"
	"github.com/BaileyPillon/marquee/internal/parse"
	"github.com/BaileyPillon/marquee/internal/sources"
	"github.com/BaileyPillon/marquee/internal/titles"
)

// Job names and timings.
const (
	MissingJob      = "search.missing"
	MissingInterval = 6 * time.Hour

	// skipIfSearchedWithin is a little under the job's interval: the job runs
	// every six hours, and a copy it searched at the end of the last run must
	// not be skipped at the start of this one.
	skipIfSearchedWithin = MissingInterval - 30*time.Minute
	// pace is the wait between two copies in one run.
	defaultPace = 2 * time.Second
	// grabMemory is how long a release sent for download keeps the same
	// episode (or film) from being sent again.
	grabMemory = 24 * time.Hour
	// historyKeep is how long search_history is kept.
	historyKeep = 60 * 24 * time.Hour
	// maxGrabsPerRun bounds one search of one copy.
	maxGrabsPerRun = 25
	// interactiveKeep is how long a manual search's results can still be grabbed.
	interactiveKeep = 10 * time.Minute
)

// Errors the web layer maps to HTTP statuses.
var (
	// ErrNoGrabber means no download client is set up to take a release.
	ErrNoGrabber = errors.New("search: no download client is set up")
	// ErrNoCopy means the title has no wanted copy of that quality to put the
	// download against.
	ErrNoCopy = errors.New("search: the title has no copy of that quality")
	// ErrReleaseGone means a release to grab is no longer remembered.
	ErrReleaseGone = errors.New("search: that release is no longer available")
	// ErrNoSources means no source is set up.
	ErrNoSources = errors.New("search: no sources are set up")
)

// InvalidError is a request the caller can fix; Message is plain words.
type InvalidError struct{ Message string }

func (e *InvalidError) Error() string { return e.Message }

func invalid(msg string) error { return &InvalidError{Message: msg} }

// Titles is the part of the catalog the search needs.
type Titles interface {
	Get(ctx context.Context, id int64) (*titles.Detail, error)
}

// Have is what a copy already holds.
type Have struct {
	Movie      bool    // the film is in the copy's folder
	EpisodeIDs []int64 // episodes that have a file in the copy's folder
}

// Shelf says what a copy already has, so a search only asks for the rest. The
// wiring builds one from the library; without one, everything monitored (and
// already aired) counts as missing.
type Shelf interface {
	Have(ctx context.Context, c grab.Copy) (Have, error)
}

// CopyFinder is an optional extra for the CopySource: look a title's copy up by
// quality. The manual grab needs it; without it the wanted copies are scanned.
type CopyFinder interface {
	FindCopy(ctx context.Context, titleID int64, quality string) (grab.Copy, bool, error)
}

// CopyEnsurer is a third optional extra: make sure a title has a copy of a
// quality, creating it if it does not (the requests module can do this). A
// manual grab uses it, so picking a 4K release for a title that only has an HD
// copy so far starts the 4K copy.
type CopyEnsurer interface {
	EnsureCopy(ctx context.Context, titleID int64, quality string) (grab.Copy, error)
}

// Scoper is another optional extra for the CopySource: a copy of a show may
// have been asked for by season. When the CopySource implements it, a search
// only looks for the seasons it names (all=true means every season).
type Scoper interface {
	CopySeasons(ctx context.Context, c grab.Copy) (seasons []int, all bool, err error)
}

// Options configure the service.
type Options struct {
	Logger *slog.Logger
	Now    func() time.Time
	// Pace is the wait between two copies in the scheduled run (default 2s; a
	// negative value means none, for tests).
	Pace time.Duration
}

// Service searches, ranks and grabs.
type Service struct {
	store  *Store
	src    *sources.Service
	dec    *decide.Service
	titles Titles
	log    *slog.Logger
	now    func() time.Time
	pace   time.Duration

	copies  atomic.Pointer[copyBox]
	grabber atomic.Pointer[grabBox]
	shelf   atomic.Pointer[shelfBox]

	locks sync.Map // copy id -> *sync.Mutex: one decision about a copy at a time

	cacheMu sync.Mutex
	cache   map[int64]map[string]cached // title id -> release key -> release
}

type (
	copyBox  struct{ c grab.CopySource }
	grabBox  struct{ g grab.Grabber }
	shelfBox struct{ s Shelf }
	cached   struct {
		item sources.Item
		name string // the source's name
		at   time.Time
	}
)

// New builds the service on the main database.
func New(d *sql.DB, src *sources.Service, dec *decide.Service, tt Titles, o Options) *Service {
	if o.Logger == nil {
		o.Logger = slog.New(slog.NewTextHandler(io.Discard, nil))
	}
	if o.Now == nil {
		o.Now = time.Now
	}
	switch {
	case o.Pace == 0:
		o.Pace = defaultPace
	case o.Pace < 0:
		o.Pace = 0
	}
	return &Service{
		store: NewStore(d), src: src, dec: dec, titles: tt, log: o.Logger, now: o.Now, pace: o.Pace,
		cache: map[int64]map[string]cached{},
	}
}

var (
	_ grab.Searcher   = (*Service)(nil)
	_ sources.Matcher = (*Service)(nil)
)

// SetCopySource wires in the requests module.
func (s *Service) SetCopySource(c grab.CopySource) {
	if c == nil {
		s.copies.Store(nil)
		return
	}
	s.copies.Store(&copyBox{c: c})
}

// SetGrabber wires in the downloads module. Until then searches find and score
// releases but nothing is sent anywhere.
func (s *Service) SetGrabber(g grab.Grabber) {
	if g == nil {
		s.grabber.Store(nil)
		return
	}
	s.grabber.Store(&grabBox{g: g})
}

// SetShelf wires in what the library already holds.
func (s *Service) SetShelf(sh Shelf) {
	if sh == nil {
		s.shelf.Store(nil)
		return
	}
	s.shelf.Store(&shelfBox{s: sh})
}

func (s *Service) copySource() grab.CopySource {
	if b := s.copies.Load(); b != nil {
		return b.c
	}
	return nil
}

func (s *Service) theGrabber() grab.Grabber {
	if b := s.grabber.Load(); b != nil {
		return b.g
	}
	return nil
}

// HasGrabber reports whether a download client is wired in.
func (s *Service) HasGrabber() bool { return s.theGrabber() != nil }

// History returns the latest searches of a copy.
func (s *Service) History(ctx context.Context, copyID int64, limit int) ([]History, error) {
	if limit <= 0 || limit > 200 {
		limit = 20
	}
	return s.store.History(ctx, copyID, limit)
}

// lockCopy serialises decisions about one copy, so the feed matcher and a
// search cannot both send the same episode.
func (s *Service) lockCopy(id int64) func() {
	v, _ := s.locks.LoadOrStore(id, &sync.Mutex{})
	mu := v.(*sync.Mutex)
	mu.Lock()
	return mu.Unlock
}

// ---- scoring a batch ---------------------------------------------------------

// scored is one release with its verdict, still tied to the feed item it came
// from (which holds the download links).
type scored struct {
	item     sources.Item
	source   string // the source's name
	priority int
	parsed   parse.Release
	quality  string // parse.QualityName
	entry    decide.Entry
}

func (sc scored) verdict() decide.Verdict { return sc.entry.Verdict }
func (sc scored) accepted() bool          { return sc.entry.Verdict.Accepted }

// pack reports whether a scored release is a whole-season pack.
func (sc scored) pack() bool { return sc.parsed.FullSeason && len(sc.parsed.Episodes) == 0 }

// judge holds what scoring releases of one title needs, loaded once.
type judge struct {
	title   titles.Title
	profile decide.Profile
	blocks  *decide.BlockIndex
	now     time.Time
}

func (s *Service) newJudge(ctx context.Context, d *titles.Detail, quality string) (judge, error) {
	p, err := s.dec.Profile(ctx, d.Kind, quality)
	if err != nil {
		return judge{}, err
	}
	b, err := s.dec.BlocksFor(ctx, d.ID)
	if err != nil {
		return judge{}, err
	}
	return judge{title: d.Title, profile: p, blocks: b, now: s.now()}, nil
}

func toRelease(it sources.Item, blocks *decide.BlockIndex) decide.Release {
	r := decide.Release{
		IndexerID: it.IndexerID, GUID: it.GUID, Title: it.Title, Protocol: it.Protocol, InfoHash: it.InfoHash,
		Size: it.Size, Seeders: it.Seeders, PublishedAt: it.PublishedAt,
		IMDb: it.IMDb, TMDb: it.TMDb, TVMaze: it.TVMaze, TVDb: it.TVDb,
	}
	if reason, blocked := blocks.Reason(it.GUID, it.InfoHash, it.Title); blocked {
		r.Blocked = reason
	}
	return r
}

// scoreOne judges one release.
func (j judge) scoreOne(it sources.Item, source string, priority int, w decide.Want) scored {
	parsed := parse.ParseRelease(it.Title)
	rel := toRelease(it, j.blocks)
	w.Now = j.now
	v := decide.Score(j.profile, j.title, w, parsed, rel)
	return scored{
		item: it, source: source, priority: priority, parsed: parsed, quality: parse.QualityName(parsed),
		entry: decide.Entry{Release: rel, Parsed: parsed, Verdict: v, Priority: priority},
	}
}

// rank orders scored releases best first (and rewrites their reasons).
func rank(list []scored) []scored {
	entries := make([]decide.Entry, len(list))
	for i := range list {
		entries[i] = list[i].entry
		entries[i].Ref = i
	}
	decide.Rank(entries)
	out := make([]scored, len(list))
	for i, e := range entries {
		sc := list[e.Ref]
		sc.entry = e
		out[i] = sc
	}
	return out
}

// Failure is one source that did not answer.
type Failure struct {
	ID    int64
	Name  string
	Error string
}

// tally counts how the sources did in one or more searches.
type tally struct {
	ok     map[int64]bool
	failed map[int64]Failure
	items  int
}

func newTally() *tally { return &tally{ok: map[int64]bool{}, failed: map[int64]Failure{}} }

func (t *tally) add(outs []sources.Outcome) {
	for _, o := range outs {
		if o.Skipped {
			continue
		}
		if o.Err != nil {
			t.failed[o.Indexer.ID] = Failure{ID: o.Indexer.ID, Name: o.Indexer.Name, Error: sources.Plain(o.Err)}
			continue
		}
		t.ok[o.Indexer.ID] = true
		t.items += len(o.Items)
	}
}

func (t *tally) failures() []Failure {
	out := make([]Failure, 0, len(t.failed))
	for id, f := range t.failed {
		if !t.ok[id] {
			out = append(out, f)
		}
	}
	return out
}

func (t *tally) okCount() int { return len(t.ok) }

// ---- queries ------------------------------------------------------------------

func queryFor(t titles.Title, season, episode int) sources.Query {
	switch t.Kind {
	case titles.KindMovie:
		return sources.Query{Media: sources.MediaMovie, Text: t.Name, Year: t.Year, IMDb: t.IMDbID, TMDb: t.TMDBID}
	case titles.KindShow:
		return sources.Query{Media: sources.MediaShow, Text: t.Name, IMDb: t.IMDbID, TMDb: t.TMDBID,
			TVMaze: t.TVmazeID, Season: season, Episode: episode}
	}
	return sources.Query{Media: sources.MediaArtist, Text: t.Name}
}

// scoreOutcomes scores every item every source returned for one query.
func (s *Service) scoreOutcomes(j judge, outs []sources.Outcome, w decide.Want) []scored {
	var list []scored
	for _, o := range outs {
		if o.Skipped || o.Err != nil {
			continue
		}
		for _, it := range o.Items {
			list = append(list, j.scoreOne(it, o.Indexer.Name, o.Indexer.Priority, w))
		}
	}
	return rank(list)
}
