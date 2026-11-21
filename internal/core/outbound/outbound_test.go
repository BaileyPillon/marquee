package outbound

import (
	"bytes"
	"context"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func noBackoff(int) time.Duration { return time.Millisecond }

func newTestClient(t *testing.T, opts Options) *Client {
	t.Helper()
	if opts.Backoff == nil {
		opts.Backoff = noBackoff
	}
	if opts.Logger == nil {
		opts.Logger = slog.New(slog.NewTextHandler(io.Discard, nil))
	}
	if opts.HostRPS == 0 {
		opts.HostRPS = 1000 // do not slow ordinary tests
		opts.HostBurst = 1000
	}
	return New("1.2.3", opts)
}

func TestUserAgentAndNoProxy(t *testing.T) {
	var got atomic.Value
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		got.Store(r.Header.Get("User-Agent"))
	}))
	defer srv.Close()

	c := newTestClient(t, Options{})
	resp, err := c.Get(context.Background(), srv.URL+"/x")
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()

	want := "Marquee/1.2.3 (+https://github.com/BaileyPillon/marquee)"
	if got.Load() != want || c.UserAgent() != want {
		t.Errorf("User-Agent = %v, want %q", got.Load(), want)
	}
	tr := c.hc.Transport.(*http.Transport)
	if tr.Proxy != nil {
		t.Error("Transport.Proxy must be nil so proxy environment variables are ignored")
	}
}

func TestCallerUserAgentIsKept(t *testing.T) {
	var got atomic.Value
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { got.Store(r.UserAgent()) }))
	defer srv.Close()
	c := newTestClient(t, Options{})
	req, _ := http.NewRequest(http.MethodGet, srv.URL, nil)
	req.Header.Set("User-Agent", "custom/9")
	resp, err := c.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if got.Load() != "custom/9" {
		t.Errorf("User-Agent = %v", got.Load())
	}
}

func TestRetriesGETOn503ThenSucceeds(t *testing.T) {
	var calls atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if calls.Add(1) <= 2 {
			w.WriteHeader(http.StatusServiceUnavailable)
			return
		}
		_, _ = io.WriteString(w, "finally")
	}))
	defer srv.Close()

	c := newTestClient(t, Options{})
	resp, err := c.Get(context.Background(), srv.URL)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != 200 || string(body) != "finally" || calls.Load() != 3 {
		t.Errorf("status %d body %q after %d calls", resp.StatusCode, body, calls.Load())
	}
}

func TestRetryStatusesAndLimit(t *testing.T) {
	for _, code := range []int{429, 502, 503, 504} {
		var calls atomic.Int32
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			calls.Add(1)
			w.WriteHeader(code)
		}))
		c := newTestClient(t, Options{})
		resp, err := c.Get(context.Background(), srv.URL)
		if err != nil {
			t.Fatal(err)
		}
		resp.Body.Close()
		srv.Close()
		// One try plus three retries, then the last response is handed back.
		if resp.StatusCode != code || calls.Load() != 4 {
			t.Errorf("code %d: got status %d after %d calls, want 4 calls", code, resp.StatusCode, calls.Load())
		}
	}
}

func TestNoRetryForOtherStatusesOrMethods(t *testing.T) {
	var calls atomic.Int32
	status := 500
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		w.WriteHeader(status)
	}))
	defer srv.Close()
	c := newTestClient(t, Options{})

	// 500 is not retried.
	resp, _ := c.Get(context.Background(), srv.URL)
	resp.Body.Close()
	if calls.Load() != 1 {
		t.Errorf("500 was retried: %d calls", calls.Load())
	}

	// POST is not retried even on 503.
	calls.Store(0)
	status = 503
	req, _ := http.NewRequest(http.MethodPost, srv.URL, strings.NewReader("x"))
	resp, err := c.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if calls.Load() != 1 {
		t.Errorf("POST was retried: %d calls", calls.Load())
	}

	// HEAD is retried.
	calls.Store(0)
	req, _ = http.NewRequest(http.MethodHead, srv.URL, nil)
	resp, _ = c.Do(req)
	resp.Body.Close()
	if calls.Load() != 4 {
		t.Errorf("HEAD made %d calls, want 4", calls.Load())
	}
}

func TestRetryAfterHeaderIsHonoured(t *testing.T) {
	var calls atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if calls.Add(1) == 1 {
			w.Header().Set("Retry-After", "1")
			w.WriteHeader(http.StatusTooManyRequests)
		}
	}))
	defer srv.Close()
	c := newTestClient(t, Options{})
	start := time.Now()
	resp, err := c.Get(context.Background(), srv.URL)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if took := time.Since(start); took < 900*time.Millisecond {
		t.Errorf("retried after %v, want at least the 1s Retry-After", took)
	}
}

func TestTotalTimeoutAndPerCallOverride(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		select {
		case <-r.Context().Done():
		case <-time.After(3 * time.Second):
		}
	}))
	defer srv.Close()

	c := newTestClient(t, Options{DefaultTimeout: 5 * time.Second})
	start := time.Now()
	_, err := c.Get(context.Background(), srv.URL, WithTimeout(100*time.Millisecond))
	if err == nil || !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("err = %v, want deadline exceeded", err)
	}
	if time.Since(start) > 2*time.Second {
		t.Errorf("per-call timeout of 100ms took %v", time.Since(start))
	}

	// The default applies when no override is given.
	c = newTestClient(t, Options{DefaultTimeout: 100 * time.Millisecond})
	if _, err := c.Get(context.Background(), srv.URL); !errors.Is(err, context.DeadlineExceeded) {
		t.Errorf("default timeout: err = %v", err)
	}
}

