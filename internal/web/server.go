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
	"net/netip"
	"sync/atomic"
	"time"

	"github.com/BaileyPillon/marquee/internal/accounts"
	"github.com/BaileyPillon/marquee/internal/alerts"
	"github.com/BaileyPillon/marquee/internal/calendar"
	"github.com/BaileyPillon/marquee/internal/core/events"
	"github.com/BaileyPillon/marquee/internal/decide"
	"github.com/BaileyPillon/marquee/internal/downloads"
	"github.com/BaileyPillon/marquee/internal/history"
	"github.com/BaileyPillon/marquee/internal/importer"
	"github.com/BaileyPillon/marquee/internal/library"
	"github.com/BaileyPillon/marquee/internal/mediaservers"
	"github.com/BaileyPillon/marquee/internal/metadata"
	"github.com/BaileyPillon/marquee/internal/requests"
	"github.com/BaileyPillon/marquee/internal/search"
	"github.com/BaileyPillon/marquee/internal/settings"
	"github.com/BaileyPillon/marquee/internal/sources"
	"github.com/BaileyPillon/marquee/internal/subtitles"
	"github.com/BaileyPillon/marquee/internal/titles"
	"github.com/BaileyPillon/marquee/internal/watchlists"
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
	Metadata *metadata.Service
	Titles   *titles.Service
	Library  *library.Service
	// Downloads is the download-client module (api_downloads.go); nil leaves
	// the download routes out.
	Downloads *downloads.Service
	Requests  *requests.Service
	// Sources, Decide and Search are the release-finding modules
	// (api_sources.go); each one that is nil leaves its routes out.
	Sources *sources.Service
	Decide  *decide.Service
	Search  *search.Service
	// Importer, MediaServers and History are the import side (api_import.go,
	// api_mediaservers.go); each one that is nil leaves its routes out.
	Importer     *importer.Service
	MediaServers *mediaservers.Service
	History      *history.Service
	// Watchlists, Calendar and Alerts (api_watchlists.go, api_calendar.go,
	// api_alerts.go); each one that is nil leaves its routes out.
	Watchlists *watchlists.Service
	Calendar   *calendar.Service
	Alerts     *alerts.Service
	// Subtitles finds and places subtitles (api_subtitles.go); nil leaves its
	// routes out.
	Subtitles *subtitles.Service
	// MCP is the Model Context Protocol server (api_mcp.go); nil leaves its
	// routes out.
	MCP MCPService
	// Jobs says how the background jobs are doing, for GET /api/v1/system/status
	// (api_system.go); nil leaves that part out. ConfigDir is the config folder,
	// shown there with its free space and database sizes.
	Jobs      JobStatuser
	ConfigDir string
	// TrustedProxies are the reverse proxies named in the environment
	// (MARQUEE_TRUSTED_PROXIES), as CIDR ranges or addresses; the owner's "server"
	// setting can add more (see proxy.go). Entries that do not parse are ignored.
	TrustedProxies []string
	// UI is the built single-page app, rooted so that index.html is at its top.
	UI fs.FS
}

// Server wires the handlers together.
type Server struct {
	Deps
	log *slog.Logger

	// rt is the parsed "server" setting, kept a few seconds (serverconfig.go);
	// envProxies are the trusted proxies from the environment.
	rt         atomic.Pointer[runtimeConfig]
	envProxies []netip.Prefix
}

// New builds a Server.
func New(d Deps) *Server {
	if d.Log == nil {
		d.Log = slog.Default()
	}
	if d.Started.IsZero() {
		d.Started = time.Now()
	}
	s := &Server{Deps: d, log: d.Log}
	if prefixes, err := ParseProxyList(d.TrustedProxies); err != nil {
		s.log.Warn("MARQUEE_TRUSTED_PROXIES is not valid; no proxy is trusted from the environment", "err", err)
	} else {
		s.envProxies = prefixes
	}
	return s
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

	// Catalog, search and artwork (api_titles.go).
	s.registerTitles(api)

	// Library folders, scans and files (api_library.go).
	s.registerLibrary(api)

	// Downloads and download clients (api_downloads.go).
	s.registerDownloads(api)

	// Requests, approvals, people and limits (api_requests.go).
	s.registerRequests(api)

	// Sources, quality profiles, the blocklist and release search (api_sources.go).
	s.registerSources(api)

	// Import: a title's history, open-on-server, manual import and its status
	// (api_import.go), and the media servers (api_mediaservers.go).
	s.registerImport(api)
	s.registerMediaServers(api)

	// Plex watchlists, the calendar and alert channels.
	s.registerWatchlists(api)
	s.registerCalendar(api)
	s.registerAlerts(api)

	// Subtitles (api_subtitles.go) and the album search (api_music.go).
	s.registerSubtitles(api)
	s.registerMusic(api)

	// The owner's and admins' view of how Marquee is doing (api_system.go).
	s.registerSystem(api)

	// Anything else under /api/ is a JSON 404, never the UI.
	api.HandleFunc("/api/", func(w http.ResponseWriter, r *http.Request) {
		writeError(w, http.StatusNotFound, "not_found", "No such endpoint.")
	})

	root := http.NewServeMux()
	// The MCP endpoint (api_mcp.go) sits outside the API guard: it takes Bearer
	// keys only and answers in JSON-RPC.
	s.registerMCP(root, api)
	root.Handle("/api/", s.apiGuard(api))
	root.Handle("/", newStatic(s.UI))

	var h http.Handler = root
	h = securityHeaders(h)
	h = s.recoverer(h)
	h = s.accessLog(h)
	h = requestID(h)
	// Outermost, so every layer below sees the visitor's real address and scheme
	// when (and only when) the request came from a trusted proxy (proxy.go).
	h = s.forwardedMiddleware(h)
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
