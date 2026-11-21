package titles

import (
	"context"
	"database/sql"
	"errors"
	"path/filepath"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/BaileyPillon/marquee/internal/core/db"
	"github.com/BaileyPillon/marquee/internal/metadata"
)

func bg() context.Context { return context.Background() }

func contextCancelled() (context.Context, context.CancelFunc) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	return ctx, cancel
}

// stepClock moves forward a second every time it is read, so rows added one
// after another get distinct, ordered timestamps.
type stepClock struct {
	mu sync.Mutex
	t  time.Time
}

func (c *stepClock) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.t = c.t.Add(time.Second)
	return c.t
}

func newTestDB(t *testing.T) *sql.DB {
	t.Helper()
	d, err := db.Open(filepath.Join(t.TempDir(), "marquee.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { d.Close() })
	if _, err := db.Migrate(bg(), d, db.SetMain); err != nil {
		t.Fatal(err)
	}
	return d
}

// ---- fake providers --------------------------------------------------------

type fakeMovies struct {
	mu       sync.Mutex
	hits     map[string][]metadata.SearchHit // by query
	movies   map[string]*metadata.MovieInfo  // by id
	searches []searchCall
	gets     atomic.Int32
	err      error
}

type searchCall struct {
	q    string
	year int
}

func (f *fakeMovies) SearchMovies(_ context.Context, q string, year int) ([]metadata.SearchHit, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.searches = append(f.searches, searchCall{q, year})
	if f.err != nil {
		return nil, f.err
	}
	return f.hits[q], nil
}

func (f *fakeMovies) Movie(ctx context.Context, id string) (*metadata.MovieInfo, error) {
	f.gets.Add(1)
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.err != nil {
		return nil, f.err
	}
	if m, ok := f.movies[id]; ok {
		cp := *m
		return &cp, nil
	}
	return nil, metadata.ErrNotFound
}

type fakeShows struct {
	mu     sync.Mutex
	hits   map[string][]metadata.SearchHit
	shows  map[string]*metadata.ShowInfo
	byIMDb map[string]*metadata.SearchHit
	gets   atomic.Int32
	cur    atomic.Int32
	peak   atomic.Int32
	delay  time.Duration
	failID string
	err    error
}

func (f *fakeShows) SearchShows(_ context.Context, q string) ([]metadata.SearchHit, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.err != nil {
		return nil, f.err
	}
	return f.hits[q], nil
}

func (f *fakeShows) ShowByIMDb(_ context.Context, id string) (*metadata.SearchHit, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if h, ok := f.byIMDb[id]; ok {
		return h, nil
	}
	return nil, metadata.ErrNotFound
}

func (f *fakeShows) Show(ctx context.Context, id string) (*metadata.ShowInfo, error) {
	f.gets.Add(1)
	n := f.cur.Add(1)
	defer f.cur.Add(-1)
	for {
		p := f.peak.Load()
		if n <= p || f.peak.CompareAndSwap(p, n) {
			break
		}
	}
	if f.delay > 0 {
		select {
		case <-time.After(f.delay):
		case <-ctx.Done():
			return nil, ctx.Err()
		}
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.err != nil {
		return nil, f.err
	}
	if id == f.failID {
		return nil, &metadata.Error{Code: "upstream_error", Message: "TVmaze did not answer."}
	}
	if s, ok := f.shows[id]; ok {
		cp := *s
		return &cp, nil
	}
	return nil, metadata.ErrNotFound
}

type fakeArtists struct {
	mu      sync.Mutex
	hits    map[string][]metadata.SearchHit
	artists map[string]*metadata.ArtistInfo
	gets    atomic.Int32
}

func (f *fakeArtists) SearchArtists(_ context.Context, q string) ([]metadata.SearchHit, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.hits[q], nil
}

func (f *fakeArtists) Artist(_ context.Context, id string) (*metadata.ArtistInfo, error) {
	f.gets.Add(1)
	f.mu.Lock()
	defer f.mu.Unlock()
	if a, ok := f.artists[id]; ok {
		cp := *a
		return &cp, nil
	}
	return nil, metadata.ErrNotFound
}

// ---- sample titles ---------------------------------------------------------

func metropolis() *metadata.MovieInfo {
	return &metadata.MovieInfo{
		TMDBID: 19, IMDbID: "tt0017136", Title: "Metropolis", OriginalTitle: "Metropolis (Fritz Lang)",
		Year: 1927, ReleaseDate: "1927-01-10", Overview: "A city divided.", Tagline: "The heart mediates.",
		RuntimeMin: 153, Genres: []string{"Drama", "Science Fiction"}, Status: "released", Certification: "NR",
		TrailerKey: "trailerKEY1",
		Poster:     metadata.Image{Ref: "tmdb/w500/metropolis.jpg"}, Backdrop: metadata.Image{Ref: "tmdb/w1280/metropolis-bd.jpg"},
		People: []metadata.Person{
			{Name: "Brigitte Helm", Role: "actor", Job: "Maria", Image: metadata.Image{Ref: "tmdb/w185/helm.jpg"}},
			{Name: "Alfred Abel", Role: "actor", Job: "Joh Fredersen"},
			{Name: "Fritz Lang", Role: "director", Job: "Director"},
		},
	}
}

func sintel() *metadata.MovieInfo {
	return &metadata.MovieInfo{
		TMDBID: 45745, IMDbID: "tt1727587", Title: "Sintel", OriginalTitle: "Sintel", Year: 2010,
		Overview: "A girl searches for her dragon.", RuntimeMin: 15, Genres: []string{"Animation", "Fantasy"}, Status: "released",
		Poster: metadata.Image{Ref: "tmdb/w500/sintel.jpg"},
	}
}

func theAmericans() *metadata.ShowInfo {
	return &metadata.ShowInfo{TVmazeID: 7, IMDbID: "tt2149175", Name: "The Americans", Year: 2013, Status: "ended"}
}

func pioneerOne() *metadata.ShowInfo {
	return &metadata.ShowInfo{
		TVmazeID: 99001, IMDbID: "tt9900001", TheTVDBID: 99991, Name: "Pioneer One", Year: 2010, Premiered: "2010-06-19",
		Overview: "A team of agents investigates.", RuntimeMin: 47, Genres: []string{"Drama", "Science-Fiction"},
		Status: "ended", Network: "VODO", Language: "English",
		Poster: metadata.Image{Ref: "tvmaze/uploads/images/original_untouched/99/99001.jpg"},
		Seasons: []metadata.SeasonInfo{
			{Number: 0, EpisodeCount: 1}, {Number: 1, EpisodeCount: 2}, {Number: 2, EpisodeCount: 1},
		},
		Episodes: []metadata.EpisodeInfo{
			{Season: 0, Number: 1, Name: "Behind the Scenes", AirDate: "2010-07-10", RuntimeMin: 12, Special: true},
			{Season: 1, Number: 1, Name: "Earthfall", AirDate: "2010-06-19", Overview: "An object falls.", RuntimeMin: 45},
			{Season: 1, Number: 2, Name: "Anomalies", AirDate: "2010-07-03"},
			{Season: 2, Number: 1, Name: "Landing Zone", AirDate: "2011-01-30", RuntimeMin: 47},
		},
		People: []metadata.Person{{Name: "Joanna Gaskell", Role: "actor", Job: "Agent Taylor"}},
	}
}

const (
	chopinMBID   = "a1b2c3d4-0000-4000-8000-000000000001"
	nocturnesMB  = "b0000000-0000-4000-8000-000000000101"
	etudesMB     = "b0000000-0000-4000-8000-000000000102"
	otherArtist  = "a1b2c3d4-0000-4000-8000-000000000002"
	bachArtistID = "a1b2c3d4-0000-4000-8000-000000000003"
)

func chopin() *metadata.ArtistInfo {
	return &metadata.ArtistInfo{
		MBID: chopinMBID, Name: "Frédéric Chopin", SortName: "Chopin, Frédéric", Type: "Person", Country: "PL",
		Disambiguation: "Polish composer and pianist", Year: 1810, Ended: true,
		Genres: []string{"classical"}, AltNames: []string{"Fryderyk Chopin", "Chopin"},
		Albums: []metadata.AlbumInfo{
			{
				MBID: nocturnesMB, ReleaseID: "c0000000-0000-4000-8000-000000000203", Title: "Nocturnes", Year: 1998, Type: "album",
				Cover: metadata.Image{Ref: "caa/release-group/" + nocturnesMB + "/front-500"},
				Tracks: []metadata.TrackInfo{
					{Disc: 1, Position: 1, Title: "Nocturne in B-flat minor, Op. 9 No. 1", LengthMS: 330000, MBID: "e0000000-0000-4000-8000-000000000401"},
					{Disc: 1, Position: 2, Title: "Nocturne in E-flat major, Op. 9 No. 2", LengthMS: 262000, MBID: "e0000000-0000-4000-8000-000000000402"},
					{Disc: 2, Position: 1, Title: "Nocturne in B major, Op. 9 No. 3", LengthMS: 410000},
				},
			},
			{
				MBID: etudesMB, Title: "Études, Op. 10", Year: 1985, Type: "album",
				Cover: metadata.Image{Ref: "caa/release-group/" + etudesMB + "/front-500"},
				Tracks: []metadata.TrackInfo{
					{Disc: 1, Position: 1, Title: "Étude in C major, Op. 10 No. 1", LengthMS: 142000},
				},
			},
		},
	}
}

// svc is a catalog on a temp database with fake providers.
type fixture struct {
	t       *testing.T
	db      *sql.DB
	svc     *Service
	movies  *fakeMovies
	shows   *fakeShows
	artists *fakeArtists
	clock   *stepClock
}

func newFixture(t *testing.T) *fixture {
	t.Helper()
	f := &fixture{
		t:  t,
		db: newTestDB(t),
		movies: &fakeMovies{
			hits:   map[string][]metadata.SearchHit{},
			movies: map[string]*metadata.MovieInfo{"19": metropolis(), "45745": sintel()},
		},
		shows: &fakeShows{
			hits:   map[string][]metadata.SearchHit{},
			shows:  map[string]*metadata.ShowInfo{"99001": pioneerOne(), "7": theAmericans()},
			byIMDb: map[string]*metadata.SearchHit{},
		},
		artists: &fakeArtists{
			hits:    map[string][]metadata.SearchHit{},
			artists: map[string]*metadata.ArtistInfo{chopinMBID: chopin()},
		},
		clock: &stepClock{t: time.Date(2026, 11, 20, 12, 0, 0, 0, time.UTC)},
	}
	f.svc = NewService(f.db, Providers{Movies: f.movies, Shows: f.shows, Artists: f.artists}, Options{Now: f.clock.Now})
	return f
}

func (f *fixture) addMovie(id string, monitored bool) *Detail {
	f.t.Helper()
	d, _, err := f.svc.Add(bg(), KindMovie, metadata.ProviderTMDB, id, AddOptions{Monitored: monitored})
	if err != nil {
		f.t.Fatal(err)
	}
	return d
}

func (f *fixture) addShow(id string, monitored bool) *Detail {
	f.t.Helper()
	d, _, err := f.svc.Add(bg(), KindShow, metadata.ProviderTVmaze, id, AddOptions{Monitored: monitored})
	if err != nil {
		f.t.Fatal(err)
	}
	return d
}

func (f *fixture) addArtist(id string, monitored bool) *Detail {
	f.t.Helper()
	d, _, err := f.svc.Add(bg(), KindArtist, metadata.ProviderMusicBrainz, id, AddOptions{Monitored: monitored})
	if err != nil {
		f.t.Fatal(err)
	}
	return d
}

// count runs a COUNT(*) for a test's own checks.
func (f *fixture) count(query string, args ...any) int {
	f.t.Helper()
	var n int
	if err := f.db.QueryRow(query, args...).Scan(&n); err != nil {
		f.t.Fatal(err)
	}
	return n
}

func isInvalid(err error) bool {
	var ie *InvalidError
	return errors.As(err, &ie)
}
