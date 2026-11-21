package metadata

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"slices"
	"strings"
	"testing"
	"time"
)

const (
	chopinMBID = "a1b2c3d4-0000-4000-8000-000000000001"
	rgNocturne = "b0000000-0000-4000-8000-000000000101"
	rgEtudes   = "b0000000-0000-4000-8000-000000000102"
	rgBallades = "b0000000-0000-4000-8000-000000000103"
)

func mbRoutes(t *testing.T) map[string]http.HandlerFunc {
	return map[string]http.HandlerFunc{
		"/ws/2/artist":               fixture(t, "mb_artist_search_chopin.json"),
		"/ws/2/artist/" + chopinMBID: fixture(t, "mb_artist_chopin.json"),
		"/ws/2/release-group":        fixture(t, "mb_release_groups_chopin.json"),
		"/ws/2/release":              releasesByGroup(t),
		"/ws/2/release/c0000000-0000-4000-8000-000000000203": fixture(t, "mb_release_nocturnes.json"),
		"/ws/2/release/c0000000-0000-4000-8000-000000000211": fixture(t, "mb_release_etudes.json"),
		"/ws/2/release/c0000000-0000-4000-8000-000000000221": fixture(t, "mb_release_ballades.json"),
	}
}

// releasesByGroup answers the "releases of a release group" browse request.
func releasesByGroup(t *testing.T) http.HandlerFunc {
	files := map[string]http.HandlerFunc{
		rgNocturne: fixture(t, "mb_releases_nocturnes.json"),
		rgEtudes:   fixture(t, "mb_releases_etudes.json"),
		rgBallades: fixture(t, "mb_releases_ballades.json"),
	}
	return func(w http.ResponseWriter, r *http.Request) {
		if h, ok := files[r.URL.Query().Get("release-group")]; ok {
			h(w, r)
			return
		}
		http.NotFound(w, r)
	}
}

func mbEnv(t *testing.T) (*env, *fakeAPI) {
	t.Helper()
	api := newFakeAPI(t, mbRoutes(t))
	return newEnv(t, Options{MusicBrainzURL: api.URL()}), api
}

func TestMusicBrainzSearchMapsHits(t *testing.T) {
	e, api := mbEnv(t)
	hits, err := e.svc.MusicBrainz.SearchArtists(bg(), "chopin")
	if err != nil {
		t.Fatal(err)
	}
	if len(hits) != 2 {
		t.Fatalf("hits = %+v", hits)
	}
	want := SearchHit{
		Kind: KindArtist, Provider: ProviderMusicBrainz, ProviderID: chopinMBID, Name: "Frédéric Chopin", Year: 1810,
		Overview: "Polish composer and pianist", Detail: "Person · PL",
	}
	if hits[0] != want {
		t.Errorf("hit =\n%+v\nwant\n%+v", hits[0], want)
	}
	if hits[1].Detail != "Group" || hits[1].Year != 0 {
		t.Errorf("second hit = %+v", hits[1])
	}
	q := api.Last("/ws/2/artist").RawQuery
	for _, w := range []string{"query=chopin", "fmt=json"} {
		if !strings.Contains(q, w) {
			t.Errorf("query %q lacks %q", q, w)
		}
	}
	ua := api.Last("/ws/2/artist").Header.Get("User-Agent")
	if !strings.HasPrefix(ua, "Marquee/1.2.3 (+https://") {
		t.Errorf("MusicBrainz needs a User-Agent with contact details; got %q", ua)
	}
}

