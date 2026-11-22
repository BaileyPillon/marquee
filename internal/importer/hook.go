package importer

import (
	"context"
	"slices"
	"time"

	"github.com/BaileyPillon/marquee/internal/titles"
)

// AfterImportHook is told which library files (file row ids) an import has just
// put in place, so the subtitles module can look for subtitles for them. It is
// called in the background, after the import has been recorded: the import never
// waits for it, and a hook that fails or panics changes nothing about the
// import. Music is not reported.
type AfterImportHook interface {
	AfterImport(ctx context.Context, fileIDs []int64)
}

// SetAfterImport wires in the hook; nil turns it off.
func (s *Service) SetAfterImport(h AfterImportHook) {
	s.mu.Lock()
	s.afterHook = h
	s.mu.Unlock()
}

// fileIDLister is the optional part of the Library the hook needs: the ids of
// the file rows at some paths. The library service has it.
type fileIDLister interface {
	FileIDs(ctx context.Context, folderID int64, relPaths []string) (map[string]int64, error)
}

// afterImport hands the files an import placed (or found already in place) to
// the hook, in the background.
func (s *Service) afterImport(_ context.Context, r *run) {
	s.mu.Lock()
	h, closed := s.afterHook, s.closed
	s.mu.Unlock()
	if h == nil || closed || r.det == nil || r.det.Kind == titles.KindArtist {
		return
	}
	lister, ok := s.deps.Library.(fileIDLister)
	if !ok {
		return
	}
	var rels []string
	for _, p := range r.placed {
		rels = append(rels, p.dest)
	}
	for _, p := range r.adopted {
		rels = append(rels, p.dest)
	}
	if len(rels) == 0 {
		return
	}
	folderID := r.folder.ID
	s.wg.Add(1)
	go func() {
		defer s.wg.Done()
		defer func() {
			if rec := recover(); rec != nil {
				s.log.Error("the after-import hook panicked", "panic", rec)
			}
		}()
		byPath, err := lister.FileIDs(s.baseCtx, folderID, rels)
		if err != nil {
			if s.baseCtx.Err() == nil {
				s.log.Warn("import: could not look up the new files for the after-import hook", "err", err)
			}
			return
		}
		ids := make([]int64, 0, len(byPath))
		for _, id := range byPath {
			ids = append(ids, id)
		}
		slices.Sort(ids)
		if len(ids) > 0 {
			h.AfterImport(s.baseCtx, ids)
		}
	}()
}

// ---- an artist's tracks ----------------------------------------------------------

// trackLoader is the optional part of the Catalog that loads an artist's
// tracks when they are not there yet. The titles service has it.
type trackLoader interface {
	EnsureTracks(ctx context.Context, id int64, albumIDs ...int64) (*titles.Detail, error)
}

// trackLoadTimeout bounds the wait for the provider: MusicBrainz answers about
// one request a second, and an artist's albums can take a while.
const trackLoadTimeout = 10 * time.Minute

// needsTracks reports whether the catalog should be asked for track lists
// before a download is read: the albums the download is for have none, or (the
// download names no album) no album of the artist has any.
func needsTracks(det *titles.Detail, albumIDs []int64) bool {
	if len(det.Albums) == 0 {
		return false // nothing to load tracks into
	}
	if len(albumIDs) > 0 {
		for _, a := range det.Albums {
			if slices.Contains(albumIDs, a.ID) && len(a.Tracks) == 0 {
				return true
			}
		}
		return false
	}
	for _, a := range det.Albums {
		if len(a.Tracks) > 0 {
			return false
		}
	}
	return true
}

// ensureTracks gives an artist's track lists a chance to load before the files
// of a download are matched against them. A failure is not the release's fault:
// the files are matched by name instead.
func (s *Service) ensureTracks(ctx context.Context, r *run, det *titles.Detail) *titles.Detail {
	if det.Kind != titles.KindArtist {
		return det
	}
	loader, ok := s.deps.Titles.(trackLoader)
	if !ok || !needsTracks(det, r.d.EpisodeIDs) {
		return det
	}
	lctx, cancel := context.WithTimeout(ctx, trackLoadTimeout)
	defer cancel()
	fresh, err := loader.EnsureTracks(lctx, det.ID, r.d.EpisodeIDs...)
	if err != nil {
		if ctx.Err() == nil {
			s.log.Warn("import: could not load the artist's tracks; matching by file name", "title", det.ID, "err", err)
		}
		return det
	}
	r.det = fresh
	return fresh
}
