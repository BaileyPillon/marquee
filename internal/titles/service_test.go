package titles

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/BaileyPillon/marquee/internal/metadata"
)

// ---- Add and Get -----------------------------------------------------------

func TestAddMoviePersistsEverythingAndGetReturnsIt(t *testing.T) {
	f := newFixture(t)
	d, created, err := f.svc.Add(bg(), KindMovie, metadata.ProviderTMDB, " 19 ", AddOptions{Monitored: true, RootFolder: " /films/open "})
	if err != nil || !created {
		t.Fatalf("Add = %v created=%v", err, created)
	}
	if d.ID == 0 || d.Kind != KindMovie || d.TMDBID != 19 || d.IMDbID != "tt0017136" || d.TVmazeID != 0 || d.MBID != "" {
		t.Errorf("ids = %+v", d.Title)
	}
	if d.Name != "Metropolis" || d.SortName != "metropolis" || d.Year != 1927 || d.RuntimeMin != 153 || d.Status != "released" ||
		d.Tagline != "The heart mediates." || d.Certification != "NR" || d.TrailerKey != "trailerKEY1" ||
		d.Overview != "A city divided." {
		t.Errorf("fields = %+v", d.Title)
	}
	if !slices.Equal(d.Genres, []string{"Drama", "Science Fiction"}) || !slices.Equal(d.AltNames, []string{"Metropolis (Fritz Lang)"}) {
		t.Errorf("genres %v alt %v", d.Genres, d.AltNames)
	}
	if d.PosterRef != "tmdb/w500/metropolis.jpg" || d.BackdropRef != "tmdb/w1280/metropolis-bd.jpg" {
		t.Errorf("artwork refs = %q %q", d.PosterRef, d.BackdropRef)
	}
	if !d.Monitored || d.RootFolder != "/films/open" {
		t.Errorf("monitored %v folder %q", d.Monitored, d.RootFolder)
	}
	if d.AddedAt.IsZero() || !d.UpdatedAt.Equal(d.AddedAt) || !d.RefreshedAt.Equal(d.AddedAt) {
		t.Errorf("timestamps = %v %v %v", d.AddedAt, d.UpdatedAt, d.RefreshedAt)
	}
	if len(d.People) != 3 || d.People[0].Name != "Brigitte Helm" || d.People[0].ImageRef != "tmdb/w185/helm.jpg" ||
		d.People[2].Role != "director" {
		t.Errorf("people = %+v", d.People)
	}
	// A movie has no seasons, episodes or albums, but the lists are arrays, not null.
	if d.Seasons == nil || d.Episodes == nil || d.Albums == nil || len(d.Seasons)+len(d.Episodes)+len(d.Albums) != 0 {
		t.Errorf("movie children = %v %v %v", d.Seasons, d.Episodes, d.Albums)
	}

	got, err := f.svc.Get(bg(), d.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.Name != d.Name || len(got.People) != 3 || got.TMDBID != 19 {
		t.Errorf("Get = %+v", got)
	}
}

func TestAddShowWithSeasonsAndEpisodes(t *testing.T) {
	f := newFixture(t)
	d := f.addShow("99001", true)
	if d.TVmazeID != 99001 || d.IMDbID != "tt9900001" || d.Name != "Pioneer One" || d.Status != "ended" ||
		d.PosterRef != "tvmaze/uploads/images/original_untouched/99/99001.jpg" {
		t.Errorf("show = %+v", d.Title)
	}

	type sn struct {
		n, count int
		mon      bool
	}
	var seasons []sn
	for _, s := range d.Seasons {
		seasons = append(seasons, sn{s.Number, s.EpisodeCount, s.Monitored})
	}
	// Specials (season 0) are never wanted by default.
	if want := []sn{{0, 1, false}, {1, 2, true}, {2, 1, true}}; !slices.Equal(seasons, want) {
		t.Errorf("seasons = %v, want %v", seasons, want)
	}

	type ep struct {
		s, n int
		name string
		mon  bool
	}
	var eps []ep
	for _, e := range d.Episodes {
		if e.ID == 0 {
			t.Errorf("episode without an id: %+v", e)
		}
		eps = append(eps, ep{e.Season, e.Number, e.Name, e.Monitored})
	}
	want := []ep{{0, 1, "Behind the Scenes", false}, {1, 1, "Earthfall", true}, {1, 2, "Anomalies", true}, {2, 1, "Landing Zone", true}}
	if !slices.Equal(eps, want) {
		t.Errorf("episodes = %v, want %v", eps, want)
	}
	if e := d.Episodes[1]; e.AirDate != "2010-06-19" || e.Overview != "An object falls." || e.RuntimeMin != 45 {
		t.Errorf("episode fields = %+v", e)
	}
	if len(d.People) != 1 || d.People[0].Job != "Agent Taylor" {
		t.Errorf("people = %+v", d.People)
	}

	// Added unmonitored: nothing is wanted.
	d2, _, err := f.svc.Add(bg(), KindShow, metadata.ProviderTVmaze, "7", AddOptions{Monitored: false})
	if err != nil {
		t.Fatal(err)
	}
	if d2.Monitored {
		t.Error("show added with monitored=false is monitored")
	}
}

func TestAddShowCreatesASeasonForEveryEpisodeSeason(t *testing.T) {
	f := newFixture(t)
	s := pioneerOne()
	s.Seasons = nil // a provider that lists episodes but no seasons
	f.shows.shows["99001"] = s
	d := f.addShow("99001", true)
	if len(d.Seasons) != 3 {
		t.Errorf("seasons = %+v, want 0, 1 and 2 derived from the episodes", d.Seasons)
	}
}

func TestAddArtistWithAlbumsAndTracks(t *testing.T) {
	f := newFixture(t)
	d := f.addArtist(strings.ToUpper(chopinMBID), true) // ids are case-insensitive
	if d.MBID != chopinMBID || d.Name != "Frédéric Chopin" || d.SortName != "chopin, frédéric" || d.Year != 1810 ||
		d.Status != "ended" || d.Overview != "Polish composer and pianist" {
		t.Errorf("artist = %+v", d.Title)
	}
	if !slices.Equal(d.AltNames, []string{"Fryderyk Chopin", "Chopin"}) {
		t.Errorf("alt names = %v", d.AltNames)
	}
	// MusicBrainz has no artist photo: the newest cover stands in.
	if d.PosterRef != "caa/release-group/"+nocturnesMB+"/front-500" {
		t.Errorf("poster = %q", d.PosterRef)
	}
	if len(d.Albums) != 2 {
		t.Fatalf("albums = %+v", d.Albums)
	}
	// Oldest first.
	etudes, noc := d.Albums[0], d.Albums[1]
	if etudes.Name != "Études, Op. 10" || etudes.Year != 1985 || len(etudes.Tracks) != 1 {
		t.Errorf("first album = %+v", etudes)
	}
	if noc.MBID != nocturnesMB || noc.Type != "album" || !noc.Monitored || noc.CoverRef == "" || len(noc.Tracks) != 3 {
		t.Errorf("second album = %+v", noc)
	}
	var order []string
	for _, tr := range noc.Tracks {
		order = append(order, fmt.Sprintf("%d.%d", tr.Disc, tr.Position))
	}
	if !slices.Equal(order, []string{"1.1", "1.2", "2.1"}) {
		t.Errorf("track order = %v", order)
	}
	if noc.Tracks[0].LengthMS != 330000 || noc.Tracks[0].MBID != "e0000000-0000-4000-8000-000000000401" {
		t.Errorf("track = %+v", noc.Tracks[0])
	}
}

func TestAddIsIdempotent(t *testing.T) {
	f := newFixture(t)
	first, created, err := f.svc.Add(bg(), KindMovie, "tmdb", "19", AddOptions{Monitored: true})
	if err != nil || !created {
		t.Fatalf("first: %v %v", err, created)
	}
	// A second add, even with other options, returns what is there: no new
	// row, no provider call, options untouched.
	again, created, err := f.svc.Add(bg(), KindMovie, "tmdb", "19", AddOptions{Monitored: false, RootFolder: "/elsewhere"})
	if err != nil || created {
		t.Fatalf("second: %v created=%v", err, created)
	}
	if again.ID != first.ID || !again.Monitored || again.RootFolder != "" {
		t.Errorf("second add changed the title: %+v", again.Title)
	}
	if f.movies.gets.Load() != 1 {
		t.Errorf("provider asked %d times, want 1", f.movies.gets.Load())
	}
	if n := f.count(`SELECT COUNT(*) FROM titles`); n != 1 {
		t.Errorf("rows = %d", n)
	}

	// Shows and artists are idempotent too, and children are not doubled.
	s1 := f.addShow("99001", true)
	s2 := f.addShow("99001", true)
	a1 := f.addArtist(chopinMBID, true)
	a2 := f.addArtist(strings.ToUpper(chopinMBID), true)
	if s1.ID != s2.ID || a1.ID != a2.ID {
		t.Errorf("ids differ: show %d/%d artist %d/%d", s1.ID, s2.ID, a1.ID, a2.ID)
	}
	if f.count(`SELECT COUNT(*) FROM episodes`) != 4 || f.count(`SELECT COUNT(*) FROM albums`) != 2 || f.count(`SELECT COUNT(*) FROM tracks`) != 4 {
		t.Error("children were duplicated by the second add")
	}
}

func TestAddConcurrentlyCreatesOneRow(t *testing.T) {
	f := newFixture(t)
	const n = 8
	var wg sync.WaitGroup
	ids := make(chan int64, n)
	errs := make(chan error, n)
	var createdCount int32
	var mu sync.Mutex
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			d, created, err := f.svc.Add(bg(), KindShow, "tvmaze", "99001", AddOptions{Monitored: true})
			if err != nil {
				errs <- err
				return
			}
			if created {
				mu.Lock()
				createdCount++
				mu.Unlock()
			}
			ids <- d.ID
		}()
	}
	wg.Wait()
	close(ids)
	close(errs)
	for err := range errs {
		t.Errorf("add: %v", err)
	}
	seen := map[int64]bool{}
	for id := range ids {
		seen[id] = true
	}
	if len(seen) != 1 {
		t.Errorf("concurrent adds produced ids %v", seen)
	}
	if createdCount != 1 {
		t.Errorf("%d callers were told they created it", createdCount)
	}
	if f.count(`SELECT COUNT(*) FROM titles`) != 1 || f.count(`SELECT COUNT(*) FROM episodes`) != 4 {
		t.Error("rows were duplicated")
	}
}

