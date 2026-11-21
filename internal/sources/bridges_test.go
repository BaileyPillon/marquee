package sources

import (
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
)

const bridgeKey = "bridgekey-0123456789abcdef-wxyz"

// bridge is a fake Prowlarr or Jackett.
type bridge struct {
	*httptest.Server
	mu   sync.Mutex
	seen []*http.Request
}

func (b *bridge) lastRequest() *http.Request {
	b.mu.Lock()
	defer b.mu.Unlock()
	if len(b.seen) == 0 {
		return nil
	}
	return b.seen[len(b.seen)-1]
}

func newProwlarr(t *testing.T) *bridge {
	t.Helper()
	b := &bridge{}
	b.Server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b.mu.Lock()
		b.seen = append(b.seen, r.Clone(r.Context()))
		b.mu.Unlock()
		if r.URL.Path != "/api/v1/indexer" {
			http.NotFound(w, r)
			return
		}
		if r.Header.Get("X-Api-Key") != bridgeKey {
			http.Error(w, `{"error":"Unauthorized"}`, http.StatusUnauthorized)
			return
		}
		_ = json.NewEncoder(w).Encode([]map[string]any{
			{"id": 3, "name": "Open Torrents A", "enable": true, "protocol": "torrent", "priority": 10, "implementation": "Torznab"},
			{"id": 4, "name": "Open Usenet B", "enable": true, "protocol": "usenet", "priority": 25, "implementation": "Newznab"},
			{"id": 5, "name": "Turned off", "enable": false, "protocol": "torrent", "priority": 25},
			{"id": 6, "name": "Odd priority", "enable": true, "protocol": "torrent", "priority": 0},
		})
	}))
	t.Cleanup(b.Close)
	return b
}

func newJackett(t *testing.T) *bridge {
	t.Helper()
	b := &bridge{}
	b.Server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b.mu.Lock()
		b.seen = append(b.seen, r.Clone(r.Context()))
		b.mu.Unlock()
		if r.URL.Path != "/api/v2.0/indexers" {
			http.NotFound(w, r)
			return
		}
		if r.URL.Query().Get("apikey") != bridgeKey {
			http.Error(w, "Unauthorized", http.StatusUnauthorized)
			return
		}
		if r.URL.Query().Get("configured") != "true" {
			http.Error(w, "this test expects configured=true", http.StatusBadRequest)
			return
		}
		_ = json.NewEncoder(w).Encode([]map[string]any{
			{"id": "openfilms", "name": "Open Films", "configured": true, "type": "public"},
			{"id": "free-tv", "name": "Free TV", "configured": true, "type": "public"},
			{"id": "unused", "name": "Never set up", "configured": false},
		})
	}))
	t.Cleanup(b.Close)
	return b
}

func TestImportFromProwlarr(t *testing.T) {
	e := newEnv(t)
	p := newProwlarr(t)
	res, err := e.svc.Import(bg(), "prowlarr", p.URL+"/", bridgeKey)
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Created) != 3 || res.Skipped != 0 {
		t.Fatalf("created %d skipped %d, want 3 and 0 (the disabled indexer is left out)", len(res.Created), res.Skipped)
	}
	byName := map[string]Indexer{}
	for _, ix := range res.Created {
		byName[ix.Name] = ix
	}
	a, b, odd := byName["Open Torrents A"], byName["Open Usenet B"], byName["Odd priority"]
	if a.Kind != KindTorznab || a.URL != p.URL+"/3/api" || a.Priority != 10 || !a.Enabled {
		t.Errorf("torrent indexer = %+v", a)
	}
	if b.Kind != KindNewznab || b.URL != p.URL+"/4/api" {
		t.Errorf("usenet indexer = %+v", b)
	}
	if odd.Priority != DefaultPriority {
		t.Errorf("a priority outside 1..50 should become the default, got %d", odd.Priority)
	}
	if a.APIKey != "••••wxyz" {
		t.Errorf("masked key = %q", a.APIKey)
	}
	if got := p.lastRequest().Header.Get("X-Api-Key"); got != bridgeKey {
		t.Errorf("the key must travel in the X-Api-Key header, got %q", got)
	}
	if p.lastRequest().URL.RawQuery != "" {
		t.Errorf("the Prowlarr call has a query string: %q", p.lastRequest().URL.RawQuery)
	}
	b2, _ := json.Marshal(res)
	noKey(t, "the import answer", string(b2), bridgeKey)
	noKey(t, "the log", e.logs.String(), bridgeKey)

	// The imported key is the Prowlarr key, sealed: a search sends it.
	rec, err := e.svc.store.Get(bg(), a.ID)
	if err != nil {
		t.Fatal(err)
	}
	if plain, _ := e.svc.open(rec.KeySealed); plain != bridgeKey {
		t.Errorf("stored key opens to %q", plain)
	}

	// Importing again adds nothing.
	again, err := e.svc.Import(bg(), "prowlarr", p.URL, bridgeKey)
	if err != nil || len(again.Created) != 0 || again.Skipped != 3 {
		t.Errorf("second import: %+v %v", again, err)
	}
	if n := e.count(`SELECT COUNT(*) FROM indexers`); n != 3 {
		t.Errorf("%d sources after two imports, want 3", n)
	}
}

