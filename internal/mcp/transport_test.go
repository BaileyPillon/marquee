package mcp

import (
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/BaileyPillon/marquee/internal/accounts"
)

func memberKey(r *rig) string { return r.key(r.mia, accounts.ScopeSee, accounts.ScopeAsk) }

func initParams(version string) map[string]any {
	return map[string]any{
		"protocolVersion": version,
		"capabilities":    map[string]any{},
		"clientInfo":      map[string]any{"name": "test-client", "version": "1.0"},
	}
}

func TestInitializeHandshake(t *testing.T) {
	r := newRig(t)
	key := memberKey(r)

	rep := r.rpc(key, "initialize", initParams("2025-06-18"))
	if rep.Status != http.StatusOK {
		t.Fatalf("initialize = %d %s", rep.Status, rep.Body)
	}
	if ct := rep.Header.Get("Content-Type"); !strings.HasPrefix(ct, "application/json") {
		t.Errorf("Content-Type = %q, want application/json", ct)
	}
	session := rep.Header.Get("Mcp-Session-Id")
	if !strings.HasPrefix(session, "mqs_") || len(session) < 20 {
		t.Errorf("Mcp-Session-Id = %q", session)
	}
	body := rep.JSON(t)
	if body["jsonrpc"] != "2.0" {
		t.Errorf("jsonrpc = %v", body["jsonrpc"])
	}
	res := rep.result(t)
	if res["protocolVersion"] != "2025-06-18" {
		t.Errorf("protocolVersion = %v, want the one the client asked for", res["protocolVersion"])
	}
	caps, _ := res["capabilities"].(map[string]any)
	if _, ok := caps["tools"].(map[string]any); !ok || len(caps) != 1 {
		t.Errorf("capabilities = %v, want {tools:{}}", res["capabilities"])
	}
	info, _ := res["serverInfo"].(map[string]any)
	if info["name"] != "marquee" || info["version"] != "9.9.9-test" {
		t.Errorf("serverInfo = %v", info)
	}
	if s, _ := res["instructions"].(string); !strings.Contains(s, "cannot approve") {
		t.Errorf("instructions = %q", s)
	}

	// The client says it is ready: a notification, accepted without a body.
	rep = r.post(key, map[string]any{"jsonrpc": "2.0", "method": "notifications/initialized"},
		"Mcp-Session-Id", session, "MCP-Protocol-Version", "2025-06-18")
	if rep.Status != http.StatusAccepted || len(rep.Body) != 0 {
		t.Errorf("notifications/initialized = %d %q, want 202 and no body", rep.Status, rep.Body)
	}

	// The session id is accepted afterwards, and not issued again.
	rep = r.rpc(key, "tools/list", nil, "Mcp-Session-Id", session, "MCP-Protocol-Version", "2025-06-18")
	if rep.Status != http.StatusOK || rep.Header.Get("Mcp-Session-Id") != "" {
		t.Errorf("tools/list with a session = %d, session header %q", rep.Status, rep.Header.Get("Mcp-Session-Id"))
	}
	if rep = r.rpc(key, "ping", nil, "Mcp-Session-Id", session); rep.Status != http.StatusOK || len(rep.result(t)) != 0 {
		t.Errorf("ping = %d %s, want an empty result", rep.Status, rep.Body)
	}
	// Each handshake gets its own session.
	other := r.rpc(key, "initialize", initParams("2025-06-18")).Header.Get("Mcp-Session-Id")
	if other == "" || other == session {
		t.Errorf("second session id = %q (first %q)", other, session)
	}
}

