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
