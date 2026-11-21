package calendar

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"path/filepath"
	"reflect"
	"strconv"
	"testing"
	"time"

	"github.com/BaileyPillon/marquee/internal/core/db"
	"github.com/BaileyPillon/marquee/internal/metadata"
	"github.com/BaileyPillon/marquee/internal/titles"
)

func bg() context.Context { return context.Background() }

var today = time.Date(2026, 11, 21, 15, 0, 0, 0, time.UTC)

func day(s string) time.Time {
	t, err := time.Parse(DateLayout, s)
	if err != nil {
		panic(err)
	}
	return t
}

// ---- fake providers ---------------------------------------------------------

type fakeShows struct{ shows map[string]*metadata.ShowInfo }

func (f fakeShows) SearchShows(context.Context, string) ([]metadata.SearchHit, error) {
	return nil, nil
}
func (f fakeShows) ShowByIMDb(context.Context, string) (*metadata.SearchHit, error) {
	return nil, metadata.ErrNotFound
}
func (f fakeShows) Show(_ context.Context, id string) (*metadata.ShowInfo, error) {
	if s, ok := f.shows[id]; ok {
		cp := *s
		return &cp, nil
	}
	return nil, metadata.ErrNotFound
}

type fakeMovies struct {
	movies map[string]*metadata.MovieInfo
}

func (f fakeMovies) SearchMovies(context.Context, string, int) ([]metadata.SearchHit, error) {
	return nil, nil
}
func (f fakeMovies) Movie(_ context.Context, id string) (*metadata.MovieInfo, error) {
	if m, ok := f.movies[id]; ok {
		cp := *m
		return &cp, nil
	}
	return nil, metadata.ErrNotFound
}

// ---- the rig ----------------------------------------------------------------

type env struct {
	t   *testing.T
	db  *sql.DB
	cat *titles.Service
	svc *Service
	now time.Time
}

func newEnv(t *testing.T) *env {
	t.Helper()
	d, err := db.Open(filepath.Join(t.TempDir(), db.MainFile))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { d.Close() })
	if _, err := db.Migrate(bg(), d, db.SetMain); err != nil {
		t.Fatal(err)
	}
	e := &env{t: t, db: d, now: today}
	e.cat = titles.NewService(d, titles.Providers{
		Shows: fakeShows{shows: map[string]*metadata.ShowInfo{
			"1": {TVmazeID: 1, Name: "Alpha Show", Year: 2026, Seasons: []metadata.SeasonInfo{{Number: 0}, {Number: 1}}, Episodes: []metadata.EpisodeInfo{
				{Season: 0, Number: 1, Name: "Special", AirDate: "2026-11-22"},
				{Season: 1, Number: 1, Name: "Aired long ago", AirDate: "2026-10-01"},
				{Season: 1, Number: 2, Name: "Downloading", AirDate: "2026-11-14"},
				{Season: 1, Number: 3, Name: "Never fetched", AirDate: "2026-11-15"},
				{Season: 1, Number: 4, Name: "Today", AirDate: "2026-11-21"},
				{Season: 1, Number: 5, Name: "Next week", AirDate: "2026-11-28"},
				{Season: 1, Number: 6, Name: "In a month", AirDate: "2026-12-20"},
				{Season: 1, Number: 7, Name: "No date", AirDate: ""},
			}},
			"2": {TVmazeID: 2, Name: "Beta Show", Year: 2026, Seasons: []metadata.SeasonInfo{{Number: 1}}, Episodes: []metadata.EpisodeInfo{
				{Season: 1, Number: 1, Name: "Pack one", AirDate: "2026-11-01"},
				{Season: 1, Number: 2, Name: "Pack two", AirDate: "2026-11-08"},
				{Season: 1, Number: 3, Name: "Not aired", AirDate: "2026-11-25"},
			}},
			"3": {TVmazeID: 3, Name: "Gamma Unwanted", Year: 2026, Seasons: []metadata.SeasonInfo{{Number: 1}}, Episodes: []metadata.EpisodeInfo{
				{Season: 1, Number: 1, Name: "Nobody wants it", AirDate: "2026-11-23"},
			}},
		}},
		Movies: fakeMovies{movies: map[string]*metadata.MovieInfo{
			"10": {TMDBID: 10, Title: "Released Movie", Year: 2026, ReleaseDate: "2026-11-05"},
			"11": {TMDBID: 11, Title: "Upcoming Movie", Year: 2026, ReleaseDate: "2026-12-04"},
			"12": {TMDBID: 12, Title: "Fetched Movie", Year: 2026, ReleaseDate: "2026-11-10"},
			"13": {TMDBID: 13, Title: "Downloading Movie", Year: 2026, ReleaseDate: "2026-11-12"},
			"14": {TMDBID: 14, Title: "Year Only Movie", Year: 2026, ReleaseDate: "2026"},
			"15": {TMDBID: 15, Title: "No Date Movie", Year: 2026},
		}},
	}, titles.Options{})
	e.svc = NewService(d, Options{Now: func() time.Time { return e.now }})
	return e
}

