package downloads

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
)

// fakeDeluge is a Deluge web UI JSON-RPC server (/json).
type fakeDeluge struct {
	t   *testing.T
	srv *httptest.Server

	mu          sync.Mutex
	password    string
	sessions    map[string]bool
	logins      int
	connected   bool
	labelPlugin bool
	labels      map[string]bool
	torrents    map[string]map[string]any
	torrentLbl  map[string]string
	calls       []string
	params      map[string][]json.RawMessage // last params per method
	statusFilt  []map[string]any
	expireNext  bool
	disconnect  bool // drop the daemon connection before the next core call
}

func newFakeDeluge(t *testing.T) *fakeDeluge {
	f := &fakeDeluge{t: t, password: "deluge", sessions: map[string]bool{}, labels: map[string]bool{},
		torrents: map[string]map[string]any{}, torrentLbl: map[string]string{}, params: map[string][]json.RawMessage{}}
	f.srv = httptest.NewServer(http.HandlerFunc(f.handle))
	t.Cleanup(f.srv.Close)
	return f
}

func (f *fakeDeluge) adapter(t *testing.T, password string) *deluge {
	t.Helper()
	a, err := newDeluge(AdapterConfig{Kind: KindDeluge, URL: f.srv.URL, Password: password, HTTP: testHTTP()})
	if err != nil {
		t.Fatal(err)
	}
	return a
}

func (f *fakeDeluge) callList() string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return strings.Join(f.calls, ",")
}

func (f *fakeDeluge) resetCalls() {
	f.mu.Lock()
	f.calls = nil
	f.mu.Unlock()
}

