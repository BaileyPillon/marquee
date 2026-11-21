package main

import (
	"context"
	"database/sql"
	"log/slog"

	"github.com/BaileyPillon/marquee/internal/accounts"
	"github.com/BaileyPillon/marquee/internal/downloads"
	"github.com/BaileyPillon/marquee/internal/mcp"
	"github.com/BaileyPillon/marquee/internal/metadata"
	"github.com/BaileyPillon/marquee/internal/requests"
	"github.com/BaileyPillon/marquee/internal/settings"
	"github.com/BaileyPillon/marquee/internal/sources"
	"github.com/BaileyPillon/marquee/internal/titles"
)

// mcpService is the MCP server as the web package asks for it (web.MCPService):
// an http.Handler, plus the description the settings screen shows.
type mcpService struct{ *mcp.Server }

// MCPInfo is {enabled, url, tools, recentCalls} for the person asking.
func (m mcpService) MCPInfo(ctx context.Context, user accounts.User, origin string) any {
	return m.Server.Info(ctx, user, origin)
}

// newMCP builds the MCP server on the services it reads from and asks through.
// It answers at /mcp and /api/v1/mcp, takes Bearer API keys only, and can be
// switched off with the "mcp" settings section ({"enabled": false}).
//
// Claude can ask for a title through requests.Service.Ask, as the key's owner and
// under their limits; there is no way to approve, delete or change settings.
//
// Media servers are not reported yet: when the media-server module exists, give
// it to the health tool with Deps.MediaServers.
func newMCP(mainDB *sql.DB, version string, acc *accounts.Service, cfg *settings.Service, meta *metadata.Service,
	cat *titles.Service, reqs *requests.Service, dl *downloads.Service, src *sources.Service, log *slog.Logger) mcpService {
	return mcpService{mcp.New(mcp.Deps{
		Version:  version,
		Log:      log,
		DB:       mainDB,
		Accounts: acc,
		Settings: cfg,
		Titles:   cat,
		Providers: titles.Providers{
			Movies:  meta.TMDB,
			Shows:   meta.TVmaze,
			Artists: meta.MusicBrainz,
		},
		Requests:  reqs,
		Downloads: dl,
		Sources:   src,
	})}
}