func (e *env) add(kind, provider, id string, monitored bool) *titles.Detail {
	e.t.Helper()
	d, _, err := e.cat.Add(bg(), kind, provider, id, titles.AddOptions{Monitored: monitored})
	if err != nil {
		e.t.Fatal(err)
	}
	return d
}

func (e *env) exec(query string, args ...any) {
	e.t.Helper()
	if _, err := e.db.Exec(query, args...); err != nil {
		e.t.Fatal(err)
	}
}

func epID(d *titles.Detail, season, number int) int64 {
	for _, e := range d.Episodes {
		if e.Season == season && e.Number == number {
			return e.ID
		}
	}
	panic("no such episode")
}

func (e *env) folder(kind string) int64 {
	e.t.Helper()
	res, err := e.db.Exec(`INSERT INTO library_folders (kind, path, label, created_at) VALUES (?, ?, '', '2026-01-01T00:00:00.000Z')`, kind, "/lib/"+kind)
	if err != nil {
		e.t.Fatal(err)
	}
	id, _ := res.LastInsertId()
	return id
}

var fileN int

func (e *env) file(folder, title int64, episodes []int64, missing bool, state string) {
	e.t.Helper()
	fileN++
	var eps any
	if episodes != nil {
		b, _ := json.Marshal(episodes)
		eps = string(b)
	}
	var miss any
	if missing {
		miss = "2026-11-20T00:00:00.000Z"
	}
	if state == "" {
		state = "matched"
	}
	e.exec(`INSERT INTO files (folder_id, rel_path, size, mtime, kind, quality, title_id, episode_ids, match_state, added_at, missing_since)
		VALUES (?, ?, 1000, 0, 'show', 'WEBDL-1080p', ?, ?, ?, '2026-01-01T00:00:00.000Z', ?)`,
		folder, "f"+strconv.Itoa(fileN)+".mkv", title, eps, state, miss)
}

func (e *env) download(title int64, kind string, episodes []int64, state string, progress float64) {
	e.t.Helper()
	// Each test has its own database, so make the client again when it is missing.
	var n int
	_ = e.db.QueryRow(`SELECT COUNT(*) FROM clients`).Scan(&n)
	if n == 0 {
		e.exec(`INSERT INTO clients (name, kind, url, created_at) VALUES ('qb', 'qbittorrent', 'http://x', '2026-01-01T00:00:00.000Z')`)
	}
	b, _ := json.Marshal(append([]int64{}, episodes...))
	fileN++
	e.exec(`INSERT INTO downloads (client_id, client_ref, protocol, title_id, kind, episode_ids, state, progress, created_at, updated_at)
		VALUES ((SELECT id FROM clients LIMIT 1), ?, 'torrent', ?, ?, ?, ?, ?, '2026-01-01T00:00:00.000Z', '2026-01-01T00:00:00.000Z')`,
		"ref"+strconv.Itoa(fileN), title, kind, string(b), state, progress)
}

func (e *env) rangeItems(from, to string, opts ...Option) []Item {
	e.t.Helper()
	items, err := e.svc.Range(bg(), day(from), day(to), Viewer{}, opts...)
	if err != nil {
		e.t.Fatal(err)
	}
	return items
}

