package metadata

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"regexp"
	"sort"
	"strconv"
	"strings"

	"github.com/BaileyPillon/marquee/internal/core/outbound"
)

// DefaultTVmazeURL is TVmaze's public API. It needs no key.
const DefaultTVmazeURL = "https://api.tvmaze.com"

// TVmaze documents "at least 20 calls every 10 seconds" per address. The
// bucket refills at that average and allows a short burst.
const (
	tvmazeRPS   = 2.0
	tvmazeBurst = 5
	maxCast     = 30
)

var imdbID = regexp.MustCompile(`^tt[0-9]{6,10}$`)

// TVmaze looks up shows.
type TVmaze struct {
	g     getter
	cache *Cache
	base  string
}

func newTVmaze(hc *outbound.Client, cache *Cache, rawBase string) (*TVmaze, error) {
	u, err := baseURL(rawBase)
	if err != nil {
		return nil, err
	}
	hc.SetHostLimit(u.Host, tvmazeRPS, tvmazeBurst)
	return &TVmaze{g: getter{hc: hc, provider: ProviderTVmaze}, cache: cache, base: u.String()}, nil
}

// ---- TVmaze JSON -----------------------------------------------------------

type tvImage struct {
	Medium   string `json:"medium"`
	Original string `json:"original"`
}

type tvShow struct {
	ID             int64    `json:"id"`
	Name           string   `json:"name"`
	Language       string   `json:"language"`
	Status         string   `json:"status"`
	Runtime        *int     `json:"runtime"`
	AverageRuntime *int     `json:"averageRuntime"`
	Premiered      string   `json:"premiered"`
	Genres         []string `json:"genres"`
	Summary        string   `json:"summary"`
	Image          *tvImage `json:"image"`
	Externals      struct {
		IMDb    string `json:"imdb"`
		TheTVDB *int64 `json:"thetvdb"`
	} `json:"externals"`
	Network *struct {
		Name string `json:"name"`
	} `json:"network"`
	WebChannel *struct {
		Name string `json:"name"`
	} `json:"webChannel"`
	Embedded *struct {
		Seasons  []tvSeason  `json:"seasons"`
		Episodes []tvEpisode `json:"episodes"`
		Cast     []tvCast    `json:"cast"`
	} `json:"_embedded"`
}

type tvSeason struct {
	Number       int      `json:"number"`
	Name         string   `json:"name"`
	EpisodeOrder *int     `json:"episodeOrder"`
	PremiereDate string   `json:"premiereDate"`
	EndDate      string   `json:"endDate"`
	Image        *tvImage `json:"image"`
}

type tvEpisode struct {
	Name    string `json:"name"`
	Season  int    `json:"season"`
	Number  *int   `json:"number"`
	Type    string `json:"type"`
	Airdate string `json:"airdate"`
	Runtime *int   `json:"runtime"`
	Summary string `json:"summary"`
}

type tvCast struct {
	Person struct {
		Name  string   `json:"name"`
		Image *tvImage `json:"image"`
	} `json:"person"`
	Character struct {
		Name string `json:"name"`
	} `json:"character"`
}

// ---- ShowProvider ----------------------------------------------------------

// SearchShows searches by name.
func (t *TVmaze) SearchShows(ctx context.Context, query string) ([]SearchHit, error) {
	q := strings.TrimSpace(query)
	if q == "" {
		return nil, nil
	}
	body, err := t.cache.fetch(ctx, ProviderTVmaze, "search:"+strings.ToLower(q), SearchTTL, false,
		func(ctx context.Context) ([]byte, error) {
			return t.g.get(ctx, t.base+"/search/shows?q="+url.QueryEscape(q), nil)
		})
	if err != nil {
		return nil, err
	}
	var raw []struct {
		Show tvShow `json:"show"`
	}
	if err := json.Unmarshal(body, &raw); err != nil {
		return nil, upstream(ProviderTVmaze, fmt.Errorf("unreadable search answer: %w", err))
	}
	hits := make([]SearchHit, 0, len(raw))
	for _, r := range raw {
		hits = append(hits, r.Show.hit())
	}
	return hits, nil
}

