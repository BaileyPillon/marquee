package web

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"net/url"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"testing/fstest"
	"time"

	"github.com/BaileyPillon/marquee/internal/accounts"
	"github.com/BaileyPillon/marquee/internal/core/db"
	"github.com/BaileyPillon/marquee/internal/core/events"
	"github.com/BaileyPillon/marquee/internal/core/secrets"
	"github.com/BaileyPillon/marquee/internal/settings"
)

// app is a whole Marquee server on a temp dir, behind httptest.
type app struct {
	t        *testing.T
	srv      *httptest.Server
	mainDB   *sql.DB
	settings *settings.Service
	hub      *events.Hub
}

var lightParams = &accounts.PasswordParams{Time: 1, Memory: 64, Threads: 1, KeyLen: 32, SaltLen: 16}

func newApp(t *testing.T, ui fstest.MapFS) *app {
	t.Helper()
	ctx := context.Background()
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
	if _, err := db.Migrate(ctx, mainDB, db.SetMain); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Migrate(ctx, eventsDB, db.SetEvents); err != nil {
		t.Fatal(err)
	}
	box, err := secrets.LoadOrCreate(filepath.Join(dir, "marquee.key"), slog.New(slog.NewTextHandler(io.Discard, nil)))
	if err != nil {
		t.Fatal(err)
	}
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	hub := events.NewHub(log, events.WithHeartbeat(50*time.Millisecond))
	t.Cleanup(hub.Close)
	cfg := settings.New(mainDB, box)
	if ui == nil {
		ui = testUI()
	}
	s := New(Deps{
		Version:  "9.9.9-test",
		Log:      log,
		MainDB:   mainDB,
		EventsDB: eventsDB,
		Accounts: accounts.NewService(mainDB, accounts.Options{Logger: log, PasswordParams: lightParams}),
		Settings: cfg,
		Hub:      hub,
		UI:       ui,
	})
	srv := httptest.NewServer(s.Handler())
	t.Cleanup(srv.Close)
	return &app{t: t, srv: srv, mainDB: mainDB, settings: cfg, hub: hub}
}

func testUI() fstest.MapFS {
	return fstest.MapFS{
		"index.html":    {Data: []byte("<!doctype html><title>Marquee</title>")},
		"assets/app.js": {Data: []byte("console.log('app')")},
	}
}

// client is one browser or script: its own cookie jar.
type client struct {
	a   *app
	hc  *http.Client
	jar *cookiejar.Jar
}

func (a *app) newClient() *client {
	jar, _ := cookiejar.New(nil)
	return &client{a: a, jar: jar, hc: &http.Client{Jar: jar, CheckRedirect: func(*http.Request, []*http.Request) error {
		return http.ErrUseLastResponse
	}}}
}

type reply struct {
	Status int
	Header http.Header
	Body   []byte
}

func (r reply) JSON(t *testing.T) map[string]any {
	t.Helper()
	var m map[string]any
	if err := json.Unmarshal(r.Body, &m); err != nil {
		t.Fatalf("body is not JSON (%d): %q", r.Status, r.Body)
	}
	return m
}

func (r reply) errCode(t *testing.T) string {
	t.Helper()
	e, _ := r.JSON(t)["error"].(map[string]any)
	if e == nil {
		t.Fatalf("no error object in %s", r.Body)
	}
	if msg, _ := e["message"].(string); msg == "" {
		t.Errorf("error without a message: %s", r.Body)
	}
	code, _ := e["code"].(string)
	return code
}

// do sends a request with the CSRF header (the way the UI does).
func (c *client) do(method, path string, body any, headers ...string) reply {
	c.a.t.Helper()
	return c.send(method, path, body, append([]string{"X-Marquee", "1"}, headers...)...)
}

