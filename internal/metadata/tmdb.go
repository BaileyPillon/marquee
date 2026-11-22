package metadata

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/BaileyPillon/marquee/internal/core/outbound"
)

// DefaultTMDBURL is TMDB's v3 API.
const DefaultTMDBURL = "https://api.themoviedb.org/3"

// KeyFunc returns the owner's TMDB credential, or "" when none is set. It is
// called for every request, so a key saved in Settings works at once.
type KeyFunc func(ctx context.Context) (string, error)

// TMDB looks up movies (and, for later milestones, shows and external ids)
// with the owner's own key. It accepts both kinds of credential TMDB issues:
// the long v4 "read access token", sent as a Bearer header, and the short v3
// "API key", sent as the api_key query parameter. The key is never logged, put
// in an error, or used in a cache key.
type TMDB struct {
	g     getter
	cache *Cache
	base  string
	key   KeyFunc
}

func newTMDB(hc *outbound.Client, cache *Cache, rawBase string, key KeyFunc) (*TMDB, error) {
	u, err := baseURL(rawBase)
	if err != nil {
		return nil, err
	}
	if key == nil {
		key = func(context.Context) (string, error) { return "", nil }
	}
	return &TMDB{g: getter{hc: hc, provider: ProviderTMDB}, cache: cache, base: u.String(), key: key}, nil
}

// HasKey reports whether a TMDB credential is set.
func (t *TMDB) HasKey(ctx context.Context) bool {
	k, err := t.key(ctx)
	return err == nil && strings.TrimSpace(k) != ""
}

// isV4Token tells a v4 read-access token (a JWT) from a v3 key (32 hex digits).
func isV4Token(k string) bool { return strings.HasPrefix(k, "eyJ") || strings.Count(k, ".") == 2 }

// fetch calls path through the cache. q holds the query parameters without the
// credential, which is added only to the request that goes out.
func (t *TMDB) fetch(ctx context.Context, path string, q url.Values, ttl time.Duration) ([]byte, error) {
	key, err := t.key(ctx)
	if err != nil {
		return nil, fmt.Errorf("metadata: reading the TMDB key: %w", err)
	}
	key = strings.TrimSpace(key)
	if key == "" {
		return nil, ErrTMDBKeyMissing
	}
	cacheKey := path
	if enc := q.Encode(); enc != "" {
		cacheKey += "?" + enc // q holds no credential yet
	}
	return t.cache.fetch(ctx, ProviderTMDB, cacheKey, ttl, false, func(ctx context.Context) ([]byte, error) {
		full := url.Values{}
		for k, v := range q {
			full[k] = v
		}
		header := http.Header{}
		if isV4Token(key) {
			header.Set("Authorization", "Bearer "+key)
		} else {
			full.Set("api_key", key)
		}
		target := t.base + path
		if enc := full.Encode(); enc != "" {
			target += "?" + enc
		}
		body, err := t.g.get(ctx, target, header)
		var se *statusError
		if errors.As(err, &se) && (se.Status == http.StatusUnauthorized || se.Status == http.StatusForbidden) {
			return nil, ErrTMDBKeyInvalid
		}
		return body, err
	})
}

// ---- TMDB JSON -------------------------------------------------------------

type tmHit struct {
	ID            int64  `json:"id"`
	Title         string `json:"title"`
	Name          string `json:"name"`
	OriginalTitle string `json:"original_title"`
	ReleaseDate   string `json:"release_date"`
	FirstAirDate  string `json:"first_air_date"`
	Overview      string `json:"overview"`
	PosterPath    string `json:"poster_path"`
}

type tmMovie struct {
	ID            int64  `json:"id"`
	IMDbID        string `json:"imdb_id"`
	Title         string `json:"title"`
	OriginalTitle string `json:"original_title"`
	ReleaseDate   string `json:"release_date"`
	Overview      string `json:"overview"`
	Tagline       string `json:"tagline"`
	Runtime       *int   `json:"runtime"`
	Status        string `json:"status"`
	PosterPath    string `json:"poster_path"`
	BackdropPath  string `json:"backdrop_path"`
	Genres        []struct {
		Name string `json:"name"`
	} `json:"genres"`
	Credits struct {
		Cast []struct {
			Name        string `json:"name"`
			Character   string `json:"character"`
			ProfilePath string `json:"profile_path"`
			Order       int    `json:"order"`
		} `json:"cast"`
		Crew []struct {
			Name        string `json:"name"`
			Job         string `json:"job"`
			ProfilePath string `json:"profile_path"`
		} `json:"crew"`
	} `json:"credits"`
	ReleaseDates struct {
		Results []struct {
			Country  string `json:"iso_3166_1"`
			Releases []struct {
				Certification string `json:"certification"`
				Type          int    `json:"type"`
				ReleaseDate   string `json:"release_date"` // an ISO time such as 2021-10-29T00:00:00.000Z
			} `json:"release_dates"`
		} `json:"results"`
	} `json:"release_dates"`
	Videos struct {
		Results []struct {
			Key      string `json:"key"`
			Site     string `json:"site"`
			Type     string `json:"type"`
			Official bool   `json:"official"`
		} `json:"results"`
	} `json:"videos"`
}

