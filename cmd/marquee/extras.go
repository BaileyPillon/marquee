package main

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"log/slog"
	"sync"
	"time"

	"github.com/BaileyPillon/marquee/internal/accounts"
	"github.com/BaileyPillon/marquee/internal/alerts"
	"github.com/BaileyPillon/marquee/internal/calendar"
	"github.com/BaileyPillon/marquee/internal/core/events"
	"github.com/BaileyPillon/marquee/internal/core/jobs"
	"github.com/BaileyPillon/marquee/internal/core/outbound"
	"github.com/BaileyPillon/marquee/internal/core/secrets"
	"github.com/BaileyPillon/marquee/internal/downloads"
	"github.com/BaileyPillon/marquee/internal/requests"
	"github.com/BaileyPillon/marquee/internal/settings"
	"github.com/BaileyPillon/marquee/internal/titles"
	"github.com/BaileyPillon/marquee/internal/watchlists"
)

// Job names that are not the module's own.
const jobHealth = "alerts.health"

// healthInterval is how often the health job looks for problems to report.
const healthInterval = 5 * time.Minute

// extras is milestone M7: Plex watchlists, the calendar and alerts, and the
// small wiring the catalog needs for artists that load in the background. The
// modules sit in the same layer as requests, so they reach it (and each other)
// through the adapters in this file.
type extras struct {
	Watchlists *watchlists.Service
	Calendar   *calendar.Service
	Alerts     *alerts.Service

	dl   *downloads.Service
	cat  *titles.Service
	stop context.CancelFunc
	done chan struct{}
	once sync.Once
}

// extrasDeps is what the wiring needs from the rest of the program.
type extrasDeps struct {
	DB       *sql.DB
	Hub      *events.Hub
	HTTP     *outbound.Client
	Box      *secrets.Box
	Settings *settings.Service
	Accounts *accounts.Service
	// Shows finds a show at TVmaze by IMDb id or name (meta.TVmaze); HasTMDBKey
	// says whether the owner has a TMDB key (meta.TMDB.HasKey).
	Shows      watchlists.ShowFinder
	HasTMDBKey func(ctx context.Context) bool
	Catalog    *titles.Service
	Requests   *requests.Service
	Downloads  *downloads.Service
	Log        *slog.Logger
	// PlexURLs and the alert service's endpoints are replaced by tests.
	PlexURLs  watchlists.PlexURLs
	AlertOpts alerts.Options
}

// newExtras builds the three modules, gives the catalog the event hub (the
// artists that load in the background report on it), and starts the alerts
// listening to the hub. Close it on the way out.
func newExtras(d extrasDeps) *extras {
	d.Catalog.SetHub(d.Hub)

	wl := watchlists.NewService(d.DB, d.Settings, d.Box, watchlists.Options{
		Logger:     d.Log,
		Plex:       watchlists.NewPlexClient(d.HTTP, d.PlexURLs),
		Asker:      watchlistAsker{d.Requests},
		Members:    memberMaker{d.Accounts},
		Shows:      d.Shows,
		HasTMDBKey: d.HasTMDBKey,
	})

	o := d.AlertOpts
	o.Logger, o.Hub = d.Log, d.Hub
	o.Requests, o.Downloads = requestReader{d.Requests}, downloadReader{d.Downloads}
	al := alerts.NewService(d.DB, d.Box, d.HTTP, o)

	ctx, cancel := context.WithCancel(context.Background())
	x := &extras{
		Watchlists: wl, Calendar: calendar.NewService(d.DB, calendar.Options{}), Alerts: al,
		dl: d.Downloads, cat: d.Catalog, stop: cancel, done: make(chan struct{}),
	}
	go func() {
		defer close(x.done)
		al.Run(ctx)
	}()
	return x
}

// registerJobs schedules the Plex check (every 15 minutes, when an account is
// linked) and the health check that feeds the alerts.
func (x *extras) registerJobs(sched *jobs.Scheduler) error {
	if err := sched.Every(watchlists.JobSync, watchlists.SyncInterval, x.Watchlists.SyncJob); err != nil {
		return err
	}
	return sched.Every(jobHealth, healthInterval, x.checkHealth)
}

