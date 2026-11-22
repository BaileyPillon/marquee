package main

import (
	"database/sql"
	"log/slog"

	"github.com/BaileyPillon/marquee/internal/core/events"
	"github.com/BaileyPillon/marquee/internal/core/jobs"
	"github.com/BaileyPillon/marquee/internal/core/outbound"
	"github.com/BaileyPillon/marquee/internal/core/secrets"
	"github.com/BaileyPillon/marquee/internal/downloads"
	"github.com/BaileyPillon/marquee/internal/grab"
	"github.com/BaileyPillon/marquee/internal/settings"
)

// newDownloads builds the downloads service: the download clients (their
// passwords and keys sealed with the master key), the poll that follows their
// downloads, and the "downloads" events on the hub. cfg is where the poll reads
// the "downloads" settings section (when to give up on a stalled download).
func newDownloads(mainDB *sql.DB, hub *events.Hub, hc *outbound.Client, box *secrets.Box, cfg *settings.Service, log *slog.Logger) *downloads.Service {
	return downloads.NewService(mainDB, downloads.Options{Logger: log, Hub: hub, HTTP: hc, Box: box, Settings: cfg})
}

// downloadGrabber is the grab.Grabber the search service is given through its
// setter once it exists:
//
//	search.SetGrabber(downloadGrabber(dl))
//
// The importer is in the same way given to the downloads service with
// dl.SetCompletedHandler(importer) and dl.SetStalledHandler(importer) (importer.go).
func downloadGrabber(svc *downloads.Service) grab.Grabber { return svc }

// registerDownloadsJobs schedules the poll. The job ticks every few seconds and
// decides for itself whether a poll is due (every 5 s while anything is in
// progress, otherwise once a minute).
func registerDownloadsJobs(sched *jobs.Scheduler, svc *downloads.Service) error {
	return sched.Every(downloads.JobPoll, downloads.PollTick, svc.PollJob)
}
