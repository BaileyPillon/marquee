package subtitles

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/BaileyPillon/marquee/internal/library"
	"github.com/BaileyPillon/marquee/internal/settings"
)

// pageSize is how many file rows are read from the library at a time.
const pageSize = 500

// forEachVideo calls fn for every movie and episode file that is matched and on
// disk, with its full path. fn returns true to stop.
func (s *Service) forEachVideo(ctx context.Context, fn func(f library.File, abs string) (stop bool, err error)) error {
	folders := map[int64]library.Folder{}
	for off := 0; ; off += pageSize {
		files, total, err := s.deps.Library.Files(ctx, library.FileFilter{State: library.StateMatched, Limit: pageSize, Offset: off})
		if err != nil {
			return err
		}
		for _, f := range files {
			if err := ctx.Err(); err != nil {
				return err
			}
			if !isVideo(f) || f.MissingSince != nil {
				continue
			}
			folder, ok := folders[f.FolderID]
			if !ok {
				if folder, err = s.deps.Library.Folder(ctx, f.FolderID); err != nil {
					if errors.Is(err, library.ErrNotFound) {
						continue
					}
					return err
				}
				folders[f.FolderID] = folder
			}
			stop, err := fn(f, filepath.Join(folder.Path, filepath.FromSlash(f.RelPath)))
			if err != nil {
				return err
			}
			if stop {
				return nil
			}
		}
		if len(files) == 0 || off+len(files) >= total {
			return nil
		}
	}
}

// due reports whether a file may be looked at again for a language: it has never
// missed, or the wait after its last run of misses is over (1 day after the
// first, 3 after the second, 7 after every one after that).
func (s *Service) due(ctx context.Context, fileID int64, language string) (bool, error) {
	misses, last, err := s.store.SearchState(ctx, fileID, language)
	if err != nil {
		return false, err
	}
	if misses == 0 {
		return true, nil
	}
	wait := backoff[min(misses, len(backoff))-1]
	return !s.now().Before(last.Add(wait)), nil
}

// Missing is one pass over the library: for every video that lacks a wanted
// language and is not waiting out its backoff, look in the providers and
// download what clears the minimum score. It stops early when no provider can
// be asked (their quotas are used up).
func (s *Service) Missing(ctx context.Context) error {
	cfg := s.Config(ctx)
	if len(cfg.Languages) == 0 || len(s.providers(cfg)) == 0 {
		return nil
	}
	s.work.Lock()
	defer s.work.Unlock()
	cache := newDirCache()
	looked, found := 0, 0
	err := s.forEachVideo(ctx, func(f library.File, abs string) (bool, error) {
		provs := s.providers(cfg)
		if !s.anyUsable(provs) || looked >= maxPerPass {
			return true, nil
		}
		langs, err := s.missingLanguages(ctx, f, abs, cfg, cache)
		if err != nil {
			return false, err
		}
		var wanted []string
		for _, l := range langs {
			ok, err := s.due(ctx, f.ID, l)
			if err != nil {
				return false, err
			}
			if ok {
				wanted = append(wanted, l)
			}
		}
		if len(wanted) == 0 {
			return false, nil
		}
		looked++
		n, err := s.autoFetch(ctx, f.ID, wanted)
		found += n
		switch {
		case err == nil:
		case errors.Is(err, ErrFileMissing), errors.Is(err, ErrNotVideo), errors.Is(err, ErrNotFound):
		case ctx.Err() != nil:
			return true, nil
		default:
			var inv *InvalidError
			if !errors.As(err, &inv) {
				s.log.Warn("subtitles: a search failed", "file", f.ID, "err", err)
			}
		}
		return false, nil
	})
	if err != nil && ctx.Err() == nil {
		return err
	}
	if err := s.store.PruneSearches(context.WithoutCancel(ctx), s.now().Add(-120*24*time.Hour)); err != nil {
		s.log.Warn("subtitles: could not forget old searches", "err", err)
	}
	s.log.Info("subtitles searched", "files", looked, "found", found)
	return nil
}

// MissingJob is the "subtitles.missing" job. The scheduler wakes it every hour;
// it runs a pass when the owner's "rescan every N hours" have gone by.
func (s *Service) MissingJob(ctx context.Context) error {
	hours := s.Config(ctx).RescanEveryHours
	s.mu.Lock()
	due := s.lastPass.IsZero() || s.now().Sub(s.lastPass) >= time.Duration(hours)*time.Hour
	if due {
		s.lastPass = s.now()
	}
	s.mu.Unlock()
	if !due {
		return nil
	}
	return s.Missing(ctx)
}

// ---- what the owner sees ----------------------------------------------------------------

// ProviderStatus is one provider's health.
type ProviderStatus struct {
	Name    string `json:"name"`
	Enabled bool   `json:"enabled"`
	// OK: it is turned on, set up, and its last call worked.
	OK bool `json:"ok"`
	// QuotaRemaining is how many downloads are left today, when the provider said.
	QuotaRemaining *int       `json:"quotaRemaining,omitempty"`
	Error          string     `json:"error,omitempty"`
	BlockedUntil   *time.Time `json:"blockedUntil,omitempty"`
}