func TestProtocolVersionNegotiation(t *testing.T) {
	r := newRig(t)
	key := memberKey(r)
	for _, tc := range []struct{ asked, want string }{
		{"2025-11-25", "2025-11-25"},
		{"2025-06-18", "2025-06-18"},
		{"2025-03-26", "2025-03-26"},
		{"2024-11-05", "2024-11-05"},
		{"2099-01-01", "2025-11-25"}, // newer than we know: our newest with a handshake
		{"1999-01-01", "2025-11-25"},
		{"2026-07-28", "2025-11-25"}, // that revision has no handshake
		{"nonsense", "2025-11-25"},
	} {
		got := r.rpc(key, "initialize", initParams(tc.asked)).result(t)["protocolVersion"]
		if got != tc.want {
			t.Errorf("initialize(%q) answered %v, want %q", tc.asked, got, tc.want)
		}
	}
	// protocolVersion is required.
	rep := r.rpc(key, "initialize", map[string]any{"capabilities": map[string]any{}})
	if rep.errCode(t) != codeInvalidParams {
		t.Errorf("initialize without a version: %s", rep.Body)
	}
	// An MCP-Protocol-Version header the server does not know is refused.
	rep = r.rpc(key, "tools/list", nil, "MCP-Protocol-Version", "1999-01-01")
	if rep.Status != http.StatusBadRequest || rep.errCode(t) != codeUnsupportedVersion {
		t.Errorf("unknown version header = %d %s", rep.Status, rep.Body)
	}
	data, _ := rep.rpcError(t)["data"].(map[string]any)
	if sup, _ := data["supported"].([]any); len(sup) != 5 || sup[0] != "2026-07-28" {
		t.Errorf("supported = %v", data["supported"])
	}
}

func TestSessions(t *testing.T) {
	r := newRig(t)
	mia, noah := memberKey(r), r.key(r.noah, accounts.ScopeSee)
	session := r.rpc(mia, "initialize", initParams("2025-11-25")).Header.Get("Mcp-Session-Id")

	// Another key cannot use somebody's session.
	if rep := r.rpc(noah, "ping", nil, "Mcp-Session-Id", session); rep.Status != http.StatusNotFound {
		t.Errorf("a session of another key = %d, want 404", rep.Status)
	}
	// An id Marquee never issued (or forgot in a restart) is just ignored.
	if rep := r.rpc(mia, "ping", nil, "Mcp-Session-Id", "mqs_unknown"); rep.Status != http.StatusOK {
		t.Errorf("unknown session id = %d, want it ignored", rep.Status)
	}
	// No header at all works too (curl, scripts).
	if rep := r.rpc(mia, "ping", nil); rep.Status != http.StatusOK {
		t.Errorf("no session = %d", rep.Status)
	}

	// DELETE ends it; after that the id is refused.
	if rep := r.send("DELETE", "/mcp", noah, nil, "Mcp-Session-Id", session); rep.Status != http.StatusNotFound {
		t.Errorf("DELETE by another key = %d, want 404", rep.Status)
	}
	if rep := r.send("DELETE", "/mcp", mia, nil, "Mcp-Session-Id", session); rep.Status != http.StatusNoContent {
		t.Errorf("DELETE = %d, want 204", rep.Status)
	}
	if rep := r.rpc(mia, "ping", nil, "Mcp-Session-Id", session); rep.Status != http.StatusNotFound {
		t.Errorf("an ended session = %d, want 404", rep.Status)
	}
	if rep := r.send("DELETE", "/mcp", mia, nil); rep.Status != http.StatusNotFound {
		t.Errorf("DELETE without a session = %d, want 404", rep.Status)
	}
}

func TestOriginIsChecked(t *testing.T) {
	r := newRig(t)
	key := memberKey(r)
	body := []byte(`{"jsonrpc":"2.0","id":1,"method":"ping"}`)

	for _, origin := range []string{"http://evil.example", "https://127.0.0.1", "null", "http://127.0.0.1.evil.example", "ftp://127.0.0.1"} {
		rep := r.send("POST", "/mcp", key, body, "Origin", origin)
		if rep.Status != http.StatusForbidden {
			t.Errorf("Origin %q = %d, want 403", origin, rep.Status)
		}
		if rep.Status == http.StatusForbidden && rep.errCode(t) == 0 {
			t.Errorf("Origin %q: the 403 has no JSON-RPC error: %s", origin, rep.Body)
		}
	}
	// The server's own origin passes, and so does no Origin (every non-browser client).
	if rep := r.send("POST", "/mcp", key, body, "Origin", r.srv.URL); rep.Status != http.StatusOK {
		t.Errorf("own Origin = %d %s", rep.Status, rep.Body)
	}
	if rep := r.send("POST", "/mcp", key, body); rep.Status != http.StatusOK {
		t.Errorf("no Origin = %d", rep.Status)
	}
	// It is checked before the key, so a rebinding page learns nothing.
	if rep := r.send("POST", "/mcp", "", body, "Origin", "http://evil.example"); rep.Status != http.StatusForbidden {
		t.Errorf("Origin without a key = %d, want 403", rep.Status)
	}
	// The owner can allow another origin.
	if err := r.cfg.Set(bg(), SettingsSection, map[string]any{"enabled": true, "allowedOrigins": []string{"https://claude.example/"}}); err != nil {
		t.Fatal(err)
	}
	if rep := r.send("POST", "/mcp", key, body, "Origin", "https://claude.example"); rep.Status != http.StatusOK {
		t.Errorf("a listed Origin = %d, want 200", rep.Status)
	}
	if rep := r.send("POST", "/mcp", key, body, "Origin", "https://other.example"); rep.Status != http.StatusForbidden {
		t.Errorf("an unlisted Origin = %d, want 403", rep.Status)
	}
}