func TestAddReturnsTheExistingTitleWhenAnotherIDMatches(t *testing.T) {
	f := newFixture(t)
	first := f.addMovie("19", true)
	// The provider answers for a different TMDB id with the same IMDb id: the
	// same film listed twice. It must not become a second row or an error.
	dup := metropolis()
	dup.TMDBID = 20
	f.movies.movies["20"] = dup
	d, created, err := f.svc.Add(bg(), KindMovie, "tmdb", "20", AddOptions{Monitored: true})
	if err != nil {
		t.Fatalf("Add: %v", err)
	}
	if created || d.ID != first.ID {
		t.Errorf("created=%v id=%d, want the existing title %d", created, d.ID, first.ID)
	}
	if f.count(`SELECT COUNT(*) FROM titles`) != 1 {
		t.Error("a duplicate row was written")
	}
}

func TestAddValidationAndProviderErrors(t *testing.T) {
	f := newFixture(t)
	f.movies.movies["6"] = &metadata.MovieInfo{TMDBID: 6, Title: "  "}
	f.movies.movies["5"] = &metadata.MovieInfo{TMDBID: 5, Title: "Fine"}
	long := strings.Repeat("x", 1025)

	for _, tc := range []struct {
		name               string
		kind, provider, id string
		opts               AddOptions
		wantInvalid        bool
		wantCode           string
	}{
		{name: "unknown kind", kind: "book", provider: "tmdb", id: "1", wantInvalid: true},
		{name: "empty kind", kind: "", provider: "tmdb", id: "1", wantInvalid: true},
		{name: "wrong provider for kind", kind: "movie", provider: "tvmaze", id: "1", wantInvalid: true},
		{name: "unknown provider", kind: "show", provider: "tmdb", id: "1", wantInvalid: true},
		{name: "artist from tmdb", kind: "artist", provider: "tmdb", id: chopinMBID, wantInvalid: true},
		{name: "id not a number", kind: "movie", provider: "tmdb", id: "abc", wantInvalid: true},
		{name: "id zero", kind: "movie", provider: "tmdb", id: "0", wantInvalid: true},
		{name: "id negative", kind: "show", provider: "tvmaze", id: "-3", wantInvalid: true},
		{name: "id empty", kind: "movie", provider: "tmdb", id: "  ", wantInvalid: true},
		{name: "artist id not a uuid", kind: "artist", provider: "musicbrainz", id: "chopin", wantInvalid: true},
		{name: "root folder too long", kind: "movie", provider: "tmdb", id: "5", opts: AddOptions{RootFolder: long}, wantInvalid: true},
		{name: "root folder with NUL", kind: "movie", provider: "tmdb", id: "5", opts: AddOptions{RootFolder: "/films\x00evil"}, wantInvalid: true},
		{name: "provider says not found", kind: "movie", provider: "tmdb", id: "404", wantCode: "not_found"},
		{name: "provider sends a nameless title", kind: "movie", provider: "tmdb", id: "6", wantCode: "upstream_error"},
	} {
		_, created, err := f.svc.Add(bg(), tc.kind, tc.provider, tc.id, tc.opts)
		if err == nil || created {
			t.Errorf("%s: no error", tc.name)
			continue
		}
		if tc.wantInvalid && !isInvalid(err) {
			t.Errorf("%s: err = %v, want an InvalidError", tc.name, err)
		}
		if tc.wantCode != "" && metadata.CodeOf(err) != tc.wantCode {
			t.Errorf("%s: code %q (%v), want %q", tc.name, metadata.CodeOf(err), err, tc.wantCode)
		}
		if isInvalid(err) && err.Error() == "" {
			t.Errorf("%s: an InvalidError needs a message", tc.name)
		}
	}
	if f.count(`SELECT COUNT(*) FROM titles`) != 0 {
		t.Error("a failed add left a row behind")
	}
	// Bad ids never reach a provider.
	if f.movies.gets.Load() != 2 { // only "404" and "6" got that far
		t.Errorf("provider calls = %d, want 2", f.movies.gets.Load())
	}
}

