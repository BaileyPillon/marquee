package downloads

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"testing"
)

// fakeQBT is a qBittorrent WebAPI v2 server.
type fakeQBT struct {
	t   *testing.T
	srv *httptest.Server

	mu          sync.Mutex
	user, pass  string
	requireAuth bool
	v5          bool // has stop/start; otherwise pause/resume
	sessions    map[string]bool
	logins      int
	cats        map[string]bool
	createdCats []string
	torrents    []map[string]any
	adds        []qbtAdd
	addMode     string // "ok" (default), "fails", "json-ok", "json-fail"
	infoQueries []url.Values
	deletes     []url.Values
	pauseCalls  []string
	expireNext  bool // answer the next authenticated call with 403 and forget the sessions
	// modern makes the fake answer the way qBittorrent 5.3 does (seen live):
	// login is 204 with no body, a wrong password is 401, and the session
	// cookie is named QBT_SID_<port>. Otherwise it answers like 4.x: 200 "Ok."
	// or "Fails." and a cookie named SID.
	modern bool
}

func (f *fakeQBT) cookieName() string {
	if f.modern {
		return "QBT_SID_8080"
	}
	return "SID"
}

type qbtAdd struct {
	Fields map[string]string
	File   []byte
	Cookie string
}

func newFakeQBT(t *testing.T) *fakeQBT {
	f := &fakeQBT{t: t, user: "admin", pass: "adminadmin", requireAuth: true, v5: true,
		sessions: map[string]bool{}, cats: map[string]bool{}}
	f.srv = httptest.NewServer(http.HandlerFunc(f.handle))
	t.Cleanup(f.srv.Close)
	return f
}

func (f *fakeQBT) adapter(t *testing.T, user, pass string) *qbittorrent {
	t.Helper()
	a, err := newQBittorrent(AdapterConfig{Kind: KindQBittorrent, URL: f.srv.URL, Username: user, Password: pass, HTTP: testHTTP()})
	if err != nil {
		t.Fatal(err)
	}
	return a
}

func (f *fakeQBT) loginCount() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.logins
}

func (f *fakeQBT) handle(w http.ResponseWriter, r *http.Request) {
	f.mu.Lock()
	defer f.mu.Unlock()
	path := strings.TrimPrefix(r.URL.Path, "/api/v2")
	if path == "/auth/login" {
		_ = r.ParseForm()
		f.logins++
		if r.PostForm.Get("username") == f.user && r.PostForm.Get("password") == f.pass {
			sid := fmt.Sprintf("sid%d", f.logins)
			f.sessions[sid] = true
			http.SetCookie(w, &http.Cookie{Name: f.cookieName(), Value: sid, Path: "/"})
			if f.modern {
				w.WriteHeader(http.StatusNoContent)
				return
			}
			_, _ = w.Write([]byte("Ok."))
			return
		}
		if f.modern {
			http.Error(w, "Unauthorized", http.StatusUnauthorized)
			return
		}
		_, _ = w.Write([]byte("Fails."))
		return
	}
	cookie := ""
	if c, err := r.Cookie(f.cookieName()); err == nil {
		cookie = c.Value
	}
	if f.requireAuth {
		if f.expireNext {
			f.expireNext = false
			f.sessions = map[string]bool{}
		}
		if !f.sessions[cookie] {
			http.Error(w, "Forbidden", http.StatusForbidden)
			return
		}
	}
	switch path {
	case "/app/version":
		_, _ = w.Write([]byte("v5.0.1"))
	case "/torrents/categories":
		out := map[string]any{}
		for c := range f.cats {
			out[c] = map[string]string{"name": c, "savePath": ""}
		}
		writeTestJSON(w, out)
	case "/torrents/createCategory":
		_ = r.ParseForm()
		c := r.PostForm.Get("category")
		if f.cats[c] {
			http.Error(w, "Category name already exists", http.StatusConflict)
			return
		}
		f.cats[c] = true
		f.createdCats = append(f.createdCats, c)
	case "/torrents/add":
		f.handleAdd(w, r, cookie)
	case "/torrents/info":
		_ = r.ParseForm()
		f.infoQueries = append(f.infoQueries, r.Form)
		var out []map[string]any
		for _, t := range f.torrents {
			if c := r.Form.Get("category"); r.Form.Has("category") && t["category"] != c {
				continue
			}
			if h := r.Form.Get("hashes"); h != "" && t["hash"] != h {
				continue
			}
			out = append(out, t)
		}
		if out == nil {
			out = []map[string]any{}
		}
		writeTestJSON(w, out)
	case "/torrents/delete":
		_ = r.ParseForm()
		f.deletes = append(f.deletes, r.PostForm)
		var keep []map[string]any
		for _, t := range f.torrents {
			if t["hash"] != r.PostForm.Get("hashes") {
				keep = append(keep, t)
			}
		}
		f.torrents = keep
	case "/torrents/stop", "/torrents/start":
		if !f.v5 {
			http.NotFound(w, r)
			return
		}
		_ = r.ParseForm()
		f.pauseCalls = append(f.pauseCalls, strings.TrimPrefix(path, "/torrents/")+":"+r.PostForm.Get("hashes"))
	case "/torrents/pause", "/torrents/resume":
		if f.v5 {
			http.NotFound(w, r)
			return
		}
		_ = r.ParseForm()
		f.pauseCalls = append(f.pauseCalls, strings.TrimPrefix(path, "/torrents/")+":"+r.PostForm.Get("hashes"))
	default:
		http.NotFound(w, r)
	}
}

