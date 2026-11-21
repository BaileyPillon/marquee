package titles

import (
	"strings"

	"github.com/BaileyPillon/marquee/internal/metadata"
)

// The functions here shape provider results into catalog rows. They hold no
// state and make no calls.

func badAnswer() error {
	return &metadata.Error{Code: metadata.ErrUpstream.Code, Message: "The provider sent a title with no name."}
}

func newDetail(kind string, opts AddOptions) *Detail {
	return &Detail{
		Title:   Title{Kind: kind, Monitored: opts.Monitored, RootFolder: opts.RootFolder, AltNames: []string{}, Genres: []string{}},
		Seasons: []Season{}, Episodes: []Episode{}, Albums: []Album{}, People: []Person{},
	}
}

// sortName is the name lower-cased with a leading article dropped, so "The
// Americans" files under A.
func sortName(name string) string {
	n := strings.ToLower(strings.TrimSpace(name))
	for _, article := range []string{"the ", "an ", "a "} {
		if strings.HasPrefix(n, article) && len(n) > len(article) {
			return n[len(article):]
		}
	}
	return n
}

// altNames lists the other names, leaving out empty ones, the main name and
// repeats (ignoring case).
func altNames(main string, others ...string) []string {
	seen := map[string]bool{strings.ToLower(main): true}
	out := []string{}
	for _, o := range others {
		o = strings.TrimSpace(o)
		k := strings.ToLower(o)
		if o == "" || seen[k] {
			continue
		}
		seen[k] = true
		out = append(out, o)
	}
	return out
}

func people(in []metadata.Person) []Person {
	out := make([]Person, 0, len(in))
	for _, p := range in {
		if strings.TrimSpace(p.Name) == "" {
			continue
		}
		out = append(out, Person{Name: p.Name, Role: p.Role, Job: p.Job, ImageRef: p.Image.Ref})
	}
	return out
}

func strs(in []string) []string {
	if in == nil {
		return []string{}
	}
	return in
}

func fromMovie(m *metadata.MovieInfo, opts AddOptions) (*Detail, error) {
	if m == nil || strings.TrimSpace(m.Title) == "" {
		return nil, badAnswer()
	}
	d := newDetail(KindMovie, opts)
	d.TMDBID, d.IMDbID = m.TMDBID, m.IMDbID
	d.Name, d.SortName = m.Title, sortName(m.Title)
	d.AltNames = altNames(m.Title, m.OriginalTitle)
	d.Year, d.Overview, d.Tagline = m.Year, m.Overview, m.Tagline
	d.Certification, d.TrailerKey = m.Certification, m.TrailerKey
	d.RuntimeMin, d.Genres, d.Status = m.RuntimeMin, strs(m.Genres), m.Status
	d.PosterRef, d.BackdropRef = m.Poster.Ref, m.Backdrop.Ref
	d.People = people(m.People)
	return d, nil
}

func fromShow(s *metadata.ShowInfo, opts AddOptions) (*Detail, error) {
	if s == nil || strings.TrimSpace(s.Name) == "" {
		return nil, badAnswer()
	}
	d := newDetail(KindShow, opts)
	d.TVmazeID, d.IMDbID = s.TVmazeID, s.IMDbID
	d.Name, d.SortName = s.Name, sortName(s.Name)
	d.Year, d.Overview = s.Year, s.Overview
	d.RuntimeMin, d.Genres, d.Status = s.RuntimeMin, strs(s.Genres), s.Status
	d.PosterRef, d.BackdropRef = s.Poster.Ref, s.Backdrop.Ref
	d.People = people(s.People)

	// New seasons and episodes are wanted if the show is, except specials
	// (season 0), which are opt-in.
	haveSeason := map[int]bool{}
	addSeason := func(n int) {
		if haveSeason[n] {
			return
		}
		haveSeason[n] = true
		d.Seasons = append(d.Seasons, Season{Number: n, Monitored: opts.Monitored && n > 0})
	}
	for _, se := range s.Seasons {
		addSeason(se.Number)
	}
	type epKey struct{ season, number int }
	haveEpisode := map[epKey]bool{}
	for _, e := range s.Episodes {
		if haveEpisode[epKey{e.Season, e.Number}] {
			continue // a provider listing one twice must not make the title unaddable
		}
		haveEpisode[epKey{e.Season, e.Number}] = true
		addSeason(e.Season)
		d.Episodes = append(d.Episodes, Episode{
			Season: e.Season, Number: e.Number, Name: e.Name, AirDate: e.AirDate,
			Overview: e.Overview, RuntimeMin: e.RuntimeMin, Monitored: opts.Monitored && e.Season > 0,
		})
	}
	return d, nil
}

func fromArtist(a *metadata.ArtistInfo, opts AddOptions) (*Detail, error) {
	if a == nil || strings.TrimSpace(a.Name) == "" || a.MBID == "" {
		return nil, badAnswer()
	}
	d := newDetail(KindArtist, opts)
	d.MBID = a.MBID
	d.Name = a.Name
	if sn := strings.ToLower(strings.TrimSpace(a.SortName)); sn != "" {
		d.SortName = sn
	} else {
		d.SortName = sortName(a.Name)
	}
	d.AltNames = altNames(a.Name, a.AltNames...)
	d.Year, d.Overview, d.Genres = a.Year, a.Disambiguation, strs(a.Genres)
	d.Status = "active"
	if a.Ended {
		d.Status = "ended"
	}

	newest := -1
	for _, al := range a.Albums {
		if strings.TrimSpace(al.Title) == "" || al.MBID == "" {
			continue
		}
		album := Album{
			MBID: al.MBID, Name: al.Title, Year: al.Year, Type: al.Type,
			Monitored: opts.Monitored, CoverRef: al.Cover.Ref, Tracks: []Track{},
		}
		if album.Type == "" {
			album.Type = "album"
		}
		for _, t := range al.Tracks {
			album.Tracks = append(album.Tracks, Track{
				Disc: t.Disc, Position: t.Position, Name: t.Title, LengthMS: t.LengthMS, MBID: t.MBID,
			})
		}
		if al.Year > newest && al.Cover.Ref != "" {
			// MusicBrainz has no artist photos. The newest cover stands in for one.
			newest, d.PosterRef = al.Year, al.Cover.Ref
		}
		d.Albums = append(d.Albums, album)
	}
	return d, nil
}
