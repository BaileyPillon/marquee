// Package calendar answers "what is coming up, and where does it stand": the
// episodes of monitored shows and the releases of monitored movies that fall
// inside a range of dates, each with a state:
//
//	available    a matched file is in the library
//	downloading  a download for it is open (progress says how far)
//	expected     it has not come out yet (or comes out today)
//	missing      it came out and nothing is in the library or on its way
//
// The package reads the catalog's, the library's and the downloads' tables for
// display only, in store.go (the precedent set by the requests module), and
// writes nothing.
package calendar

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"slices"
	"strings"
	"time"
)

// MaxRangeDays is the longest range one call answers: from and to may be at
// most this many days apart.
const MaxRangeDays = 92

// DateLayout is how dates are written: 2026-11-21.
const DateLayout = "2006-01-02"

// Kinds of item.
const (
	KindEpisode = "episode"
	KindMovie   = "movie"
)

// States of an item.
const (
	StateAvailable   = "available"
	StateDownloading = "downloading"
	StateExpected    = "expected"
	StateMissing     = "missing"
)

// Errors the web layer maps to HTTP statuses.
var (
	ErrInvalidRange  = errors.New("calendar: the end of the range is before its start")
	ErrRangeTooLarge = fmt.Errorf("calendar: the range is longer than %d days", MaxRangeDays)
)

// Item is one episode or one movie release on a day.
type Item struct {
	Date    string `json:"date"` // YYYY-MM-DD
	TitleID int64  `json:"titleId"`
	Kind    string `json:"kind"` // episode or movie
	// Name is the show's or the movie's name.
	Name string `json:"name"`
	// Season and Episode are null for a movie.
	Season      *int   `json:"season"`
	Episode     *int   `json:"episode"`
	EpisodeName string `json:"episodeName"`
	// Label is a short text for the item: "S02E05" for an episode, "Release" for
	// a movie.
	Label string `json:"label"`
	State string `json:"state"`
	// Progress (0 to 1) is how far the download is; null unless State is
	// downloading.
	Progress *float64 `json:"progress"`
	// RequestedByMe is true when the person asking for the calendar has an ask
	// for the title that was not declined or cancelled.
	RequestedByMe bool `json:"requestedByMe"`
}

// Viewer is the person the calendar is for.
type Viewer struct {
	UserID int64
	Role   string
}

// Option changes one Range call.
type Option func(*rangeOpts)

type rangeOpts struct{ mine bool }

// OnlyMine keeps the titles the viewer has asked for.
func OnlyMine() Option { return func(o *rangeOpts) { o.mine = true } }

// Options configure the service.
type Options struct {
	// Now replaces the clock (tests). The day it falls on is "today".
	Now func() time.Time
}

// Service is the calendar.
type Service struct {
	store *Store
	now   func() time.Time
}

// NewService builds the calendar on the main database.
func NewService(d *sql.DB, o Options) *Service {
	if o.Now == nil {
		o.Now = time.Now
	}
	return &Service{store: NewStore(d), now: o.Now}
}