func (f *fakeQBT) handleAdd(w http.ResponseWriter, r *http.Request, cookie string) {
	if err := r.ParseMultipartForm(10 << 20); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	add := qbtAdd{Fields: map[string]string{}, Cookie: cookie}
	for k, v := range r.MultipartForm.Value {
		add.Fields[k] = v[0]
	}
	if fhs := r.MultipartForm.File["torrents"]; len(fhs) == 1 {
		fh, _ := fhs[0].Open()
		buf := make([]byte, fhs[0].Size)
		_, _ = fh.Read(buf)
		fh.Close()
		add.File = buf
		add.Fields["filename"] = fhs[0].Filename
		add.Fields["file-content-type"] = fhs[0].Header.Get("Content-Type")
	}
	f.adds = append(f.adds, add)
	switch f.addMode {
	case "fails":
		_, _ = w.Write([]byte("Fails."))
	case "json-ok":
		writeTestJSON(w, map[string]any{"success_count": 1, "failure_count": 0, "pending_count": 0, "added_torrent_ids": []string{strings.Repeat("ab", 20)}})
	case "json-fail":
		w.WriteHeader(http.StatusConflict)
		writeTestJSON(w, map[string]any{"success_count": 0, "failure_count": 1, "pending_count": 0})
	case "415":
		w.WriteHeader(http.StatusUnsupportedMediaType)
	default:
		_, _ = w.Write([]byte("Ok."))
	}
}

func writeTestJSON(w http.ResponseWriter, v any) {
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(v)
}

func (f *fakeQBT) setTorrents(ts ...map[string]any) {
	f.mu.Lock()
	f.torrents = ts
	f.mu.Unlock()
}

func (f *fakeQBT) lastAdd(t *testing.T) qbtAdd {
	t.Helper()
	f.mu.Lock()
	defer f.mu.Unlock()
	if len(f.adds) == 0 {
		t.Fatal("qBittorrent saw no add")
	}
	return f.adds[len(f.adds)-1]
}

// ---- tests -------------------------------------------------------------------

// bothDialects runs a test against the 4.x-style and the 5.3-style qBittorrent.
func bothDialects(t *testing.T, run func(t *testing.T, f *fakeQBT)) {
	for name, modern := range map[string]bool{"4.x": false, "5.3": true} {
		t.Run(name, func(t *testing.T) {
			f := newFakeQBT(t)
			f.modern = modern
			run(t, f)
		})
	}
}