func TestMusicBrainzArtistAlbumsAndTracks(t *testing.T) {
	e, api := mbEnv(t)
	a, err := e.svc.MusicBrainz.Artist(bg(), strings.ToUpper(chopinMBID)) // ids are case-insensitive
	if err != nil {
		t.Fatal(err)
	}
	if a.MBID != chopinMBID || a.Name != "Frédéric Chopin" || a.SortName != "Chopin, Frédéric" || a.Type != "Person" ||
		a.Country != "PL" || a.Year != 1810 || !a.Ended || a.Disambiguation != "Polish composer and pianist" {
		t.Errorf("artist = %+v", a)
	}
	if !slices.Equal(a.Genres, []string{"classical", "romantic"}) {
		t.Errorf("genres = %v", a.Genres)
	}
	// The artist's own name, in any case, is not an alias of itself.
	if !slices.Equal(a.AltNames, []string{"Fryderyk Chopin", "F. Chopin", "Chopin"}) {
		t.Errorf("aliases = %v", a.AltNames)
	}

	// The live album and the single are left out; the EP stays.
	var titles []string
	for _, al := range a.Albums {
		titles = append(titles, al.Title+"|"+al.Type)
	}
	if want := []string{"Nocturnes|album", "Études, Op. 10|album", "Ballades (sampler)|ep"}; !slices.Equal(titles, want) {
		t.Fatalf("albums = %v, want %v", titles, want)
	}

	noc := a.Albums[0]
	if noc.MBID != rgNocturne || noc.Year != 1998 || noc.ReleaseDate != "1998-03-10" ||
		noc.Cover.Ref != "caa/release-group/"+rgNocturne+"/front-500" {
		t.Errorf("album = %+v", noc)
	}
	// The bootleg (1990) is older and the reissue is newer: the earliest
	// OFFICIAL release, ...0203, supplies the tracks.
	if noc.ReleaseID != "c0000000-0000-4000-8000-000000000203" {
		t.Errorf("release picked = %s", noc.ReleaseID)
	}
	type tr struct {
		disc, pos int
		title     string
		ms        int
		mbid      string
	}
	var got []tr
	for _, x := range noc.Tracks {
		got = append(got, tr{x.Disc, x.Position, x.Title, x.LengthMS, x.MBID})
	}
	want := []tr{
		{1, 1, "Nocturne in B-flat minor, Op. 9 No. 1", 330000, "e0000000-0000-4000-8000-000000000401"},
		{1, 2, "Nocturne in E-flat major, Op. 9 No. 2", 262000, "e0000000-0000-4000-8000-000000000402"}, // length from the recording
		{2, 1, "Nocturne in B major, Op. 9 No. 3", 410000, "e0000000-0000-4000-8000-000000000403"},
	}
	if !slices.Equal(got, want) {
		t.Errorf("tracks =\n%v\nwant\n%v", got, want)
	}

	etudes := a.Albums[1]
	if etudes.Year != 1985 || len(etudes.Tracks) != 2 || etudes.Tracks[1].LengthMS != 88000 || etudes.Tracks[1].MBID != "e0000000-0000-4000-8000-000000000412" {
		t.Errorf("etudes = %+v", etudes)
	}
	// Only releases that are not official at all: the single release is used.
	ballades := a.Albums[2]
	if ballades.ReleaseID != "c0000000-0000-4000-8000-000000000221" || len(ballades.Tracks) != 1 {
		t.Errorf("ballades = %+v", ballades)
	}
	// No first-release-date on the release group: the release's own date fills the year.
	if ballades.Year != 2003 {
		t.Errorf("ballades year = %d, want 2003 from the release", ballades.Year)
	}

	// The calls are the documented ones.
	if q := api.Last("/ws/2/release-group").RawQuery; !strings.Contains(q, "artist="+chopinMBID) ||
		!strings.Contains(q, "type=album%7Cep") || !strings.Contains(q, "fmt=json") {
		t.Errorf("release-group query = %q", q)
	}
	if q := api.Last("/ws/2/release/c0000000-0000-4000-8000-000000000203").RawQuery; !strings.Contains(q, "inc=recordings") ||
		!strings.Contains(q, "fmt=json") {
		t.Errorf("release query = %q", q)
	}
}

func TestMusicBrainzOneRequestPerSecond(t *testing.T) {
	e, api := mbEnv(t)
	for i := 0; i < 4; i++ {
		if _, err := e.svc.MusicBrainz.SearchArtists(bg(), fmt.Sprintf("artist %d", i)); err != nil {
			t.Fatal(err)
		}
	}
	if api.Count("/ws/2/artist") != 4 {
		t.Fatalf("calls = %d", api.Count("/ws/2/artist"))
	}
	// The first goes straight out; each of the other three waits a second.
	sleeps := e.clock.Sleeps()
	if len(sleeps) != 3 {
		t.Fatalf("sleeps = %v, want 3", sleeps)
	}
	for _, d := range sleeps {
		if d < 900*time.Millisecond || d > 1100*time.Millisecond {
			t.Errorf("wait = %v, want about 1s", d)
		}
	}
}

func TestMusicBrainzFullArtistStaysUnderOneRequestPerSecond(t *testing.T) {
	e, api := mbEnv(t)
	if _, err := e.svc.MusicBrainz.Artist(bg(), chopinMBID); err != nil {
		t.Fatal(err)
	}
	// artist + one release-group page + (releases, release) for each of 3 albums
	if api.Total() != 8 {
		t.Fatalf("%d requests for one artist, want 8", api.Total())
	}
	var total time.Duration
	for _, d := range e.clock.Sleeps() {
		total += d
	}
	// Eight requests need at least seven seconds between the first and last.
	if total < 6900*time.Millisecond {
		t.Errorf("total wait %v for 8 requests, want at least 7s", total)
	}
}

