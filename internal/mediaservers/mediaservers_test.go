package mediaservers

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/url"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/BaileyPillon/marquee/internal/core/db"
	"github.com/BaileyPillon/marquee/internal/core/outbound"
	"github.com/BaileyPillon/marquee/internal/core/pathmap"
	"github.com/BaileyPillon/marquee/internal/core/secrets"
	"github.com/BaileyPillon/marquee/internal/settings"
	"github.com/BaileyPillon/marquee/internal/titles"
)

func bg() context.Context { return context.Background() }

func quietLog() *slog.Logger { return slog.New(slog.NewTextHandler(io.Discard, nil)) }

func str(s string) *string { return &s }

const token = "super-secret-token-abcdef123456"

type env struct {
	t   *testing.T
	db  *sql.DB
	svc *Service
}

func newEnv(t *testing.T) *env {
	t.Helper()
	dir := t.TempDir()
	d, err := db.Open(filepath.Join(dir, db.MainFile))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { d.Close() })
	if _, err := db.Migrate(bg(), d, db.SetMain); err != nil {
		t.Fatal(err)
	}
	box, err := secrets.LoadOrCreate(filepath.Join(dir, "marquee.key"), quietLog())
	if err != nil {
		t.Fatal(err)
	}
	hc := outbound.New("test", outbound.Options{Logger: quietLog(), HostRPS: 100000, HostBurst: 100000, MaxRetries: -1})
	return &env{t: t, db: d, svc: New(d, Options{Logger: quietLog(), HTTP: hc, Box: box})}
}

func (e *env) add(name, kind, url string, mod ...func(*Input)) View {
	e.t.Helper()
	in := Input{Name: str(name), Kind: str(kind), URL: str(url), Token: str(token)}
	for _, m := range mod {
		m(&in)
	}
	v, err := e.svc.Add(bg(), in)
	if err != nil {
		e.t.Fatalf("Add(%s): %v", name, err)
	}
	return v
}

func withMap(rules ...pathmap.Rule) func(*Input) {
	return func(in *Input) { in.PathMap = &rules }
}

// ---- a fake server --------------------------------------------------------------

type call struct {
	method, path string
	query        url.Values
	header       http.Header
	body         string
}

type fake struct {
	srv     *httptest.Server
	mu      sync.Mutex
	calls   []call
	handler func(w http.ResponseWriter, c call)
}

func newFake(t *testing.T, h func(w http.ResponseWriter, c call)) *fake {
	f := &fake{handler: h}
	f.srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		c := call{method: r.Method, path: r.URL.Path, query: r.URL.Query(), header: r.Header.Clone(), body: string(b)}
		f.mu.Lock()
		f.calls = append(f.calls, c)
		f.mu.Unlock()
		f.handler(w, c)
	}))
	t.Cleanup(f.srv.Close)
	return f
}

func (f *fake) recorded() []call {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]call(nil), f.calls...)
}

func (f *fake) pathsCalled() []string {
	var out []string
	for _, c := range f.recorded() {
		out = append(out, c.method+" "+c.path)
	}
	return out
}

func jsonReply(w http.ResponseWriter, v string) {
	w.Header().Set("Content-Type", "application/json")
	_, _ = io.WriteString(w, v)
}

// jellyfinFake answers like a Jellyfin that knows one token.
func jellyfinFake(t *testing.T, items string, updatedStatus int) *fake {
	return newFake(t, func(w http.ResponseWriter, c call) {
		if c.header.Get("X-Emby-Token") != token && !strings.Contains(c.header.Get("Authorization"), token) {
			http.Error(w, "no", http.StatusUnauthorized)
			return
		}
		switch c.path {
		case "/System/Info":
			jsonReply(w, `{"Version":"12.4.0","ServerName":"Jelly","Id":"server-id-1"}`)
		case "/Library/VirtualFolders":
			jsonReply(w, `[{"Name":"Movies","ItemId":"lib-1","Locations":["/media/movies"]},{"Name":"TV","ItemId":"lib-2","Locations":["/media/tv","/media/anime"]}]`)
		case "/Library/Media/Updated":
			w.WriteHeader(updatedStatus)
		case "/Library/Refresh":
			w.WriteHeader(http.StatusNoContent)
		case "/Items":
			jsonReply(w, items)
		default:
			if c.method == http.MethodPost && strings.HasPrefix(c.path, "/Items/") && strings.HasSuffix(c.path, "/Refresh") {
				w.WriteHeader(http.StatusNoContent)
				return
			}
			http.NotFound(w, nil)
		}
	})
}

