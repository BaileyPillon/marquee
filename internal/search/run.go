package search

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"sort"
	"strings"
	"time"

	"github.com/BaileyPillon/marquee/internal/decide"
	"github.com/BaileyPillon/marquee/internal/grab"
	"github.com/BaileyPillon/marquee/internal/parse"
	"github.com/BaileyPillon/marquee/internal/sources"
	"github.com/BaileyPillon/marquee/internal/titles"
)

// ---- what is missing -----------------------------------------------------------

// need is what a copy still lacks.
type need struct {
	movie   bool
	seasons map[int][]titles.Episode // season -> missing episodes, in order
	totals  map[int]int              // season -> episodes the catalog lists
	albums  []titles.Album           // an artist's monitored albums with no file, not just sent for download
}

func movieKey() string                 { return "movie" }
func seasonKey(season int) string      { return fmt.Sprintf("s%02d", season) }
func episodeKey(season, ep int) string { return fmt.Sprintf("s%02de%02d", season, ep) }

// needFor works out what a copy still lacks: the film, or the monitored and
// already aired episodes that have no file and were not sent for download in
// the last day.
func (s *Service) needFor(ctx context.Context, c grab.Copy, d *titles.Detail) (need, error) {
	var n need
	var have Have
	if b := s.shelf.Load(); b != nil {
		h, err := b.s.Have(ctx, c)
		if err != nil {
			return need{}, err
		}
		have = h
	}
	recent, err := s.store.RecentlyGrabbed(ctx, c.ID, s.now().Add(-grabMemory))
	if err != nil {
		return need{}, err
	}
	switch d.Kind {
	case titles.KindMovie:
		n.movie = !have.Movie && !recent[movieKey()]
	case titles.KindArtist:
		haveAlbum := map[int64]bool{}
		for _, id := range have.AlbumIDs {
			haveAlbum[id] = true
		}
		for _, a := range d.Albums {
			if a.Monitored && !haveAlbum[a.ID] && !recent[albumKey(a.ID)] {
				n.albums = append(n.albums, a)
			}
		}
	case titles.KindShow:
		n.seasons, n.totals = map[int][]titles.Episode{}, map[int]int{}
		haveEp := map[int64]bool{}
		for _, id := range have.EpisodeIDs {
			haveEp[id] = true
		}
		// A copy asked for by season is only searched for those seasons.
		var asked map[int]bool
		if sc, ok := s.copySource().(Scoper); ok {
			seasons, all, err := sc.CopySeasons(ctx, c)
			if err != nil {
				return need{}, err
			}
			if !all && len(seasons) > 0 {
				asked = map[int]bool{}
				for _, sn := range seasons {
					asked[sn] = true
				}
			}
		}
		today := s.now().UTC().Format("2006-01-02")
		for _, e := range d.Episodes {
			n.totals[e.Season]++
			switch {
			case e.Season == 0, !e.Monitored, e.AirDate == "", e.AirDate > today, haveEp[e.ID]:
				continue
			case asked != nil && !asked[e.Season]:
				continue
			case recent[episodeKey(e.Season, e.Number)], recent[seasonKey(e.Season)]:
				continue
			}
			n.seasons[e.Season] = append(n.seasons[e.Season], e)
		}
	}
	return n, nil
}

func numbers(eps []titles.Episode) []int {
	out := make([]int, len(eps))
	for i, e := range eps {
		out[i] = e.Number
	}
	return out
}

// ---- one run -------------------------------------------------------------------

// run is one pass of deciding and grabbing for a copy.
type run struct {
	s        *Service
	copy     grab.Copy
	d        *titles.Detail
	j        judge
	n        need
	tally    *tally
	grabbed  int
	keys     []string
	titles   []string // release names sent
	bestGUID string
	notes    []string
	// lastDownload is the downloads row of the latest release sent.
	lastDownload int64
}

func (r *run) note(format string, args ...any) {
	r.notes = append(r.notes, fmt.Sprintf(format, args...))
}

