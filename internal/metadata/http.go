package metadata

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"

	"github.com/BaileyPillon/marquee/internal/core/outbound"
)

// maxBody caps one provider response. TVmaze answers for a long-running show
// with every episode embedded run to a few megabytes.
const maxBody = 16 << 20

// statusError carries an unexpected HTTP status inside an *Error.
type statusError struct{ Status int }

func (e *statusError) Error() string { return fmt.Sprintf("HTTP %d", e.Status) }

// getter sends GET requests for one provider through the outbound client.
type getter struct {
	hc       *outbound.Client
	provider string
}

// get fetches rawURL and returns the body of a 200 answer. A 404 is
// ErrNotFound; every other failure is an upstream error carrying a
// *statusError or the (already query-free) transport error. The URL, which may
// carry an API key, is never put in an error.
func (g getter) get(ctx context.Context, rawURL string, header http.Header) ([]byte, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, rawURL, nil)
	if err != nil {
		return nil, upstream(g.provider, errors.New("could not build the request")) // err can quote the URL, and the URL can hold a key
	}
	req.Header.Set("Accept", "application/json")
	for k, v := range header {
		req.Header[k] = v
	}
	resp, err := g.hc.Do(req)
	if err != nil {
		return nil, upstream(g.provider, err)
	}
	defer resp.Body.Close()

	switch {
	case resp.StatusCode == http.StatusOK:
	case resp.StatusCode == http.StatusNotFound:
		_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 4096))
		return nil, notFound(g.provider)
	default:
		_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 4096))
		return nil, upstream(g.provider, &statusError{Status: resp.StatusCode})
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, maxBody+1))
	if err != nil {
		return nil, upstream(g.provider, fmt.Errorf("reading the answer: %w", err))
	}
	if len(body) > maxBody {
		return nil, upstream(g.provider, fmt.Errorf("the answer is larger than %d MB", maxBody>>20))
	}
	return body, nil
}

// baseURL parses and validates a provider base URL.
func baseURL(raw string) (*url.URL, error) {
	u, err := url.Parse(strings.TrimRight(raw, "/"))
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
		return nil, fmt.Errorf("metadata: bad base URL %q", raw)
	}
	return u, nil
}