func TestMusicBrainzTrackBudgetBoundsOneCallAndLaterCallsFinishTheJob(t *testing.T) {
	e, api := mbEnv(t)
	mb := e.svc.MusicBrainz
	if mb.trackBudget != 15 {
		t.Fatalf("default budget = %d, want 15 albums per call", mb.trackBudget)
	}
	mb.trackBudget = 1 // three albums, one read per call

	tracks := func(a *ArtistInfo) []int {
		var n []int
		for _, al := range a.Albums {
			n = append(n, len(al.Tracks))
		}
		return n
	}

	// First call: newest album only. Albums come newest first.
	a, err := mb.Artist(bg(), chopinMBID)
	if err != nil {
		t.Fatal(err)
	}
	var names []string
	for _, al := range a.Albums {
		names = append(names, al.Title)
	}
	if want := []string{"Nocturnes", "Études, Op. 10", "Ballades (sampler)"}; !slices.Equal(names, want) {
		t.Errorf("albums = %v, want newest first %v", names, want)
	}
	if got := tracks(a); !slices.Equal(got, []int{3, 0, 0}) {
		t.Errorf("tracks per album = %v, want [3 0 0]", got)
	}
	if api.Total() != 4 { // artist, release groups, releases + release of the newest
		t.Errorf("%d requests, want 4", api.Total())
	}

	// A refresh (which skips the cache for the artist and the album list, not
	// for track lists) reads the next album; the first costs nothing.
	before := api.Total()
	a, err = mb.Artist(BypassCache(bg()), chopinMBID)
	if err != nil {
		t.Fatal(err)
	}
	if got := tracks(a); !slices.Equal(got, []int{3, 2, 0}) {
		t.Errorf("after the second call: %v, want [3 2 0]", got)
	}
	if extra := api.Total() - before; extra != 4 { // artist, release groups, and two for Etudes
		t.Errorf("second call made %d requests, want 4", extra)
	}

	a, err = mb.Artist(BypassCache(bg()), chopinMBID)
	if err != nil {
		t.Fatal(err)
	}
	if got := tracks(a); !slices.Equal(got, []int{3, 2, 1}) {
		t.Errorf("after the third call: %v, want [3 2 1]", got)
	}

	// Nothing left to read: only the artist and the album list are asked again.
	before = api.Total()
	if _, err := mb.Artist(BypassCache(bg()), chopinMBID); err != nil {
		t.Fatal(err)
	}
	if extra := api.Total() - before; extra != 2 {
		t.Errorf("a finished artist made %d requests on refresh, want 2", extra)
	}
}

func TestMusicBrainzArtistErrors(t *testing.T) {
	e, api := mbEnv(t)
	before := api.Total()
	for _, bad := range []string{"", "chopin", "a1b2c3d4-0000-4000-8000-00000000000", "../ws/2/url"} {
		if _, err := e.svc.MusicBrainz.Artist(bg(), bad); !errors.Is(err, ErrNotFound) {
			t.Errorf("Artist(%q) = %v", bad, err)
		}
	}
	if api.Total() != before {
		t.Error("a malformed id reached the network")
	}
	if _, err := e.svc.MusicBrainz.Artist(bg(), "a1b2c3d4-0000-4000-8000-0000000000ff"); !errors.Is(err, ErrNotFound) {
		t.Errorf("unknown artist = %v", err)
	}
}

func TestMusicBrainzMissingReleaseMeansNoTracksNotFailure(t *testing.T) {
	e, api := mbEnv(t)
	api.Set("/ws/2/release", httpStatus(http.StatusNotFound))
	a, err := e.svc.MusicBrainz.Artist(bg(), chopinMBID)
	if err != nil {
		t.Fatalf("Artist: %v", err)
	}
	if len(a.Albums) != 3 {
		t.Fatalf("albums = %d", len(a.Albums))
	}
	for _, al := range a.Albums {
		if len(al.Tracks) != 0 {
			t.Errorf("%s has tracks %v", al.Title, al.Tracks)
		}
	}
}

func TestMusicBrainzFailureMidwayResumesFromCache(t *testing.T) {
	e, api := mbEnv(t)
	good := api.handlers["/ws/2/release/c0000000-0000-4000-8000-000000000211"]
	api.Set("/ws/2/release/c0000000-0000-4000-8000-000000000211", httpStatus(503))

	if _, err := e.svc.MusicBrainz.Artist(bg(), chopinMBID); !errors.Is(err, ErrUpstream) {
		t.Fatalf("err = %v, want upstream", err)
	}
	callsBefore := api.Total()
	api.Set("/ws/2/release/c0000000-0000-4000-8000-000000000211", good)

	a, err := e.svc.MusicBrainz.Artist(bg(), chopinMBID)
	if err != nil {
		t.Fatalf("retry: %v", err)
	}
	if len(a.Albums) != 3 || len(a.Albums[1].Tracks) != 2 {
		t.Fatalf("retry result = %+v", a.Albums)
	}
	// Everything that worked the first time is served from the cache: the
	// retry asks only for what was missing (the Etudes release, then the
	// Ballades list and release).
	if extra := api.Total() - callsBefore; extra != 3 {
		t.Errorf("retry made %d requests, want 3", extra)
	}
}

