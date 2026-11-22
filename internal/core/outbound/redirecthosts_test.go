package outbound

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"testing"
)

func TestHostMatches(t *testing.T) {
	yes := [][2]string{
		{"coverartarchive.org", "coverartarchive.org"},
		{"archive.org", "archive.org"},
		{"archive.org", "ARCHIVE.org"},
		{"archive.org", " archive.org. "},
		{"ia800000.us.archive.org", "*.archive.org"},
		{"a.b.archive.org", "*.archive.org"},
		{"127.0.0.1", "127.0.0.1"},
	}
	for _, c := range yes {
		if !hostMatches(c[0], c[1]) {
			t.Errorf("hostMatches(%q, %q) = false", c[0], c[1])
		}
	}
	no := [][2]string{
		{"archive.org", "*.archive.org"},          // the wildcard needs a label in front
		{"evilarchive.org", "*.archive.org"},      // a different name that ends the same way
		{"archive.org.evil.com", "archive.org"},   // the name only at the start
		{"archive.org.evil.com", "*.archive.org"}, // and not in the middle either
		{"xarchive.org", "archive.org"},
		{"evil.com", "archive.org"},
		{"", ""},
		{"archive.org", ""},
		{"", "archive.org"},
		{"127.0.0.2", "*.0.0.1"}, // a wildcard is for names, not addresses
	}
	for _, c := range no {
		if hostMatches(c[0], c[1]) {
			t.Errorf("hostMatches(%q, %q) = true", c[0], c[1])
		}
	}
}

func mustURL(t *testing.T, s string) *url.URL {
	t.Helper()
	u, err := url.Parse(s)
	if err != nil {
		t.Fatal(err)
	}
	return u
}

func TestRedirectAllowed(t *testing.T) {
	from := func(s string) []*http.Request {
		r, _ := http.NewRequest("GET", s, nil)
		return []*http.Request{r}
	}
	hosts := []string{"coverartarchive.org", "archive.org", "*.archive.org"}
	for _, ok := range []string{
		"https://archive.org/download/mbid-1/front.jpg",
		"https://ia800000.us.archive.org/1/items/x/front.jpg?cnt=0",
		"https://coverartarchive.org/release/x/front",
	} {
		if err := redirectAllowed(mustURL(t, ok), from("https://coverartarchive.org/a"), hosts); err != nil {
			t.Errorf("%s: %v", ok, err)
		}
	}
	for name, bad := range map[string]string{
		"another host":        "https://evil.example/x.jpg",
		"a look-alike":        "https://archive.org.evil.example/x.jpg",
		"a longer name":       "https://notarchive.org/x.jpg",
		"an address":          "https://169.254.169.254/latest/meta-data",
		"a local name":        "https://localhost/x",
		"leaves https":        "http://archive.org/x.jpg",
		"another scheme":      "ftp://archive.org/x.jpg",
		"a magnet link":       "magnet:?xt=urn:btih:dd8255ecdc7ca55fb0bbf81323d87062db1f6d1c",
		"a login in the link": "https://user:pw@archive.org/x.jpg",
	} {
		err := redirectAllowed(mustURL(t, bad), from("https://coverartarchive.org/a"), hosts)
		if !errors.Is(err, ErrRedirectNotAllowed) {
			t.Errorf("%s (%s): %v, want ErrRedirectNotAllowed", name, bad, err)
		}
	}
	// No hosts at all: nothing is followed.
	if err := redirectAllowed(mustURL(t, "https://archive.org/x"), from("https://archive.org/a"), nil); !errors.Is(err, ErrRedirectNotAllowed) {
		t.Errorf("no hosts: %v", err)
	}
	// From plain http, staying on http is allowed when the host is.
	if err := redirectAllowed(mustURL(t, "http://archive.org/x"), from("http://archive.org/a"), hosts); err != nil {
		t.Errorf("http to http: %v", err)
	}
}

// stubTransport answers each URL from a table, so a redirect to a host that does
// not exist here (ia800000.us.archive.org) can be followed without a network.
type stubTransport struct {
	mu    sync.Mutex
	pages map[string]stubPage
	seen  []string
}

type stubPage struct {
	status   int
	location string
	body     string
}

func (s *stubTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	s.mu.Lock()
	s.seen = append(s.seen, req.URL.String())
	p, ok := s.pages[req.URL.String()]
	s.mu.Unlock()
	if !ok {
		p = stubPage{status: 404}
	}
	h := http.Header{}
	if p.location != "" {
		h.Set("Location", p.location)
	}
	return &http.Response{StatusCode: p.status, Status: http.StatusText(p.status), Header: h, Request: req,
		Body: io.NopCloser(strings.NewReader(p.body))}, nil
}

