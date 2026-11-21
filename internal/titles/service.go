package titles

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"
	"unicode"

	"github.com/BaileyPillon/marquee/internal/core/db"
	"github.com/BaileyPillon/marquee/internal/metadata"
)

// Providers are the metadata sources the catalog uses. A nil provider means
// that kind cannot be looked up.
type Providers struct {
	Movies  metadata.MovieProvider
	Shows   metadata.ShowProvider
	Artists metadata.ArtistProvider
}

// Options configure the service.
type Options struct {
	Logger *slog.Logger
	Now    func() time.Time
	// RefreshConcurrency is how many titles the scheduled refresh updates at
	// once (default 3).
	RefreshConcurrency int
}

// Service is the catalog.
type Service struct {
	store *Store
	p     Providers
	log   *slog.Logger
	now   func() time.Time
	conc  int
}

// NewService builds the catalog on the main database.
func NewService(d *sql.DB, p Providers, o Options) *Service {
	if o.Logger == nil {
		o.Logger = slog.New(slog.NewTextHandler(io.Discard, nil))
	}
	if o.Now == nil {
		o.Now = time.Now
	}
	if o.RefreshConcurrency <= 0 {
		o.RefreshConcurrency = 3
	}
	return &Service{store: NewStore(d), p: p, log: o.Logger, now: o.Now, conc: o.RefreshConcurrency}
}

// ---- Lookup ----------------------------------------------------------------

var (
	trailingYear = regexp.MustCompile(`^(.*\S)\s+\(?((?:19|20)[0-9]{2})\)?$`)
	imdbShape    = regexp.MustCompile(`^(?i)tt[0-9]{6,10}$`)
)

// splitYear separates "Metropolis 1927" or "Metropolis (1927)" into the name
// and the year.
func splitYear(q string, now time.Time) (string, int) {
	m := trailingYear.FindStringSubmatch(strings.TrimSpace(q))
	if m == nil {
		return q, 0
	}
	y, _ := strconv.Atoi(m[2])
	if y > now.Year()+1 {
		return q, 0
	}
	return strings.TrimSpace(m[1]), y
}

// Lookup asks the provider for candidates. Nothing is saved; each candidate
// says whether it is already in the library.
func (s *Service) Lookup(ctx context.Context, kind, query string) ([]LookupResult, error) {
	if !metadata.ValidKind(kind) {
		return nil, invalid("kind must be movie, show or artist")
	}
	query = strings.TrimSpace(query)
	if query == "" {
		return nil, invalid("Type something to search for.")
	}

	var (
		hits []metadata.SearchHit
		err  error
	)
	switch kind {
	case KindMovie:
		if s.p.Movies == nil {
			return nil, errNoProvider(kind)
		}
		name, year := splitYear(query, s.now())
		hits, err = s.p.Movies.SearchMovies(ctx, name, year)
		if err == nil && len(hits) == 0 && year != 0 {
			// "Blade Runner 2049": the number was part of the name.
			hits, err = s.p.Movies.SearchMovies(ctx, query, 0)
		}
	case KindShow:
		if s.p.Shows == nil {
			return nil, errNoProvider(kind)
		}
		if imdbShape.MatchString(query) {
			var hit *metadata.SearchHit
			hit, err = s.p.Shows.ShowByIMDb(ctx, query)
			switch {
			case err == nil:
				hits = []metadata.SearchHit{*hit}
			case errors.Is(err, metadata.ErrNotFound):
				hits, err = nil, nil
			}
		} else {
			hits, err = s.p.Shows.SearchShows(ctx, query)
		}
	case KindArtist:
		if s.p.Artists == nil {
			return nil, errNoProvider(kind)
		}
		hits, err = s.p.Artists.SearchArtists(ctx, query)
	}
	if err != nil {
		return nil, err
	}
	return s.annotate(ctx, kind, hits)
}

func errNoProvider(kind string) error {
	return &metadata.Error{Code: metadata.ErrUpstream.Code, Message: "No metadata source is set up for " + kind + "s."}
}

// annotate marks the candidates that are already in the library.
func (s *Service) annotate(ctx context.Context, kind string, hits []metadata.SearchHit) ([]LookupResult, error) {
	out := make([]LookupResult, len(hits))
	var (
		ints   []int64
		texts  []string
		column string
	)
	for i, h := range hits {
		out[i].SearchHit = h
		switch kind {
		case KindMovie, KindShow:
			if n, err := strconv.ParseInt(h.ProviderID, 10, 64); err == nil {
				ints = append(ints, n)
			}
		case KindArtist:
			texts = append(texts, strings.ToLower(h.ProviderID))
		}
	}
	switch kind {
	case KindMovie:
		column = "tmdb_id"
	case KindShow:
		column = "tvmaze_id"
	case KindArtist:
		column = "mbid"
	}
	known, err := s.store.q().inLibrary(ctx, kind, column, ints, texts)
	if err != nil {
		return nil, err
	}
	for i := range out {
		key := out[i].ProviderID
		if kind == KindArtist {
			key = strings.ToLower(key)
		}
		if id, ok := known[key]; ok {
			out[i].InLibrary, out[i].TitleID = true, id
		}
	}
	return out, nil
}

