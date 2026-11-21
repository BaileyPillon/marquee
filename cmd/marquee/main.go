// Command marquee is the whole program: one binary with the web interface
// embedded.
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"io/fs"
	"log/slog"
	"net"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"github.com/BaileyPillon/marquee/internal/accounts"
	"github.com/BaileyPillon/marquee/internal/core/db"
	"github.com/BaileyPillon/marquee/internal/core/events"
	"github.com/BaileyPillon/marquee/internal/core/jobs"
	"github.com/BaileyPillon/marquee/internal/core/outbound"
	"github.com/BaileyPillon/marquee/internal/core/secrets"
	"github.com/BaileyPillon/marquee/internal/settings"
	"github.com/BaileyPillon/marquee/internal/web"
	ui "github.com/BaileyPillon/marquee/web"
)

// version is set at link time: -ldflags "-X main.version=..."
var version = "dev"

const (
	defaultAddr   = ":6464"
	defaultConfig = "./config"
)

func main() {
	if err := run(os.Args[1:], os.Stderr); err != nil {
		fmt.Fprintln(os.Stderr, "marquee:", err)
		os.Exit(1)
	}
}

func envOr(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}

func run(args []string, stderr io.Writer) error {
	flags := flag.NewFlagSet("marquee", flag.ContinueOnError)
	flags.SetOutput(stderr)
	addr := flags.String("addr", envOr("MARQUEE_ADDR", defaultAddr), "listen address (env MARQUEE_ADDR)")
	configDir := flags.String("config", envOr("MARQUEE_CONFIG", defaultConfig), "config directory (env MARQUEE_CONFIG)")
	showVersion := flags.Bool("version", false, "print the version and exit")
	if err := flags.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return nil
		}
		return err
	}
	if *showVersion {
		fmt.Fprintln(stderr, "marquee", version)
		return nil
	}

	log := newLogger(stderr, envOr("MARQUEE_LOG_LEVEL", "info"))
	slog.SetDefault(log)

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	return serve(ctx, log, *addr, *configDir)
}

func newLogger(w io.Writer, level string) *slog.Logger {
	var l slog.Level
	if err := l.UnmarshalText([]byte(strings.ToUpper(level))); err != nil {
		l = slog.LevelInfo
	}
	return slog.New(slog.NewTextHandler(w, &slog.HandlerOptions{Level: l}))
}

