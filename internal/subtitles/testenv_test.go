package subtitles

import (
	"archive/zip"
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
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
	"github.com/BaileyPillon/marquee/internal/library"
	"github.com/BaileyPillon/marquee/internal/settings"
	"github.com/BaileyPillon/marquee/internal/titles"
)

func bg() context.Context { return context.Background() }

func quietLog() *slog.Logger { return slog.New(slog.NewTextHandler(io.Discard, nil)) }

const (
	testKey      = "os-api-key-0123456789abcdef"
	testUser     = "bailey"
	testPassword = "correct horse battery"
	testVersion  = "1.2.3"
	srtOne       = "1\n00:00:01,000 --> 00:00:02,000\nHello there\n\n"
)

// ---- the fake catalog ---------------------------------------------------------------

type fakeCatalog struct{ details map[int64]*titles.Detail }

func (f fakeCatalog) Get(_ context.Context, id int64) (*titles.Detail, error) {
	d, ok := f.details[id]
	if !ok {
		return nil, titles.ErrNotFound
	}
	return d, nil
}

const (
	movieID = 1
	showID  = 2
)

func newCatalog() fakeCatalog {
	return fakeCatalog{details: map[int64]*titles.Detail{
		movieID: {Title: titles.Title{ID: movieID, Kind: "movie", Name: "Night of the Living Dead", Year: 1968, IMDbID: "tt0063350", TMDBID: 10331}},
		showID: {Title: titles.Title{ID: showID, Kind: "show", Name: "Pioneer One", Year: 2010, IMDbID: "tt1621045"},
			Episodes: []titles.Episode{{ID: 11, Season: 1, Number: 1}, {ID: 12, Season: 1, Number: 2}, {ID: 21, Season: 2, Number: 1}}},
	}}
}

// ---- the fake OpenSubtitles ------------------------------------------------------------

// osItem is one subtitle the fake offers.
type osItem struct {
	FileID    int64
	Release   string
	Language  string
	HI        bool
	Downloads int
	HashMatch bool
	Year      int
	Season    int
	Episode   int
	Machine   bool
	CDNumber  int
}

type fakeOS struct {
	*httptest.Server
	t *testing.T

	mu         sync.Mutex
	searches   []url.Values
	downloads  []int64
	logins     int
	files      map[int64]string // file id -> subtitle text
	items      func(q url.Values) []osItem
	remaining  int // downloads left today; 0 answers 406
	status     int // when not 0: every search answers with this status
	retryAfter string
	badHeaders []string
	downloadAs func(fileID int64) (int, string) // overrides a download's answer
	linkBody   map[int64][]byte                 // raw bytes behind a link (default: files)
}

func newFakeOS(t *testing.T) *fakeOS {
	f := &fakeOS{t: t, files: map[int64]string{}, remaining: 100, linkBody: map[int64][]byte{}}
	f.Server = httptest.NewServer(http.HandlerFunc(f.serve))
	t.Cleanup(f.Close)
	return f
}

func (f *fakeOS) base() string { return f.URL + "/api/v1" }

func (f *fakeOS) checkHeaders(r *http.Request) {
	if r.Header.Get("Api-Key") != testKey {
		f.badHeaders = append(f.badHeaders, "Api-Key="+r.Header.Get("Api-Key"))
	}
	if r.Header.Get("User-Agent") != "Marquee v"+testVersion {
		f.badHeaders = append(f.badHeaders, "User-Agent="+r.Header.Get("User-Agent"))
	}
}

