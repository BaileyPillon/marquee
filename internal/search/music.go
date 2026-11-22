package search

import (
	"context"
	"sort"
	"strconv"
	"strings"

	"github.com/BaileyPillon/marquee/internal/decide"
	"github.com/BaileyPillon/marquee/internal/grab"
	"github.com/BaileyPillon/marquee/internal/sources"
	"github.com/BaileyPillon/marquee/internal/titles"
)

// Music is searched album by album. For each monitored album of an artist's copy
// that has no file on disk, every source is asked for that album (the music
// search with artist and album when the source takes them, else the text
// "<artist> <album>", always in the 3000-series music categories) and the best
// acceptable release is sent to the download client.
//
// An album is remembered in two places, neither of which needs a change to the
// grab package:
//
//   - search_history.grabbed_for holds the key "a<album id>" (albumKey), the way
//     "s01e04" does for an episode, so an album just sent is not sent again for a
//     day, and the importer can make the search forget it when the release was bad;
//   - the download itself carries the album in its episode ids (grab.Target has
//     no album field and is not changed): for an artist, "episode ids" are album
//     ids. The importer reads them to know which album to expect, and the pages
//     read them to show the album as downloading.
//
// album_searches remembers when each album was last looked for, so a run that
// stops at its limit carries on with the albums it has not tried.

// maxAlbumsPerRun bounds how many albums one search of one copy looks for.
const maxAlbumsPerRun = 12

func albumKey(id int64) string { return "a" + strconv.FormatInt(id, 10) }

// AlbumKey is the key the search remembers an album's grab by.
func AlbumKey(id int64) string { return albumKey(id) }

// queryForAlbum is the music search for one album of an artist.
func queryForAlbum(t titles.Title, a titles.Album) sources.Query {
	return sources.Query{
		Media: sources.MediaArtist, Artist: t.Name, Album: a.Name,
		Text: strings.TrimSpace(t.Name + " " + a.Name),
	}
}

// wantFor says what a release has to be to count as the album.
func wantFor(a titles.Album) decide.Want {
	return decide.Want{Album: a.Name, AlbumYear: a.Year, Tracks: len(a.Tracks)}
}

func firstAccepted(list []scored) (scored, bool) {
	for _, sc := range list {
		if sc.accepted() {
			return sc, true
		}
	}
	return scored{}, false
}

// grabAlbums looks for the albums an artist's copy is missing, least recently
// searched first, and sends the best acceptable release of each.
func (r *run) grabAlbums(ctx context.Context) error {
	albums := append([]titles.Album(nil), r.n.albums...)
	if len(albums) == 0 {
		r.note("Nothing to look for: every monitored album is in the library, or was just sent for download")
		return nil
	}
	last, err := r.s.store.AlbumSearched(ctx, r.copy.ID)
	if err != nil {
		return err
	}
	sort.SliceStable(albums, func(i, j int) bool {
		ti, seenI := last[albums[i].ID]
		tj, seenJ := last[albums[j].ID]
		switch {
		case seenI != seenJ:
			return !seenI // never searched first
		case seenI && !ti.Equal(tj):
			return ti.Before(tj)
		case albums[i].Year != albums[j].Year:
			return albums[i].Year < albums[j].Year
		}
		return albums[i].Name < albums[j].Name
	})
	for n, a := range albums {
		if err := ctx.Err(); err != nil {
			return err
		}
		if n >= maxAlbumsPerRun {
			r.note("Stopped after %d albums; the rest follow in the next search", maxAlbumsPerRun)
			break
		}
		if err := r.grabAlbum(ctx, a); err != nil {
			return err
		}
	}
	return nil
}

