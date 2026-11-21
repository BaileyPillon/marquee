package mediaservers

import (
	"context"
	"fmt"
	"net/http"
	"net/url"
	"sort"
	"strconv"
	"strings"

	"github.com/BaileyPillon/marquee/internal/core/outbound"
	"github.com/BaileyPillon/marquee/internal/core/pathmap"
	"github.com/BaileyPillon/marquee/internal/titles"
)

// plexClient talks to a Plex Media Server.
type plexClient struct {
	c *caller
}

var _ adapter = (*plexClient)(nil)

func newPlex(hc *outbound.Client, base, token string) *plexClient {
	return &plexClient{c: &caller{hc: hc, base: base, headers: map[string]string{
		"X-Plex-Token": token,
		"Accept":       "application/json",
	}}}
}

const plexWho = "Plex"

type plexIdentity struct {
	MediaContainer struct {
		MachineIdentifier string `json:"machineIdentifier"`
		Version           string `json:"version"`
	} `json:"MediaContainer"`
}

func (p *plexClient) identity(ctx context.Context) (plexIdentity, error) {
	rep, err := p.c.do(ctx, http.MethodGet, "/identity", nil, nil, outbound.WithTimeout(callTimeout))
	if err != nil {
		return plexIdentity{}, err
	}
	if !rep.ok() {
		return plexIdentity{}, rep.err(plexWho)
	}
	var id plexIdentity
	if err := rep.json(plexWho, &id); err != nil {
		return plexIdentity{}, err
	}
	return id, nil
}

// plexSection is one library section and the folders it reads.
type plexSection struct {
	Key   string
	Title string
	Type  string
	Paths []string
}

func (p *plexClient) sections(ctx context.Context) ([]plexSection, error) {
	rep, err := p.c.do(ctx, http.MethodGet, "/library/sections", nil, nil, outbound.WithTimeout(callTimeout))
	if err != nil {
		return nil, err
	}
	if !rep.ok() {
		return nil, rep.err(plexWho)
	}
	var res struct {
		MediaContainer struct {
			Directory []struct {
				Key      string `json:"key"`
				Title    string `json:"title"`
				Type     string `json:"type"`
				Location []struct {
					Path string `json:"path"`
				} `json:"Location"`
			} `json:"Directory"`
		} `json:"MediaContainer"`
	}
	if err := rep.json(plexWho, &res); err != nil {
		return nil, err
	}
	out := make([]plexSection, 0, len(res.MediaContainer.Directory))
	for _, d := range res.MediaContainer.Directory {
		s := plexSection{Key: d.Key, Title: d.Title, Type: d.Type, Paths: []string{}}
		for _, l := range d.Location {
			s.Paths = append(s.Paths, l.Path)
		}
		out = append(out, s)
	}
	return out, nil
}

func (p *plexClient) test(ctx context.Context) (string, []Library, error) {
	id, err := p.identity(ctx)
	if err != nil {
		return "", nil, err
	}
	// /identity answers without a token; the section list is what checks it.
	secs, err := p.sections(ctx)
	if err != nil {
		return id.MediaContainer.Version, nil, err
	}
	libs := make([]Library, 0, len(secs))
	for _, s := range secs {
		libs = append(libs, Library{ID: s.Key, Name: s.Title, Paths: s.Paths})
	}
	return id.MediaContainer.Version, libs, nil
}

// sectionFor finds the section whose folders hold a path; the deepest folder
// wins when sections overlap.
func sectionFor(secs []plexSection, p string) (plexSection, bool) {
	var (
		best    plexSection
		bestLen = -1
	)
	for _, s := range secs {
		for _, loc := range s.Paths {
			if pathmap.Within(loc, p) && len(loc) > bestLen {
				best, bestLen = s, len(loc)
			}
		}
	}
	return best, bestLen >= 0
}

