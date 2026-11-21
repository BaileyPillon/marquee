package requests

import (
	"context"
	"database/sql"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"strconv"
	"sync"
	"testing"
	"time"

	"github.com/BaileyPillon/marquee/internal/accounts"
	"github.com/BaileyPillon/marquee/internal/core/db"
	"github.com/BaileyPillon/marquee/internal/core/events"
	"github.com/BaileyPillon/marquee/internal/core/secrets"
	"github.com/BaileyPillon/marquee/internal/library"
	"github.com/BaileyPillon/marquee/internal/metadata"
	"github.com/BaileyPillon/marquee/internal/settings"
	"github.com/BaileyPillon/marquee/internal/titles"
)

func bg() context.Context { return context.Background() }

func quietLog() *slog.Logger { return slog.New(slog.NewTextHandler(io.Discard, nil)) }

// ---- clock -----------------------------------------------------------------

type testClock struct {
	mu sync.Mutex
	t  time.Time
}

func (c *testClock) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.t
}

func (c *testClock) Advance(d time.Duration) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.t = c.t.Add(d)
}

var t0 = time.Date(2026, 11, 20, 12, 0, 0, 0, time.UTC)

// ---- fake metadata providers -----------------------------------------------

// Movie ids 19 and 20 are real-looking films (so files can be matched to them);
// every other number is "Movie N". 404 does not exist.
type fakeMovies struct{}

var namedMovies = map[int64]struct {
	name string
	year int
}{19: {"Metropolis", 1927}, 20: {"Nosferatu", 1922}}

func (fakeMovies) SearchMovies(context.Context, string, int) ([]metadata.SearchHit, error) {
	return nil, nil
}

func (fakeMovies) Movie(_ context.Context, id string) (*metadata.MovieInfo, error) {
	n, err := strconv.ParseInt(id, 10, 64)
	if err != nil || n == 404 {
		return nil, metadata.ErrNotFound
	}
	m := &metadata.MovieInfo{TMDBID: n, Title: "Movie " + id, Year: 1990 + int(n%30),
		Poster: metadata.Image{Ref: "tmdb/w500/m" + id + ".jpg"}}
	if nm, ok := namedMovies[n]; ok {
		m.Title, m.Year = nm.name, nm.year
	}
	return m, nil
}

// Show 99001 is Pioneer One: a special, two episodes in season 1, one in season
// 2 and one in season 2 that has not aired yet. Any other number is "Show N".
type fakeShows struct{}

func (fakeShows) SearchShows(context.Context, string) ([]metadata.SearchHit, error) { return nil, nil }

func (fakeShows) ShowByIMDb(context.Context, string) (*metadata.SearchHit, error) {
	return nil, metadata.ErrNotFound
}

func (fakeShows) Show(_ context.Context, id string) (*metadata.ShowInfo, error) {
	n, err := strconv.ParseInt(id, 10, 64)
	if err != nil || n == 404 {
		return nil, metadata.ErrNotFound
	}
	if n == 99001 {
		return &metadata.ShowInfo{
			TVmazeID: 99001, Name: "Pioneer One", Year: 2010, Status: "ended",
			Poster:  metadata.Image{Ref: "tvmaze/uploads/images/original_untouched/99/99001.jpg"},
			Seasons: []metadata.SeasonInfo{{Number: 0, EpisodeCount: 1}, {Number: 1, EpisodeCount: 2}, {Number: 2, EpisodeCount: 2}},
			Episodes: []metadata.EpisodeInfo{
				{Season: 0, Number: 1, Name: "Behind the Scenes", AirDate: "2010-07-10", Special: true},
				{Season: 1, Number: 1, Name: "Earthfall", AirDate: "2010-06-19"},
				{Season: 1, Number: 2, Name: "Anomalies", AirDate: "2010-07-03"},
				{Season: 2, Number: 1, Name: "Landing Zone", AirDate: "2011-01-30"},
				{Season: 2, Number: 2, Name: "Not Yet", AirDate: "2099-01-01"},
			},
		}, nil
	}
	return &metadata.ShowInfo{
		TVmazeID: n, Name: "Show " + id, Year: 2000, Status: "ended",
		Seasons:  []metadata.SeasonInfo{{Number: 1, EpisodeCount: 1}},
		Episodes: []metadata.EpisodeInfo{{Season: 1, Number: 1, Name: "Pilot", AirDate: "2001-01-01"}},
	}, nil
}

