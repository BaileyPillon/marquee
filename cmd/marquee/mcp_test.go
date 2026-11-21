package main

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/cookiejar"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"
)

// mcpStartApp runs the whole program on a temp config dir and returns its address.
func mcpStartApp(t *testing.T) string {
	t.Helper()
	t.Setenv("MARQUEE_MASTER_KEY_FILE", "")
	cfg := filepath.Join(t.TempDir(), "config")
	logs := &lockedBuffer{}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- serve(ctx, newLogger(logs, "info"), "127.0.0.1:0", cfg) }()
	t.Cleanup(func() {
		cancel()
		select {
		case <-done:
		case <-time.After(15 * time.Second):
			t.Error("serve did not stop")
		}
	})
	started := regexp.MustCompile(`msg="marquee started" version=\S+ addr=(127\.0\.0\.1:\d+)`)
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		if m := started.FindStringSubmatch(logs.String()); m != nil {
			return "http://" + m[1]
		}
		select {
		case err := <-done:
			t.Fatalf("serve returned early: %v\n%s", err, logs.String())
		case <-time.After(20 * time.Millisecond):
		}
	}
	t.Fatalf("no startup line:\n%s", logs.String())
	return ""
}

func mcpJSONCall(t *testing.T, c *http.Client, method, url string, body any, headers ...string) (int, http.Header, map[string]any) {
	t.Helper()
	var rd io.Reader
	if body != nil {
		b, _ := json.Marshal(body)
		rd = bytes.NewReader(b)
	}
	req, err := http.NewRequest(method, url, rd)
	if err != nil {
		t.Fatal(err)
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	for i := 0; i+1 < len(headers); i += 2 {
		req.Header.Set(headers[i], headers[i+1])
	}
	resp, err := c.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	b, _ := io.ReadAll(resp.Body)
	var m map[string]any
	_ = json.Unmarshal(b, &m)
	return resp.StatusCode, resp.Header, m
}

// TestMCPOnTheWholeServer is the wiring check: a key made through the API works
// at /mcp and /api/v1/mcp, the info route knows the tools, and Settings can
// switch the endpoint off.
func TestMCPOnTheWholeServer(t *testing.T) {
	base := mcpStartApp(t)
	jar, _ := cookiejar.New(nil)
	browser := &http.Client{Jar: jar}
	plain := &http.Client{}

	if st, _, _ := mcpJSONCall(t, browser, "POST", base+"/api/v1/setup/owner", map[string]string{"name": "Bailey", "password": "correct horse"}, "X-Marquee", "1"); st != http.StatusCreated {
		t.Fatalf("setup/owner = %d", st)
	}
	st, _, key := mcpJSONCall(t, browser, "POST", base+"/api/v1/apikeys", map[string]any{"name": "claude", "scopes": []string{"see", "ask"}}, "X-Marquee", "1")
	secret, _ := key["secret"].(string)
	if st != http.StatusCreated || !strings.HasPrefix(secret, "mq_") {
		t.Fatalf("apikeys = %d %v", st, key)
	}
	auth := []string{"Authorization", "Bearer " + secret}

	for _, path := range []string{"/mcp", "/api/v1/mcp"} {
		st, hdr, res := mcpJSONCall(t, plain, "POST", base+path, map[string]any{"jsonrpc": "2.0", "id": 1, "method": "initialize",
			"params": map[string]any{"protocolVersion": "2025-11-25", "capabilities": map[string]any{}, "clientInfo": map[string]any{"name": "t", "version": "1"}}}, auth...)
		result, _ := res["result"].(map[string]any)
		info, _ := result["serverInfo"].(map[string]any)
		if st != 200 || info["name"] != "marquee" || info["version"] != version || hdr.Get("Mcp-Session-Id") == "" {
			t.Errorf("initialize at %s = %d %v", path, st, res)
		}
	}
	st, _, res := mcpJSONCall(t, plain, "POST", base+"/mcp", map[string]any{"jsonrpc": "2.0", "id": 2, "method": "tools/call",
		"params": map[string]any{"name": "health", "arguments": map[string]any{}}}, auth...)
	sc, _ := res["result"].(map[string]any)["structuredContent"].(map[string]any)
	if st != 200 || sc["version"] != version || sc["status"] != "ok" {
		t.Errorf("health tool = %d %v", st, res)
	}
	if st, hdr, _ := mcpJSONCall(t, plain, "POST", base+"/mcp", map[string]any{"jsonrpc": "2.0", "id": 3, "method": "ping"}); st != http.StatusUnauthorized || hdr.Get("WWW-Authenticate") == "" {
		t.Errorf("no key = %d %v", st, hdr)
	}

	// The settings screen's view.
	st, _, info := mcpJSONCall(t, browser, "GET", base+"/api/v1/mcp/info", nil)
	tools, _ := info["tools"].([]any)
	calls, _ := info["recentCalls"].([]any)
	if st != 200 || info["enabled"] != true || info["url"] != base+"/mcp" || len(tools) != 7 || len(calls) != 1 {
		t.Errorf("info = %d %v", st, info)
	}
	if len(calls) == 1 {
		c := calls[0].(map[string]any)
		if c["tool"] != "health" || c["userName"] != "Bailey" || c["ok"] != true || c["at"] == nil {
			t.Errorf("recent call = %v", c)
		}
	}
	if st, _, _ := mcpJSONCall(t, plain, "GET", base+"/api/v1/mcp/info", nil); st != http.StatusUnauthorized {
		t.Errorf("info without signing in = %d", st)
	}

	// Switch it off in Settings, and it is gone at once; the info route says so.
	if st, _, _ := mcpJSONCall(t, browser, "PUT", base+"/api/v1/settings/mcp", map[string]any{"enabled": false}, "X-Marquee", "1"); st != 200 {
		t.Fatalf("PUT settings/mcp = %d", st)
	}
	for _, path := range []string{"/mcp", "/api/v1/mcp"} {
		if st, _, _ := mcpJSONCall(t, plain, "POST", base+path, map[string]any{"jsonrpc": "2.0", "id": 4, "method": "ping"}, auth...); st != http.StatusNotFound {
			t.Errorf("POST %s while off = %d, want 404", path, st)
		}
	}
	if _, _, info := mcpJSONCall(t, browser, "GET", base+"/api/v1/mcp/info", nil); info["enabled"] != false {
		t.Errorf("info while off = %v", info)
	}
}
