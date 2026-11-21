package main

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"sync"
	"time"

	"github.com/BaileyPillon/marquee/internal/core/jobs"
	"github.com/BaileyPillon/marquee/internal/core/outbound"
	"github.com/BaileyPillon/marquee/internal/core/secrets"
	"github.com/BaileyPillon/marquee/internal/decide"
	"github.com/BaileyPillon/marquee/internal/downloads"
	"github.com/BaileyPillon/marquee/internal/grab"
	"github.com/BaileyPillon/marquee/internal/library"
	"github.com/BaileyPillon/marquee/internal/requests"
	"github.com/BaileyPillon/marquee/internal/search"
	"github.com/BaileyPillon/marquee/internal/sources"
	"github.com/BaileyPillon/marquee/internal/titles"
)

// The requests module is the CopySource, and can also make a copy on demand
// (a manual grab for a quality the title has no copy of yet). These lines turn
// a change to either signature into a compile error here.
var (
	_ grab.CopySource    = (*requests.Service)(nil)
	_ search.CopyEnsurer = (*requests.Service)(nil)
)

// releaseFinding is the three services that find and pick releases.
type releaseFinding struct {
	Sources *sources.Service
	Decide  *decide.Service
	Search  *search.Service
}

// newReleaseFinding builds the sources, the quality profiles and the search
// service on the main database, and joins them to the rest:
//
//   - copies is who says what is wanted (the requests module, a grab.CopySource);
//   - grabber is who takes the pick (the downloads module);
//   - lib tells the search what is already on disk;
//   - the search service is also the feed matcher the sources hand new items to.
//
// The result's Search is a grab.Searcher: give it to the requests module with
// reqs.SetSearcher(rf.Search) so an approval starts a search.
func newReleaseFinding(mainDB *sql.DB, hc *outbound.Client, box *secrets.Box, cat *titles.Service,
	lib *library.Service, copies grab.CopySource, grabber grab.Grabber, log *slog.Logger) *releaseFinding {
	src := sources.New(sources.NewStore(mainDB), box, hc, sources.Options{Logger: log})
	dec := decide.New(decide.NewStore(mainDB), decide.Options{})
	sr := search.New(mainDB, src, dec, cat, search.Options{Logger: log})
	sr.SetCopySource(copies)
	sr.SetGrabber(noClientAsNoGrabber{grabber})
	sr.SetShelf(&libraryShelf{lib: lib})
	src.SetMatcher(sr)
	return &releaseFinding{Sources: src, Decide: dec, Search: sr}
}

// registerReleaseJobs schedules the feed sync and the missing-release search.
func registerReleaseJobs(sched *jobs.Scheduler, rf *releaseFinding) error {
	if err := sched.Every(sources.RSSJob, sources.RSSInterval, rf.Sources.SyncRSS); err != nil {
		return err
	}
	return sched.Every(search.MissingJob, search.MissingInterval, rf.Search.SearchMissing)
}

// noClientAsNoGrabber turns "there is no download client" from the downloads
// module into the search module's own error, which the API answers with the
// 409 no_grabber the UI keys off.
type noClientAsNoGrabber struct{ grab.Grabber }

func (g noClientAsNoGrabber) Grab(ctx context.Context, t grab.Target, r grab.Release) (int64, error) {
	id, err := g.Grabber.Grab(ctx, t, r)
	if err != nil && errors.Is(err, downloads.ErrNoClient) {
		return 0, fmt.Errorf("%w: %w", search.ErrNoGrabber, err)
	}
	return id, err
}

// ---- what is already on disk -----------------------------------------------------

// libraryShelf reads the library's matched files to say what a copy already has.
// It reads a file the same way the requests module does when it decides a copy
// is wanted: a file belongs to the "uhd" copy when its quality label names 2160p
// and to the "hd" copy otherwise, wherever it sits. The facts are kept for a
// minute, so a run over many copies reads the files once.
type libraryShelf struct {
	lib *library.Service

	mu    sync.Mutex
	at    time.Time
	facts map[int64]*shelfFacts
}

type shelfFacts struct {
	quality  map[string]bool
	episodes map[string]map[int64]bool
}

const (
	shelfTTL  = time.Minute
	shelfPage = 2000
)

func (l *libraryShelf) Have(ctx context.Context, c grab.Copy) (search.Have, error) {
	facts, err := l.load(ctx)
	if err != nil {
		return search.Have{}, err
	}
	var h search.Have
	if f := facts[c.TitleID]; f != nil {
		h.Movie = f.quality[c.Quality]
		for id := range f.episodes[c.Quality] {
			h.EpisodeIDs = append(h.EpisodeIDs, id)
		}
	}
	return h, nil
}

func (l *libraryShelf) load(ctx context.Context) (map[int64]*shelfFacts, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.facts != nil && time.Since(l.at) < shelfTTL {
		return l.facts, nil
	}
	out := map[int64]*shelfFacts{}
	for off := 0; ; off += shelfPage {
		files, total, err := l.lib.Files(ctx, library.FileFilter{State: library.StateMatched, Limit: shelfPage, Offset: off})
		if err != nil {
			return nil, err
		}
		for _, f := range files {
			if f.TitleID == nil || f.MissingSince != nil || f.State != library.StateMatched {
				continue
			}
			q := grab.QualityHD
			if f.Kind != library.KindMusic && strings.Contains(f.Quality, "2160p") {
				q = grab.QualityUHD
			}
			sf := out[*f.TitleID]
			if sf == nil {
				sf = &shelfFacts{quality: map[string]bool{}, episodes: map[string]map[int64]bool{}}
				out[*f.TitleID] = sf
			}
			sf.quality[q] = true
			for _, e := range f.EpisodeIDs {
				if sf.episodes[q] == nil {
					sf.episodes[q] = map[int64]bool{}
				}
				sf.episodes[q][e] = true
			}
		}
		if len(files) == 0 || off+len(files) >= total {
			break
		}
	}
	l.facts, l.at = out, time.Now()
	return out, nil
}
