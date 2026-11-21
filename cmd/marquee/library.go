package main

import (
	"context"
	"database/sql"
	"log/slog"

	"github.com/BaileyPillon/marquee/internal/core/events"
	"github.com/BaileyPillon/marquee/internal/core/jobs"
	"github.com/BaileyPillon/marquee/internal/library"
	"github.com/BaileyPillon/marquee/internal/titles"
)

// catalogLister is the part of the titles service the library matches against.
type catalogLister interface {
	Candidates(ctx context.Context, kind string) ([]titles.Candidate, error)
	EpisodeID(ctx context.Context, titleID int64, season, number int) (int64, bool, error)
}

// titleSource lets the library match files against the catalog. The two modules
// each define a Candidate (the library does not import titles), so this only
// converts between them.
type titleSource struct{ cat catalogLister }

var _ library.TitleSource = titleSource{}

func (t titleSource) Candidates(ctx context.Context, kind string) ([]library.Candidate, error) {
	list, err := t.cat.Candidates(ctx, kind)
	if err != nil {
		return nil, err
	}
	out := make([]library.Candidate, len(list))
	for i, c := range list {
		out[i] = library.Candidate{ID: c.ID, Kind: c.Kind, Name: c.Name, Year: c.Year, AltNames: c.AltNames}
	}
	return out, nil
}

func (t titleSource) EpisodeID(ctx context.Context, titleID int64, season, number int) (int64, bool, error) {
	return t.cat.EpisodeID(ctx, titleID, season, number)
}

// newLibrary builds the library on the main database and lets it match files
// against the catalog. Its scans publish progress on the event hub.
func newLibrary(mainDB *sql.DB, hub *events.Hub, cat *titles.Service, log *slog.Logger) *library.Service {
	lib := library.NewService(mainDB, library.Options{Logger: log, Hub: hub})
	lib.SetTitleSource(titleSource{cat: cat})
	return lib
}

// registerLibraryJobs schedules the library's background work.
func registerLibraryJobs(sched *jobs.Scheduler, lib *library.Service) error {
	return sched.Every(library.JobScan, library.ScanInterval, lib.ScanJob)
}
