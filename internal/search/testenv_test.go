package search

import (
	"bytes"
	"context"
	"database/sql"
	"errors"
	"fmt"
	"log/slog"
	"net/url"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/BaileyPillon/marquee/internal/core/db"
	"github.com/BaileyPillon/marquee/internal/core/outbound"
	"github.com/BaileyPillon/marquee/internal/core/secrets"
	"github.com/BaileyPillon/marquee/internal/decide"
	"github.com/BaileyPillon/marquee/internal/grab"
	"github.com/BaileyPillon/marquee/internal/sources"
	"github.com/BaileyPillon/marquee/internal/sources/sourcestest"
	"github.com/BaileyPillon/marquee/internal/titles"
)

func bg() context.Context { return context.Background() }

const testKey = "9f8e7d6c5b4a39281706f5e4d3c2b1a0"

const gbSize = int64(1 << 30)

// ---- fakes for the grab contract -------------------------------------------------

type fakeCopies struct {
	mu     sync.Mutex
	copies map[int64]grab.Copy
	// wantedErr makes Wanted fail.
	wantedErr error
}

func (f *fakeCopies) Copy(_ context.Context, id int64) (grab.Copy, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	c, ok := f.copies[id]
	if !ok {
		return grab.Copy{}, errors.New("no such copy")
	}
	return c, nil
}

func (f *fakeCopies) Wanted(context.Context) ([]grab.Copy, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.wantedErr != nil {
		return nil, f.wantedErr
	}
	var out []grab.Copy
	for id := int64(1); id < 100; id++ {
		if c, ok := f.copies[id]; ok && c.Monitored {
			out = append(out, c)
		}
	}
	return out, nil
}

// finderCopies adds the optional lookup by title and quality.
type finderCopies struct{ *fakeCopies }

func (f finderCopies) FindCopy(_ context.Context, titleID int64, quality string) (grab.Copy, bool, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	for _, c := range f.copies {
		if c.TitleID == titleID && c.Quality == quality {
			return c, true, nil
		}
	}
	return grab.Copy{}, false, nil
}

// scopedCopies restricts a show copy to some seasons.
type scopedCopies struct {
	*fakeCopies
	seasons map[int64][]int
}

func (s scopedCopies) CopySeasons(_ context.Context, c grab.Copy) ([]int, bool, error) {
	if seasons, ok := s.seasons[c.ID]; ok {
		return seasons, false, nil
	}
	return nil, true, nil
}

type grabCall struct {
	Target  grab.Target
	Release grab.Release
}

type fakeGrabber struct {
	mu    sync.Mutex
	calls []grabCall
	err   error
	next  int64
}

func (g *fakeGrabber) Grab(_ context.Context, t grab.Target, r grab.Release) (int64, error) {
	g.mu.Lock()
	defer g.mu.Unlock()
	if g.err != nil {
		return 0, g.err
	}
	g.calls = append(g.calls, grabCall{t, r})
	g.next++
	return 100 + g.next, nil
}

func (g *fakeGrabber) all() []grabCall {
	g.mu.Lock()
	defer g.mu.Unlock()
	return append([]grabCall(nil), g.calls...)
}

func (g *fakeGrabber) titlesGrabbed() []string {
	var out []string
	for _, c := range g.all() {
		out = append(out, c.Release.Title)
	}
	return out
}

type fakeShelf struct {
	mu   sync.Mutex
	have map[int64]Have
	err  error
}

func (f *fakeShelf) Have(_ context.Context, c grab.Copy) (Have, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.have[c.ID], f.err
}

// ---- the rig ---------------------------------------------------------------------

type syncBuffer struct {
	mu sync.Mutex
	b  bytes.Buffer
}

func (s *syncBuffer) Write(p []byte) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.b.Write(p)
}

func (s *syncBuffer) String() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.b.String()
}

type rig struct {
	t       *testing.T
	db      *sql.DB
	src     *sources.Service
	dec     *decide.Service
	cat     *titles.Service
	svc     *Service
	srv     *sourcestest.Server
	ix      sources.Indexer
	copies  *fakeCopies
	grabber *fakeGrabber
	shelf   *fakeShelf
	logs    *syncBuffer
	skew    time.Duration
}

func (r *rig) now() time.Time { return time.Now().Add(r.skew) }