func (f *fakeOS) serve(w http.ResponseWriter, r *http.Request) {
	f.mu.Lock()
	defer f.mu.Unlock()
	w.Header().Set("Content-Type", "application/json")
	switch {
	case r.URL.Path == "/api/v1/subtitles" && r.Method == http.MethodGet:
		f.checkHeaders(r)
		f.searches = append(f.searches, r.URL.Query())
		if f.status != 0 {
			if f.retryAfter != "" {
				w.Header().Set("Retry-After", f.retryAfter)
			}
			w.WriteHeader(f.status)
			_, _ = io.WriteString(w, `{"message":"nope"}`)
			return
		}
		var items []osItem
		if f.items != nil {
			items = f.items(r.URL.Query())
		}
		type file struct {
			FileID   int64  `json:"file_id"`
			CDNumber int    `json:"cd_number"`
			FileName string `json:"file_name"`
		}
		var data []map[string]any
		for _, it := range items {
			cd := it.CDNumber
			if cd == 0 {
				cd = 1
			}
			data = append(data, map[string]any{
				"id": strconv.FormatInt(it.FileID+1000, 10), "type": "subtitle",
				"attributes": map[string]any{
					"subtitle_id": strconv.FormatInt(it.FileID+1000, 10), "language": it.Language, "download_count": it.Downloads,
					"hearing_impaired": it.HI, "release": it.Release, "moviehash_match": it.HashMatch,
					"ai_translated": false, "machine_translated": it.Machine,
					"feature_details": map[string]any{"year": it.Year, "season_number": it.Season, "episode_number": it.Episode},
					"files":           []file{{FileID: it.FileID, CDNumber: cd, FileName: it.Release + ".srt"}},
				},
			})
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"total_count": len(data), "total_pages": 1, "page": 1, "data": data})

	case r.URL.Path == "/api/v1/login" && r.Method == http.MethodPost:
		f.checkHeaders(r)
		var in struct{ Username, Password string }
		_ = json.NewDecoder(r.Body).Decode(&in)
		f.logins++
		if in.Username != testUser || in.Password != testPassword {
			w.WriteHeader(http.StatusUnauthorized)
			_, _ = io.WriteString(w, `{"message":"You cannot consume this service"}`)
			return
		}
		_, _ = io.WriteString(w, `{"user":{"allowed_downloads":100},"token":"jwt-token","status":200,"base_url":"evil.example.net"}`)

	case r.URL.Path == "/api/v1/download" && r.Method == http.MethodPost:
		f.checkHeaders(r)
		var in struct {
			FileID    int64  `json:"file_id"`
			SubFormat string `json:"sub_format"`
		}
		_ = json.NewDecoder(r.Body).Decode(&in)
		f.downloads = append(f.downloads, in.FileID)
		if f.downloadAs != nil {
			if status, body := f.downloadAs(in.FileID); status != 0 {
				if status == http.StatusTooManyRequests && f.retryAfter != "" {
					w.Header().Set("Retry-After", f.retryAfter)
				}
				w.WriteHeader(status)
				_, _ = io.WriteString(w, body)
				return
			}
		}
		if f.remaining <= 0 {
			w.WriteHeader(http.StatusNotAcceptable)
			_, _ = io.WriteString(w, `{"message":"You have downloaded your allowed 20 subtitles for 24h.","requests":20,"remaining":0,"reset_time":"23 hours","reset_time_utc":"2026-11-22T00:00:00.000Z"}`)
			return
		}
		f.remaining--
		_ = json.NewEncoder(w).Encode(map[string]any{
			"link": f.URL + "/dl/" + strconv.FormatInt(in.FileID, 10) + ".srt", "file_name": "x.srt",
			"requests": 1, "remaining": f.remaining, "reset_time_utc": "2026-11-22T00:00:00.000Z",
		})

	case strings.HasPrefix(r.URL.Path, "/dl/"):
		id, _ := strconv.ParseInt(strings.TrimSuffix(strings.TrimPrefix(r.URL.Path, "/dl/"), ".srt"), 10, 64)
		if raw, ok := f.linkBody[id]; ok {
			_, _ = w.Write(raw)
			return
		}
		text, ok := f.files[id]
		if !ok {
			text = srtOne
		}
		_, _ = io.WriteString(w, text)

	default:
		http.NotFound(w, r)
	}
}

func (f *fakeOS) searchCount() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.searches)
}

func (f *fakeOS) lastSearch() url.Values {
	f.mu.Lock()
	defer f.mu.Unlock()
	if len(f.searches) == 0 {
		return nil
	}
	return f.searches[len(f.searches)-1]
}

func (f *fakeOS) downloaded() []int64 {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]int64(nil), f.downloads...)
}

func (f *fakeOS) noBadHeaders() {
	f.t.Helper()
	f.mu.Lock()
	defer f.mu.Unlock()
	if len(f.badHeaders) > 0 {
		f.t.Errorf("OpenSubtitles was called with wrong headers: %v", f.badHeaders)
	}
}

// ---- the fake Podnapisi -----------------------------------------------------------------