// ---- Add -------------------------------------------------------------------

// Add fetches a title's full details and saves everything in one transaction.
// It is idempotent: a title already in the catalog is returned as it is (and
// created is false), without a network call.
func (s *Service) Add(ctx context.Context, kind, provider, providerID string, opts AddOptions) (d *Detail, created bool, err error) {
	if !metadata.ValidKind(kind) {
		return nil, false, invalid("kind must be movie, show or artist")
	}
	if want := metadata.ProviderFor(kind); provider != want {
		return nil, false, invalid("A " + kind + " comes from " + want + ", not " + strconv.Quote(provider) + ".")
	}
	pid, id, err := cleanProviderID(kind, providerID)
	if err != nil {
		return nil, false, err
	}
	opts.RootFolder, err = cleanRootFolder(opts.RootFolder)
	if err != nil {
		return nil, false, err
	}

	if existing, ok, err := s.store.q().findExisting(ctx, kind, id); err != nil {
		return nil, false, err
	} else if ok {
		d, err := s.Get(ctx, existing)
		return d, false, err
	}

	fresh, err := s.fetch(ctx, kind, pid, opts)
	if err != nil {
		return nil, false, err
	}

	var newID int64
	err = s.store.inTx(ctx, func(q queries) error {
		var err error
		newID, err = q.insert(ctx, fresh, s.now())
		return err
	})
	if db.IsUniqueViolation(err) {
		// Added at the same moment by someone else, or the provider gave us an
		// id (the IMDb one, say) that another row already holds.
		if existing, ok, ferr := s.store.q().findExisting(ctx, kind, fresh.ids()); ferr == nil && ok {
			d, err := s.Get(ctx, existing)
			return d, false, err
		}
	}
	if err != nil {
		return nil, false, err
	}
	s.log.Info("title added", "id", newID, "kind", kind, "name", fresh.Name)
	d, err = s.Get(ctx, newID)
	return d, true, err
}

// cleanProviderID checks and normalises an id as the provider writes it.
func cleanProviderID(kind, raw string) (string, ids, error) {
	raw = strings.TrimSpace(raw)
	switch kind {
	case KindMovie, KindShow:
		n, err := strconv.ParseInt(raw, 10, 64)
		if err != nil || n <= 0 {
			return "", ids{}, invalid("That is not a valid " + metadata.ProviderFor(kind) + " id.")
		}
		if kind == KindMovie {
			return raw, ids{TMDB: n}, nil
		}
		return raw, ids{TVmaze: n}, nil
	default:
		raw = strings.ToLower(raw)
		if !metadata.IsMBID(raw) {
			return "", ids{}, invalid("That is not a valid MusicBrainz id.")
		}
		return raw, ids{MBID: raw}, nil
	}
}

func cleanRootFolder(s string) (string, error) {
	s = strings.TrimSpace(s)
	if len(s) > 1024 {
		return "", invalid("That folder path is too long.")
	}
	for _, r := range s {
		if unicode.IsControl(r) {
			return "", invalid("That folder path has a control character in it.")
		}
	}
	return s, nil
}

// fetch gets a title from its provider and shapes it for the catalog.
func (s *Service) fetch(ctx context.Context, kind, providerID string, opts AddOptions) (*Detail, error) {
	switch kind {
	case KindMovie:
		if s.p.Movies == nil {
			return nil, errNoProvider(kind)
		}
		info, err := s.p.Movies.Movie(ctx, providerID)
		if err != nil {
			return nil, err
		}
		return fromMovie(info, opts)
	case KindShow:
		if s.p.Shows == nil {
			return nil, errNoProvider(kind)
		}
		info, err := s.p.Shows.Show(ctx, providerID)
		if err != nil {
			return nil, err
		}
		return fromShow(info, opts)
	default:
		if s.p.Artists == nil {
			return nil, errNoProvider(kind)
		}
		info, err := s.p.Artists.Artist(ctx, providerID)
		if err != nil {
			return nil, err
		}
		return fromArtist(info, opts)
	}
}

// ---- Get, List, change, delete ---------------------------------------------

// Get returns a title with its seasons, episodes, albums, tracks and people.
func (s *Service) Get(ctx context.Context, id int64) (*Detail, error) {
	return s.store.q().detail(ctx, id)
}

