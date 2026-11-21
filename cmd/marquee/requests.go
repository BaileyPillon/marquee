package main

import (
	"database/sql"
	"log/slog"

	"github.com/BaileyPillon/marquee/internal/core/events"
	"github.com/BaileyPillon/marquee/internal/library"
	"github.com/BaileyPillon/marquee/internal/requests"
	"github.com/BaileyPillon/marquee/internal/settings"
	"github.com/BaileyPillon/marquee/internal/titles"
)

// newRequests builds the requests module on the catalog and the library. It
// reads the owner's defaults from the "requests" settings section, and
// publishes a "requests" event whenever an ask is made or answered.
//
// The result is also the grab.CopySource for the download side. Once the
// searcher exists, wire it in with reqs.SetSearcher(searcher) so an approval
// starts a search; until then approving only makes the copy. Close it on the
// way out.
func newRequests(mainDB *sql.DB, hub *events.Hub, cat *titles.Service, lib *library.Service,
	cfg *settings.Service, log *slog.Logger) *requests.Service {
	return requests.NewService(mainDB, cat, lib, cfg, requests.Options{Logger: log, Hub: hub})
}