type fakePod struct {
	*httptest.Server
	t *testing.T

	mu        sync.Mutex
	searches  []url.Values
	accepts   []string
	downloads []string
	entries   func(q url.Values) []map[string]any
	zips      map[string][]byte // id -> the body of its download
	status    int
}

func newFakePod(t *testing.T) *fakePod {
	f := &fakePod{t: t, zips: map[string][]byte{}}
	f.Server = httptest.NewServer(http.HandlerFunc(f.serve))
	t.Cleanup(f.Close)
	return f
}

func (f *fakePod) serve(w http.ResponseWriter, r *http.Request) {
	f.mu.Lock()
	defer f.mu.Unlock()
	switch {
	case r.URL.Path == "/subtitles/search/advanced":
		f.searches = append(f.searches, r.URL.Query())
		f.accepts = append(f.accepts, r.Header.Get("Accept"))
		if f.status != 0 {
			w.WriteHeader(f.status)
			return
		}
		var entries []map[string]any
		if f.entries != nil {
			entries = f.entries(r.URL.Query())
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{"status": "ok", "data": entries})
	case strings.HasPrefix(r.URL.Path, "/subtitles/") && strings.HasSuffix(r.URL.Path, "/download"):
		id := strings.TrimSuffix(strings.TrimPrefix(r.URL.Path, "/subtitles/"), "/download")
		f.downloads = append(f.downloads, id)
		body, ok := f.zips[id]
		if !ok {
			body = zipOf(map[string]string{"sub.srt": srtOne})
		}
		_, _ = w.Write(body)
	default:
		http.NotFound(w, r)
	}
}

func (f *fakePod) searchCount() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.searches)
}

func (f *fakePod) downloaded() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]string(nil), f.downloads...)
}

// zipOf builds a zip archive with the given files (in sorted name order).
func zipOf(files map[string]string) []byte {
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	var names []string
	for n := range files {
		names = append(names, n)
	}
	sortStrings(names)
	for _, n := range names {
		w, _ := zw.Create(n)
		_, _ = io.WriteString(w, files[n])
	}
	_ = zw.Close()
	return buf.Bytes()
}

func sortStrings(s []string) {
	for i := 1; i < len(s); i++ {
		for j := i; j > 0 && s[j] < s[j-1]; j-- {
			s[j], s[j-1] = s[j-1], s[j]
		}
	}
}

// ---- the rig -------------------------------------------------------------------------------

type rig struct {
	t     *testing.T
	db    *sql.DB
	lib   *library.Service
	cfg   *settings.Service
	hub   *events.Hub
	svc   *Service
	os    *fakeOS
	pod   *fakePod
	root  string
	movie library.Folder
	tv    library.Folder

	clockMu sync.Mutex
	clockAt time.Time

	notified []string
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
	box, err := secrets.New([]byte(strings.Repeat("k", secrets.KeySize)))
	if err != nil {
		t.Fatal(err)
	}
	log := quietLog()
	r := &rig{t: t, db: d, root: root, clockAt: time.Date(2026, 11, 21, 12, 0, 0, 0, time.UTC)}
	r.hub = events.NewHub(log)
	t.Cleanup(r.hub.Close)
	r.lib = library.NewService(d, library.Options{Logger: log, Hub: r.hub})
	t.Cleanup(r.lib.Close)
	r.cfg = settings.New(d, box)
	r.os, r.pod = newFakeOS(t), newFakePod(t)

	hc := outbound.New("test", outbound.Options{Logger: log, HostRPS: 100000, HostBurst: 100000, MaxRetries: -1})
	r.svc = New(d, Deps{Library: r.lib, Catalog: newCatalog(), Settings: r.cfg}, Options{
		Logger: log, Now: r.now, HTTP: hc, Version: testVersion, Hub: r.hub,
		OpenSubtitlesURL: r.os.base(), PodnapisiURL: r.pod.URL,
	})
	r.svc.SetNotifier(func(_ context.Context, paths []string) { r.notified = append(r.notified, paths...) })

	for _, f := range []struct {
		kind string
		dst  *library.Folder
	}{{"movie", &r.movie}, {"show", &r.tv}} {
		dir := filepath.Join(root, "library", f.kind)
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
		if *f.dst, err = r.lib.AddFolder(bg(), f.kind, dir, f.kind); err != nil {
			t.Fatal(err)
		}
	}
	return r
}

