package web

import (
	"context"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"path/filepath"
	"strings"
	"testing"

	"github.com/BaileyPillon/marquee/internal/accounts"
	"github.com/BaileyPillon/marquee/internal/core/db"
	"github.com/BaileyPillon/marquee/internal/core/events"
	"github.com/BaileyPillon/marquee/internal/core/secrets"
	"github.com/BaileyPillon/marquee/internal/settings"
)

// ---- parsing -------------------------------------------------------------------------

func TestParseProxyList(t *testing.T) {
	got, err := ParseProxyList([]string{"10.0.0.0/8", " 192.168.1.5 ", "", "10.1.2.3/8", "::1", "fd00::/8", "::ffff:172.16.0.9", "172.16.0.9"})
	if err != nil {
		t.Fatal(err)
	}
	var s []string
	for _, p := range got {
		s = append(s, p.String())
	}
	// 10.1.2.3/8 is the same range as 10.0.0.0/8, and an IPv4 address written as IPv6 is the IPv4 one.
	want := "10.0.0.0/8,192.168.1.5/32,::1/128,fd00::/8,172.16.0.9/32"
	if strings.Join(s, ",") != want {
		t.Errorf("got %v, want %s", s, want)
	}
	for _, bad := range []string{"not an address", "10.0.0.0/33", "10.0.0.256", "10.0.0.0/", "0.0.0.0/0", "::/0", "10.0.0.1-10.0.0.9", "example.com"} {
		if _, err := ParseProxyList([]string{"10.0.0.1", bad}); err == nil {
			t.Errorf("%q was accepted", bad)
		} else if !strings.Contains(err.Error(), bad) {
			t.Errorf("the error for %q does not name it: %v", bad, err)
		}
	}
	if got, err := ParseProxyList(nil); err != nil || len(got) != 0 {
		t.Errorf("nothing = %v, %v", got, err)
	}
	if got := SplitList("10.0.0.0/8, 192.168.1.5;fd00::/8\n172.16.0.1"); len(got) != 4 {
		t.Errorf("SplitList = %v", got)
	}
}

func trust(prefixes ...string) func(netip.Addr) bool {
	var list []netip.Prefix
	for _, p := range prefixes {
		list = append(list, netip.MustParsePrefix(p))
	}
	return func(a netip.Addr) bool {
		for _, p := range list {
			if p.Contains(a.Unmap()) {
				return true
			}
		}
		return false
	}
}

func TestOriginalClientIsTheNearestAddressThatIsNotAProxy(t *testing.T) {
	proxies := trust("10.0.0.0/8", "fd00::/8")
	cases := []struct {
		name   string
		header []string
		want   string
		ok     bool
	}{
		{"one hop", []string{"203.0.113.9"}, "203.0.113.9", true},
		{"through a proxy", []string{"203.0.113.9, 10.0.0.2"}, "203.0.113.9", true},
		// The visitor wrote their own address first; the proxy added the real one after it.
		{"a visitor's own claim is ignored", []string{"1.2.3.4, 203.0.113.9"}, "203.0.113.9", true},
		{"claim behind two proxies", []string{"1.2.3.4, 203.0.113.9, 10.0.0.7, 10.0.0.2"}, "203.0.113.9", true},
		{"several header lines", []string{"1.2.3.4", "203.0.113.9, 10.0.0.2"}, "203.0.113.9", true},
		{"every hop is a proxy", []string{"10.0.0.9, 10.0.0.2"}, "10.0.0.9", true},
		{"ipv6", []string{"2001:db8::7, fd00::2"}, "2001:db8::7", true},
		{"with a port", []string{"203.0.113.9:51234, 10.0.0.2"}, "203.0.113.9", true},
		{"ipv6 with a port", []string{"[2001:db8::7]:443"}, "2001:db8::7", true},
		{"ipv6 in brackets", []string{"[2001:db8::7]"}, "2001:db8::7", true},
		{"ipv4 as ipv6", []string{"::ffff:203.0.113.9"}, "203.0.113.9", true},
		{"spaces and empty entries", []string{" , 203.0.113.9 ,, 10.0.0.2 "}, "203.0.113.9", true},
		{"no header", nil, "", false},
		{"empty header", []string{""}, "", false},
		{"not an address", []string{"203.0.113.9, unknown"}, "", false},
		{"text", []string{"<script>"}, "", false},
		{"a name", []string{"example.com"}, "", false},
	}
	for _, c := range cases {
		got, ok := originalClient(c.header, proxies)
		if ok != c.ok || (ok && got.String() != c.want) {
			t.Errorf("%s: got %v %v, want %q %v", c.name, got, ok, c.want, c.ok)
		}
	}
}