func TestAuthentication(t *testing.T) {
	r := newRig(t)
	body := []byte(`{"jsonrpc":"2.0","id":1,"method":"tools/list"}`)

	check := func(name, key string, headers ...string) {
		t.Helper()
		rep := r.send("POST", "/mcp", key, body, headers...)
		if rep.Status != http.StatusUnauthorized {
			t.Errorf("%s = %d, want 401 (%s)", name, rep.Status, rep.Body)
			return
		}
		if c := rep.Header.Get("WWW-Authenticate"); !strings.HasPrefix(c, "Bearer realm=") {
			t.Errorf("%s: WWW-Authenticate = %q", name, c)
		}
		rep.rpcError(t)
	}
	check("no key", "")
	check("not a Marquee key", "sk-ant-not-ours")
	check("a made-up key", "mq_abcdefgh_ijklmnopqrstuvwxyz")
	check("a session cookie and no key", "", "Cookie", "marquee_session=whatever")
	check("a non-bearer Authorization", "", "Authorization", "Basic Zm9vOmJhcg==")

	if c := r.send("POST", "/mcp", "mq_abcdefgh_ijklmnopqrstuvwxyz", body).Header.Get("WWW-Authenticate"); !strings.Contains(c, `error="invalid_token"`) {
		t.Errorf("an invalid key's challenge = %q", c)
	}
	if c := r.send("POST", "/mcp", "", body).Header.Get("WWW-Authenticate"); strings.Contains(c, "error=") {
		t.Errorf("a missing key's challenge = %q, want no error code", c)
	}

	// A revoked key and a disabled owner stop working.
	k, secret, err := r.acc.CreateAPIKey(bg(), r.mia, "short lived", []string{"see"})
	if err != nil {
		t.Fatal(err)
	}
	if rep := r.send("POST", "/mcp", secret, body); rep.Status != http.StatusOK {
		t.Fatalf("a fresh key = %d %s", rep.Status, rep.Body)
	}
	if err := r.acc.RevokeAPIKey(bg(), r.mia, k.ID); err != nil {
		t.Fatal(err)
	}
	check("a revoked key", secret)
}

func TestKeyOwnerSession(t *testing.T) {
	// The server runs as the key's owner: a signed-in browser session means nothing here.
	r := newRig(t)
	login, err := r.acc.SignIn(bg(), "Bailey", "correct horse", accounts.ClientInfo{})
	if err != nil {
		t.Fatal(err)
	}
	rep := r.send("POST", "/mcp", "", []byte(`{"jsonrpc":"2.0","id":1,"method":"tools/list"}`),
		"Cookie", "marquee_session="+login.Token, "X-Marquee", "1")
	if rep.Status != http.StatusUnauthorized {
		t.Errorf("a session cookie = %d, want 401", rep.Status)
	}
}

func TestOnlyPostAndDelete(t *testing.T) {
	r := newRig(t)
	key := memberKey(r)
	for _, method := range []string{"GET", "PUT", "PATCH"} {
		rep := r.send(method, "/mcp", key, nil)
		if rep.Status != http.StatusMethodNotAllowed {
			t.Errorf("%s /mcp = %d, want 405", method, rep.Status)
		}
		if allow := rep.Header.Get("Allow"); !strings.Contains(allow, "POST") {
			t.Errorf("%s: Allow = %q", method, allow)
		}
	}
	// GET asks for an event stream: with a key that is 405, without one it is 401.
	if rep := r.send("GET", "/mcp", "", nil, "Accept", "text/event-stream"); rep.Status != http.StatusUnauthorized {
		t.Errorf("GET without a key = %d, want 401", rep.Status)
	}
}

