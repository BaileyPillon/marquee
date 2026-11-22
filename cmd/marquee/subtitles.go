package main

import (
	"context"
	"database/sql"
	"log/slog"

	"github.com/BaileyPillon/marquee/internal/core/events"
	"github.com/BaileyPillon/marquee/internal/core/jobs"
	"github.com/BaileyPillon/marquee/internal/core/outbound"
	"github.com/BaileyPillon/marquee/internal/library"
	"github.com/BaileyPillon/marquee/internal/mediaservers"
	"github.com/BaileyPillon/marquee/internal/settings"
	"github.com/BaileyPillon/marquee/internal/subtitles"
	"github.com/BaileyPillon/marquee/internal/titles"
)

// These lines turn a change to a signature the subtitles module relies on into a
// compile error here.
var (
	_ subtitles.Library  = (*library.Service)(nil)
	_ subtitles.Catalog  = (*titles.Service)(nil)
	_ subtitles.Settings = (*settings.Service)(nil)
)

// newSubtitles builds the subtitles module and joins it to the rest:
//
//   - the importer tells it which files it just added (importer.SetAfterImport),
//     so subtitles are looked for as soon as a film or an episode arrives;
//   - a subtitle it writes is announced to the media servers like any new file;
//   - the owner's settings ("subtitles" section) are read on every use, so a key
//     saved in Settings works at once.
//
// The two providers are called through the shared outbound client, which this
// also gives a gentler per-host limit than its default: OpenSubtitles allows 40
// requests every 10 seconds, so 4 a second with a burst of 2 stays well under
// it, and Podnapisi is a free service run by a few people.
func newSubtitles(mainDB *sql.DB, hub *events.Hub, hc *outbound.Client, cfg *settings.Service, lib *library.Service,
	cat *titles.Service, imp *importSide, log *slog.Logger) *subtitles.Service {
	hc.SetHostLimit("api.opensubtitles.com", 4, 2)
	hc.SetHostLimit("www.podnapisi.net", 2, 2)

	svc := subtitles.New(mainDB, subtitles.Deps{Library: lib, Catalog: cat, Settings: cfg}, subtitles.Options{
		Logger: log, HTTP: hc, Version: version, Hub: hub,
	})
	imp.Importer.SetAfterImport(svc)
	if imp.Servers != nil {
		svc.SetNotifier(func(ctx context.Context, paths []string) {
			imp.Servers.Notify(ctx, mediaservers.Event{Kind: mediaservers.EventCreated, Paths: paths})
		})
	}
	return svc
}

// registerSubtitlesJobs schedules the pass that looks for subtitles files are
// still missing. The job wakes every hour and runs a pass when the owner's
// "rescan every N hours" setting says it is time.
func registerSubtitlesJobs(sched *jobs.Scheduler, svc *subtitles.Service) error {
	return sched.Every(subtitles.JobMissing, subtitles.JobInterval, svc.MissingJob)
}