func TestTimeoutKeepsRunningWhileBodyIsRead(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.WriteString(w, "start")
		w.(http.Flusher).Flush()
		select {
		case <-r.Context().Done():
		case <-time.After(3 * time.Second):
		}
	}))
	defer srv.Close()
	c := newTestClient(t, Options{})
	resp, err := c.Get(context.Background(), srv.URL, WithTimeout(150*time.Millisecond))
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if _, err := io.ReadAll(resp.Body); err == nil {
		t.Error("a stalled body should be cut off by the total timeout")
	}
}

func TestDebugLogHasNoQueryOrHeaders(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))
	defer srv.Close()

	var logs bytes.Buffer
	log := slog.New(slog.NewTextHandler(&logs, &slog.HandlerOptions{Level: slog.LevelDebug}))
	c := newTestClient(t, Options{Logger: log})

	req, _ := http.NewRequest(http.MethodGet, srv.URL+"/api/search?apikey=SECRETKEY&q=movie", nil)
	req.Header.Set("Authorization", "Bearer SECRETTOKEN")
	req.Header.Set("X-Api-Key", "SECRETHEADER")
	resp, err := c.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()

	out := logs.String()
	if n := strings.Count(out, "\n"); n != 1 {
		t.Errorf("want exactly one log line per call, got %d:\n%s", n, out)
	}
	for _, want := range []string{"level=DEBUG", "method=GET", "path=/api/search", "status=200", "ms="} {
		if !strings.Contains(out, want) {
			t.Errorf("log line missing %q: %s", want, out)
		}
	}
	for _, leak := range []string{"SECRETKEY", "SECRETTOKEN", "SECRETHEADER", "apikey", "q=movie", "?"} {
		if strings.Contains(out, leak) {
			t.Errorf("log line leaks %q: %s", leak, out)
		}
	}
}

func TestErrorsDoNotLeakQueryString(t *testing.T) {
	var logs bytes.Buffer
	log := slog.New(slog.NewTextHandler(&logs, &slog.HandlerOptions{Level: slog.LevelDebug}))
	c := newTestClient(t, Options{Logger: log, MaxRetries: -1})

	// Nothing listens here, so the dial fails.
	_, err := c.Get(context.Background(), "http://127.0.0.1:1/search?apikey=SECRETKEY")
	if err == nil {
		t.Fatal("expected a connection error")
	}
	if strings.Contains(err.Error(), "SECRETKEY") || strings.Contains(logs.String(), "SECRETKEY") {
		t.Errorf("key leaked.\nerror: %v\nlog: %s", err, logs.String())
	}
	if !strings.Contains(err.Error(), "/search") {
		t.Errorf("error should still say which path failed: %v", err)
	}
}

func TestPerHostRateLimit(t *testing.T) {
	var calls atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { calls.Add(1) }))
	defer srv.Close()

	c := newTestClient(t, Options{})
	host := strings.TrimPrefix(srv.URL, "http://")
	c.SetHostLimit(host, 20, 1) // 20/s, no burst: one request per 50ms

	start := time.Now()
	for range 5 {
		resp, err := c.Get(context.Background(), srv.URL)
		if err != nil {
			t.Fatal(err)
		}
		resp.Body.Close()
	}
	// 5 requests, first free, four more at 50ms each.
	if took := time.Since(start); took < 150*time.Millisecond {
		t.Errorf("5 requests took only %v; the limit is not applied", took)
	}
}

func TestBucketMath(t *testing.T) {
	now := time.Unix(1000, 0)
	l := newLimiter(2, 2, func() time.Time { return now }, nil) // 2/s, burst 2

	// The burst of 2 is free.
	for i := range 2 {
		if d := l.reserve("h"); d != 0 {
			t.Fatalf("request %d waits %v, want 0", i, d)
		}
	}
	// The third must wait for half a second.
	if d := l.reserve("h"); d != 500*time.Millisecond {
		t.Errorf("third request waits %v, want 500ms", d)
	}
	// Another host has its own bucket.
	if d := l.reserve("other"); d != 0 {
		t.Errorf("other host waits %v", d)
	}
	// After a full second the bucket has refilled (capped at the burst).
	now = now.Add(10 * time.Second)
	for i := range 2 {
		if d := l.reserve("h"); d != 0 {
			t.Errorf("after refill, request %d waits %v", i, d)
		}
	}
	if d := l.reserve("h"); d == 0 {
		t.Error("burst should be capped at 2")
	}
}

func TestRateLimitWaitHonoursContext(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))
	defer srv.Close()
	c := newTestClient(t, Options{})
	c.SetHostLimit(strings.TrimPrefix(srv.URL, "http://"), 0.1, 1) // one per 10s

	resp, err := c.Get(context.Background(), srv.URL)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	if _, err := c.Get(ctx, srv.URL); err == nil {
		t.Fatal("expected the wait to be cancelled")
	}
}