// send sends exactly the headers given.
func (c *client) send(method, path string, body any, headers ...string) reply {
	c.a.t.Helper()
	var rd io.Reader
	if body != nil {
		b, _ := json.Marshal(body)
		rd = bytes.NewReader(b)
	}
	req, err := http.NewRequest(method, c.a.srv.URL+path, rd)
	if err != nil {
		c.a.t.Fatal(err)
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	for i := 0; i+1 < len(headers); i += 2 {
		req.Header.Set(headers[i], headers[i+1])
	}
	resp, err := c.hc.Do(req)
	if err != nil {
		c.a.t.Fatal(err)
	}
	defer resp.Body.Close()
	b, _ := io.ReadAll(resp.Body)
	return reply{Status: resp.StatusCode, Header: resp.Header, Body: b}
}

func (c *client) cookie() *http.Cookie {
	u, _ := url.Parse(c.a.srv.URL)
	for _, ck := range c.jar.Cookies(u) {
		if ck.Name == SessionCookie {
			return ck
		}
	}
	return nil
}

const ownerPW = "correct horse"

// owner sets the instance up and returns a client signed in as the owner.
func (a *app) owner() *client {
	a.t.Helper()
	c := a.newClient()
	r := c.do("POST", "/api/v1/setup/owner", map[string]string{"name": "Bailey", "password": ownerPW})
	if r.Status != http.StatusCreated {
		a.t.Fatalf("setup/owner = %d %s", r.Status, r.Body)
	}
	return c
}

// member creates a member through an invite and returns a signed-in client.
func (a *app) member(owner *client, name string) *client {
	a.t.Helper()
	inv := owner.do("POST", "/api/v1/invites", map[string]string{"role": "member"})
	if inv.Status != http.StatusCreated {
		a.t.Fatalf("invite = %d %s", inv.Status, inv.Body)
	}
	c := a.newClient()
	r := c.do("POST", "/api/v1/invites/accept", map[string]string{
		"token": inv.JSON(a.t)["token"].(string), "name": name, "password": "member password"})
	if r.Status != http.StatusCreated {
		a.t.Fatalf("accept = %d %s", r.Status, r.Body)
	}
	return c
}

// ---- health ----------------------------------------------------------------

func TestHealth(t *testing.T) {
	a := newApp(t, nil)
	r := a.newClient().send("GET", "/api/v1/health", nil)
	if r.Status != 200 {
		t.Fatalf("status %d: %s", r.Status, r.Body)
	}
	m := r.JSON(t)
	if m["status"] != "ok" || m["version"] != "9.9.9-test" || m["db"] != "ok" {
		t.Errorf("health = %v", m)
	}
	if _, ok := m["uptimeSeconds"].(float64); !ok {
		t.Errorf("uptimeSeconds missing or not a number: %v", m)
	}
	if got := r.Header.Get("Content-Type"); !strings.HasPrefix(got, "application/json") {
		t.Errorf("Content-Type = %q", got)
	}
	if r.Header.Get("X-Request-ID") == "" {
		t.Error("no X-Request-ID")
	}
}

func TestHealthReportsBrokenDatabase(t *testing.T) {
	a := newApp(t, nil)
	a.mainDB.Close()
	r := a.newClient().send("GET", "/api/v1/health", nil)
	if r.Status != http.StatusServiceUnavailable {
		t.Fatalf("status %d, want 503", r.Status)
	}
	if m := r.JSON(t); m["status"] != "degraded" || m["db"] != "error" {
		t.Errorf("health = %v", m)
	}
}

// ---- headers and error shape -----------------------------------------------

func TestSecurityHeadersOnEveryResponse(t *testing.T) {
	a := newApp(t, nil)
	c := a.newClient()
	for _, path := range []string{"/", "/api/v1/health", "/api/v1/nothing", "/assets/app.js", "/some/spa/route"} {
		r := c.send("GET", path, nil)
		if got := r.Header.Get("Content-Security-Policy"); got != ContentSecurityPolicy {
			t.Errorf("%s: CSP = %q", path, got)
		}
		if r.Header.Get("X-Content-Type-Options") != "nosniff" || r.Header.Get("Referrer-Policy") != "same-origin" {
			t.Errorf("%s: headers = %v", path, r.Header)
		}
	}
	for _, want := range []string{
		"default-src 'self'", "img-src 'self' data: https://image.tmdb.org https://coverartarchive.org",
		"style-src 'self' 'unsafe-inline'", "connect-src 'self'", "frame-ancestors 'none'", "base-uri 'self'",
	} {
		if !strings.Contains(ContentSecurityPolicy, want) {
			t.Errorf("CSP lacks %q", want)
		}
	}
}

func TestUnknownAPIPathIsJSON404(t *testing.T) {
	a := newApp(t, nil)
	c := a.newClient()
	for _, path := range []string{"/api/v1/nope", "/api/", "/api/v2/health"} {
		r := c.send("GET", path, nil)
		if r.Status != 404 || r.errCode(t) != "not_found" {
			t.Errorf("%s: %d %s", path, r.Status, r.Body)
		}
	}
	// Wrong method on a real path is also a JSON error, not the UI.
	if r := c.do("POST", "/api/v1/health", map[string]string{}); r.Status != 404 || r.errCode(t) != "not_found" {
		t.Errorf("POST health: %d %s", r.Status, r.Body)
	}
}

func TestBadJSONBodies(t *testing.T) {
	a := newApp(t, nil)
	c := a.newClient()
	for name, body := range map[string]string{
		"empty": "", "not json": "{nope", "two values": `{"name":"a"}{"name":"b"}`, "wrong type": `[1,2]`,
	} {
		req, _ := http.NewRequest("POST", a.srv.URL+"/api/v1/auth/login", strings.NewReader(body))
		req.Header.Set("X-Marquee", "1")
		resp, err := c.hc.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		b, _ := io.ReadAll(resp.Body)
		resp.Body.Close()
		if resp.StatusCode != 400 {
			t.Errorf("%s: status %d: %s", name, resp.StatusCode, b)
		}
	}
	huge := strings.Repeat("x", 2<<20)
	r := c.do("POST", "/api/v1/auth/login", map[string]string{"name": huge, "password": "x"})
	if r.Status != http.StatusRequestEntityTooLarge || r.errCode(t) != "too_large" {
		t.Errorf("huge body: %d %s", r.Status, r.Body)
	}
}

// ---- first run, login, /me, logout -----------------------------------------

func TestSetupStatusAndOwnerCreation(t *testing.T) {
	a := newApp(t, nil)
	c := a.newClient()

	if m := c.send("GET", "/api/v1/setup", nil).JSON(t); m["needsOwner"] != true {
		t.Fatalf("setup = %v, want needsOwner true", m)
	}
	r := c.do("POST", "/api/v1/setup/owner", map[string]string{"name": "Bailey", "password": ownerPW})
	if r.Status != 201 {
		t.Fatalf("setup/owner = %d %s", r.Status, r.Body)
	}
	user := r.JSON(t)["user"].(map[string]any)
	if user["name"] != "Bailey" || user["role"] != "owner" {
		t.Errorf("user = %v", user)
	}
	if strings.Contains(string(r.Body), "password") || strings.Contains(string(r.Body), "argon2") {
		t.Errorf("response leaks password material: %s", r.Body)
	}
	// They are signed in straight away.
	if me := c.send("GET", "/api/v1/me", nil); me.Status != 200 {
		t.Errorf("not signed in after setup: %d", me.Status)
	}
	if m := c.send("GET", "/api/v1/setup", nil).JSON(t); m["needsOwner"] != false {
		t.Errorf("setup = %v, want needsOwner false", m)
	}
	// A second attempt is refused.
	r = a.newClient().do("POST", "/api/v1/setup/owner", map[string]string{"name": "Mallory", "password": "mallory password"})
	if r.Status != http.StatusConflict || r.errCode(t) != "owner_exists" {
		t.Errorf("second owner: %d %s", r.Status, r.Body)
	}
	// Validation.
	b := newApp(t, nil)
	if r := b.newClient().do("POST", "/api/v1/setup/owner", map[string]string{"name": "x", "password": "short"}); r.Status != 400 || r.errCode(t) != "weak_password" {
		t.Errorf("short password: %d %s", r.Status, r.Body)
	}
}

func TestConcurrentOwnerSetupOverHTTP(t *testing.T) {
	a := newApp(t, nil)
	const n = 10
	var wg sync.WaitGroup
	var created, conflict, other atomic.Int32
	start := make(chan struct{})
	for i := range n {
		wg.Add(1)
		go func() {
			defer wg.Done()
			c := a.newClient()
			<-start
			r := c.do("POST", "/api/v1/setup/owner", map[string]string{
				"name": "owner" + string(rune('a'+i)), "password": "password-" + string(rune('a'+i))})
			switch r.Status {
			case 201:
				created.Add(1)
			case 409:
				conflict.Add(1)
			default:
				other.Add(1)
				t.Errorf("unexpected %d: %s", r.Status, r.Body)
			}
		}()
	}
	close(start)
	wg.Wait()
	if created.Load() != 1 || conflict.Load() != n-1 {
		t.Fatalf("%d created, %d conflicts, %d other; want 1 and %d", created.Load(), conflict.Load(), other.Load(), n-1)
	}
}

func TestLoginMeLogout(t *testing.T) {
	a := newApp(t, nil)
	a.owner()

	c := a.newClient()
	if r := c.send("GET", "/api/v1/me", nil); r.Status != 401 || r.errCode(t) != "unauthorized" {
		t.Fatalf("anonymous /me: %d %s", r.Status, r.Body)
	}

	r := c.do("POST", "/api/v1/auth/login", map[string]string{"name": "bailey", "password": ownerPW})
	if r.Status != 200 {
		t.Fatalf("login = %d %s", r.Status, r.Body)
	}

	// Cookie attributes.
	var set *http.Cookie
	for _, ck := range (&http.Response{Header: r.Header}).Cookies() {
		if ck.Name == SessionCookie {
			set = ck
		}
	}
	if set == nil {
		t.Fatal("no marquee_session cookie")
	}
	if !set.HttpOnly || set.SameSite != http.SameSiteLaxMode || set.Path != "/" {
		t.Errorf("cookie = %+v", set)
	}
	if set.Secure {
		t.Error("cookie is Secure on plain HTTP")
	}
	if days := time.Until(set.Expires).Hours() / 24; days < 29.9 || days > 30.1 {
		t.Errorf("cookie lasts %.2f days, want 30", days)
	}
	if len(set.Value) < 43 {
		t.Errorf("token looks too short: %d chars", len(set.Value))
	}

	me := c.send("GET", "/api/v1/me", nil)
	if me.Status != 200 {
		t.Fatalf("/me = %d %s", me.Status, me.Body)
	}
	m := me.JSON(t)
	user := m["user"].(map[string]any)
	if user["name"] != "Bailey" || user["role"] != "owner" || m["via"] != "session" {
		t.Errorf("/me = %v", m)
	}
	if scopes, _ := m["scopes"].([]any); len(scopes) != 7 {
		t.Errorf("owner scopes = %v", m["scopes"])
	}

	if lo := c.do("POST", "/api/v1/auth/logout", nil); lo.Status != 204 {
		t.Fatalf("logout = %d %s", lo.Status, lo.Body)
	}
	if r := c.send("GET", "/api/v1/me", nil); r.Status != 401 {
		t.Errorf("/me after logout = %d", r.Status)
	}
}

func TestLogoutInvalidatesTheTokenServerSide(t *testing.T) {
	a := newApp(t, nil)
	owner := a.owner()
	token := owner.cookie().Value
	owner.do("POST", "/api/v1/auth/logout", nil)

	// Replaying the old cookie must not work.
	req, _ := http.NewRequest("GET", a.srv.URL+"/api/v1/me", nil)
	req.AddCookie(&http.Cookie{Name: SessionCookie, Value: token})
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != 401 {
		t.Errorf("old token still works: %d", resp.StatusCode)
	}
}

func TestWrongPasswordThenRateLimit(t *testing.T) {
	a := newApp(t, nil)
	a.owner()
	c := a.newClient()

	for i := 1; i <= accounts.LoginMaxFailures; i++ {
		r := c.do("POST", "/api/v1/auth/login", map[string]string{"name": "Bailey", "password": "wrong password"})
		if r.Status != 401 || r.errCode(t) != "invalid_credentials" {
			t.Fatalf("attempt %d: %d %s", i, r.Status, r.Body)
		}
	}
	// Now even the right password is turned away.
	r := c.do("POST", "/api/v1/auth/login", map[string]string{"name": "Bailey", "password": ownerPW})
	if r.Status != http.StatusTooManyRequests || r.errCode(t) != "too_many_attempts" {
		t.Fatalf("after %d failures: %d %s", accounts.LoginMaxFailures, r.Status, r.Body)
	}
	if ra := r.Header.Get("Retry-After"); ra == "" || ra == "0" {
		t.Errorf("Retry-After = %q", ra)
	}
	if c.cookie() != nil {
		t.Error("a blocked login set a cookie")
	}
}

// ---- CSRF ------------------------------------------------------------------

func TestCookiePOSTNeedsCSRFHeader(t *testing.T) {
	a := newApp(t, nil)
	owner := a.owner()

	body := map[string]string{"role": "member"}
	if r := owner.send("POST", "/api/v1/invites", body); r.Status != 403 || r.errCode(t) != "csrf" {
		t.Fatalf("without header: %d %s", r.Status, r.Body)
	}
	if r := owner.send("POST", "/api/v1/invites", body, "X-Marquee", "0"); r.Status != 403 {
		t.Errorf("wrong header value: %d", r.Status)
	}
	if r := owner.send("POST", "/api/v1/invites", body, "X-Marquee", "1"); r.Status != 201 {
		t.Errorf("with header: %d %s", r.Status, r.Body)
	}

	// Every unsafe method is covered, not only POST.
	if r := owner.send("PUT", "/api/v1/settings/general", map[string]any{"a": "b"}); r.Status != 403 {
		t.Errorf("PUT without header: %d", r.Status)
	}
	if r := owner.send("DELETE", "/api/v1/apikeys/1", nil); r.Status != 403 {
		t.Errorf("DELETE without header: %d", r.Status)
	}
	if r := owner.send("POST", "/api/v1/auth/logout", nil); r.Status != 403 {
		t.Errorf("logout without header: %d", r.Status)
	}
	// Safe methods never need it.
	if r := owner.send("GET", "/api/v1/me", nil); r.Status != 200 {
		t.Errorf("GET /me: %d", r.Status)
	}
	// The session survived all those refusals.
	if r := owner.send("GET", "/api/v1/me", nil); r.Status != 200 {
		t.Errorf("session lost: %d", r.Status)
	}
}

func TestPublicPOSTsAlsoNeedCSRFHeader(t *testing.T) {
	a := newApp(t, nil)
	c := a.newClient()
	// First-run setup is the endpoint a cross-site form would most like to hit.
	if r := c.send("POST", "/api/v1/setup/owner", map[string]string{"name": "Mallory", "password": "mallory password"}); r.Status != 403 {
		t.Fatalf("setup without header: %d %s", r.Status, r.Body)
	}
	if need := c.send("GET", "/api/v1/setup", nil).JSON(t)["needsOwner"]; need != true {
		t.Error("a request without the header created the owner")
	}
	if r := c.send("POST", "/api/v1/auth/login", map[string]string{"name": "a", "password": "b"}); r.Status != 403 {
		t.Errorf("login without header: %d", r.Status)
	}
}

// ---- API keys --------------------------------------------------------------

func TestAPIKeyBearerAuthAndRevoke(t *testing.T) {
	a := newApp(t, nil)
	owner := a.owner()

	cr := owner.do("POST", "/api/v1/apikeys", map[string]any{"name": "Claude Code", "scopes": []string{"see", "ask"}})
	if cr.Status != 201 {
		t.Fatalf("create key = %d %s", cr.Status, cr.Body)
	}
	body := cr.JSON(t)
	secret := body["secret"].(string)
	key := body["key"].(map[string]any)
	id := int64(key["id"].(float64))
	if !strings.HasPrefix(secret, "mq_"+key["prefix"].(string)+"_") {
		t.Errorf("secret %q does not match prefix %v", secret, key["prefix"])
	}

	// The list never shows the secret again.
	list := owner.send("GET", "/api/v1/apikeys", nil)
	if list.Status != 200 || strings.Contains(string(list.Body), secret) || strings.Contains(string(list.Body), "secret") {
		t.Errorf("list = %d %s", list.Status, list.Body)
	}
	if keys := list.JSON(t)["keys"].([]any); len(keys) != 1 {
		t.Errorf("keys = %v", keys)
	}

	// Bearer works, no cookie, no CSRF header needed (nothing to forge).
	script := a.newClient()
	me := script.send("GET", "/api/v1/me", nil, "Authorization", "Bearer "+secret)
	if me.Status != 200 {
		t.Fatalf("bearer /me = %d %s", me.Status, me.Body)
	}
	if m := me.JSON(t); m["via"] != "apikey" || m["user"].(map[string]any)["name"] != "Bailey" {
		t.Errorf("bearer /me = %v", m)
	}
	if script.cookie() != nil {
		t.Error("a bearer request set a cookie")
	}

	// Scopes are enforced: this key can see and ask but not touch settings, keys or invites.
	for _, c := range []struct{ method, path string }{
		{"GET", "/api/v1/settings/general"}, {"PUT", "/api/v1/settings/general"}, {"POST", "/api/v1/invites"},
	} {
		r := script.send(c.method, c.path, map[string]any{"role": "member"}, "Authorization", "Bearer "+secret)
		if r.Status != 403 || r.errCode(t) != "insufficient_scope" {
			t.Errorf("%s %s with a see+ask key: %d %s", c.method, c.path, r.Status, r.Body)
		}
	}
	// A key cannot manage keys at all, so it cannot mint a stronger one.
	r := script.send("POST", "/api/v1/apikeys", map[string]any{"name": "x", "scopes": []string{"see"}}, "Authorization", "Bearer "+secret)
	if r.Status != 403 || r.errCode(t) != "session_required" {
		t.Errorf("key creating key: %d %s", r.Status, r.Body)
	}

	// Revoke it; the same bearer token is now refused.
	if rv := owner.do("DELETE", "/api/v1/apikeys/"+itoa(id), nil); rv.Status != 204 {
		t.Fatalf("revoke = %d %s", rv.Status, rv.Body)
	}
	after := script.send("GET", "/api/v1/me", nil, "Authorization", "Bearer "+secret)
	if after.Status != 401 || after.errCode(t) != "invalid_api_key" {
		t.Errorf("after revoke: %d %s", after.Status, after.Body)
	}
	// Garbage keys and other Authorization schemes.
	if r := script.send("GET", "/api/v1/me", nil, "Authorization", "Bearer mq_abcdefgh_nope"); r.Status != 401 {
		t.Errorf("garbage key: %d", r.Status)
	}
	if r := script.send("GET", "/api/v1/me", nil, "Authorization", "Basic dXNlcjpwYXNz"); r.Status != 401 || r.errCode(t) != "unauthorized" {
		t.Errorf("Basic auth should simply be ignored: %d %s", r.Status, r.Body)
	}
	// Public endpoints do not care about a dead key.
	if r := script.send("GET", "/api/v1/setup", nil); r.Status != 200 {
		t.Errorf("setup status: %d", r.Status)
	}
}

func TestAPIKeyWithSettingsScopeCanReadSettings(t *testing.T) {
	a := newApp(t, nil)
	owner := a.owner()
	cr := owner.do("POST", "/api/v1/apikeys", map[string]any{"name": "ops", "scopes": []string{"settings"}})
	secret := cr.JSON(t)["secret"].(string)
	script := a.newClient()
	if r := script.send("GET", "/api/v1/settings/general", nil, "Authorization", "Bearer "+secret); r.Status != 200 {
		t.Errorf("settings via key: %d %s", r.Status, r.Body)
	}
}

func TestAPIKeyScopesFollowRole(t *testing.T) {
	a := newApp(t, nil)
	owner := a.owner()
	friend := a.member(owner, "Friend")

	for _, sc := range []string{"approve", "settings", "admin"} {
		r := friend.do("POST", "/api/v1/apikeys", map[string]any{"name": "k", "scopes": []string{sc}})
		if r.Status != 400 || r.errCode(t) != "scope_not_allowed" {
			t.Errorf("member asking for %q: %d %s", sc, r.Status, r.Body)
		}
	}
	if r := owner.do("POST", "/api/v1/apikeys", map[string]any{"name": "k", "scopes": []string{"warp"}}); r.Status != 400 || r.errCode(t) != "invalid_scope" {
		t.Errorf("unknown scope: %d %s", r.Status, r.Body)
	}
	if r := friend.do("POST", "/api/v1/apikeys", map[string]any{"name": "mine", "scopes": []string{"see", "ask"}}); r.Status != 201 {
		t.Errorf("member see+ask: %d %s", r.Status, r.Body)
	}
	// Keys are private to their person.
	if keys := owner.send("GET", "/api/v1/apikeys", nil).JSON(t)["keys"].([]any); len(keys) != 0 {
		t.Errorf("owner sees the friend's keys: %v", keys)
	}
	if r := owner.do("DELETE", "/api/v1/apikeys/1", nil); r.Status != 404 {
		t.Errorf("revoking someone else's key: %d", r.Status)
	}
	if r := owner.do("DELETE", "/api/v1/apikeys/abc", nil); r.Status != 404 {
		t.Errorf("bad id: %d", r.Status)
	}
}

// ---- invites ---------------------------------------------------------------

func TestInvitesOverHTTP(t *testing.T) {
	a := newApp(t, nil)
	owner := a.owner()

	if r := a.newClient().do("POST", "/api/v1/invites", map[string]string{"role": "member"}); r.Status != 401 {
		t.Errorf("anonymous invite: %d", r.Status)
	}
	inv := owner.do("POST", "/api/v1/invites", map[string]string{"role": "member"})
	if inv.Status != 201 {
		t.Fatalf("invite = %d %s", inv.Status, inv.Body)
	}
	im := inv.JSON(t)
	token := im["token"].(string)
	exp, err := time.Parse(time.RFC3339, im["expiresAt"].(string))
	if err != nil || time.Until(exp) < 6*24*time.Hour || time.Until(exp) > 7*24*time.Hour+time.Minute {
		t.Errorf("expiresAt = %v (%v), want about 7 days out", im["expiresAt"], err)
	}

	friend := a.newClient()
	r := friend.do("POST", "/api/v1/invites/accept", map[string]string{"token": token, "name": "Friend", "password": "friend password"})
	if r.Status != 201 || r.JSON(t)["user"].(map[string]any)["role"] != "member" {
		t.Fatalf("accept = %d %s", r.Status, r.Body)
	}
	if me := friend.send("GET", "/api/v1/me", nil); me.Status != 200 {
		t.Errorf("not signed in after accepting: %d", me.Status)
	}
	// One-time.
	r = a.newClient().do("POST", "/api/v1/invites/accept", map[string]string{"token": token, "name": "Other", "password": "other password"})
	if r.Status != 400 || r.errCode(t) != "invalid_invite" {
		t.Errorf("reuse: %d %s", r.Status, r.Body)
	}
	// Members cannot invite, and bad roles are refused.
	if r := friend.do("POST", "/api/v1/invites", map[string]string{"role": "member"}); r.Status != 403 {
		t.Errorf("member inviting: %d", r.Status)
	}
	if r := owner.do("POST", "/api/v1/invites", map[string]string{"role": "owner"}); r.Status != 400 || r.errCode(t) != "invalid_role" {
		t.Errorf("owner role: %d %s", r.Status, r.Body)
	}
}

// ---- settings --------------------------------------------------------------

func TestSettingsSecretMaskingOverHTTP(t *testing.T) {
	a := newApp(t, nil)
	owner := a.owner()
	const secret = "abcd1234efgh5678"

	put := owner.do("PUT", "/api/v1/settings/tmdb", map[string]any{"apiKey": secret, "language": "en-US"})
	if put.Status != 200 {
		t.Fatalf("PUT = %d %s", put.Status, put.Body)
	}
	if strings.Contains(string(put.Body), secret) {
		t.Fatalf("PUT response leaks the secret: %s", put.Body)
	}
	got := owner.send("GET", "/api/v1/settings/tmdb", nil)
	v := got.JSON(t)["value"].(map[string]any)
	if got.Status != 200 || v["apiKey"] != "••••5678" || v["language"] != "en-US" {
		t.Fatalf("GET = %d %s", got.Status, got.Body)
	}
	if strings.Contains(string(got.Body), secret) {
		t.Errorf("GET leaks the secret: %s", got.Body)
	}

	// In the database it is sealed, and other modules can still read it.
	var raw string
	if err := a.mainDB.QueryRow(`SELECT value FROM settings WHERE key = 'tmdb'`).Scan(&raw); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(raw, secret) || strings.Contains(raw, "5678") {
		t.Errorf("stored value is not sealed: %s", raw)
	}
	var cfg struct {
		APIKey string `json:"apiKey"`
	}
	if ok, err := a.settings.Get(context.Background(), "tmdb", &cfg); err != nil || !ok || cfg.APIKey != secret {
		t.Errorf("service Get = %+v, %v, %v", cfg, ok, err)
	}

	// Saving the masked form back keeps the secret.
	owner.do("PUT", "/api/v1/settings/tmdb", map[string]any{"apiKey": "••••5678", "language": "fr"})
	if ok, _ := a.settings.Get(context.Background(), "tmdb", &cfg); !ok || cfg.APIKey != secret {
		t.Errorf("secret lost after saving the masked value: %+v", cfg)
	}
}

func TestSettingsAreOwnerOnly(t *testing.T) {
	a := newApp(t, nil)
	owner := a.owner()
	friend := a.member(owner, "Friend")

	if r := a.newClient().send("GET", "/api/v1/settings/general", nil); r.Status != 401 {
		t.Errorf("anonymous GET: %d", r.Status)
	}
	if r := friend.send("GET", "/api/v1/settings/general", nil); r.Status != 403 || r.errCode(t) != "forbidden" {
		t.Errorf("member GET: %d %s", r.Status, r.Body)
	}
	if r := friend.do("PUT", "/api/v1/settings/general", map[string]any{"x": 1}); r.Status != 403 {
		t.Errorf("member PUT: %d", r.Status)
	}
	if r := owner.send("GET", "/api/v1/settings/general", nil); r.Status != 200 || len(r.JSON(t)["value"].(map[string]any)) != 0 {
		t.Errorf("owner GET unset: %d %s", r.Status, r.Body)
	}
	if r := owner.send("GET", "/api/v1/settings/Bad_Name", nil); r.Status != 404 {
		t.Errorf("bad section: %d", r.Status)
	}
	// Arrays and scalars are not sections.
	req, _ := http.NewRequest("PUT", a.srv.URL+"/api/v1/settings/general", strings.NewReader(`[1,2]`))
	req.Header.Set("X-Marquee", "1")
	resp, _ := owner.hc.Do(req)
	resp.Body.Close()
	if resp.StatusCode != 400 {
		t.Errorf("PUT array: %d", resp.StatusCode)
	}
}

// ---- events ----------------------------------------------------------------

func TestEventsStreamNeedsSignIn(t *testing.T) {
	a := newApp(t, nil)
	if r := a.newClient().send("GET", "/api/v1/events", nil); r.Status != 401 {
		t.Errorf("anonymous events: %d", r.Status)
	}
	owner := a.owner()

	req, _ := http.NewRequest("GET", a.srv.URL+"/api/v1/events", nil)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	resp, err := owner.hc.Do(req.WithContext(ctx))
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 || resp.Header.Get("Content-Type") != "text/event-stream" {
		t.Fatalf("events = %d %q", resp.StatusCode, resp.Header.Get("Content-Type"))
	}
	deadline := time.Now().Add(2 * time.Second)
	for a.hub.Len() == 0 && time.Now().Before(deadline) {
		time.Sleep(5 * time.Millisecond)
	}
	a.hub.Publish("downloads", map[string]int{"percent": 55})

	buf := make([]byte, 0, 512)
	tmp := make([]byte, 256)
	for !bytes.Contains(buf, []byte(`"percent":55`)) {
		n, err := resp.Body.Read(tmp)
		buf = append(buf, tmp[:n]...)
		if err != nil {
			t.Fatalf("stream ended: %v; got %q", err, buf)
		}
	}
	if !bytes.Contains(buf, []byte("event: downloads")) {
		t.Errorf("stream = %q", buf)
	}
}

// ---- static UI -------------------------------------------------------------

func TestSPAServedFromRootAndFallsBack(t *testing.T) {
	a := newApp(t, nil)
	c := a.newClient()

	for _, path := range []string{"/", "/index.html", "/movie/123", "/settings/connections", "/artist/Mr.Big", "/deep/er/route/"} {
		r := c.send("GET", path, nil)
		if r.Status != 200 || !strings.Contains(string(r.Body), "<title>Marquee</title>") {
			t.Errorf("%s = %d %q", path, r.Status, r.Body)
		}
		if ct := r.Header.Get("Content-Type"); ct != "text/html; charset=utf-8" {
			t.Errorf("%s Content-Type = %q", path, ct)
		}
		if cc := r.Header.Get("Cache-Control"); cc != "no-cache" {
			t.Errorf("%s Cache-Control = %q, want no-cache", path, cc)
		}
	}
	// A real asset is not replaced by the UI shell.
	r := c.send("GET", "/assets/app.js", nil)
	if r.Status != 200 || string(r.Body) != "console.log('app')" {
		t.Errorf("asset = %d %q", r.Status, r.Body)
	}
	if cc := r.Header.Get("Cache-Control"); cc != "public, max-age=31536000, immutable" {
		t.Errorf("asset Cache-Control = %q", cc)
	}
	if ct := r.Header.Get("Content-Type"); ct != "text/javascript; charset=utf-8" {
		t.Errorf("asset Content-Type = %q", ct)
	}
	// A missing asset is a 404, not index.html wearing an immutable header.
	if r := c.send("GET", "/assets/missing.js", nil); r.Status != 404 {
		t.Errorf("missing asset = %d", r.Status)
	}
	// API paths never fall through to the UI.
	if r := c.send("GET", "/api/v1/whatever", nil); r.Status != 404 || !strings.HasPrefix(r.Header.Get("Content-Type"), "application/json") {
		t.Errorf("api 404 = %d %s", r.Status, r.Header.Get("Content-Type"))
	}
	// Static files are GET/HEAD only.
	if r := c.send("POST", "/movie/1", nil, "X-Marquee", "1"); r.Status != http.StatusMethodNotAllowed {
		t.Errorf("POST to a UI path = %d", r.Status)
	}
}

func brotliBytes(t *testing.T, s string) []byte {
	t.Helper()
	return compressBR(t, []byte(s))
}

func TestBrotliNegotiation(t *testing.T) {
	const js = "export const answer = 42; // " + "padding padding padding padding padding padding "
	ui := fstest.MapFS{
		"index.html":       {Data: []byte("<title>Marquee</title> raw")},
		"index.html.br":    {Data: brotliBytes(t, "<title>Marquee</title> from br")},
		"assets/app.js.br": {Data: brotliBytes(t, js)},       // br only: the build deleted the original
		"assets/style.css": {Data: []byte("body{margin:0}")}, // raw only: too small to compress
		"favicon.svg":      {Data: []byte("<svg/>")},
	}
	a := newApp(t, ui)

	get := func(path, acceptEncoding string) reply {
		var h []string
		if acceptEncoding != "\x00" {
			h = []string{"Accept-Encoding", acceptEncoding}
		}
		// A transport that does not add or undo any encoding.
		req, _ := http.NewRequest("GET", a.srv.URL+path, nil)
		for i := 0; i+1 < len(h); i += 2 {
			req.Header.Set(h[i], h[i+1])
		}
		resp, err := (&http.Client{Transport: &http.Transport{DisableCompression: true}}).Do(req)
		if err != nil {
			t.Fatal(err)
		}
		defer resp.Body.Close()
		b, _ := io.ReadAll(resp.Body)
		return reply{Status: resp.StatusCode, Header: resp.Header, Body: b}
	}

	// Browser accepts br: the .br bytes go out untouched, labelled with the
	// type of the original file.
	r := get("/assets/app.js", "gzip, deflate, br")
	if r.Status != 200 || r.Header.Get("Content-Encoding") != "br" {
		t.Fatalf("br accepted: %d, Content-Encoding %q", r.Status, r.Header.Get("Content-Encoding"))
	}
	if ct := r.Header.Get("Content-Type"); ct != "text/javascript; charset=utf-8" {
		t.Errorf("Content-Type = %q, want the original file's type", ct)
	}
	if string(decompressBR(t, r.Body)) != js {
		t.Error("br body does not decode to the original")
	}
	if r.Header.Get("Vary") != "Accept-Encoding" {
		t.Errorf("Vary = %q", r.Header.Get("Vary"))
	}
	if cl := r.Header.Get("Content-Length"); cl != itoa(int64(len(r.Body))) {
		t.Errorf("Content-Length = %q, body is %d bytes", cl, len(r.Body))
	}
	if cc := r.Header.Get("Cache-Control"); cc != "public, max-age=31536000, immutable" {
		t.Errorf("Cache-Control = %q", cc)
	}

	// Browser does not accept br (or says nothing): the server decompresses.
	for _, ae := range []string{"gzip", "identity", "\x00", "br;q=0", "br;q=0.0, gzip"} {
		r := get("/assets/app.js", ae)
		if r.Status != 200 || r.Header.Get("Content-Encoding") != "" || string(r.Body) != js {
			t.Errorf("Accept-Encoding %q: %d, Content-Encoding %q, body %q", ae, r.Status, r.Header.Get("Content-Encoding"), r.Body)
		}
		if r.Header.Get("Vary") != "Accept-Encoding" {
			t.Errorf("Accept-Encoding %q: Vary = %q", ae, r.Header.Get("Vary"))
		}
		if ct := r.Header.Get("Content-Type"); ct != "text/javascript; charset=utf-8" {
			t.Errorf("Accept-Encoding %q: Content-Type = %q", ae, ct)
		}
	}
	// "*" allows br; an explicit q=0 for br wins over "*".
	if r := get("/assets/app.js", "*"); r.Header.Get("Content-Encoding") != "br" {
		t.Errorf("* should allow br: %q", r.Header.Get("Content-Encoding"))
	}
	if r := get("/assets/app.js", "*, br;q=0"); r.Header.Get("Content-Encoding") != "" {
		t.Errorf("br;q=0 should beat *: %q", r.Header.Get("Content-Encoding"))
	}

	// index.html keeps both: br when accepted, the raw copy otherwise.
	if r := get("/", "br"); r.Header.Get("Content-Encoding") != "br" || string(decompressBR(t, r.Body)) != "<title>Marquee</title> from br" {
		t.Errorf("index with br: %q %q", r.Header.Get("Content-Encoding"), r.Body)
	}
	if r := get("/anything/spa", "gzip"); r.Header.Get("Content-Encoding") != "" || string(r.Body) != "<title>Marquee</title> raw" {
		t.Errorf("index without br: %q %q", r.Header.Get("Content-Encoding"), r.Body)
	}
	if r := get("/anything/spa", "br"); r.Header.Get("Content-Encoding") != "br" || r.Header.Get("Cache-Control") != "no-cache" {
		t.Errorf("SPA fallback with br: %q %q", r.Header.Get("Content-Encoding"), r.Header.Get("Cache-Control"))
	}

	// Raw-only files are sent raw even to a br browser, with Vary.
	r = get("/assets/style.css", "br")
	if r.Header.Get("Content-Encoding") != "" || string(r.Body) != "body{margin:0}" || r.Header.Get("Vary") != "Accept-Encoding" {
		t.Errorf("raw-only file: enc %q body %q vary %q", r.Header.Get("Content-Encoding"), r.Body, r.Header.Get("Vary"))
	}
	if ct := r.Header.Get("Content-Type"); ct != "text/css; charset=utf-8" {
		t.Errorf("css Content-Type = %q", ct)
	}
	if r := get("/favicon.svg", "br"); r.Header.Get("Content-Type") != "image/svg+xml" || r.Header.Get("Cache-Control") != "no-cache" {
		t.Errorf("favicon: %v", r.Header)
	}

	// The .br twins themselves are not addressable.
	for _, p := range []string{"/assets/app.js.br", "/index.html.br"} {
		if r := get(p, "br"); r.Status != 404 {
			t.Errorf("%s = %d, want 404", p, r.Status)
		}
	}
}

func TestStaticConditionalAndHead(t *testing.T) {
	ui := fstest.MapFS{
		"index.html":       {Data: []byte("<title>Marquee</title>")},
		"assets/app.js.br": {Data: brotliBytes(t, strings.Repeat("let x=1;", 200))},
	}
	a := newApp(t, ui)
	hc := &http.Client{Transport: &http.Transport{DisableCompression: true}}

	req, _ := http.NewRequest("GET", a.srv.URL+"/assets/app.js", nil)
	req.Header.Set("Accept-Encoding", "br")
	resp, err := hc.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	etag := resp.Header.Get("ETag")
	if etag == "" {
		t.Fatal("no ETag")
	}

	req, _ = http.NewRequest("GET", a.srv.URL+"/assets/app.js", nil)
	req.Header.Set("Accept-Encoding", "br")
	req.Header.Set("If-None-Match", etag)
	resp, _ = hc.Do(req)
	b, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if resp.StatusCode != 304 || len(b) != 0 {
		t.Errorf("conditional GET = %d with %d bytes", resp.StatusCode, len(b))
	}

	// The decompressed representation has a different tag.
	req, _ = http.NewRequest("GET", a.srv.URL+"/assets/app.js", nil)
	req.Header.Set("Accept-Encoding", "gzip")
	req.Header.Set("If-None-Match", etag)
	resp, _ = hc.Do(req)
	resp.Body.Close()
	if resp.StatusCode != 200 || resp.Header.Get("ETag") == etag {
		t.Errorf("identity representation reused the br ETag: %d %q", resp.StatusCode, resp.Header.Get("ETag"))
	}

	req, _ = http.NewRequest("HEAD", a.srv.URL+"/", nil)
	resp, _ = hc.Do(req)
	b, _ = io.ReadAll(resp.Body)
	resp.Body.Close()
	if resp.StatusCode != 200 || len(b) != 0 || resp.Header.Get("Content-Type") != "text/html; charset=utf-8" {
		t.Errorf("HEAD / = %d, %d bytes, %q", resp.StatusCode, len(b), resp.Header.Get("Content-Type"))
	}
}

func TestAcceptsBrotli(t *testing.T) {
	for header, want := range map[string]bool{
		"": false, "gzip": false, "br": true, "BR": true, "gzip, br": true, "gzip,deflate,br": true,
		"br;q=1": true, "br;q=0.5": true, "br;q=0": false, "br; q=0": false, "*": true, "*;q=0": false,
		"gzip, *": true, "*, br;q=0": false, "identity": false, "brr": false,
	} {
		if got := acceptsBrotli(header); got != want {
			t.Errorf("acceptsBrotli(%q) = %v, want %v", header, got, want)
		}
	}
}

func TestStaticPathSafety(t *testing.T) {
	a := newApp(t, testUI())
	hc := &http.Client{CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	for _, p := range []string{"/..%2f..%2fetc/passwd", "/%2e%2e/%2e%2e/secret", "/assets/..%5c..%5csecret", "/a%00b"} {
		resp, err := hc.Get(a.srv.URL + p)
		if err != nil {
			t.Fatal(err)
		}
		b, _ := io.ReadAll(resp.Body)
		resp.Body.Close()
		// Either refused, redirected to a clean path, or the harmless UI shell.
		if resp.StatusCode == 200 && !strings.Contains(string(b), "<title>Marquee</title>") {
			t.Errorf("%s served %q", p, b)
		}
	}
}

func TestPanicInHandlerBecomesJSON500(t *testing.T) {
	a := newApp(t, nil)
	s := New(Deps{Log: slog.New(slog.NewTextHandler(io.Discard, nil)), UI: testUI(), Hub: a.hub})
	h := s.recoverer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { panic("boom") }))
	rec := httptest.NewRecorder()
	h.ServeHTTP(&statusWriter{ResponseWriter: rec}, httptest.NewRequest("GET", "/", nil))
	if rec.Code != 500 || !strings.Contains(rec.Body.String(), `"code":"internal"`) || strings.Contains(rec.Body.String(), "boom") {
		t.Errorf("recovered response = %d %s", rec.Code, rec.Body.String())
	}
}

func TestRequestIDIsKeptWhenSane(t *testing.T) {
	a := newApp(t, nil)
	c := a.newClient()
	if r := c.send("GET", "/api/v1/health", nil, "X-Request-ID", "abc-123_XYZ"); r.Header.Get("X-Request-ID") != "abc-123_XYZ" {
		t.Errorf("sane id replaced: %q", r.Header.Get("X-Request-ID"))
	}
	if r := c.send("GET", "/api/v1/health", nil, "X-Request-ID", "bad id\twith junk"); r.Header.Get("X-Request-ID") == "bad id\twith junk" || r.Header.Get("X-Request-ID") == "" {
		t.Errorf("bad id kept or missing: %q", r.Header.Get("X-Request-ID"))
	}
}

func itoa(n int64) string { return strconv.FormatInt(n, 10) }
