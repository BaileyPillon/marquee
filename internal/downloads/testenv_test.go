package downloads

import (
	"context"
	"database/sql"
	"fmt"
	"io"
	"log/slog"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/BaileyPillon/marquee/internal/core/db"
	"github.com/BaileyPillon/marquee/internal/core/events"
	"github.com/BaileyPillon/marquee/internal/core/outbound"
	"github.com/BaileyPillon/marquee/internal/core/secrets"
)

func testLogger() *slog.Logger { return slog.New(slog.NewTextHandler(io.Discard, nil)) }

// testHTTP is an outbound client that does not slow tests down or retry.
func testHTTP() *outbound.Client {
	return outbound.New("test", outbound.Options{
		Logger: testLogger(), HostRPS: 100000, HostBurst: 100000, MaxRetries: -1,
		DefaultTimeout: 10 * time.Second,
	})
}

// env is a downloads service on a temp database.
type env struct {
	t    *testing.T
	db   *sql.DB
	box  *secrets.Box
	hub  *events.Hub
	hc   *outbound.Client
	svc  *Service
	fake *fakeClients

	nowMu sync.Mutex
	now   time.Time
}

func newEnv(t *testing.T) *env {
	t.Helper()
	dir := t.TempDir()
	d, err := db.Open(filepath.Join(dir, db.MainFile))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { d.Close() })
	if _, err := db.Migrate(context.Background(), d, db.SetMain); err != nil {
		t.Fatal(err)
	}
	box, err := secrets.LoadOrCreate(filepath.Join(dir, "marquee.key"), testLogger())
	if err != nil {
		t.Fatal(err)
	}
	hub := events.NewHub(testLogger())
	t.Cleanup(hub.Close)
	e := &env{
		t: t, db: d, box: box, hub: hub, hc: testHTTP(), fake: &fakeClients{byURL: map[string]*fakeAdapter{}},
		now: time.Date(2026, 11, 20, 12, 0, 0, 0, time.UTC),
	}
	e.svc = NewService(d, Options{
		Logger: testLogger(), Hub: hub, HTTP: e.hc, Box: box,
		Adapters: e.fake.factory(),
		Now:      e.clock,
	})
	return e
}

func (e *env) clock() time.Time {
	e.nowMu.Lock()
	defer e.nowMu.Unlock()
	return e.now
}

func (e *env) advance(d time.Duration) {
	e.nowMu.Lock()
	e.now = e.now.Add(d)
	e.nowMu.Unlock()
}

// realAdapters makes the service build the real adapters (to talk to httptest fakes).
func (e *env) realAdapters() {
	e.svc.factory = NewAdapter
}

func str(s string) *string { return &s }
func boolp(b bool) *bool   { return &b }
func intp(i int) *int      { return &i }

// addClient adds a client of a kind at url. Options: priority, category, savePath.
func (e *env) addClient(name, kind, url string, mod ...func(*ClientInput)) ClientView {
	e.t.Helper()
	in := ClientInput{Name: str(name), Kind: str(kind), URL: str(url)}
	if kind == KindSABnzbd {
		in.APIKey = str("sab-api-key-0123456789abcdef")
	}
	for _, m := range mod {
		m(&in)
	}
	c, err := e.svc.AddClient(context.Background(), in)
	if err != nil {
		e.t.Fatalf("AddClient(%s): %v", name, err)
	}
	return c
}

// addTitle inserts a title row (the titles module owns the table; downloads
// only reads it).
func (e *env) addTitle(name string, year int) int64 {
	e.t.Helper()
	ts := db.TS(time.Now())
	res, err := e.db.Exec(`INSERT INTO titles (kind, name, sort_name, year, poster_ref, added_at, updated_at, refreshed_at)
		VALUES ('movie', ?, ?, ?, 'tmdb/w500/poster.jpg', ?, ?, ?)`, name, strings.ToLower(name), year, ts, ts, ts)
	if err != nil {
		e.t.Fatal(err)
	}
	id, _ := res.LastInsertId()
	return id
}

// forceState sets a download's state directly (the importer's SetState only
// allows the states it owns).
func (e *env) forceState(id int64, state string) {
	e.t.Helper()
	if _, err := e.db.Exec(`UPDATE downloads SET state = ? WHERE id = ?`, state, id); err != nil {
		e.t.Fatal(err)
	}
}

