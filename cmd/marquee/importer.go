package main

import (
	"context"
	"database/sql"
	"log/slog"

	"github.com/BaileyPillon/marquee/internal/core/events"
	"github.com/BaileyPillon/marquee/internal/core/jobs"
	"github.com/BaileyPillon/marquee/internal/core/outbound"
	"github.com/BaileyPillon/marquee/internal/core/secrets"
	"github.com/BaileyPillon/marquee/internal/downloads"
	"github.com/BaileyPillon/marquee/internal/grab"
	"github.com/BaileyPillon/marquee/internal/history"
	"github.com/BaileyPillon/marquee/internal/importer"
	"github.com/BaileyPillon/marquee/internal/library"
	"github.com/BaileyPillon/marquee/internal/mediaservers"
	"github.com/BaileyPillon/marquee/internal/requests"
	"github.com/BaileyPillon/marquee/internal/search"
	"github.com/BaileyPillon/marquee/internal/titles"
)

// These lines turn a change to a signature the importer relies on into a
// compile error here.
var (
	_ importer.Searcher  = (*search.Service)(nil)
	_ importer.Requests  = (*requests.Service)(nil)
	_ importer.Downloads = (*downloads.Service)(nil)
	_ importer.Library   = (*library.Service)(nil)
	_ importer.Servers   = (*mediaservers.Service)(nil)
	_ importer.Catalog   = (*titles.Service)(nil)
)

// newHistory builds the history (the story of each title, in the main database)
// and hands it to the requests module, which writes "requested", "approved" and
// "declined" to it. The grab side is wired with recordedGrabber.
func newHistory(mainDB *sql.DB, reqs *requests.Service, log *slog.Logger) *history.Service {
	h := history.New(mainDB, history.Options{Logger: log})
	reqs.SetRecorder(h)
	return h
}

// recordedGrabber writes a "grabbed" entry to a title's history each time a
// release is sent to a download client. It sits around the downloads module's
// Grabber, so neither the search nor the downloads code knows about the history.
func recordedGrabber(g grab.Grabber, h history.Recorder) grab.Grabber {
	return grabRecorder{Grabber: g, h: h}
}

type grabRecorder struct {
	grab.Grabber
	h history.Recorder
}

func (g grabRecorder) Grab(ctx context.Context, t grab.Target, r grab.Release) (int64, error) {
	id, err := g.Grabber.Grab(ctx, t, r)
	if err != nil || g.h == nil {
		return id, err
	}
	detail := map[string]any{
		"release": r.Title, "protocol": r.Protocol, "size": r.Size, "quality": r.Quality, "indexerId": r.IndexerID,
	}
	if len(r.Reasons) > 0 {
		detail["why"] = r.Reasons[0]
	}
	if len(t.EpisodeIDs) > 0 {
		detail["episodeIds"] = t.EpisodeIDs
	}
	_ = g.h.Record(context.WithoutCancel(ctx), history.Entry{
		TitleID: t.TitleID, CopyID: t.CopyID, DownloadID: id, Event: history.Grabbed, Detail: detail,
	})
	return id, err
}

// importSide is the import module and the media servers it tells.
type importSide struct {
	Servers  *mediaservers.Service
	Importer *importer.Service
}

// newImportSide builds the media servers and the importer and joins the
// importer to the downloads module (it is told when a download finishes) and to
// everything it works with.
func newImportSide(mainDB *sql.DB, hub *events.Hub, hc *outbound.Client, box *secrets.Box, dl *downloads.Service,
	reqs *requests.Service, lib *library.Service, cat *titles.Service, rf *releaseFinding, hist *history.Service,
	log *slog.Logger) *importSide {
	servers := mediaservers.New(mainDB, mediaservers.Options{Logger: log, HTTP: hc, Box: box})
	imp := importer.New(mainDB, importer.Deps{
		Downloads: dl,
		Copies:    reqs,
		Titles:    cat,
		Library:   lib,
		Profiles:  rf.Decide,
		Blocklist: rf.Decide,
		Requests:  reqs,
		Servers:   servers,
		Search:    rf.Search,
		History:   hist,
	}, importer.Options{Logger: log, Hub: hub})
	dl.SetCompletedHandler(imp)
	return &importSide{Servers: servers, Importer: imp}
}

// registerImportJobs schedules the pick-up of finished downloads nobody has
// imported, and starts one pass soon after startup: the downloads module does
// not tell the importer about downloads that finished before it was wired in.
func registerImportJobs(sched *jobs.Scheduler, imp *importer.Service) error {
	if err := sched.Every(importer.JobPending, importer.PendingInterval, imp.Pending); err != nil {
		return err
	}
	imp.StartupPass()
	return nil
}
