package web

import (
	"context"
	"errors"
	"fmt"
	"net/netip"
	"net/url"
	"strings"
	"time"

	"github.com/BaileyPillon/marquee/internal/downloads"
	"github.com/BaileyPillon/marquee/internal/requests"
)

// ServerSection is the settings section that holds how Marquee is reached:
//
//	{"publicUrl": "https://marquee.example.com", "trustedProxies": ["10.0.0.0/8", "192.168.1.5"]}
//
// publicUrl is the address people use from outside (a reverse proxy's address). The
// MCP address and any absolute link Marquee shows are built from it instead of from
// the request's Host header. trustedProxies are the addresses of reverse proxies in
// front of Marquee: only a request that comes from one of them is believed when it
// says, in X-Forwarded-For and X-Forwarded-Proto, who the visitor is and whether
// they used HTTPS. The environment variable MARQUEE_TRUSTED_PROXIES adds to the list.
const ServerSection = "server"

const (
	maxPublicURLLen   = 300
	maxTrustedProxies = 64
	configTTL         = 5 * time.Second
)

// serverSettings is the section as stored.
type serverSettings struct {
	PublicURL      string   `json:"publicUrl"`
	TrustedProxies []string `json:"trustedProxies"`
}

// runtimeConfig is the section parsed for use on every request. It is rebuilt at
// most every configTTL, and at once after the owner saves the section.
type runtimeConfig struct {
	at        time.Time
	publicURL string
	// proxies are the trusted proxies: the environment's plus the section's.
	proxies []netip.Prefix
	// stored are the section's own entries, as saved.
	stored []string
}

func (c *runtimeConfig) trusts(a netip.Addr) bool {
	a = a.Unmap()
	for _, p := range c.proxies {
		if p.Contains(a) {
			return true
		}
	}
	return false
}

// ParseProxyList reads a list of trusted proxies: each entry is a CIDR range
// ("10.0.0.0/8") or one address ("192.168.1.5", which means just that address).
// A range that would take in every address (/0) is refused: it would make the
// "trusted" part meaningless. Empty entries are skipped.
func ParseProxyList(list []string) ([]netip.Prefix, error) {
	var out []netip.Prefix
	seen := map[netip.Prefix]bool{}
	for _, raw := range list {
		s := strings.TrimSpace(raw)
		if s == "" {
			continue
		}
		var (
			p   netip.Prefix
			err error
		)
		if strings.Contains(s, "/") {
			p, err = netip.ParsePrefix(s)
		} else {
			var a netip.Addr
			if a, err = netip.ParseAddr(s); err == nil {
				a = a.Unmap()
				p = netip.PrefixFrom(a, a.BitLen())
			}
		}
		if err != nil {
			return nil, fmt.Errorf("%q is not a CIDR range or an IP address", s)
		}
		if p.Bits() == 0 {
			return nil, fmt.Errorf("%q would trust every address", s)
		}
		p = p.Masked()
		if !seen[p] {
			seen[p] = true
			out = append(out, p)
		}
	}
	return out, nil
}

// SplitList splits an environment value written as a comma or space separated list.
func SplitList(v string) []string {
	return strings.FieldsFunc(v, func(r rune) bool { return r == ',' || r == ' ' || r == '\n' || r == '\t' || r == ';' })
}

// cleanPublicURL checks an address people use from outside and returns it without
// a trailing slash: http or https, a host, no login, no query. An empty address
// means "not set".
func cleanPublicURL(raw string) (string, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return "", nil
	}
	if len(raw) > maxPublicURLLen {
		return "", errors.New("that address is too long")
	}
	u, err := url.Parse(raw)
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Hostname() == "" {
		return "", errors.New("write it like https://marquee.example.com")
	}
	if u.User != nil || u.RawQuery != "" || u.Fragment != "" || strings.ContainsAny(raw, " \t\r\n") {
		return "", errors.New("it must be an address without a login, a query or a fragment")
	}
	return strings.TrimRight(u.Scheme+"://"+u.Host+u.EscapedPath(), "/"), nil
}

