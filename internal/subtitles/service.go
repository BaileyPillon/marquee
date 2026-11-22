package subtitles

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/BaileyPillon/marquee/internal/core/events"
	"github.com/BaileyPillon/marquee/internal/core/outbound"
	"github.com/BaileyPillon/marquee/internal/library"
	"github.com/BaileyPillon/marquee/internal/parse"
)

// Deps are the other modules the service works with.
type Deps struct {
	Library  Library
	Catalog  Catalog
	Settings Settings
}

// Options configure the service.
type Options struct {
	Logger *slog.Logger
	Now    func() time.Time
	// HTTP is the outbound client every provider call goes through.
	HTTP *outbound.Client
	// Version goes into the User-Agent ("Marquee v1.2.3").
	Version string
	// OpenSubtitlesURL and PodnapisiURL replace the real addresses (tests point
	// them at httptest servers).
	OpenSubtitlesURL string
	PodnapisiURL     string
	// Providers, when set, replaces the built-in providers (tests of the
	// orchestration use fakes).
	Providers func(Config) []Provider
	// Hub gets an event on the "subtitles" topic when a file gains or loses a
	// subtitle.
	Hub *events.Hub
}

// Service finds, downloads and places subtitles.
type Service struct {
	store    *Store
	deps     Deps
	settings Settings
	log      *slog.Logger
	now      func() time.Time
	hub      *events.Hub
	version  string
	hc       *outbound.Client
	osURL    string

	opensub *OpenSubtitles
	podna   *Podnapisi
	custom  func(Config) []Provider

	// work serialises the automated passes: they are all network and disk work.
	work sync.Mutex

	mu       sync.Mutex
	states   map[string]*provState
	lastPass time.Time
	found    map[foundKey]foundEntry // manual searches, so a result can be downloaded by id
	missing  struct {
		at time.Time
		n  int
	}
	notify func(ctx context.Context, paths []string)
}

// New builds the service on the main database.
func New(d *sql.DB, deps Deps, o Options) *Service {
	if o.Logger == nil {
		o.Logger = slog.New(slog.NewTextHandler(io.Discard, nil))
	}
	if o.Now == nil {
		o.Now = time.Now
	}
	s := &Service{
		store: NewStore(d), deps: deps, settings: deps.Settings, log: o.Logger, now: o.Now, hub: o.Hub,
		version: o.Version, hc: o.HTTP, osURL: o.OpenSubtitlesURL, custom: o.Providers,
		states: map[string]*provState{}, found: map[foundKey]foundEntry{},
	}
	if o.HTTP != nil {
		s.opensub = NewOpenSubtitles(o.HTTP, o.OpenSubtitlesURL, o.Version)
		s.opensub.now = o.Now
		s.podna = NewPodnapisi(o.HTTP, o.PodnapisiURL, o.Version)
	}
	return s
}

// SetNotifier sets what is told of a new subtitle file (the media servers, so
// they pick it up). Nil turns it off.
func (s *Service) SetNotifier(f func(ctx context.Context, paths []string)) {
	s.mu.Lock()
	s.notify = f
	s.mu.Unlock()
}

// ---- providers and what is known of them ------------------------------------------

// provState is what Marquee remembers of one provider between calls.
type provState struct {
	remaining    *int      // downloads left today, from the last download
	resetAt      time.Time // when the quota comes back
	blockedUntil time.Time // do not call before this (rate limit, no quota)
	lastErr      string    // the last failure, "" after a success
}

func (s *Service) state(name string) *provState {
	st := s.states[name]
	if st == nil {
		st = &provState{}
		s.states[name] = st
	}
	return st
}

// providers builds the providers that are turned on and set up.
func (s *Service) providers(cfg Config) []Provider {
	if s.custom != nil {
		return s.custom(cfg)
	}
	var out []Provider
	if s.opensub != nil && cfg.openSubtitlesReady() {
		s.opensub.Configure(cfg.Providers.OpenSubtitles)
		out = append(out, s.opensub)
	}
	if s.podna != nil && cfg.Providers.Podnapisi.Enabled {
		out = append(out, s.podna)
	}
	return out
}