func TestQBTLoginAndVersion(t *testing.T) {
	bothDialects(t, func(t *testing.T, f *fakeQBT) {
		a := f.adapter(t, "admin", "adminadmin")
		v, err := a.Test(context.Background())
		if err != nil || v != "v5.0.1" {
			t.Fatalf("Test = %q, %v", v, err)
		}
		if f.loginCount() != 1 {
			t.Errorf("logins = %d, want 1 (signed in before the first call, not after a 403)", f.loginCount())
		}
		// The session is kept: a second call does not sign in again.
		if _, err := a.Test(context.Background()); err != nil || f.loginCount() != 1 {
			t.Errorf("second call: %v, logins %d", err, f.loginCount())
		}
		// Whatever the cookie is called, it goes back on later requests.
		if _, err := a.Add(context.Background(), AddInput{TorrentFile: tinyTorrent(tinyInfo("a", 1)), Category: "marquee"}); err != nil {
			t.Fatal(err)
		}
		if add := f.lastAdd(t); add.Cookie == "" {
			t.Error("the session cookie was not sent back")
		}
	})
}

func TestQBTWrongPasswordIsAnAuthError(t *testing.T) {
	bothDialects(t, func(t *testing.T, f *fakeQBT) {
		_, err := f.adapter(t, "admin", "nope").Test(context.Background())
		if !errors.Is(err, ErrAuth) {
			t.Fatalf("err = %v, want ErrAuth", err)
		}
		if strings.Contains(err.Error(), "nope") {
			t.Errorf("the password is in the error: %v", err)
		}
		if got := PlainError(err); !strings.Contains(got, "sign-in") {
			t.Errorf("PlainError = %q", got)
		}
	})
}

func TestQBTReLoginsOn403(t *testing.T) {
	bothDialects(t, func(t *testing.T, f *fakeQBT) {
		a := f.adapter(t, "admin", "adminadmin")
		if _, err := a.Test(context.Background()); err != nil {
			t.Fatal(err)
		}
		// qBittorrent forgets the session (restart, expiry): the next call gets a
		// 403, signs in again and succeeds.
		f.mu.Lock()
		f.expireNext = true
		f.mu.Unlock()
		items, err := a.List(context.Background(), "marquee")
		if err != nil {
			t.Fatalf("List after expiry: %v", err)
		}
		if len(items) != 0 || f.loginCount() != 2 {
			t.Errorf("items %v, logins %d, want 2 logins", items, f.loginCount())
		}
	})
}

func TestQBTNoLoginWhenQBittorrentDoesNotAskForOne(t *testing.T) {
	f := newFakeQBT(t)
	f.requireAuth = false // localhost bypass
	a := f.adapter(t, "", "")
	if v, err := a.Test(context.Background()); err != nil || v == "" {
		t.Fatalf("Test = %q, %v", v, err)
	}
	if f.loginCount() != 0 {
		t.Errorf("logins = %d, want none", f.loginCount())
	}
}

func TestQBTNoCredentialsButOneIsNeeded(t *testing.T) {
	f := newFakeQBT(t)
	_, err := f.adapter(t, "", "").Test(context.Background())
	if !errors.Is(err, ErrAuth) {
		t.Errorf("err = %v, want ErrAuth", err)
	}
}

func TestQBTUnreachable(t *testing.T) {
	f := newFakeQBT(t)
	a := f.adapter(t, "admin", "adminadmin")
	f.srv.Close()
	_, err := a.Test(context.Background())
	if !errors.Is(err, ErrUnreachable) {
		t.Fatalf("err = %v, want ErrUnreachable", err)
	}
	if got := PlainError(err); !strings.HasPrefix(got, "Marquee could not reach the client.") {
		t.Errorf("PlainError = %q", got)
	}
}

