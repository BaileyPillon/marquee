package search

import (
	"context"
	"fmt"
	"time"

	"github.com/BaileyPillon/marquee/internal/decide"
	"github.com/BaileyPillon/marquee/internal/grab"
	"github.com/BaileyPillon/marquee/internal/sources"
	"github.com/BaileyPillon/marquee/internal/titles"
)

// Result is one release a manual search found, with the verdict on it. It has
// no download link: those stay inside the service.
type Result struct {
	IndexerID   int64
	IndexerName string
	GUID        string
	Title       string
	Size        int64
	Protocol    string
	Seeders     int // -1: the source did not say
	PublishedAt time.Time
	Quality     string
	Score       int
	Accepted    bool
	Reasons     []string
	Rejection   string
}

// InteractiveResult is the answer to a manual search.
type InteractiveResult struct {
	Results          []Result
	SearchedAt       time.Time
	IndexersSearched int
	IndexersFailed   []Failure
}

func validQuality(q string) bool { return q == grab.QualityHD || q == grab.QualityUHD }

func cacheKey(indexerID int64, guid string) string { return fmt.Sprintf("%d|%s", indexerID, guid) }

// Interactive searches every enabled source for a title and returns every
// result with its verdict, best first, for the "Releases found" table. For a
// show, season (and episode) say what to look for; with neither, the whole show
// is searched and every release is judged on its own merits. The results can be
// grabbed for the next ten minutes.
func (s *Service) Interactive(ctx context.Context, titleID int64, quality string, season, episode int) (InteractiveResult, error) {
	if !validQuality(quality) {
		return InteractiveResult{}, invalid("quality must be hd or uhd.")
	}
	if season < 0 || episode < 0 {
		return InteractiveResult{}, invalid("season and episode must be positive numbers.")
	}
	if episode > 0 && season == 0 {
		return InteractiveResult{}, invalid("Pick a season to go with the episode.")
	}
	d, err := s.titles.Get(ctx, titleID)
	if err != nil {
		return InteractiveResult{}, err
	}
	j, err := s.newJudge(ctx, d, quality)
	if err != nil {
		return InteractiveResult{}, err
	}

	var w decide.Want
	if d.Kind == titles.KindShow && season > 0 {
		var nums []int
		total := 0
		for _, e := range d.Episodes {
			if e.Season == season {
				total++
				nums = append(nums, e.Number)
			}
		}
		switch {
		case episode > 0:
			w = decide.Want{Season: season, Episodes: []int{episode}, SeasonEpisodes: total}
		case len(nums) > 0:
			w = decide.Want{Season: season, Episodes: nums, SeasonEpisodes: total}
		}
	} else {
		season, episode = 0, 0
	}

	outs, err := s.src.SearchAll(ctx, queryFor(d.Title, season, episode))
	if err != nil {
		return InteractiveResult{}, err
	}
	t := newTally()
	t.add(outs)
	list := s.scoreOutcomes(j, outs, w)

	res := InteractiveResult{
		Results: make([]Result, 0, len(list)), SearchedAt: s.now().UTC(),
		IndexersSearched: len(t.ok) + len(t.failures()), IndexersFailed: t.failures(),
	}
	keep := map[string]cached{}
	for _, sc := range list {
		v := sc.verdict()
		res.Results = append(res.Results, Result{
			IndexerID: sc.item.IndexerID, IndexerName: sc.source, GUID: sc.item.GUID, Title: sc.item.Title,
			Size: sc.item.Size, Protocol: sc.item.Protocol, Seeders: sc.item.Seeders, PublishedAt: sc.item.PublishedAt,
			Quality: sc.quality, Score: v.Score, Accepted: v.Accepted, Reasons: v.Reasons, Rejection: v.Rejection,
		})
		keep[cacheKey(sc.item.IndexerID, sc.item.GUID)] = cached{item: sc.item, name: sc.source, at: s.now()}
	}
	s.remember(titleID, keep)
	return res, nil
}

// ---- the ten-minute memory of manual searches ----------------------------------

const maxCachedTitles = 50

func (s *Service) remember(titleID int64, items map[string]cached) {
	s.cacheMu.Lock()
	defer s.cacheMu.Unlock()
	now := s.now()
	for id, m := range s.cache {
		for k, c := range m {
			if now.Sub(c.at) > interactiveKeep {
				delete(m, k)
			}
		}
		if len(m) == 0 {
			delete(s.cache, id)
		}
	}
	m := s.cache[titleID]
	if m == nil {
		// Keep the memory small: drop the stalest title when full.
		if len(s.cache) >= maxCachedTitles {
			var oldest int64
			var oldestAt time.Time
			for id, mm := range s.cache {
				for _, c := range mm {
					if oldestAt.IsZero() || c.at.Before(oldestAt) {
						oldest, oldestAt = id, c.at
					}
					break
				}
			}
			delete(s.cache, oldest)
		}
		m = map[string]cached{}
		s.cache[titleID] = m
	}
	for k, c := range items {
		m[k] = c
	}
}

