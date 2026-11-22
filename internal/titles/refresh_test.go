package titles

import (
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/BaileyPillon/marquee/internal/core/jobs"
	"github.com/BaileyPillon/marquee/internal/core/outbound"
	"github.com/BaileyPillon/marquee/internal/metadata"
)

// A title stored with the provider's primary year (2024) takes the first
// release year (2021) on its next refresh. Year is provider data, not a field
// the user can set, so refresh overwrites it; the calendar's primary date stays.
func TestRefreshCorrectsAMoviesYearToItsFirstRelease(t *testing.T) {
	f := newFixture(t)
	old := &metadata.MovieInfo{TMDBID: 891761, Title: "Sprite Fright", OriginalTitle: "Sprite Fright", Year: 2024, ReleaseDate: "2024-03-01"}
	f.movies.movies["891761"] = old
	d, _, err := f.svc.Add(bg(), KindMovie, metadata.ProviderTMDB, "891761", AddOptions{Monitored: true, RootFolder: "/movies"})
	if err != nil {
		t.Fatal(err)
	}
	if d.Year != 2024 {
		t.Fatalf("year before = %d", d.Year)
	}

	fresh := *old
	fresh.Year = 2021 // what metadata now reports: the 2021-10-29 premiere
	f.movies.movies["891761"] = &fresh
	after, err := f.svc.Refresh(bg(), d.ID)
	if err != nil {
		t.Fatal(err)
	}
	if after.Year != 2021 || after.ReleaseDate != "2024-03-01" {
		t.Errorf("after refresh: year=%d releaseDate=%q, want 2021 and the primary date kept", after.Year, after.ReleaseDate)
	}
	if !after.Monitored || after.RootFolder != "/movies" {
		t.Errorf("user choices changed: monitored=%v folder=%q", after.Monitored, after.RootFolder)
	}
}