func TestAddWithoutAProviderOrAfterCancel(t *testing.T) {
	f := newFixture(t)
	svc := NewService(f.db, Providers{}, Options{})
	if _, _, err := svc.Add(bg(), KindMovie, "tmdb", "19", AddOptions{}); err == nil {
		t.Error("Add with no movie provider succeeded")
	}
	ctx, cancel := context.WithCancel(bg())
	cancel()
	if _, _, err := f.svc.Add(ctx, KindMovie, "tmdb", "19", AddOptions{Monitored: true}); err == nil {
		t.Error("Add under a cancelled context succeeded")
	}
	if f.count(`SELECT COUNT(*) FROM titles`) != 0 {
		t.Error("a cancelled add left a row behind")
	}
}

func TestAddRollsBackEverythingWhenAnInsertFails(t *testing.T) {
	f := newFixture(t)
	// Make the last insert of an Add (the first person) fail, after the title,
	// seasons and episodes are already written inside the transaction.
	if _, err := f.db.Exec(`CREATE TRIGGER people_fail BEFORE INSERT ON people BEGIN SELECT RAISE(ABORT, 'disk full, pretend'); END`); err != nil {
		t.Fatal(err)
	}
	_, created, err := f.svc.Add(bg(), KindShow, "tvmaze", "99001", AddOptions{Monitored: true})
	if err == nil || created {
		t.Fatalf("Add = created %v, err %v; want a failure", created, err)
	}
	for _, table := range []string{"titles", "seasons", "episodes", "people"} {
		if n := f.count(`SELECT COUNT(*) FROM ` + table); n != 0 {
			t.Errorf("a failed Add left %d rows in %s", n, table)
		}
	}
	// And once the fault is gone the same Add works.
	if _, err := f.db.Exec(`DROP TRIGGER people_fail`); err != nil {
		t.Fatal(err)
	}
	if _, created, err := f.svc.Add(bg(), KindShow, "tvmaze", "99001", AddOptions{Monitored: true}); err != nil || !created {
		t.Errorf("retry: %v created=%v", err, created)
	}
}