func (f *fakeDeluge) handle(w http.ResponseWriter, r *http.Request) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if r.URL.Path != "/json" || r.Method != http.MethodPost {
		http.NotFound(w, r)
		return
	}
	var req struct {
		Method string            `json:"method"`
		Params []json.RawMessage `json:"params"`
		ID     int               `json:"id"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "bad json", 400)
		return
	}
	f.calls = append(f.calls, req.Method)
	f.params[req.Method] = req.Params

	reply := func(result any, rpcErr string, code int) {
		out := map[string]any{"id": req.ID, "result": result, "error": nil}
		if rpcErr != "" {
			out["error"] = map[string]any{"message": rpcErr, "code": code}
		}
		writeTestJSON(w, out)
	}
	str := func(i int) string {
		var s string
		if i < len(req.Params) {
			_ = json.Unmarshal(req.Params[i], &s)
		}
		return s
	}

	if req.Method == "auth.login" {
		f.logins++
		sid := fmt.Sprintf("sess%d", f.logins)
		http.SetCookie(w, &http.Cookie{Name: "_session_id", Value: sid, Path: "/"})
		if str(0) != f.password {
			reply(false, "", 0)
			return
		}
		f.sessions[sid] = true
		reply(true, "", 0)
		return
	}
	cookie := ""
	if c, err := r.Cookie("_session_id"); err == nil {
		cookie = c.Value
	}
	if f.expireNext {
		f.expireNext = false
		f.sessions = map[string]bool{}
	}
	if !f.sessions[cookie] {
		reply(nil, "Not authenticated", 1)
		return
	}
	switch req.Method {
	case "web.connected":
		reply(f.connected, "", 0)
		return
	case "web.get_hosts":
		reply([][]any{{"hostid1", "127.0.0.1", 58846, "Online"}}, "", 0)
		return
	case "web.connect":
		f.connected = true
		reply([]string{"core.add_torrent_file"}, "", 0)
		return
	}
	if f.disconnect {
		f.disconnect = false
		f.connected = false
	}
	if !f.connected {
		reply(nil, "Not connected to a daemon", 3)
		return
	}

	switch req.Method {
	case "daemon.get_version":
		reply("2.1.1", "", 0)
	case "label.get_labels":
		if !f.labelPlugin {
			reply(nil, "Unknown method", 2)
			return
		}
		out := []string{}
		for l := range f.labels {
			out = append(out, l)
		}
		reply(out, "", 0)
	case "label.add":
		f.labels[str(0)] = true
		reply(nil, "", 0)
	case "label.set_torrent":
		f.torrentLbl[str(0)] = str(1)
		reply(nil, "", 0)
	case "core.add_torrent_file":
		raw, err := base64.StdEncoding.DecodeString(str(1))
		if err != nil {
			reply(nil, "bad base64", 3)
			return
		}
		meta, err := ParseTorrent(raw)
		if err != nil {
			reply(nil, "Not a torrent", 3)
			return
		}
		f.addTorrent(reply, meta.InfoHash, meta.Name)
	case "core.add_torrent_magnet":
		h, err := MagnetInfoHash(str(0))
		if err != nil {
			reply(nil, "Bad magnet", 3)
			return
		}
		f.addTorrent(reply, h, "magnet")
	case "core.get_torrents_status":
		var filt map[string]any
		_ = json.Unmarshal(req.Params[0], &filt)
		f.statusFilt = append(f.statusFilt, filt)
		out := map[string]any{}
		for id, t := range f.torrents {
			if want, ok := filt["label"].(string); ok && f.torrentLbl[id] != want {
				continue
			}
			out[id] = t
		}
		reply(out, "", 0)
	case "core.remove_torrent":
		id := str(0)
		if _, ok := f.torrents[id]; !ok {
			reply(nil, "torrent_id "+id+" not in session.", 3)
			return
		}
		delete(f.torrents, id)
		reply(true, "", 0)
	case "core.pause_torrent", "core.resume_torrent":
		reply(nil, "", 0)
	default:
		reply(nil, "Unknown method "+req.Method, 2)
	}
}

func (f *fakeDeluge) addTorrent(reply func(any, string, int), id, name string) {
	if _, dup := f.torrents[id]; dup {
		reply(nil, "Torrent already in session ("+id+").", 3)
		return
	}
	f.torrents[id] = map[string]any{"name": name, "state": "Downloading", "progress": 0.0, "total_size": 100,
		"total_done": 0, "download_payload_rate": 0, "eta": 0, "save_path": "/dl", "is_finished": false, "message": "OK"}
	reply(id, "", 0)
}

func (f *fakeDeluge) param(method string, i int, dst any) {
	f.t.Helper()
	f.mu.Lock()
	defer f.mu.Unlock()
	ps := f.params[method]
	if i >= len(ps) {
		f.t.Fatalf("%s: no param %d (have %d)", method, i, len(ps))
	}
	if err := json.Unmarshal(ps[i], dst); err != nil {
		f.t.Fatal(err)
	}
}

// ---- tests ---------------------------------------------------------------------

func TestDelugeSignInConnectAndVersion(t *testing.T) {
	f := newFakeDeluge(t)
	v, err := f.adapter(t, "deluge").Test(context.Background())
	if err != nil || v != "2.1.1" {
		t.Fatalf("Test = %q, %v", v, err)
	}
	if got := f.callList(); got != "auth.login,web.connected,web.get_hosts,web.connect,daemon.get_version" {
		t.Errorf("call order = %s", got)
	}

	// Already connected: no host lookup, no connect.
	f = newFakeDeluge(t)
	f.connected = true
	if _, err := f.adapter(t, "deluge").Test(context.Background()); err != nil {
		t.Fatal(err)
	}
	if got := f.callList(); got != "auth.login,web.connected,daemon.get_version" {
		t.Errorf("call order (connected) = %s", got)
	}
}

func TestDelugeWrongPassword(t *testing.T) {
	f := newFakeDeluge(t)
	a := f.adapter(t, "wrong")
	_, err := a.Test(context.Background())
	if !errors.Is(err, ErrAuth) {
		t.Fatalf("err = %v, want ErrAuth", err)
	}
	// The session cookie Deluge hands out even on a failed login must not be
	// mistaken for a session on the next try.
	if _, err := a.Test(context.Background()); !errors.Is(err, ErrAuth) {
		t.Errorf("second try: %v", err)
	}
	if f.logins != 2 {
		t.Errorf("logins = %d, want 2", f.logins)
	}
}

func TestDelugeReloginAndReconnect(t *testing.T) {
	f := newFakeDeluge(t)
	a := f.adapter(t, "deluge")
	ctx := context.Background()
	if _, err := a.Test(ctx); err != nil {
		t.Fatal(err)
	}

	f.mu.Lock()
	f.expireNext = true
	f.mu.Unlock()
	if _, err := a.List(ctx, ""); err != nil {
		t.Fatalf("List after the session expired: %v", err)
	}
	if f.logins != 2 {
		t.Errorf("logins = %d, want 2", f.logins)
	}

	f.mu.Lock()
	f.disconnect = true
	f.mu.Unlock()
	f.resetCalls()
	if _, err := a.List(ctx, ""); err != nil {
		t.Fatalf("List after the daemon connection dropped: %v", err)
	}
	if got := f.callList(); !strings.Contains(got, "web.connect") {
		t.Errorf("did not reconnect: %s", got)
	}
}

func TestDelugeAddFileWithLabel(t *testing.T) {
	f := newFakeDeluge(t)
	f.labelPlugin = true
	a := f.adapter(t, "deluge")
	file := tinyTorrent(tinyInfo("Movie.mkv", 100))
	want, _ := ParseTorrent(file)

	ref, err := a.Add(context.Background(), AddInput{TorrentFile: file, Category: "Marquee Movies", SavePath: "/data/movies", Paused: true})
	if err != nil {
		t.Fatal(err)
	}
	if ref != want.InfoHash {
		t.Errorf("ref = %s, want %s", ref, want.InfoHash)
	}
	var b64 string
	f.param("core.add_torrent_file", 1, &b64)
	if raw, _ := base64.StdEncoding.DecodeString(b64); string(raw) != string(file) {
		t.Error("the file was not sent as base64 of the torrent")
	}
	var opts map[string]any
	f.param("core.add_torrent_file", 2, &opts)
	if opts["add_paused"] != true || opts["download_location"] != "/data/movies" {
		t.Errorf("options = %v", opts)
	}
	// The label plugin got a label that it accepts, and the torrent has it.
	if !f.labels["marquee-movies"] || f.torrentLbl[ref] != "marquee-movies" {
		t.Errorf("labels %v, torrent label %q", f.labels, f.torrentLbl[ref])
	}
	// Listing the category filters by that label.
	f.torrents["other"] = map[string]any{"name": "someone else's", "state": "Seeding"}
	items, err := a.List(context.Background(), "Marquee Movies")
	if err != nil {
		t.Fatal(err)
	}
	if len(items) != 1 || items[0].Ref != ref {
		t.Errorf("items = %+v", items)
	}
	if got := f.statusFilt[len(f.statusFilt)-1]["label"]; got != "marquee-movies" {
		t.Errorf("filter = %v", f.statusFilt)
	}
}

func TestDelugeWithoutTheLabelPlugin(t *testing.T) {
	f := newFakeDeluge(t)
	a := f.adapter(t, "deluge")
	ref, err := a.Add(context.Background(), AddInput{TorrentFile: tinyTorrent(tinyInfo("a.mkv", 1)), Category: "marquee"})
	if err != nil {
		t.Fatalf("adding without the Label plugin must still work: %v", err)
	}
	if strings.Contains(f.callList(), "label.add") || strings.Contains(f.callList(), "label.set_torrent") {
		t.Errorf("label calls without the plugin: %s", f.callList())
	}
	// With no labels, List cannot filter: it returns everything, and the poll
	// follows its own refs.
	f.torrents["other"] = map[string]any{"name": "other", "state": "Seeding", "progress": 100.0}
	items, err := a.List(context.Background(), "marquee")
	if err != nil || len(items) != 2 {
		t.Fatalf("items = %+v, %v", items, err)
	}
	if _, has := f.statusFilt[len(f.statusFilt)-1]["label"]; has {
		t.Error("filtered by label although the plugin is off")
	}
	_ = ref
}

func TestDelugeAddMagnetAndDuplicate(t *testing.T) {
	f := newFakeDeluge(t)
	a := f.adapter(t, "deluge")
	ctx := context.Background()
	ref, err := a.Add(ctx, AddInput{MagnetURL: "magnet:?xt=urn:btih:" + bbbBase32 + "&dn=x", Category: "marquee"})
	if err != nil || ref != bbbHex {
		t.Fatalf("magnet: %q, %v", ref, err)
	}
	// Adding the same one again: Deluge says "already in session"; that is fine.
	ref, err = a.Add(ctx, AddInput{MagnetURL: "magnet:?xt=urn:btih:" + bbbHex, Category: "marquee"})
	if err != nil || ref != bbbHex {
		t.Errorf("duplicate: %q, %v", ref, err)
	}
	// Rejections.
	if _, err := a.Add(ctx, AddInput{TorrentFile: []byte("junk")}); !errors.Is(err, ErrRejected) {
		t.Errorf("junk file: %v", err)
	}
	if _, err := a.Add(ctx, AddInput{}); !errors.Is(err, ErrRejected) {
		t.Errorf("nothing: %v", err)
	}
}

func TestDelugeStateMapping(t *testing.T) {
	f := newFakeDeluge(t)
	f.connected = true
	cases := []struct {
		state    string
		progress float64
		finished bool // is_finished
		want     string
		done     bool
	}{
		{"Downloading", 40, false, StateDownloading, false},
		{"Queued", 0, false, StateQueued, false},
		{"Checking", 10, false, StateQueued, false},
		{"Allocating", 0, false, StateQueued, false},
		{"Paused", 40, false, StatePaused, false},
		{"Moving", 100, true, StateDownloading, false},
		{"Seeding", 100, true, StateCompleted, true},
		{"Paused", 100, true, StateCompleted, true},
		{"Queued", 100, true, StateCompleted, true},
		{"Error", 20, false, StateFailed, false},
	}
	for i, c := range cases {
		f.torrents[fmt.Sprintf("%040x", i)] = map[string]any{
			"name": c.state, "state": c.state, "progress": c.progress, "total_size": 2000, "total_done": 800,
			"download_payload_rate": 4096.0, "eta": 30.0, "save_path": "/dl", "is_finished": c.finished, "message": "disk is full",
		}
	}
	items, err := f.adapter(t, "deluge").List(context.Background(), "")
	if err != nil {
		t.Fatal(err)
	}
	by := map[string]Item{}
	for _, it := range items {
		by[it.Ref] = it
	}
	for i, c := range cases {
		it, ok := by[fmt.Sprintf("%040x", i)]
		if !ok {
			t.Fatalf("case %d missing", i)
		}
		if it.State != c.want || it.Finished != c.done {
			t.Errorf("%s/%v/%v: state %q finished %v; want %q %v", c.state, c.progress, c.finished, it.State, it.Finished, c.want, c.done)
		}
		if it.ContentPath != "/dl/"+c.state {
			t.Errorf("content path = %q", it.ContentPath)
		}
		if !c.done && it.State != StateFailed && (it.Progress != c.progress/100 || it.Size != 2000 || it.Done != 800 || it.SpeedBps != 4096 || it.ETA != 30) {
			t.Errorf("%s: numbers = %+v", c.state, it)
		}
		if c.want == StateFailed && !strings.Contains(it.Error, "disk is full") {
			t.Errorf("error text = %q", it.Error)
		}
	}
}

func TestDelugeRemovePauseResume(t *testing.T) {
	f := newFakeDeluge(t)
	a := f.adapter(t, "deluge")
	ctx := context.Background()
	ref, err := a.Add(ctx, AddInput{MagnetURL: "magnet:?xt=urn:btih:" + bbbHex, Category: "marquee"})
	if err != nil {
		t.Fatal(err)
	}
	if err := a.Pause(ctx, ref); err != nil {
		t.Fatal(err)
	}
	if err := a.Resume(ctx, ref); err != nil {
		t.Fatal(err)
	}
	var id string
	f.param("core.pause_torrent", 0, &id)
	if id != ref {
		t.Errorf("paused %q", id)
	}
	if err := a.Remove(ctx, ref, true); err != nil {
		t.Fatal(err)
	}
	var del bool
	f.param("core.remove_torrent", 1, &del)
	if !del {
		t.Error("deleteData was not passed on")
	}
	if len(f.torrents) != 0 {
		t.Error("torrent not removed")
	}
	// Gone already: not an error.
	if err := a.Remove(ctx, ref, false); err != nil {
		t.Errorf("removing a missing torrent: %v", err)
	}
}

func TestDelugeNoDaemonHost(t *testing.T) {
	f := newFakeDeluge(t)
	// A web UI with no host configured.
	f.srv.Config.Handler = http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var req struct {
			Method string `json:"method"`
			ID     int    `json:"id"`
		}
		_ = json.NewDecoder(r.Body).Decode(&req)
		res := any(true)
		switch req.Method {
		case "web.connected":
			res = false
		case "web.get_hosts":
			res = [][]any{}
		}
		http.SetCookie(w, &http.Cookie{Name: "_session_id", Value: "x"})
		writeTestJSON(w, map[string]any{"id": req.ID, "result": res, "error": nil})
	})
	_, err := f.adapter(t, "deluge").Test(context.Background())
	if err == nil || !strings.Contains(err.Error(), "no daemon") {
		t.Errorf("err = %v", err)
	}
}

func TestDelugeWrongKindOfServer(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte("<html>not deluge</html>"))
	}))
	defer srv.Close()
	a, _ := newDeluge(AdapterConfig{URL: srv.URL, Password: "x", HTTP: testHTTP()})
	if _, err := a.Test(context.Background()); err == nil || !strings.Contains(err.Error(), "web UI") {
		t.Errorf("err = %v", err)
	}
}
