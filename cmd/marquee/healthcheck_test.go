package main

import (
	"bytes"
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

// healthStub answers /api/v1/health with a fixed status and body and counts the
// calls. It fails the test if the probe asks for anything else.
func healthStub(t *testing.T, status int, body string) (addr string, hits *atomic.Int32) {
	t.Helper()
	hits = new(atomic.Int32)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits.Add(1)
		if r.Method != http.MethodGet || r.URL.Path != "/api/v1/health" {
			t.Errorf("probe asked for %s %s", r.Method, r.URL.Path)
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(status)
		_, _ = w.Write([]byte(body))
	}))
	t.Cleanup(srv.Close)
	return strings.TrimPrefix(srv.URL, "http://"), hits
}

func probe(args ...string) (code int, stdout, stderr string) {
	var out, errb bytes.Buffer
	code = runHealthcheck(args, &out, &errb)
	return code, out.String(), errb.String()
}

func TestHealthcheckHealthy(t *testing.T) {
	addr, hits := healthStub(t, 200, `{"status":"ok","version":"0.1.0","uptimeSeconds":12,"db":"ok"}`)
	code, out, errs := probe("-addr", addr)
	if code != 0 || out != "ok\n" || errs != "" {
		t.Errorf("code=%d stdout=%q stderr=%q, want 0, ok, nothing", code, out, errs)
	}
	if hits.Load() != 1 {
		t.Errorf("%d calls, want 1", hits.Load())
	}
}

func TestHealthcheckUnhealthyAnswers(t *testing.T) {
	cases := []struct {
		name   string
		status int
		body   string
		want   string
	}{
		{"degraded 503", 503, `{"status":"degraded","db":"error"}`, "unhealthy: status degraded\n"},
		{"degraded 200", 200, `{"status":"degraded"}`, "unhealthy: status degraded\n"},
		{"starting", 200, `{"status":"starting"}`, "unhealthy: status starting\n"},
		{"upper case is not ok", 200, `{"status":"OK"}`, "unhealthy: status OK\n"},
		{"not json", 200, `<html>hello</html>`, "unhealthy: the answer is not JSON\n"},
		{"empty body", 200, ``, "unhealthy: the answer is not JSON\n"},
		{"no status", 200, `{"db":"ok"}`, "unhealthy: the answer has no status\n"},
		{"status is not a string", 200, `{"status":true}`, "unhealthy: the answer is not JSON\n"},
		{"ok but not 200", 503, `{"status":"ok"}`, "unhealthy: status ok\n"},
		{"404", 404, `not found`, "unhealthy: HTTP 404\n"},
		{"500 empty", 500, ``, "unhealthy: HTTP 500\n"},
		{"newline in status stays one line", 200, `{"status":"bad\nline\u001b[31m"}`, "unhealthy: status bad line [31m\n"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			addr, _ := healthStub(t, c.status, c.body)
			code, out, errs := probe("-addr", addr)
			if code != 1 || out != "" || errs != c.want {
				t.Errorf("code=%d stdout=%q stderr=%q, want 1, nothing, %q", code, out, errs, c.want)
			}
			if strings.Count(errs, "\n") != 1 {
				t.Errorf("stderr is not one line: %q", errs)
			}
		})
	}
}

func TestHealthcheckLongStatusIsCut(t *testing.T) {
	addr, _ := healthStub(t, 200, `{"status":"`+strings.Repeat("x", 500)+`"}`)
	_, _, errs := probe("-addr", addr)
	if len(errs) > 80 || !strings.HasPrefix(errs, "unhealthy: status xxx") {
		t.Errorf("stderr = %q", errs)
	}
}

func TestHealthcheckDoesNotFollowRedirects(t *testing.T) {
	good, goodHits := healthStub(t, 200, `{"status":"ok"}`)
	redirector := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, "http://"+good+"/api/v1/health", http.StatusFound)
	}))
	defer redirector.Close()

	code, _, errs := probe("-addr", strings.TrimPrefix(redirector.URL, "http://"))
	if code != 1 || errs != "unhealthy: HTTP 302\n" {
		t.Errorf("code=%d stderr=%q, want 1 and HTTP 302", code, errs)
	}
	if goodHits.Load() != 0 {
		t.Error("the probe followed a redirect")
	}
}