func TestImportFromJackett(t *testing.T) {
	e := newEnv(t)
	j := newJackett(t)
	res, err := e.svc.Import(bg(), "jackett", j.URL, bridgeKey)
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Created) != 2 {
		t.Fatalf("created %d, want 2 (an indexer that was never configured is left out)", len(res.Created))
	}
	byName := map[string]Indexer{}
	for _, ix := range res.Created {
		byName[ix.Name] = ix
	}
	films, tv := byName["Open Films"], byName["Free TV"]
	if films.Kind != KindTorznab || films.URL != j.URL+"/api/v2.0/indexers/openfilms/results/torznab/" {
		t.Errorf("films = %+v", films)
	}
	if tv.URL != j.URL+"/api/v2.0/indexers/free-tv/results/torznab/" || tv.Priority != DefaultPriority {
		t.Errorf("tv = %+v", tv)
	}
	if films.APIKey != "••••wxyz" || strings.Contains(films.URL, "apikey") {
		t.Errorf("films key %q url %q", films.APIKey, films.URL)
	}
	noKey(t, "the log", e.logs.String(), bridgeKey)
	if again, err := e.svc.Import(bg(), "jackett", j.URL, bridgeKey); err != nil || again.Skipped != 2 || len(again.Created) != 0 {
		t.Errorf("second import: %+v %v", again, err)
	}
}

func TestImportErrors(t *testing.T) {
	e := newEnv(t)
	p := newProwlarr(t)

	var se *Error
	_, err := e.svc.Import(bg(), "prowlarr", p.URL, "wrong-key-wrong-key-wrong-key")
	if !errors.As(err, &se) || se.Code != CodeKeyRefused {
		t.Errorf("wrong key: %v", err)
	} else {
		noKey(t, "the message", se.Message, "wrong-key-wrong-key-wrong-key")
	}

	// A Jackett address given as Prowlarr is not a Prowlarr.
	j := newJackett(t)
	_, err = e.svc.Import(bg(), "prowlarr", j.URL, bridgeKey)
	if !errors.As(err, &se) || se.Code != CodeNotAFeed {
		t.Errorf("wrong kind of server: %v", err)
	}

	// Something that answers 200 but not with JSON.
	html := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte("<html>hello</html>"))
	}))
	t.Cleanup(html.Close)
	_, err = e.svc.Import(bg(), "jackett", html.URL, bridgeKey)
	if !errors.As(err, &se) || se.Code != CodeNotAFeed {
		t.Errorf("html answer: %v", err)
	}

	// Nothing listening.
	dead := httptest.NewServer(http.NotFoundHandler())
	addr := dead.URL
	dead.Close()
	_, err = e.svc.Import(bg(), "prowlarr", addr, bridgeKey)
	if !errors.As(err, &se) || se.Code != CodeUnreachable {
		t.Errorf("dead server: %v", err)
	} else {
		noKey(t, "the message", se.Message, bridgeKey)
	}
	noKey(t, "the log", e.logs.String(), bridgeKey)

	// Input checks.
	for name, args := range map[string][3]string{
		"unknown bridge": {"sonarr", p.URL, bridgeKey},
		"no key":         {"prowlarr", p.URL, ""},
		"bad url":        {"prowlarr", "not a url", bridgeKey},
	} {
		if _, err := e.svc.Import(bg(), args[0], args[1], args[2]); !asInvalid(err) {
			t.Errorf("%s: err = %v, want InvalidError", name, err)
		}
	}
	if n := e.count(`SELECT COUNT(*) FROM indexers`); n != 0 {
		t.Errorf("a failed import left %d sources behind", n)
	}
}