func serve(ctx context.Context, log *slog.Logger, addr, configDir string) error {
	absConfig, err := filepath.Abs(configDir)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(absConfig, 0o700); err != nil {
		return fmt.Errorf("config dir: %w", err)
	}

	// Secrets first: nothing else can store a key without the master key.
	box, err := secrets.LoadOrCreate(secrets.KeyPath(absConfig), log)
	if err != nil {
		return err
	}

	mainDB, err := db.Open(filepath.Join(absConfig, db.MainFile))
	if err != nil {
		return err
	}
	defer mainDB.Close()
	eventsDB, err := db.Open(filepath.Join(absConfig, db.EventsFile))
	if err != nil {
		return err
	}
	defer eventsDB.Close()
	if _, err := db.Migrate(ctx, mainDB, db.SetMain); err != nil {
		return err
	}
	if _, err := db.Migrate(ctx, eventsDB, db.SetEvents); err != nil {
		return err
	}

	// Services, bottom layer up.
	hc := outbound.New(version, outbound.Options{Logger: log})
	hub := events.NewHub(log)
	acc := accounts.NewService(mainDB, accounts.Options{Logger: log})
	cfg := settings.New(mainDB, box)
	meta, catalog, err := newCatalog(mainDB, hc, cfg, absConfig, log)
	if err != nil {
		return err
	}

	lib := newLibrary(mainDB, hub, catalog, log)
	defer lib.Close()

	// Requests. A later step calls reqs.SetSearcher once there is a searcher.
	reqs := newRequests(mainDB, hub, catalog, lib, cfg, log)
	defer reqs.Close()

	// Download clients and download tracking. The search step will be given
	// downloadGrabber(dl); the importer will be set with dl.SetCompletedHandler.
	dl := newDownloads(mainDB, hub, hc, box, log)

	// The history of each title (importer.go): asks, approvals and declines come
	// from requests; "grabbed" is written by the grabber wrapper below.
	hist := newHistory(mainDB, reqs, log)

	// Sources, quality profiles and release search (sources.go). The search is
	// what an approved ask starts, and it sends its pick to the download client.
	rf := newReleaseFinding(mainDB, hc, box, catalog, lib, reqs, recordedGrabber(downloadGrabber(dl), hist), log)
	reqs.SetSearcher(rf.Search)

	// The importer and the media servers (importer.go): a finished download goes
	// into the library, and Jellyfin, Emby and Plex are told.
	imp := newImportSide(mainDB, hub, hc, box, dl, reqs, lib, catalog, rf, hist, log)
	defer imp.Importer.Close()

	// Plex watchlists, the calendar and alerts (extras.go).
	ex := newExtras(extrasDeps{DB: mainDB, Hub: hub, HTTP: hc, Box: box, Settings: cfg, Accounts: acc,
		Shows: meta.TVmaze, HasTMDBKey: meta.TMDB.HasKey, Catalog: catalog, Requests: reqs, Downloads: dl, Log: log})
	defer ex.Close()

	sched := jobs.New(log)
	if err := sched.Every("accounts-cleanup", time.Hour, acc.Cleanup); err != nil {
		return err
	}
	if err := registerCatalogJobs(sched, meta, catalog); err != nil {
		return err
	}
	if err := registerLibraryJobs(sched, lib); err != nil {
		return err
	}
	if err := registerDownloadsJobs(sched, dl); err != nil {
		return err
	}
	if err := registerReleaseJobs(sched, rf); err != nil {
		return err
	}
	if err := registerImportJobs(sched, imp.Importer); err != nil {
		return err
	}
	if err := ex.registerJobs(sched); err != nil {
		return err
	}
	sched.Start()
	defer sched.Stop()

	dist, err := fs.Sub(ui.Dist, "dist")
	if err != nil {
		return err
	}
	// Claude's way in (mcp.go): /mcp, with per-key permissions; it can ask, never approve.
	mcpSvc := newMCP(mainDB, version, acc, cfg, meta, catalog, reqs, dl, rf.Sources, log)
	srv := web.New(web.Deps{
		Version:      version,
		Log:          log,
		MainDB:       mainDB,
		EventsDB:     eventsDB,
		Accounts:     acc,
		Settings:     cfg,
		Hub:          hub,
		Metadata:     meta,
		Titles:       catalog,
		Library:      lib,
		Downloads:    dl,
		Requests:     reqs,
		Sources:      rf.Sources,
		Decide:       rf.Decide,
		Search:       rf.Search,
		Importer:     imp.Importer,
		MediaServers: imp.Servers,
		History:      hist,
		Watchlists:   ex.Watchlists,
		Calendar:     ex.Calendar,
		Alerts:       ex.Alerts,
		MCP:          mcpSvc,
		UI:           dist,
	})

	ln, err := net.Listen("tcp", addr)
	if err != nil {
		return err
	}
	httpSrv := &http.Server{
		Handler:           srv.Handler(),
		ReadHeaderTimeout: 10 * time.Second,
		IdleTimeout:       2 * time.Minute,
		// No WriteTimeout: the SSE stream stays open for hours.
		ErrorLog: slog.NewLogLogger(log.Handler(), slog.LevelWarn),
	}
	// Open event streams would hold a graceful shutdown for ever.
	httpSrv.RegisterOnShutdown(hub.Close)

	log.Info("marquee started", "version", version, "addr", ln.Addr().String(), "config", absConfig)

	errc := make(chan error, 1)
	go func() { errc <- httpSrv.Serve(ln) }()

	select {
	case err := <-errc:
		if !errors.Is(err, http.ErrServerClosed) {
			return err
		}
		return nil
	case <-ctx.Done():
	}

	log.Info("shutting down")
	shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := httpSrv.Shutdown(shutdownCtx); err != nil {
		_ = httpSrv.Close()
		return fmt.Errorf("shutdown: %w", err)
	}
	return nil
}