// Show returns a show with its seasons, episodes and cast.
func (t *TVmaze) Show(ctx context.Context, id string) (*ShowInfo, error) {
	n, err := strconv.ParseInt(strings.TrimSpace(id), 10, 64)
	if err != nil || n <= 0 {
		return nil, notFound(ProviderTVmaze)
	}
	body, err := t.cache.fetch(ctx, ProviderTVmaze, "show:"+strconv.FormatInt(n, 10), DetailsTTL, false,
		func(ctx context.Context) ([]byte, error) {
			return t.g.get(ctx, t.base+"/shows/"+strconv.FormatInt(n, 10)+"?embed[]=seasons&embed[]=episodes&embed[]=cast", nil)
		})
	if err != nil {
		return nil, err
	}
	var raw tvShow
	if err := json.Unmarshal(body, &raw); err != nil {
		return nil, upstream(ProviderTVmaze, fmt.Errorf("unreadable show answer: %w", err))
	}
	return raw.info(), nil
}

// ShowByIMDb finds a show from its IMDb id. TVmaze answers with a redirect to
// the show, which the client follows.
func (t *TVmaze) ShowByIMDb(ctx context.Context, id string) (*SearchHit, error) {
	id = strings.ToLower(strings.TrimSpace(id))
	if !imdbID.MatchString(id) {
		return nil, notFound(ProviderTVmaze)
	}
	body, err := t.cache.fetch(ctx, ProviderTVmaze, "lookup:imdb:"+id, DetailsTTL, false,
		func(ctx context.Context) ([]byte, error) {
			return t.g.get(ctx, t.base+"/lookup/shows?imdb="+url.QueryEscape(id), http.Header{})
		})
	if err != nil {
		return nil, err
	}
	var raw tvShow
	if err := json.Unmarshal(body, &raw); err != nil {
		return nil, upstream(ProviderTVmaze, fmt.Errorf("unreadable lookup answer: %w", err))
	}
	h := raw.hit()
	return &h, nil
}

// ---- mapping ---------------------------------------------------------------

func (s tvShow) networkName() string {
	switch {
	case s.Network != nil && s.Network.Name != "":
		return s.Network.Name
	case s.WebChannel != nil:
		return s.WebChannel.Name
	}
	return ""
}

func (s tvShow) hit() SearchHit {
	return SearchHit{
		Kind: KindShow, Provider: ProviderTVmaze, ProviderID: strconv.FormatInt(s.ID, 10),
		Name: cleanText(s.Name), Year: year(s.Premiered),
		Overview: truncate(stripHTML(s.Summary), 400),
		Poster:   tvmazeImage(s.Image, "poster"),
		IMDbID:   s.Externals.IMDb,
		Detail:   s.networkName(),
	}
}