func TestHealthcheckConnectionRefused(t *testing.T) {
	srv := httptest.NewServer(http.NotFoundHandler())
	addr := strings.TrimPrefix(srv.URL, "http://")
	srv.Close() // nothing listens there any more

	code, out, errs := probe("-addr", addr)
	if code != 1 || out != "" || errs != "unhealthy: connection refused\n" {
		t.Errorf("code=%d stdout=%q stderr=%q", code, out, errs)
	}
}

func TestHealthcheckTimesOut(t *testing.T) {
	old := healthTimeout
	healthTimeout = 150 * time.Millisecond
	t.Cleanup(func() { healthTimeout = old })

	release := make(chan struct{})
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		select {
		case <-release:
		case <-r.Context().Done():
		}
	}))
	t.Cleanup(srv.Close)
	t.Cleanup(func() { close(release) }) // runs before srv.Close

	start := time.Now()
	code, _, errs := probe("-addr", strings.TrimPrefix(srv.URL, "http://"))
	if code != 1 || errs != "unhealthy: timed out after 150ms\n" {
		t.Errorf("code=%d stderr=%q", code, errs)
	}
	if d := time.Since(start); d > 3*time.Second {
		t.Errorf("took %s", d)
	}
}

func TestHealthClientSettings(t *testing.T) {
	if defaultHealthTimeout != 3*time.Second || healthTimeout != defaultHealthTimeout {
		t.Errorf("timeout = %s, want 3s", healthTimeout)
	}
	c := newHealthClient()
	if c.Timeout != healthTimeout {
		t.Errorf("client timeout = %s", c.Timeout)
	}
	tr, ok := c.Transport.(*http.Transport)
	if !ok || tr.Proxy != nil {
		t.Errorf("transport = %#v, want a *http.Transport with no proxy", c.Transport)
	}
	if c.CheckRedirect == nil || c.CheckRedirect(nil, nil) != http.ErrUseLastResponse {
		t.Error("redirects are not stopped")
	}
}

func TestHealthURL(t *testing.T) {
	cases := []struct{ addr, want string }{
		{":6464", "http://127.0.0.1:6464/api/v1/health"},
		{"0.0.0.0:7000", "http://127.0.0.1:7000/api/v1/health"},
		{"[::]:7000", "http://127.0.0.1:7000/api/v1/health"},
		{"[0:0:0:0:0:0:0:0]:7000", "http://127.0.0.1:7000/api/v1/health"},
		{"127.0.0.1:6464", "http://127.0.0.1:6464/api/v1/health"},
		{"localhost:6464", "http://localhost:6464/api/v1/health"},
		{"192.168.1.5:8080", "http://192.168.1.5:8080/api/v1/health"},
		{"marquee.lan:6464", "http://marquee.lan:6464/api/v1/health"},
		{"[::1]:6464", "http://[::1]:6464/api/v1/health"},
		{"[2001:db8::7]:6464", "http://[2001:db8::7]:6464/api/v1/health"},
		{"[fe80::1%eth0]:6464", "http://[fe80::1%25eth0]:6464/api/v1/health"},
	}
	for _, c := range cases {
		got, err := healthURL(c.addr)
		if err != nil || got != c.want {
			t.Errorf("healthURL(%q) = %q, %v; want %q", c.addr, got, err, c.want)
		}
	}

	for _, bad := range []string{"6464", "localhost", "[::1]", "127.0.0.1:", "127.0.0.1:0", "127.0.0.1:99999",
		"127.0.0.1:http", "http://127.0.0.1:6464", "::1:6464", "a:b:c"} {
		if got, err := healthURL(bad); err == nil {
			t.Errorf("healthURL(%q) = %q, want an error", bad, got)
		} else if strings.Contains(err.Error(), "\n") {
			t.Errorf("healthURL(%q) error has a newline: %q", bad, err)
		}
	}
}