func TestRefreshUpdatesTheTitleAndKeepsWhatTheUserChose(t *testing.T) {
	f := newFixture(t)
	d, _, err := f.svc.Add(bg(), KindShow, "tvmaze", "99001", AddOptions{Monitored: true, RootFolder: "/tv"})
	if err != nil {
		t.Fatal(err)
	}
	if err := f.svc.SetMonitored(bg(), d.ID, false); err != nil {
		t.Fatal(err)
	}
	// The user (through a later milestone's controls) wants one episode of an
	// otherwise unmonitored show.
	if _, err := f.db.Exec(`UPDATE episodes SET monitored = 1 WHERE title_id = ? AND season = 1 AND number = 2`, d.ID); err != nil {
		t.Fatal(err)
	}
	before, _ := f.svc.Get(bg(), d.ID)

	next := pioneerOne()
	next.Name, next.Overview, next.Status = "Pioneer One (2010)", "A newer synopsis.", "continuing"
	next.Poster = metadata.Image{Ref: "tvmaze/uploads/images/original_untouched/99/new.jpg"}
	next.Episodes = []metadata.EpisodeInfo{ // S1E2 is gone from the provider's list
		{Season: 0, Number: 1, Name: "Behind the Scenes", AirDate: "2010-07-10"},
		{Season: 1, Number: 1, Name: "Earthfall (renamed)", AirDate: "2010-06-19", Overview: "New text."},
		{Season: 2, Number: 1, Name: "Landing Zone", AirDate: "2011-01-30"},
		{Season: 2, Number: 2, Name: "Fallout", AirDate: "2011-02-06"},
		{Season: 3, Number: 1, Name: "Third Wave", AirDate: "2012-01-01"},
	}
	next.Seasons = nil
	next.People = nil // an empty cast list means "unknown", not "everyone left"
	f.shows.shows["99001"] = next

	after, err := f.svc.Refresh(bg(), d.ID)
	if err != nil {
		t.Fatal(err)
	}

	if after.Name != "Pioneer One (2010)" || after.Overview != "A newer synopsis." || after.Status != "continuing" ||
		after.PosterRef != "tvmaze/uploads/images/original_untouched/99/new.jpg" {
		t.Errorf("fields not updated: %+v", after.Title)
	}
	if after.Monitored || after.RootFolder != "/tv" || !after.AddedAt.Equal(before.AddedAt) {
		t.Errorf("user choices changed: monitored=%v folder=%q added=%v", after.Monitored, after.RootFolder, after.AddedAt)
	}
	if !after.RefreshedAt.After(before.RefreshedAt) || !after.UpdatedAt.After(before.UpdatedAt) {
		t.Errorf("refreshed_at/updated_at did not move: %v -> %v", before.RefreshedAt, after.RefreshedAt)
	}

	byKey := map[string]Episode{}
	for _, e := range after.Episodes {
		byKey[fmt.Sprintf("%dx%d", e.Season, e.Number)] = e
	}
	oldKey := map[string]Episode{}
	for _, e := range before.Episodes {
		oldKey[fmt.Sprintf("%dx%d", e.Season, e.Number)] = e
	}
	if len(after.Episodes) != 6 {
		t.Errorf("episodes = %d, want 6 (4 old, 2 new; none deleted)", len(after.Episodes))
	}
	if e := byKey["1x2"]; e.ID == 0 || e.Name != "Anomalies" || !e.Monitored {
		t.Errorf("an episode the provider dropped must stay, with the user's flag: %+v", e)
	}
	if e := byKey["1x1"]; e.Name != "Earthfall (renamed)" || e.Overview != "New text." || e.ID != oldKey["1x1"].ID {
		t.Errorf("existing episode = %+v (id was %d)", e, oldKey["1x1"].ID)
	}
	for _, k := range []string{"2x2", "3x1"} {
		if e := byKey[k]; e.ID == 0 || e.Monitored {
			t.Errorf("new episode %s = %+v; the show is unmonitored so it must be too", k, e)
		}
	}
	var seasons []int
	for _, s := range after.Seasons {
		seasons = append(seasons, s.Number)
	}
	if !slices.Equal(seasons, []int{0, 1, 2, 3}) {
		t.Errorf("seasons = %v", seasons)
	}
	if len(after.People) != 1 || after.People[0].Name != "Joanna Gaskell" {
		t.Errorf("people = %+v, want the old cast kept", after.People)
	}

	// Monitoring on, then a refresh: new episodes follow their season.
	if err := f.svc.SetMonitored(bg(), d.ID, true); err != nil {
		t.Fatal(err)
	}
	next2 := *next
	next2.Episodes = append(slices.Clone(next.Episodes),
		metadata.EpisodeInfo{Season: 2, Number: 3, Name: "Aftermath"},
		metadata.EpisodeInfo{Season: 0, Number: 2, Name: "Outtakes", Special: true},
		metadata.EpisodeInfo{Season: 4, Number: 1, Name: "Fourth Wave"})
	next2.People = []metadata.Person{{Name: "New Actor", Role: "actor", Job: "Someone"}}
	f.shows.shows["99001"] = &next2
	after, err = f.svc.Refresh(bg(), d.ID)
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range after.Episodes {
		k := fmt.Sprintf("%dx%d", e.Season, e.Number)
		switch k {
		case "2x3", "4x1":
			if !e.Monitored {
				t.Errorf("new episode %s should follow its monitored season", k)
			}
		case "0x2":
			if e.Monitored {
				t.Error("a new special must not be monitored")
			}
		}
	}
	if len(after.People) != 1 || after.People[0].Name != "New Actor" {
		t.Errorf("people = %+v, want the new cast to replace the old", after.People)
	}
}