func TestAddSurvivesAProviderListingAnEpisodeTwice(t *testing.T) {
	f := newFixture(t)
	s := pioneerOne()
	s.Episodes = append(s.Episodes, s.Episodes[1], s.Episodes[3])
	f.shows.shows["99001"] = s
	d := f.addShow("99001", true)
	if len(d.Episodes) != 4 {
		t.Errorf("episodes = %d, want the duplicates dropped", len(d.Episodes))
	}
}

func TestAnAlbumSharedByTwoArtistsIsNotDuplicated(t *testing.T) {
	f := newFixture(t)
	other := chopin()
	other.MBID, other.Name = otherArtist, "Other Pianist"
	other.Albums = other.Albums[:1] // also credited on Nocturnes
	f.artists.artists[otherArtist] = other
	f.addArtist(otherArtist, true)
	d := f.addArtist(chopinMBID, true)
	if f.count(`SELECT COUNT(*) FROM albums WHERE mbid = ?`, nocturnesMB) != 1 {
		t.Error("a release group credited to two artists was duplicated")
	}
	if d.Name != "Frédéric Chopin" {
		t.Errorf("artist = %+v", d.Title)
	}
}

func TestGetUnknownID(t *testing.T) {
	f := newFixture(t)
	if _, err := f.svc.Get(bg(), 12345); !errors.Is(err, ErrNotFound) {
		t.Errorf("err = %v", err)
	}
}

// ---- Lookup ----------------------------------------------------------------

func TestLookupMarksTitlesAlreadyInTheLibrary(t *testing.T) {
	f := newFixture(t)
	mov := f.addMovie("19", true)
	shw := f.addShow("99001", true)
	art := f.addArtist(chopinMBID, true)

	f.movies.hits["Metropolis"] = []metadata.SearchHit{
		{Kind: "movie", Provider: "tmdb", ProviderID: "19", Name: "Metropolis", Year: 1927},
		{Kind: "movie", Provider: "tmdb", ProviderID: "90002", Name: "Metropolis", Year: 2001},
	}
	f.shows.hits["Pioneer One"] = []metadata.SearchHit{
		{Kind: "show", Provider: "tvmaze", ProviderID: "99001", Name: "Pioneer One"},
		{Kind: "show", Provider: "tvmaze", ProviderID: "99002", Name: "Pioneer One: The Making Of"},
	}
	f.artists.hits["chopin"] = []metadata.SearchHit{
		{Kind: "artist", Provider: "musicbrainz", ProviderID: strings.ToUpper(chopinMBID), Name: "Frédéric Chopin"},
		{Kind: "artist", Provider: "musicbrainz", ProviderID: otherArtist, Name: "Chopin"},
	}

	for _, tc := range []struct {
		kind, q string
		inID    int64
	}{{"movie", "Metropolis", mov.ID}, {"show", "Pioneer One", shw.ID}, {"artist", "chopin", art.ID}} {
		res, err := f.svc.Lookup(bg(), tc.kind, tc.q)
		if err != nil {
			t.Fatalf("%s: %v", tc.kind, err)
		}
		if len(res) != 2 {
			t.Fatalf("%s: results = %+v", tc.kind, res)
		}
		if !res[0].InLibrary || res[0].TitleID != tc.inID {
			t.Errorf("%s: first = %+v, want in library as %d", tc.kind, res[0], tc.inID)
		}
		if res[1].InLibrary || res[1].TitleID != 0 {
			t.Errorf("%s: second = %+v, want not in library", tc.kind, res[1])
		}
		if res[0].Name == "" || res[0].Provider == "" {
			t.Errorf("%s: the provider's fields were lost: %+v", tc.kind, res[0])
		}
	}
}

