package importer

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/BaileyPillon/marquee/internal/accounts"
	"github.com/BaileyPillon/marquee/internal/core/db"
	"github.com/BaileyPillon/marquee/internal/core/events"
	"github.com/BaileyPillon/marquee/internal/core/outbound"
	"github.com/BaileyPillon/marquee/internal/core/pathmap"
	"github.com/BaileyPillon/marquee/internal/core/secrets"
	"github.com/BaileyPillon/marquee/internal/decide"
	"github.com/BaileyPillon/marquee/internal/downloads"
	"github.com/BaileyPillon/marquee/internal/grab"
	"github.com/BaileyPillon/marquee/internal/history"
	"github.com/BaileyPillon/marquee/internal/library"
	"github.com/BaileyPillon/marquee/internal/mediaservers"
	"github.com/BaileyPillon/marquee/internal/metadata"
	"github.com/BaileyPillon/marquee/internal/requests"
	"github.com/BaileyPillon/marquee/internal/settings"
	"github.com/BaileyPillon/marquee/internal/titles"
)

func bg() context.Context { return context.Background() }

func quietLog() *slog.Logger { return slog.New(slog.NewTextHandler(io.Discard, nil)) }

// ---- fake metadata -------------------------------------------------------------

const (
	notldTMDB  = "10331" // Night of the Living Dead (1968)
	matrixTMDB = "603"   // The Matrix (1999)
	pioneerTVM = "99001" // Pioneer One (2010): S00E01, S01E01-E02, S02E01
	chopinMBID = "a1b2c3d4-0000-4000-8000-000000000001"
	nocturnes  = "b0000000-0000-4000-8000-000000000101"
)

type fakeMovies struct{}

func (fakeMovies) SearchMovies(context.Context, string, int) ([]metadata.SearchHit, error) {
	return nil, nil
}

func (fakeMovies) Movie(_ context.Context, id string) (*metadata.MovieInfo, error) {
	switch id {
	case notldTMDB:
		return &metadata.MovieInfo{TMDBID: 10331, IMDbID: "tt0063350", Title: "Night of the Living Dead", Year: 1968,
			Poster: metadata.Image{Ref: "tmdb/w500/notld.jpg"}}, nil
	case matrixTMDB:
		return &metadata.MovieInfo{TMDBID: 603, IMDbID: "tt0133093", Title: "The Matrix", Year: 1999,
			Poster: metadata.Image{Ref: "tmdb/w500/matrix.jpg"}}, nil
	}
	return nil, metadata.ErrNotFound
}

type fakeShows struct{}

func (fakeShows) SearchShows(context.Context, string) ([]metadata.SearchHit, error) { return nil, nil }

func (fakeShows) ShowByIMDb(context.Context, string) (*metadata.SearchHit, error) {
	return nil, metadata.ErrNotFound
}

func (fakeShows) Show(_ context.Context, id string) (*metadata.ShowInfo, error) {
	if id != pioneerTVM {
		return nil, metadata.ErrNotFound
	}
	return &metadata.ShowInfo{
		TVmazeID: 99001, Name: "Pioneer One", Year: 2010, Status: "ended",
		Poster:  metadata.Image{Ref: "tvmaze/uploads/images/original_untouched/99/99001.jpg"},
		Seasons: []metadata.SeasonInfo{{Number: 0, EpisodeCount: 1}, {Number: 1, EpisodeCount: 2}, {Number: 2, EpisodeCount: 1}},
		Episodes: []metadata.EpisodeInfo{
			{Season: 0, Number: 1, Name: "Behind the Scenes", AirDate: "2010-07-10", Special: true},
			{Season: 1, Number: 1, Name: "Earthfall", AirDate: "2010-06-19"},
			{Season: 1, Number: 2, Name: "Anomalies", AirDate: "2010-07-03"},
			{Season: 2, Number: 1, Name: "Landing Zone", AirDate: "2011-01-30"},
		},
	}, nil
}