func TestSchemeIsHTTPS(t *testing.T) {
	cases := map[string]bool{
		"https": true, "HTTPS": true, " https ": true, "http": false, "": false, "ftp": false, "httpss": false,
		"http, https": true, "https, http": false, // the nearest proxy's word counts: the last
	}
	for v, want := range cases {
		if got := schemeIsHTTPS([]string{v}); got != want {
			t.Errorf("schemeIsHTTPS(%q) = %v, want %v", v, got, want)
		}
	}
	if schemeIsHTTPS(nil) || !schemeIsHTTPS([]string{"http", "https"}) || schemeIsHTTPS([]string{"https", "http"}) {
		t.Error("several header lines: the last counts")
	}
}

// ---- the middleware, over real requests -------------------------------------------------------

// proxyApp is a server whose Deps the test can shape, on a temp database, with a
// way to see the address the server recorded for a session.
type proxyApp struct {
	*app
}

func newProxyApp(t *testing.T, mod func(*Deps)) *proxyApp {
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
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	box, err := secrets.LoadOrCreate(filepath.Join(dir, "marquee.key"), log)
	if err != nil {
		t.Fatal(err)
	}
	hub := events.NewHub(log)
	t.Cleanup(hub.Close)
	cfg := settings.New(mainDB, box)
	deps := Deps{
		Version: "9.9.9-test", Log: log, MainDB: mainDB, EventsDB: eventsDB, Hub: hub, Settings: cfg, UI: testUI(),
		Accounts: accounts.NewService(mainDB, accounts.Options{Logger: log, PasswordParams: lightParams}),
	}
	if mod != nil {
		mod(&deps)
	}
	srv := httptest.NewServer(New(deps).Handler())
	t.Cleanup(srv.Close)
	return &proxyApp{app: &app{t: t, srv: srv, mainDB: mainDB, settings: cfg, hub: hub}}
}

// lastSessionIP is the address stored with the newest session.
func (a *proxyApp) lastSessionIP() string {
	a.t.Helper()
	var ip string
	if err := a.mainDB.QueryRow(`SELECT ip FROM sessions ORDER BY created_at DESC, rowid DESC LIMIT 1`).Scan(&ip); err != nil {
		a.t.Fatal(err)
	}
	return ip
}

func setCookieOf(r reply) string { return strings.Join(r.Header.Values("Set-Cookie"), "; ") }

func (a *proxyApp) signIn(c *client, headers ...string) reply {
	a.t.Helper()
	return c.do("POST", "/api/v1/auth/login", map[string]string{"name": "Bailey", "password": ownerPW}, headers...)
}