func TestBothEndpointsAnswer(t *testing.T) {
	r := newRig(t)
	key := memberKey(r)
	for _, path := range []string{"/mcp", "/api/v1/mcp"} {
		// No X-Marquee header: this is not the browser API, so there is no CSRF rule.
		rep := r.send("POST", path, key, []byte(`{"jsonrpc":"2.0","id":7,"method":"ping"}`))
		if rep.Status != http.StatusOK || rep.JSON(t)["id"] != float64(7) {
			t.Errorf("POST %s = %d %s", path, rep.Status, rep.Body)
		}
		if rep.Header.Get("Cache-Control") != "no-store" {
			t.Errorf("POST %s: Cache-Control = %q", path, rep.Header.Get("Cache-Control"))
		}
	}
}

func TestBatch(t *testing.T) {
	r := newRig(t)
	key := memberKey(r)

	rep := r.send("POST", "/mcp", key, []byte(`[
		{"jsonrpc":"2.0","id":1,"method":"ping"},
		{"jsonrpc":"2.0","method":"notifications/initialized"},
		{"jsonrpc":"2.0","id":"b","method":"tools/list"},
		{"jsonrpc":"2.0","id":3,"method":"nope/nope"},
		{"jsonrpc":"2.0","id":4,"method":"tools/call","params":{"name":"health","arguments":{}}}
	]`))
	if rep.Status != http.StatusOK {
		t.Fatalf("batch = %d %s", rep.Status, rep.Body)
	}
	got := rep.JSONArray(t)
	if len(got) != 4 {
		t.Fatalf("%d replies, want 4 (the notification gets none): %s", len(got), rep.Body)
	}
	if got[0]["id"] != float64(1) || got[1]["id"] != "b" || got[2]["id"] != float64(3) || got[3]["id"] != float64(4) {
		t.Errorf("ids = %v %v %v %v, want 1 b 3 4 in order", got[0]["id"], got[1]["id"], got[2]["id"], got[3]["id"])
	}
	if got[1]["result"].(map[string]any)["tools"] == nil {
		t.Errorf("tools/list in a batch: %v", got[1])
	}
	if e, _ := got[2]["error"].(map[string]any); e == nil || e["code"] != float64(codeMethodNotFound) {
		t.Errorf("an unknown method in a batch: %v", got[2])
	}
	if res, _ := got[3]["result"].(map[string]any); res == nil || res["isError"] != false {
		t.Errorf("tools/call in a batch: %v", got[3])
	}

	// A batch of notifications only is accepted without a body.
	rep = r.send("POST", "/mcp", key, []byte(`[{"jsonrpc":"2.0","method":"notifications/initialized"},{"jsonrpc":"2.0","method":"notifications/cancelled"}]`))
	if rep.Status != http.StatusAccepted || len(rep.Body) != 0 {
		t.Errorf("notification batch = %d %q", rep.Status, rep.Body)
	}
	// An empty batch is not a request.
	rep = r.send("POST", "/mcp", key, []byte(`[]`))
	if rep.Status != http.StatusBadRequest || rep.errCode(t) != codeInvalidRequest {
		t.Errorf("empty batch = %d %s", rep.Status, rep.Body)
	}
	// An element that is not a request is answered, and the others still are.
	rep = r.send("POST", "/mcp", key, []byte(`[1, {"jsonrpc":"2.0","id":2,"method":"ping"}]`))
	got = rep.JSONArray(t)
	if len(got) != 2 || got[0]["error"].(map[string]any)["code"] != float64(codeInvalidRequest) || got[1]["id"] != float64(2) {
		t.Errorf("a bad element = %s", rep.Body)
	}
}