type fakeArtists struct{}

func (fakeArtists) SearchArtists(context.Context, string) ([]metadata.SearchHit, error) {
	return nil, nil
}

func (fakeArtists) Artist(_ context.Context, id string) (*metadata.ArtistInfo, error) {
	if id != chopinMBID {
		return nil, metadata.ErrNotFound
	}
	return &metadata.ArtistInfo{MBID: chopinMBID, Name: "Frédéric Chopin", SortName: "Chopin, Frédéric", Year: 1810, Ended: true,
		Albums: []metadata.AlbumInfo{{MBID: nocturnes, Title: "Nocturnes", Year: 1998, Type: "album",
			Tracks: []metadata.TrackInfo{{Disc: 1, Position: 1, Title: "Nocturne in B-flat minor, Op. 9 No. 1"}, {Disc: 1, Position: 2, Title: "Nocturne in E-flat major, Op. 9 No. 2"}}}}}, nil
}

// ---- fake download clients ------------------------------------------------------

type removal struct {
	ref        string
	deleteData bool
}

// fakeClient is a download client that reports whatever a test tells it to.
type fakeClient struct {
	mu      sync.Mutex
	items   map[string]*downloads.Item
	removed []removal
	n       int
}

var _ downloads.Adapter = (*fakeClient)(nil)

func (f *fakeClient) Test(context.Context) (string, error) { return "fake", nil }

func (f *fakeClient) Add(_ context.Context, in downloads.AddInput) (string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.n++
	ref := fmt.Sprintf("nzo_%d", f.n)
	if in.MagnetURL != "" {
		h, err := downloads.MagnetInfoHash(in.MagnetURL)
		if err != nil {
			return "", err
		}
		ref = h
	}
	f.items[ref] = &downloads.Item{Ref: ref, Name: in.Name, State: downloads.StateDownloading, ETA: -1}
	return ref, nil
}

func (f *fakeClient) List(context.Context, string) ([]downloads.Item, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := make([]downloads.Item, 0, len(f.items))
	for _, it := range f.items {
		out = append(out, *it)
	}
	return out, nil
}

func (f *fakeClient) Remove(_ context.Context, ref string, deleteData bool) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.removed = append(f.removed, removal{ref, deleteData})
	delete(f.items, ref)
	return nil
}

func (f *fakeClient) Pause(context.Context, string) error  { return nil }
func (f *fakeClient) Resume(context.Context, string) error { return nil }

// finish makes the client report a download as done, with its files at content.
func (f *fakeClient) finish(ref, content, save string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	it := f.items[ref]
	it.Finished, it.Progress, it.ContentPath, it.SavePath = true, 1, content, save
}

func (f *fakeClient) removals() []removal {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]removal(nil), f.removed...)
}

// ---- fake search ------------------------------------------------------------------

type forget struct {
	copyID int64
	keys   []string
}

type fakeSearch struct {
	mu        sync.Mutex
	searched  chan int64
	forgotten []forget
}

func newFakeSearch() *fakeSearch { return &fakeSearch{searched: make(chan int64, 16)} }

func (f *fakeSearch) SearchCopy(_ context.Context, copyID int64) error {
	f.searched <- copyID
	return nil
}

func (f *fakeSearch) ForgetGrab(_ context.Context, copyID int64, keys []string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.forgotten = append(f.forgotten, forget{copyID, keys})
	return nil
}

func (f *fakeSearch) forgets() []forget {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]forget(nil), f.forgotten...)
}

// waitSearch returns the next copy the importer searched again, or fails.
func (f *fakeSearch) waitSearch(t *testing.T) int64 {
	t.Helper()
	select {
	case id := <-f.searched:
		return id
	case <-time.After(3 * time.Second):
		t.Fatal("the copy was not searched again")
		return 0
	}
}