// The proxy headers of a request that does not come from a trusted proxy mean nothing.
func TestForwardedHeadersAreIgnoredWithoutATrustedProxy(t *testing.T) {
	a := newProxyApp(t, nil)
	c := a.newClient()
	r := c.do("POST", "/api/v1/setup/owner", map[string]string{"name": "Bailey", "password": ownerPW},
		"X-Forwarded-For", "203.0.113.9", "X-Forwarded-Proto", "https")
	if r.Status != http.StatusCreated {
		t.Fatalf("setup = %d %s", r.Status, r.Body)
	}
	if ip := a.lastSessionIP(); ip != "127.0.0.1" {
		t.Errorf("the session was recorded for %q: a header from a stranger was believed", ip)
	}
	if cookie := setCookieOf(r); strings.Contains(cookie, "Secure") {
		t.Errorf("X-Forwarded-Proto: https from a stranger made the cookie Secure: %s", cookie)
	}
	r = a.signIn(a.newClient(), "X-Forwarded-For", "198.51.100.77, 10.0.0.1", "X-Forwarded-Proto", "https")
	if r.Status != 200 || strings.Contains(setCookieOf(r), "Secure") || a.lastSessionIP() != "127.0.0.1" {
		t.Errorf("sign-in = %d, cookie %s, ip %s", r.Status, setCookieOf(r), a.lastSessionIP())
	}

	// So the sign-in limit cannot be dodged by sending a different address each time.
	wrong := map[string]string{"name": "Bailey", "password": "wrong password"}
	limited := 0
	for i := 0; i < accounts.LoginMaxFailures+3; i++ {
		ip := "203.0.113." + itoa(int64(i+1))
		if r := a.newClient().do("POST", "/api/v1/auth/login", wrong, "X-Forwarded-For", ip); r.Status == http.StatusTooManyRequests {
			limited++
		}
	}
	if limited == 0 {
		t.Error("rotating X-Forwarded-For got round the sign-in limit")
	}
}

func TestForwardedHeadersAreBelievedFromATrustedProxy(t *testing.T) {
	for name, mod := range map[string]func(*Deps){
		"environment": func(d *Deps) { d.TrustedProxies = []string{"127.0.0.0/8"} },
		"address":     func(d *Deps) { d.TrustedProxies = []string{"127.0.0.1"} },
		"ipv6 and ipv4": func(d *Deps) {
			d.TrustedProxies = []string{"::1", "10.0.0.0/8", "127.0.0.1"}
		},
	} {
		t.Run(name, func(t *testing.T) {
			a := newProxyApp(t, mod)
			a.owner()
			r := a.signIn(a.newClient(), "X-Forwarded-For", "203.0.113.9", "X-Forwarded-Proto", "https")
			if r.Status != 200 {
				t.Fatalf("sign-in = %d %s", r.Status, r.Body)
			}
			if ip := a.lastSessionIP(); ip != "203.0.113.9" {
				t.Errorf("session ip = %q, want the visitor's", ip)
			}
			if cookie := setCookieOf(r); !strings.Contains(cookie, "Secure") {
				t.Errorf("a request the proxy says was https should get a Secure cookie: %s", cookie)
			}
			// http behind the proxy: not Secure, and no header at all: the proxy's own address.
			r = a.signIn(a.newClient(), "X-Forwarded-For", "203.0.113.10", "X-Forwarded-Proto", "http")
			if strings.Contains(setCookieOf(r), "Secure") || a.lastSessionIP() != "203.0.113.10" {
				t.Errorf("http: cookie %s, ip %s", setCookieOf(r), a.lastSessionIP())
			}
			r = a.signIn(a.newClient())
			if strings.Contains(setCookieOf(r), "Secure") || a.lastSessionIP() != "127.0.0.1" {
				t.Errorf("no headers: cookie %s, ip %s", setCookieOf(r), a.lastSessionIP())
			}
			// A visitor who writes their own address in front of the proxy's does not win.
			a.signIn(a.newClient(), "X-Forwarded-For", "1.2.3.4, 203.0.113.99")
			if ip := a.lastSessionIP(); ip != "203.0.113.99" {
				t.Errorf("a made-up first address won: %q", ip)
			}
			// A header that is not an address is not believed.
			a.signIn(a.newClient(), "X-Forwarded-For", "unknown")
			if ip := a.lastSessionIP(); ip != "127.0.0.1" {
				t.Errorf("garbage was believed: %q", ip)
			}
		})
	}
}