// states maps "Name S01E02" (or the movie's name) to its state.
func states(items []Item) map[string]string {
	out := map[string]string{}
	for _, it := range items {
		k := it.Name
		if it.Kind == KindEpisode {
			k += " " + it.Label
		}
		out[k] = it.State
	}
	return out
}

// ---- tests ------------------------------------------------------------------

func TestEpisodesCarryTheirState(t *testing.T) {
	e := newEnv(t)
	alpha := e.add("show", "tvmaze", "1", true)
	folder := e.folder("show")

	e.file(folder, alpha.ID, []int64{epID(alpha, 1, 1)}, false, "")               // aired, in the library
	e.download(alpha.ID, "show", []int64{epID(alpha, 1, 2)}, "downloading", 0.4)  // aired, on its way
	e.download(alpha.ID, "show", []int64{epID(alpha, 1, 2)}, "downloading", 0.15) // (a second try, further back)
	e.download(alpha.ID, "show", []int64{epID(alpha, 1, 5)}, "downloading", 0.9)  // not aired yet, but fetched early
	e.download(alpha.ID, "show", []int64{epID(alpha, 1, 3)}, "failed", 0.5)       // a download that failed is not open
	e.download(alpha.ID, "show", []int64{epID(alpha, 1, 3)}, "imported", 1)       // and neither is one that finished
	e.download(alpha.ID, "movie", []int64{epID(alpha, 1, 3)}, "removed", 0.2)     // or one that was removed

	items := e.rangeItems("2026-10-01", "2026-12-31")
	got := states(items)
	want := map[string]string{
		"Alpha Show S01E01": StateAvailable,
		"Alpha Show S01E02": StateDownloading,
		"Alpha Show S01E03": StateMissing,
		"Alpha Show S01E04": StateExpected, // airs today
		"Alpha Show S01E05": StateDownloading,
		"Alpha Show S01E06": StateExpected,
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("states =\n%v\nwant\n%v", got, want)
	}
	for _, it := range items {
		switch it.Label {
		case "S01E02":
			if it.Progress == nil || *it.Progress != 0.4 {
				t.Errorf("S01E02 progress = %v, want the furthest, 0.4", it.Progress)
			}
		case "S01E05":
			if it.Progress == nil || *it.Progress != 0.9 {
				t.Errorf("S01E05 progress = %v", it.Progress)
			}
		default:
			if it.Progress != nil {
				t.Errorf("%s has progress %v while %s", it.Label, *it.Progress, it.State)
			}
		}
		if it.Kind != KindEpisode || it.Season == nil || it.Episode == nil || it.TitleID != alpha.ID {
			t.Errorf("item = %+v", it)
		}
	}
	first := items[0]
	if first.Date != "2026-10-01" || first.EpisodeName != "Aired long ago" || first.Label != "S01E01" || *first.Season != 1 || *first.Episode != 1 {
		t.Errorf("first item = %+v", first)
	}
}

func TestAFileThatIsGoneOrUnmatchedDoesNotMakeAnEpisodeAvailable(t *testing.T) {
	e := newEnv(t)
	alpha := e.add("show", "tvmaze", "1", true)
	folder := e.folder("show")
	e.file(folder, alpha.ID, []int64{epID(alpha, 1, 1)}, true, "")                     // gone from disk
	e.file(folder, alpha.ID, []int64{epID(alpha, 1, 3)}, false, "ignored")             // ignored by the owner
	e.file(folder, alpha.ID, []int64{epID(alpha, 1, 2), epID(alpha, 1, 4)}, false, "") // one file, two episodes
	got := states(e.rangeItems("2026-10-01", "2026-12-31"))
	if got["Alpha Show S01E01"] != StateMissing || got["Alpha Show S01E03"] != StateMissing ||
		got["Alpha Show S01E02"] != StateAvailable || got["Alpha Show S01E04"] != StateAvailable {
		t.Errorf("states = %v", got)
	}
}