// grabAlbum searches for one album and sends the best acceptable release.
func (r *run) grabAlbum(ctx context.Context, a titles.Album) error {
	outs, err := r.s.src.SearchAll(ctx, queryForAlbum(r.d.Title, a))
	if err != nil {
		return err
	}
	r.tally.add(outs)
	list := r.s.scoreOutcomes(r.j, outs, wantFor(a))
	before := r.grabbed
	if sc, ok := firstAccepted(list); ok {
		if err := r.send(ctx, sc, []int64{a.ID}, []string{albumKey(a.ID)}); err != nil {
			return err
		}
	} else {
		switch {
		case len(list) == 0:
			r.note("Nothing found for %q", a.Name)
		default:
			r.note("Nothing acceptable yet for %q (closest: %s)", a.Name, strings.TrimSpace(list[0].verdict().Rejection))
		}
	}
	if err := r.s.store.NoteAlbumSearch(context.WithoutCancel(ctx), r.copy.ID, a.ID, r.s.now(), r.grabbed > before); err != nil {
		r.s.log.Warn("could not record an album search", "copy", r.copy.ID, "album", a.ID, "err", err)
	}
	return nil
}

// AlbumResult is what a search for one album did.
type AlbumResult struct {
	// Grabbed says a release was sent to the download client.
	Grabbed    bool
	DownloadID int64
	Release    string // the name of the release sent
	Quality    string
	Score      int
	Reasons    []string // why that release was picked
	// Found is how many releases the sources offered; Note says, in plain words,
	// why nothing was sent when nothing was.
	Found          int
	Note           string
	IndexersFailed []Failure
}

// SearchAlbum looks for one album of an artist now, whatever the scheduled
// search has done, and sends the best acceptable release to the download client.
// It makes the artist's copy if it has none. Nothing acceptable is not an error:
// the result says so.
func (s *Service) SearchAlbum(ctx context.Context, titleID, albumID int64) (AlbumResult, error) {
	g := s.theGrabber()
	if g == nil {
		return AlbumResult{}, ErrNoGrabber
	}
	d, err := s.titles.Get(ctx, titleID)
	if err != nil {
		return AlbumResult{}, err
	}
	if d.Kind != titles.KindArtist {
		return AlbumResult{}, invalid("Only an artist has albums.")
	}
	var album *titles.Album
	for i := range d.Albums {
		if d.Albums[i].ID == albumID {
			album = &d.Albums[i]
		}
	}
	if album == nil {
		return AlbumResult{}, invalid("That artist has no such album.")
	}
	enabled, err := s.src.Enabled(ctx)
	if err != nil {
		return AlbumResult{}, err
	}
	if len(enabled) == 0 {
		return AlbumResult{}, ErrNoSources
	}
	c, err := s.findCopy(ctx, titleID, grab.QualityHD)
	if err != nil {
		return AlbumResult{}, err
	}
	defer s.lockCopy(c.ID)()
	j, err := s.newJudge(ctx, d, c.Quality)
	if err != nil {
		return AlbumResult{}, err
	}
	r := &run{s: s, copy: c, d: d, j: j, tally: newTally()}
	defer r.finish(ctx)

	outs, err := s.src.SearchAll(ctx, queryForAlbum(d.Title, *album))
	if err != nil {
		return AlbumResult{}, err
	}
	r.tally.add(outs)
	list := s.scoreOutcomes(j, outs, wantFor(*album))
	res := AlbumResult{Found: len(list), IndexersFailed: r.tally.failures()}
	sc, ok := firstAccepted(list)
	if !ok {
		r.explain(list)
		res.Note = strings.Join(r.notes, "; ")
		if err := s.store.NoteAlbumSearch(context.WithoutCancel(ctx), c.ID, album.ID, s.now(), false); err != nil {
			s.log.Warn("could not record an album search", "copy", c.ID, "album", album.ID, "err", err)
		}
		return res, nil
	}
	if err := r.send(ctx, sc, []int64{album.ID}, []string{albumKey(album.ID)}); err != nil {
		return AlbumResult{}, err
	}
	if err := s.store.NoteAlbumSearch(context.WithoutCancel(ctx), c.ID, album.ID, s.now(), true); err != nil {
		s.log.Warn("could not record an album search", "copy", c.ID, "album", album.ID, "err", err)
	}
	res.Grabbed, res.Release, res.Quality = true, sc.item.Title, sc.quality
	res.Score, res.Reasons = sc.verdict().Score, sc.verdict().Reasons
	res.DownloadID = r.lastDownload
	return res, nil
}