const (
	chopinMBID   = "a1b2c3d4-0000-4000-8000-000000000001"
	nocturnesMB  = "b0000000-0000-4000-8000-000000000101"
	etudesMB     = "b0000000-0000-4000-8000-000000000102"
	otherArtist  = "a1b2c3d4-0000-4000-8000-000000000002"
	notAnArtist1 = "a1b2c3d4-0000-4000-8000-0000000000ff"
)

type fakeArtists struct{}

func (fakeArtists) SearchArtists(context.Context, string) ([]metadata.SearchHit, error) {
	return nil, nil
}

func (fakeArtists) Artist(_ context.Context, id string) (*metadata.ArtistInfo, error) {
	switch id {
	case chopinMBID:
		return &metadata.ArtistInfo{
			MBID: chopinMBID, Name: "Frédéric Chopin", SortName: "Chopin, Frédéric", Year: 1810, Ended: true,
			Albums: []metadata.AlbumInfo{
				{MBID: nocturnesMB, Title: "Nocturnes", Year: 1998, Type: "album",
					Tracks: []metadata.TrackInfo{{Disc: 1, Position: 1, Title: "Nocturne 1"}}},
				{MBID: etudesMB, Title: "Études", Year: 1985, Type: "album"},
			},
		}, nil
	case otherArtist:
		return &metadata.ArtistInfo{MBID: otherArtist, Name: "Other Artist", Albums: []metadata.AlbumInfo{
			{MBID: "b0000000-0000-4000-8000-000000000201", Title: "One", Year: 2000, Type: "album"}}}, nil
	}
	return nil, metadata.ErrNotFound
}

// ---- fake searcher ---------------------------------------------------------

type fakeSearcher struct {
	mu    sync.Mutex
	calls []int64
	got   chan int64
	err   error
}

func newFakeSearcher() *fakeSearcher { return &fakeSearcher{got: make(chan int64, 16)} }

func (f *fakeSearcher) SearchCopy(_ context.Context, copyID int64) error {
	f.mu.Lock()
	f.calls = append(f.calls, copyID)
	f.mu.Unlock()
	f.got <- copyID
	return f.err
}

// wait returns the next copy id the searcher was asked about, or fails.
func (f *fakeSearcher) wait(t *testing.T) int64 {
	t.Helper()
	select {
	case id := <-f.got:
		return id
	case <-time.After(3 * time.Second):
		t.Fatal("the searcher was not called")
		return 0
	}
}

func (f *fakeSearcher) count() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.calls)
}

// ---- the whole rig ---------------------------------------------------------

type env struct {
	t     *testing.T
	db    *sql.DB
	clock *testClock
	acc   *accounts.Service
	cat   *titles.Service
	lib   *library.Service
	cfg   *settings.Service
	hub   *events.Hub
	svc   *Service

	owner, admin, mia, noah accounts.User
}

var lightParams = &accounts.PasswordParams{Time: 1, Memory: 64, Threads: 1, KeyLen: 32, SaltLen: 16}

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
	log := quietLog()
	box, err := secrets.LoadOrCreate(filepath.Join(t.TempDir(), "marquee.key"), log)
	if err != nil {
		t.Fatal(err)
	}
	e := &env{t: t, db: d, clock: &testClock{t: t0}}
	e.cfg = settings.New(d, box)
	e.hub = events.NewHub(log)
	t.Cleanup(e.hub.Close)
	e.acc = accounts.NewService(d, accounts.Options{Logger: log, PasswordParams: lightParams})
	e.cat = titles.NewService(d, titles.Providers{Movies: fakeMovies{}, Shows: fakeShows{}, Artists: fakeArtists{}},
		titles.Options{Logger: log})
	e.lib = library.NewService(d, library.Options{Logger: log, Hub: e.hub})
	t.Cleanup(e.lib.Close)
	e.lib.SetTitleSource(catalogSource{e.cat})
	e.svc = NewService(d, e.cat, e.lib, e.cfg, Options{Logger: log, Hub: e.hub, Now: e.clock.Now})
	t.Cleanup(e.svc.Close)

	login, err := e.acc.SetupOwner(bg(), "Bailey", "correct horse", accounts.ClientInfo{})
	if err != nil {
		t.Fatal(err)
	}
	e.owner = login.User
	e.admin = e.invite(accounts.RoleAdmin, "Alice")
	e.mia = e.invite(accounts.RoleMember, "Mia")
	e.noah = e.invite(accounts.RoleMember, "Noah")
	return e
}

