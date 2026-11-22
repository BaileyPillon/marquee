package metadata

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"slices"
	"strings"
	"sync/atomic"
	"testing"
)

const (
	v3Key   = "0123456789abcdef0123456789abcdef"
	v4Token = "eyJhbGciOiJIUzI1NiJ9.eyJhdWQiOiJ0ZXN0In0.c2lnbmF0dXJlLWZvci10ZXN0cw"
)

func fixedKey(k string) KeyFunc { return func(context.Context) (string, error) { return k, nil } }

func tmdbEnv(t *testing.T, key string) (*env, *fakeAPI) {
	t.Helper()
	api := newFakeAPI(t, map[string]http.HandlerFunc{
		"/search/movie":          fixture(t, "tmdb_search_movie_metropolis.json"),
		"/search/tv":             fixture(t, "tmdb_search_tv_pioneer.json"),
		"/movie/19":              fixture(t, "tmdb_movie_metropolis.json"),
		"/tv/70001/external_ids": fixture(t, "tmdb_external_ids.json"),
	})
	return newEnv(t, Options{TMDBURL: api.URL(), TMDBKey: fixedKey(key)}), api
}

func TestTMDBWithoutKeyFailsWithCodeAndNeverCallsOut(t *testing.T) {
	e, api := tmdbEnv(t, "")
	for name, call := range map[string]func() error{
		"search": func() error { _, err := e.svc.TMDB.SearchMovies(bg(), "Metropolis", 0); return err },
		"movie":  func() error { _, err := e.svc.TMDB.Movie(bg(), "19"); return err },
		"tv":     func() error { _, err := e.svc.TMDB.SearchTV(bg(), "Pioneer One", 0); return err },
		"ids":    func() error { _, err := e.svc.TMDB.ExternalIDs(bg(), KindShow, "70001"); return err },
	} {
		err := call()
		if !errors.Is(err, ErrTMDBKeyMissing) || CodeOf(err) != "tmdb_key_missing" {
			t.Errorf("%s: err = %v, want tmdb_key_missing", name, err)
		}
	}
	if api.Total() != 0 {
		t.Errorf("%d requests went out without a key", api.Total())
	}
	if rep := e.svc.Status(bg()); rep.HasKey {
		t.Error("Status.HasKey is true with no key")
	}
	// A key that is only spaces is no key.
	e2, _ := tmdbEnv(t, "   ")
	if _, err := e2.svc.TMDB.SearchMovies(bg(), "x", 0); !errors.Is(err, ErrTMDBKeyMissing) {
		t.Errorf("blank key: err = %v", err)
	}
}

func TestTMDBV3KeyIsAQueryParameterAndNeverLogged(t *testing.T) {
	e, api := tmdbEnv(t, v3Key)
	if _, err := e.svc.TMDB.SearchMovies(bg(), "Metropolis", 1927); err != nil {
		t.Fatal(err)
	}
	if _, err := e.svc.TMDB.Movie(bg(), "19"); err != nil {
		t.Fatal(err)
	}
	// A failing call as well, so error paths are covered by the log check.
	api.Set("/movie/20", httpStatus(500))
	if _, err := e.svc.TMDB.Movie(bg(), "20"); !errors.Is(err, ErrUpstream) {
		t.Fatalf("500: %v", err)
	}

	call := api.Last("/search/movie")
	if !strings.Contains(call.RawQuery, "api_key="+v3Key) {
		t.Errorf("v3 key not sent as api_key: %q", call.RawQuery)
	}
	if call.Header.Get("Authorization") != "" {
		t.Errorf("a v3 key must not be sent as a header: %q", call.Header.Get("Authorization"))
	}
	for _, want := range []string{"query=Metropolis", "year=1927", "include_adult=false"} {
		if !strings.Contains(call.RawQuery, want) {
			t.Errorf("query %q lacks %q", call.RawQuery, want)
		}
	}

	// The key is nowhere we keep text: not the log, not the cache keys, not
	// the status report.
	if strings.Contains(e.logs.String(), v3Key) {
		t.Errorf("the TMDB key is in the log:\n%s", e.logs.String())
	}
	if !strings.Contains(e.logs.String(), "host=") {
		t.Error("expected outbound debug lines in the log, found none (the check above proves nothing)")
	}
	rows, err := e.db.Query(`SELECT provider || ' ' || key FROM metadata_cache`)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	n := 0
	for rows.Next() {
		var k string
		_ = rows.Scan(&k)
		n++
		if strings.Contains(k, v3Key) || strings.Contains(k, "api_key") {
			t.Errorf("cache key carries the credential: %q", k)
		}
	}
	if n == 0 {
		t.Fatal("nothing was cached")
	}
	st := e.svc.Status(bg())
	if !st.HasKey {
		t.Error("Status.HasKey false with a key set")
	}
	if msg := st.Providers[ProviderTMDB].LastError; msg == "" || strings.Contains(msg, v3Key) {
		t.Errorf("status last error = %q (want an error that does not hold the key)", msg)
	}
}

