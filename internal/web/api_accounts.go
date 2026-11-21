package web

import (
	"errors"
	"net/http"
	"strconv"
	"time"

	"github.com/BaileyPillon/marquee/internal/accounts"
)

// ---- first run -------------------------------------------------------------

func (s *Server) handleSetupStatus(w http.ResponseWriter, r *http.Request) {
	need, err := s.Accounts.NeedsOwner(r.Context())
	if err != nil {
		s.internalError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]bool{"needsOwner": need})
}

type credentials struct {
	Name     string `json:"name"`
	Password string `json:"password"`
}

func (s *Server) handleSetupOwner(w http.ResponseWriter, r *http.Request) {
	var in credentials
	if !decodeJSON(w, r, &in) {
		return
	}
	login, err := s.Accounts.SetupOwner(r.Context(), in.Name, in.Password, clientInfo(r))
	if err != nil {
		s.accountsError(w, r, err)
		return
	}
	setSessionCookie(w, r, login.Token, login.ExpiresAt)
	writeJSON(w, http.StatusCreated, map[string]any{"user": login.User})
}

// ---- sign in / out ---------------------------------------------------------

func (s *Server) handleLogin(w http.ResponseWriter, r *http.Request) {
	var in credentials
	if !decodeJSON(w, r, &in) {
		return
	}
	login, err := s.Accounts.SignIn(r.Context(), in.Name, in.Password, clientInfo(r))
	if err != nil {
		s.accountsError(w, r, err)
		return
	}
	setSessionCookie(w, r, login.Token, login.ExpiresAt)
	writeJSON(w, http.StatusOK, map[string]any{"user": login.User})
}

func (s *Server) handleLogout(w http.ResponseWriter, r *http.Request) {
	if c, err := r.Cookie(SessionCookie); err == nil && c.Value != "" {
		if err := s.Accounts.SignOut(r.Context(), c.Value); err != nil {
			s.internalError(w, r, err)
			return
		}
	}
	clearSessionCookie(w, r)
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) handleMe(w http.ResponseWriter, r *http.Request, p *accounts.Principal) {
	writeJSON(w, http.StatusOK, map[string]any{
		"user":   p.User,
		"via":    p.Via,
		"scopes": p.Scopes,
	})
}

// ---- invites ---------------------------------------------------------------

func (s *Server) handleInviteCreate(w http.ResponseWriter, r *http.Request, p *accounts.Principal) {
	var in struct {
		Role accounts.Role `json:"role"`
	}
	if !decodeJSON(w, r, &in) {
		return
	}
	token, expires, err := s.Accounts.CreateInvite(r.Context(), p.User, in.Role)
	if err != nil {
		s.accountsError(w, r, err)
		return
	}
	writeJSON(w, http.StatusCreated, map[string]any{
		"token":     token,
		"role":      in.Role,
		"expiresAt": expires,
	})
}

func (s *Server) handleInviteAccept(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Token    string `json:"token"`
		Name     string `json:"name"`
		Password string `json:"password"`
	}
	if !decodeJSON(w, r, &in) {
		return
	}
	login, err := s.Accounts.AcceptInvite(r.Context(), in.Token, in.Name, in.Password, clientInfo(r))
	if err != nil {
		s.accountsError(w, r, err)
		return
	}
	setSessionCookie(w, r, login.Token, login.ExpiresAt)
	writeJSON(w, http.StatusCreated, map[string]any{"user": login.User})
}

// ---- API keys --------------------------------------------------------------

func (s *Server) handleAPIKeyList(w http.ResponseWriter, r *http.Request, p *accounts.Principal) {
	keys, err := s.Accounts.ListAPIKeys(r.Context(), p.User)
	if err != nil {
		s.internalError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"keys":   keys,
		"scopes": accounts.ScopesForRole(p.User.Role), // what this person may tick
	})
}

