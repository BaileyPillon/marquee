package importer

import (
	"context"

	"github.com/BaileyPillon/marquee/internal/decide"
	"github.com/BaileyPillon/marquee/internal/grab"
	"github.com/BaileyPillon/marquee/internal/history"
)

// StalledPrefix starts the reason a stalled release is blocklisted with.
const StalledPrefix = "Stalled: "

var _ grab.StalledHandler = (*Service)(nil)

// OnStalled implements grab.StalledHandler: the downloads module gave up on a
// download that made no progress (an Internet Archive torrent stuck at 99.97%) and
// has marked it failed with reason. What is left is what a bad release gets: it is
// blocklisted (by feed id and info-hash) so the search cannot pick it again, the
// search forgets what it grabbed for the copy, the copy is searched again through
// the same debounced, capped path an import failure uses, and the title's history
// says what happened. It never waits for the search.
func (s *Service) OnStalled(ctx context.Context, downloadID int64, reason string, removedFromClient bool) {
	d, err := s.deps.Downloads.Download(ctx, downloadID)
	if err != nil {
		s.log.Warn("stalled download: cannot read it", "download", downloadID, "err", err)
		return
	}

	blocked := false
	if s.deps.Blocklist != nil && d.TitleID > 0 {
		_, err := s.deps.Blocklist.Block(ctx, decide.BlockEntry{
			TitleID: d.TitleID, GUID: d.GUID, InfoHash: blockHash(d), ReleaseTitle: d.ReleaseTitle, Reason: StalledPrefix + reason,
		})
		if err != nil {
			s.log.Warn("stalled download: could not blocklist the release", "download", d.ID, "err", err)
		} else {
			blocked = true
		}
	}

	searching := false
	if s.deps.Search != nil && d.CopyID != 0 {
		if det, err := s.deps.Titles.Get(ctx, d.TitleID); err != nil {
			s.log.Warn("stalled download: could not read the title to make the search forget the grab", "download", d.ID, "err", err)
		} else if keys := grabKeys(det, d); len(keys) > 0 {
			if err := s.deps.Search.ForgetGrab(ctx, d.CopyID, keys); err != nil {
				s.log.Warn("stalled download: could not make the search forget the release", "download", d.ID, "err", err)
			}
		}
		searching = s.scheduleSearch(d.CopyID)
	}

	s.note(ctx, history.Entry{TitleID: d.TitleID, CopyID: d.CopyID, DownloadID: d.ID, Event: history.Failed, Detail: map[string]any{
		"release": d.ReleaseTitle, "reason": reason, "stalled": true, "blocklisted": blocked,
		"removedFromClient": removedFromClient, "searchingAgain": searching}})
}