func newRig(t *testing.T) *rig {
	t.Helper()
	d, err := db.Open(filepath.Join(t.TempDir(), "marquee.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { d.Close() })
	if _, err := db.Migrate(bg(), d, db.SetMain); err != nil {
		t.Fatal(err)
	}
	box, err := secrets.New([]byte(strings.Repeat("k", secrets.KeySize)))
	if err != nil {
		t.Fatal(err)
	}
	r := &rig{t: t, db: d, logs: &syncBuffer{}, copies: &fakeCopies{copies: map[int64]grab.Copy{}},
		grabber: &fakeGrabber{}, shelf: &fakeShelf{have: map[int64]Have{}}}
	log := slog.New(slog.NewTextHandler(r.logs, &slog.HandlerOptions{Level: slog.LevelDebug}))
	hc := outbound.New("test", outbound.Options{Logger: log, Backoff: func(int) time.Duration { return 0 }, HostRPS: 1000, HostBurst: 1000})
	r.src = sources.New(sources.NewStore(d), box, hc, sources.Options{Logger: log, Now: r.now})
	r.dec = decide.New(decide.NewStore(d), decide.Options{Now: r.now})
	r.cat = titles.NewService(d, titles.Providers{}, titles.Options{})
	r.svc = New(d, r.src, r.dec, r.cat, Options{Logger: log, Now: r.now, Pace: -1})
	r.svc.SetCopySource(r.copies)
	r.svc.SetGrabber(r.grabber)
	r.svc.SetShelf(r.shelf)
	r.src.SetMatcher(r.svc)

	r.srv = sourcestest.NewServer(t, testKey)
	r.ix, err = r.src.Create(bg(), sources.Input{Name: "Alpha", Kind: "torznab", URL: r.srv.Feed(), APIKey: testKey})
	if err != nil {
		t.Fatal(err)
	}
	return r
}

// serve sets what the fake source answers.
func (r *rig) serve(f func(q url.Values) []sourcestest.Item) { r.srv.Search = f }

// always answers every search with the same items.
func (r *rig) always(items ...sourcestest.Item) {
	r.serve(func(url.Values) []sourcestest.Item { return items })
}

func (r *rig) count(query string, args ...any) int {
	r.t.Helper()
	var n int
	if err := r.db.QueryRow(query, args...).Scan(&n); err != nil {
		r.t.Fatal(err)
	}
	return n
}

// ---- catalog rows ------------------------------------------------------------------

func nz(n int64) any {
	if n == 0 {
		return nil
	}
	return n
}

func nzs(s string) any {
	if s == "" {
		return nil
	}
	return s
}

func (r *rig) addMovie(name string, year int, imdb string, tmdb int64, runtime int) int64 {
	r.t.Helper()
	ts := db.TS(time.Now())
	res, err := r.db.Exec(`INSERT INTO titles (kind, tmdb_id, imdb_id, name, sort_name, year, runtime_min, added_at, updated_at, refreshed_at)
		VALUES ('movie', ?, ?, ?, ?, ?, ?, ?, ?, ?)`, nz(tmdb), nzs(imdb), name, name, year, runtime, ts, ts, ts)
	if err != nil {
		r.t.Fatal(err)
	}
	id, _ := res.LastInsertId()
	return id
}

type epRow struct {
	Season, Number int
	AirDate        string // default 2010-01-01; "none" for no date
	Unmonitored    bool
}

// addShow adds a show and returns its id and the episode ids by "S:E".
func (r *rig) addShow(name string, year int, tvmaze int64, imdb string, runtime int, eps ...epRow) (int64, map[string]int64) {
	r.t.Helper()
	ts := db.TS(time.Now())
	res, err := r.db.Exec(`INSERT INTO titles (kind, tvmaze_id, imdb_id, name, sort_name, year, runtime_min, added_at, updated_at, refreshed_at)
		VALUES ('show', ?, ?, ?, ?, ?, ?, ?, ?, ?)`, nz(tvmaze), nzs(imdb), name, name, year, runtime, ts, ts, ts)
	if err != nil {
		r.t.Fatal(err)
	}
	id, _ := res.LastInsertId()
	ids := map[string]int64{}
	for _, e := range eps {
		air := e.AirDate
		switch air {
		case "":
			air = "2010-01-01"
		case "none":
			air = ""
		}
		mon := 1
		if e.Unmonitored {
			mon = 0
		}
		res, err := r.db.Exec(`INSERT INTO episodes (title_id, season, number, air_date, monitored) VALUES (?, ?, ?, ?, ?)`,
			id, e.Season, e.Number, air, mon)
		if err != nil {
			r.t.Fatal(err)
		}
		eid, _ := res.LastInsertId()
		ids[fmt.Sprintf("%d:%d", e.Season, e.Number)] = eid
	}
	return id, ids
}

func (r *rig) addArtist(name string) int64 {
	r.t.Helper()
	ts := db.TS(time.Now())
	res, err := r.db.Exec(`INSERT INTO titles (kind, mbid, name, sort_name, added_at, updated_at, refreshed_at)
		VALUES ('artist', 'a1b2c3d4-0000-4000-8000-000000000001', ?, ?, ?, ?, ?)`, name, name, ts, ts, ts)
	if err != nil {
		r.t.Fatal(err)
	}
	id, _ := res.LastInsertId()
	return id
}

// addCopy registers a wanted copy.
func (r *rig) addCopy(id, titleID int64, kind, quality string) grab.Copy {
	c := grab.Copy{ID: id, TitleID: titleID, Kind: kind, Quality: quality, FolderID: 1, FolderPath: "/media", Monitored: true}
	r.copies.mu.Lock()
	r.copies.copies[id] = c
	r.copies.mu.Unlock()
	return c
}

func (r *rig) history(copyID int64) []History {
	r.t.Helper()
	h, err := r.svc.History(bg(), copyID, 20)
	if err != nil {
		r.t.Fatal(err)
	}
	return h
}

// ---- releases to answer with ----------------------------------------------------------

// tor is a torrent item with 20 seeders and a GUID of its own name.
func tor(name string, size int64) sourcestest.Item {
	it := sourcestest.Torrent(name, size, 20)
	it.Age = 3 * time.Hour
	return it
}

func torSeeded(name string, size int64, seeders int) sourcestest.Item {
	it := sourcestest.Torrent(name, size, seeders)
	it.Age = 3 * time.Hour
	return it
}

func noKey(t *testing.T, what, text string) {
	t.Helper()
	if strings.Contains(text, testKey) {
		t.Errorf("%s contains the API key: %.300q", what, text)
	}
}