func TestMusicBrainzReleaseGroupPaging(t *testing.T) {
	api := newFakeAPI(t, nil)
	page := func(offset, n, total int) []byte {
		var groups []map[string]any
		for i := 0; i < n; i++ {
			groups = append(groups, map[string]any{
				"id":                 fmt.Sprintf("b0000000-0000-4000-8000-%012d", offset+i),
				"title":              fmt.Sprintf("Album %d", offset+i),
				"primary-type":       "Album",
				"secondary-types":    []string{},
				"first-release-date": "2000",
			})
		}
		b, _ := json.Marshal(map[string]any{"release-group-count": total, "release-group-offset": offset, "release-groups": groups})
		return b
	}
	api.Set("/ws/2/artist/"+chopinMBID, fixture(t, "mb_artist_chopin.json"))
	api.Set("/ws/2/release-group", func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Query().Get("offset") {
		case "0":
			_, _ = w.Write(page(0, 100, 103))
		case "100":
			_, _ = w.Write(page(100, 3, 103))
		default:
			http.NotFound(w, r)
		}
	})
	api.Set("/ws/2/release", httpStatus(404)) // no track lists needed here
	e := newEnv(t, Options{MusicBrainzURL: api.URL()})

	a, err := e.svc.MusicBrainz.Artist(bg(), chopinMBID)
	if err != nil {
		t.Fatal(err)
	}
	if len(a.Albums) != 103 {
		t.Errorf("albums = %d, want 103 over two pages", len(a.Albums))
	}
	if api.Count("/ws/2/release-group") != 2 {
		t.Errorf("release-group pages fetched = %d", api.Count("/ws/2/release-group"))
	}
}

func TestEarliestOfficial(t *testing.T) {
	r := func(id, status, date string) mbRelease { return mbRelease{ID: id, Status: status, Date: date} }
	for _, tc := range []struct {
		name string
		in   []mbRelease
		want string
	}{
		{"nothing", nil, ""},
		{"earliest official beats an earlier bootleg", []mbRelease{r("a", "Bootleg", "1990"), r("b", "Official", "2001-05-01"), r("c", "Official", "1998-03-10")}, "c"},
		{"partial dates sort as text", []mbRelease{r("a", "Official", "1998-03"), r("b", "Official", "1998")}, "b"},
		{"official without a date loses to a dated official", []mbRelease{r("a", "Official", ""), r("b", "Official", "2010")}, "b"},
		{"no official: earliest of the rest", []mbRelease{r("a", "Promotion", "2005"), r("b", "Bootleg", "2003")}, "b"},
		{"all undated: the first", []mbRelease{r("a", "Official", ""), r("b", "Official", "")}, "a"},
		{"case of the status does not matter", []mbRelease{r("a", "official", "2000"), r("b", "Bootleg", "1999")}, "a"},
		{"entries without an id are ignored", []mbRelease{r("", "Official", "1900"), r("z", "Official", "2000")}, "z"},
	} {
		got := earliestOfficial(tc.in)
		switch {
		case tc.want == "" && got != nil:
			t.Errorf("%s: got %q, want none", tc.name, got.ID)
		case tc.want != "" && (got == nil || got.ID != tc.want):
			t.Errorf("%s: got %v, want %q", tc.name, got, tc.want)
		}
	}
}

func TestKeepReleaseGroup(t *testing.T) {
	for _, tc := range []struct {
		name string
		g    mbReleaseGroup
		keep bool
	}{
		{"album", mbReleaseGroup{ID: "x", Title: "t", PrimaryType: "Album"}, true},
		{"ep", mbReleaseGroup{ID: "x", Title: "t", PrimaryType: "EP"}, true},
		{"soundtrack stays", mbReleaseGroup{ID: "x", Title: "t", PrimaryType: "Album", SecondaryTypes: []string{"Soundtrack"}}, true},
		{"live", mbReleaseGroup{ID: "x", Title: "t", PrimaryType: "Album", SecondaryTypes: []string{"Live"}}, false},
		{"compilation", mbReleaseGroup{ID: "x", Title: "t", PrimaryType: "Album", SecondaryTypes: []string{"Compilation"}}, false},
		{"live and soundtrack", mbReleaseGroup{ID: "x", Title: "t", PrimaryType: "Album", SecondaryTypes: []string{"Soundtrack", "Live"}}, false},
		{"single", mbReleaseGroup{ID: "x", Title: "t", PrimaryType: "Single"}, false},
		{"no id", mbReleaseGroup{Title: "t", PrimaryType: "Album"}, false},
	} {
		if got := keepReleaseGroup(tc.g); got != tc.keep {
			t.Errorf("%s: keep = %v, want %v", tc.name, got, tc.keep)
		}
	}
}