func TestLookupSavesNothing(t *testing.T) {
	f := newFixture(t)
	f.movies.hits["Sintel"] = []metadata.SearchHit{{Kind: "movie", Provider: "tmdb", ProviderID: "45745", Name: "Sintel"}}
	res, err := f.svc.Lookup(bg(), "movie", "Sintel")
	if err != nil || len(res) != 1 {
		t.Fatalf("%v %v", res, err)
	}
	if res[0].InLibrary {
		t.Error("an unsaved title is marked in library")
	}
	if f.count(`SELECT COUNT(*) FROM titles`) != 0 || f.movies.gets.Load() != 0 {
		t.Error("Lookup saved something or fetched full details")
	}
}

func TestLookupMovieYearHandling(t *testing.T) {
	f := newFixture(t)
	f.movies.hits["Metropolis"] = []metadata.SearchHit{{Kind: "movie", ProviderID: "19", Name: "Metropolis", Year: 1927}}
	f.movies.hits["Blade Runner 2049"] = []metadata.SearchHit{{Kind: "movie", ProviderID: "335984", Name: "Blade Runner 2049", Year: 2017}}

	for _, q := range []string{"Metropolis 1927", "Metropolis (1927)", "  metropolis" + " (1927) "} {
		f.movies.searches = nil
		if _, err := f.svc.Lookup(bg(), "movie", q); err != nil {
			t.Fatal(err)
		}
		if len(f.movies.searches) < 1 || f.movies.searches[0].year != 1927 || !strings.EqualFold(f.movies.searches[0].q, "Metropolis") {
			t.Errorf("%q searched %+v, want name Metropolis with year 1927", q, f.movies.searches)
		}
	}

	// "2049" is far enough ahead not to be taken for a release year.
	f.movies.searches = nil
	res, err := f.svc.Lookup(bg(), "movie", "Blade Runner 2049")
	if err != nil || len(res) != 1 {
		t.Fatalf("%v %v", res, err)
	}
	if len(f.movies.searches) != 1 || f.movies.searches[0].year != 0 || f.movies.searches[0].q != "Blade Runner 2049" {
		t.Errorf("searches = %+v", f.movies.searches)
	}

	// A year that finds nothing is tried again as part of the name.
	f.movies.hits["Apollo 1999"] = []metadata.SearchHit{{Kind: "movie", ProviderID: "7", Name: "Apollo 1999"}}
	f.movies.searches = nil
	res, err = f.svc.Lookup(bg(), "movie", "Apollo 1999")
	if err != nil || len(res) != 1 {
		t.Fatalf("%v %v", res, err)
	}
	if len(f.movies.searches) != 2 || f.movies.searches[0] != (searchCall{"Apollo", 1999}) || f.movies.searches[1] != (searchCall{"Apollo 1999", 0}) {
		t.Errorf("searches = %+v", f.movies.searches)
	}
}

func TestLookupShowByIMDbID(t *testing.T) {
	f := newFixture(t)
	f.shows.byIMDb["tt9900001"] = &metadata.SearchHit{Kind: "show", Provider: "tvmaze", ProviderID: "99001", Name: "Pioneer One", IMDbID: "tt9900001"}
	shw := f.addShow("99001", true)

	res, err := f.svc.Lookup(bg(), "show", "tt9900001")
	if err != nil || len(res) != 1 || !res[0].InLibrary || res[0].TitleID != shw.ID {
		t.Fatalf("res=%+v err=%v", res, err)
	}
	// An IMDb id TVmaze does not know is an empty result, not an error.
	res, err = f.svc.Lookup(bg(), "show", "tt0000000")
	if err != nil || len(res) != 0 {
		t.Errorf("unknown imdb: res=%+v err=%v", res, err)
	}
}

func TestLookupErrors(t *testing.T) {
	f := newFixture(t)
	if _, err := f.svc.Lookup(bg(), "book", "x"); !isInvalid(err) {
		t.Errorf("bad kind: %v", err)
	}
	if _, err := f.svc.Lookup(bg(), "movie", "   "); !isInvalid(err) {
		t.Errorf("blank query: %v", err)
	}
	// A provider failure comes through with its code, so the API can say why
	// (for example tmdb_key_missing).
	f.movies.err = metadata.ErrTMDBKeyMissing
	_, err := f.svc.Lookup(bg(), "movie", "Metropolis")
	if !errors.Is(err, metadata.ErrTMDBKeyMissing) || metadata.CodeOf(err) != "tmdb_key_missing" {
		t.Errorf("err = %v", err)
	}
	// Other kinds are not affected.
	if _, err := f.svc.Lookup(bg(), "show", "Pioneer One"); err != nil {
		t.Errorf("show lookup failed because movies have no key: %v", err)
	}
}