func TestTMDBV4TokenIsABearerHeader(t *testing.T) {
	e, api := tmdbEnv(t, v4Token)
	if _, err := e.svc.TMDB.Movie(bg(), "19"); err != nil {
		t.Fatal(err)
	}
	call := api.Last("/movie/19")
	if got := call.Header.Get("Authorization"); got != "Bearer "+v4Token {
		t.Errorf("Authorization = %q", got)
	}
	if strings.Contains(call.RawQuery, "api_key") || strings.Contains(call.RawQuery, v4Token) {
		t.Errorf("a v4 token must not appear in the query: %q", call.RawQuery)
	}
	if strings.Contains(e.logs.String(), v4Token) {
		t.Error("the v4 token is in the log")
	}
	if got := call.RawQuery; got != "append_to_response=credits%2Crelease_dates%2Cvideos" {
		t.Errorf("movie query = %q", got)
	}
}

func TestTMDBKeyChangeAppliesAtOnce(t *testing.T) {
	var key atomic.Value
	key.Store("")
	api := newFakeAPI(t, map[string]http.HandlerFunc{"/search/movie": fixture(t, "tmdb_search_movie_metropolis.json")})
	e := newEnv(t, Options{TMDBURL: api.URL(), TMDBKey: func(context.Context) (string, error) { return key.Load().(string), nil }})

	if _, err := e.svc.TMDB.SearchMovies(bg(), "Metropolis", 0); !errors.Is(err, ErrTMDBKeyMissing) {
		t.Fatalf("before: %v", err)
	}
	key.Store(v3Key)
	if _, err := e.svc.TMDB.SearchMovies(bg(), "Metropolis", 0); err != nil {
		t.Fatalf("after setting a key: %v", err)
	}
	key.Store("")
	if _, err := e.svc.TMDB.SearchMovies(bg(), "Metropolis", 0); !errors.Is(err, ErrTMDBKeyMissing) {
		t.Errorf("a cached answer must not bypass a removed key: %v", err)
	}
}

func TestTMDBKeyLookupErrorIsNotMistakenForNoKey(t *testing.T) {
	boom := errors.New("cannot decrypt")
	e := newEnv(t, Options{TMDBKey: func(context.Context) (string, error) { return "", boom }})
	_, err := e.svc.TMDB.SearchMovies(bg(), "x", 0)
	if !errors.Is(err, boom) || errors.Is(err, ErrTMDBKeyMissing) {
		t.Errorf("err = %v", err)
	}
}

func TestTMDBRejectedKey(t *testing.T) {
	e, api := tmdbEnv(t, v3Key)
	api.Set("/search/movie", func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
		_, _ = w.Write([]byte(`{"status_code":7,"status_message":"Invalid API key: You must be granted a valid key."}`))
	})
	_, err := e.svc.TMDB.SearchMovies(bg(), "Metropolis", 0)
	if !errors.Is(err, ErrTMDBKeyInvalid) || CodeOf(err) != "tmdb_key_invalid" {
		t.Fatalf("err = %v, want tmdb_key_invalid", err)
	}
	if e.cacheRows() != 0 {
		t.Error("a rejected call was cached")
	}
	if msg := e.svc.Status(bg()).Providers[ProviderTMDB].LastError; !strings.Contains(msg, "tmdb_key_invalid") {
		t.Errorf("status = %q", msg)
	}
}