// Status is the answer to GET /subtitles/status.
type Status struct {
	Providers []ProviderStatus `json:"providers"`
	// MissingCount is how many videos still lack a wanted language.
	MissingCount int      `json:"missingCount"`
	Languages    []string `json:"languages"`
}

func (s *Service) providerStatus(cfg Config) []ProviderStatus {
	s.mu.Lock()
	defer s.mu.Unlock()
	row := func(name string, enabled, configured bool) ProviderStatus {
		ps := ProviderStatus{Name: name, Enabled: enabled}
		st := s.states[name]
		ps.OK = enabled && configured
		if !configured && enabled {
			ps.Error = "Needs an API key."
		}
		if st != nil {
			ps.QuotaRemaining = st.remaining
			if st.lastErr != "" && enabled {
				ps.OK, ps.Error = false, st.lastErr
			}
			if s.now().Before(st.blockedUntil) {
				t := st.blockedUntil
				ps.BlockedUntil = &t
				ps.OK = false
			}
		}
		return ps
	}
	return []ProviderStatus{
		row(ProviderOpenSubtitles, cfg.Providers.OpenSubtitles.Enabled, cfg.Providers.OpenSubtitles.APIKey != ""),
		row(ProviderPodnapisi, cfg.Providers.Podnapisi.Enabled, true),
	}
}

// Status reports the providers and how many videos are still missing a
// subtitle. The count is kept for a minute.
func (s *Service) Status(ctx context.Context) (Status, error) {
	cfg := s.Config(ctx)
	n, err := s.MissingCount(ctx, cfg)
	if err != nil {
		return Status{}, err
	}
	return Status{Providers: s.providerStatus(cfg), MissingCount: n, Languages: cfg.Languages}, nil
}

// MissingCount counts the videos that lack at least one wanted language (ignoring
// any backoff).
func (s *Service) MissingCount(ctx context.Context, cfg Config) (int, error) {
	s.mu.Lock()
	if !s.missing.at.IsZero() && s.now().Sub(s.missing.at) < time.Minute {
		n := s.missing.n
		s.mu.Unlock()
		return n, nil
	}
	s.mu.Unlock()
	if len(cfg.Languages) == 0 {
		return 0, nil
	}
	cache := newDirCache()
	n := 0
	err := s.forEachVideo(ctx, func(f library.File, abs string) (bool, error) {
		langs, err := s.missingLanguages(ctx, f, abs, cfg, cache)
		if err != nil {
			return false, err
		}
		if len(langs) > 0 {
			n++
		}
		return false, nil
	})
	if err != nil {
		return 0, err
	}
	s.mu.Lock()
	s.missing.at, s.missing.n = s.now(), n
	s.mu.Unlock()
	return n, nil
}

// FileSubtitle is one subtitle of a file as the pages show it.
type FileSubtitle struct {
	// ID is set for a file Marquee wrote (it can be removed); 0 for any other.
	ID       int64     `json:"id,omitempty"`
	Language string    `json:"language"`
	HI       bool      `json:"hi"`
	Forced   bool      `json:"forced,omitempty"`
	Provider string    `json:"provider,omitempty"`
	Score    int       `json:"score,omitempty"`
	Name     string    `json:"name"`   // the file's name
	Source   string    `json:"source"` // "marquee" or "file"
	Added    time.Time `json:"addedAt,omitzero"`
}

// FileSubtitles is the answer to GET /library/files/{id}/subtitles.
type FileSubtitles struct {
	FileID    int64          `json:"fileId"`
	Languages []string       `json:"languages"` // wanted
	Subtitles []FileSubtitle `json:"subtitles"`
	Missing   []string       `json:"missing"`
	// CanSearch: the file is a movie or episode that is on disk, and a provider is set up.
	CanSearch bool `json:"canSearch"`
}

// ForFile lists the subtitles of a file: the ones Marquee wrote and the ones
// that are just there beside the video.
func (s *Service) ForFile(ctx context.Context, fileID int64) (FileSubtitles, error) {
	f, _, abs, err := s.loadFile(ctx, fileID)
	if err != nil {
		return FileSubtitles{}, err
	}
	if !isVideo(f) {
		return FileSubtitles{}, ErrNotVideo
	}
	cfg := s.Config(ctx)
	return s.describe(ctx, f, abs, cfg, newDirCache(), len(s.providers(cfg)) > 0)
}