// List returns one page of titles.
func (s *Service) List(ctx context.Context, f ListFilter) (*ListResult, error) {
	if f.Kind != "" && !metadata.ValidKind(f.Kind) {
		return nil, invalid("kind must be movie, show or artist")
	}
	switch f.Sort {
	case "":
		f.Sort = SortAdded
	case SortAdded, SortName, SortYear:
	default:
		return nil, invalid("sort must be added, name or year")
	}
	switch {
	case f.Limit <= 0:
		f.Limit = DefaultLimit
	case f.Limit > MaxLimit:
		f.Limit = MaxLimit
	}
	if f.Offset < 0 {
		return nil, invalid("offset cannot be negative")
	}
	f.Q = strings.TrimSpace(f.Q)
	items, total, err := s.store.q().list(ctx, f)
	if err != nil {
		return nil, err
	}
	return &ListResult{Items: items, Total: total, Limit: f.Limit, Offset: f.Offset}, nil
}

// SetMonitored turns monitoring of a title, and of everything under it, on or
// off. Turning it on leaves specials off.
func (s *Service) SetMonitored(ctx context.Context, id int64, monitored bool) error {
	return s.store.inTx(ctx, func(q queries) error { return q.setMonitored(ctx, id, monitored) })
}

// Delete removes a title and its catalog rows. It never touches files.
func (s *Service) Delete(ctx context.Context, id int64) error {
	return s.store.inTx(ctx, func(q queries) error { return q.delete(ctx, id) })
}

// ---- Refresh ---------------------------------------------------------------

// Refresh asks the provider again (skipping the cache) and updates the title.
// What the user set (monitored, folder) stays; new episodes and albums are
// added, and none is ever deleted.
func (s *Service) Refresh(ctx context.Context, id int64) (*Detail, error) {
	t, err := s.store.q().title(ctx, id)
	if err != nil {
		return nil, err
	}
	var providerID string
	switch t.Kind {
	case KindMovie:
		providerID = strconv.FormatInt(t.TMDBID, 10)
	case KindShow:
		providerID = strconv.FormatInt(t.TVmazeID, 10)
	default:
		providerID = t.MBID
	}
	if providerID == "" || providerID == "0" {
		return nil, invalid("This title has no provider id to refresh from.")
	}
	fresh, err := s.fetch(metadata.BypassCache(ctx), t.Kind, providerID, AddOptions{Monitored: t.Monitored, RootFolder: t.RootFolder})
	if err != nil {
		return nil, err
	}
	err = s.store.inTx(ctx, func(q queries) error { return q.refresh(ctx, id, fresh, s.now()) })
	if err != nil {
		return nil, err
	}
	return s.Get(ctx, id)
}

// RefreshMonitored refreshes every monitored title, a few at a time. It is the
// "titles.refresh" job. A title that fails does not stop the others; the job
// reports how many failed.
func (s *Service) RefreshMonitored(ctx context.Context) error {
	ids, err := s.store.q().monitoredIDs(ctx)
	if err != nil {
		if ctx.Err() != nil {
			return nil // shutting down
		}
		return err
	}
	var (
		mu                         sync.Mutex
		wg                         sync.WaitGroup
		refreshed, skipped, failed int
		first                      error
	)
	sem := make(chan struct{}, s.conc)
loop:
	for _, id := range ids {
		select {
		case sem <- struct{}{}:
		case <-ctx.Done():
			break loop
		}
		wg.Add(1)
		go func() {
			defer wg.Done()
			defer func() { <-sem }()
			_, err := s.Refresh(ctx, id)
			mu.Lock()
			defer mu.Unlock()
			switch {
			case err == nil:
				refreshed++
			case errors.Is(err, metadata.ErrTMDBKeyMissing), errors.Is(err, ErrNotFound), ctx.Err() != nil:
				skipped++ // no TMDB key yet, deleted meanwhile, or shutting down
			default:
				failed++
				if first == nil {
					first = fmt.Errorf("title %d: %w", id, err)
				}
				s.log.Warn("title refresh failed", "id", id, "err", err)
			}
		}()
	}
	wg.Wait()
	s.log.Info("titles refreshed", "refreshed", refreshed, "skipped", skipped, "failed", failed)
	if failed > 0 {
		return fmt.Errorf("titles.refresh: %d of %d titles failed; first: %w", failed, len(ids), first)
	}
	return nil
}

// ---- helpers for the library matcher ---------------------------------------

// Candidates lists the titles a file can be matched to. kind "" means every
// kind.
func (s *Service) Candidates(ctx context.Context, kind string) ([]Candidate, error) {
	if kind != "" && !metadata.ValidKind(kind) {
		return nil, invalid("kind must be movie, show or artist")
	}
	return s.store.q().candidates(ctx, kind)
}

// EpisodeID finds the episode of a show by season and number.
func (s *Service) EpisodeID(ctx context.Context, titleID int64, season, number int) (int64, bool, error) {
	return s.store.q().episodeID(ctx, titleID, season, number)
}