func (s *Service) recall(titleID, indexerID int64, guid string) (cached, bool) {
	s.cacheMu.Lock()
	defer s.cacheMu.Unlock()
	c, ok := s.cache[titleID][cacheKey(indexerID, guid)]
	if !ok || s.now().Sub(c.at) > interactiveKeep {
		return cached{}, false
	}
	return c, true
}

// ---- grabbing by hand ------------------------------------------------------------

// GrabResult is what a manual grab did.
type GrabResult struct {
	DownloadID int64
	Title      string
	Quality    string
	Score      int
	Reasons    []string
}

// GrabRelease sends one release a person picked to the download client. The
// release is looked up again from the last manual search (ten minutes) or from
// what the feeds showed lately; the owner's choice stands even when the profile
// would have turned it down, and the reasons say so.
func (s *Service) GrabRelease(ctx context.Context, titleID, indexerID int64, guid, quality string) (GrabResult, error) {
	if !validQuality(quality) {
		return GrabResult{}, invalid("quality must be hd or uhd.")
	}
	if guid == "" || indexerID <= 0 {
		return GrabResult{}, invalid("Say which release: indexerId and guid are needed.")
	}
	g := s.theGrabber()
	if g == nil {
		return GrabResult{}, ErrNoGrabber
	}
	d, err := s.titles.Get(ctx, titleID)
	if err != nil {
		return GrabResult{}, err
	}

	var (
		item   sources.Item
		source string
		prio   = sources.DefaultPriority
	)
	if c, ok := s.recall(titleID, indexerID, guid); ok {
		item, source = c.item, c.name
	} else {
		it, ok, err := s.src.SeenItem(ctx, indexerID, guid)
		if err != nil {
			return GrabResult{}, err
		}
		if !ok {
			return GrabResult{}, ErrReleaseGone
		}
		item = it
	}
	if ix, err := s.src.Get(ctx, indexerID); err == nil {
		source, prio = ix.Name, ix.Priority
	}

	c, err := s.findCopy(ctx, titleID, quality)
	if err != nil {
		return GrabResult{}, err
	}
	defer s.lockCopy(c.ID)()
	j, err := s.newJudge(ctx, d, quality)
	if err != nil {
		return GrabResult{}, err
	}
	sc := j.scoreOne(item, source, prio, decide.Want{})
	v := sc.verdict()

	rel := item.Release()
	rel.Quality, rel.Score = sc.quality, v.Score
	rel.Reasons = append([]string{"Picked by hand from the releases found"}, v.Reasons...)

	var ids []int64
	var keys []string
	switch {
	case d.Kind == titles.KindMovie:
		keys = []string{movieKey()}
	case sc.pack():
		keys = []string{seasonKey(sc.parsed.Season)}
	default:
		for _, e := range sc.parsed.Episodes {
			keys = append(keys, episodeKey(sc.parsed.Season, e))
			for _, ep := range d.Episodes {
				if ep.Season == sc.parsed.Season && ep.Number == e {
					ids = append(ids, ep.ID)
				}
			}
		}
	}
	t := grab.Target{CopyID: c.ID, TitleID: d.ID, Kind: d.Kind, EpisodeIDs: ids, Quality: c.Quality}
	downloadID, err := g.Grab(ctx, t, rel)
	if err != nil {
		return GrabResult{}, err
	}
	s.log.Info("release picked by hand", "title", d.Name, "release", item.Title, "source", source)
	hctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
	defer cancel()
	if err := s.store.AddHistory(hctx, History{
		CopyID: c.ID, At: s.now(), BestGUID: guid, Grabbed: 1, GrabbedFor: keys, Note: "Picked by hand: " + item.Title,
	}); err != nil {
		s.log.Warn("could not record a manual grab", "err", err)
	}
	return GrabResult{DownloadID: downloadID, Title: item.Title, Quality: sc.quality, Score: v.Score, Reasons: rel.Reasons}, nil
}

// findCopy looks up a title's copy of a quality: through the CopySource's own
// lookup when it has one, else by making sure the copy exists when it can, else
// among the wanted copies.
func (s *Service) findCopy(ctx context.Context, titleID int64, quality string) (grab.Copy, error) {
	cs := s.copySource()
	if cs == nil {
		return grab.Copy{}, ErrNoCopy
	}
	if f, ok := cs.(CopyFinder); ok {
		c, found, err := f.FindCopy(ctx, titleID, quality)
		if err != nil {
			return grab.Copy{}, err
		}
		if !found {
			return grab.Copy{}, ErrNoCopy
		}
		return c, nil
	}
	if e, ok := cs.(CopyEnsurer); ok {
		c, err := e.EnsureCopy(ctx, titleID, quality)
		if err != nil {
			s.log.Warn("could not make the copy for a manual grab", "title", titleID, "quality", quality, "err", err)
			return grab.Copy{}, fmt.Errorf("%w: %v", ErrNoCopy, err)
		}
		return c, nil
	}
	wanted, err := cs.Wanted(ctx)
	if err != nil {
		return grab.Copy{}, err
	}
	for _, c := range wanted {
		if c.TitleID == titleID && c.Quality == quality {
			return c, nil
		}
	}
	return grab.Copy{}, ErrNoCopy
}