func TestAPackDownloadCoversTheAiredEpisodesWithoutAFile(t *testing.T) {
	e := newEnv(t)
	beta := e.add("show", "tvmaze", "2", true)
	folder := e.folder("show")
	e.file(folder, beta.ID, []int64{epID(beta, 1, 1)}, false, "")
	e.download(beta.ID, "show", nil, "downloading", 0.6) // a season pack: no episode list
	got := states(e.rangeItems("2026-11-01", "2026-11-30"))
	want := map[string]string{
		"Beta Show S01E01": StateAvailable,
		"Beta Show S01E02": StateDownloading,
		"Beta Show S01E03": StateExpected, // has not aired: the pack cannot have it
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("states = %v, want %v", got, want)
	}
}

func TestOnlyMonitoredShowsAndEpisodesAppear(t *testing.T) {
	e := newEnv(t)
	e.add("show", "tvmaze", "1", true)
	e.add("show", "tvmaze", "3", false) // not monitored
	items := e.rangeItems("2026-11-01", "2026-12-31")
	for _, it := range items {
		if it.Name == "Gamma Unwanted" {
			t.Errorf("an unmonitored show is on the calendar: %+v", it)
		}
		if it.EpisodeName == "Special" || it.EpisodeName == "No date" {
			t.Errorf("%q should not be on the calendar", it.EpisodeName)
		}
	}
	// Turning on one season of an unmonitored show puts only that in.
	d, _ := e.cat.Get(bg(), 2)
	if err := e.cat.MonitorSeasons(bg(), d.ID, []int{1}); err != nil {
		t.Fatal(err)
	}
	if got := states(e.rangeItems("2026-11-23", "2026-11-23")); got["Gamma Unwanted S01E01"] != StateExpected {
		t.Errorf("after monitoring season 1: %v", got)
	}
}

func TestMoviesAppearOnTheirReleaseDate(t *testing.T) {
	e := newEnv(t)
	released := e.add("movie", "tmdb", "10", true)
	e.add("movie", "tmdb", "11", true)
	fetched := e.add("movie", "tmdb", "12", true)
	dl := e.add("movie", "tmdb", "13", true)
	e.add("movie", "tmdb", "14", true) // a year, not a day
	e.add("movie", "tmdb", "15", true) // no date at all
	folder := e.folder("movie")
	e.file(folder, fetched.ID, nil, false, "")
	e.download(dl.ID, "movie", nil, "stalled", 0.25)
	_ = released

	items := e.rangeItems("2026-11-01", "2026-12-31")
	want := map[string]string{
		"Released Movie":    StateMissing,
		"Upcoming Movie":    StateExpected,
		"Fetched Movie":     StateAvailable,
		"Downloading Movie": StateDownloading,
	}
	if got := states(items); !reflect.DeepEqual(got, want) {
		t.Errorf("states = %v, want %v", got, want)
	}
	for _, it := range items {
		if it.Kind != KindMovie || it.Season != nil || it.Episode != nil || it.EpisodeName != "" || it.Label != "Release" {
			t.Errorf("movie item = %+v", it)
		}
		if (it.Name == "Downloading Movie") != (it.Progress != nil) {
			t.Errorf("%s progress = %v", it.Name, it.Progress)
		}
	}
	// Oldest first.
	var dates []string
	for _, it := range items {
		dates = append(dates, it.Date)
	}
	if !reflect.DeepEqual(dates, []string{"2026-11-05", "2026-11-10", "2026-11-12", "2026-12-04"}) {
		t.Errorf("dates = %v", dates)
	}
	if got := states(e.rangeItems("2026-11-06", "2026-11-11")); !reflect.DeepEqual(got, map[string]string{"Fetched Movie": StateAvailable}) {
		t.Errorf("narrow range = %v", got)
	}
}