func TestRefreshArtistAddsAlbumsAndFollowsTracks(t *testing.T) {
	f := newFixture(t)
	d := f.addArtist(chopinMBID, true)
	// One album the user unmonitored.
	if _, err := f.db.Exec(`UPDATE albums SET monitored = 0 WHERE mbid = ?`, nocturnesMB); err != nil {
		t.Fatal(err)
	}
	var etudesTrackID int64
	for _, a := range d.Albums {
		if a.MBID == etudesMB {
			etudesTrackID = a.Tracks[0].ID
		}
	}

	next := chopin()
	next.Albums[0].Tracks = []metadata.TrackInfo{ // Nocturnes: one track renamed, two others gone
		{Disc: 1, Position: 1, Title: "Nocturne in B-flat minor (new title)", LengthMS: 331000, MBID: "e0000000-0000-4000-8000-000000000401"},
	}
	next.Albums[1].Tracks = nil // Études: the track list is unknown this time
	next.Albums = append(next.Albums, metadata.AlbumInfo{
		MBID: "b0000000-0000-4000-8000-000000000103", Title: "Ballades", Year: 2003, Type: "album",
		Cover:  metadata.Image{Ref: "caa/release-group/b0000000-0000-4000-8000-000000000103/front-500"},
		Tracks: []metadata.TrackInfo{{Disc: 1, Position: 1, Title: "Ballade No. 1", LengthMS: 570000}},
	})
	f.artists.artists[chopinMBID] = next

	after, err := f.svc.Refresh(bg(), d.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(after.Albums) != 3 {
		t.Fatalf("albums = %d, want 3", len(after.Albums))
	}
	byMBID := map[string]Album{}
	for _, a := range after.Albums {
		byMBID[a.MBID] = a
	}
	noc := byMBID[nocturnesMB]
	if noc.Monitored {
		t.Error("the user's unmonitored flag on an album was overwritten")
	}
	if len(noc.Tracks) != 1 || noc.Tracks[0].Name != "Nocturne in B-flat minor (new title)" || noc.Tracks[0].LengthMS != 331000 {
		t.Errorf("tracks = %+v, want the list to follow the provider", noc.Tracks)
	}
	et := byMBID[etudesMB]
	if len(et.Tracks) != 1 || et.Tracks[0].ID != etudesTrackID {
		t.Errorf("an empty answer must keep the old tracks: %+v", et.Tracks)
	}
	bal := byMBID["b0000000-0000-4000-8000-000000000103"]
	if !bal.Monitored || bal.Type != "album" || len(bal.Tracks) != 1 {
		t.Errorf("new album = %+v (should follow the artist's monitored flag)", bal)
	}
	if after.PosterRef != "caa/release-group/b0000000-0000-4000-8000-000000000103/front-500" {
		t.Errorf("poster = %q, want the newest cover", after.PosterRef)
	}
}

func TestRefreshMovieFillsAMissingIMDbIDOnlyIfFree(t *testing.T) {
	f := newFixture(t)
	noIMDb := sintel()
	noIMDb.IMDbID = ""
	f.movies.movies["45745"] = noIMDb
	d := f.addMovie("45745", true)
	if d.IMDbID != "" {
		t.Fatalf("imdb = %q", d.IMDbID)
	}

	f.movies.movies["45745"] = sintel() // now with tt1727587
	after, err := f.svc.Refresh(bg(), d.ID)
	if err != nil || after.IMDbID != "tt1727587" {
		t.Fatalf("imdb = %q err = %v", after.IMDbID, err)
	}

	// A second title whose provider data claims an IMDb id another row holds
	// refreshes without failing and without stealing it.
	f.movies.movies["46"] = &metadata.MovieInfo{TMDBID: 46, Title: "Other"}
	other := f.addMovie("46", true)
	f.movies.movies["46"] = &metadata.MovieInfo{TMDBID: 46, Title: "Other", IMDbID: "tt1727587"}
	got, err := f.svc.Refresh(bg(), other.ID)
	if err != nil {
		t.Fatalf("refresh with a taken imdb id failed: %v", err)
	}
	if got.IMDbID != "" {
		t.Errorf("imdb id = %q, it belongs to another title", got.IMDbID)
	}
}

func TestRefreshErrorsLeaveTheTitleAlone(t *testing.T) {
	f := newFixture(t)
	d := f.addShow("99001", true)

	if _, err := f.svc.Refresh(bg(), 9999); !errors.Is(err, ErrNotFound) {
		t.Errorf("unknown title: %v", err)
	}
	f.shows.err = &metadata.Error{Code: "upstream_error", Message: "TVmaze did not answer."}
	if _, err := f.svc.Refresh(bg(), d.ID); metadata.CodeOf(err) != "upstream_error" {
		t.Errorf("provider down: %v", err)
	}
	f.shows.err = metadata.ErrNotFound
	if _, err := f.svc.Refresh(bg(), d.ID); !errors.Is(err, metadata.ErrNotFound) {
		t.Errorf("provider says gone: %v", err)
	}
	after, err := f.svc.Get(bg(), d.ID)
	if err != nil || after.Name != d.Name || len(after.Episodes) != len(d.Episodes) || !after.RefreshedAt.Equal(d.RefreshedAt) {
		t.Errorf("a failed refresh changed the title: %+v %v", after, err)
	}
}

// ---- the scheduled job -----------------------------------------------------

func TestRefreshMonitoredRefreshesOnlyMonitoredTitles(t *testing.T) {
	f := newFixture(t)
	shw := f.addShow("99001", true)
	off := f.addMovie("45745", false) // unmonitored: left alone
	_ = off
	startGets := f.movies.gets.Load()

	next := pioneerOne()
	next.Overview = "Refreshed by the job."
	f.shows.shows["99001"] = next
	f.movies.movies["45745"] = &metadata.MovieInfo{TMDBID: 45745, Title: "Sintel (changed)"}

	if err := f.svc.RefreshMonitored(bg()); err != nil {
		t.Fatal(err)
	}
	got, _ := f.svc.Get(bg(), shw.ID)
	if got.Overview != "Refreshed by the job." {
		t.Errorf("monitored show not refreshed: %q", got.Overview)
	}
	if f.movies.gets.Load() != startGets {
		t.Error("an unmonitored title was refreshed")
	}
	if m, _ := f.svc.Candidates(bg(), KindMovie); len(m) != 1 || m[0].Name != "Sintel" {
		t.Errorf("unmonitored movie changed: %+v", m)
	}
}

func TestRefreshMonitoredCarriesOnPastFailuresAndMissingKey(t *testing.T) {
	f := newFixture(t)
	f.addShow("99001", true)
	f.addShow("7", true) // this one will fail
	f.addMovie("19", true)

	next := pioneerOne()
	next.Overview = "Still refreshed."
	f.shows.shows["99001"] = next
	f.shows.failID = "7"
	f.movies.err = metadata.ErrTMDBKeyMissing // movies cannot refresh without a key; that is not a failure

	err := f.svc.RefreshMonitored(bg())
	if err == nil {
		t.Fatal("a failing title should make the job report an error")
	}
	if !strings.Contains(err.Error(), "1 of 3") || !strings.Contains(err.Error(), "TVmaze did not answer") {
		t.Errorf("job error = %v", err)
	}
	cands, _ := f.svc.Candidates(bg(), KindShow)
	for _, c := range cands {
		if c.Name == "Pioneer One" {
			d, _ := f.svc.Get(bg(), c.ID)
			if d.Overview != "Still refreshed." {
				t.Errorf("the healthy show was not refreshed: %q", d.Overview)
			}
		}
	}

	// With the fault gone, the job is clean, and a missing TMDB key alone is not an error.
	f.shows.failID = ""
	if err := f.svc.RefreshMonitored(bg()); err != nil {
		t.Errorf("clean run: %v", err)
	}
}

func TestRefreshMonitoredLimitsConcurrency(t *testing.T) {
	f := newFixture(t)
	for i := 0; i < 6; i++ {
		id := fmt.Sprint(200 + i)
		f.shows.shows[id] = &metadata.ShowInfo{TVmazeID: int64(200 + i), Name: "Show " + id}
		f.addShow(id, true)
	}
	svc := NewService(f.db, Providers{Shows: f.shows}, Options{Now: f.clock.Now, RefreshConcurrency: 2})
	f.shows.delay = 30 * time.Millisecond
	f.shows.peak.Store(0)

	if err := svc.RefreshMonitored(bg()); err != nil {
		t.Fatal(err)
	}
	if peak := f.shows.peak.Load(); peak != 2 {
		t.Errorf("peak concurrent refreshes = %d, want exactly the limit of 2", peak)
	}
}

func TestRefreshMonitoredStopsWhenCancelled(t *testing.T) {
	f := newFixture(t)
	f.addShow("99001", true)
	f.addShow("7", true)
	ctx, cancel := contextCancelled()
	defer cancel()
	startGets := f.shows.gets.Load()
	if err := f.svc.RefreshMonitored(ctx); err != nil {
		t.Errorf("a cancelled run is a shutdown, not a failure: %v", err)
	}
	if f.shows.gets.Load() > startGets+1 {
		t.Errorf("the job kept calling providers after cancel: %d calls", f.shows.gets.Load()-startGets)
	}
}

func TestRefreshMonitoredRunsAsAScheduledJob(t *testing.T) {
	f := newFixture(t)
	f.addShow("99001", true)

	sched := jobs.New(nil)
	if err := sched.Every("titles.refresh", 12*time.Hour, f.svc.RefreshMonitored); err != nil {
		t.Fatal(err)
	}
	if err := sched.RunNow("titles.refresh"); err != nil {
		t.Fatalf("RunNow: %v", err)
	}
	st := sched.Status()
	if len(st) != 1 || st[0].Name != "titles.refresh" || st[0].Runs != 1 || st[0].Failures != 0 || st[0].Interval != 12*time.Hour {
		t.Errorf("job status = %+v", st)
	}
	f.shows.err = errors.New("boom")
	if err := sched.RunNow("titles.refresh"); err == nil {
		t.Error("a failing refresh should fail the job so it shows in the status")
	}
	if st := sched.Status(); st[0].Failures != 1 || st[0].LastErr == "" {
		t.Errorf("job status after a failure = %+v", st)
	}
}

// ---- with the real metadata module -----------------------------------------

// TestRefreshSkipsTheMetadataCache runs a real metadata service against a fake
// TVmaze: a refresh must ask TVmaze again even though the details were cached
// a moment ago, or "refresh" would never show anything new for 24 hours.
func TestRefreshSkipsTheMetadataCache(t *testing.T) {
	name := atomic.Value{}
	name.Store("Test Show")
	var hits atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/shows/501" {
			http.NotFound(w, r)
			return
		}
		hits.Add(1)
		fmt.Fprintf(w, `{"id":501,"name":%q,"premiered":"2020-01-01","status":"Running","genres":["Drama"],"externals":{"imdb":"tt0000501"},
			"_embedded":{"seasons":[],"cast":[],"episodes":[{"id":1,"name":"Pilot","season":1,"number":1,"type":"regular","airdate":"2020-01-01"}]}}`,
			name.Load().(string))
	}))
	defer srv.Close()

	d := newTestDB(t)
	hc := outbound.New("1.2.3", outbound.Options{MaxRetries: -1})
	meta, err := metadata.New(d, hc, metadata.Options{TVmazeURL: srv.URL, ArtworkDir: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	svc := NewService(d, Providers{Shows: meta.TVmaze}, Options{})

	added, created, err := svc.Add(bg(), KindShow, "tvmaze", "501", AddOptions{Monitored: true})
	if err != nil || !created || added.Name != "Test Show" || len(added.Episodes) != 1 {
		t.Fatalf("Add: %+v %v %v", added, created, err)
	}
	if added.Status != "continuing" || added.IMDbID != "tt0000501" {
		t.Errorf("show = %+v", added.Title)
	}

	name.Store("Test Show Renamed")
	got, err := svc.Refresh(bg(), added.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.Name != "Test Show Renamed" {
		t.Errorf("name after refresh = %q: the refresh was answered from the cache", got.Name)
	}
	if hits.Load() != 2 {
		t.Errorf("TVmaze was asked %d times, want 2 (add, refresh)", hits.Load())
	}
}
