package web

import (
	"context"
	"net/http"
	"strings"

	"github.com/BaileyPillon/marquee/internal/accounts"
)

// MCPService is the MCP server as the web package sees it: an http.Handler for
// the endpoint, and the description the settings screen shows. The mcp module
// sits at the same layer as this package, so main hands it in through this
// interface instead of web importing it.
type MCPService interface {
	http.Handler
	// MCPInfo is {enabled, url, tools, recentCalls} for a signed-in person; origin
	// is "scheme://host" as they reached Marquee.
	MCPInfo(ctx context.Context, user accounts.User, origin string) any
}

// registerMCP mounts the MCP endpoint and its info route. Without the service
// (a test server that does not need it) it adds nothing.
//
// The endpoint is mounted on the root mux, outside the API guard: that guard
// works out who is calling from a cookie or a key and enforces CSRF, and answers
// in Marquee's own error shape. The MCP server takes Bearer API keys only (a
// browser cannot attach one to a cross-site request), checks the Origin header,
// and answers in JSON-RPC.
func (s *Server) registerMCP(root, api *http.ServeMux) {
	if s.MCP == nil {
		return
	}
	root.Handle("/mcp", s.MCP)
	root.Handle("/api/v1/mcp", s.MCP)
	api.HandleFunc("GET /api/v1/mcp/info", s.guarded(guard{}, s.handleMCPInfo))
}

// GET /api/v1/mcp/info: {enabled, url, tools: [{name, description, scope}],
// recentCalls: [{at, tool, userName, ok}]}. The owner sees everyone's recent
// calls; everyone else sees their own.
func (s *Server) handleMCPInfo(w http.ResponseWriter, r *http.Request, p *accounts.Principal) {
	writeJSON(w, http.StatusOK, s.MCP.MCPInfo(r.Context(), p.User, requestOrigin(r)))
}

// requestOrigin is the scheme and host the caller used, for showing a URL.
func requestOrigin(r *http.Request) string {
	scheme := "http"
	if r.TLS != nil || strings.EqualFold(r.Header.Get("X-Forwarded-Proto"), "https") {
		scheme = "https"
	}
	return scheme + "://" + r.Host
}