// ---- adding and editing ------------------------------------------------------------

func TestAddListUpdateDeleteAndMasking(t *testing.T) {
	e := newEnv(t)
	v := e.add("Living room", "jellyfin", "http://192.168.1.10:8096/", withMap(pathmap.Rule{From: `M:\media`, To: "/media"}))
	if v.URL != "http://192.168.1.10:8096" || v.Kind != "jellyfin" || !v.Enabled {
		t.Errorf("view = %+v", v)
	}
	if v.Token == token || !strings.HasPrefix(v.Token, settings.MaskPrefix) || !strings.HasSuffix(v.Token, token[len(token)-4:]) {
		t.Errorf("the token should come back masked, got %q", v.Token)
	}
	raw, _ := json.Marshal(v)
	if strings.Contains(string(raw), token) {
		t.Errorf("the token leaked into JSON: %s", raw)
	}
	var sealed string
	if err := e.db.QueryRow(`SELECT token_sealed FROM media_servers WHERE id = ?`, v.ID).Scan(&sealed); err != nil || sealed == "" || strings.Contains(sealed, token) {
		t.Errorf("the token must be stored sealed: %q, %v", sealed, err)
	}
	if len(v.PathMap) != 1 || v.PathMap[0].To != "/media" {
		t.Errorf("path map = %+v", v.PathMap)
	}

	// Sending the masked token back keeps the stored one.
	up, err := e.svc.Update(bg(), v.ID, Input{Name: str("Lounge"), Token: &v.Token})
	if err != nil || up.Name != "Lounge" || up.Token != v.Token {
		t.Fatalf("Update = %+v, %v", up, err)
	}
	var again string
	_ = e.db.QueryRow(`SELECT token_sealed FROM media_servers WHERE id = ?`, v.ID).Scan(&again)
	if again != sealed {
		t.Error("a masked token must keep the stored secret")
	}
	// Changing the address without typing the token again is refused (the token must not go to a new host).
	if _, err := e.svc.Update(bg(), v.ID, Input{URL: str("http://evil.example:8096")}); err == nil {
		t.Error("a saved token must not be sent to a different host")
	}
	// ...but typing it again is fine, and clearing works.
	if _, err := e.svc.Update(bg(), v.ID, Input{URL: str("http://other.example:8096"), Token: str("a brand new token 123456")}); err != nil {
		t.Errorf("new address with a new token: %v", err)
	}
	cleared, err := e.svc.Update(bg(), v.ID, Input{Token: str("")})
	if err != nil || cleared.Token != "" {
		t.Errorf("clearing the token: %+v, %v", cleared, err)
	}
	// The path map can be replaced and emptied.
	empty := []pathmap.Rule{}
	if up, err := e.svc.Update(bg(), v.ID, Input{PathMap: &empty}); err != nil || len(up.PathMap) != 0 {
		t.Errorf("clearing the path map: %+v, %v", up, err)
	}

	if _, err := e.svc.Update(bg(), v.ID, Input{Kind: str("plex")}); err == nil {
		t.Error("the kind cannot change")
	}
	if list, err := e.svc.List(bg()); err != nil || len(list) != 1 {
		t.Errorf("List = %v, %v", list, err)
	}
	if err := e.svc.Delete(bg(), v.ID); err != nil {
		t.Fatal(err)
	}
	if err := e.svc.Delete(bg(), v.ID); !errors.Is(err, ErrNotFound) {
		t.Errorf("second delete: %v", err)
	}
	if _, err := e.svc.Get(bg(), v.ID); !errors.Is(err, ErrNotFound) {
		t.Errorf("Get after delete: %v", err)
	}
}

func TestAddValidation(t *testing.T) {
	e := newEnv(t)
	e.add("One", "plex", "http://plex.local:32400")
	bad := []Input{
		{Name: str("x"), URL: str("http://a:1")}, // no kind
		{Name: str("x"), Kind: str("kodi"), URL: str("http://a:1")},
		{Kind: str("emby"), URL: str("http://a:1")}, // no name
		{Name: str("x"), Kind: str("emby")},         // no address
		{Name: str("x"), Kind: str("emby"), URL: str("ftp://a")},
		{Name: str("x"), Kind: str("emby"), URL: str("not a url")},
		{Name: str("x"), Kind: str("emby"), URL: str("http://a:1"), Token: str("bad\x00token")},
	}
	for i, in := range bad {
		_, err := e.svc.Add(bg(), in)
		var ie *InvalidError
		if !errors.As(err, &ie) {
			t.Errorf("case %d: want InvalidError, got %v", i, err)
		}
	}
	if _, err := e.svc.Add(bg(), Input{Name: str("ONE"), Kind: str("emby"), URL: str("http://a:1")}); !errors.Is(err, ErrNameTaken) {
		t.Errorf("a name is unique without regard to case: %v", err)
	}
	badRules := []pathmap.Rule{{From: "/a"}}
	if _, err := e.svc.Add(bg(), Input{Name: str("Two"), Kind: str("emby"), URL: str("http://a:1"), PathMap: &badRules}); err == nil {
		t.Error("a path rule with one side should be refused")
	}
}

