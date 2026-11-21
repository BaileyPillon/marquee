package alerts

import (
	"bufio"
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"database/sql"
	"encoding/base64"
	"errors"
	"io"
	"log/slog"
	"math/big"
	"net"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/BaileyPillon/marquee/internal/core/db"
	"github.com/BaileyPillon/marquee/internal/core/events"
	"github.com/BaileyPillon/marquee/internal/core/outbound"
	"github.com/BaileyPillon/marquee/internal/core/secrets"
)

func bg() context.Context { return context.Background() }

func quiet() *slog.Logger { return slog.New(slog.NewTextHandler(io.Discard, nil)) }

// ---- a fake place to send to -------------------------------------------------

// hit is one request the recorder saw.
type hit struct {
	Method string
	Path   string
	Query  string
	Header http.Header
	Body   []byte
}

// recorder is a server that remembers every request and answers with a status
// and body the test chooses (200 and "{}" by default).
type recorder struct {
	srv    *httptest.Server
	mu     sync.Mutex
	hits   []hit
	status map[string]int // path prefix -> status
	body   map[string]string
}

func newRecorder(t *testing.T) *recorder {
	r := &recorder{status: map[string]int{}, body: map[string]string{}}
	r.srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		b, _ := io.ReadAll(req.Body)
		r.mu.Lock()
		r.hits = append(r.hits, hit{req.Method, req.URL.Path, req.URL.RawQuery, req.Header.Clone(), b})
		status, body := http.StatusOK, "{}"
		for prefix, st := range r.status {
			if strings.HasPrefix(req.URL.Path, prefix) {
				status = st
			}
		}
		for prefix, bd := range r.body {
			if strings.HasPrefix(req.URL.Path, prefix) {
				body = bd
			}
		}
		r.mu.Unlock()
		w.WriteHeader(status)
		_, _ = w.Write([]byte(body))
	}))
	t.Cleanup(r.srv.Close)
	return r
}

func (r *recorder) all() []hit {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]hit(nil), r.hits...)
}

func (r *recorder) only(t *testing.T) hit {
	t.Helper()
	h := r.all()
	if len(h) != 1 {
		t.Fatalf("%d requests, want exactly 1: %+v", len(h), h)
	}
	return h[0]
}

func (r *recorder) answer(prefix string, status int, body string) {
	r.mu.Lock()
	r.status[prefix], r.body[prefix] = status, body
	r.mu.Unlock()
}

func (r *recorder) reset() {
	r.mu.Lock()
	r.hits = nil
	r.mu.Unlock()
}

// ---- fakes the service reads from -------------------------------------------------

type fakeRequests struct {
	mu    sync.Mutex
	byID  map[int64]RequestInfo
	err   error
	gate  chan struct{} // when set, Request waits for it
	calls int
}

