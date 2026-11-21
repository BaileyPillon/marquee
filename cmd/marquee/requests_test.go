package main

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/cookiejar"
	"path/filepath"
	"regexp"
	"testing"
	"time"
)

// TestServeWiresUpRequests starts the whole program and checks that the
// requests routes answer, so the service reached the web layer.
func TestServeWiresUpRequests(t *testing.T) {
	t.Setenv("MARQUEE_MASTER_KEY_FILE", "")
	cfg := filepath.Join(t.TempDir(), "config")
	logs := &lockedBuffer{}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- serve(ctx, newLogger(logs, "info"), "127.0.0.1:0", cfg) }()
	defer func() {
		cancel()
		select {
		case <-done:
		case <-time.After(10 * time.Second):
			t.Error("serve did not stop")
		}
	}()

	started := regexp.MustCompile(`msg="marquee started" version=\S+ addr=(127\.0\.0\.1:\d+)`)
	var addr string
	for deadline := time.Now().Add(10 * time.Second); addr == "" && time.Now().Before(deadline); {
		if m := started.FindStringSubmatch(logs.String()); m != nil {
			addr = m[1]
			break
		}
		select {
		case err := <-done:
			t.Fatalf("serve returned early: %v\nlogs:\n%s", err, logs.String())
		case <-time.After(20 * time.Millisecond):
		}
	}
	if addr == "" {
		t.Fatalf("no startup line:\n%s", logs.String())
	}

	jar, _ := cookiejar.New(nil)
	hc := &http.Client{Jar: jar}
	call := func(method, path string, body any) (int, map[string]any) {
		t.Helper()
		var rd *bytes.Reader
		if body != nil {
			b, _ := json.Marshal(body)
			rd = bytes.NewReader(b)
		} else {
			rd = bytes.NewReader(nil)
		}
		req, err := http.NewRequest(method, "http://"+addr+path, rd)
		if err != nil {
			t.Fatal(err)
		}
		req.Header.Set("X-Marquee", "1")
		resp, err := hc.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		defer resp.Body.Close()
		var out map[string]any
		_ = json.NewDecoder(resp.Body).Decode(&out)
		return resp.StatusCode, out
	}

	if status, _ := call("GET", "/api/v1/requests/summary", nil); status != http.StatusUnauthorized {
		t.Fatalf("summary before sign-in = %d, want 401 (the route exists, the person is not signed in)", status)
	}
	if status, _ := call("POST", "/api/v1/setup/owner", map[string]string{"name": "Bailey", "password": "correct horse"}); status != http.StatusCreated {
		t.Fatalf("setup = %d", status)
	}
	status, sum := call("GET", "/api/v1/requests/summary", nil)
	if status != http.StatusOK || sum["waitingForYou"] != float64(0) {
		t.Errorf("summary = %d %v", status, sum)
	}
	if status, list := call("GET", "/api/v1/requests", nil); status != http.StatusOK || list["total"] != float64(0) {
		t.Errorf("list = %d %v", status, list)
	}
	status, people := call("GET", "/api/v1/people", nil)
	if status != http.StatusOK || people["people"] == nil || people["defaults"] == nil {
		t.Errorf("people = %d %v", status, people)
	}
	// The ask reaches the catalog, which has no such title.
	status, body := call("POST", "/api/v1/requests", map[string]any{"titleId": 1, "quality": "hd"})
	if status != http.StatusNotFound {
		t.Errorf("ask for a title that is not there = %d %v", status, body)
	}
}
