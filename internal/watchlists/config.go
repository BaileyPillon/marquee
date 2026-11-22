package watchlists

import (
	"context"

	"github.com/BaileyPillon/marquee/internal/grab"
	"github.com/BaileyPillon/marquee/internal/settings"
)

// PlexConfig is the "plex" part of the "watchlists" settings section:
//
//	{"plex": {"enabled": true, "includeFriends": true, "autoCreateMembers": false, "quality": "hd"}}
type PlexConfig struct {
	// Enabled turns the check on or off without unlinking the account.
	Enabled bool `json:"enabled"`
	// IncludeFriends reads the owner's friends' watchlists too.
	IncludeFriends bool `json:"includeFriends"`
	// AutoCreateMembers makes a Marquee member for a Plex friend who is not
	// linked to anybody yet.
	AutoCreateMembers bool `json:"autoCreateMembers"`
	// Quality is the quality asked for: "hd" or "uhd".
	Quality string `json:"quality"`
}

// Config is the whole "watchlists" settings section.
type Config struct {
	Plex PlexConfig `json:"plex"`
}

// DefaultConfig is what a section that was never saved means.
func DefaultConfig() Config {
	return Config{Plex: PlexConfig{Enabled: true, IncludeFriends: true, AutoCreateMembers: false, Quality: grab.QualityHD}}
}

// PlexPatch is a change to the Plex settings: a field left nil keeps its value.
type PlexPatch struct {
	Enabled           *bool
	IncludeFriends    *bool
	AutoCreateMembers *bool
	Quality           *string
}

// InvalidError is a settings change the owner can fix; Message says how.
type InvalidError struct{ Message string }

func (e *InvalidError) Error() string { return "watchlists: " + e.Message }

// SetPlexConfig changes the Plex settings and returns the status as it is then.
// The quality must be "hd" or "uhd".
func (s *Service) SetPlexConfig(ctx context.Context, p PlexPatch) (Status, error) {
	if p.Quality != nil && *p.Quality != grab.QualityHD && *p.Quality != grab.QualityUHD {
		return Status{}, &InvalidError{Message: `The quality must be "hd" or "uhd".`}
	}
	cfg, err := LoadConfig(ctx, s.cfg)
	if err != nil {
		// A section that does not read is replaced: the owner is saving a new one.
		s.log.Warn("the watchlists settings cannot be read; saving over them", "err", err)
		cfg = DefaultConfig()
	}
	if p.Enabled != nil {
		cfg.Plex.Enabled = *p.Enabled
	}
	if p.IncludeFriends != nil {
		cfg.Plex.IncludeFriends = *p.IncludeFriends
	}
	if p.AutoCreateMembers != nil {
		cfg.Plex.AutoCreateMembers = *p.AutoCreateMembers
	}
	if p.Quality != nil {
		cfg.Plex.Quality = *p.Quality
	}
	if err := s.cfg.Set(ctx, SettingsSection, cfg); err != nil {
		return Status{}, err
	}
	return s.Status(ctx)
}

// LoadConfig reads the section over the defaults: a field that is not there
// keeps its default. A quality that is not hd or uhd is hd.
func LoadConfig(ctx context.Context, s *settings.Service) (Config, error) {
	c := DefaultConfig()
	if s == nil {
		return c, nil
	}
	if _, err := s.Get(ctx, SettingsSection, &c); err != nil {
		return DefaultConfig(), err
	}
	if c.Plex.Quality != grab.QualityHD && c.Plex.Quality != grab.QualityUHD {
		c.Plex.Quality = grab.QualityHD
	}
	return c, nil
}
