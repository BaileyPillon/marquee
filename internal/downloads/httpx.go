package downloads

import (
	"fmt"
	"io"
	"net/http"
	"net/url"
	"path"
	"strings"

	"github.com/BaileyPillon/marquee/internal/core/outbound"
)

// maxReply caps what one client reply may weigh. A busy client's torrent list
// is a few MB at most.
const maxReply = 32 << 20

// normalizeBase checks a client address and tidies it: http or https, a host,
// no query, no fragment, no trailing slash. A sub-path (a reverse proxy in
// front of the client) is kept.
func normalizeBase(raw string) (string, error) {
	raw = strings.TrimSpace(raw)
	u, err := url.Parse(raw)
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Hostname() == "" {
		return "", &InvalidError{Message: "The address must be a web address such as http://192.168.1.10:8080."}
	}
	u.RawQuery, u.Fragment, u.User = "", "", nil
	return strings.TrimRight(u.String(), "/"), nil
}

// send performs one request and reads the reply. A network failure comes back
// wrapped in ErrUnreachable (outbound has already stripped any query string
// from the error). Nothing here puts a URL into an error.
func send(hc *outbound.Client, req *http.Request) (status int, header http.Header, body []byte, err error) {
	resp, err := hc.Do(req)
	if err != nil {
		return 0, nil, nil, unreachable(err)
	}
	defer resp.Body.Close()
	body, err = io.ReadAll(io.LimitReader(resp.Body, maxReply))
	if err != nil {
		return 0, nil, nil, unreachable(err)
	}
	return resp.StatusCode, resp.Header, body, nil
}

// limitBody is the reply body, capped at maxReply.
func limitBody(resp *http.Response) io.Reader { return io.LimitReader(resp.Body, maxReply) }

// statusError describes an unexpected HTTP status without naming the URL.
func statusError(who string, status int) error {
	switch {
	case status == http.StatusUnauthorized || status == http.StatusForbidden:
		return fmt.Errorf("%w (%s answered HTTP %d)", ErrAuth, who, status)
	case status >= 500:
		return fmt.Errorf("%w: %s answered HTTP %d", ErrUnreachable, who, status)
	}
	return fmt.Errorf("%s answered an unexpected HTTP %d", who, status)
}

// joinClientPath joins a client's folder and a name the way the client does:
// with backslashes if the folder uses them (a Windows client), else slashes.
func joinClientPath(dir, name string) string {
	switch {
	case dir == "":
		return name
	case name == "":
		return dir
	case strings.Contains(dir, `\`) && !strings.Contains(dir, "/"):
		return strings.TrimRight(dir, `\`) + `\` + name
	}
	return path.Join(dir, name)
}
