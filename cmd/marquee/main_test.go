package main

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"testing"
	"time"
)

// lockedBuffer is a log sink that the server goroutine and the test can share.
type lockedBuffer struct {
	mu sync.Mutex
	b  bytes.Buffer
}

func (l *lockedBuffer) Write(p []byte) (int, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.b.Write(p)
}

func (l *lockedBuffer) String() string {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.b.String()
}

func TestServeStartsAnswersAndShutsDownGracefully(t *testing.T) {
	t.Setenv("MARQUEE_MASTER_KEY_FILE", "")
	cfg := filepath.Join(t.TempDir(), "config")
	logs := &lockedBuffer{}
	log := newLogger(logs, "info")

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- serve(ctx, log, "127.0.0.1:0", cfg) }()

	// One startup line with version, address and config path.
	started := regexp.MustCompile(`msg="marquee started" version=(\S+) addr=(127\.0\.0\.1:\d+) config=(.+)`)
	var m []string
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		if m = started.FindStringSubmatch(logs.String()); m != nil {
			break
		}
		select {
		case err := <-done:
			t.Fatalf("serve returned early: %v\nlogs:\n%s", err, logs.String())
		case <-time.After(20 * time.Millisecond):
		}
	}
	if m == nil {
		t.Fatalf("no startup line:\n%s", logs.String())
	}
	if m[1] != version {
		t.Errorf("startup line version = %q, want %q", m[1], version)
	}
	if got, _ := filepath.Abs(cfg); !strings.Contains(m[3], filepath.Base(got)) {
		t.Errorf("startup line config = %q, want the config dir", m[3])
	}
	if n := strings.Count(logs.String(), "marquee started"); n != 1 {
		t.Errorf("%d startup lines, want 1", n)
	}

	resp, err := http.Get("http://" + m[2] + "/api/v1/health")
	if err != nil {
		t.Fatal(err)
	}
	var health map[string]any
	if err := json.NewDecoder(resp.Body).Decode(&health); err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if health["status"] != "ok" || health["db"] != "ok" || health["version"] != version {
		t.Errorf("health = %v", health)
	}

	// The master key, both databases, and the warning exist after first start.
	for _, f := range []string{"marquee.key", "marquee.db", "marquee-events.db"} {
		if _, err := os.Stat(filepath.Join(cfg, f)); err != nil {
			t.Errorf("%s missing: %v", f, err)
		}
	}
	if !strings.Contains(logs.String(), "back this file up") {
		t.Errorf("no master key backup warning in the log:\n%s", logs.String())
	}

	// An open event stream must not hold shutdown up.
	var ev *http.Response
	ev, err = http.Get("http://" + m[2] + "/api/v1/events")
	if err != nil {
		t.Fatal(err)
	}
	ev.Body.Close() // 401 without a session; the point is only that the server is still up

	cancel()
	select {
	case err := <-done:
		if err != nil {
			t.Errorf("serve returned %v after cancel", err)
		}
	case <-time.After(15 * time.Second):
		t.Fatal("serve did not return after the context was cancelled")
	}
}

func TestSecondStartReusesKeyAndDatabases(t *testing.T) {
	t.Setenv("MARQUEE_MASTER_KEY_FILE", "")
	cfg := filepath.Join(t.TempDir(), "config")
	for i := range 2 {
		logs := &lockedBuffer{}
		ctx, cancel := context.WithCancel(context.Background())
		done := make(chan error, 1)
		go func() { done <- serve(ctx, newLogger(logs, "info"), "127.0.0.1:0", cfg) }()
		deadline := time.Now().Add(10 * time.Second)
		for !strings.Contains(logs.String(), "marquee started") && time.Now().Before(deadline) {
			select {
			case err := <-done:
				t.Fatalf("start %d failed: %v", i+1, err)
			case <-time.After(20 * time.Millisecond):
			}
		}
		cancel()
		if err := <-done; err != nil {
			t.Fatalf("start %d: %v", i+1, err)
		}
		warned := strings.Contains(logs.String(), "back this file up")
		if (i == 0) != warned {
			t.Errorf("start %d: master key warning = %v", i+1, warned)
		}
	}
}

func TestRunFlagsAndEnv(t *testing.T) {
	var out bytes.Buffer
	if err := run([]string{"-version"}, &out); err != nil || !strings.Contains(out.String(), "marquee") {
		t.Errorf("-version: %v %q", err, out.String())
	}
	if err := run([]string{"-nope"}, &out); err == nil {
		t.Error("an unknown flag should be an error")
	}

	t.Setenv("MARQUEE_ADDR", "")
	if envOr("MARQUEE_ADDR", defaultAddr) != ":6464" {
		t.Error("default address should be :6464")
	}
	t.Setenv("MARQUEE_ADDR", "127.0.0.1:7000")
	if got := envOr("MARQUEE_ADDR", defaultAddr); got != "127.0.0.1:7000" {
		t.Errorf("env address = %q", got)
	}
	t.Setenv("MARQUEE_CONFIG", "")
	if envOr("MARQUEE_CONFIG", defaultConfig) != "./config" {
		t.Error("default config dir should be ./config")
	}
}