func TestMalformedRequests(t *testing.T) {
	r := newRig(t)
	key := memberKey(r)
	for _, tc := range []struct {
		name   string
		body   string
		status int
		code   int
	}{
		{"not JSON", `{"jsonrpc":`, 400, codeParseError},
		{"empty", ``, 400, codeParseError},
		{"wrong version", `{"jsonrpc":"1.0","id":1,"method":"ping"}`, 400, codeInvalidRequest},
		{"no method", `{"jsonrpc":"2.0","id":1}`, 400, codeInvalidRequest},
		{"null id", `{"jsonrpc":"2.0","id":null,"method":"ping"}`, 400, codeInvalidRequest},
		{"unknown method", `{"jsonrpc":"2.0","id":1,"method":"resources/list"}`, 200, codeMethodNotFound},
	} {
		rep := r.send("POST", "/mcp", key, []byte(tc.body))
		if rep.Status != tc.status || rep.errCode(t) != tc.code {
			t.Errorf("%s = %d %s, want %d / %d", tc.name, rep.Status, rep.Body, tc.status, tc.code)
		}
	}
	// A answer from the client (Marquee never sends requests) is accepted and ignored.
	rep := r.send("POST", "/mcp", key, []byte(`{"jsonrpc":"2.0","id":1,"result":{}}`))
	if rep.Status != http.StatusAccepted {
		t.Errorf("a client response = %d, want 202", rep.Status)
	}
	// A body over the limit is refused.
	rep = r.send("POST", "/mcp", key, []byte(`{"jsonrpc":"2.0","id":1,"method":"ping","params":{"x":"`+strings.Repeat("a", maxBody)+`"}}`))
	if rep.Status != http.StatusRequestEntityTooLarge {
		t.Errorf("a huge body = %d, want 413", rep.Status)
	}
}

func TestSwitchedOff(t *testing.T) {
	r := newRig(t)
	key := memberKey(r)
	body := []byte(`{"jsonrpc":"2.0","id":1,"method":"ping"}`)

	// Never saved: on by default.
	if !r.mcp.Enabled(bg()) {
		t.Fatal("the server should be on until the owner switches it off")
	}
	if err := r.cfg.Set(bg(), SettingsSection, map[string]any{"enabled": false}); err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{"/mcp", "/api/v1/mcp"} {
		for _, method := range []string{"POST", "GET", "DELETE"} {
			rep := r.send(method, path, key, body)
			if rep.Status != http.StatusNotFound {
				t.Errorf("%s %s while off = %d, want 404", method, path, rep.Status)
			}
		}
		// It does not even ask who is calling.
		if rep := r.send("POST", path, "", body); rep.Status != http.StatusNotFound {
			t.Errorf("POST %s with no key while off = %d, want 404", path, rep.Status)
		}
	}
	if r.mcp.Info(bg(), r.owner, "http://x").Enabled {
		t.Error("Info says enabled while off")
	}

	// Back on: at once, no restart.
	if err := r.cfg.Set(bg(), SettingsSection, map[string]any{"enabled": true}); err != nil {
		t.Fatal(err)
	}
	if rep := r.send("POST", "/mcp", key, body); rep.Status != http.StatusOK {
		t.Errorf("after switching on = %d", rep.Status)
	}
}

func TestRateLimitPerKey(t *testing.T) {
	r := newRig(t)
	a, b := memberKey(r), r.key(r.noah, accounts.ScopeSee)

	for i := range CallsPerMinute {
		if rep := r.rpc(a, "ping", nil); rep.Status != http.StatusOK {
			t.Fatalf("call %d = %d %s", i+1, rep.Status, rep.Body)
		}
	}
	rep := r.rpc(a, "tools/list", nil)
	if rep.Status != http.StatusTooManyRequests {
		t.Fatalf("call %d = %d, want 429", CallsPerMinute+1, rep.Status)
	}
	if rep.errCode(t) != codeRateLimited {
		t.Errorf("code = %d, want %d", rep.errCode(t), codeRateLimited)
	}
	if !strings.Contains(rep.rpcError(t)["message"].(string), "Too many calls") {
		t.Errorf("message = %v", rep.rpcError(t)["message"])
	}
	if ra := rep.Header.Get("Retry-After"); ra == "" || ra == "0" {
		t.Errorf("Retry-After = %q", ra)
	}
	// The same JSON-RPC id comes back on the error.
	if rep.JSON(t)["id"] == nil {
		t.Errorf("the rate-limit error has no id: %s", rep.Body)
	}
	// Another key has its own allowance.
	if rep := r.rpc(b, "ping", nil); rep.Status != http.StatusOK {
		t.Errorf("another key = %d", rep.Status)
	}
	// Notifications are not calls.
	if rep := r.post(a, map[string]any{"jsonrpc": "2.0", "method": "notifications/initialized"}); rep.Status != http.StatusAccepted {
		t.Errorf("a notification while limited = %d", rep.Status)
	}
	// The window slides: a minute later the key is free again.
	r.clock.Advance(61 * time.Second)
	if rep := r.rpc(a, "ping", nil); rep.Status != http.StatusOK {
		t.Errorf("after the minute = %d %s", rep.Status, rep.Body)
	}
}