// notify refreshes the folder of every path in the section that holds it. A
// path no section holds makes the whole library refresh instead.
func (p *plexClient) notify(ctx context.Context, ev Event) error {
	secs, err := p.sections(ctx)
	if err != nil {
		return err
	}
	type target struct{ key, dir string }
	var (
		targets []target
		seen    = map[target]bool{}
		all     bool
	)
	for _, path := range ev.Paths {
		sec, ok := sectionFor(secs, path)
		if !ok {
			all = true
			continue
		}
		t := target{key: sec.Key, dir: pathmap.Dir(path)}
		if !seen[t] {
			seen[t] = true
			targets = append(targets, t)
		}
	}
	if all || len(ev.Paths) == 0 {
		for _, s := range secs {
			t := target{key: s.Key}
			if !seen[t] {
				seen[t] = true
				targets = append(targets, t)
			}
		}
	}
	sort.Slice(targets, func(i, j int) bool {
		if targets[i].key != targets[j].key {
			return targets[i].key < targets[j].key
		}
		return targets[i].dir < targets[j].dir
	})
	var failed error
	for _, t := range targets {
		q := url.Values{}
		if t.dir != "" {
			q.Set("path", t.dir)
		}
		rep, err := p.c.do(ctx, http.MethodGet, "/library/sections/"+url.PathEscape(t.key)+"/refresh", q, nil,
			outbound.WithTimeout(callTimeout))
		if err == nil && !rep.ok() {
			err = rep.err(plexWho)
		}
		if err != nil && failed == nil {
			failed = err
		}
	}
	return failed
}

func plexTypeFor(kind string) string {
	switch kind {
	case titles.KindShow:
		return "show"
	case titles.KindArtist:
		return "artist"
	}
	return "movie"
}

type plexItem struct {
	RatingKey string `json:"ratingKey"`
	Title     string `json:"title"`
	Year      int    `json:"year"`
	GUID      []struct {
		ID string `json:"id"`
	} `json:"Guid"`
}

// guidMatch reports whether an item carries one of the title's ids ("tmdb://603",
// "imdb://tt0133093").
func (it plexItem) guidMatch(q titleQuery) bool {
	for _, g := range it.GUID {
		scheme, val, ok := strings.Cut(strings.ToLower(g.ID), "://")
		if !ok || val == "" {
			continue
		}
		switch scheme {
		case "tmdb":
			if q.TMDB > 0 && val == strconv.FormatInt(q.TMDB, 10) {
				return true
			}
		case "tvmaze":
			if q.TVmaze > 0 && val == strconv.FormatInt(q.TVmaze, 10) {
				return true
			}
		case "imdb":
			if q.IMDb != "" && val == q.IMDb {
				return true
			}
		case "mbid":
			if q.MBID != "" && val == q.MBID {
				return true
			}
		}
	}
	return false
}

func (p *plexClient) open(ctx context.Context, q titleQuery) (string, bool, error) {
	id, err := p.identity(ctx)
	if err != nil {
		return "", false, err
	}
	secs, err := p.sections(ctx)
	if err != nil {
		return "", false, err
	}
	want := plexTypeFor(q.Kind)
	var byName *plexItem
	for _, sec := range secs {
		if sec.Type != want {
			continue
		}
		query := url.Values{}
		query.Set("title", q.Name)
		query.Set("includeGuids", "1")
		rep, err := p.c.do(ctx, http.MethodGet, "/library/sections/"+url.PathEscape(sec.Key)+"/all", query, nil,
			outbound.WithTimeout(openTimeout))
		if err != nil {
			return "", false, err
		}
		if !rep.ok() {
			return "", false, rep.err(plexWho)
		}
		var res struct {
			MediaContainer struct {
				Metadata []plexItem `json:"Metadata"`
			} `json:"MediaContainer"`
		}
		if err := rep.json(plexWho, &res); err != nil {
			return "", false, err
		}
		for i := range res.MediaContainer.Metadata {
			it := res.MediaContainer.Metadata[i]
			if it.RatingKey == "" {
				continue
			}
			if it.guidMatch(q) {
				return plexPage(id.MediaContainer.MachineIdentifier, it.RatingKey), true, nil
			}
			if byName == nil && nameMatches(q, it.Title, it.Year) {
				byName = &it
			}
		}
	}
	if byName != nil {
		return plexPage(id.MediaContainer.MachineIdentifier, byName.RatingKey), true, nil
	}
	return "", false, nil
}

// plexPage is the address of an item in Plex's web app.
func plexPage(machineID, ratingKey string) string {
	return fmt.Sprintf("https://app.plex.tv/desktop#!/server/%s/details?key=%s",
		machineID, url.QueryEscape("/library/metadata/"+ratingKey))
}