func TestTMDBSearchMovies(t *testing.T) {
	e, api := tmdbEnv(t, v3Key)
	hits, err := e.svc.TMDB.SearchMovies(bg(), "  Metropolis ", 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(hits) != 2 {
		t.Fatalf("hits = %d", len(hits))
	}
	want := SearchHit{
		Kind: KindMovie, Provider: ProviderTMDB, ProviderID: "19", Name: "Metropolis", Year: 1927,
		Overview: "In a futuristic city sharply divided between the rich and the poor, the son of the city's mastermind falls in love with a prophet.",
		Poster:   Image{Ref: "tmdb/w500/metropolis1927.jpg", Kind: "poster"},
	}
	if hits[0] != want {
		t.Errorf("hit =\n%+v\nwant\n%+v", hits[0], want)
	}
	if hits[1].ProviderID != "90002" || hits[1].Year != 2001 || !hits[1].Poster.IsZero() {
		t.Errorf("second hit = %+v", hits[1])
	}
	if strings.Contains(api.Last("/search/movie").RawQuery, "year=") {
		t.Errorf("year=0 must not be sent: %q", api.Last("/search/movie").RawQuery)
	}
}

func TestTMDBMovieMapping(t *testing.T) {
	e, _ := tmdbEnv(t, v3Key)
	m, err := e.svc.TMDB.Movie(bg(), "19")
	if err != nil {
		t.Fatal(err)
	}
	if m.TMDBID != 19 || m.IMDbID != "tt0017136" || m.Title != "Metropolis" || m.OriginalTitle != "Metropolis" ||
		m.Year != 1927 || m.ReleaseDate != "1927-01-10" || m.RuntimeMin != 153 || m.Status != "released" {
		t.Errorf("movie = %+v", m)
	}
	if !strings.HasPrefix(m.Tagline, "There can be no understanding") {
		t.Errorf("tagline = %q", m.Tagline)
	}
	if !slices.Equal(m.Genres, []string{"Drama", "Science Fiction"}) {
		t.Errorf("genres = %v", m.Genres)
	}
	if m.Poster.Ref != "tmdb/w500/metropolis1927.jpg" || m.Backdrop.Ref != "tmdb/w1280/metropolis1927-bd.jpg" {
		t.Errorf("images = %+v / %+v", m.Poster, m.Backdrop)
	}
	if m.Certification != "NR" {
		t.Errorf("certification = %q, want the US theatrical one (NR)", m.Certification)
	}
	if m.TrailerKey != "trailerKEY1" {
		t.Errorf("trailer = %q, want the official YouTube trailer", m.TrailerKey)
	}

	type pp struct{ name, role, job, image string }
	var got []pp
	for _, p := range m.People {
		got = append(got, pp{p.Name, p.Role, p.Job, p.Image.Ref})
	}
	want := []pp{
		{"Brigitte Helm", "actor", "Maria / Robot Maria", "tmdb/w185/helm.jpg"}, // billing order, not list order
		{"Alfred Abel", "actor", "Joh Fredersen", "tmdb/w185/abel.jpg"},
		{"Extra Without Photo", "actor", "Worker", ""},
		{"Fritz Lang", "director", "Director", "tmdb/w185/lang.jpg"},
		{"Thea von Harbou", "writer", "Screenplay", ""}, // the producer is left out
	}
	if !slices.Equal(got, want) {
		t.Errorf("people =\n%v\nwant\n%v", got, want)
	}
}

// Sprite Fright premiered in October 2021, but TMDB's primary release date is
// 2024-03-01. Release names carry 2021, so the catalog's year is the first one;
// the primary date stays the date the calendar uses.
func TestTMDBMovieYearIsTheFirstReleaseNotThePrimaryDate(t *testing.T) {
	api := newFakeAPI(t, map[string]http.HandlerFunc{
		"/movie/891761": fixture(t, "tmdb_movie_sprite_fright.json"),
	})
	e := newEnv(t, Options{TMDBURL: api.URL(), TMDBKey: fixedKey(v3Key)})
	m, err := e.svc.TMDB.Movie(bg(), "891761")
	if err != nil {
		t.Fatal(err)
	}
	if m.Year != 2021 {
		t.Errorf("year = %d, want 2021 (the 2021-10-29 premiere)", m.Year)
	}
	if m.ReleaseDate != "2024-03-01" {
		t.Errorf("releaseDate = %q, want the primary date kept for the calendar", m.ReleaseDate)
	}
}

func TestTMDBFirstReleaseYear(t *testing.T) {
	entry := func(typ int, date string) map[string]any { return map[string]any{"type": typ, "release_date": date} }
	movie := func(primary string, entries ...map[string]any) tmMovie {
		raw := map[string]any{
			"release_date": primary,
			"release_dates": map[string]any{"results": []any{
				map[string]any{"iso_3166_1": "US", "release_dates": entries},
			}},
		}
		b, err := json.Marshal(raw)
		if err != nil {
			t.Fatal(err)
		}
		var m tmMovie
		if err := json.Unmarshal(b, &m); err != nil {
			t.Fatal(err)
		}
		return m
	}
	for _, tc := range []struct {
		name string
		m    tmMovie
		want int
	}{
		{"primary date only", movie("1927-01-10"), 1927},
		{"an earlier premiere beats a later primary date", movie("2024-03-01", entry(1, "2021-10-29T00:00:00.000Z")), 2021},
		{"every kind of release counts", movie("2024-03-01",
			entry(5, "2023-01-01T00:00:00.000Z"), entry(4, "2022-01-01T00:00:00.000Z"),
			entry(3, "2021-06-01T00:00:00.000Z"), entry(2, "2020-06-01T00:00:00.000Z")), 2020},
		{"a later entry never moves the year", movie("2019-05-05", entry(3, "2021-06-01T00:00:00.000Z")), 2019},
		{"television is not a first release", movie("2024-03-01", entry(6, "2015-01-01T00:00:00.000Z")), 2024},
		{"an unknown type is left out", movie("2024-03-01", entry(0, "2015-01-01T00:00:00.000Z"), entry(9, "2015-01-01T00:00:00.000Z")), 2024},
		{"a placeholder date is left out", movie("2024-03-01", entry(1, "0001-01-01T00:00:00.000Z")), 2024},
		{"an entry with no date is left out", movie("2024-03-01", entry(1, "")), 2024},
		{"no primary date: the earliest entry", movie("", entry(4, "2022-01-01T00:00:00.000Z"), entry(1, "2021-10-29T00:00:00.000Z")), 2021},
		{"no dates at all", movie(""), 0},
	} {
		if got := tc.m.firstReleaseYear(); got != tc.want {
			t.Errorf("%s: year = %d, want %d", tc.name, got, tc.want)
		}
	}
}

func TestTMDBMovieNotFoundAndBadID(t *testing.T) {
	e, api := tmdbEnv(t, v3Key)
	if _, err := e.svc.TMDB.Movie(bg(), "424242"); !errors.Is(err, ErrNotFound) {
		t.Errorf("unknown id: %v", err)
	}
	before := api.Total()
	for _, bad := range []string{"", "x", "0", "-1", "19/credits"} {
		if _, err := e.svc.TMDB.Movie(bg(), bad); !errors.Is(err, ErrNotFound) {
			t.Errorf("Movie(%q) = %v", bad, err)
		}
	}
	if api.Total() != before {
		t.Error("a malformed id reached the network")
	}
}

func TestTMDBTVAndExternalIDs(t *testing.T) {
	e, api := tmdbEnv(t, v3Key)
	hits, err := e.svc.TMDB.SearchTV(bg(), "Pioneer One", 2010)
	if err != nil {
		t.Fatal(err)
	}
	if len(hits) != 1 || hits[0].Kind != KindShow || hits[0].Provider != ProviderTMDB || hits[0].ProviderID != "70001" ||
		hits[0].Name != "Pioneer One" || hits[0].Year != 2010 {
		t.Errorf("tv hits = %+v", hits)
	}
	if !strings.Contains(api.Last("/search/tv").RawQuery, "first_air_date_year=2010") {
		t.Errorf("tv query = %q", api.Last("/search/tv").RawQuery)
	}
	ids, err := e.svc.TMDB.ExternalIDs(bg(), KindShow, "70001")
	if err != nil {
		t.Fatal(err)
	}
	if ids.IMDbID != "tt9900001" || ids.TVDBID != 99991 {
		t.Errorf("ids = %+v", ids)
	}
}
