package main

import (
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/netip"
	"net/url"
	"strconv"
	"strings"
	"syscall"
	"time"
)

// "marquee healthcheck" asks a running Marquee whether it is well and exits 0 if
// so. It exists for Docker: the image is FROM scratch, so there is no shell or
// curl to write a HEALTHCHECK with. It is a probe, not a start: main runs it
// before any flag parsing and it never opens the config folder, the databases or
// the master key.

const (
	healthPath           = "/api/v1/health"
	healthUsage          = "usage: marquee healthcheck [-addr host:port]"
	defaultHealthTimeout = 3 * time.Second
)

// healthTimeout is the whole budget of one check (connect, send, read the
// answer). It is a variable only so a test can shorten it.
var healthTimeout = defaultHealthTimeout

// runHealthcheck runs the subcommand with the arguments after "healthcheck" and
// returns the exit code: 0 only when /api/v1/health answered 200 with status
// "ok". Everything else prints one line starting "unhealthy:" to stderr and
// returns 1 (so does a usage mistake, and -h: nothing but a healthy answer
// exits 0, so a typo in a HEALTHCHECK line can never pass).
func runHealthcheck(args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("marquee healthcheck", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	addr := fs.String("addr", "", "address Marquee listens on (default: env MARQUEE_ADDR, else :6464)")
	if err := fs.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			fmt.Fprintln(stderr, healthUsage)
		} else {
			fmt.Fprintf(stderr, "unhealthy: %v (%s)\n", err, healthUsage)
		}
		return 1
	}
	if fs.NArg() > 0 {
		fmt.Fprintf(stderr, "unhealthy: unexpected argument %q (%s)\n", fs.Arg(0), healthUsage)
		return 1
	}

	target, err := healthURL(resolveHealthAddr(*addr))
	if err != nil {
		fmt.Fprintf(stderr, "unhealthy: %v\n", err)
		return 1
	}
	if err := checkHealth(newHealthClient(), target); err != nil {
		fmt.Fprintf(stderr, "unhealthy: %v\n", err)
		return 1
	}
	fmt.Fprintln(stdout, "ok")
	return 0
}

// resolveHealthAddr picks the address to ask: -addr, else MARQUEE_ADDR, else the
// default, which is what the server itself would listen on.
func resolveHealthAddr(flagValue string) string {
	if flagValue != "" {
		return flagValue
	}
	return envOr("MARQUEE_ADDR", defaultAddr)
}

// healthURL turns a listen address into the health URL to call. A listen
// address often has no host (":6464") or the any-address ("0.0.0.0:6464",
// "[::]:6464"), which cannot be dialled, so those mean this machine. An IPv6
// host keeps its brackets.
func healthURL(addr string) (string, error) {
	host, port, err := net.SplitHostPort(addr)
	if err != nil {
		var ae *net.AddrError
		if errors.As(err, &ae) {
			return "", fmt.Errorf("bad address %q: %s", addr, ae.Err)
		}
		return "", fmt.Errorf("bad address %q: %v", addr, err)
	}
	if n, err := strconv.Atoi(port); err != nil || n < 1 || n > 65535 {
		return "", fmt.Errorf("bad address %q: the port must be a number from 1 to 65535", addr)
	}
	if ip, err := netip.ParseAddr(host); host == "" || (err == nil && ip.Unmap().IsUnspecified()) {
		host = "127.0.0.1"
	}
	u := url.URL{Scheme: "http", Host: net.JoinHostPort(host, port), Path: healthPath}
	return u.String(), nil
}

// newHealthClient is a plain client for one local call: no proxy (a proxy
// variable in the container must not send the probe elsewhere), no redirects
// (a health answer that redirects is not healthy) and a hard total timeout.
func newHealthClient() *http.Client {
	return &http.Client{
		Timeout:   healthTimeout,
		Transport: &http.Transport{Proxy: nil, DisableKeepAlives: true},
		CheckRedirect: func(*http.Request, []*http.Request) error {
			return http.ErrUseLastResponse
		},
	}
}

// checkHealth returns nil for HTTP 200 with JSON status "ok", else a short
// reason with no newline in it.
func checkHealth(client *http.Client, target string) error {
	resp, err := client.Get(target)
	if err != nil {
		return describeTransportError(err)
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(io.LimitReader(resp.Body, 64<<10))
	if err != nil {
		return describeTransportError(err)
	}

	var answer struct {
		Status string `json:"status"`
	}
	decodeErr := json.Unmarshal(body, &answer)
	status := clip(answer.Status, 40)

	if resp.StatusCode != http.StatusOK {
		// The server's own 503 says why ("degraded"); anything else is just a code.
		if decodeErr == nil && status != "" {
			return fmt.Errorf("status %s", status)
		}
		return fmt.Errorf("HTTP %d", resp.StatusCode)
	}
	switch {
	case decodeErr != nil:
		return errors.New("the answer is not JSON")
	case status == "":
		return errors.New("the answer has no status")
	case status != "ok":
		return fmt.Errorf("status %s", status)
	}
	return nil
}

// describeTransportError says in a few words why no answer came.
func describeTransportError(err error) error {
	var ne net.Error
	switch {
	case isConnRefused(err):
		return errors.New("connection refused")
	case errors.As(err, &ne) && ne.Timeout():
		return fmt.Errorf("timed out after %s", healthTimeout)
	}
	var ue *url.Error
	if errors.As(err, &ue) {
		err = ue.Err // the address is already known to the caller
	}
	return errors.New(clip(err.Error(), 200))
}

// isConnRefused recognises "connection refused" on Linux (ECONNREFUSED) and on
// Windows, where Go reports the Winsock error 10061 instead.
func isConnRefused(err error) bool {
	if errors.Is(err, syscall.ECONNREFUSED) {
		return true
	}
	var en syscall.Errno
	return errors.As(err, &en) && en == 10061
}

// clip makes text from a server safe to print on one line: control characters
// and runs of white space become single spaces, and it is cut to at most limit
// characters.
func clip(s string, limit int) string {
	s = strings.Map(func(r rune) rune {
		if r < ' ' || r == 0x7f {
			return ' '
		}
		return r
	}, s)
	s = strings.Join(strings.Fields(s), " ")
	if r := []rune(s); len(r) > limit {
		s = string(r[:limit]) + "..."
	}
	return s
}