// ---- List ------------------------------------------------------------------

func TestListFilterSortAndPaging(t *testing.T) {
	f := newFixture(t)
	f.movies.movies["46"] = &metadata.MovieInfo{TMDBID: 46, Title: "100% Wolf", Year: 2020}
	f.movies.movies["47"] = &metadata.MovieInfo{TMDBID: 47, Title: "An Unpredictable Film", OriginalTitle: "Ein Film", Year: 1999}
	metro := f.addMovie("19", true)      // 1927
	sin := f.addMovie("45745", false)    // 2010, unmonitored
	wolf := f.addMovie("46", true)       // 2020
	unp := f.addMovie("47", true)        // 1999, sorts under U
	shw := f.addShow("7", true)          // The Americans, sorts under A
	art := f.addArtist(chopinMBID, true) // sorts "chopin, frédéric"
	ids := func(items []Title) []int64 {
		var out []int64
		for _, it := range items {
			out = append(out, it.ID)
		}
		return out
	}
	list := func(flt ListFilter) *ListResult {
		t.Helper()
		r, err := f.svc.List(bg(), flt)
		if err != nil {
			t.Fatalf("List(%+v): %v", flt, err)
		}
		return r
	}

	// Default: newest added first, with the defaults filled in.
	r := list(ListFilter{})
	want := []int64{art.ID, shw.ID, unp.ID, wolf.ID, sin.ID, metro.ID}
	if !slices.Equal(ids(r.Items), want) || r.Total != 6 || r.Limit != DefaultLimit || r.Offset != 0 {
		t.Errorf("default list = %v total %d limit %d", ids(r.Items), r.Total, r.Limit)
	}

	// By name: articles are ignored ("The Americans" under A, "An Unpredictable
	// Film" under U), and "100% Wolf" comes first.
	r = list(ListFilter{Sort: SortName})
	if want := []int64{wolf.ID, shw.ID, art.ID, metro.ID, sin.ID, unp.ID}; !slices.Equal(ids(r.Items), want) {
		t.Errorf("by name = %v, want %v", ids(r.Items), want)
	}
	// By year, newest first; ties by name. Artists with a begin year of 1810 are last.
	r = list(ListFilter{Sort: SortYear})
	if want := []int64{wolf.ID, shw.ID, sin.ID, unp.ID, metro.ID, art.ID}; !slices.Equal(ids(r.Items), want) {
		t.Errorf("by year = %v, want %v", ids(r.Items), want)
	}

	// Kind and monitored filters.
	if r := list(ListFilter{Kind: KindMovie}); r.Total != 4 {
		t.Errorf("movies = %d", r.Total)
	}
	no := false
	if r := list(ListFilter{Monitored: &no}); r.Total != 1 || r.Items[0].ID != sin.ID {
		t.Errorf("unmonitored = %+v", r)
	}
	yes := true
	if r := list(ListFilter{Kind: KindMovie, Monitored: &yes}); r.Total != 3 {
		t.Errorf("monitored movies = %d", r.Total)
	}

	// Search matches the name or an alternative name, ignores case, and treats
	// % and _ as ordinary characters.
	for _, tc := range []struct {
		q    string
		want []int64
	}{
		{"METRO", []int64{metro.ID}},
		{"film", []int64{unp.ID}},
		{"ein film", []int64{unp.ID}}, // the alternative name
		{"fryderyk", []int64{art.ID}}, // an artist's alias
		{"100%", []int64{wolf.ID}},    // % is not a wildcard
		{"%", []int64{wolf.ID}},       // only the title that really has a %
		{"m_tro", nil},                // _ is not a wildcard
		{"  americans ", []int64{shw.ID}},
		{"nothing like this", nil},
	} {
		r := list(ListFilter{Q: tc.q, Sort: SortName})
		if !slices.Equal(ids(r.Items), tc.want) || r.Total != len(tc.want) {
			t.Errorf("q=%q: %v (total %d), want %v", tc.q, ids(r.Items), r.Total, tc.want)
		}
	}

	// Paging reports the total of all matches.
	r = list(ListFilter{Sort: SortName, Limit: 2, Offset: 2})
	if !slices.Equal(ids(r.Items), []int64{art.ID, metro.ID}) || r.Total != 6 || r.Limit != 2 || r.Offset != 2 {
		t.Errorf("page = %v total %d", ids(r.Items), r.Total)
	}
	if r := list(ListFilter{Offset: 100}); len(r.Items) != 0 || r.Total != 6 || r.Items == nil {
		t.Errorf("past the end = %+v", r)
	}
	if r := list(ListFilter{Limit: 100000}); r.Limit != MaxLimit {
		t.Errorf("limit = %d, want it capped at %d", r.Limit, MaxLimit)
	}

	// Bad input.
	for _, flt := range []ListFilter{{Kind: "book"}, {Sort: "random"}, {Offset: -1}} {
		if _, err := f.svc.List(bg(), flt); !isInvalid(err) {
			t.Errorf("List(%+v) = %v, want an InvalidError", flt, err)
		}
	}
}

