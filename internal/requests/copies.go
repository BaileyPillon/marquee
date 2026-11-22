package requests

import (
	"context"
	"encoding/json"
	"errors"
	"slices"
	"strings"

	"github.com/BaileyPillon/marquee/internal/grab"
	"github.com/BaileyPillon/marquee/internal/library"
	"github.com/BaileyPillon/marquee/internal/parse"
	"github.com/BaileyPillon/marquee/internal/titles"
)

// EnsureCopy makes sure a title has a monitored copy at a quality, in the folder
// the settings (or the folder rules) pick, and returns it. Approving a request
// does this; the owner adding a title directly will too. It does not touch the
// title's own monitored flag.
func (s *Service) EnsureCopy(ctx context.Context, titleID int64, quality string) (grab.Copy, error) {
	if !validQuality(quality) {
		return grab.Copy{}, invalid("invalid_quality", `quality must be "hd" or "uhd".`)
	}
	d, err := s.cat.Get(ctx, titleID)
	if err != nil {
		return grab.Copy{}, err
	}
	if d.Kind == KindArtist && quality == QualityUHD {
		return grab.Copy{}, invalid("invalid_quality", "Music comes in one quality.")
	}
	folder, err := s.resolveFolder(ctx, s.Config(ctx), d.Kind, quality)
	if err != nil {
		return grab.Copy{}, err
	}
	id, err := s.store.q().ensureCopy(ctx, titleID, quality, folder.ID, s.now())
	if err != nil {
		return grab.Copy{}, err
	}
	return s.Copy(ctx, id)
}

// Copy implements grab.CopySource: one copy with the path of its library folder.
// A copy whose folder was removed from the library is given a folder again, by
// the usual rules, if the library has one.
func (s *Service) Copy(ctx context.Context, id int64) (grab.Copy, error) {
	row, err := s.store.q().copyByID(ctx, id)
	if err != nil {
		return grab.Copy{}, err
	}
	folders, err := s.lib.ListFolders(ctx)
	if err != nil {
		return grab.Copy{}, err
	}
	return s.grabCopy(ctx, row, folders, nil)
}

// grabCopy turns a copy row into the shared type. cfg may be nil.
func (s *Service) grabCopy(ctx context.Context, row copyRow, folders []library.Folder, cfg *Config) (grab.Copy, error) {
	c := grab.Copy{ID: row.ID, TitleID: row.TitleID, Kind: row.Kind, Quality: row.Quality, Monitored: row.Monitored}
	if row.FolderID == 0 {
		// The folder it had was removed. Give it one if the library has one now.
		if cfg == nil {
			v := s.Config(ctx)
			cfg = &v
		}
		if f, ok := pickFolder(*cfg, row.Kind, row.Quality, folders); ok {
			if err := s.store.q().setCopyFolder(ctx, row.ID, f.ID); err != nil {
				return grab.Copy{}, err
			}
			row.FolderID = f.ID
		}
	}
	for _, f := range folders {
		if f.ID == row.FolderID {
			c.FolderID, c.FolderPath = f.ID, f.Path
			break
		}
	}
	return c, nil
}

// ---- wanted ----------------------------------------------------------------

// filePage is how many file rows Wanted reads from the library at a time.
const filePage = 2000

// Wanted implements grab.CopySource: the monitored copies that still have
// something missing. Only copies whose title is monitored too count, so turning
// a title off in the catalog stops its searches.
//
// What "missing" means for now is a simple reading of the library's matched
// files (a file counts only if it is matched to the title and still on disk):
//
//   - A file's quality is "uhd" when parse.QualityName's label for it names a
//     2160p resolution (Bluray-2160p, WEBDL-2160p, Remux-2160p, Unknown-2160p),
//     and "hd" for anything else, including no resolution at all. Music files
//     count as "hd".
//   - Movie: missing when the title has no file of the copy's quality.
//   - Artist: missing when one of the monitored albums has no matched music file
//     (a file belongs to an album by its track id, or, for a file scanned before
//     the tracks were known, by the album its path names). An artist whose
//     albums are not loaded yet is missing when it has no music file at all.
//   - Show: the episodes that count are the monitored ones that have aired (a
//     known air date up to today) and sit in a season the copy was asked for (any
//     season, when the copy has no request that names seasons, or one asks for
//     all). It is missing when one of them has no file of the copy's quality
//     that names it. A show with no episode rows at all is missing when it has
//     no file of that quality.
//
// A copy with no folder yet is still listed; its FolderPath is empty.
func (s *Service) Wanted(ctx context.Context) ([]grab.Copy, error) {
	rows, err := s.store.q().wantedCopies(ctx)
	if err != nil {
		return nil, err
	}
	out := []grab.Copy{}
	if len(rows) == 0 {
		return out, nil
	}
	facts, err := s.fileFacts(ctx, 0)
	if err != nil {
		return nil, err
	}
	scopes, err := s.store.q().liveScopes(ctx)
	if err != nil {
		return nil, err
	}
	folders, err := s.lib.ListFolders(ctx)
	if err != nil {
		return nil, err
	}
	cfg := s.Config(ctx)
	today := s.now().UTC().Format("2006-01-02")

	for _, row := range rows {
		ff := facts[row.TitleID]
		var missing bool
		switch row.Kind {
		case KindShow:
			d, err := s.cat.Get(ctx, row.TitleID)
			if errors.Is(err, titles.ErrNotFound) {
				continue // deleted while we were looking
			}
			if err != nil {
				return nil, err
			}
			missing = showMissing(d, row.Quality, ff, scopes[copyKey{row.TitleID, row.Quality}], today)
		case KindArtist:
			d, err := s.cat.Get(ctx, row.TitleID)
			if errors.Is(err, titles.ErrNotFound) {
				continue
			}
			if err != nil {
				return nil, err
			}
			missing = artistMissing(d, ff)
		default:
			missing = !ff.has(row.Quality)
		}
		if !missing {
			continue
		}
		c, err := s.grabCopy(ctx, row, folders, &cfg)
		if err != nil {
			return nil, err
		}
		out = append(out, c)
	}
	return out, nil
}