// noSearch fails the test if the importer searches again.
func (f *fakeSearch) noSearch(t *testing.T) {
	t.Helper()
	select {
	case id := <-f.searched:
		t.Fatalf("the importer searched copy %d again, but should not have", id)
	case <-time.After(300 * time.Millisecond):
	}
}

// ---- fake Jellyfin ----------------------------------------------------------------

type jfCall struct {
	method, path, token string
	body                string
}

type fakeJellyfin struct {
	srv   *httptest.Server
	mu    sync.Mutex
	calls []jfCall
	// updatedStatus is what POST /Library/Media/Updated answers (default 204).
	updatedStatus int
}

func newFakeJellyfin(t *testing.T) *fakeJellyfin {
	f := &fakeJellyfin{updatedStatus: http.StatusNoContent}
	f.srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		f.mu.Lock()
		f.calls = append(f.calls, jfCall{method: r.Method, path: r.URL.Path, token: r.Header.Get("X-Emby-Token"), body: string(body)})
		status := f.updatedStatus
		f.mu.Unlock()
		switch r.URL.Path {
		case "/Library/Media/Updated":
			w.WriteHeader(status)
		case "/Library/Refresh":
			w.WriteHeader(http.StatusNoContent)
		case "/System/Info":
			_, _ = io.WriteString(w, `{"Version":"12.4.0","ServerName":"Fake","Id":"srv1"}`)
		case "/Library/VirtualFolders":
			_, _ = io.WriteString(w, `[{"Name":"Movies","ItemId":"lib1","Locations":["/media/movies"]},{"Name":"TV","ItemId":"lib2","Locations":["/media/tv"]}]`)
		case "/Items/lib1/Refresh", "/Items/lib2/Refresh":
			w.WriteHeader(http.StatusNoContent)
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(f.srv.Close)
	return f
}

func (f *fakeJellyfin) all() []jfCall {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]jfCall(nil), f.calls...)
}

// updates returns the decoded Updates of every POST /Library/Media/Updated.
func (f *fakeJellyfin) updates(t *testing.T) []map[string]string {
	t.Helper()
	var out []map[string]string
	for _, c := range f.all() {
		if c.path != "/Library/Media/Updated" {
			continue
		}
		var body struct {
			Updates []map[string]string `json:"Updates"`
		}
		if err := json.Unmarshal([]byte(c.body), &body); err != nil {
			t.Fatalf("bad Media/Updated body %q: %v", c.body, err)
		}
		out = append(out, body.Updates...)
	}
	return out
}

// ---- the rig ------------------------------------------------------------------------

type rig struct {
	t     *testing.T
	db    *sql.DB
	hub   *events.Hub
	owner accounts.User
	cat   *titles.Service
	lib   *library.Service
	reqs  *requests.Service
	dec   *decide.Service
	dl    *downloads.Service
	hist  *history.Service
	srvs  *mediaservers.Service
	jelly *fakeJellyfin
	srch  *fakeSearch
	imp   *Service

	qbt, sab         *fakeClient
	qbtView, sabView downloads.ClientView

	dlDir, moviesDir, showsDir, musicDir string
	moviesFolder, showsFolder            library.Folder

	clockMu sync.Mutex
	clockAt time.Time
}

func (r *rig) now() time.Time {
	r.clockMu.Lock()
	defer r.clockMu.Unlock()
	return r.clockAt
}

func (r *rig) advance(d time.Duration) {
	r.clockMu.Lock()
	r.clockAt = r.clockAt.Add(d)
	r.clockMu.Unlock()
}

var lightParams = &accounts.PasswordParams{Time: 1, Memory: 64, Threads: 1, KeyLen: 32, SaltLen: 16}