// send hands one release to the download client.
func (r *run) send(ctx context.Context, sc scored, episodeIDs []int64, keys []string) error {
	g := r.s.theGrabber()
	if g == nil {
		return ErrNoGrabber
	}
	rel := sc.item.Release()
	rel.Quality = sc.quality
	rel.Score = sc.verdict().Score
	rel.Reasons = sc.verdict().Reasons
	t := grab.Target{CopyID: r.copy.ID, TitleID: r.d.ID, Kind: r.d.Kind, EpisodeIDs: episodeIDs, Quality: r.copy.Quality}
	id, err := g.Grab(ctx, t, rel)
	if err != nil {
		return err
	}
	r.lastDownload = id
	r.grabbed++
	r.keys = append(r.keys, keys...)
	r.titles = append(r.titles, sc.item.Title)
	if r.bestGUID == "" {
		r.bestGUID = sc.item.GUID
	}
	r.s.log.Info("release sent for download", "copy", r.copy.ID, "title", r.d.Name, "release", sc.item.Title, "source", sc.source)
	return nil
}

// grabMovie sends the best acceptable release of a ranked list.
func (r *run) grabMovie(ctx context.Context, list []scored) error {
	for _, sc := range list {
		if sc.accepted() {
			return r.send(ctx, sc, nil, []string{movieKey()})
		}
	}
	r.explain(list)
	return nil
}

// grabSeason decides what to send for the missing episodes of one season. With
// two or more missing, an acceptable whole-season pack wins; otherwise each
// missing episode gets the best acceptable release of its own.
func (r *run) grabSeason(ctx context.Context, season int, missing []titles.Episode, list []scored) error {
	nums := numbers(missing)
	if len(nums) >= 2 {
		for _, sc := range list {
			if sc.accepted() && sc.pack() {
				return r.send(ctx, sc, nil, []string{seasonKey(season)})
			}
		}
	}
	covered := map[int]bool{}
	for _, n := range nums {
		if covered[n] {
			continue
		}
		if r.grabbed >= maxGrabsPerRun {
			r.note("Stopped after %d releases; the rest follow in the next search", maxGrabsPerRun)
			return nil
		}
		found := false
		for _, sc := range list {
			if !sc.accepted() || sc.pack() || sc.parsed.Season != season || !slices.Contains(sc.parsed.Episodes, n) {
				continue
			}
			var ids []int64
			var keys []string
			for _, e := range sc.parsed.Episodes {
				covered[e] = true
				keys = append(keys, episodeKey(season, e))
				for _, ep := range r.d.Episodes {
					if ep.Season == season && ep.Number == e {
						ids = append(ids, ep.ID)
					}
				}
			}
			if err := r.send(ctx, sc, ids, keys); err != nil {
				return err
			}
			found = true
			break
		}
		if !found {
			r.note("Nothing acceptable yet for %s", epLabel(season, n))
		}
	}
	return nil
}

func epLabel(season, ep int) string { return fmt.Sprintf("S%02dE%02d", season, ep) }

// explain notes why a list had no acceptable release.
func (r *run) explain(list []scored) {
	switch {
	case len(list) == 0:
		r.note("Nothing found")
	default:
		r.note("Found %d releases, none acceptable. Closest: %s", len(list), strings.TrimSpace(list[0].verdict().Rejection))
	}
}

// finish records the run in search_history, even when the request was cancelled.
func (r *run) finish(ctx context.Context) {
	note := strings.Join(r.notes, "; ")
	if r.grabbed > 0 {
		sent := fmt.Sprintf("Sent %d release", r.grabbed)
		if r.grabbed != 1 {
			sent += "s"
		}
		note = sent + " for download: " + r.titles[0]
		if r.grabbed > 1 {
			note += fmt.Sprintf(" and %d more", r.grabbed-1)
		}
		if len(r.notes) > 0 {
			note += "; " + strings.Join(r.notes, "; ")
		}
	}
	if len(note) > 500 {
		note = note[:500]
	}
	hctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
	defer cancel()
	err := r.s.store.AddHistory(hctx, History{
		CopyID: r.copy.ID, At: r.s.now(), IndexersOK: r.tally.okCount(), IndexersFailed: len(r.tally.failures()),
		Results: r.tally.items, BestGUID: r.bestGUID, Grabbed: r.grabbed, GrabbedFor: r.keys, Note: note,
	})
	if err != nil {
		r.s.log.Warn("could not record a search", "copy", r.copy.ID, "err", err)
	}
}

// ---- SearchCopy ----------------------------------------------------------------

