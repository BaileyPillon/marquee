// Package outbound is the one HTTP client every call to the outside world goes
// through. It has timeouts, bounded retries for idempotent reads, a per-host
// rate limit, a User-Agent, and exactly one debug log line per call.
//
// It never reads proxy environment variables, and it never logs a query string
// or a header (API keys travel in both).
package outbound

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"math/rand/v2"
	"net"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"time"
)

// Defaults.
const (
	DialTimeout    = 10 * time.Second
	DefaultTimeout = 30 * time.Second
	MaxRetries     = 3
	DefaultHostRPS = 10.0
	DefaultBurst   = 10
	maxRetryWait   = 10 * time.Second
	ProjectURL     = "https://github.com/BaileyPillon/marquee"
)

// Options tune a Client. The zero value is fine.
type Options struct {
	Logger         *slog.Logger
	DialTimeout    time.Duration // default 10s
	DefaultTimeout time.Duration // total per call including retries, default 30s
	MaxRetries     int           // default 3 (set -1 for none)
	HostRPS        float64       // default per-host rate, requests per second
	HostBurst      int           // default per-host burst
	// Backoff returns the wait before retry number attempt (1-based). Tests
	// replace it. Default: 500ms, 1s, 2s with jitter.
	Backoff func(attempt int) time.Duration
	// Now and Sleep are test hooks for the rate limiter.
	Now   func() time.Time
	Sleep func(ctx context.Context, d time.Duration) error
}

// Client is safe for concurrent use.
type Client struct {
	hc         *http.Client
	log        *slog.Logger
	userAgent  string
	timeout    time.Duration
	maxRetries int
	backoff    func(int) time.Duration
	limits     *limiter
}

// New builds a Client that identifies itself as Marquee/<version>.
func New(version string, opts Options) *Client {
	if opts.Logger == nil {
		opts.Logger = slog.Default()
	}
	if opts.DialTimeout <= 0 {
		opts.DialTimeout = DialTimeout
	}
	if opts.DefaultTimeout <= 0 {
		opts.DefaultTimeout = DefaultTimeout
	}
	switch {
	case opts.MaxRetries == 0:
		opts.MaxRetries = MaxRetries
	case opts.MaxRetries < 0:
		opts.MaxRetries = 0
	}
	if opts.HostRPS <= 0 {
		opts.HostRPS = DefaultHostRPS
	}
	if opts.HostBurst <= 0 {
		opts.HostBurst = DefaultBurst
	}
	if opts.Backoff == nil {
		opts.Backoff = defaultBackoff
	}
	transport := &http.Transport{
		Proxy: nil, // proxy environment variables are deliberately ignored
		DialContext: (&net.Dialer{
			Timeout:   opts.DialTimeout,
			KeepAlive: 30 * time.Second,
		}).DialContext,
		ForceAttemptHTTP2:     true,
		MaxIdleConns:          64,
		MaxIdleConnsPerHost:   8,
		IdleConnTimeout:       90 * time.Second,
		TLSHandshakeTimeout:   10 * time.Second,
		ExpectContinueTimeout: time.Second,
	}
	return &Client{
		// The total deadline is applied per call (see Do), not here, so a
		// caller can override it.
		hc:         &http.Client{Transport: transport, CheckRedirect: checkRedirect},
		log:        opts.Logger,
		userAgent:  "Marquee/" + version + " (+" + ProjectURL + ")",
		timeout:    opts.DefaultTimeout,
		maxRetries: opts.MaxRetries,
		backoff:    opts.Backoff,
		limits:     newLimiter(opts.HostRPS, opts.HostBurst, opts.Now, opts.Sleep),
	}
}

// UserAgent returns the User-Agent the client sends.
func (c *Client) UserAgent() string { return c.userAgent }

// SetHostLimit sets the rate limit for one host (for example 1/s for
// MusicBrainz).
func (c *Client) SetHostLimit(host string, rps float64, burst int) {
	c.limits.set(strings.ToLower(host), rps, burst)
}