// ---- Jellyfin and Emby: telling them -----------------------------------------------------

func TestJellyfinGetsMappedPathsInOneUpdate(t *testing.T) {
	e := newEnv(t)
	f := jellyfinFake(t, `{"Items":[]}`, http.StatusNoContent)
	e.add("Jelly", "jellyfin", f.srv.URL, withMap(
		pathmap.Rule{From: `M:\MarqueeTest\media`, To: "/media"}))

	res := e.svc.Notify(bg(), Event{Kind: EventCreated, Paths: []string{
		`M:\MarqueeTest\media\movies\Night of the Living Dead (1968)\Night of the Living Dead (1968) Bluray-1080p.mkv`,
		`M:\MarqueeTest\media\tv\Pioneer One (2010)\Season 01\Pioneer One - S01E01 - Earthfall Bluray-1080p.mkv`,
		`D:\elsewhere\unmapped.mkv`,
	}})
	if len(res) != 1 || !res[0].OK {
		t.Fatalf("results = %+v", res)
	}
	calls := f.recorded()
	if len(calls) < 1 || calls[0].method != http.MethodPost || calls[0].path != "/Library/Media/Updated" {
		t.Fatalf("calls = %v", f.pathsCalled())
	}
	// Then the two libraries that hold the mapped paths are asked to rescan (the unmapped path is in neither).
	if got := f.pathsCalled(); !reflect.DeepEqual(got, []string{"POST /Library/Media/Updated", "GET /Library/VirtualFolders",
		"POST /Items/lib-1/Refresh", "POST /Items/lib-2/Refresh"}) {
		t.Errorf("calls = %v", got)
	}
	var body struct {
		Updates []map[string]string
	}
	if err := json.Unmarshal([]byte(calls[0].body), &body); err != nil {
		t.Fatal(err)
	}
	want := []map[string]string{
		{"Path": "/media/movies/Night of the Living Dead (1968)/Night of the Living Dead (1968) Bluray-1080p.mkv", "UpdateType": "Created"},
		{"Path": "/media/tv/Pioneer One (2010)/Season 01/Pioneer One - S01E01 - Earthfall Bluray-1080p.mkv", "UpdateType": "Created"},
		{"Path": `D:\elsewhere\unmapped.mkv`, "UpdateType": "Created"},
	}
	if !reflect.DeepEqual(body.Updates, want) {
		t.Errorf("body = %s", calls[0].body)
	}
	if calls[0].header.Get("X-Emby-Token") != token || !strings.Contains(calls[0].header.Get("Authorization"), token) {
		t.Errorf("the token should be sent as X-Emby-Token and as MediaBrowser Token=: %v", calls[0].header)
	}
	if got := res[0].Sent[0]; got != want[0]["Path"] {
		t.Errorf("Sent = %v", res[0].Sent)
	}
	list, _ := e.svc.List(bg())
	if list[0].LastOKAt == nil || list[0].LastError != "" {
		t.Errorf("a good call should be recorded: %+v", list[0])
	}
}

func TestDeletedAndRenamedEvents(t *testing.T) {
	e := newEnv(t)
	f := jellyfinFake(t, `{}`, http.StatusNoContent)
	e.add("Jelly", "jellyfin", f.srv.URL)

	e.svc.Notify(bg(), Event{Kind: EventDeleted, Paths: []string{"/m/a.mkv"}})
	e.svc.NotifyRenamed(bg(), []string{"/m/old.mkv"}, []string{"/m/new.mkv"})
	var types []string
	for _, c := range f.recorded() {
		var b struct{ Updates []map[string]string }
		_ = json.Unmarshal([]byte(c.body), &b)
		for _, u := range b.Updates {
			types = append(types, u["UpdateType"]+":"+u["Path"])
		}
	}
	want := []string{"Deleted:/m/a.mkv", "Deleted:/m/old.mkv", "Created:/m/new.mkv"}
	if !reflect.DeepEqual(types, want) {
		t.Errorf("events = %v, want %v", types, want)
	}
	if got := e.svc.Notify(bg(), Event{Kind: "exploded"}); got != nil {
		t.Errorf("an unknown event kind does nothing, got %v", got)
	}
}