func TestQBTAddByFile(t *testing.T) {
	f := newFakeQBT(t)
	a := f.adapter(t, "admin", "adminadmin")
	info := tinyInfo("Movie.2020.1080p.mkv", 1234)
	file := tinyTorrent(info)
	want, _ := ParseTorrent(file)

	ref, err := a.Add(context.Background(), AddInput{TorrentFile: file, Category: "marquee", SavePath: `M:\dl`})
	if err != nil {
		t.Fatal(err)
	}
	if ref != want.InfoHash {
		t.Errorf("ref = %s, want the torrent's info-hash %s", ref, want.InfoHash)
	}
	add := f.lastAdd(t)
	if string(add.File) != string(file) {
		t.Error("the client did not receive the torrent file bytes")
	}
	if add.Fields["file-content-type"] != "application/x-bittorrent" || !strings.HasSuffix(add.Fields["filename"], ".torrent") {
		t.Errorf("file part = %q %q", add.Fields["filename"], add.Fields["file-content-type"])
	}
	if _, has := add.Fields["urls"]; has {
		t.Error("urls sent along with a file")
	}
	for k, v := range map[string]string{
		"category": "marquee", "savepath": `M:\dl`, "autoTMM": "false", "paused": "false", "stopped": "false",
	} {
		if add.Fields[k] != v {
			t.Errorf("field %s = %q, want %q", k, add.Fields[k], v)
		}
	}
	if add.Cookie == "" {
		t.Error("add was sent without the session cookie")
	}
	// The category did not exist: it was created, once.
	if len(f.createdCats) != 1 || f.createdCats[0] != "marquee" {
		t.Errorf("created categories = %v", f.createdCats)
	}
	if _, err := a.Add(context.Background(), AddInput{TorrentFile: tinyTorrent(tinyInfo("other.mkv", 5)), Category: "marquee"}); err != nil {
		t.Fatal(err)
	}
	if len(f.createdCats) != 1 {
		t.Errorf("category created again: %v", f.createdCats)
	}
	// No save path given: none sent, and automatic torrent management is left alone.
	if add := f.lastAdd(t); add.Fields["savepath"] != "" || add.Fields["autoTMM"] != "" {
		t.Errorf("savepath %q autoTMM %q sent without a save path", add.Fields["savepath"], add.Fields["autoTMM"])
	}
}

func TestQBTAddKeepsAnExistingCategory(t *testing.T) {
	f := newFakeQBT(t)
	f.cats["marquee"] = true
	if _, err := f.adapter(t, "admin", "adminadmin").Add(context.Background(),
		AddInput{TorrentFile: tinyTorrent(tinyInfo("a", 1)), Category: "marquee"}); err != nil {
		t.Fatal(err)
	}
	if len(f.createdCats) != 0 {
		t.Errorf("created %v although it existed", f.createdCats)
	}
}

func TestQBTAddByMagnetHexAndBase32(t *testing.T) {
	for name, magnet := range map[string]string{
		"hex":    "magnet:?xt=urn:btih:" + strings.ToUpper(bbbHex) + "&dn=Big+Buck+Bunny",
		"base32": "magnet:?xt=urn:btih:" + bbbBase32 + "&dn=Big+Buck+Bunny",
	} {
		f := newFakeQBT(t)
		ref, err := f.adapter(t, "admin", "adminadmin").Add(context.Background(),
			AddInput{MagnetURL: magnet, Category: "marquee", Paused: true})
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		if ref != bbbHex {
			t.Errorf("%s: ref = %s, want %s", name, ref, bbbHex)
		}
		add := f.lastAdd(t)
		if add.Fields["urls"] != magnet || len(add.File) != 0 {
			t.Errorf("%s: urls = %q, file %d bytes", name, add.Fields["urls"], len(add.File))
		}
		// qBittorrent 5 calls it stopped; 4 calls it paused: both are sent.
		if add.Fields["paused"] != "true" || add.Fields["stopped"] != "true" {
			t.Errorf("%s: paused %q stopped %q", name, add.Fields["paused"], add.Fields["stopped"])
		}
	}
}