// SearchCopy searches every enabled source for what a copy is missing and sends
// the best acceptable release of each thing to the download client. It
// implements grab.Searcher. Nothing acceptable is not an error; the outcome is
// recorded in search_history either way.
func (s *Service) SearchCopy(ctx context.Context, copyID int64) (err error) {
	cs := s.copySource()
	if cs == nil {
		return errors.New("search: the requests module is not connected yet")
	}
	c, err := cs.Copy(ctx, copyID)
	if err != nil {
		return err
	}
	defer s.lockCopy(copyID)()
	if s.theGrabber() == nil {
		return ErrNoGrabber
	}
	d, err := s.titles.Get(ctx, c.TitleID)
	if err != nil {
		return err
	}
	j, err := s.newJudge(ctx, d, c.Quality)
	if err != nil {
		return err
	}
	r := &run{s: s, copy: c, d: d, j: j, tally: newTally()}
	defer r.finish(ctx)
	defer func() {
		// Runs before finish, so the history says the search did not go through.
		if err != nil && ctx.Err() == nil {
			r.note("The search stopped early because something failed")
		}
	}()

	enabled, err := s.src.Enabled(ctx)
	if err != nil {
		return err
	}
	if len(enabled) == 0 {
		r.note("No sources are set up yet")
		return nil
	}
	if r.n, err = s.needFor(ctx, c, d); err != nil {
		return err
	}

	switch d.Kind {
	case titles.KindMovie:
		if !r.n.movie {
			r.note("Nothing to look for: it is already in the library, or was just sent for download")
			return nil
		}
		outs, err := s.src.SearchAll(ctx, queryFor(d.Title, 0, 0))
		if err != nil {
			return err
		}
		r.tally.add(outs)
		return r.grabMovie(ctx, s.scoreOutcomes(j, outs, decide.Want{}))

	case titles.KindArtist:
		return r.grabAlbums(ctx)

	default:
		seasons := make([]int, 0, len(r.n.seasons))
		for sn, eps := range r.n.seasons {
			if len(eps) > 0 {
				seasons = append(seasons, sn)
			}
		}
		sort.Ints(seasons)
		if len(seasons) == 0 {
			r.note("Nothing to look for: every aired episode is in the library, or was just sent for download")
			return nil
		}
		for _, sn := range seasons {
			if err := ctx.Err(); err != nil {
				return err
			}
			missing := r.n.seasons[sn]
			ep := 0
			if len(missing) == 1 {
				ep = missing[0].Number
			}
			outs, err := s.src.SearchAll(ctx, queryFor(d.Title, sn, ep))
			if err != nil {
				return err
			}
			r.tally.add(outs)
			w := decide.Want{Season: sn, Episodes: numbers(missing), SeasonEpisodes: r.n.totals[sn]}
			if err := r.grabSeason(ctx, sn, missing, s.scoreOutcomes(j, outs, w)); err != nil {
				return err
			}
			if r.grabbed >= maxGrabsPerRun {
				break
			}
		}
		return nil
	}
}

// ---- the scheduled run ---------------------------------------------------------

// SearchMissing is the "search.missing" job: search every wanted copy that was
// not searched lately, one after another with a short pause between them so the
// sources are not hammered.
func (s *Service) SearchMissing(ctx context.Context) error {
	cs := s.copySource()
	if cs == nil {
		return nil // requests not wired in (yet)
	}
	wanted, err := cs.Wanted(ctx)
	if err != nil {
		if ctx.Err() != nil {
			return nil
		}
		return err
	}
	last, err := s.store.LastSearched(ctx)
	if err != nil {
		return err
	}
	var searched, failed int
	var first error
	for _, c := range wanted {
		if t, ok := last[c.ID]; ok && s.now().Sub(t) < skipIfSearchedWithin {
			continue
		}
		if searched > 0 && s.pace > 0 {
			select {
			case <-ctx.Done():
				return nil
			case <-time.After(s.pace):
			}
		}
		if ctx.Err() != nil {
			return nil
		}
		searched++
		err := s.SearchCopy(ctx, c.ID)
		switch {
		case err == nil:
		case errors.Is(err, ErrNoGrabber):
			s.log.Info("search.missing: no download client is set up, so nothing is searched")
			return nil
		case ctx.Err() != nil:
			return nil
		default:
			failed++
			if first == nil {
				first = fmt.Errorf("copy %d: %w", c.ID, err)
			}
			s.log.Warn("search failed", "copy", c.ID, "err", err)
		}
	}
	if _, err := s.store.PruneHistory(ctx, s.now().Add(-historyKeep)); err != nil && ctx.Err() == nil {
		s.log.Warn("could not forget old searches", "err", err)
	}
	s.log.Info("missing releases searched", "copies", searched, "failed", failed)
	if searched > 0 && failed == searched {
		return fmt.Errorf("search.missing: every search failed; first: %w", first)
	}
	return nil
}

// ---- the feed matcher ----------------------------------------------------------

