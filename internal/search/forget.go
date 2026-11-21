package search

import (
	"context"
	"fmt"
	"strings"
)

// The keys a grab is remembered by (see needFor). The importer uses them to
// make the search forget a release whose files turned out to be bad, so the
// copy is searched again at once instead of after the day's wait.

// MovieKey is the key of a film.
func MovieKey() string { return movieKey() }

// SeasonKey is the key of a whole-season pack: "s01".
func SeasonKey(season int) string { return seasonKey(season) }

// EpisodeKey is the key of one episode: "s01e04".
func EpisodeKey(season, episode int) string { return episodeKey(season, episode) }

// ForgetGrab makes the search forget that these things were sent for download
// for a copy, so the next search of the copy looks for them again. Keys that
// were never remembered are ignored.
func (s *Service) ForgetGrab(ctx context.Context, copyID int64, keys []string) error {
	clean := map[string]bool{}
	for _, k := range keys {
		if k = strings.TrimSpace(strings.ToLower(k)); k != "" {
			clean[k] = true
		}
	}
	if len(clean) == 0 {
		return nil
	}
	n, err := s.store.ForgetGrabs(ctx, copyID, s.now().Add(-grabMemory), clean)
	if err != nil {
		return fmt.Errorf("search: forgetting a grab: %w", err)
	}
	if n > 0 {
		s.log.Info("search forgot a bad grab", "copy", copyID, "keys", len(clean))
	}
	return nil
}
