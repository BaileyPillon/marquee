package metadata

import (
	"context"
	"database/sql"
	"io"
	"log/slog"
	"time"

	"github.com/BaileyPillon/marquee/internal/core/outbound"
)

// Options configure the service. The zero value talks to the real providers.
type Options struct {
	// TMDBKey returns the owner's TMDB credential (a v3 key or a v4 token).
	TMDBKey KeyFunc
	// ArtworkDir is where proxied images are cached, normally
	// <config>/cache/artwork. Required.
	ArtworkDir      string
	ArtworkMaxBytes int64 // default 500 MB

	// Base URLs, replaced in tests with httptest servers.
	TMDBURL        string
	TVmazeURL      string
	MusicBrainzURL string
	// ArtworkBases replaces the image hosts, keyed "tmdb", "tvmaze" and "caa".
	ArtworkBases map[string]string

	Logger *slog.Logger
	Now    func() time.Time
}

// Service holds the providers, the cache and the artwork proxy.
type Service struct {
	TMDB        *TMDB
	TVmaze      *TVmaze
	MusicBrainz *MusicBrainz
	Artwork     *Artwork

	cache  *Cache
	status *Status
}

// New builds the providers on a shared outbound client and database cache.
func New(d *sql.DB, hc *outbound.Client, o Options) (*Service, error) {
	if o.Logger == nil {
		o.Logger = slog.New(slog.NewTextHandler(io.Discard, nil))
	}
	if o.Now == nil {
		o.Now = time.Now
	}
	pick := func(v, def string) string {
		if v != "" {
			return v
		}
		return def
	}
	status := newStatus(o.Now)
	cache := &Cache{store: &store{db: d}, status: status, log: o.Logger, now: o.Now}

	tmdb, err := newTMDB(hc, cache, pick(o.TMDBURL, DefaultTMDBURL), o.TMDBKey)
	if err != nil {
		return nil, err
	}
	tvmaze, err := newTVmaze(hc, cache, pick(o.TVmazeURL, DefaultTVmazeURL))
	if err != nil {
		return nil, err
	}
	mb, err := newMusicBrainz(hc, cache, pick(o.MusicBrainzURL, DefaultMusicBrainzURL))
	if err != nil {
		return nil, err
	}
	art, err := newArtwork(hc, status, o.Logger, o.Now, o.ArtworkDir, o.ArtworkMaxBytes, o.ArtworkBases)
	if err != nil {
		return nil, err
	}
	return &Service{TMDB: tmdb, TVmaze: tvmaze, MusicBrainz: mb, Artwork: art, cache: cache, status: status}, nil
}

// StatusReport is what GET /api/v1/metadata/status shows.
type StatusReport struct {
	// HasKey says whether a TMDB credential is set. Without one, movie
	// lookups fail with tmdb_key_missing; shows and music still work.
	HasKey    bool                      `json:"hasKey"`
	Providers map[string]ProviderStatus `json:"providers"`
}

// Status reports whether TMDB is configured and how each provider last fared.
func (s *Service) Status(ctx context.Context) StatusReport {
	return StatusReport{HasKey: s.TMDB.HasKey(ctx), Providers: s.status.Snapshot()}
}

// PruneCache deletes cache rows that expired long ago. It is a scheduled job.
func (s *Service) PruneCache(ctx context.Context) error {
	_, err := s.cache.Prune(ctx)
	return err
}