func TestHealthcheckAddressSources(t *testing.T) {
	t.Setenv("MARQUEE_ADDR", "")
	if got := resolveHealthAddr(""); got != ":6464" {
		t.Errorf("default = %q, want :6464", got)
	}
	t.Setenv("MARQUEE_ADDR", "127.0.0.1:7000")
	if got := resolveHealthAddr(""); got != "127.0.0.1:7000" {
		t.Errorf("env = %q", got)
	}
	if got := resolveHealthAddr("127.0.0.1:7100"); got != "127.0.0.1:7100" {
		t.Errorf("flag should beat env, got %q", got)
	}

	// End to end: the env variable alone finds the server...
	addr, hits := healthStub(t, 200, `{"status":"ok"}`)
	t.Setenv("MARQUEE_ADDR", addr)
	if code, _, errs := probe(); code != 0 {
		t.Fatalf("env only: code=%d stderr=%q", code, errs)
	}
	// ...and -addr wins over a wrong env value.
	t.Setenv("MARQUEE_ADDR", "127.0.0.1:1")
	if code, _, errs := probe("-addr", addr); code != 0 {
		t.Fatalf("flag over env: code=%d stderr=%q", code, errs)
	}
	// An empty -addr falls back to the env value rather than to nothing.
	t.Setenv("MARQUEE_ADDR", addr)
	if code, _, errs := probe("-addr="); code != 0 {
		t.Fatalf("empty flag: code=%d stderr=%q", code, errs)
	}
	if hits.Load() != 3 {
		t.Errorf("%d calls, want 3", hits.Load())
	}
}

func TestHealthcheckBadArguments(t *testing.T) {
	t.Setenv("MARQUEE_ADDR", "")
	cases := [][]string{
		{"-nope"},
		{"stray"},
		{"-addr"},
		{"-addr", "6464"},
		{"-addr", "localhost"},
		{"-h"},
	}
	for _, args := range cases {
		code, out, errs := probe(args...)
		if code == 0 || out != "" || errs == "" {
			t.Errorf("%v: code=%d stdout=%q stderr=%q, want a failure with one stderr line", args, code, out, errs)
		}
		if strings.Count(errs, "\n") != 1 {
			t.Errorf("%v: stderr is not one line: %q", args, errs)
		}
	}
}

// The probe must work in an image that has no config folder to read and must
// never create one.
func TestHealthcheckLeavesTheConfigFolderAlone(t *testing.T) {
	dir := t.TempDir()
	t.Chdir(dir)
	t.Setenv("MARQUEE_CONFIG", filepath.Join(dir, "no-such", "config"))
	t.Setenv("MARQUEE_MASTER_KEY_FILE", filepath.Join(dir, "no-such", "marquee.key"))

	good, _ := healthStub(t, 200, `{"status":"ok"}`)
	if code, _, errs := probe("-addr", good); code != 0 {
		t.Fatalf("healthy: code=%d stderr=%q", code, errs)
	}
	if code, _, _ := probe("-addr", "127.0.0.1:1"); code != 1 {
		t.Fatalf("refused: code=%d", code)
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 0 {
		var names []string
		for _, e := range entries {
			names = append(names, e.Name())
		}
		t.Errorf("the probe created files: %v", names)
	}
}

// Against the real server: its health answer must pass the probe, and a server
// that is gone must fail it.
func TestHealthcheckAgainstTheRealServer(t *testing.T) {
	t.Setenv("MARQUEE_MASTER_KEY_FILE", "")
	logs := &lockedBuffer{}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	cfg := filepath.Join(t.TempDir(), "config")
	go func() { done <- serve(ctx, newLogger(logs, "info"), "127.0.0.1:0", cfg) }()

	started := regexp.MustCompile(`msg="marquee started" version=\S+ addr=(127\.0\.0\.1:\d+)`)
	var m []string
	for deadline := time.Now().Add(10 * time.Second); m == nil && time.Now().Before(deadline); {
		if m = started.FindStringSubmatch(logs.String()); m != nil {
			break
		}
		select {
		case err := <-done:
			t.Fatalf("serve returned early: %v\n%s", err, logs.String())
		case <-time.After(20 * time.Millisecond):
		}
	}
	if m == nil {
		t.Fatalf("no startup line:\n%s", logs.String())
	}

	if code, out, errs := probe("-addr", m[1]); code != 0 || out != "ok\n" || errs != "" {
		t.Errorf("running server: code=%d stdout=%q stderr=%q", code, out, errs)
	}
	// The same address written as a listen address (any host) reaches it too.
	port := m[1][strings.LastIndex(m[1], ":"):]
	if code, _, errs := probe("-addr", "0.0.0.0"+port); code != 0 {
		t.Errorf("0.0.0.0 form: code=%d stderr=%q", code, errs)
	}

	cancel()
	if err := <-done; err != nil {
		t.Fatalf("serve: %v", err)
	}
	if code, _, errs := probe("-addr", m[1]); code != 1 || errs != "unhealthy: connection refused\n" {
		t.Errorf("stopped server: code=%d stderr=%q", code, errs)
	}
}