func TestQBTAddRefusals(t *testing.T) {
	ctx := context.Background()
	file := tinyTorrent(tinyInfo("a.mkv", 1))
	want, _ := ParseTorrent(file)

	// "Fails." because the torrent is already there: fine, same ref.
	f := newFakeQBT(t)
	f.addMode = "fails"
	f.setTorrents(map[string]any{"hash": want.InfoHash, "name": "a.mkv", "state": "downloading", "category": "marquee"})
	ref, err := f.adapter(t, "admin", "adminadmin").Add(ctx, AddInput{TorrentFile: file, Category: "marquee"})
	if err != nil || ref != want.InfoHash {
		t.Errorf("duplicate: %q, %v", ref, err)
	}

	// "Fails." and it is not there: rejected.
	f = newFakeQBT(t)
	f.addMode = "fails"
	if _, err := f.adapter(t, "admin", "adminadmin").Add(ctx, AddInput{TorrentFile: file, Category: "marquee"}); !errors.Is(err, ErrRejected) {
		t.Errorf("fails: %v, want ErrRejected", err)
	}
	f = newFakeQBT(t)
	f.addMode = "415"
	if _, err := f.adapter(t, "admin", "adminadmin").Add(ctx, AddInput{TorrentFile: file, Category: "marquee"}); !errors.Is(err, ErrRejected) {
		t.Errorf("415: %v, want ErrRejected", err)
	}

	// Newer qBittorrent answers JSON.
	f = newFakeQBT(t)
	f.addMode = "json-ok"
	if ref, err := f.adapter(t, "admin", "adminadmin").Add(ctx, AddInput{TorrentFile: file, Category: "marquee"}); err != nil || ref != want.InfoHash {
		t.Errorf("json ok: %q, %v", ref, err)
	}
	f = newFakeQBT(t)
	f.addMode = "json-fail"
	if _, err := f.adapter(t, "admin", "adminadmin").Add(ctx, AddInput{TorrentFile: file, Category: "marquee"}); !errors.Is(err, ErrRejected) {
		t.Errorf("json fail: %v, want ErrRejected", err)
	}

	// Garbage in: nothing reaches the client.
	f = newFakeQBT(t)
	a := f.adapter(t, "admin", "adminadmin")
	for name, in := range map[string]AddInput{
		"empty":        {},
		"bad file":     {TorrentFile: []byte("<html>")},
		"bad magnet":   {MagnetURL: "magnet:?dn=nohash"},
		"not a magnet": {MagnetURL: "http://example.com"},
	} {
		if _, err := a.Add(ctx, in); !errors.Is(err, ErrRejected) {
			t.Errorf("%s: %v, want ErrRejected", name, err)
		}
	}
	f.mu.Lock()
	n := len(f.adds)
	f.mu.Unlock()
	if n != 0 {
		t.Errorf("%d bad adds reached qBittorrent", n)
	}
}