// describe reads one video's subtitles; hasProvider says a provider is set up.
func (s *Service) describe(ctx context.Context, f library.File, abs string, cfg Config, cache *dirCache, hasProvider bool) (FileSubtitles, error) {
	tracked, err := s.store.List(ctx, f.ID)
	if err != nil {
		return FileSubtitles{}, err
	}
	out := FileSubtitles{FileID: f.ID, Languages: cfg.Languages, Subtitles: []FileSubtitle{}, Missing: []string{}}
	ours := map[string]bool{}
	for _, t := range tracked {
		name := filepath.Base(t.Path)
		ours[strings.ToLower(name)] = true
		out.Subtitles = append(out.Subtitles, FileSubtitle{
			ID: t.ID, Language: t.Language, HI: t.HI, Provider: t.Provider, Score: t.Score, Name: name, Source: "marquee", Added: t.CreatedAt,
		})
	}
	onDisk := false
	if st, err := os.Stat(abs); err == nil && st.Mode().IsRegular() {
		onDisk = true
		for _, x := range externalSubtitles(abs, cache) {
			if ours[strings.ToLower(x.Name)] {
				continue
			}
			out.Subtitles = append(out.Subtitles, FileSubtitle{Language: x.Language, HI: x.HI, Forced: x.Forced, Name: x.Name, Source: "file"})
		}
	}
	have := map[string]bool{}
	for _, x := range out.Subtitles {
		if x.Language != "" && !x.Forced {
			have[x.Language] = true
		}
	}
	for _, l := range cfg.Languages {
		if !have[l] {
			out.Missing = append(out.Missing, l)
		}
	}
	out.CanSearch = onDisk && hasProvider
	return out, nil
}

// TitleFile is one video file of a title with its subtitles.
type TitleFile struct {
	FileSubtitles
	// Name is the video's file name (never its folder).
	Name string `json:"name"`
	// EpisodeIDs are the episodes the file holds (a show's files).
	EpisodeIDs []int64 `json:"episodeIds"`
}

// ForTitle lists the subtitles of every video file of a movie or a show, so a
// page can show which languages each file has with one request.
func (s *Service) ForTitle(ctx context.Context, titleID int64) ([]TitleFile, error) {
	cfg := s.Config(ctx)
	hasProvider := len(s.providers(cfg)) > 0
	cache := newDirCache()
	folders := map[int64]library.Folder{}
	out := []TitleFile{}
	for off := 0; ; off += pageSize {
		files, total, err := s.deps.Library.Files(ctx, library.FileFilter{TitleID: titleID, State: library.StateMatched, Limit: pageSize, Offset: off})
		if err != nil {
			return nil, err
		}
		for _, f := range files {
			if !isVideo(f) {
				continue
			}
			folder, ok := folders[f.FolderID]
			if !ok {
				if folder, err = s.deps.Library.Folder(ctx, f.FolderID); err != nil {
					if errors.Is(err, library.ErrNotFound) {
						continue
					}
					return nil, err
				}
				folders[f.FolderID] = folder
			}
			abs := filepath.Join(folder.Path, filepath.FromSlash(f.RelPath))
			d, err := s.describe(ctx, f, abs, cfg, cache, hasProvider)
			if err != nil {
				return nil, err
			}
			eps := f.EpisodeIDs
			if eps == nil {
				eps = []int64{}
			}
			out = append(out, TitleFile{FileSubtitles: d, Name: filepath.Base(abs), EpisodeIDs: eps})
		}
		if len(files) == 0 || off+len(files) >= total {
			return out, nil
		}
	}
}

// TestProvider checks one provider answers and accepts the owner's key or
// login. Fields in override that are empty or still masked keep what the
// settings hold, so the Test button can try what is typed in the form before it
// is saved.
func (s *Service) TestProvider(ctx context.Context, name string, override OpenSubtitlesConfig) error {
	cfg := s.Config(ctx)
	switch name {
	case ProviderOpenSubtitles:
		oc := cfg.Providers.OpenSubtitles
		for _, f := range []struct {
			dst *string
			v   string
		}{
			{&oc.APIKey, override.APIKey}, {&oc.Username, override.Username}, {&oc.Password, override.Password},
		} {
			if v := strings.TrimSpace(f.v); v != "" && !strings.HasPrefix(v, settings.MaskPrefix) {
				*f.dst = v
			}
		}
		if s.hc == nil && s.custom == nil {
			return ErrNoProvider
		}
		var p Provider
		if s.custom != nil {
			p = providerByName(s.custom(cfg), name)
			if p == nil {
				return ErrNoProvider
			}
		} else {
			// A throwaway provider: the saved login must not be replaced by a try.
			t := NewOpenSubtitles(s.hc, s.osURL, s.version)
			t.now = s.now
			t.Configure(oc)
			p = t
		}
		err := p.Test(ctx)
		s.note(name, err)
		return err
	case ProviderPodnapisi:
		var p Provider
		switch {
		case s.custom != nil:
			p = providerByName(s.custom(cfg), name)
		case s.podna != nil:
			p = s.podna
		}
		if p == nil {
			return ErrNoProvider
		}
		err := p.Test(ctx)
		s.note(name, err)
		return err
	}
	return invalid("%q is not a subtitle source Marquee knows (opensubtitles or podnapisi).", name)
}