func (s *Server) handleAPIKeyCreate(w http.ResponseWriter, r *http.Request, p *accounts.Principal) {
	var in struct {
		Name   string   `json:"name"`
		Scopes []string `json:"scopes"`
	}
	if !decodeJSON(w, r, &in) {
		return
	}
	key, secret, err := s.Accounts.CreateAPIKey(r.Context(), p.User, in.Name, in.Scopes)
	if err != nil {
		s.accountsError(w, r, err)
		return
	}
	writeJSON(w, http.StatusCreated, map[string]any{"key": key, "secret": secret})
}

func (s *Server) handleAPIKeyRevoke(w http.ResponseWriter, r *http.Request, p *accounts.Principal) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		writeError(w, http.StatusNotFound, "not_found", "No such key.")
		return
	}
	if err := s.Accounts.RevokeAPIKey(r.Context(), p.User, id); err != nil {
		s.accountsError(w, r, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// ---- helpers ---------------------------------------------------------------

func clientInfo(r *http.Request) accounts.ClientInfo {
	return accounts.ClientInfo{IP: clientIP(r), UserAgent: r.UserAgent()}
}

// accountsError maps a service error to an HTTP response.
func (s *Server) accountsError(w http.ResponseWriter, r *http.Request, err error) {
	var (
		limited *accounts.RateLimitedError
		scope   *accounts.ScopeError
	)
	switch {
	case errors.As(err, &limited):
		secs := int(limited.RetryAfter.Round(time.Second) / time.Second)
		w.Header().Set("Retry-After", strconv.Itoa(max(secs, 1)))
		writeError(w, http.StatusTooManyRequests, "too_many_attempts", "Too many failed sign-ins. Wait a few minutes and try again.")
	case errors.Is(err, accounts.ErrInvalidCredentials):
		writeError(w, http.StatusUnauthorized, "invalid_credentials", "Wrong name or password.")
	case errors.Is(err, accounts.ErrOwnerExists):
		writeError(w, http.StatusConflict, "owner_exists", "Marquee already has an owner. Sign in instead.")
	case errors.Is(err, accounts.ErrNameTaken):
		writeError(w, http.StatusConflict, "name_taken", "That name is already taken.")
	case errors.Is(err, accounts.ErrInvalidName):
		writeError(w, http.StatusBadRequest, "invalid_name", "Pick a name of 1 to 40 characters.")
	case errors.Is(err, accounts.ErrWeakPassword):
		writeError(w, http.StatusBadRequest, "weak_password", "Use a password of at least 8 characters.")
	case errors.Is(err, accounts.ErrPasswordTooLong):
		writeError(w, http.StatusBadRequest, "password_too_long", "That password is too long.")
	case errors.Is(err, accounts.ErrInvalidInvite):
		writeError(w, http.StatusBadRequest, "invalid_invite", "That invite is not valid, was already used, or has expired.")
	case errors.Is(err, accounts.ErrInvalidRole):
		writeError(w, http.StatusBadRequest, "invalid_role", "Role must be \"admin\" or \"member\".")
	case errors.Is(err, accounts.ErrInvalidKeyName):
		writeError(w, http.StatusBadRequest, "invalid_name", "Give the key a name of 1 to 60 characters.")
	case errors.As(err, &scope) && errors.Is(scope.Err, accounts.ErrScopeNotAllowed):
		writeError(w, http.StatusBadRequest, "scope_not_allowed", "Your role cannot grant the \""+scope.Scope+"\" permission.")
	case errors.As(err, &scope):
		writeError(w, http.StatusBadRequest, "invalid_scope", "Choose at least one known permission.")
	case errors.Is(err, accounts.ErrForbidden):
		writeError(w, http.StatusForbidden, "forbidden", "You are not allowed to do that.")
	case errors.Is(err, accounts.ErrNotFound):
		writeError(w, http.StatusNotFound, "not_found", "Not found.")
	default:
		s.internalError(w, r, err)
	}
}