// Found live on Jellyfin 12.4: Media/Updated for a film in a folder the server has never
// seen answers 204 and does nothing. The library that holds the path must be asked to rescan.
func TestJellyfinRescansTheLibraryThatHoldsANewFolder(t *testing.T) {
	e := newEnv(t)
	f := jellyfinFake(t, `{}`, http.StatusNoContent)
	e.add("Jelly", "jellyfin", f.srv.URL)
	res := e.svc.Notify(bg(), Event{Kind: EventCreated, Paths: []string{"/media/movies/Night of the Living Dead (1968)/Night of the Living Dead (1968) Bluray-1080p.mkv"}})
	if len(res) != 1 || !res[0].OK {
		t.Fatalf("results = %+v", res)
	}
	if got := f.pathsCalled(); !reflect.DeepEqual(got, []string{"POST /Library/Media/Updated", "GET /Library/VirtualFolders", "POST /Items/lib-1/Refresh"}) {
		t.Errorf("calls = %v (only the Movies library holds that path)", got)
	}
	q := f.recorded()[2].query
	if q.Get("Recursive") != "true" || q.Get("MetadataRefreshMode") != "Default" || q.Get("ReplaceAllMetadata") != "false" || q.Get("ReplaceAllImages") != "false" {
		t.Errorf("refresh query = %v", q)
	}

	// A deleted file cleans up the same way.
	res = e.svc.Notify(bg(), Event{Kind: EventDeleted, Paths: []string{"/media/tv/Show/Season 01/a.mkv"}})
	if !res[0].OK {
		t.Fatalf("results = %+v", res)
	}
	if got := f.pathsCalled(); got[len(got)-1] != "POST /Items/lib-2/Refresh" {
		t.Errorf("calls = %v", got)
	}
	// One import of many files asks each library once.
	f2 := jellyfinFake(t, `{}`, http.StatusNoContent)
	e2 := newEnv(t)
	e2.add("Jelly", "jellyfin", f2.srv.URL)
	e2.svc.Notify(bg(), Event{Kind: EventCreated, Paths: []string{"/media/tv/S/1.mkv", "/media/tv/S/2.mkv", "/media/anime/X/1.mkv", "/media/movies/M/m.mkv"}})
	counts := map[string]int{}
	for _, p := range f2.pathsCalled() {
		counts[p]++
	}
	if counts["POST /Items/lib-2/Refresh"] != 1 || counts["POST /Items/lib-1/Refresh"] != 1 || counts["POST /Library/Media/Updated"] != 1 {
		t.Errorf("calls = %v", f2.pathsCalled())
	}
}

func TestJellyfinSaysSoWhenNoLibraryHoldsThePath(t *testing.T) {
	e := newEnv(t)
	f := jellyfinFake(t, `{}`, http.StatusNoContent)
	e.add("Jelly", "jellyfin", f.srv.URL)
	res := e.svc.Notify(bg(), Event{Kind: EventCreated, Paths: []string{`M:\MarqueeTest\media\movies\A\a.mkv`}}) // no path rule: the server sees /media/movies
	if len(res) != 1 || res[0].OK || !strings.Contains(res[0].Error, "path rules") {
		t.Fatalf("results = %+v", res)
	}
	if got := f.pathsCalled(); got[len(got)-1] != "POST /Library/Refresh" {
		t.Errorf("a full rescan is still asked for: %v", got)
	}
	v, _ := e.svc.List(bg())
	if !strings.Contains(v[0].LastError, "path rules") {
		t.Errorf("the owner should see it on the server: %+v", v[0])
	}
}

func TestJellyfinLibraryRefreshFailureIsReported(t *testing.T) {
	e := newEnv(t)
	f := newFake(t, func(w http.ResponseWriter, c call) {
		switch {
		case c.path == "/Library/Media/Updated":
			w.WriteHeader(http.StatusNoContent)
		case c.path == "/Library/VirtualFolders":
			jsonReply(w, `[{"Name":"Movies","ItemId":"lib-1","Locations":["/media/movies"]}]`)
		default:
			http.Error(w, "boom", http.StatusInternalServerError)
		}
	})
	e.add("Jelly", "jellyfin", f.srv.URL)
	res := e.svc.Notify(bg(), Event{Kind: EventCreated, Paths: []string{"/media/movies/A/a.mkv"}})
	if len(res) != 1 || res[0].OK || res[0].Error == "" {
		t.Errorf("results = %+v", res)
	}
}