func (f *fakeRequests) Request(ctx context.Context, id int64) (RequestInfo, error) {
	f.mu.Lock()
	gate := f.gate
	f.calls++
	f.mu.Unlock()
	if gate != nil {
		select {
		case <-gate:
		case <-ctx.Done():
			return RequestInfo{}, ctx.Err()
		}
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.err != nil {
		return RequestInfo{}, f.err
	}
	r, ok := f.byID[id]
	if !ok {
		return RequestInfo{}, errors.New("no such request")
	}
	return r, nil
}

type fakeDownloads struct{ byID map[int64]DownloadInfo }

func (f *fakeDownloads) Download(_ context.Context, id int64) (DownloadInfo, error) {
	if d, ok := f.byID[id]; ok {
		return d, nil
	}
	return DownloadInfo{}, errors.New("no such download")
}

// clock is a time the test moves.
type clock struct {
	mu sync.Mutex
	t  time.Time
}

func (c *clock) Now() time.Time { c.mu.Lock(); defer c.mu.Unlock(); return c.t }
func (c *clock) Advance(d time.Duration) {
	c.mu.Lock()
	c.t = c.t.Add(d)
	c.mu.Unlock()
}

// ---- the rig ---------------------------------------------------------------------

type env struct {
	t     *testing.T
	db    *sql.DB
	box   *secrets.Box
	hub   *events.Hub
	svc   *Service
	rec   *recorder
	reqs  *fakeRequests
	dls   *fakeDownloads
	clock *clock
}

type rigOpts struct {
	hubBuffer int
	tls       *tls.Config
}

func newEnv(t *testing.T, ro ...rigOpts) *env {
	t.Helper()
	d, err := db.Open(filepath.Join(t.TempDir(), db.MainFile))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { d.Close() })
	if _, err := db.Migrate(bg(), d, db.SetMain); err != nil {
		t.Fatal(err)
	}
	box, err := secrets.LoadOrCreate(filepath.Join(t.TempDir(), "marquee.key"), quiet())
	if err != nil {
		t.Fatal(err)
	}
	var o rigOpts
	if len(ro) > 0 {
		o = ro[0]
	}
	hubOpts := []events.Option{}
	if o.hubBuffer > 0 {
		hubOpts = append(hubOpts, events.WithBuffer(o.hubBuffer))
	}
	hub := events.NewHub(quiet(), hubOpts...)
	t.Cleanup(hub.Close)
	e := &env{
		t: t, db: d, box: box, hub: hub, rec: newRecorder(t),
		reqs:  &fakeRequests{byID: map[int64]RequestInfo{}},
		dls:   &fakeDownloads{byID: map[int64]DownloadInfo{}},
		clock: &clock{t: time.Date(2026, 11, 21, 12, 0, 0, 0, time.UTC)},
	}
	hc := outbound.New("test", outbound.Options{Logger: quiet(), MaxRetries: -1, HostRPS: 100000, HostBurst: 100000})
	e.svc = NewService(d, box, hc, Options{
		Logger: quiet(), Now: e.clock.Now, Hub: hub, Requests: e.reqs, Downloads: e.dls,
		TelegramURL: e.rec.srv.URL + "/telegram", PushoverURL: e.rec.srv.URL + "/pushover/1/messages.json",
		SMTPTLS: o.tls,
	})
	t.Cleanup(e.svc.Close)
	return e
}

func ptr[T any](v T) *T { return &v }

// channel creates a channel and fails the test if that does not work.
func (e *env) channel(name, kind string, cfg map[string]any, events ...string) Channel {
	e.t.Helper()
	in := Input{Name: &name, Kind: &kind, Config: cfg}
	if events != nil {
		in.Events = &events
	}
	ch, err := e.svc.Create(bg(), in)
	if err != nil {
		e.t.Fatalf("Create(%s): %v", name, err)
	}
	return ch
}

// webhookChannel is a webhook pointed at the recorder.
func (e *env) webhookChannel(name string, events ...string) Channel {
	return e.channel(name, KindWebhook, map[string]any{"url": e.rec.srv.URL + "/hook/" + name}, events...)
}

// start runs the hub listener and returns once it is listening.
func (e *env) start() {
	e.t.Helper()
	ctx, cancel := context.WithCancel(bg())
	done := make(chan struct{})
	go func() { e.svc.Run(ctx); close(done) }()
	e.t.Cleanup(func() { cancel(); <-done })
	waitFor(e.t, func() bool { return e.hub.Len() == 1 })
}

func waitFor(t *testing.T, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for !cond() {
		if time.Now().After(deadline) {
			t.Fatal("timed out waiting")
		}
		time.Sleep(5 * time.Millisecond)
	}
}

// ---- a fake mail server ------------------------------------------------------------

// smtpServer is a small SMTP server: it can offer STARTTLS, or speak TLS from the
// first byte, and it keeps the mail it was given.
type smtpServer struct {
	addr     string
	mu       sync.Mutex
	mails    []string
	froms    []string
	rcpts    []string
	auths    []string
	starttls bool // offer STARTTLS
	cert     tls.Certificate
	tlsFirst bool
	sawTLS   bool
}

