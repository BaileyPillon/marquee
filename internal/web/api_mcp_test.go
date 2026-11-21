package web

import (
	"context"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/BaileyPillon/marquee/internal/accounts"
	"github.com/BaileyPillon/marquee/internal/core/db"
	"github.com/BaileyPillon/marquee/internal/core/events"
	"github.com/BaileyPillon/marquee/internal/core/secrets"
	"github.com/BaileyPillon/marquee/internal/settings"
)

// fakeMCP stands in for the MCP server: the web package only mounts it.
type fakeMCP struct {
	mu         sync.Mutex
	paths      []string
	auth       []string
	csrf       []string
	infoUser   string
	infoOrigin string
}

func (f *fakeMCP) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	f.mu.Lock()
	f.paths = append(f.paths, r.Method+" "+r.URL.Path)
	f.auth = append(f.auth, r.Header.Get("Authorization"))
	f.csrf = append(f.csrf, r.Header.Get(CSRFHeader))
	f.mu.Unlock()
	w.Header().Set("Content-Type", "application/json")
	_, _ = io.WriteString(w, `{"jsonrpc":"2.0","id":1,"result":{}}`)
}

func (f *fakeMCP) MCPInfo(_ context.Context, u accounts.User, origin string) any {
	f.mu.Lock()
	f.infoUser, f.infoOrigin = u.Name, origin
	f.mu.Unlock()
	return map[string]any{"enabled": true, "url": origin + "/mcp", "tools": []any{}, "recentCalls": []any{}}
}

func newMCPApp(t *testing.T, mcp MCPService) *app {
	t.Helper()
	dir := t.TempDir()
	mainDB, err := db.Open(filepath.Join(dir, db.MainFile))
	if err != nil {
		t.Fatal(err)
	}
	eventsDB, err := db.Open(filepath.Join(dir, db.EventsFile))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { mainDB.Close(); eventsDB.Close() })
	if _, err := db.Migrate(context.Background(), mainDB, db.SetMain); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Migrate(context.Background(), eventsDB, db.SetEvents); err != nil {
		t.Fatal(err)
	}
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	box, err := secrets.LoadOrCreate(filepath.Join(dir, "marquee.key"), log)
	if err != nil {
		t.Fatal(err)
	}
	hub := events.NewHub(log)
	t.Cleanup(hub.Close)
	cfg := settings.New(mainDB, box)
	s := New(Deps{
		Version: "9.9.9-test", Log: log, MainDB: mainDB, EventsDB: eventsDB,
		Accounts: accounts.NewService(mainDB, accounts.Options{Logger: log, PasswordParams: lightParams}),
		Settings: cfg, Hub: hub, MCP: mcp, UI: testUI(),
	})
	srv := httptest.NewServer(s.Handler())
	t.Cleanup(srv.Close)
	return &app{t: t, srv: srv, mainDB: mainDB, settings: cfg, hub: hub}
}

func TestMCPRoutesAreLeftOutWithoutTheService(t *testing.T) {
	a := newMCPApp(t, nil)
	c := a.newClient()
	owner := a.owner()
	// /mcp is then just a path of the single-page app, and the info route is a JSON 404.
	if r := c.send("POST", "/mcp", nil); r.Status == http.StatusOK && strings.Contains(string(r.Body), "jsonrpc") {
		t.Errorf("/mcp answered without the service: %s", r.Body)
	}
	if r := owner.do("GET", "/api/v1/mcp/info", nil); r.Status != http.StatusNotFound || r.errCode(t) != "not_found" {
		t.Errorf("info without the service = %d %s", r.Status, r.Body)
	}
}

func TestMCPEndpointIsMountedOutsideTheAPIGuard(t *testing.T) {
	f := &fakeMCP{}
	a := newMCPApp(t, f)
	c := a.newClient()

	for _, path := range []string{"/mcp", "/api/v1/mcp"} {
		// No cookie, no X-Marquee header, and a key the API guard would call invalid
		// (it would answer 401 in Marquee's shape): the MCP server gets the request and decides.
		r := c.send("POST", path, map[string]any{"jsonrpc": "2.0", "id": 1, "method": "ping"},
			"Authorization", "Bearer mq_notarealkey_value")
		if r.Status != http.StatusOK || r.JSON(t)["jsonrpc"] != "2.0" {
			t.Errorf("POST %s = %d %s, want the MCP handler's answer", path, r.Status, r.Body)
		}
		// Every response still carries the security headers.
		if r.Header.Get("X-Content-Type-Options") != "nosniff" || r.Header.Get("Content-Security-Policy") == "" {
			t.Errorf("POST %s: headers = %v", path, r.Header)
		}
	}
	// GET and DELETE reach it too (it answers 405 and ends sessions itself).
	c.send("GET", "/mcp", nil)
	c.send("DELETE", "/api/v1/mcp", nil)

	f.mu.Lock()
	defer f.mu.Unlock()
	want := []string{"POST /mcp", "POST /api/v1/mcp", "GET /mcp", "DELETE /api/v1/mcp"}
	if len(f.paths) != len(want) {
		t.Fatalf("handler saw %v, want %v", f.paths, want)
	}
	for i := range want {
		if f.paths[i] != want[i] {
			t.Errorf("request %d = %q, want %q", i, f.paths[i], want[i])
		}
	}
	if f.auth[0] != "Bearer mq_notarealkey_value" {
		t.Errorf("the Authorization header did not reach the handler: %q", f.auth[0])
	}
}

func TestMCPInfoRoute(t *testing.T) {
	f := &fakeMCP{}
	a := newMCPApp(t, f)

	// Signed in only.
	if r := a.newClient().send("GET", "/api/v1/mcp/info", nil); r.Status != http.StatusUnauthorized || r.errCode(t) != "unauthorized" {
		t.Errorf("anonymous = %d %s", r.Status, r.Body)
	}

	owner := a.owner()
	r := owner.do("GET", "/api/v1/mcp/info", nil)
	if r.Status != http.StatusOK {
		t.Fatalf("owner = %d %s", r.Status, r.Body)
	}
	m := r.JSON(t)
	if m["enabled"] != true || m["url"] != a.srv.URL+"/mcp" {
		t.Errorf("info = %v", m)
	}
	if f.infoUser != "Bailey" || f.infoOrigin != a.srv.URL {
		t.Errorf("the service was told user %q origin %q", f.infoUser, f.infoOrigin)
	}

	// A member gets it too, as themselves; behind a TLS proxy the URL says https.
	member := a.member(owner, "Mia")
	r = member.do("GET", "/api/v1/mcp/info", nil, "X-Forwarded-Proto", "https")
	if r.Status != http.StatusOK || f.infoUser != "Mia" || !strings.HasPrefix(f.infoOrigin, "https://127.0.0.1:") {
		t.Errorf("member = %d %s (user %q origin %q)", r.Status, r.Body, f.infoUser, f.infoOrigin)
	}

	// A key works as well as a session (it is "signed in"), and only GET exists.
	k := owner.do("POST", "/api/v1/apikeys", map[string]any{"name": "claude", "scopes": []string{"see"}})
	secret := k.JSON(t)["secret"].(string)
	if r := a.newClient().send("GET", "/api/v1/mcp/info", nil, "Authorization", "Bearer "+secret); r.Status != http.StatusOK {
		t.Errorf("with a key = %d %s", r.Status, r.Body)
	}
	if r := owner.do("POST", "/api/v1/mcp/info", map[string]any{}); r.Status == http.StatusOK {
		t.Errorf("POST info = %d", r.Status)
	}
}