// ---- MovieProvider ---------------------------------------------------------

// SearchMovies searches by title; year (0 = any) narrows the result.
func (t *TMDB) SearchMovies(ctx context.Context, query string, yr int) ([]SearchHit, error) {
	return t.search(ctx, "/search/movie", KindMovie, query, "year", yr)
}

// SearchTV searches shows by name. Marquee's own show lookups use TVmaze; this
// is here for mapping TMDB ids (watchlists, requests) in later milestones.
func (t *TMDB) SearchTV(ctx context.Context, query string, yr int) ([]SearchHit, error) {
	return t.search(ctx, "/search/tv", KindShow, query, "first_air_date_year", yr)
}

func (t *TMDB) search(ctx context.Context, path, kind, query, yearParam string, yr int) ([]SearchHit, error) {
	q := strings.TrimSpace(query)
	if q == "" {
		return nil, nil
	}
	v := url.Values{"query": {q}, "include_adult": {"false"}}
	if yr > 0 {
		v.Set(yearParam, strconv.Itoa(yr))
	}
	body, err := t.fetch(ctx, path, v, SearchTTL)
	if err != nil {
		return nil, err
	}
	var raw struct {
		Results []tmHit `json:"results"`
	}
	if err := json.Unmarshal(body, &raw); err != nil {
		return nil, upstream(ProviderTMDB, fmt.Errorf("unreadable search answer: %w", err))
	}
	hits := make([]SearchHit, 0, len(raw.Results))
	for _, r := range raw.Results {
		name, date := r.Title, r.ReleaseDate
		if kind == KindShow {
			name, date = r.Name, r.FirstAirDate
		}
		hits = append(hits, SearchHit{
			Kind: kind, Provider: ProviderTMDB, ProviderID: strconv.FormatInt(r.ID, 10),
			Name: cleanText(name), Year: year(date),
			Overview: truncate(cleanText(r.Overview), 400),
			Poster:   tmdbImage(r.PosterPath, "w500", "poster"),
		})
	}
	return hits, nil
}

// Movie returns a movie with its cast, crew, rating and trailer.
func (t *TMDB) Movie(ctx context.Context, id string) (*MovieInfo, error) {
	n, err := strconv.ParseInt(strings.TrimSpace(id), 10, 64)
	if err != nil || n <= 0 {
		return nil, notFound(ProviderTMDB)
	}
	body, err := t.fetch(ctx, "/movie/"+strconv.FormatInt(n, 10),
		url.Values{"append_to_response": {"credits,release_dates,videos"}}, DetailsTTL)
	if err != nil {
		return nil, err
	}
	var raw tmMovie
	if err := json.Unmarshal(body, &raw); err != nil {
		return nil, upstream(ProviderTMDB, fmt.Errorf("unreadable movie answer: %w", err))
	}
	return raw.info(), nil
}

// ExternalIDs returns the IMDb (and, for shows, TheTVDB) id of a TMDB title.
// kind is KindMovie or KindShow.
func (t *TMDB) ExternalIDs(ctx context.Context, kind, id string) (*ExternalIDs, error) {
	n, err := strconv.ParseInt(strings.TrimSpace(id), 10, 64)
	if err != nil || n <= 0 {
		return nil, notFound(ProviderTMDB)
	}
	seg := "movie"
	if kind == KindShow {
		seg = "tv"
	}
	body, err := t.fetch(ctx, "/"+seg+"/"+strconv.FormatInt(n, 10)+"/external_ids", url.Values{}, DetailsTTL)
	if err != nil {
		return nil, err
	}
	var raw struct {
		IMDbID string `json:"imdb_id"`
		TVDBID int64  `json:"tvdb_id"`
	}
	if err := json.Unmarshal(body, &raw); err != nil {
		return nil, upstream(ProviderTMDB, fmt.Errorf("unreadable external ids: %w", err))
	}
	return &ExternalIDs{IMDbID: raw.IMDbID, TVDBID: raw.TVDBID}, nil
}

