package titles

import (
	"context"
	"time"
)

// EnsureTracks makes sure the listed albums of an artist have their track
// lists, for a caller that needs them now (the importer names a downloaded
// track by the catalog's). With no album ids it looks at every album.
//
// An artist whose albums are still being loaded in the background is waited for.
// An album with no tracks after that is read again from the provider (skipping
// the cache) once; one the provider has no tracks for stays empty, and the
// caller works from the file names. The artist is returned as it is afterwards.
func (s *Service) EnsureTracks(ctx context.Context, id int64, albumIDs ...int64) (*Detail, error) {
	if err := s.waitHydrated(ctx, id); err != nil {
		return nil, err
	}
	d, err := s.Get(ctx, id)
	if err != nil {
		return nil, err
	}
	if d.Kind != KindArtist || !lacksTracks(d, albumIDs) {
		return d, nil
	}
	return s.Refresh(ctx, id)
}

// lacksTracks reports whether one of the albums (all of them with no ids) has
// no tracks.
func lacksTracks(d *Detail, ids []int64) bool {
	want := map[int64]bool{}
	for _, id := range ids {
		want[id] = true
	}
	for _, a := range d.Albums {
		if (len(want) == 0 || want[a.ID]) && len(a.Tracks) == 0 {
			return true
		}
	}
	return false
}

// waitHydrated blocks while the artist's albums are being loaded.
func (s *Service) waitHydrated(ctx context.Context, id int64) error {
	for s.Hydrating(id) {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(250 * time.Millisecond):
		}
	}
	return nil
}
