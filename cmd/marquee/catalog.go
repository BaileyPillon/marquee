package main

import (
	"context"
	"database/sql"
	"log/slog"
	"path/filepath"
	"strings"
	"time"

	"github.com/BaileyPillon/marquee/internal/core/jobs"
	"github.com/BaileyPillon/marquee/internal/core/outbound"
	"github.com/BaileyPillon/marquee/internal/metadata"
	"github.com/BaileyPillon/marquee/internal/settings"
	"github.com/BaileyPillon/marquee/internal/titles"
)

// newCatalog builds the metadata providers (with the artwork proxy cache under
// <config>/cache/artwork) and the title catalog on top of them.
func newCatalog(mainDB *sql.DB, hc *outbound.Client, cfg *settings.Service, configDir string, log *slog.Logger) (*metadata.Service, *titles.Service, error) {
	meta, err := metadata.New(mainDB, hc, metadata.Options{
		TMDBKey:    tmdbKey(cfg, log),
		ArtworkDir: filepath.Join(configDir, "cache", "artwork"),
		Logger:     log,
	})
	if err != nil {
		return nil, nil, err
	}
	cat := titles.NewService(mainDB, titles.Providers{
		Movies:  meta.TMDB,
		Shows:   meta.TVmaze,
		Artists: meta.MusicBrainz,
	}, titles.Options{Logger: log})
	return meta, cat, nil
}

// tmdbKey reads the owner's TMDB credential from the "tmdb" settings section,
// field apiKey. It is read on every call, so a key saved in Settings works at
// once. A credential that cannot be decrypted (the master key file was
// replaced) is treated as no credential, so the owner is asked for it again.
func tmdbKey(cfg *settings.Service, log *slog.Logger) metadata.KeyFunc {
	type tmdbSettings struct {
		APIKey string `json:"apiKey"`
	}
	return func(ctx context.Context) (string, error) {
		v, _, err := settings.GetAs[tmdbSettings](ctx, cfg, "tmdb")
		if err != nil {
			log.Warn("the TMDB key in settings cannot be read; treating it as not set", "err", err)
			return "", nil
		}
		return strings.TrimSpace(v.APIKey), nil
	}
}

// registerCatalogJobs schedules the catalog's background work.
func registerCatalogJobs(sched *jobs.Scheduler, meta *metadata.Service, cat *titles.Service) error {
	if err := sched.Every("titles.refresh", 12*time.Hour, cat.RefreshMonitored); err != nil {
		return err
	}
	return sched.Every("metadata.prune", 24*time.Hour, meta.PruneCache)
}