func TestJellyfinFallsBackToARescan(t *testing.T) {
	e := newEnv(t)
	f := jellyfinFake(t, `{}`, http.StatusNotFound)
	e.add("Jelly", "jellyfin", f.srv.URL)
	res := e.svc.Notify(bg(), Event{Kind: EventCreated, Paths: []string{"/m/a.mkv"}})
	if !res[0].OK {
		t.Fatalf("the fallback should have worked: %+v", res)
	}
	if got := f.pathsCalled(); !reflect.DeepEqual(got, []string{"POST /Library/Media/Updated", "POST /Library/Refresh"}) {
		t.Errorf("calls = %v", got)
	}
}

func TestAServerThatRefusesTheTokenIsRecordedAndNotRetried(t *testing.T) {
	e := newEnv(t)
	f := jellyfinFake(t, `{}`, http.StatusNoContent)
	v := e.add("Jelly", "jellyfin", f.srv.URL, func(in *Input) { in.Token = str("the wrong token entirely") })
	res := e.svc.Notify(bg(), Event{Kind: EventCreated, Paths: []string{"/m/a.mkv"}})
	if len(res) != 1 || res[0].OK || !strings.Contains(res[0].Error, "refused the token") {
		t.Fatalf("results = %+v", res)
	}
	if strings.Contains(res[0].Error, "wrong token") || strings.Contains(res[0].Error, f.srv.URL) {
		t.Errorf("an error must not carry the token or the address: %q", res[0].Error)
	}
	if got := f.pathsCalled(); len(got) != 1 {
		t.Errorf("a refused token is not a reason to try the rescan: %v", got)
	}
	got, _ := e.svc.Get(bg(), v.ID)
	if got.LastError == "" || got.LastOKAt != nil {
		t.Errorf("the failure should be on the server's row: %+v", got)
	}
}

func TestADownServerIsNeverFatalAndOthersAreStillTold(t *testing.T) {
	e := newEnv(t)
	dead := httptest.NewServer(http.NotFoundHandler())
	deadURL := dead.URL
	dead.Close()
	e.add("Dead", "jellyfin", deadURL)
	alive := jellyfinFake(t, `{}`, http.StatusNoContent)
	e.add("Alive", "emby", alive.srv.URL)
	off := jellyfinFake(t, `{}`, http.StatusNoContent)
	e.add("Off", "jellyfin", off.srv.URL, func(in *Input) { in.Enabled = new(bool) })

	res := e.svc.Notify(bg(), Event{Kind: EventCreated, Paths: []string{"/media/movies/a.mkv"}})
	if len(res) != 2 {
		t.Fatalf("only the two enabled servers are told: %+v", res)
	}
	var ok, bad int
	for _, r := range res {
		if r.OK {
			ok++
		} else if strings.Contains(r.Error, "could not reach") {
			bad++
		}
	}
	if ok != 1 || bad != 1 {
		t.Errorf("results = %+v", res)
	}
	if len(off.recorded()) != 0 {
		t.Error("a disabled server must not be called")
	}
	list, _ := e.svc.List(bg())
	if list[0].LastError == "" {
		t.Errorf("the dead server's row should say why: %+v", list[0])
	}
	if got := New(e.db, Options{HTTP: nil, Box: nil}); got == nil {
		t.Error("New with no HTTP client should still build")
	}
}

func TestNotifyWithNoServersDoesNothing(t *testing.T) {
	e := newEnv(t)
	if got := e.svc.Notify(bg(), Event{Kind: EventCreated, Paths: []string{"/x"}}); got != nil {
		t.Errorf("got %v", got)
	}
}

// ---- Plex -----------------------------------------------------------------------------------------

const plexSections = `{"MediaContainer":{"Directory":[
  {"key":"1","title":"Movies","type":"movie","Location":[{"id":1,"path":"/data/movies"}]},
  {"key":"2","title":"TV","type":"show","Location":[{"id":2,"path":"/data/tv"}]},
  {"key":"3","title":"Classics","type":"movie","Location":[{"id":3,"path":"/data/movies/classics"}]}
]}}`