// Close stops the alerts' listener and waits for the messages in flight, and
// the artists that are still loading their albums.
func (x *extras) Close() {
	x.once.Do(func() {
		x.stop()
		<-x.done
		x.Alerts.Close()
		x.cat.Close()
	})
}

// checkHealth reports what is wrong with the things Marquee depends on, as the
// alerts' health.problem events: a download client that does not answer.
func (x *extras) checkHealth(ctx context.Context) error {
	clients, err := x.dl.Clients(ctx)
	if err != nil {
		return err
	}
	problems := map[string]string{}
	for _, c := range clients {
		if c.Enabled && c.LastError != "" {
			problems[fmt.Sprintf("client:%d", c.ID)] = fmt.Sprintf("The download client %s is not answering: %s", c.Name, c.LastError)
		}
	}
	x.Alerts.ReportProblems(ctx, problems)
	return nil
}

// ---- adapters ----------------------------------------------------------------

// watchlistAsker makes a watchlist item an ask, through the one entry point the
// requests module has. It acts as the person the item belongs to, with the
// source "watchlist", so the weekly limits and the auto-approve rules apply.
type watchlistAsker struct{ reqs *requests.Service }

var _ watchlists.Asker = watchlistAsker{}

func (a watchlistAsker) Ask(ctx context.Context, in watchlists.AskInput) (watchlists.AskResult, error) {
	res, err := a.reqs.Ask(ctx,
		requests.Actor{UserID: in.UserID, Role: in.Role, Source: requests.SourceWatchlist},
		requests.AskInput{Kind: in.Kind, Provider: in.Provider, ProviderID: in.ProviderID, Quality: in.Quality, SourceRef: in.SourceRef})
	var (
		lim *requests.LimitError
		dup *requests.DuplicateError
	)
	switch {
	case err == nil:
		return watchlists.AskResult{RequestID: res.Request.ID, AutoApproved: res.AutoApproved}, nil
	case errors.As(err, &lim):
		return watchlists.AskResult{}, fmt.Errorf("%w: %v", watchlists.ErrLimitReached, err)
	case errors.As(err, &dup):
		return watchlists.AskResult{}, watchlists.ErrAlreadyAsked
	case errors.Is(err, requests.ErrNoFolder):
		return watchlists.AskResult{}, watchlists.ErrNoFolder
	}
	return watchlists.AskResult{}, err
}

// memberMaker makes a Marquee member for a Plex friend.
type memberMaker struct{ acc *accounts.Service }

var _ watchlists.Members = memberMaker{}

func (m memberMaker) CreateMember(ctx context.Context, name string) (int64, error) {
	u, err := m.acc.CreateMember(ctx, name)
	if errors.Is(err, accounts.ErrNameTaken) {
		return 0, watchlists.ErrNameTaken
	}
	return u.ID, err
}

// requestReader tells the alerts about an ask (as the owner would see it).
type requestReader struct{ reqs *requests.Service }

var _ alerts.RequestReader = requestReader{}

func (r requestReader) Request(ctx context.Context, id int64) (alerts.RequestInfo, error) {
	it, err := r.reqs.Get(ctx, requests.Actor{UserID: 1, Role: "owner"}, id)
	if err != nil {
		return alerts.RequestInfo{}, err
	}
	info := alerts.RequestInfo{
		ID: it.ID, TitleName: it.Title.Name, Year: it.Title.Year, Kind: it.Title.Kind,
		Quality: it.Quality, State: it.State, DeclineReason: it.DeclineReason,
	}
	if it.User != nil {
		info.UserName = it.User.Name
	}
	if it.DecidedBy != nil {
		info.DecidedByName = it.DecidedBy.Name
	}
	return info, nil
}

// downloadReader tells the alerts about a download.
type downloadReader struct{ dl *downloads.Service }

var _ alerts.DownloadReader = downloadReader{}

func (r downloadReader) Download(ctx context.Context, id int64) (alerts.DownloadInfo, error) {
	d, err := r.dl.Download(ctx, id)
	if err != nil {
		return alerts.DownloadInfo{}, err
	}
	return alerts.DownloadInfo{ID: d.ID, TitleID: d.TitleID, ReleaseTitle: d.ReleaseTitle, State: d.State, Error: d.Error}, nil
}