// Do sends the request. The caller owns resp.Body and must close it.
//
// Options for one call: use WithTimeout to replace the 30 second total.
func (c *Client) Do(req *http.Request, opts ...CallOption) (*http.Response, error) {
	var co callOptions
	for _, o := range opts {
		o(&co)
	}
	timeout := c.timeout
	if co.timeout > 0 {
		timeout = co.timeout
	}

	start := time.Now()
	ctx, cancel := context.WithTimeout(req.Context(), timeout)
	if co.noRedirect {
		ctx = context.WithValue(ctx, noRedirectKey{}, true)
	}
	if co.limitRedirects {
		ctx = context.WithValue(ctx, redirectHostsKey{}, co.redirectHosts)
	}
	req = req.Clone(ctx)
	if req.Header.Get("User-Agent") == "" {
		req.Header.Set("User-Agent", c.userAgent)
	}
	host := strings.ToLower(req.URL.Host)

	retryable := (req.Method == http.MethodGet || req.Method == http.MethodHead) && (req.Body == nil || req.Body == http.NoBody)
	attempts := 0
	var (
		resp *http.Response
		err  error
	)
	for {
		if err = c.limits.wait(ctx, host); err != nil {
			break
		}
		attempts++
		resp, err = c.hc.Do(req)
		if err != nil || !retryable || !retryStatus(resp.StatusCode) || attempts > c.maxRetries {
			break
		}
		wait := c.backoff(attempts)
		if ra := retryAfter(resp.Header.Get("Retry-After")); ra > 0 {
			wait = min(ra, maxRetryWait)
		}
		// Drain a little so the connection can be reused, then close.
		_, _ = io.CopyN(io.Discard, resp.Body, 4096)
		_ = resp.Body.Close()
		if err = sleep(ctx, wait); err != nil {
			resp = nil
			break
		}
	}

	status := 0
	if resp != nil {
		status = resp.StatusCode
	}
	c.logCall(req, status, attempts, time.Since(start), err)

	if err != nil {
		cancel()
		return nil, sanitize(err)
	}
	// The deadline must outlive this function: it keeps running while the
	// caller reads the body, and ends when the body is closed.
	resp.Body = &cancelBody{ReadCloser: resp.Body, cancel: cancel}
	return resp, nil
}

// Get is a convenience for GET requests.
func (c *Client) Get(ctx context.Context, rawURL string, opts ...CallOption) (*http.Response, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, rawURL, nil)
	if err != nil {
		return nil, sanitize(err)
	}
	return c.Do(req, opts...)
}

// CallOption changes one call.
type CallOption func(*callOptions)

type callOptions struct {
	timeout    time.Duration
	noRedirect bool
	// redirectHosts, when limitRedirects is set, are the only hosts a redirect of
	// this call may lead to.
	limitRedirects bool
	redirectHosts  []string
}

// WithTimeout replaces the default total timeout for one call.
func WithTimeout(d time.Duration) CallOption { return func(o *callOptions) { o.timeout = d } }

// WithNoRedirect makes one call hand back a 3xx response as it is, instead of
// following it. The caller reads the Location header itself. This is how a
// redirect to a magnet: link (which no HTTP client can follow) is seen.
func WithNoRedirect() CallOption { return func(o *callOptions) { o.noRedirect = true } }

// WithRedirectHosts limits where one call's redirects may lead: every redirect must
// go to a host that matches one of hosts, or the call fails with
// ErrRedirectNotAllowed. A host is written as a name ("archive.org", which means
// exactly that name) or as "*.archive.org" (any name below it, but not archive.org
// itself, so list both). Names are compared without case or port. A redirect must
// also stay on http or https and never fall back from https to http. With no hosts
// at all, no redirect is followed.
//
// A call without this option follows redirects anywhere (up to 10 of them); use it
// whenever the address a call starts from is not enough reason to trust wherever
// it ends up.
func WithRedirectHosts(hosts ...string) CallOption {
	return func(o *callOptions) {
		o.limitRedirects = true
		o.redirectHosts = append([]string(nil), hosts...)
	}
}

// ErrRedirectNotAllowed is returned (wrapped) when a redirect leads somewhere a
// call said it must not (WithRedirectHosts).
var ErrRedirectNotAllowed = errors.New("outbound: the redirect leads to a host that is not allowed")

type (
	noRedirectKey    struct{}
	redirectHostsKey struct{}
)

// maxRedirects matches net/http's own default.
const maxRedirects = 10

// checkRedirect is the client-wide redirect policy: stop after maxRedirects,
// stop at once for a call that asked for WithNoRedirect, and refuse a redirect to
// a host a call did not allow (WithRedirectHosts).
func checkRedirect(req *http.Request, via []*http.Request) error {
	if req.Context().Value(noRedirectKey{}) != nil {
		return http.ErrUseLastResponse
	}
	if hosts, ok := req.Context().Value(redirectHostsKey{}).([]string); ok {
		if err := redirectAllowed(req.URL, via, hosts); err != nil {
			return err
		}
	}
	if len(via) >= maxRedirects {
		return errors.New("stopped after 10 redirects")
	}
	return nil
}

// redirectAllowed checks one redirect target against a call's allowed hosts.
func redirectAllowed(target *url.URL, via []*http.Request, hosts []string) error {
	if target.Scheme != "http" && target.Scheme != "https" {
		return fmt.Errorf("%w: it is not an http or https address", ErrRedirectNotAllowed)
	}
	if len(via) > 0 && via[len(via)-1].URL.Scheme == "https" && target.Scheme != "https" {
		return fmt.Errorf("%w: it would leave https", ErrRedirectNotAllowed)
	}
	if target.User != nil {
		return fmt.Errorf("%w: it carries a login", ErrRedirectNotAllowed)
	}
	host := strings.TrimSuffix(strings.ToLower(target.Hostname()), ".")
	for _, pattern := range hosts {
		if hostMatches(host, pattern) {
			return nil
		}
	}
	return fmt.Errorf("%w: %s", ErrRedirectNotAllowed, host)
}