func TestQBTListAndStateMapping(t *testing.T) {
	cases := []struct {
		state    string
		want     string
		finished bool
	}{
		{"downloading", StateDownloading, false},
		{"forcedDL", StateDownloading, false},
		{"metaDL", StateDownloading, false},
		{"forcedMetaDL", StateDownloading, false},
		{"moving", StateDownloading, false},
		{"stalledDL", StateStalled, false},
		{"queuedDL", StateQueued, false},
		{"checkingDL", StateQueued, false},
		{"allocating", StateQueued, false},
		{"pausedDL", StatePaused, false},
		{"stoppedDL", StatePaused, false},
		{"uploading", StateCompleted, true},
		{"stalledUP", StateCompleted, true},
		{"queuedUP", StateCompleted, true},
		{"forcedUP", StateCompleted, true},
		{"checkingUP", StateCompleted, true},
		{"pausedUP", StateCompleted, true},
		{"stoppedUP", StateCompleted, true},
		{"error", StateFailed, false},
		{"missingFiles", StateFailed, false},
		{"somethingNew", StateDownloading, false},
	}
	f := newFakeQBT(t)
	var ts []map[string]any
	for i, c := range cases {
		ts = append(ts, map[string]any{
			"hash": fmt.Sprintf("%040x", i), "name": c.state, "state": c.state, "category": "marquee",
			"progress": 0.5, "size": 1000, "amount_left": 500, "dlspeed": 2048, "eta": 60,
			"save_path": "M:\\dl", "content_path": "M:\\dl\\" + c.state,
		})
	}
	ts = append(ts, map[string]any{"hash": "ffff", "name": "someone elses", "state": "downloading", "category": "movies"})
	f.setTorrents(ts...)

	items, err := f.adapter(t, "admin", "adminadmin").List(context.Background(), "marquee")
	if err != nil {
		t.Fatal(err)
	}
	if len(items) != len(cases) {
		t.Fatalf("got %d items, want %d (the other category must be filtered out)", len(items), len(cases))
	}
	f.mu.Lock()
	if q := f.infoQueries[0]; q.Get("category") != "marquee" {
		t.Errorf("category query = %v", q)
	}
	f.mu.Unlock()
	for i, c := range cases {
		it := items[i]
		if it.State != c.want || it.Finished != c.finished {
			t.Errorf("%s: state %q finished %v, want %q %v", c.state, it.State, it.Finished, c.want, c.finished)
		}
		if it.Ref != fmt.Sprintf("%040x", i) || it.ContentPath != "M:\\dl\\"+c.state || it.SavePath != "M:\\dl" {
			t.Errorf("%s: ref/paths = %q %q %q", c.state, it.Ref, it.ContentPath, it.SavePath)
		}
		if c.finished {
			if it.Progress != 1 || it.ETA != 0 || it.SpeedBps != 0 {
				t.Errorf("%s: finished item = %+v", c.state, it)
			}
		} else if it.Progress != 0.5 || it.Size != 1000 || it.Done != 500 || it.SpeedBps != 2048 || it.ETA != 60 {
			t.Errorf("%s: numbers = %+v", c.state, it)
		}
		if c.want == StateFailed && it.Error == "" {
			t.Errorf("%s: no error text", c.state)
		}
	}
}

func TestQBTListOddities(t *testing.T) {
	f := newFakeQBT(t)
	f.setTorrents(
		// ETA of 8640000 is "never"; no content_path (qBittorrent before 4.3.2).
		map[string]any{"hash": "AAAA", "name": "old", "state": "downloading", "category": "marquee", "progress": 0.1,
			"size": 10, "amount_left": 9, "dlspeed": 0, "eta": 8640000, "save_path": "/dl/"},
		// Progress out of range.
		map[string]any{"hash": "bbbb", "name": "odd", "state": "downloading", "category": "marquee", "progress": 1.7,
			"size": 10, "amount_left": -3, "eta": -1},
	)
	items, err := f.adapter(t, "admin", "adminadmin").List(context.Background(), "marquee")
	if err != nil {
		t.Fatal(err)
	}
	if items[0].Ref != "aaaa" || items[0].ETA != -1 || items[0].ContentPath != "/dl/old" {
		t.Errorf("item 0 = %+v", items[0])
	}
	if items[1].Progress != 1 || items[1].ETA != -1 {
		t.Errorf("item 1 = %+v", items[1])
	}
}