// MatchNew is called with feed items that were not seen before. Those that
// match something a wanted copy lacks, and that the copy's profile accepts, are
// sent for download. It implements sources.Matcher.
func (s *Service) MatchNew(ctx context.Context, items []sources.Item) {
	cs, g := s.copySource(), s.theGrabber()
	if cs == nil || g == nil || len(items) == 0 {
		return
	}
	wanted, err := cs.Wanted(ctx)
	if err != nil || len(wanted) == 0 {
		if err != nil && ctx.Err() == nil {
			s.log.Warn("feed matching: could not list wanted copies", "err", err)
		}
		return
	}
	enabled, err := s.src.Enabled(ctx)
	if err != nil {
		s.log.Warn("feed matching: could not list sources", "err", err)
		return
	}
	names := map[int64]sources.Indexer{}
	for _, ix := range enabled {
		names[ix.ID] = ix
	}

	type parsedItem struct {
		item   sources.Item
		parsed parse.Release
	}
	parsedItems := make([]parsedItem, 0, len(items))
	for _, it := range items {
		if _, ok := names[it.IndexerID]; !ok {
			continue // the source was disabled or removed meanwhile
		}
		parsedItems = append(parsedItems, parsedItem{it, parse.ParseRelease(it.Title)})
	}
	details := map[int64]*titles.Detail{}
	for _, c := range wanted {
		if ctx.Err() != nil {
			return
		}
		d, ok := details[c.TitleID]
		if !ok {
			if d, err = s.titles.Get(ctx, c.TitleID); err != nil {
				s.log.Warn("feed matching: could not load a title", "title", c.TitleID, "err", err)
				d = nil
			}
			details[c.TitleID] = d
		}
		if d == nil || d.Kind == titles.KindArtist {
			continue
		}
		var cands []sources.Item
		for _, pi := range parsedItems {
			rel := decide.Release{Title: pi.item.Title, IMDb: pi.item.IMDb, TMDb: pi.item.TMDb, TVMaze: pi.item.TVMaze}
			if ok, _ := decide.TitleMatches(d.Title, pi.parsed, rel); ok {
				cands = append(cands, pi.item)
			}
		}
		if len(cands) == 0 {
			continue
		}
		if err := s.matchCopy(ctx, c, d, cands, names); err != nil && ctx.Err() == nil {
			s.log.Warn("feed matching failed", "copy", c.ID, "err", err)
		}
	}
}

func (s *Service) matchCopy(ctx context.Context, c grab.Copy, d *titles.Detail, cands []sources.Item, names map[int64]sources.Indexer) error {
	defer s.lockCopy(c.ID)()
	j, err := s.newJudge(ctx, d, c.Quality)
	if err != nil {
		return err
	}
	n, err := s.needFor(ctx, c, d)
	if err != nil {
		return err
	}
	r := &run{s: s, copy: c, d: d, j: j, n: n, tally: newTally()}
	defer func() {
		// Only a grab is worth a history row here: a feed that merely mentions
		// the title must not count as a search of the copy.
		if r.grabbed > 0 {
			r.notes = append([]string{"Found in the latest feeds"}, r.notes...)
			r.finish(ctx)
		}
	}()
	score := func(w decide.Want, only func(parse.Release) bool) []scored {
		var list []scored
		for _, it := range cands {
			ix := names[it.IndexerID]
			sc := j.scoreOne(it, ix.Name, ix.Priority, w)
			if only == nil || only(sc.parsed) {
				list = append(list, sc)
			}
		}
		return rank(list)
	}

	switch d.Kind {
	case titles.KindMovie:
		if !n.movie {
			return nil
		}
		for _, sc := range score(decide.Want{}, nil) {
			if sc.accepted() {
				if err := r.send(ctx, sc, nil, []string{movieKey()}); err != nil {
					return err
				}
				break
			}
		}
	default:
		seasons := make([]int, 0, len(n.seasons))
		for sn, eps := range n.seasons {
			if len(eps) > 0 {
				seasons = append(seasons, sn)
			}
		}
		sort.Ints(seasons)
		for _, sn := range seasons {
			missing := n.seasons[sn]
			w := decide.Want{Season: sn, Episodes: numbers(missing), SeasonEpisodes: n.totals[sn]}
			list := score(w, func(p parse.Release) bool { return p.Season == sn })
			if len(list) == 0 {
				continue
			}
			if err := r.grabSeason(ctx, sn, missing, list); err != nil {
				return err
			}
		}
	}
	return nil
}