// titleFiles is what the matched files say about one title.
type titleFiles struct {
	quality  map[string]bool
	episodes map[string]map[int64]bool // quality -> episode ids with a file
	tracks   map[int64]bool            // music: track ids with a file
	albums   []string                  // music files with no track id: the album their path names
}

func (f *titleFiles) has(quality string) bool { return f != nil && f.quality[quality] }

func (f *titleFiles) hasEpisode(quality string, id int64) bool {
	return f != nil && f.episodes[quality][id]
}

// fileQuality is the copy quality a file belongs to: "uhd" for a 2160p label,
// "hd" otherwise.
func fileQuality(label string) string {
	if strings.Contains(label, "2160p") {
		return QualityUHD
	}
	return QualityHD
}

// fileFacts reads every matched file once and groups what it says by title. A
// titleID that is not 0 reads only that title's files.
func (s *Service) fileFacts(ctx context.Context, titleID int64) (map[int64]*titleFiles, error) {
	out := map[int64]*titleFiles{}
	for off := 0; ; off += filePage {
		files, total, err := s.lib.Files(ctx, library.FileFilter{State: library.StateMatched, TitleID: titleID, Limit: filePage, Offset: off})
		if err != nil {
			return nil, err
		}
		for _, f := range files {
			if f.TitleID == nil || f.MissingSince != nil || f.State != library.StateMatched {
				continue
			}
			q := QualityHD
			if f.Kind != library.KindMusic {
				q = fileQuality(f.Quality)
			}
			tf := out[*f.TitleID]
			if tf == nil {
				tf = &titleFiles{quality: map[string]bool{}, episodes: map[string]map[int64]bool{}}
				out[*f.TitleID] = tf
			}
			tf.quality[q] = true
			if f.Kind == library.KindMusic {
				tf.addMusic(f)
			}
			for _, e := range f.EpisodeIDs {
				if tf.episodes[q] == nil {
					tf.episodes[q] = map[int64]bool{}
				}
				tf.episodes[q][e] = true
			}
		}
		if len(files) == 0 || off+len(files) >= total {
			return out, nil
		}
	}
}

// addMusic notes which track or album a music file stands for.
func (f *titleFiles) addMusic(file library.File) {
	if file.TrackID != nil {
		if f.tracks == nil {
			f.tracks = map[int64]bool{}
		}
		f.tracks[*file.TrackID] = true
		return
	}
	var parsed parse.MusicRelease
	if json.Unmarshal(file.Parsed, &parsed) == nil && parsed.Album != "" && !slices.Contains(f.albums, parsed.Album) {
		f.albums = append(f.albums, parsed.Album)
	}
}

// albumsHaving says which of an artist's albums have a file: one with a track
// that has a file, or one a file's path names (a file the scanner matched to
// the artist before the tracks were loaded).
func albumsHaving(d *titles.Detail, ff *titleFiles) map[int64]bool {
	out := map[int64]bool{}
	if ff == nil {
		return out
	}
	for _, a := range d.Albums {
		for _, t := range a.Tracks {
			if ff.tracks[t.ID] {
				out[a.ID] = true
				break
			}
		}
		if out[a.ID] {
			continue
		}
		for _, name := range ff.albums {
			if parse.AlbumNameMatches(a.Name, name) {
				out[a.ID] = true
				break
			}
		}
	}
	return out
}

// artistMissing applies the artist rule described at Wanted.
func artistMissing(d *titles.Detail, ff *titleFiles) bool {
	if len(d.Albums) == 0 {
		return !ff.has(QualityHD)
	}
	have := albumsHaving(d, ff)
	for _, a := range d.Albums {
		if a.Monitored && !have[a.ID] {
			return true
		}
	}
	return false
}

// AlbumsOnDisk says which of an artist's albums have at least one matched file
// that is still on disk, by album id. The artist page and the music search use
// it, so they agree with Wanted about what is missing.
func (s *Service) AlbumsOnDisk(ctx context.Context, titleID int64) (map[int64]bool, error) {
	d, err := s.cat.Get(ctx, titleID)
	if err != nil {
		return nil, err
	}
	facts, err := s.fileFacts(ctx, titleID)
	if err != nil {
		return nil, err
	}
	return albumsHaving(d, facts[titleID]), nil
}

// showMissing applies the show rule described at Wanted.
func showMissing(d *titles.Detail, quality string, ff *titleFiles, scopes []Scope, today string) bool {
	allSeasons := len(scopes) == 0
	seasons := map[int]bool{}
	for _, sc := range scopes {
		if sc.All || len(sc.Seasons) == 0 {
			allSeasons = true
		}
		for _, n := range sc.Seasons {
			seasons[n] = true
		}
	}
	if len(d.Episodes) == 0 {
		return !ff.has(quality)
	}
	for _, e := range d.Episodes {
		if !e.Monitored || e.AirDate == "" || e.AirDate > today {
			continue
		}
		if !allSeasons && !seasons[e.Season] {
			continue
		}
		if !ff.hasEpisode(quality, e.ID) {
			return true
		}
	}
	return false
}