func TestTheRangeIsInclusiveAndOrderedAndMixesKinds(t *testing.T) {
	e := newEnv(t)
	e.add("show", "tvmaze", "1", true)
	e.add("show", "tvmaze", "2", true)
	e.add("movie", "tmdb", "10", true)
	items := e.rangeItems("2026-11-01", "2026-11-05") // Pack one (Nov 1) to the movie (Nov 5), both ends included
	var got []string
	for _, it := range items {
		got = append(got, it.Date+" "+it.Name)
	}
	if want := []string{"2026-11-01 Beta Show", "2026-11-05 Released Movie"}; !reflect.DeepEqual(got, want) {
		t.Errorf("items = %v, want %v", got, want)
	}
	if one := e.rangeItems("2026-11-21", "2026-11-21"); len(one) != 1 || one[0].Label != "S01E04" {
		t.Errorf("a single day = %+v", one)
	}
	// An empty range is an empty list, which is [] in JSON.
	none := e.rangeItems("2025-01-01", "2025-01-31")
	if none == nil || len(none) != 0 {
		t.Errorf("empty range = %#v", none)
	}
	if b, _ := json.Marshal(none); string(b) != "[]" {
		t.Errorf("empty range encodes as %s", b)
	}
}

func TestTheRangeLimit(t *testing.T) {
	e := newEnv(t)
	if _, err := e.svc.Range(bg(), day("2026-01-01"), day("2026-04-03"), Viewer{}); err != nil { // 92 days
		t.Errorf("92 days: %v", err)
	}
	if _, err := e.svc.Range(bg(), day("2026-01-01"), day("2026-04-04"), Viewer{}); !errors.Is(err, ErrRangeTooLarge) { // 93 days
		t.Errorf("93 days: %v", err)
	}
	if _, err := e.svc.Range(bg(), day("2026-01-01"), day("2027-01-01"), Viewer{}); !errors.Is(err, ErrRangeTooLarge) {
		t.Errorf("a year: %v", err)
	}
	if _, err := e.svc.Range(bg(), day("2026-02-01"), day("2026-01-31"), Viewer{}); !errors.Is(err, ErrInvalidRange) {
		t.Errorf("backwards: %v", err)
	}
	// Times of day do not matter, only the dates.
	from := time.Date(2026, 1, 1, 23, 59, 0, 0, time.UTC)
	to := time.Date(2026, 4, 3, 0, 1, 0, 0, time.UTC)
	if _, err := e.svc.Range(bg(), from, to, Viewer{}); err != nil {
		t.Errorf("92 days with times of day: %v", err)
	}
}

func TestRequestedByMeAndTheMineFilter(t *testing.T) {
	e := newEnv(t)
	alpha := e.add("show", "tvmaze", "1", true)
	e.add("show", "tvmaze", "2", true)
	e.exec(`INSERT INTO users (name, role, password_hash, created_at) VALUES ('Priya', 'member', 'x', '2026-01-01T00:00:00.000Z'), ('Omar', 'member', 'x', '2026-01-01T00:00:00.000Z')`)
	e.exec(`INSERT INTO requests (user_id, title_id, kind, quality, state, created_at, updated_at) VALUES
		(1, ?, 'show', 'hd', 'approved', '2026-01-01T00:00:00.000Z', '2026-01-01T00:00:00.000Z'),
		(2, ?, 'show', 'hd', 'declined', '2026-01-01T00:00:00.000Z', '2026-01-01T00:00:00.000Z')`, alpha.ID, alpha.ID)

	priya := Viewer{UserID: 1, Role: "member"}
	items, err := e.svc.Range(bg(), day("2026-10-01"), day("2026-11-30"), priya)
	if err != nil {
		t.Fatal(err)
	}
	for _, it := range items {
		if want := it.Name == "Alpha Show"; it.RequestedByMe != want {
			t.Errorf("%s requestedByMe = %v for Priya", it.Name, it.RequestedByMe)
		}
	}
	mine, _ := e.svc.Range(bg(), day("2026-10-01"), day("2026-11-30"), priya, OnlyMine())
	if len(mine) == 0 {
		t.Fatal("OnlyMine left nothing")
	}
	for _, it := range mine {
		if it.Name != "Alpha Show" {
			t.Errorf("OnlyMine kept %s", it.Name)
		}
	}
	// A declined ask is not "mine".
	omar, _ := e.svc.Range(bg(), day("2026-10-01"), day("2026-11-30"), Viewer{UserID: 2, Role: "member"}, OnlyMine())
	if len(omar) != 0 {
		t.Errorf("Omar's declined ask put %d items in his calendar", len(omar))
	}
}