func (e *env) invite(role accounts.Role, name string) accounts.User {
	e.t.Helper()
	token, _, err := e.acc.CreateInvite(bg(), e.owner, role)
	if err != nil {
		e.t.Fatal(err)
	}
	login, err := e.acc.AcceptInvite(bg(), token, name, "member password", accounts.ClientInfo{})
	if err != nil {
		e.t.Fatal(err)
	}
	return login.User
}

// catalogSource lets the library match files to the catalog's titles.
type catalogSource struct{ cat *titles.Service }

func (c catalogSource) Candidates(ctx context.Context, kind string) ([]library.Candidate, error) {
	list, err := c.cat.Candidates(ctx, kind)
	if err != nil {
		return nil, err
	}
	out := make([]library.Candidate, len(list))
	for i, x := range list {
		out[i] = library.Candidate{ID: x.ID, Kind: x.Kind, Name: x.Name, Year: x.Year, AltNames: x.AltNames}
	}
	return out, nil
}

func (c catalogSource) EpisodeID(ctx context.Context, titleID int64, season, number int) (int64, bool, error) {
	return c.cat.EpisodeID(ctx, titleID, season, number)
}

func actorOf(u accounts.User) Actor { return Actor{UserID: u.ID, Role: string(u.Role)} }

// addFolder makes a real folder named dirName and puts it in the library.
func (e *env) addFolder(kind, label, dirName string, files ...string) library.Folder {
	e.t.Helper()
	root := filepath.Join(e.t.TempDir(), dirName)
	if err := os.MkdirAll(root, 0o755); err != nil {
		e.t.Fatal(err)
	}
	for _, rel := range files {
		p := filepath.Join(root, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			e.t.Fatal(err)
		}
		if err := os.WriteFile(p, nil, 0o644); err != nil {
			e.t.Fatal(err)
		}
	}
	f, err := e.lib.AddFolder(bg(), kind, root, label)
	if err != nil {
		e.t.Fatal(err)
	}
	return f
}

// standardFolders adds one folder of each kind, plus a 4K movie folder.
func (e *env) standardFolders() (movies, movies4k, shows, music library.Folder) {
	e.t.Helper()
	movies = e.addFolder("movie", "Movies", "movies")
	movies4k = e.addFolder("movie", "Movies 4K", "movies-4k")
	shows = e.addFolder("show", "Shows", "shows")
	music = e.addFolder("music", "Music", "music")
	return
}

func (e *env) ask(u accounts.User, in AskInput) (*AskResult, error) {
	e.t.Helper()
	return e.svc.Ask(bg(), actorOf(u), in)
}

func (e *env) mustAsk(u accounts.User, in AskInput) *AskResult {
	e.t.Helper()
	r, err := e.ask(u, in)
	if err != nil {
		e.t.Fatalf("Ask: %v", err)
	}
	return r
}

// movie names a movie by its TMDB id.
func movie(id int64, quality string) AskInput {
	return AskInput{Kind: KindMovie, Provider: metadata.ProviderTMDB, ProviderID: strconv.FormatInt(id, 10), Quality: quality}
}

func show(id int64, quality string, scope Scope) AskInput {
	return AskInput{Kind: KindShow, Provider: metadata.ProviderTVmaze, ProviderID: strconv.FormatInt(id, 10), Quality: quality, Scope: scope}
}

func (e *env) count(query string, args ...any) int {
	e.t.Helper()
	var n int
	if err := e.db.QueryRow(query, args...).Scan(&n); err != nil {
		e.t.Fatal(err)
	}
	return n
}

// setOverride changes one person's overrides, as the owner.
func (e *env) setLimits(u accounts.User, p LimitsPatch) {
	e.t.Helper()
	if _, err := e.svc.SetLimits(bg(), actorOf(e.owner), u.ID, p); err != nil {
		e.t.Fatal(err)
	}
}

func intP(n int) Patch[int]    { return Patch[int]{Set: true, Value: &n} }
func boolP(b bool) Patch[bool] { return Patch[bool]{Set: true, Value: &b} }
func clearInt() Patch[int]     { return Patch[int]{Set: true} }
func clearBool() Patch[bool]   { return Patch[bool]{Set: true} }
func isInvalid(err error) bool { _, ok := err.(*InvalidError); return ok }

func itoa(n int64) string { return strconv.FormatInt(n, 10) }

// libraryFolder keeps the folder tables in the tests short.
type libraryFolder = library.Folder