func plexFake(t *testing.T, all map[string]string) *fake {
	return newFake(t, func(w http.ResponseWriter, c call) {
		if c.path != "/identity" && c.header.Get("X-Plex-Token") != token {
			http.Error(w, "no", http.StatusUnauthorized)
			return
		}
		switch {
		case c.path == "/identity":
			jsonReply(w, `{"MediaContainer":{"machineIdentifier":"abc123machine","version":"1.41.0.8992"}}`)
		case c.path == "/library/sections":
			jsonReply(w, plexSections)
		case strings.HasSuffix(c.path, "/refresh"):
			w.WriteHeader(http.StatusOK)
		case strings.HasSuffix(c.path, "/all"):
			key := strings.Split(c.path, "/")[3]
			if body, ok := all[key]; ok {
				jsonReply(w, body)
				return
			}
			jsonReply(w, `{"MediaContainer":{"size":0}}`)
		default:
			http.NotFound(w, nil)
		}
	})
}

func TestPlexRefreshesTheSectionAndFolderOfEachPath(t *testing.T) {
	e := newEnv(t)
	f := plexFake(t, nil)
	e.add("Plex", "plex", f.srv.URL, withMap(
		pathmap.Rule{From: `M:\MarqueeTest\media\movies`, To: "/data/movies"},
		pathmap.Rule{From: `M:\MarqueeTest\media\tv`, To: "/data/tv"}))

	res := e.svc.Notify(bg(), Event{Kind: EventCreated, Paths: []string{
		`M:\MarqueeTest\media\movies\Night of the Living Dead (1968)\Night of the Living Dead (1968) Bluray-1080p.mkv`,
		`M:\MarqueeTest\media\movies\classics\Nosferatu (1922)\Nosferatu (1922) DVD.mkv`,
		`M:\MarqueeTest\media\tv\Pioneer One (2010)\Season 01\Pioneer One - S01E01.mkv`,
		`M:\MarqueeTest\media\movies\Night of the Living Dead (1968)\Night of the Living Dead (1968) Bluray-1080p-extra.mkv`,
	}})
	if len(res) != 1 || !res[0].OK {
		t.Fatalf("results = %+v", res)
	}
	got := map[string]bool{}
	for _, c := range f.recorded() {
		if strings.HasSuffix(c.path, "/refresh") {
			if c.method != http.MethodGet {
				t.Errorf("a Plex refresh is a GET, got %s", c.method)
			}
			got[c.path+" path="+c.query.Get("path")] = true
		}
	}
	want := map[string]bool{
		"/library/sections/1/refresh path=/data/movies/Night of the Living Dead (1968)": true,
		"/library/sections/3/refresh path=/data/movies/classics/Nosferatu (1922)":       true, // the deeper section wins
		"/library/sections/2/refresh path=/data/tv/Pioneer One (2010)/Season 01":        true,
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("refreshes = %v\nwant       %v", got, want)
	}
}

func TestPlexRefreshesEverythingForAPathNoSectionHolds(t *testing.T) {
	e := newEnv(t)
	f := plexFake(t, nil)
	e.add("Plex", "plex", f.srv.URL)
	e.svc.Notify(bg(), Event{Kind: EventDeleted, Paths: []string{"/somewhere/else/a.mkv"}})
	var keys []string
	for _, c := range f.recorded() {
		if strings.HasSuffix(c.path, "/refresh") {
			keys = append(keys, strings.Split(c.path, "/")[3])
			if c.query.Get("path") != "" {
				t.Errorf("a full refresh has no path: %v", c.query)
			}
		}
	}
	if !reflect.DeepEqual(keys, []string{"1", "2", "3"}) {
		t.Errorf("sections refreshed = %v", keys)
	}
}

// ---- testing a server -------------------------------------------------------------------------------

func TestTestReportsVersionAndLibraries(t *testing.T) {
	e := newEnv(t)
	jf := jellyfinFake(t, `{}`, http.StatusNoContent)
	px := plexFake(t, nil)
	j := e.add("Jelly", "jellyfin", jf.srv.URL)
	p := e.add("Plex", "plex", px.srv.URL)
	bad := e.add("Wrong", "plex", px.srv.URL+"/", func(in *Input) { in.Name = str("Wrong token"); in.Token = str("nope nope nope") })

	res, err := e.svc.Test(bg(), j.ID)
	if err != nil || !res.OK || res.Version != "12.4.0" || res.Error != "" {
		t.Fatalf("Jellyfin test = %+v, %v", res, err)
	}
	if len(res.Libraries) != 2 || res.Libraries[1].Name != "TV" || res.Libraries[1].ID != "lib-2" ||
		!reflect.DeepEqual(res.Libraries[1].Paths, []string{"/media/tv", "/media/anime"}) {
		t.Errorf("libraries = %+v", res.Libraries)
	}
	res, err = e.svc.Test(bg(), p.ID)
	if err != nil || !res.OK || res.Version != "1.41.0.8992" || len(res.Libraries) != 3 || res.Libraries[0].Paths[0] != "/data/movies" {
		t.Errorf("Plex test = %+v, %v", res, err)
	}
	res, err = e.svc.Test(bg(), bad.ID)
	if err != nil || res.OK || !strings.Contains(res.Error, "refused the token") || res.Libraries == nil {
		t.Errorf("a wrong token should give ok=false with a plain error and an empty list: %+v, %v", res, err)
	}
	if _, err := e.svc.Test(bg(), 999); !errors.Is(err, ErrNotFound) {
		t.Errorf("unknown server: %v", err)
	}
	got, _ := e.svc.Get(bg(), bad.ID)
	if got.LastError == "" {
		t.Error("a failed test should be recorded")
	}
}