// hostMatches reports whether a lower-case host name fits a pattern: the same name,
// or for "*.name" any name below it.
func hostMatches(host, pattern string) bool {
	pattern = strings.TrimSuffix(strings.ToLower(strings.TrimSpace(pattern)), ".")
	switch {
	case host == "" || pattern == "":
		return false
	case strings.HasPrefix(pattern, "*."):
		suffix := pattern[1:] // ".archive.org"
		return len(host) > len(suffix) && strings.HasSuffix(host, suffix)
	}
	return host == pattern
}

func (c *Client) logCall(req *http.Request, status, attempts int, took time.Duration, err error) {
	if !c.log.Enabled(req.Context(), slog.LevelDebug) {
		return
	}
	attrs := []any{
		"method", req.Method,
		"host", req.URL.Host,
		"path", req.URL.Path, // never the query string
		"status", status,
		"ms", took.Milliseconds(),
	}
	if attempts > 1 {
		attrs = append(attrs, "attempts", attempts)
	}
	if err != nil {
		attrs = append(attrs, "err", sanitize(err).Error())
	}
	c.log.Debug("outbound", attrs...)
}

func retryStatus(code int) bool {
	switch code {
	case http.StatusTooManyRequests, http.StatusBadGateway, http.StatusServiceUnavailable, http.StatusGatewayTimeout:
		return true
	}
	return false
}

func defaultBackoff(attempt int) time.Duration {
	base := 500 * time.Millisecond << (attempt - 1) // 500ms, 1s, 2s
	jitter := time.Duration(rand.Int64N(int64(base) / 4))
	return base + jitter
}

// retryAfter reads a Retry-After header in seconds. HTTP dates are rare for
// the APIs we call and are ignored.
func retryAfter(v string) time.Duration {
	if v == "" {
		return 0
	}
	n, err := strconv.Atoi(strings.TrimSpace(v))
	if err != nil || n < 0 {
		return 0
	}
	return time.Duration(n) * time.Second
}

func sleep(ctx context.Context, d time.Duration) error {
	if d <= 0 {
		return ctx.Err()
	}
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-t.C:
		return nil
	}
}

// sanitize strips the query string out of a *url.Error, because net/http puts
// the full URL in it and Torznab-style APIs carry the API key in the query.
func sanitize(err error) error {
	var ue *url.Error
	if errors.As(err, &ue) {
		clean := &url.Error{Op: ue.Op, URL: redactURL(ue.URL), Err: ue.Err}
		return clean
	}
	return err
}

func redactURL(raw string) string {
	u, err := url.Parse(raw)
	if err != nil {
		return "(unparseable url)"
	}
	u.RawQuery = ""
	u.Fragment = ""
	u.User = nil
	return u.String()
}

type cancelBody struct {
	io.ReadCloser
	cancel context.CancelFunc
}

func (b *cancelBody) Close() error {
	err := b.ReadCloser.Close()
	b.cancel()
	return err
}

// ---- per-host token bucket -------------------------------------------------

type bucket struct {
	rate   float64 // tokens per second
	burst  float64
	tokens float64
	last   time.Time
}

type limiter struct {
	mu       sync.Mutex
	defRate  float64
	defBurst int
	buckets  map[string]*bucket
	now      func() time.Time
	sleep    func(context.Context, time.Duration) error
}

func newLimiter(rps float64, burst int, now func() time.Time, sl func(context.Context, time.Duration) error) *limiter {
	if now == nil {
		now = time.Now
	}
	if sl == nil {
		sl = sleep
	}
	return &limiter{
		defRate: rps, defBurst: burst,
		buckets: map[string]*bucket{},
		now:     now, sleep: sl,
	}
}

func (l *limiter) set(host string, rps float64, burst int) {
	l.mu.Lock()
	defer l.mu.Unlock()
	if burst < 1 {
		burst = 1
	}
	l.buckets[host] = &bucket{rate: rps, burst: float64(burst), tokens: float64(burst), last: l.now()}
}

// reserve takes one token and returns how long the caller must wait before
// using it.
func (l *limiter) reserve(host string) time.Duration {
	l.mu.Lock()
	defer l.mu.Unlock()
	now := l.now()
	b := l.buckets[host]
	if b == nil {
		b = &bucket{rate: l.defRate, burst: float64(l.defBurst), tokens: float64(l.defBurst), last: now}
		l.buckets[host] = b
	}
	if elapsed := now.Sub(b.last); elapsed > 0 {
		b.tokens = min(b.burst, b.tokens+elapsed.Seconds()*b.rate)
		b.last = now
	}
	b.tokens--
	if b.tokens >= 0 {
		return 0
	}
	return time.Duration(-b.tokens / b.rate * float64(time.Second))
}

func (l *limiter) wait(ctx context.Context, host string) error {
	d := l.reserve(host)
	if d <= 0 {
		return nil
	}
	if err := l.sleep(ctx, d); err != nil {
		return fmt.Errorf("outbound: rate limit wait for %s: %w", host, err)
	}
	return nil
}