// blocked reports whether a provider is to be left alone for now (it asked to be
// slowed down, or its quota is used up).
func (s *Service) blocked(name string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	st := s.states[name]
	return st != nil && s.now().Before(st.blockedUntil)
}

// anyUsable reports whether at least one provider is not blocked.
func (s *Service) anyUsable(provs []Provider) bool {
	for _, p := range provs {
		if !s.blocked(p.Name()) {
			return true
		}
	}
	return false
}

// note records the outcome of a provider call: a failure (which may block the
// provider for a while) or a success.
func (s *Service) note(name string, err error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	st := s.state(name)
	if err == nil {
		st.lastErr = ""
		return
	}
	st.lastErr = err.Error()
	var (
		rl *RateLimitError
		q  *QuotaError
	)
	switch {
	case errors.As(err, &rl):
		wait := rl.RetryAfter
		if wait <= 0 {
			wait = 30 * time.Second
		}
		st.blockedUntil = s.now().Add(wait)
	case errors.As(err, &q):
		until := q.ResetAt
		if until.IsZero() || !until.After(s.now()) {
			until = s.now().Add(6 * time.Hour)
		}
		st.blockedUntil, st.resetAt = until, until
		zero := 0
		st.remaining = &zero
	}
}

// noteQuota remembers what a download said about the quota.
func (s *Service) noteQuota(name string, res DownloadResult) {
	if res.Remaining == nil {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	st := s.state(name)
	n := *res.Remaining
	st.remaining, st.resetAt = &n, res.ResetAt
	if n <= 0 {
		until := res.ResetAt
		if until.IsZero() || !until.After(s.now()) {
			until = s.now().Add(6 * time.Hour)
		}
		st.blockedUntil = until
	}
}

// ---- the video a search is for -----------------------------------------------------

// target is a library file read for a search.
type target struct {
	file   library.File
	folder library.Folder
	abs    string
	video  VideoInfo
}

func (s *Service) loadFile(ctx context.Context, fileID int64) (library.File, library.Folder, string, error) {
	f, err := s.deps.Library.File(ctx, fileID)
	if err != nil {
		if errors.Is(err, library.ErrNotFound) {
			return library.File{}, library.Folder{}, "", ErrNotFound
		}
		return library.File{}, library.Folder{}, "", err
	}
	folder, err := s.deps.Library.Folder(ctx, f.FolderID)
	if err != nil {
		if errors.Is(err, library.ErrNotFound) {
			return library.File{}, library.Folder{}, "", ErrNotFound
		}
		return library.File{}, library.Folder{}, "", err
	}
	return f, folder, filepath.Join(folder.Path, filepath.FromSlash(f.RelPath)), nil
}

func isVideo(f library.File) bool {
	return f.Kind != library.KindMusic && f.TitleID != nil && f.State == library.StateMatched
}

// target reads a file, its title and its hash.
func (s *Service) target(ctx context.Context, fileID int64) (target, error) {
	f, folder, abs, err := s.loadFile(ctx, fileID)
	if err != nil {
		return target{}, err
	}
	if !isVideo(f) {
		return target{}, ErrNotVideo
	}
	if st, err := os.Stat(abs); err != nil || !st.Mode().IsRegular() {
		return target{}, ErrFileMissing
	}
	d, err := s.deps.Catalog.Get(ctx, *f.TitleID)
	if err != nil {
		return target{}, err
	}
	v := VideoInfo{
		FileID: f.ID, Path: abs, FileName: filepath.Base(abs), Kind: f.Kind, Title: d.Name, Year: d.Year,
		IMDb: d.IMDbID, TMDb: d.TMDBID, Parsed: parse.ParseRelease(filepath.Base(abs)),
	}
	if f.Kind == library.KindShow {
		if len(f.EpisodeIDs) == 0 {
			return target{}, invalid("Marquee does not know which episode this file is, so it cannot look for its subtitle.")
		}
		for _, e := range d.Episodes {
			if e.ID == f.EpisodeIDs[0] {
				v.Season, v.Episode = e.Season, e.Number
			}
		}
		if v.Episode == 0 {
			return target{}, invalid("The episode this file is has gone from the catalog.")
		}
	}
	// A hash that cannot be read (a tiny or unreadable file) only costs the exact match.
	if h, size, err := HashFile(abs); err == nil {
		v.Hash, v.Size = h, size
	} else {
		v.Size = size
	}
	return target{file: f, folder: folder, abs: abs, video: v}, nil
}

// ---- searching the providers ----------------------------------------------------------

// ProviderOutcome says how one provider did in a search.
type ProviderOutcome struct {
	Name  string `json:"name"`
	OK    bool   `json:"ok"`
	Error string `json:"error,omitempty"`
	// Skipped: it was not asked (it is waiting out a rate limit or its quota).
	Skipped bool `json:"skipped,omitempty"`
}

// searchAll asks every provider that is not blocked, one after another, and
// returns everything they offered and how each one did.
func (s *Service) searchAll(ctx context.Context, provs []Provider, q Query) ([]Candidate, []ProviderOutcome) {
	var (
		all  []Candidate
		outs []ProviderOutcome
	)
	for _, p := range provs {
		if err := ctx.Err(); err != nil {
			break
		}
		name := p.Name()
		if s.blocked(name) {
			s.mu.Lock()
			msg := s.state(name).lastErr
			s.mu.Unlock()
			outs = append(outs, ProviderOutcome{Name: name, Skipped: true, Error: msg})
			continue
		}
		list, err := p.Search(ctx, q)
		if ctx.Err() == nil {
			s.note(name, err)
		}
		if err != nil {
			s.log.Warn("subtitles: a provider search failed", "provider", name, "err", err)
			outs = append(outs, ProviderOutcome{Name: name, Error: err.Error()})
			continue
		}
		outs = append(outs, ProviderOutcome{Name: name, OK: true})
		all = append(all, list...)
	}
	return all, outs
}

func providerByName(provs []Provider, name string) Provider {
	for _, p := range provs {
		if p.Name() == name {
			return p
		}
	}
	return nil
}

// ---- downloading and placing -----------------------------------------------------------

// fetchAndPlace downloads one subtitle, makes it UTF-8, checks it is a subtitle,
// writes it next to the video (replacing only a file Marquee wrote) and records it.
func (s *Service) fetchAndPlace(ctx context.Context, p Provider, t target, c Candidate, language string) (Subtitle, error) {
	// A subtitle file that is there and not Marquee's is left alone: no download
	// is spent on it.
	path := SubtitlePath(t.abs, language, c.HI)
	ours, err := s.store.OwnsPath(ctx, path)
	if err != nil {
		return Subtitle{}, err
	}
	if _, err := os.Lstat(path); err == nil && !ours {
		return Subtitle{}, ErrForeignFile
	}
	res, err := p.Download(ctx, c)
	if ctx.Err() == nil {
		s.note(p.Name(), err)
	}
	if err != nil {
		return Subtitle{}, err
	}
	s.noteQuota(p.Name(), res)
	data := ToUTF8(res.Data)
	if err := CheckSubtitle(data); err != nil {
		return Subtitle{}, err
	}
	if err := WriteSubtitle(path, data, ours); err != nil {
		return Subtitle{}, err
	}
	row, err := s.store.Upsert(ctx, Subtitle{
		FileID: t.file.ID, Language: language, HI: c.HI, Provider: c.Provider, ProviderID: c.ProviderID,
		Path: path, Score: c.Score, CreatedAt: s.now(),
	})
	if err != nil {
		// The file is written but not recorded: Marquee would take it for someone
		// else's next time. Take it back out.
		_ = os.Remove(path)
		return Subtitle{}, err
	}
	s.mu.Lock()
	notify := s.notify
	s.missing.at = time.Time{} // the count of files missing subtitles is out of date
	s.mu.Unlock()
	if notify != nil {
		notify(context.WithoutCancel(ctx), []string{path})
	}
	s.publish(t.file.ID, "added")
	s.log.Info("subtitle downloaded", "file", t.file.ID, "language", language, "provider", c.Provider, "score", c.Score)
	return row, nil
}

func (s *Service) publish(fileID int64, event string) {
	if s.hub != nil {
		s.hub.Publish(EventTopic, map[string]any{"fileId": fileID, "event": event})
	}
}

// ---- looking and downloading on its own -----------------------------------------------

// autoFetch looks for the languages of one file in every provider and downloads
// the best result of each that scores at least the minimum, recording the look
// in subtitle_searches. It returns how many languages got a subtitle.
func (s *Service) autoFetch(ctx context.Context, fileID int64, langs []string) (int, error) {
	cfg := s.Config(ctx)
	provs := s.providers(cfg)
	if len(provs) == 0 {
		return 0, ErrNoProvider
	}
	t, err := s.target(ctx, fileID)
	if err != nil {
		return 0, err
	}
	cands, outs := s.searchAll(ctx, provs, Query{Video: t.video, Languages: langs})
	anyOK := false
	for _, o := range outs {
		anyOK = anyOK || o.OK
	}
	ranked := Rank(t.video, cands, langs, cfg.HearingImpaired)

	got := 0
	for _, lang := range langs {
		if err := ctx.Err(); err != nil {
			return got, err
		}
		result := ResultNone
		if !anyOK {
			result = ResultError // nobody answered: nothing is known, and no miss is counted
		} else {
			attempts, failed := 0, false
		pick:
			for _, c := range ranked {
				if c.Language != lang {
					continue
				}
				if c.Score < cfg.MinScore || c.Score == 0 || attempts >= downloadAttempts {
					break
				}
				p := providerByName(provs, c.Provider)
				if p == nil || s.blocked(c.Provider) {
					failed = true
					continue
				}
				attempts++
				_, err := s.fetchAndPlace(ctx, p, t, c, lang)
				switch {
				case err == nil:
					result = ResultFound
					got++
					break pick
				case errors.Is(err, ErrForeignFile):
					// A subtitle file by that name is there already, from someone else:
					// the language is covered; nothing is written.
					result = ResultFound
					break pick
				case errors.Is(err, ErrNotSubtitle):
					s.log.Warn("subtitles: a provider sent something that is not a subtitle", "provider", c.Provider)
				default:
					failed = true
					var q *QuotaError
					if errors.As(err, &q) || ctx.Err() != nil {
						break pick
					}
				}
			}
			if result == ResultNone && failed {
				result = ResultError
			}
		}
		if err := s.store.AddSearch(context.WithoutCancel(ctx), fileID, lang, s.now(), result); err != nil {
			s.log.Warn("subtitles: could not record a search", "file", fileID, "err", err)
		}
	}
	return got, nil
}

// AfterImport is told by the importer which library files it just added. It
// looks for the wanted languages of each, right away (the 1, 3, 7 day backoff
// does not apply to a file that has never been looked at). It does nothing when
// "search after import" is off or no provider is set up. It is meant to be
// called in the background.
func (s *Service) AfterImport(ctx context.Context, fileIDs []int64) {
	cfg := s.Config(ctx)
	if !cfg.SearchAfterImport || len(cfg.Languages) == 0 || len(s.providers(cfg)) == 0 {
		return
	}
	s.work.Lock()
	defer s.work.Unlock()
	cache := newDirCache()
	for _, id := range fileIDs {
		if ctx.Err() != nil {
			return
		}
		f, _, abs, err := s.loadFile(ctx, id)
		if err != nil || !isVideo(f) {
			continue
		}
		langs, err := s.missingLanguages(ctx, f, abs, cfg, cache)
		if err != nil || len(langs) == 0 {
			continue
		}
		if _, err := s.autoFetch(ctx, id, langs); err != nil && ctx.Err() == nil && !errors.Is(err, ErrFileMissing) {
			s.log.Warn("subtitles: the search after an import failed", "file", id, "err", err)
		}
	}
}

// ---- looking by hand -----------------------------------------------------------------

type foundKey struct {
	fileID           int64
	provider, itemID string
}

type foundEntry struct {
	c  Candidate
	at time.Time
}

// SearchResult is the answer to a manual search.
type SearchResult struct {
	Language string            `json:"language"`
	Results  []Candidate       `json:"results"`
	Sources  []ProviderOutcome `json:"providers"`
	MinScore int               `json:"minScore"`
}

// maxManualResults bounds what a manual search lists.
const maxManualResults = 60

// Search looks for subtitles of one language for a file, in every provider that
// is turned on, and lists what it found, best first, with scores. Nothing is
// downloaded. The results can be downloaded for the next 15 minutes.
func (s *Service) Search(ctx context.Context, fileID int64, language string) (SearchResult, error) {
	language, err := cleanLanguage(language)
	if err != nil {
		return SearchResult{}, err
	}
	cfg := s.Config(ctx)
	provs := s.providers(cfg)
	if len(provs) == 0 {
		return SearchResult{}, ErrNoProvider
	}
	t, err := s.target(ctx, fileID)
	if err != nil {
		return SearchResult{}, err
	}
	cands, outs := s.searchAll(ctx, provs, Query{Video: t.video, Languages: []string{language}})
	ranked := Rank(t.video, cands, []string{language}, cfg.HearingImpaired)
	if len(ranked) > maxManualResults {
		ranked = ranked[:maxManualResults]
	}
	s.remember(fileID, ranked)
	if ranked == nil {
		ranked = []Candidate{}
	}
	return SearchResult{Language: language, Results: ranked, Sources: outs, MinScore: cfg.MinScore}, nil
}

func (s *Service) remember(fileID int64, list []Candidate) {
	s.mu.Lock()
	defer s.mu.Unlock()
	now := s.now()
	for k, e := range s.found {
		if now.Sub(e.at) > searchKeep {
			delete(s.found, k)
		}
	}
	for _, c := range list {
		s.found[foundKey{fileID, c.Provider, c.ProviderID}] = foundEntry{c: c, at: now}
	}
}

func (s *Service) recall(fileID int64, provider, itemID string) (Candidate, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	e, ok := s.found[foundKey{fileID, provider, itemID}]
	if !ok || s.now().Sub(e.at) > searchKeep {
		return Candidate{}, false
	}
	return e.c, true
}

// Download fetches the subtitle a person picked from a manual search and writes
// it next to the video. The owner's choice stands whatever its score. A subtitle
// file by that name that Marquee did not write is never replaced (ErrForeignFile).
func (s *Service) Download(ctx context.Context, fileID int64, provider, providerID, language string) (Subtitle, error) {
	language, err := cleanLanguage(language)
	if err != nil {
		return Subtitle{}, err
	}
	if provider == "" || providerID == "" {
		return Subtitle{}, invalid("Say which subtitle: provider and providerId are needed.")
	}
	cfg := s.Config(ctx)
	provs := s.providers(cfg)
	if len(provs) == 0 {
		return Subtitle{}, ErrNoProvider
	}
	p := providerByName(provs, provider)
	if p == nil {
		return Subtitle{}, invalid("%q is not a subtitle source that is turned on.", provider)
	}
	t, err := s.target(ctx, fileID)
	if err != nil {
		return Subtitle{}, err
	}
	c, ok := s.recall(fileID, provider, providerID)
	if !ok {
		// Not from a search Marquee remembers: take it as a plain subtitle.
		c = Candidate{Provider: provider, ProviderID: providerID, Language: language}
	}
	c.Language = language
	row, err := s.fetchAndPlace(ctx, p, t, c, language)
	if err != nil {
		return Subtitle{}, err
	}
	if err := s.store.AddSearch(context.WithoutCancel(ctx), fileID, language, s.now(), ResultFound); err != nil {
		s.log.Warn("subtitles: could not record a download", "file", fileID, "err", err)
	}
	return row, nil
}

func cleanLanguage(code string) (string, error) {
	code = normalizeLanguage(code)
	if !reLanguage.MatchString(code) {
		return "", invalid("Pick a language, for example en or pt-br.")
	}
	return code, nil
}

// Remove deletes a subtitle Marquee wrote: its file and its row. The file is
// only removed if it is still where Marquee put it; a file Marquee did not write
// is never touched, because only rows of Marquee's own writes can be named.
func (s *Service) Remove(ctx context.Context, id int64) error {
	x, err := s.store.Get(ctx, id)
	if err != nil {
		return err
	}
	if err := os.Remove(x.Path); err != nil && !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("the subtitle file could not be removed: %w", err)
	}
	if err := s.store.Delete(ctx, id); err != nil && !errors.Is(err, ErrNotFound) {
		return err
	}
	s.mu.Lock()
	s.missing.at = time.Time{}
	s.mu.Unlock()
	s.publish(x.FileID, "removed")
	return nil
}