// ---- open on the server --------------------------------------------------------------------------------

func movieTitle() titles.Title {
	return titles.Title{ID: 7, Kind: titles.KindMovie, Name: "Night of the Living Dead", Year: 1968, TMDBID: 10331, IMDbID: "tt0063350"}
}

func TestOpenURLMatchesByProviderIDBeforeName(t *testing.T) {
	e := newEnv(t)
	f := jellyfinFake(t, `{"Items":[
	  {"Id":"wrong","Name":"Night of the Living Dead","Type":"Movie","ProductionYear":1968,"ProviderIds":{"Tmdb":"999"}},
	  {"Id":"remake","Name":"Night of the Living Dead 3D","Type":"Movie","ProductionYear":2006,"ProviderIds":{"Imdb":"tt0000001"}},
	  {"Id":"right","Name":"Night of the Living Dead (restored)","Type":"Movie","ProductionYear":1968,"ProviderIds":{"Imdb":"tt0063350","Tmdb":"10331"}}
	]}`, http.StatusNoContent)
	e.add("Jelly", "jellyfin", f.srv.URL+"/")

	res, err := e.svc.OpenURL(bg(), movieTitle())
	if err != nil {
		t.Fatal(err)
	}
	if res.URL != f.srv.URL+"/web/#/details?id=right" || res.Server.Name != "Jelly" || res.Server.Kind != "jellyfin" {
		t.Errorf("OpenURL = %+v", res)
	}
	q := f.recorded()[0].query
	if q.Get("searchTerm") != "Night of the Living Dead" || q.Get("Recursive") != "true" ||
		q.Get("IncludeItemTypes") != "Movie,Series,MusicArtist" || q.Get("Fields") != "ProviderIds,ProductionYear,Path" {
		t.Errorf("the search was %v", q)
	}
}

func TestOpenURLFallsBackToNameAndYear(t *testing.T) {
	e := newEnv(t)
	f := jellyfinFake(t, `{"Items":[
	  {"Id":"other-year","Name":"Night of the Living Dead","Type":"Movie","ProductionYear":1990},
	  {"Id":"a-series","Name":"Night of the Living Dead","Type":"Series","ProductionYear":1968},
	  {"Id":"hit","Name":"night of the LIVING dead","Type":"Movie","ProductionYear":1969}
	]}`, http.StatusNoContent)
	e.add("Jelly", "jellyfin", f.srv.URL)
	res, err := e.svc.OpenURL(bg(), movieTitle())
	if err != nil || !strings.HasSuffix(res.URL, "id=hit") {
		t.Fatalf("OpenURL = %+v, %v (same name within a year of 1968, and a film, not a series)", res, err)
	}
}

func TestOpenURLNotOnAnyServer(t *testing.T) {
	e := newEnv(t)
	if _, err := e.svc.OpenURL(bg(), movieTitle()); !errors.Is(err, ErrNotOnServer) {
		t.Errorf("no servers: %v", err)
	}
	f := jellyfinFake(t, `{"Items":[{"Id":"x","Name":"Something Else","Type":"Movie","ProductionYear":1968}]}`, http.StatusNoContent)
	e.add("Jelly", "jellyfin", f.srv.URL)
	if _, err := e.svc.OpenURL(bg(), movieTitle()); !errors.Is(err, ErrNotOnServer) {
		t.Errorf("not there: %v", err)
	}
}

func TestOpenURLReportsAFailureWhenEveryServerFails(t *testing.T) {
	e := newEnv(t)
	f := jellyfinFake(t, `{}`, http.StatusNoContent)
	e.add("Jelly", "jellyfin", f.srv.URL, func(in *Input) { in.Token = str("wrong wrong wrong") })
	_, err := e.svc.OpenURL(bg(), movieTitle())
	if !errors.Is(err, ErrAuth) {
		t.Errorf("want the auth failure, got %v", err)
	}
}