func (s tvShow) info() *ShowInfo {
	info := &ShowInfo{
		TVmazeID: s.ID, IMDbID: s.Externals.IMDb,
		Name: cleanText(s.Name), Year: year(s.Premiered), Premiered: s.Premiered,
		Overview: stripHTML(s.Summary),
		Genres:   nonNil(s.Genres),
		Status:   showStatus(s.Status),
		Network:  s.networkName(), Language: s.Language,
		Poster:  tvmazeImage(s.Image, "poster"),
		Seasons: []SeasonInfo{}, Episodes: []EpisodeInfo{}, People: []Person{},
	}
	if s.Externals.TheTVDB != nil {
		info.TheTVDBID = *s.Externals.TheTVDB
	}
	switch {
	case s.Runtime != nil:
		info.RuntimeMin = *s.Runtime
	case s.AverageRuntime != nil:
		info.RuntimeMin = *s.AverageRuntime
	}
	if s.Embedded == nil {
		return info
	}

	// Episodes. A special has no number on TVmaze; Marquee files them as
	// season 0 and numbers them in the order TVmaze lists them.
	type key struct{ season, number int }
	seen := map[key]bool{}
	specials := 0
	for _, e := range s.Embedded.Episodes {
		ep := EpisodeInfo{
			Name: cleanText(e.Name), AirDate: e.Airdate, Overview: stripHTML(e.Summary),
			Season: e.Season,
		}
		if e.Runtime != nil {
			ep.RuntimeMin = *e.Runtime
		}
		if e.Number != nil {
			ep.Number = *e.Number
		} else {
			specials++
			ep.Season, ep.Number, ep.Special = 0, specials, true
		}
		if e.Type != "" && e.Type != "regular" {
			ep.Special = true
		}
		k := key{ep.Season, ep.Number}
		if seen[k] || ep.Number <= 0 && !ep.Special {
			continue
		}
		seen[k] = true
		info.Episodes = append(info.Episodes, ep)
	}
	sort.SliceStable(info.Episodes, func(i, j int) bool {
		a, b := info.Episodes[i], info.Episodes[j]
		if a.Season != b.Season {
			return a.Season < b.Season
		}
		return a.Number < b.Number
	})

	// Seasons: the ones TVmaze lists, plus season 0 for specials.
	counts := map[int]int{}
	for _, e := range info.Episodes {
		counts[e.Season]++
	}
	listed := map[int]bool{}
	for _, se := range s.Embedded.Seasons {
		sn := SeasonInfo{
			Number: se.Number, Name: cleanText(se.Name),
			EpisodeCount: counts[se.Number], PremiereDate: se.PremiereDate, EndDate: se.EndDate,
			Poster: tvmazeImage(se.Image, "poster"),
		}
		if sn.EpisodeCount == 0 && se.EpisodeOrder != nil {
			sn.EpisodeCount = *se.EpisodeOrder
		}
		info.Seasons = append(info.Seasons, sn)
		listed[se.Number] = true
	}
	for n, c := range counts {
		if !listed[n] {
			info.Seasons = append(info.Seasons, SeasonInfo{Number: n, EpisodeCount: c})
		}
	}
	sort.Slice(info.Seasons, func(i, j int) bool { return info.Seasons[i].Number < info.Seasons[j].Number })

	for i, c := range s.Embedded.Cast {
		if i >= maxCast {
			break
		}
		if c.Person.Name == "" {
			continue
		}
		info.People = append(info.People, Person{
			Name: cleanText(c.Person.Name), Role: "actor", Job: cleanText(c.Character.Name),
			Image: tvmazeImage(c.Person.Image, "profile"),
		})
	}
	return info
}

func showStatus(s string) string {
	switch strings.ToLower(strings.TrimSpace(s)) {
	case "running":
		return "continuing"
	case "ended":
		return "ended"
	case "to be determined", "in development":
		return "upcoming"
	}
	return strings.ToLower(strings.TrimSpace(s))
}

// tvmazeImage turns a TVmaze image into a proxy ref. Only images on TVmaze's
// own image host can be proxied.
func tvmazeImage(img *tvImage, kind string) Image {
	if img == nil {
		return Image{}
	}
	raw := img.Original
	if raw == "" {
		raw = img.Medium
	}
	u, err := url.Parse(raw)
	if err != nil || u.Host != "static.tvmaze.com" {
		return Image{}
	}
	p := strings.TrimPrefix(u.Path, "/")
	if p == "" {
		return Image{}
	}
	return Image{Ref: ProviderTVmaze + "/" + p, Kind: kind}
}

func nonNil(s []string) []string {
	if s == nil {
		return []string{}
	}
	return s
}

// truncate cuts s to at most n runes, ending in an ellipsis if it was cut.
func truncate(s string, n int) string {
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	return strings.TrimSpace(string(r[:n-1])) + "…"
}