func TestRateLimitInABatch(t *testing.T) {
	r := newRig(t, func(d *Deps) { d.CallsPerMinute = 2 })
	key := memberKey(r)
	rep := r.send("POST", "/mcp", key, []byte(`[
		{"jsonrpc":"2.0","id":1,"method":"ping"},
		{"jsonrpc":"2.0","id":2,"method":"ping"},
		{"jsonrpc":"2.0","id":3,"method":"ping"}]`))
	got := rep.JSONArray(t)
	if rep.Status != 200 || len(got) != 3 || got[2]["error"].(map[string]any)["code"] != float64(codeRateLimited) || got[1]["result"] == nil {
		t.Errorf("batch over the limit = %d %s", rep.Status, rep.Body)
	}
}

// ---- revision 2026-07-28 ----------------------------------------------------------------

func modernParams(extra map[string]any) map[string]any {
	p := map[string]any{"_meta": map[string]any{
		"io.modelcontextprotocol/protocolVersion":    "2026-07-28",
		"io.modelcontextprotocol/clientInfo":         map[string]any{"name": "test-client", "version": "1"},
		"io.modelcontextprotocol/clientCapabilities": map[string]any{},
	}}
	for k, v := range extra {
		p[k] = v
	}
	return p
}

func modernHeaders(method, name string) []string {
	h := []string{"MCP-Protocol-Version", "2026-07-28", "Mcp-Method", method}
	if name != "" {
		h = append(h, "Mcp-Name", name)
	}
	return h
}

