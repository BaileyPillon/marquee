// Package web is Marquee's HTTP face: the REST API under /api/v1, the SSE
// stream, sign-in and CSRF checks, and the embedded single-page UI.
//
// It sits on top of the other modules. Handlers here call the accounts and
// settings services; no SQL lives in this package.
package web

import (
	"database/sql"
	"io/fs"
	"log/slog"
	"net/http"
	"time"

	"github.com/BaileyPillon/marquee/internal/accounts"
	"github.com/BaileyPillon/marquee/internal/core/events"
	"github.com/BaileyPillon/marquee/internal/settings"
)

// Deps is everything the server needs.
type Deps struct {
	Version  string
	Started  time.Time // process start, for uptime (zero = now)
	Log      *slog.Logger
	MainDB   *sql.DB
	EventsDB *sql.DB
	Accounts *accounts.Service
	Settings *settings.Service
	Hub      *events.Hub
	// UI is the built single-page app, rooted so that index.html is at its top.
	UI fs.FS
}

// Server wires the handlers together.
type Server struct {
	Deps
	log *slog.Logger
}

// New builds a Server.
func New(d Deps) *Server {
	if d.Log == nil {
		d.Log = slog.Default()
	}
	if d.Started.IsZero() {
		d.Started = time.Now()
	}
	return &Server{Deps: d, log: d.Log}
}

// Handler returns the full handler: middleware, API, and UI.
func (s *Server) Handler() http.Handler {
	api := http.NewServeMux()

	// Public.
	api.HandleFunc("GET /api/v1/health", s.handleHealth)
	api.HandleFunc("GET /api/v1/setup", s.handleSetupStatus)
	api.HandleFunc("POST /api/v1/setup/owner", s.handleSetupOwner)
	api.HandleFunc("POST /api/v1/auth/login", s.handleLogin)
	api.HandleFunc("POST /api/v1/invites/accept", s.handleInviteAccept)

	// Signed in.
	api.HandleFunc("POST /api/v1/auth/logout", s.handleLogout)
	api.HandleFunc("GET /api/v1/me", s.guarded(guard{}, s.handleMe))
	api.HandleFunc("GET /api/v1/events", s.guarded(guard{scope: accounts.ScopeSee}, s.handleEvents))

	// People. Keys and invites are managed from a browser session; a key
	// can never mint more keys.
	api.HandleFunc("POST /api/v1/invites", s.guarded(
		guard{roles: []accounts.Role{accounts.RoleOwner, accounts.RoleAdmin}, scope: accounts.ScopeAdmin}, s.handleInviteCreate))
	api.HandleFunc("GET /api/v1/apikeys", s.guarded(guard{sessionOnly: true}, s.handleAPIKeyList))
	api.HandleFunc("POST /api/v1/apikeys", s.guarded(guard{sessionOnly: true}, s.handleAPIKeyCreate))
	api.HandleFunc("DELETE /api/v1/apikeys/{id}", s.guarded(guard{sessionOnly: true}, s.handleAPIKeyRevoke))

	// Owner only.
	owner := guard{roles: []accounts.Role{accounts.RoleOwner}, scope: accounts.ScopeSettings}
	api.HandleFunc("GET /api/v1/settings/{section}", s.guarded(owner, s.handleSettingsGet))
	api.HandleFunc("PUT /api/v1/settings/{section}", s.guarded(owner, s.handleSettingsPut))

	// Anything else under /api/ is a JSON 404, never the UI.
	api.HandleFunc("/api/", func(w http.ResponseWriter, r *http.Request) {
		writeError(w, http.StatusNotFound, "not_found", "No such endpoint.")
	})

	root := http.NewServeMux()
	root.Handle("/api/", s.apiGuard(api))
	root.Handle("/", newStatic(s.UI))

	var h http.Handler = root
	h = securityHeaders(h)
	h = s.recoverer(h)
	h = s.accessLog(h)
	h = requestID(h)
	return h
}

// internalError logs an unexpected failure and tells the client nothing useful
// to an attacker.
func (s *Server) internalError(w http.ResponseWriter, r *http.Request, err error) {
	s.log.Error("request failed", "id", RequestID(r.Context()), "method", r.Method, "path", r.URL.Path, "err", err)
	writeError(w, http.StatusInternalServerError, "internal", "Something went wrong.")
}

func (s *Server) handleEvents(w http.ResponseWriter, r *http.Request, _ *accounts.Principal) {
	s.Hub.ServeHTTP(w, r)
}