func selfSigned(t *testing.T) tls.Certificate {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	tmpl := &x509.Certificate{
		SerialNumber: big.NewInt(1), Subject: pkix.Name{CommonName: "localhost"},
		NotBefore: time.Now().Add(-time.Hour), NotAfter: time.Now().Add(time.Hour),
		DNSNames: []string{"localhost"}, IPAddresses: []net.IP{net.ParseIP("127.0.0.1")},
		KeyUsage: x509.KeyUsageDigitalSignature, ExtKeyUsage: []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &key.PublicKey, key)
	if err != nil {
		t.Fatal(err)
	}
	return tls.Certificate{Certificate: [][]byte{der}, PrivateKey: key}
}

func newSMTP(t *testing.T, starttls, tlsFirst bool) *smtpServer {
	t.Helper()
	s := &smtpServer{starttls: starttls, tlsFirst: tlsFirst, cert: selfSigned(t)}
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	if tlsFirst {
		ln = tls.NewListener(ln, &tls.Config{Certificates: []tls.Certificate{s.cert}})
	}
	s.addr = ln.Addr().String()
	t.Cleanup(func() { ln.Close() })
	go func() {
		for {
			c, err := ln.Accept()
			if err != nil {
				return
			}
			go s.serve(c)
		}
	}()
	return s
}

func (s *smtpServer) serve(c net.Conn) {
	defer c.Close()
	br := bufio.NewReader(c)
	say := func(line string) { _, _ = io.WriteString(c, line+"\r\n") }
	secure := s.tlsFirst
	say("220 fake ESMTP")
	var data bool
	var buf strings.Builder
	for {
		line, err := br.ReadString('\n')
		if err != nil {
			return
		}
		line = strings.TrimRight(line, "\r\n")
		if data {
			if line == "." {
				data = false
				s.mu.Lock()
				s.mails = append(s.mails, buf.String())
				s.mu.Unlock()
				say("250 queued")
				continue
			}
			buf.WriteString(strings.TrimPrefix(line, ".") + "\n") // undo dot-stuffing
			continue
		}
		cmd := strings.ToUpper(line)
		switch {
		case strings.HasPrefix(cmd, "EHLO"), strings.HasPrefix(cmd, "HELO"):
			say("250-fake")
			if s.starttls && !secure {
				say("250-STARTTLS")
			}
			if secure {
				say("250-AUTH PLAIN")
			}
			say("250 8BITMIME")
		case cmd == "STARTTLS":
			say("220 go ahead")
			tc := tls.Server(c, &tls.Config{Certificates: []tls.Certificate{s.cert}})
			if err := tc.Handshake(); err != nil {
				return
			}
			c = tc
			br = bufio.NewReader(tc)
			secure = true
			s.mu.Lock()
			s.sawTLS = true
			s.mu.Unlock()
		case strings.HasPrefix(cmd, "AUTH PLAIN"):
			raw, _ := base64.StdEncoding.DecodeString(strings.TrimSpace(line[len("AUTH PLAIN"):]))
			s.mu.Lock()
			s.auths = append(s.auths, strings.ReplaceAll(string(raw), "\x00", "|"))
			s.mu.Unlock()
			say("235 ok")
		case strings.HasPrefix(cmd, "MAIL FROM:"):
			s.mu.Lock()
			s.froms = append(s.froms, between(line[len("MAIL FROM:"):]))
			s.mu.Unlock()
			say("250 ok")
		case strings.HasPrefix(cmd, "RCPT TO:"):
			s.mu.Lock()
			s.rcpts = append(s.rcpts, between(line[len("RCPT TO:"):]))
			s.mu.Unlock()
			say("250 ok")
		case cmd == "DATA":
			data = true
			buf.Reset()
			say("354 go")
		case cmd == "QUIT":
			say("221 bye")
			return
		default:
			say("250 ok")
		}
	}
}

func (s *smtpServer) host() string { h, _, _ := net.SplitHostPort(s.addr); return h }
func (s *smtpServer) port() string { _, p, _ := net.SplitHostPort(s.addr); return p }

func testNow() time.Time { return time.Date(2026, 11, 21, 12, 0, 0, 0, time.UTC) }

// between returns what is inside the angle brackets of "<a@b.test> BODY=8BITMIME".
func between(s string) string {
	i, j := strings.Index(s, "<"), strings.Index(s, ">")
	if i < 0 || j < i {
		return strings.TrimSpace(s)
	}
	return s[i+1 : j]
}