// ---- mapping ---------------------------------------------------------------

func (m tmMovie) info() *MovieInfo {
	info := &MovieInfo{
		TMDBID: m.ID, IMDbID: m.IMDbID,
		Title: cleanText(m.Title), OriginalTitle: cleanText(m.OriginalTitle),
		Year: m.firstReleaseYear(), ReleaseDate: m.ReleaseDate,
		Overview: cleanText(m.Overview), Tagline: cleanText(m.Tagline),
		Status:   movieStatus(m.Status),
		Genres:   []string{},
		Poster:   tmdbImage(m.PosterPath, "w500", "poster"),
		Backdrop: tmdbImage(m.BackdropPath, "w1280", "backdrop"),
		People:   []Person{},
	}
	if m.Runtime != nil {
		info.RuntimeMin = *m.Runtime
	}
	for _, g := range m.Genres {
		if g.Name != "" {
			info.Genres = append(info.Genres, g.Name)
		}
	}

	// Cast in billing order, then the director and writers.
	cast := m.Credits.Cast
	sort.SliceStable(cast, func(i, j int) bool { return cast[i].Order < cast[j].Order })
	for i, c := range cast {
		if i >= maxCast {
			break
		}
		info.People = append(info.People, Person{
			Name: cleanText(c.Name), Role: "actor", Job: cleanText(c.Character),
			Image: tmdbImage(c.ProfilePath, "w185", "profile"),
		})
	}
	for _, c := range m.Credits.Crew {
		role := ""
		switch c.Job {
		case "Director":
			role = "director"
		case "Screenplay", "Writer", "Story", "Novel":
			role = "writer"
		default:
			continue
		}
		info.People = append(info.People, Person{
			Name: cleanText(c.Name), Role: role, Job: c.Job,
			Image: tmdbImage(c.ProfilePath, "w185", "profile"),
		})
	}

	info.Certification = m.usCertification()
	info.TrailerKey = m.trailerKey()
	return info
}

// earliestEntryYear is the oldest year a release_dates entry can plausibly carry.
// TMDB has the odd placeholder ("0001-01-01") that must not become a film's year.
const earliestEntryYear = 1870

// firstReleaseYear is the year a film first came out: the earliest of the
// primary release date and every dated premiere (1), limited theatrical (2),
// theatrical (3), digital (4) or physical (5) release in any country. TMDB's
// primary date is often a later wide release: "Sprite Fright" premiered in
// October 2021 but its primary date is 2024. Release names carry the first
// year, so that is the year the catalog keeps. TV and other release types (6)
// are left out because they say nothing about when the film first came out.
func (m tmMovie) firstReleaseYear() int {
	first := year(m.ReleaseDate)
	for _, res := range m.ReleaseDates.Results {
		for _, r := range res.Releases {
			if r.Type < 1 || r.Type > 5 {
				continue
			}
			y := year(r.ReleaseDate)
			if y < earliestEntryYear {
				continue
			}
			if first == 0 || y < first {
				first = y
			}
		}
	}
	return first
}

// usCertification picks the US rating of the theatrical release (type 3),
// else any US rating.
func (m tmMovie) usCertification() string {
	best := ""
	for _, res := range m.ReleaseDates.Results {
		if res.Country != "US" {
			continue
		}
		for _, r := range res.Releases {
			if r.Certification == "" {
				continue
			}
			if r.Type == 3 {
				return r.Certification
			}
			if best == "" {
				best = r.Certification
			}
		}
	}
	return best
}

// trailerKey picks a YouTube trailer, an official one if there is one.
func (m tmMovie) trailerKey() string {
	var fallback string
	for _, v := range m.Videos.Results {
		if v.Site != "YouTube" || v.Key == "" {
			continue
		}
		switch {
		case v.Type == "Trailer" && v.Official:
			return v.Key
		case v.Type == "Trailer" && fallback == "":
			fallback = v.Key
		}
	}
	return fallback
}

func movieStatus(s string) string {
	switch strings.ToLower(strings.TrimSpace(s)) {
	case "released":
		return "released"
	case "canceled", "cancelled":
		return "canceled"
	case "":
		return ""
	}
	return "upcoming"
}

// tmdbImage turns a TMDB file path ("/abc.jpg") into a proxy ref.
func tmdbImage(path, size, kind string) Image {
	p := strings.TrimPrefix(strings.TrimSpace(path), "/")
	if p == "" || strings.Contains(p, "/") {
		return Image{}
	}
	return Image{Ref: ProviderTMDB + "/" + size + "/" + p, Kind: kind}
}