// normalizeServerSection checks what the owner sent for the "server" section and
// returns it in the form it is stored: a clean publicUrl and a clean list of
// trusted proxies.
func normalizeServerSection(in map[string]any) (map[string]any, error) {
	out := make(map[string]any, len(in)+2)
	for k, v := range in {
		out[k] = v
	}
	pub := ""
	if v, ok := in["publicUrl"]; ok && v != nil {
		s, isStr := v.(string)
		if !isStr {
			return nil, &sectionError{code: "invalid_request", message: "publicUrl must be text."}
		}
		var err error
		if pub, err = cleanPublicURL(s); err != nil {
			return nil, &sectionError{code: "invalid_request", message: "publicUrl: " + err.Error() + "."}
		}
	}
	out["publicUrl"] = pub

	var list []string
	switch v := in["trustedProxies"].(type) {
	case nil:
	case string:
		list = SplitList(v)
	case []any:
		for _, e := range v {
			s, ok := e.(string)
			if !ok {
				return nil, &sectionError{code: "invalid_request", message: "trustedProxies must be a list of text."}
			}
			list = append(list, s)
		}
	default:
		return nil, &sectionError{code: "invalid_request", message: "trustedProxies must be a list."}
	}
	if len(list) > maxTrustedProxies {
		return nil, &sectionError{code: "invalid_request", message: fmt.Sprintf("trustedProxies can hold at most %d entries.", maxTrustedProxies)}
	}
	prefixes, err := ParseProxyList(list)
	if err != nil {
		return nil, &sectionError{code: "invalid_request", message: "trustedProxies: " + err.Error() + "."}
	}
	canon := make([]any, 0, len(prefixes))
	for _, p := range prefixes {
		canon = append(canon, p.String())
	}
	out["trustedProxies"] = canon
	return out, nil
}

// sectionError is a settings section the owner sent that cannot be stored; the
// message says what to fix.
type sectionError struct{ code, message string }

func (e *sectionError) Error() string { return e.message }

// normalizeRequestsSection checks the requests defaults (the weekly limits may be a
// number or the word "unlimited").
func normalizeRequestsSection(in map[string]any) (map[string]any, error) {
	out, err := requests.NormalizeConfig(in)
	var inv *requests.InvalidError
	if errors.As(err, &inv) {
		return nil, &sectionError{code: inv.Code, message: inv.Message}
	}
	return out, err
}

// normalizeDownloadsSection checks the download settings (when to give up on a
// download that makes no progress).
func normalizeDownloadsSection(in map[string]any) (map[string]any, error) {
	out, err := downloads.NormalizeSettings(in)
	var inv *downloads.InvalidError
	if errors.As(err, &inv) {
		return nil, &sectionError{code: "invalid_request", message: inv.Message}
	}
	return out, err
}

// normalizers check and tidy a settings section before it is stored. A section with
// none is stored as it came.
var normalizers = map[string]func(map[string]any) (map[string]any, error){
	ServerSection:             normalizeServerSection,
	requests.SettingsSection:  normalizeRequestsSection,
	downloads.SettingsSection: normalizeDownloadsSection,
}

// ---- reading it back -------------------------------------------------------------------

// runtime returns the parsed "server" section (and the environment's proxies),
// cached for a few seconds. A section that cannot be read is logged and treated as
// empty: Marquee then trusts only the proxies the environment names.
func (s *Server) runtime(ctx context.Context) *runtimeConfig {
	if c := s.rt.Load(); c != nil && time.Since(c.at) < configTTL {
		return c
	}
	c := &runtimeConfig{at: time.Now(), proxies: append([]netip.Prefix(nil), s.envProxies...)}
	if s.Settings != nil {
		rctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 3*time.Second)
		defer cancel()
		var sec serverSettings
		if _, err := s.Settings.Get(rctx, ServerSection, &sec); err != nil {
			s.log.Warn("the server settings cannot be read; using the environment's trusted proxies only", "err", err)
		} else {
			c.publicURL, _ = cleanPublicURL(sec.PublicURL)
			prefixes, err := ParseProxyList(sec.TrustedProxies)
			if err != nil {
				s.log.Warn("the trusted proxies in the server settings are not valid; ignoring them", "err", err)
			} else {
				for _, p := range prefixes {
					c.stored = append(c.stored, p.String())
				}
				c.proxies = append(c.proxies, prefixes...)
			}
		}
	}
	s.rt.Store(c)
	return c
}

// forgetRuntime makes the next request read the "server" section again.
func (s *Server) forgetRuntime() { s.rt.Store(nil) }