func (s *stubTransport) hits() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]string(nil), s.seen...)
}

func TestWithRedirectHostsFollowsTheAllowedAndStopsAtTheRest(t *testing.T) {
	st := &stubTransport{pages: map[string]stubPage{
		// The Cover Art Archive hands a cover to archive.org, which hands it to a storage server.
		"https://coverartarchive.org/release-group/1/front": {status: 307, location: "https://archive.org/download/mbid-1/mbid-1-front.jpg"},
		"https://archive.org/download/mbid-1/mbid-1-front.jpg": {status: 302,
			location: "https://ia800000.us.archive.org/9/items/mbid-1/mbid-1-front.jpg"},
		"https://ia800000.us.archive.org/9/items/mbid-1/mbid-1-front.jpg": {status: 200, body: "IMAGE"},
		// A look-alike that sends the call somewhere it must not go.
		"https://coverartarchive.org/release-group/2/front": {status: 307, location: "https://evil.example/steal.jpg"},
		"https://evil.example/steal.jpg":                    {status: 200, body: "EVIL"},
		// And one that tries to leave https.
		"https://coverartarchive.org/release-group/3/front": {status: 307, location: "http://archive.org/x.jpg"},
		"http://archive.org/x.jpg":                          {status: 200, body: "PLAIN"},
	}}
	c := newTestClient(t, Options{})
	c.hc.Transport = st
	ctx := context.Background()
	opt := WithRedirectHosts("coverartarchive.org", "archive.org", "*.archive.org")

	resp, err := c.Get(ctx, "https://coverartarchive.org/release-group/1/front", opt)
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if string(body) != "IMAGE" || len(st.hits()) != 3 {
		t.Fatalf("body %q after %v", body, st.hits())
	}

	for _, path := range []string{"2", "3"} {
		resp, err = c.Get(ctx, "https://coverartarchive.org/release-group/"+path+"/front", opt)
		if !errors.Is(err, ErrRedirectNotAllowed) {
			if resp != nil {
				resp.Body.Close()
			}
			t.Errorf("release-group %s: %v, want ErrRedirectNotAllowed", path, err)
		}
	}
	for _, hit := range st.hits() {
		if strings.Contains(hit, "evil.example") || strings.HasPrefix(hit, "http://") {
			t.Errorf("the client went to %s", hit)
		}
	}

	// A call without the option still follows anywhere: only the calls that ask are limited.
	resp, err = c.Get(ctx, "https://coverartarchive.org/release-group/2/front")
	if err != nil {
		t.Fatal(err)
	}
	body, _ = io.ReadAll(resp.Body)
	resp.Body.Close()
	if string(body) != "EVIL" {
		t.Errorf("an unrestricted call = %q", body)
	}

	// No hosts: no redirect at all.
	if resp, err = c.Get(ctx, "https://coverartarchive.org/release-group/1/front", WithRedirectHosts()); !errors.Is(err, ErrRedirectNotAllowed) {
		if resp != nil {
			resp.Body.Close()
		}
		t.Errorf("with no hosts: %v", err)
	}
}

func TestRedirectErrorDoesNotLeakTheQueryString(t *testing.T) {
	st := &stubTransport{pages: map[string]stubPage{
		"https://one.example/a?apikey=SECRET": {status: 302, location: "https://two.example/b?apikey=SECRET"},
	}}
	c := newTestClient(t, Options{})
	c.hc.Transport = st
	_, err := c.Get(context.Background(), "https://one.example/a?apikey=SECRET", WithRedirectHosts("one.example"))
	if !errors.Is(err, ErrRedirectNotAllowed) {
		t.Fatalf("err = %v", err)
	}
	if strings.Contains(err.Error(), "SECRET") {
		t.Errorf("the error carries the key: %v", err)
	}
}

func TestRedirectHostsWithARealServer(t *testing.T) {
	var final *httptest.Server
	final = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { _, _ = io.WriteString(w, "ok") }))
	defer final.Close()
	start := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, final.URL+"/x", http.StatusFound)
	}))
	defer start.Close()
	c := newTestClient(t, Options{})

	// Both servers are 127.0.0.1 (different ports): the host is what counts.
	resp, err := c.Get(context.Background(), start.URL, WithRedirectHosts("127.0.0.1"))
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp, err := c.Get(context.Background(), start.URL, WithRedirectHosts("localhost")); !errors.Is(err, ErrRedirectNotAllowed) {
		if resp != nil {
			resp.Body.Close()
		}
		t.Errorf("a different host name: %v", err)
	}
}