// configure writes the subtitles settings section (merging into the defaults the
// service uses for what is left out).
func (r *rig) configure(values map[string]any) {
	r.t.Helper()
	if _, err := r.cfg.Put(bg(), Section, values); err != nil {
		r.t.Fatal(err)
	}
}

// useOpenSubtitles turns the fake OpenSubtitles on with the test key and, when
// login is true, the test account.
func (r *rig) useOpenSubtitles(login bool, more map[string]any) {
	r.t.Helper()
	os := map[string]any{"enabled": true, "apiKey": testKey}
	if login {
		os["username"], os["password"] = testUser, testPassword
	}
	v := map[string]any{
		"languages": []any{"en"},
		"providers": map[string]any{"opensubtitles": os, "podnapisi": map[string]any{"enabled": false}},
	}
	for k, x := range more {
		v[k] = x
	}
	r.configure(v)
}

func (r *rig) usePodnapisi(more map[string]any) {
	r.t.Helper()
	v := map[string]any{
		"languages": []any{"en"},
		"providers": map[string]any{"opensubtitles": map[string]any{"enabled": false}, "podnapisi": map[string]any{"enabled": true}},
	}
	for k, x := range more {
		v[k] = x
	}
	r.configure(v)
}

// movieFile puts a video of the movie in the library (matched to it) and returns
// its file id and path. size is the file's size in bytes.
func (r *rig) movieFile(rel string, size int) (int64, string) {
	r.t.Helper()
	return r.videoFile(r.movie, rel, size, movieID, nil)
}

func (r *rig) episodeFile(rel string, episodeID int64) (int64, string) {
	r.t.Helper()
	return r.videoFile(r.tv, rel, 200_000, showID, []int64{episodeID})
}

func (r *rig) videoFile(folder library.Folder, rel string, size int, titleID int64, episodes []int64) (int64, string) {
	r.t.Helper()
	p := filepath.Join(folder.Path, filepath.FromSlash(rel))
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		r.t.Fatal(err)
	}
	data := make([]byte, size)
	for i := range data {
		data[i] = byte(i*7 + int(titleID))
	}
	if err := os.WriteFile(p, data, 0o644); err != nil {
		r.t.Fatal(err)
	}
	if err := r.lib.ImportFiles(bg(), folder.ID, []library.ImportedFile{{RelPath: rel, TitleID: titleID, EpisodeIDs: episodes}}); err != nil {
		r.t.Fatal(err)
	}
	ids, err := r.lib.FileIDs(bg(), folder.ID, []string{rel})
	if err != nil || ids[rel] == 0 {
		r.t.Fatalf("FileIDs = %v, %v", ids, err)
	}
	return ids[rel], p
}

const nolRel = "Night of the Living Dead (1968)/Night of the Living Dead (1968) Bluray-1080p.mkv"

func (r *rig) nol() (int64, string) { return r.movieFile(nolRel, 200_000) }

func (r *rig) read(p string) string {
	r.t.Helper()
	b, err := os.ReadFile(p)
	if err != nil {
		r.t.Fatal(err)
	}
	return string(b)
}

func exists(p string) bool { _, err := os.Stat(p); return err == nil }

func (r *rig) subtitleRows(fileID int64) []Subtitle {
	r.t.Helper()
	rows, err := r.svc.store.List(bg(), fileID)
	if err != nil {
		r.t.Fatal(err)
	}
	return rows
}

func (r *rig) count(query string, args ...any) int {
	r.t.Helper()
	var n int
	if err := r.db.QueryRow(query, args...).Scan(&n); err != nil {
		r.t.Fatal(err)
	}
	return n
}

func (r *rig) results(fileID int64, lang string) []string {
	r.t.Helper()
	rows, err := r.db.Query(`SELECT result FROM subtitle_searches WHERE file_id = ? AND language = ? ORDER BY at, rowid`, fileID, lang)
	if err != nil {
		r.t.Fatal(err)
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var s string
		if err := rows.Scan(&s); err != nil {
			r.t.Fatal(err)
		}
		out = append(out, s)
	}
	return out
}

func itoa(n int64) string { return fmt.Sprint(n) }