func TestListWhenEmptyIsAnEmptyArray(t *testing.T) {
	f := newFixture(t)
	r, err := f.svc.List(bg(), ListFilter{})
	if err != nil || r.Items == nil || len(r.Items) != 0 || r.Total != 0 {
		t.Errorf("%+v %v", r, err)
	}
}

// ---- monitored, delete -----------------------------------------------------

func TestSetMonitoredCascades(t *testing.T) {
	f := newFixture(t)
	shw := f.addShow("99001", true)
	art := f.addArtist(chopinMBID, true)

	if err := f.svc.SetMonitored(bg(), shw.ID, false); err != nil {
		t.Fatal(err)
	}
	if err := f.svc.SetMonitored(bg(), art.ID, false); err != nil {
		t.Fatal(err)
	}
	off, _ := f.svc.Get(bg(), shw.ID)
	if off.Monitored {
		t.Error("title still monitored")
	}
	for _, s := range off.Seasons {
		if s.Monitored {
			t.Errorf("season %d still monitored", s.Number)
		}
	}
	for _, e := range off.Episodes {
		if e.Monitored {
			t.Errorf("episode %dx%d still monitored", e.Season, e.Number)
		}
	}
	artOff, _ := f.svc.Get(bg(), art.ID)
	for _, a := range artOff.Albums {
		if a.Monitored {
			t.Errorf("album %s still monitored", a.Name)
		}
	}

	if err := f.svc.SetMonitored(bg(), shw.ID, true); err != nil {
		t.Fatal(err)
	}
	if err := f.svc.SetMonitored(bg(), art.ID, true); err != nil {
		t.Fatal(err)
	}
	on, _ := f.svc.Get(bg(), shw.ID)
	if !on.Monitored {
		t.Error("title not monitored")
	}
	for _, s := range on.Seasons {
		if s.Monitored != (s.Number > 0) {
			t.Errorf("season %d monitored=%v (specials stay off)", s.Number, s.Monitored)
		}
	}
	for _, e := range on.Episodes {
		if e.Monitored != (e.Season > 0) {
			t.Errorf("episode %dx%d monitored=%v", e.Season, e.Number, e.Monitored)
		}
	}
	artOn, _ := f.svc.Get(bg(), art.ID)
	for _, a := range artOn.Albums {
		if !a.Monitored {
			t.Errorf("album %s not monitored", a.Name)
		}
	}

	if err := f.svc.SetMonitored(bg(), 9999, true); !errors.Is(err, ErrNotFound) {
		t.Errorf("unknown id: %v", err)
	}
}

func TestDeleteRemovesCatalogRowsOnly(t *testing.T) {
	f := newFixture(t)
	dir := t.TempDir()
	keep := filepath.Join(dir, "Pioneer One S01E01.mkv")
	if err := os.WriteFile(keep, []byte("not a real video"), 0o600); err != nil {
		t.Fatal(err)
	}
	shw, _, err := f.svc.Add(bg(), KindShow, "tvmaze", "99001", AddOptions{Monitored: true, RootFolder: dir})
	if err != nil {
		t.Fatal(err)
	}
	art := f.addArtist(chopinMBID, true)
	other := f.addMovie("19", true)

	if err := f.svc.Delete(bg(), shw.ID); err != nil {
		t.Fatal(err)
	}
	if err := f.svc.Delete(bg(), art.ID); err != nil {
		t.Fatal(err)
	}
	for _, table := range []string{"seasons", "episodes", "albums", "tracks"} {
		if n := f.count(`SELECT COUNT(*) FROM ` + table); n != 0 {
			t.Errorf("%s has %d rows left", table, n)
		}
	}
	if n := f.count(`SELECT COUNT(*) FROM people WHERE title_id IN (?, ?)`, shw.ID, art.ID); n != 0 {
		t.Errorf("people left: %d", n)
	}
	if _, err := f.svc.Get(bg(), shw.ID); !errors.Is(err, ErrNotFound) {
		t.Errorf("Get after delete: %v", err)
	}
	if _, err := os.Stat(keep); err != nil {
		t.Errorf("Delete touched files: %v", err)
	}
	if _, err := f.svc.Get(bg(), other.ID); err != nil {
		t.Errorf("another title went with it: %v", err)
	}
	if err := f.svc.Delete(bg(), shw.ID); !errors.Is(err, ErrNotFound) {
		t.Errorf("second delete: %v", err)
	}

	// The same title can be added again afterwards.
	if _, created, err := f.svc.Add(bg(), KindShow, "tvmaze", "99001", AddOptions{Monitored: true}); err != nil || !created {
		t.Errorf("re-add: %v created=%v", err, created)
	}
}

// ---- helpers for the library matcher ---------------------------------------