func TestQBTPauseResumeUsesStopStartThenFallsBack(t *testing.T) {
	ctx := context.Background()

	// qBittorrent 5: stop and start.
	f := newFakeQBT(t)
	a := f.adapter(t, "admin", "adminadmin")
	if err := a.Pause(ctx, "abc"); err != nil {
		t.Fatal(err)
	}
	if err := a.Resume(ctx, "abc"); err != nil {
		t.Fatal(err)
	}
	if got := strings.Join(f.pauseCalls, ","); got != "stop:abc,start:abc" {
		t.Errorf("v5 calls = %s", got)
	}

	// qBittorrent 4: stop and start are 404, so it falls back to pause and
	// resume, and remembers: the next call goes straight to pause.
	f = newFakeQBT(t)
	f.v5 = false
	a = f.adapter(t, "admin", "adminadmin")
	if err := a.Pause(ctx, "abc"); err != nil {
		t.Fatal(err)
	}
	if err := a.Resume(ctx, "abc"); err != nil {
		t.Fatal(err)
	}
	if got := strings.Join(f.pauseCalls, ","); got != "pause:abc,resume:abc" {
		t.Errorf("v4 calls = %s", got)
	}
	if a.pauseAPI.Load() != 2 {
		t.Errorf("pauseAPI = %d, want 2 (v4 remembered)", a.pauseAPI.Load())
	}
}

func TestQBTRemove(t *testing.T) {
	f := newFakeQBT(t)
	f.setTorrents(map[string]any{"hash": "abc", "name": "x", "state": "downloading", "category": "marquee"})
	a := f.adapter(t, "admin", "adminadmin")
	if err := a.Remove(context.Background(), "abc", true); err != nil {
		t.Fatal(err)
	}
	if err := a.Remove(context.Background(), "gone", false); err != nil {
		t.Fatalf("removing something that is not there: %v", err)
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	if len(f.deletes) != 2 || f.deletes[0].Get("hashes") != "abc" || f.deletes[0].Get("deleteFiles") != "true" ||
		f.deletes[1].Get("deleteFiles") != "false" {
		t.Errorf("deletes = %v", f.deletes)
	}
	if len(f.torrents) != 0 {
		t.Error("torrent still there")
	}
}

func TestQBTAddressNormalisation(t *testing.T) {
	for in, want := range map[string]string{
		"http://localhost:8080":         "http://localhost:8080",
		"http://localhost:8080/":        "http://localhost:8080",
		"  http://10.0.0.5:8080/qbt/  ": "http://10.0.0.5:8080/qbt",
		"https://u:p@host/?x=1#f":       "https://host",
	} {
		got, err := normalizeBase(in)
		if err != nil || got != want {
			t.Errorf("normalizeBase(%q) = %q, %v; want %q", in, got, err, want)
		}
	}
	for _, bad := range []string{"", "localhost:8080", "ftp://x", "http://", "javascript:alert(1)", "/relative"} {
		if _, err := normalizeBase(bad); err == nil {
			t.Errorf("normalizeBase(%q) should fail", bad)
		}
	}
}

func TestQBTStopsKnockingAfterTwoRefusedLogins(t *testing.T) {
	// qBittorrent bans an address for an hour after five wrong logins, and the
	// poll runs every five seconds: Marquee must give up on its own after two.
	bothDialects(t, func(t *testing.T, f *fakeQBT) {
		ctx := context.Background()
		a := f.adapter(t, "admin", "wrong")
		for i := 0; i < 6; i++ {
			if _, err := a.List(ctx, "marquee"); !errors.Is(err, ErrAuth) {
				t.Fatalf("call %d: %v, want ErrAuth", i, err)
			}
		}
		if n := f.loginCount(); n != maxAuthFails {
			t.Fatalf("qBittorrent saw %d logins, want %d", n, maxAuthFails)
		}
		if _, err := a.List(ctx, "marquee"); err == nil || !strings.Contains(err.Error(), "stopped trying") {
			t.Errorf("err = %v, want it to say Marquee stopped trying", err)
		}
		// Pressing Test is the owner asking for another go.
		if _, err := a.Test(ctx); !errors.Is(err, ErrAuth) {
			t.Fatal(err)
		}
		if n := f.loginCount(); n != maxAuthFails+1 {
			t.Errorf("after Test: %d logins, want %d", n, maxAuthFails+1)
		}
		// A client with the right password is never held back, however many calls it makes.
		good := f.adapter(t, "admin", "adminadmin")
		for i := 0; i < 5; i++ {
			if _, err := good.List(ctx, "marquee"); err != nil {
				t.Fatal(err)
			}
		}
	})
}