// Range lists the items from the day of from to the day of to, both included,
// oldest first. The range may be at most MaxRangeDays days from end to end.
func (s *Service) Range(ctx context.Context, from, to time.Time, viewer Viewer, opts ...Option) ([]Item, error) {
	var o rangeOpts
	for _, f := range opts {
		f(&o)
	}
	fromDay, toDay := from.Format(DateLayout), to.Format(DateLayout)
	fd, _ := time.Parse(DateLayout, fromDay)
	td, _ := time.Parse(DateLayout, toDay)
	switch {
	case td.Before(fd):
		return nil, ErrInvalidRange
	case td.Sub(fd) > MaxRangeDays*24*time.Hour:
		return nil, ErrRangeTooLarge
	}
	today := s.now().Format(DateLayout)

	eps, err := s.store.episodes(ctx, fromDay, toDay)
	if err != nil {
		return nil, err
	}
	movies, err := s.store.movies(ctx, fromDay, toDay)
	if err != nil {
		return nil, err
	}
	if len(eps)+len(movies) == 0 {
		return []Item{}, nil
	}

	titleSet := map[int64]bool{}
	for _, e := range eps {
		titleSet[e.TitleID] = true
	}
	for _, m := range movies {
		titleSet[m.TitleID] = true
	}
	ids := make([]int64, 0, len(titleSet))
	for id := range titleSet {
		ids = append(ids, id)
	}
	slices.Sort(ids)

	epFiles, err := s.store.episodeFiles(ctx, ids)
	if err != nil {
		return nil, err
	}
	movieFiles, err := s.store.movieFiles(ctx, ids)
	if err != nil {
		return nil, err
	}
	dls, err := s.store.openDownloads(ctx, ids)
	if err != nil {
		return nil, err
	}
	var mine map[int64]bool
	if viewer.UserID > 0 {
		if mine, err = s.store.requestedBy(ctx, viewer.UserID); err != nil {
			return nil, err
		}
	}

	out := make([]Item, 0, len(eps)+len(movies))
	for _, e := range eps {
		if o.mine && !mine[e.TitleID] {
			continue
		}
		season, number := e.Season, e.Number
		it := Item{
			Date: e.AirDate, TitleID: e.TitleID, Kind: KindEpisode, Name: e.TitleName,
			Season: &season, Episode: &number, EpisodeName: e.Name,
			Label: fmt.Sprintf("S%02dE%02d", e.Season, e.Number), RequestedByMe: mine[e.TitleID],
		}
		switch {
		case epFiles[fileKey{e.TitleID, e.ID}]:
			it.State = StateAvailable
		case dls.coversEpisode(e.TitleID, e.ID, e.AirDate <= today):
			it.State = StateDownloading
			p := dls.progress(e.TitleID, e.ID)
			it.Progress = &p
		case e.AirDate >= today:
			it.State = StateExpected
		default:
			it.State = StateMissing
		}
		out = append(out, it)
	}
	for _, m := range movies {
		if o.mine && !mine[m.TitleID] {
			continue
		}
		it := Item{
			Date: m.ReleaseDate, TitleID: m.TitleID, Kind: KindMovie, Name: m.Name,
			Label: "Release", RequestedByMe: mine[m.TitleID],
		}
		switch {
		case movieFiles[m.TitleID]:
			it.State = StateAvailable
		case dls.coversTitle(m.TitleID):
			it.State = StateDownloading
			p := dls.progress(m.TitleID, 0)
			it.Progress = &p
		case m.ReleaseDate >= today:
			it.State = StateExpected
		default:
			it.State = StateMissing
		}
		out = append(out, it)
	}
	slices.SortStableFunc(out, func(a, b Item) int {
		if c := strings.Compare(a.Date, b.Date); c != 0 {
			return c
		}
		if c := strings.Compare(strings.ToLower(a.Name), strings.ToLower(b.Name)); c != 0 {
			return c
		}
		if c := cmpInt(deref(a.Season), deref(b.Season)); c != 0 {
			return c
		}
		return cmpInt(deref(a.Episode), deref(b.Episode))
	})
	return out, nil
}

func deref(p *int) int {
	if p == nil {
		return 0
	}
	return *p
}

func cmpInt(a, b int) int {
	switch {
	case a < b:
		return -1
	case a > b:
		return 1
	}
	return 0
}

// fileKey is a title and one of its episodes.
type fileKey struct{ title, episode int64 }

// download is an open download as the calendar needs it.
type download struct {
	TitleID    int64
	Kind       string
	EpisodeIDs []int64
	Progress   float64
}

type openDownloads []download

// coversEpisode says whether an open download is for the episode: it lists it, or
// it is a pack for the show (it lists no episodes) and the episode has aired.
func (d openDownloads) coversEpisode(title, episode int64, aired bool) bool {
	for _, x := range d {
		if x.TitleID != title {
			continue
		}
		if slices.Contains(x.EpisodeIDs, episode) || (len(x.EpisodeIDs) == 0 && x.Kind == KindShowDownload && aired) {
			return true
		}
	}
	return false
}

func (d openDownloads) coversTitle(title int64) bool {
	for _, x := range d {
		if x.TitleID == title {
			return true
		}
	}
	return false
}

// progress is the furthest any download covering the item has got.
func (d openDownloads) progress(title, episode int64) float64 {
	best := 0.0
	for _, x := range d {
		if x.TitleID != title {
			continue
		}
		if episode != 0 && len(x.EpisodeIDs) > 0 && !slices.Contains(x.EpisodeIDs, episode) {
			continue
		}
		best = max(best, x.Progress)
	}
	return min(max(best, 0), 1)
}

// KindShowDownload is the downloads module's kind for a show's download.
const KindShowDownload = "show"