func TestCandidates(t *testing.T) {
	f := newFixture(t)
	f.addMovie("19", true)
	f.addMovie("45745", false)
	f.addShow("99001", true)
	f.addArtist(chopinMBID, true)

	movies, err := f.svc.Candidates(bg(), KindMovie)
	if err != nil {
		t.Fatal(err)
	}
	if len(movies) != 2 {
		t.Fatalf("movies = %+v", movies)
	}
	m := movies[0]
	if m.ID == 0 || m.Kind != KindMovie || m.Name != "Metropolis" || m.Year != 1927 || !slices.Equal(m.AltNames, []string{"Metropolis (Fritz Lang)"}) {
		t.Errorf("candidate = %+v", m)
	}
	if movies[1].Name != "Sintel" || len(movies[1].AltNames) != 0 || movies[1].AltNames == nil {
		t.Errorf("candidate without alt names = %+v (AltNames should be empty, not nil)", movies[1])
	}

	shows, _ := f.svc.Candidates(bg(), KindShow)
	if len(shows) != 1 || shows[0].Name != "Pioneer One" || shows[0].Year != 2010 || shows[0].Kind != KindShow {
		t.Errorf("shows = %+v", shows)
	}
	artists, _ := f.svc.Candidates(bg(), KindArtist)
	if len(artists) != 1 || !slices.Equal(artists[0].AltNames, []string{"Fryderyk Chopin", "Chopin"}) {
		t.Errorf("artists = %+v", artists)
	}
	// Unmonitored titles are still candidates: files for them still exist.
	if all, _ := f.svc.Candidates(bg(), ""); len(all) != 4 {
		t.Errorf("all kinds = %d, want 4", len(all))
	}
	if _, err := f.svc.Candidates(bg(), "book"); !isInvalid(err) {
		t.Errorf("bad kind: %v", err)
	}
	empty := NewService(newTestDB(t), Providers{}, Options{})
	if c, err := empty.Candidates(bg(), KindMovie); err != nil || c == nil || len(c) != 0 {
		t.Errorf("empty catalog: %v %v", c, err)
	}
}

func TestEpisodeID(t *testing.T) {
	f := newFixture(t)
	shw := f.addShow("99001", true)
	for _, e := range shw.Episodes {
		id, ok, err := f.svc.EpisodeID(bg(), shw.ID, e.Season, e.Number)
		if err != nil || !ok || id != e.ID {
			t.Errorf("EpisodeID(%dx%d) = %d %v %v, want %d", e.Season, e.Number, id, ok, err, e.ID)
		}
	}
	for _, tc := range []struct {
		title    int64
		s, n     int
		describe string
	}{{shw.ID, 1, 99, "no such episode"}, {shw.ID, 9, 1, "no such season"}, {9999, 1, 1, "no such show"}} {
		if id, ok, err := f.svc.EpisodeID(bg(), tc.title, tc.s, tc.n); ok || id != 0 || err != nil {
			t.Errorf("%s: %d %v %v", tc.describe, id, ok, err)
		}
	}
}

// ---- pure helpers ----------------------------------------------------------

func TestSplitYear(t *testing.T) {
	now := time.Date(2026, 11, 20, 0, 0, 0, 0, time.UTC)
	for _, tc := range []struct {
		in   string
		name string
		year int
	}{
		{"Metropolis 1927", "Metropolis", 1927},
		{"Metropolis (1927)", "Metropolis", 1927},
		{"  Metropolis   (1927) ", "Metropolis", 1927},
		{"Metropolis", "Metropolis", 0},
		{"1984", "1984", 0},
		{"2001 A Space Odyssey", "2001 A Space Odyssey", 0},
		{"Blade Runner 2049", "Blade Runner 2049", 0}, // beyond next year: part of the name
		{"Dune 2027", "Dune", 2027},                   // next year is plausible
		{"Sintel 2010 ", "Sintel", 2010},
		{"Room 1812x", "Room 1812x", 0},
	} {
		name, year := splitYear(tc.in, now)
		if strings.TrimSpace(name) != tc.name || year != tc.year {
			t.Errorf("splitYear(%q) = %q, %d; want %q, %d", tc.in, name, year, tc.name, tc.year)
		}
	}
}

func TestSortName(t *testing.T) {
	for in, want := range map[string]string{
		"The Americans": "americans", "A Quiet Place": "quiet place", "An Unpredictable Film": "unpredictable film",
		"Theory of Everything": "theory of everything", "The": "the", "  Sintel ": "sintel", "Ánima": "ánima",
	} {
		if got := sortName(in); got != want {
			t.Errorf("sortName(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestAltNames(t *testing.T) {
	got := altNames("Metropolis", "metropolis", "", "  Metrópolis ", "METRÓPOLIS", "Metropolis 1927")
	if !slices.Equal(got, []string{"Metrópolis", "Metropolis 1927"}) {
		t.Errorf("altNames = %v", got)
	}
	if got := altNames("X"); got == nil || len(got) != 0 {
		t.Errorf("no alternatives should be an empty slice, got %#v", got)
	}
}