func TestOnlyAProxyThatIsListedIsTrusted(t *testing.T) {
	// 10.0.0.0/8 is trusted, but the test client connects from 127.0.0.1.
	a := newProxyApp(t, func(d *Deps) { d.TrustedProxies = []string{"10.0.0.0/8"} })
	a.owner()
	r := a.signIn(a.newClient(), "X-Forwarded-For", "203.0.113.9", "X-Forwarded-Proto", "https")
	if a.lastSessionIP() != "127.0.0.1" || strings.Contains(setCookieOf(r), "Secure") {
		t.Errorf("a proxy that is not the one connecting was trusted: ip %s, cookie %s", a.lastSessionIP(), setCookieOf(r))
	}
	// A bad entry in the environment trusts nobody (main refuses to start with one).
	b := newProxyApp(t, func(d *Deps) { d.TrustedProxies = []string{"127.0.0.1", "nonsense"} })
	b.owner()
	b.signIn(b.newClient(), "X-Forwarded-For", "203.0.113.9")
	if b.lastSessionIP() != "127.0.0.1" {
		t.Errorf("with an invalid list a header was believed: %s", b.lastSessionIP())
	}
}

func TestTrustedProxiesCanBeSetInTheSettings(t *testing.T) {
	a := newProxyApp(t, nil)
	owner := a.owner()
	headers := []string{"X-Forwarded-For", "203.0.113.9", "X-Forwarded-Proto", "https"}

	// Not trusted yet.
	a.signIn(a.newClient(), headers...)
	if a.lastSessionIP() != "127.0.0.1" {
		t.Fatal("trusted before it was set")
	}
	// The owner lists the proxy; it takes effect at once.
	r := owner.do("PUT", "/api/v1/settings/server", map[string]any{"trustedProxies": []string{"127.0.0.1/8", "10.1.2.3/8"}})
	if r.Status != 200 {
		t.Fatalf("PUT = %d %s", r.Status, r.Body)
	}
	list := obj(t, r.JSON(t), "value")["trustedProxies"].([]any)
	if len(list) != 2 || list[0] != "127.0.0.0/8" || list[1] != "10.0.0.0/8" {
		t.Errorf("stored as %v, want the canonical ranges", list)
	}
	sign := a.signIn(a.newClient(), headers...)
	if a.lastSessionIP() != "203.0.113.9" || !strings.Contains(setCookieOf(sign), "Secure") {
		t.Errorf("after trusting it: ip %s, cookie %s", a.lastSessionIP(), setCookieOf(sign))
	}
	// And taking it away takes effect at once too.
	owner.do("PUT", "/api/v1/settings/server", map[string]any{"trustedProxies": []string{}})
	a.signIn(a.newClient(), headers...)
	if a.lastSessionIP() != "127.0.0.1" {
		t.Errorf("still trusted after it was removed: %s", a.lastSessionIP())
	}

	// A comma separated string is accepted too; mistakes are refused and change nothing.
	if r := owner.do("PUT", "/api/v1/settings/server", map[string]any{"trustedProxies": "127.0.0.1, 10.0.0.0/8"}); r.Status != 200 ||
		len(obj(t, r.JSON(t), "value")["trustedProxies"].([]any)) != 2 {
		t.Errorf("a list as text = %d %s", r.Status, r.Body)
	}
	for name, body := range map[string]map[string]any{
		"not an address": {"trustedProxies": []string{"nonsense"}},
		"bad range":      {"trustedProxies": []string{"10.0.0.0/40"}},
		"everyone":       {"trustedProxies": []string{"0.0.0.0/0"}},
		"not text":       {"trustedProxies": []any{7}},
		"a number":       {"trustedProxies": 7},
		"too many":       {"trustedProxies": manyProxies()},
	} {
		if r := owner.do("PUT", "/api/v1/settings/server", body); r.Status != 400 || r.errCode(t) != "invalid_request" {
			t.Errorf("%s = %d %s", name, r.Status, r.Body)
		}
	}
	got := obj(t, owner.do("GET", "/api/v1/settings/server", nil).JSON(t), "value")
	if len(got["trustedProxies"].([]any)) != 2 {
		t.Errorf("a refused save changed the section: %v", got)
	}
}

func manyProxies() []string {
	var out []string
	for i := 0; i < maxTrustedProxies+1; i++ {
		out = append(out, "10.0."+itoa(int64(i))+".0/24")
	}
	return out
}

func TestOnlyTheOwnerChangesTheServerSettings(t *testing.T) {
	a := newProxyApp(t, nil)
	owner := a.owner()
	member := a.member(owner, "Mia")
	for _, c := range []*client{member, a.newClient()} {
		if r := c.do("PUT", "/api/v1/settings/server", map[string]any{"trustedProxies": []string{"127.0.0.1"}}); r.Status != 401 && r.Status != 403 {
			t.Errorf("PUT = %d", r.Status)
		}
	}
}