func (e *env) download(id int64) Download {
	e.t.Helper()
	d, err := e.svc.Download(context.Background(), id)
	if err != nil {
		e.t.Fatalf("Download(%d): %v", id, err)
	}
	return d
}

// ---- fake adapters -----------------------------------------------------------

// fakeClients hands out fakeAdapters by client address.
type fakeClients struct {
	mu    sync.Mutex
	byURL map[string]*fakeAdapter
}

func (f *fakeClients) factory() AdapterFactory {
	return func(cfg AdapterConfig) (Adapter, error) {
		f.mu.Lock()
		defer f.mu.Unlock()
		a := f.byURL[cfg.URL]
		if a == nil {
			a = &fakeAdapter{url: cfg.URL}
			f.byURL[cfg.URL] = a
		}
		a.cfg = cfg
		return a, nil
	}
}

// at returns (creating if needed) the fake behind an address.
func (f *fakeClients) at(url string) *fakeAdapter {
	f.mu.Lock()
	defer f.mu.Unlock()
	a := f.byURL[url]
	if a == nil {
		a = &fakeAdapter{url: url}
		f.byURL[url] = a
	}
	return a
}

// fakeAdapter is a download client that does what the test tells it to.
type fakeAdapter struct {
	url string
	cfg AdapterConfig

	mu       sync.Mutex
	addErr   error
	listErr  error
	fixedRef string // when set, every Add returns this ref
	actErr   error  // returned by Pause, Resume and Remove
	items    []Item
	added    []AddInput
	removed  []string
	deleted  []bool
	paused   []string
	resumed  []string
	nextRef  int
	listCats []string
}

var _ Adapter = (*fakeAdapter)(nil)

func (f *fakeAdapter) Test(context.Context) (string, error) { return "fake 1.0", f.listErr }

func (f *fakeAdapter) Add(_ context.Context, in AddInput) (string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.addErr != nil {
		return "", f.addErr
	}
	f.added = append(f.added, in)
	f.nextRef++
	if f.fixedRef != "" {
		return f.fixedRef, nil
	}
	return fmt.Sprintf("%s#%d", f.url, f.nextRef), nil
}

func (f *fakeAdapter) List(_ context.Context, category string) ([]Item, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.listCats = append(f.listCats, category)
	if f.listErr != nil {
		return nil, f.listErr
	}
	return append([]Item(nil), f.items...), nil
}

func (f *fakeAdapter) Remove(_ context.Context, ref string, deleteData bool) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.removed = append(f.removed, ref)
	f.deleted = append(f.deleted, deleteData)
	return f.actErr
}

func (f *fakeAdapter) Pause(_ context.Context, ref string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.paused = append(f.paused, ref)
	return f.actErr
}

func (f *fakeAdapter) Resume(_ context.Context, ref string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.resumed = append(f.resumed, ref)
	return f.actErr
}

// setItems replaces what the client lists.
func (f *fakeAdapter) setItems(items ...Item) {
	f.mu.Lock()
	f.items = items
	f.mu.Unlock()
}

func (f *fakeAdapter) setListErr(err error) {
	f.mu.Lock()
	f.listErr = err
	f.mu.Unlock()
}

func (f *fakeAdapter) addedCount() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.added)
}

// ---- tiny torrent files --------------------------------------------------------

func bstr(s string) string { return strconv.Itoa(len(s)) + ":" + s }

// tinyInfo builds a bencoded info dictionary (keys in sorted order, as the
// format requires) with binary "pieces" bytes.
func tinyInfo(name string, length int) string {
	pieces := "\x00\xff\x10\x80ABCDEFGHIJKLMNOPQ" // 20 bytes, not valid UTF-8
	return "d6:lengthi" + strconv.Itoa(length) + "e4:name" + bstr(name) + "12:piece lengthi16384e6:pieces" + bstr(pieces) + "e"
}

// tinyTorrent wraps an info dictionary in a torrent file. The keys around
// "info" prove the hash covers only the info bytes.
func tinyTorrent(info string) []byte {
	return []byte("d8:announce" + bstr("http://tracker.invalid/announce") + "7:comment" + bstr("test") +
		"4:info" + info + "8:url-listl" + bstr("http://web.invalid/x") + "ee")
}
