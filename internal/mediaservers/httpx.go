package mediaservers

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"

	"github.com/BaileyPillon/marquee/internal/core/outbound"
)

// normalizeBase checks a server address and tidies it: http or https, a host,
// no query, no fragment, no trailing slash. A sub-path (a reverse proxy) stays.
func normalizeBase(raw string) (string, error) {
	raw = strings.TrimSpace(raw)
	u, err := url.Parse(raw)
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Hostname() == "" {
		return "", &InvalidError{Message: "The address must be a web address such as http://192.168.1.10:8096."}
	}
	u.RawQuery, u.Fragment, u.User = "", "", nil
	return strings.TrimRight(u.String(), "/"), nil
}

func hostOf(raw string) string {
	u, err := url.Parse(raw)
	if err != nil {
		return ""
	}
	return strings.ToLower(u.Host)
}

// caller sends requests to one server.
type caller struct {
	hc      *outbound.Client
	base    string
	headers map[string]string
}

// reply is one answer.
type reply struct {
	status int
	body   []byte
}

// do sends a request and reads the answer (capped at maxReply). Only network
// failures are errors here; the caller decides what a status means.
func (c *caller) do(ctx context.Context, method, path string, query url.Values, body any, timeout ...outbound.CallOption) (reply, error) {
	var rd io.Reader
	if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			return reply{}, err
		}
		rd = bytes.NewReader(b)
	}
	u := c.base + path
	if len(query) > 0 {
		u += "?" + query.Encode()
	}
	req, err := http.NewRequestWithContext(ctx, method, u, rd)
	if err != nil {
		return reply{}, fmt.Errorf("the server address is not usable")
	}
	for k, v := range c.headers {
		req.Header.Set(k, v)
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := c.hc.Do(req, timeout...)
	if err != nil {
		return reply{}, unreachable(err)
	}
	defer resp.Body.Close()
	b, err := io.ReadAll(io.LimitReader(resp.Body, maxReply))
	if err != nil {
		return reply{}, unreachable(err)
	}
	return reply{status: resp.StatusCode, body: b}, nil
}

// ok reports a 2xx status.
func (r reply) ok() bool { return r.status >= 200 && r.status < 300 }

// err turns an unexpected status into an error that never names the URL.
func (r reply) err(who string) error {
	switch {
	case r.status == http.StatusUnauthorized || r.status == http.StatusForbidden:
		return fmt.Errorf("%w (%s answered HTTP %d)", ErrAuth, who, r.status)
	case r.status >= 500:
		return fmt.Errorf("%w: %s answered HTTP %d", ErrUnreachable, who, r.status)
	}
	return fmt.Errorf("%s answered an unexpected HTTP %d", who, r.status)
}

// json decodes the body into dst.
func (r reply) json(who string, dst any) error {
	if err := json.Unmarshal(r.body, dst); err != nil {
		return fmt.Errorf("%s sent an answer Marquee could not read", who)
	}
	return nil
}
