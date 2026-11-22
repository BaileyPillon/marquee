package subtitles

import (
	"context"
	"regexp"
	"strings"
)

// Config is the "subtitles" settings section. Fields named apiKey and password
// are sealed by the settings service and shown masked.
type Config struct {
	// Languages are the codes the owner wants, in order ("en", "pt-br").
	Languages []string `json:"languages"`
	Providers struct {
		OpenSubtitles OpenSubtitlesConfig `json:"opensubtitles"`
		Podnapisi     PodnapisiConfig     `json:"podnapisi"`
	} `json:"providers"`
	// HearingImpaired is "prefer-not", "prefer" or "only".
	HearingImpaired string `json:"hearingImpaired"`
	// MinScore is the lowest score (0 to 100) an automatic download accepts.
	MinScore int `json:"minScore"`
	// SearchAfterImport looks for subtitles as soon as the importer adds a file.
	SearchAfterImport bool `json:"searchAfterImport"`
	// RescanEveryHours is how often the scheduled job looks for files that still
	// miss a language.
	RescanEveryHours int `json:"rescanEveryHours"`
}

// OpenSubtitlesConfig is the owner's OpenSubtitles.com account: the API key is
// needed; the username and password are for downloads (a bigger daily quota).
type OpenSubtitlesConfig struct {
	Enabled  bool   `json:"enabled"`
	APIKey   string `json:"apiKey"`
	Username string `json:"username"`
	Password string `json:"password"`
}

// PodnapisiConfig turns Podnapisi on or off; it needs nothing else.
type PodnapisiConfig struct {
	Enabled bool `json:"enabled"`
}

// DefaultConfig is what the settings hold until the owner changes them.
func DefaultConfig() Config {
	var c Config
	c.Languages = []string{"en"}
	c.Providers.Podnapisi.Enabled = true
	c.HearingImpaired = HIPreferNot
	c.MinScore = 60
	c.SearchAfterImport = true
	c.RescanEveryHours = 24
	return c
}

var reLanguage = regexp.MustCompile(`^[a-z]{2,3}(?:-[a-z0-9]{2,4})?$`)

// normalize turns whatever the settings hold into a Config the module can use:
// languages lower-cased, tidy and few; the other fields in range.
func (c Config) normalize() Config {
	seen := map[string]bool{}
	langs := make([]string, 0, len(c.Languages))
	for _, l := range c.Languages {
		l = normalizeLanguage(l)
		if !reLanguage.MatchString(l) || seen[l] {
			continue
		}
		seen[l] = true
		langs = append(langs, l)
		if len(langs) == maxLanguages {
			break
		}
	}
	c.Languages = langs
	switch c.HearingImpaired {
	case HIPreferNot, HIPrefer, HIOnly:
	default:
		c.HearingImpaired = HIPreferNot
	}
	c.MinScore = min(max(c.MinScore, 0), 100)
	if c.RescanEveryHours <= 0 {
		c.RescanEveryHours = 24
	}
	c.RescanEveryHours = min(c.RescanEveryHours, 24*30)
	c.Providers.OpenSubtitles.APIKey = strings.TrimSpace(c.Providers.OpenSubtitles.APIKey)
	c.Providers.OpenSubtitles.Username = strings.TrimSpace(c.Providers.OpenSubtitles.Username)
	return c
}

// Config reads the settings, filling in what the owner has not set.
func (s *Service) Config(ctx context.Context) Config {
	c := DefaultConfig()
	if s.settings != nil {
		if _, err := s.settings.Get(ctx, Section, &c); err != nil {
			s.log.Warn("subtitles: the settings cannot be read; using the defaults", "err", err)
			c = DefaultConfig()
		}
	}
	return c.normalize()
}

// openSubtitlesReady reports whether OpenSubtitles is turned on and has a key.
func (c Config) openSubtitlesReady() bool {
	return c.Providers.OpenSubtitles.Enabled && c.Providers.OpenSubtitles.APIKey != ""
}