func newRig(t *testing.T) *rig {
	t.Helper()
	root := t.TempDir()
	d, err := db.Open(filepath.Join(root, db.MainFile))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { d.Close() })
	if _, err := db.Migrate(bg(), d, db.SetMain); err != nil {
		t.Fatal(err)
	}
	log := quietLog()
	box, err := secrets.LoadOrCreate(filepath.Join(root, "marquee.key"), log)
	if err != nil {
		t.Fatal(err)
	}
	r := &rig{t: t, db: d, clockAt: time.Date(2026, 11, 21, 12, 0, 0, 0, time.UTC),
		qbt: &fakeClient{items: map[string]*downloads.Item{}}, sab: &fakeClient{items: map[string]*downloads.Item{}}}
	r.hub = events.NewHub(log)
	t.Cleanup(r.hub.Close)

	acc := accounts.NewService(d, accounts.Options{Logger: log, PasswordParams: lightParams})
	login, err := acc.SetupOwner(bg(), "Bailey", "correct horse", accounts.ClientInfo{})
	if err != nil {
		t.Fatal(err)
	}
	r.owner = login.User

	cfg := settings.New(d, box)
	r.cat = titles.NewService(d, titles.Providers{Movies: fakeMovies{}, Shows: fakeShows{}, Artists: fakeArtists{}}, titles.Options{Logger: log})
	r.lib = library.NewService(d, library.Options{Logger: log, Hub: r.hub})
	t.Cleanup(r.lib.Close)
	r.lib.SetTitleSource(catalogSource{r.cat})
	r.reqs = requests.NewService(d, r.cat, r.lib, cfg, requests.Options{Logger: log, Hub: r.hub, Now: r.now})
	t.Cleanup(r.reqs.Close)
	r.dec = decide.New(decide.NewStore(d), decide.Options{})
	r.hist = history.New(d, history.Options{Logger: log})
	r.reqs.SetRecorder(r.hist)

	hc := outbound.New("test", outbound.Options{Logger: log, HostRPS: 100000, HostBurst: 100000, MaxRetries: -1})
	r.dl = downloads.NewService(d, downloads.Options{
		Logger: log, Hub: r.hub, HTTP: hc, Box: box, Now: r.now,
		Adapters: func(cfg downloads.AdapterConfig) (downloads.Adapter, error) {
			if strings.Contains(cfg.URL, "sab.test") {
				return r.sab, nil
			}
			return r.qbt, nil
		},
	})
	r.srvs = mediaservers.New(d, mediaservers.Options{Logger: log, HTTP: hc, Box: box})
	r.jelly = newFakeJellyfin(t)
	r.srch = newFakeSearch()

	r.dlDir = filepath.Join(root, "downloads")
	r.moviesDir = filepath.Join(root, "library", "movies")
	r.showsDir = filepath.Join(root, "library", "tv")
	r.musicDir = filepath.Join(root, "library", "music")
	for _, p := range []string{r.dlDir, r.moviesDir, r.showsDir, r.musicDir} {
		if err := os.MkdirAll(p, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	if r.moviesFolder, err = r.lib.AddFolder(bg(), "movie", r.moviesDir, "Movies"); err != nil {
		t.Fatal(err)
	}
	if r.showsFolder, err = r.lib.AddFolder(bg(), "show", r.showsDir, "TV"); err != nil {
		t.Fatal(err)
	}
	if _, err := r.lib.AddFolder(bg(), "music", r.musicDir, "Music"); err != nil {
		t.Fatal(err)
	}

	str := func(s string) *string { return &s }
	r.qbtView, err = r.dl.AddClient(bg(), downloads.ClientInput{Name: str("qBittorrent"), Kind: str("qbittorrent"), URL: str("http://qbt.test:8080")})
	if err != nil {
		t.Fatal(err)
	}
	r.sabView, err = r.dl.AddClient(bg(), downloads.ClientInput{Name: str("SABnzbd"), Kind: str("sabnzbd"), URL: str("http://sab.test:8080"),
		APIKey: str("sab-api-key-0123456789abcdef")})
	if err != nil {
		t.Fatal(err)
	}
	return r
}

// withImporter builds the importer on the rig (most tests call it) and joins it
// to the downloads module.
func (r *rig) withImporter(mod ...func(*Deps)) *rig {
	r.t.Helper()
	deps := Deps{
		Downloads: r.dl, Copies: r.reqs, Titles: r.cat, Library: r.lib, Profiles: r.dec, Blocklist: r.dec,
		Requests: r.reqs, Servers: r.srvs, Search: r.srch, History: r.hist,
	}
	for _, m := range mod {
		m(&deps)
	}
	r.imp = New(r.db, deps, Options{Logger: quietLog(), Hub: r.hub, Now: r.now, SearchDelay: 30 * time.Millisecond})
	r.t.Cleanup(r.imp.Close)
	r.dl.SetCompletedHandler(r.imp)
	return r
}

// addJellyfin registers the fake Jellyfin as a media server, with the movies and
// TV folders as it sees them: /media/movies and /media/tv.
func (r *rig) addJellyfin() {
	r.t.Helper()
	str := func(s string) *string { return &s }
	pm := []pathmap.Rule{{From: r.moviesDir, To: "/media/movies"}, {From: r.showsDir, To: "/media/tv"}}
	if _, err := r.srvs.Add(bg(), mediaservers.Input{Name: str("Jellyfin"), Kind: str("jellyfin"), URL: str(r.jelly.srv.URL),
		Token: str("jellyfin-api-key-0123456789"), PathMap: &pm}); err != nil {
		r.t.Fatal(err)
	}
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

// ---- helpers: titles, requests, downloads -----------------------------------------------

func (r *rig) movie(tmdb string) *titles.Detail {
	r.t.Helper()
	d, _, err := r.cat.Add(bg(), titles.KindMovie, metadata.ProviderTMDB, tmdb, titles.AddOptions{Monitored: true})
	if err != nil {
		r.t.Fatal(err)
	}
	return d
}

func (r *rig) show(tvmaze string) *titles.Detail {
	r.t.Helper()
	d, _, err := r.cat.Add(bg(), titles.KindShow, metadata.ProviderTVmaze, tvmaze, titles.AddOptions{Monitored: true})
	if err != nil {
		r.t.Fatal(err)
	}
	return d
}

func (r *rig) artist(mbid string) *titles.Detail {
	r.t.Helper()
	d, _, err := r.cat.Add(bg(), titles.KindArtist, metadata.ProviderMusicBrainz, mbid, titles.AddOptions{Monitored: true})
	if err != nil {
		r.t.Fatal(err)
	}
	return d
}

func (r *rig) actor() requests.Actor {
	return requests.Actor{UserID: r.owner.ID, Role: string(r.owner.Role)}
}

// ask makes the owner's own (so approved at once) request for a title and
// returns its copy and the request id.
func (r *rig) ask(titleID int64, quality string, scope requests.Scope) (grab.Copy, int64) {
	r.t.Helper()
	res, err := r.reqs.Ask(bg(), r.actor(), requests.AskInput{TitleID: titleID, Quality: quality, Scope: scope})
	if err != nil {
		r.t.Fatalf("Ask: %v", err)
	}
	cp, err := r.reqs.EnsureCopy(bg(), titleID, quality)
	if err != nil {
		r.t.Fatal(err)
	}
	return cp, res.Request.ID
}

func (r *rig) requestState(id int64) string {
	r.t.Helper()
	it, err := r.reqs.Get(bg(), r.actor(), id)
	if err != nil {
		r.t.Fatal(err)
	}
	return it.State
}

var hashCounter int

// hash makes a fresh 40-character info-hash.
func hash() string {
	hashCounter++
	return fmt.Sprintf("%040x", hashCounter)
}

// grabAs sends a release to the client the way the search would, and returns the
// downloads row id. Torrents get a fresh info-hash; usenet ones an NZB link.
func (r *rig) grabAs(cp grab.Copy, t *titles.Detail, release, protocol string, episodeIDs []int64) int64 {
	r.t.Helper()
	rel := grab.Release{Title: release, Protocol: protocol, Size: 1 << 20, GUID: "guid-" + release, IndexerID: 0}
	if protocol == grab.ProtocolUsenet {
		rel.DownloadURL = "http://indexer.test/getnzb/" + strconv.Itoa(int(time.Now().UnixNano()%1e6))
	} else {
		rel.MagnetURL = "magnet:?xt=urn:btih:" + hash() + "&dn=" + release
	}
	id, err := r.dl.Grab(bg(), grab.Target{CopyID: cp.ID, TitleID: t.ID, Kind: t.Kind, Quality: cp.Quality, EpisodeIDs: episodeIDs}, rel)
	if err != nil {
		r.t.Fatal(err)
	}
	return id
}

// content makes a download's files under the client's download folder and
// returns the path of its folder (name) or, with a single file name, the file.
func (r *rig) content(name string, files map[string]string) string {
	r.t.Helper()
	root := filepath.Join(r.dlDir, name)
	for rel, data := range files {
		p := filepath.Join(root, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			r.t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(data), 0o644); err != nil {
			r.t.Fatal(err)
		}
	}
	return root
}

// complete says the client finished the download, with its files at content,
// lets the downloads poll notice, and waits for the import to end.
func (r *rig) complete(id int64, content string) {
	r.t.Helper()
	r.finish(id, content)
	r.imp.Wait()
}

// finish is complete without the wait.
func (r *rig) finish(id int64, content string) {
	r.t.Helper()
	d, err := r.dl.Download(bg(), id)
	if err != nil {
		r.t.Fatal(err)
	}
	c := r.qbt
	if d.Protocol == grab.ProtocolUsenet {
		c = r.sab
	}
	c.finish(d.ClientRef, content, filepath.Dir(content))
	if err := r.dl.Poll(bg()); err != nil {
		r.t.Fatal(err)
	}
}

func (r *rig) download(id int64) downloads.Download {
	r.t.Helper()
	d, err := r.dl.Download(bg(), id)
	if err != nil {
		r.t.Fatal(err)
	}
	return d
}

// historyEvents lists the events of a title's history, oldest first.
func (r *rig) historyEvents(titleID int64) []string {
	r.t.Helper()
	items, err := r.hist.ForTitle(bg(), titleID, 100)
	if err != nil {
		r.t.Fatal(err)
	}
	var out []string
	for i := len(items) - 1; i >= 0; i-- {
		out = append(out, items[i].Event)
	}
	return out
}

func (r *rig) historyItem(titleID int64, event string) map[string]any {
	r.t.Helper()
	items, err := r.hist.ForTitle(bg(), titleID, 100)
	if err != nil {
		r.t.Fatal(err)
	}
	for _, it := range items {
		if it.Event == event {
			var m map[string]any
			if err := json.Unmarshal(it.Detail, &m); err != nil {
				r.t.Fatal(err)
			}
			return m
		}
	}
	r.t.Fatalf("no %q in the history of title %d (have %v)", event, titleID, r.historyEvents(titleID))
	return nil
}

// libraryFiles lists a folder's file rows by relative path.
func (r *rig) libraryFiles(f library.Folder) map[string]library.File {
	r.t.Helper()
	list, _, err := r.lib.Files(bg(), library.FileFilter{FolderID: f.ID, Limit: 1000})
	if err != nil {
		r.t.Fatal(err)
	}
	out := map[string]library.File{}
	for _, f := range list {
		out[f.RelPath] = f
	}
	return out
}

func (r *rig) blocklist() []decide.BlockEntry {
	r.t.Helper()
	list, _, err := r.dec.Blocklist(bg(), 100, 0)
	if err != nil {
		r.t.Fatal(err)
	}
	return list
}
