package web

import (
	"context"
	"errors"
	"net/http"
	"slices"
	"strings"
	"time"

	"github.com/BaileyPillon/marquee/internal/accounts"
)

// Names fixed by the API contract.
const (
	SessionCookie = "marquee_session"
	CSRFHeader    = "X-Marquee"
	CSRFValue     = "1"
)

func principalFrom(ctx context.Context) *accounts.Principal {
	p, _ := ctx.Value(principalKey).(*accounts.Principal)
	return p
}

// apiGuard runs in front of every /api/ route. It works out who is calling
// (a Bearer API key, else the session cookie) and enforces CSRF.
//
// CSRF rule: browsers attach cookies to cross-site requests but cannot add a
// custom header without a CORS preflight, which we never grant. So every
// request that is not a safe method must carry X-Marquee: 1, unless it is
// authenticated by a Bearer key (which a browser never attaches on its own).
// This also covers the public POST endpoints (sign-in, first-run setup,
// accepting an invite), which would otherwise be open to cross-site forms.
func (s *Server) apiGuard(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cache-Control", "no-store")

		var p *accounts.Principal
		if token, ok := bearerKey(r); ok {
			var err error
			p, err = s.Accounts.AuthenticateAPIKey(r.Context(), token)
			switch {
			case errors.Is(err, accounts.ErrUnauthenticated):
				writeError(w, http.StatusUnauthorized, "invalid_api_key", "That API key is not valid or has been revoked.")
				return
			case err != nil:
				s.internalError(w, r, err)
				return
			}
		} else if c, err := r.Cookie(SessionCookie); err == nil && c.Value != "" {
			p, err = s.Accounts.AuthenticateSession(r.Context(), c.Value)
			switch {
			case errors.Is(err, accounts.ErrUnauthenticated):
				clearSessionCookie(w, r) // stale cookie: tidy it up
				p = nil
			case err != nil:
				s.internalError(w, r, err)
				return
			case p.Refreshed:
				setSessionCookie(w, r, c.Value, p.ExpiresAt)
			}
		}

		viaKey := p != nil && p.Via == accounts.ViaAPIKey
		if !viaKey && !safeMethod(r.Method) && r.Header.Get(CSRFHeader) != CSRFValue {
			writeError(w, http.StatusForbidden, "csrf", "Missing X-Marquee header.")
			return
		}
		if p != nil {
			r = r.WithContext(context.WithValue(r.Context(), principalKey, p))
		}
		next.ServeHTTP(w, r)
	})
}

func safeMethod(m string) bool {
	return m == http.MethodGet || m == http.MethodHead || m == http.MethodOptions
}

// bearerKey extracts "Authorization: Bearer mq_..." and ignores any other
// Authorization header (a reverse proxy may use it for its own login).
func bearerKey(r *http.Request) (string, bool) {
	h := r.Header.Get("Authorization")
	if len(h) < 8 || !strings.EqualFold(h[:7], "bearer ") {
		return "", false
	}
	token := strings.TrimSpace(h[7:])
	return token, accounts.IsAPIKey(token)
}

// guard describes what a route requires.
type guard struct {
	roles       []accounts.Role // any of these; empty = any signed-in role
	scope       string          // required scope; sessions hold every scope their role has
	sessionOnly bool            // refuse API keys
}

type handlerFunc func(w http.ResponseWriter, r *http.Request, p *accounts.Principal)

func (s *Server) guarded(g guard, h handlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		p := principalFrom(r.Context())
		switch {
		case p == nil:
			writeError(w, http.StatusUnauthorized, "unauthorized", "Sign in first.")
		case g.sessionOnly && p.Via != accounts.ViaSession:
			writeError(w, http.StatusForbidden, "session_required", "Sign in from the browser to do that; API keys cannot.")
		case len(g.roles) > 0 && !slices.Contains(g.roles, p.User.Role):
			writeError(w, http.StatusForbidden, "forbidden", "You are not allowed to do that.")
		case g.scope != "" && !p.HasScope(g.scope):
			writeError(w, http.StatusForbidden, "insufficient_scope", "This key does not have the \""+g.scope+"\" permission.")
		default:
			h(w, r, p)
		}
	}
}

// ---- session cookie --------------------------------------------------------

func setSessionCookie(w http.ResponseWriter, r *http.Request, token string, expires time.Time) {
	http.SetCookie(w, &http.Cookie{
		Name:     SessionCookie,
		Value:    token,
		Path:     "/",
		Expires:  expires,
		MaxAge:   max(int(time.Until(expires).Seconds()), 1),
		HttpOnly: true,
		SameSite: http.SameSiteLaxMode,
		Secure:   requestIsHTTPS(r),
	})
}

func clearSessionCookie(w http.ResponseWriter, r *http.Request) {
	http.SetCookie(w, &http.Cookie{
		Name:     SessionCookie,
		Value:    "",
		Path:     "/",
		MaxAge:   -1,
		HttpOnly: true,
		SameSite: http.SameSiteLaxMode,
		Secure:   requestIsHTTPS(r),
	})
}