func TestOpenURLForEmbyAndForShowsAndMusic(t *testing.T) {
	e := newEnv(t)
	f := jellyfinFake(t, `{"Items":[
	  {"Id":"s1","Name":"Pioneer One","Type":"Series","ProductionYear":2010,"ProviderIds":{"TvMaze":"99001"}},
	  {"Id":"a1","Name":"Frédéric Chopin","Type":"MusicArtist","ProviderIds":{"MusicBrainzArtist":"a1b2c3d4-0000-4000-8000-000000000001"}}
	]}`, http.StatusNoContent)
	e.add("Emby", "emby", f.srv.URL)

	show := titles.Title{Kind: titles.KindShow, Name: "Pioneer One", Year: 2010, TVmazeID: 99001}
	res, err := e.svc.OpenURL(bg(), show)
	if err != nil {
		t.Fatal(err)
	}
	if res.URL != f.srv.URL+"/web/index.html#!/item?id=s1&serverId=server-id-1" || res.Server.Kind != "emby" {
		t.Errorf("Emby page = %q", res.URL)
	}
	artist := titles.Title{Kind: titles.KindArtist, Name: "Chopin", MBID: "A1B2C3D4-0000-4000-8000-000000000001"}
	res, err = e.svc.OpenURL(bg(), artist)
	if err != nil || !strings.Contains(res.URL, "id=a1") {
		t.Errorf("artist = %+v, %v", res, err)
	}
}

func TestOpenURLOnPlex(t *testing.T) {
	e := newEnv(t)
	f := plexFake(t, map[string]string{
		"1": `{"MediaContainer":{"Metadata":[
		  {"ratingKey":"500","title":"Night of the Living Dead","year":1990,"Guid":[{"id":"tmdb://111"}]},
		  {"ratingKey":"501","title":"Night of the Living Dead (original)","year":1968,"Guid":[{"id":"imdb://tt0063350"},{"id":"tmdb://10331"}]}
		]}}`,
		"3": `{"MediaContainer":{"Metadata":[]}}`,
	})
	e.add("Plex", "plex", f.srv.URL)
	res, err := e.svc.OpenURL(bg(), movieTitle())
	if err != nil {
		t.Fatal(err)
	}
	want := "https://app.plex.tv/desktop#!/server/abc123machine/details?key=%2Flibrary%2Fmetadata%2F501"
	if res.URL != want || res.Server.Kind != "plex" {
		t.Errorf("OpenURL = %+v\nwant url %s", res, want)
	}
	for _, c := range f.recorded() {
		if strings.HasSuffix(c.path, "/all") && c.query.Get("title") != "Night of the Living Dead" {
			t.Errorf("the title filter was %q", c.query.Get("title"))
		}
		if strings.HasSuffix(c.path, "/all") && strings.Contains(c.path, "/2/") {
			t.Error("the TV section must not be searched for a film")
		}
	}

	// By name when no id matches.
	p2 := plexFake(t, map[string]string{"1": `{"MediaContainer":{"Metadata":[{"ratingKey":"77","title":"Night of the Living Dead","year":1968}]}}`})
	e2 := newEnv(t)
	e2.add("Plex", "plex", p2.srv.URL)
	if res, err := e2.svc.OpenURL(bg(), movieTitle()); err != nil || !strings.HasSuffix(res.URL, "%2Flibrary%2Fmetadata%2F77") {
		t.Errorf("by name: %+v, %v", res, err)
	}
	// And not found.
	e3 := newEnv(t)
	e3.add("Plex", "plex", plexFake(t, nil).srv.URL)
	if _, err := e3.svc.OpenURL(bg(), movieTitle()); !errors.Is(err, ErrNotOnServer) {
		t.Errorf("not on Plex: %v", err)
	}
}

func TestSlowServerDoesNotHangTheCaller(t *testing.T) {
	e := newEnv(t)
	slow := newFake(t, func(w http.ResponseWriter, c call) {
		time.Sleep(300 * time.Millisecond)
		w.WriteHeader(http.StatusNoContent)
	})
	e.add("Slow", "jellyfin", slow.srv.URL)
	ctx, cancel := context.WithTimeout(bg(), 50*time.Millisecond)
	defer cancel()
	start := time.Now()
	res := e.svc.Notify(ctx, Event{Kind: EventCreated, Paths: []string{"/m/a.mkv"}})
	if time.Since(start) > 2*time.Second || len(res) != 1 || res[0].OK {
		t.Errorf("a cancelled context should end the call quickly with a failure: %v, %+v", time.Since(start), res)
	}
}