func TestModernEra(t *testing.T) {
	r := newRig(t)
	key := memberKey(r)

	// server/discover.
	rep := r.rpc(key, "server/discover", modernParams(nil), modernHeaders("server/discover", "")...)
	if rep.Status != 200 {
		t.Fatalf("server/discover = %d %s", rep.Status, rep.Body)
	}
	res := rep.result(t)
	if res["resultType"] != "complete" || res["cacheScope"] != "public" || res["ttlMs"] == nil {
		t.Errorf("discover = %v", res)
	}
	if v, _ := res["supportedVersions"].([]any); len(v) < 2 || v[0] != "2026-07-28" {
		t.Errorf("supportedVersions = %v", res["supportedVersions"])
	}
	meta, _ := res["_meta"].(map[string]any)
	if si, _ := meta["io.modelcontextprotocol/serverInfo"].(map[string]any); si["name"] != "marquee" {
		t.Errorf("serverInfo = %v", meta)
	}
	if rep.Header.Get("Mcp-Session-Id") != "" {
		t.Error("the modern era has no sessions")
	}

	// tools/list: private to the key, with a TTL.
	rep = r.rpc(key, "tools/list", modernParams(nil), modernHeaders("tools/list", "")...)
	res = rep.result(t)
	if res["resultType"] != "complete" || res["cacheScope"] != "private" || res["ttlMs"] == nil || len(res["tools"].([]any)) != 6 {
		t.Errorf("modern tools/list = %v", res)
	}

	// tools/call carries Mcp-Name; the result says what kind it is.
	rep = r.rpc(key, "tools/call", modernParams(map[string]any{"name": "health", "arguments": map[string]any{}}), modernHeaders("tools/call", "health")...)
	res = rep.result(t)
	if rep.Status != 200 || res["resultType"] != "complete" || res["isError"] != false || res["structuredContent"] == nil {
		t.Errorf("modern tools/call = %d %v", rep.Status, res)
	}
	// A name that needs encoding may come as =?base64?...?=.
	rep = r.rpc(key, "tools/call", modernParams(map[string]any{"name": "health", "arguments": map[string]any{}}),
		modernHeaders("tools/call", "=?base64?aGVhbHRo?=")...)
	if rep.Status != 200 {
		t.Errorf("base64 Mcp-Name = %d %s", rep.Status, rep.Body)
	}

	for _, tc := range []struct {
		name    string
		method  string
		params  map[string]any
		headers []string
		status  int
		code    int
	}{
		{"no version header", "tools/list", modernParams(nil), []string{"Mcp-Method", "tools/list"}, 400, codeHeaderMismatch},
		{"a version header that differs", "tools/list", modernParams(nil), []string{"MCP-Protocol-Version", "2025-11-25", "Mcp-Method", "tools/list"}, 400, codeHeaderMismatch},
		{"no Mcp-Method", "tools/list", modernParams(nil), []string{"MCP-Protocol-Version", "2026-07-28"}, 400, codeHeaderMismatch},
		{"a wrong Mcp-Method", "tools/list", modernParams(nil), modernHeaders("ping", ""), 400, codeHeaderMismatch},
		{"no Mcp-Name", "tools/call", modernParams(map[string]any{"name": "health"}), modernHeaders("tools/call", ""), 400, codeHeaderMismatch},
		{"a wrong Mcp-Name", "tools/call", modernParams(map[string]any{"name": "health"}), modernHeaders("tools/call", "calendar"), 400, codeHeaderMismatch},
	} {
		rep := r.rpc(key, tc.method, tc.params, tc.headers...)
		if rep.Status != tc.status || rep.errCode(t) != tc.code {
			t.Errorf("%s = %d %s, want %d / %d", tc.name, rep.Status, rep.Body, tc.status, tc.code)
		}
	}

	// An unknown version in _meta: a modern error that lists what is supported.
	p := modernParams(nil)
	p["_meta"].(map[string]any)["io.modelcontextprotocol/protocolVersion"] = "2027-01-01"
	rep = r.rpc(key, "tools/list", p, "MCP-Protocol-Version", "2027-01-01")
	if rep.Status != 400 || rep.errCode(t) != codeUnsupportedVersion {
		// the header version is unknown too: refused before the body is looked at
		t.Errorf("unknown version = %d %s", rep.Status, rep.Body)
	}
	rep = r.rpc(key, "tools/list", p, "MCP-Protocol-Version", "2026-07-28")
	if rep.Status != 400 || rep.errCode(t) != codeUnsupportedVersion {
		t.Errorf("unknown _meta version = %d %s", rep.Status, rep.Body)
	}

	// Client capabilities are required.
	p = modernParams(nil)
	delete(p["_meta"].(map[string]any), "io.modelcontextprotocol/clientCapabilities")
	rep = r.rpc(key, "tools/list", p, modernHeaders("tools/list", "")...)
	if rep.Status != 400 || rep.errCode(t) != codeInvalidParams {
		t.Errorf("no capabilities = %d %s", rep.Status, rep.Body)
	}

	// The header says 2026-07-28 but the body has no _meta.
	rep = r.rpc(key, "tools/list", nil, modernHeaders("tools/list", "")...)
	if rep.Status != 400 || rep.errCode(t) != codeInvalidParams {
		t.Errorf("header without _meta = %d %s", rep.Status, rep.Body)
	}

	// An unknown method is a 404 with a JSON-RPC body, which is how a client tells us from a legacy server.
	rep = r.rpc(key, "resources/list", modernParams(nil), modernHeaders("resources/list", "")...)
	if rep.Status != http.StatusNotFound || rep.errCode(t) != codeMethodNotFound {
		t.Errorf("modern unknown method = %d %s", rep.Status, rep.Body)
	}

	// Batches are not part of that revision.
	rep = r.send("POST", "/mcp", key, []byte(`[{"jsonrpc":"2.0","id":1,"method":"tools/list","params":`+
		`{"_meta":{"io.modelcontextprotocol/protocolVersion":"2026-07-28","io.modelcontextprotocol/clientCapabilities":{}}}}]`),
		"MCP-Protocol-Version", "2026-07-28")
	if got := rep.JSONArray(t); len(got) != 1 || got[0]["error"] == nil {
		t.Errorf("a modern request in a batch = %d %s", rep.Status, rep.Body)
	}
}