// ---- the public address and the MCP url ----------------------------------------------------------

func TestPublicURLReplacesTheRequestHostInTheMCPAddress(t *testing.T) {
	f := &fakeMCP{}
	a := newMCPApp(t, f)
	owner := a.owner()

	info := func(headers ...string) string {
		t.Helper()
		r := owner.do("GET", "/api/v1/mcp/info", nil, headers...)
		if r.Status != 200 {
			t.Fatalf("info = %d %s", r.Status, r.Body)
		}
		return r.JSON(t)["url"].(string)
	}
	if got := info(); got != a.srv.URL+"/mcp" {
		t.Errorf("no public address: %s", got)
	}
	// X-Forwarded-Proto from anybody is ignored.
	if got := info("X-Forwarded-Proto", "https"); got != a.srv.URL+"/mcp" {
		t.Errorf("an untrusted X-Forwarded-Proto changed the address: %s", got)
	}

	r := owner.do("PUT", "/api/v1/settings/server", map[string]any{"publicUrl": " https://marquee.example.com/ "})
	if r.Status != 200 || obj(t, r.JSON(t), "value")["publicUrl"] != "https://marquee.example.com" {
		t.Fatalf("PUT = %d %s", r.Status, r.Body)
	}
	if got := info(); got != "https://marquee.example.com/mcp" {
		t.Errorf("with a public address: %s", got)
	}
	if f.infoOrigin != "https://marquee.example.com" {
		t.Errorf("the service was told origin %q", f.infoOrigin)
	}
	// A path prefix is kept.
	owner.do("PUT", "/api/v1/settings/server", map[string]any{"publicUrl": "http://nas.local:8080/marquee"})
	if got := info(); got != "http://nas.local:8080/marquee/mcp" {
		t.Errorf("with a prefix: %s", got)
	}
	// Cleared: back to the request's own host.
	owner.do("PUT", "/api/v1/settings/server", map[string]any{"publicUrl": ""})
	if got := info(); got != a.srv.URL+"/mcp" {
		t.Errorf("cleared: %s", got)
	}
	// Mistakes.
	for name, v := range map[string]any{
		"no scheme":    "marquee.example.com",
		"wrong scheme": "ftp://marquee.example.com",
		"login":        "https://user:pw@marquee.example.com",
		"query":        "https://marquee.example.com/?a=b",
		"fragment":     "https://marquee.example.com/#x",
		"no host":      "https:///x",
		"spaces":       "https://marquee .example.com",
		"not text":     7,
	} {
		if r := owner.do("PUT", "/api/v1/settings/server", map[string]any{"publicUrl": v}); r.Status != 400 || r.errCode(t) != "invalid_request" {
			t.Errorf("%s = %d %s", name, r.Status, r.Body)
		}
	}
}

func TestAProxyThatIsTrustedMakesTheMCPAddressHTTPS(t *testing.T) {
	f := &fakeMCP{}
	a := newMCPApp(t, f)
	owner := a.owner()
	member := a.member(owner, "Mia")
	if r := owner.do("PUT", "/api/v1/settings/server", map[string]any{"trustedProxies": []string{"127.0.0.1"}}); r.Status != 200 {
		t.Fatalf("PUT = %d %s", r.Status, r.Body)
	}
	r := member.do("GET", "/api/v1/mcp/info", nil, "X-Forwarded-Proto", "https")
	if r.Status != 200 || !strings.HasPrefix(f.infoOrigin, "https://127.0.0.1:") {
		t.Errorf("behind a trusted proxy: %d, origin %q", r.Status, f.infoOrigin)
	}
	member.do("GET", "/api/v1/mcp/info", nil, "X-Forwarded-Proto", "http")
	if !strings.HasPrefix(f.infoOrigin, "http://127.0.0.1:") {
		t.Errorf("proxy says http: origin %q", f.infoOrigin)
	}
}
