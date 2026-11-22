package web

import (
	"context"
	"net"
	"net/http"
	"net/netip"
	"strings"
)

// Reverse proxies. Behind one, every request arrives from the proxy's address, so
// the visitor's own address (for the sign-in rate limit, the session list and the
// logs) and whether they used HTTPS (for the Secure flag of the session cookie)
// can only come from headers the proxy adds: X-Forwarded-For and
// X-Forwarded-Proto. Anybody can send those headers, so they are believed only on
// a request that comes from a trusted proxy (the "server" settings, or the
// environment variable MARQUEE_TRUSTED_PROXIES). On any other request they are
// ignored, whatever they say.

// forwarded is what a trusted proxy said about a request.
type forwarded struct {
	ip    string // the visitor's address; "" when the proxy did not say
	https bool
}

// forwardedFor believes the proxy headers of a request that came from a trusted
// proxy, and ignores them otherwise.
func (s *Server) forwardedFor(r *http.Request) (forwarded, bool) {
	cfg := s.runtime(r.Context())
	if len(cfg.proxies) == 0 {
		return forwarded{}, false
	}
	peer, ok := peerAddr(r.RemoteAddr)
	if !ok || !cfg.trusts(peer) {
		return forwarded{}, false
	}
	var f forwarded
	if ip, ok := originalClient(r.Header.Values("X-Forwarded-For"), cfg.trusts); ok {
		f.ip = ip.String()
	}
	f.https = schemeIsHTTPS(r.Header.Values("X-Forwarded-Proto"))
	return f, true
}

// forwardedMiddleware puts what a trusted proxy said about the request into its
// context, for clientIP and requestIsHTTPS.
func (s *Server) forwardedMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if f, ok := s.forwardedFor(r); ok {
			r = r.WithContext(context.WithValue(r.Context(), forwardedKey, f))
		}
		next.ServeHTTP(w, r)
	})
}

// peerAddr is the address a connection came from.
func peerAddr(remote string) (netip.Addr, bool) {
	if ap, err := netip.ParseAddrPort(remote); err == nil {
		return ap.Addr().Unmap(), true
	}
	if a, err := netip.ParseAddr(remote); err == nil {
		return a.Unmap(), true
	}
	return netip.Addr{}, false
}

// originalClient reads X-Forwarded-For (any number of header lines, each a comma
// separated list, the nearest hop last) and returns the visitor: the nearest
// address that is not itself a trusted proxy, so a visitor cannot pick their own
// address by sending the header themselves (the proxy adds the real one after it).
// When every hop is a trusted proxy the first is the visitor. A header with an entry
// that is not an address is not believed at all.
func originalClient(values []string, trusted func(netip.Addr) bool) (netip.Addr, bool) {
	var hops []netip.Addr
	for _, v := range values {
		for _, part := range strings.Split(v, ",") {
			part = strings.TrimSpace(part)
			if part == "" {
				continue
			}
			a, ok := parseHop(part)
			if !ok {
				return netip.Addr{}, false
			}
			hops = append(hops, a)
		}
	}
	if len(hops) == 0 {
		return netip.Addr{}, false
	}
	for i := len(hops) - 1; i >= 0; i-- {
		if !trusted(hops[i]) {
			return hops[i], true
		}
	}
	return hops[0], true
}

// parseHop reads one X-Forwarded-For entry: an address, with or without a port,
// IPv6 in brackets or not.
func parseHop(s string) (netip.Addr, bool) {
	s = strings.Trim(s, `"`)
	if a, err := netip.ParseAddr(s); err == nil {
		return a.Unmap(), true
	}
	if ap, err := netip.ParseAddrPort(s); err == nil {
		return ap.Addr().Unmap(), true
	}
	if strings.HasPrefix(s, "[") && strings.HasSuffix(s, "]") {
		if a, err := netip.ParseAddr(s[1 : len(s)-1]); err == nil {
			return a.Unmap(), true
		}
	}
	return netip.Addr{}, false
}

// schemeIsHTTPS reads X-Forwarded-Proto. The nearest proxy's word counts (the last
// one, when several were added).
func schemeIsHTTPS(values []string) bool {
	last := ""
	for _, v := range values {
		for _, part := range strings.Split(v, ",") {
			if p := strings.TrimSpace(part); p != "" {
				last = p
			}
		}
	}
	return strings.EqualFold(last, "https")
}

// clientIP is the address of the person making the request: the one a trusted
// proxy reported, else the connection's own.
func clientIP(r *http.Request) string {
	if f, ok := r.Context().Value(forwardedKey).(forwarded); ok && f.ip != "" {
		return f.ip
	}
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return r.RemoteAddr
	}
	return host
}

// requestIsHTTPS reports whether the person reached Marquee over HTTPS: the
// connection is TLS, or a trusted proxy said it was.
func requestIsHTTPS(r *http.Request) bool {
	if r.TLS != nil {
		return true
	}
	f, ok := r.Context().Value(forwardedKey).(forwarded)
	return ok && f.https
}
