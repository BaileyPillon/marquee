package sources

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
)

// seal turns a feed item into the row remembered in releases_seen, with both
// links sealed.
func (s *Service) seal(it Item) (seenRow, error) {
	row := seenRow{
		IndexerID: it.IndexerID, GUID: it.GUID, Title: it.Title, Size: it.Size,
		PublishedAt: it.PublishedAt, Protocol: it.Protocol, InfoHash: it.InfoHash,
		Seeders: it.Seeders, SeenAt: s.now(),
	}
	var err error
	if it.DownloadURL != "" {
		if row.LinkSealed, err = s.box.Seal(it.DownloadURL); err != nil {
			return seenRow{}, err
		}
	}
	if it.MagnetURL != "" {
		if row.MagnetSealed, err = s.box.Seal(it.MagnetURL); err != nil {
			return seenRow{}, err
		}
	}
	raw, err := json.Marshal(it.Attrs)
	if err != nil {
		return seenRow{}, err
	}
	row.RawAttrs = string(raw)
	return row, nil
}

// SeenItem looks one remembered release up again, with its links opened. It is
// how a grab finds a release it was told about earlier.
func (s *Service) SeenItem(ctx context.Context, indexerID int64, guid string) (Item, bool, error) {
	row, ok, err := s.store.GetSeen(ctx, indexerID, guid)
	if err != nil || !ok {
		return Item{}, false, err
	}
	it := Item{
		IndexerID: row.IndexerID, GUID: row.GUID, Title: row.Title, Size: row.Size,
		PublishedAt: row.PublishedAt, Protocol: row.Protocol, InfoHash: row.InfoHash,
		Seeders: row.Seeders, Peers: -1, Grabs: -1, Attrs: map[string]string{},
	}
	_ = json.Unmarshal([]byte(row.RawAttrs), &it.Attrs)
	it.readIDs()
	if row.LinkSealed != "" {
		if it.DownloadURL, err = s.box.Open(row.LinkSealed); err != nil {
			return Item{}, false, fmt.Errorf("sources: a remembered link cannot be decrypted: %w", err)
		}
	}
	if row.MagnetSealed != "" {
		if it.MagnetURL, err = s.box.Open(row.MagnetSealed); err != nil {
			return Item{}, false, fmt.Errorf("sources: a remembered link cannot be decrypted: %w", err)
		}
	}
	return it, true, nil
}

// Remember stores items in releases_seen and returns the ones that were new.
func (s *Service) Remember(ctx context.Context, items []Item) ([]Item, error) {
	rows := make([]seenRow, 0, len(items))
	kept := make([]Item, 0, len(items))
	for _, it := range items {
		row, err := s.seal(it)
		if err != nil {
			return nil, err
		}
		rows = append(rows, row)
		kept = append(kept, it)
	}
	fresh, err := s.store.InsertSeen(ctx, rows)
	if err != nil {
		return nil, err
	}
	out := make([]Item, len(fresh))
	for i, idx := range fresh {
		out[i] = kept[idx]
	}
	return out, nil
}

// SyncRSS is the "sources.rss" job: fetch the latest items of every enabled
// source, remember them for a week, and hand the new ones to the matcher. It
// returns an error only when every source failed, so a single flaky source does
// not make the whole job look broken (its own error shows on its card).
func (s *Service) SyncRSS(ctx context.Context) error {
	outs, err := s.SearchAll(ctx, Query{Limit: DefaultLimit})
	if err != nil {
		if ctx.Err() != nil {
			return nil // shutting down
		}
		return err
	}
	var (
		fresh        []Item
		asked, bad   int
		firstFailure error
	)
	for _, o := range outs {
		if o.Skipped {
			continue
		}
		asked++
		if o.Err != nil {
			bad++
			if firstFailure == nil {
				firstFailure = fmt.Errorf("source %d: %w", o.Indexer.ID, o.Err)
			}
			continue
		}
		items, err := s.Remember(ctx, o.Items)
		if err != nil {
			if ctx.Err() != nil {
				return nil
			}
			return err
		}
		fresh = append(fresh, items...)
	}
	if _, err := s.store.PruneSeen(ctx, s.now().Add(-SeenKeep)); err != nil && ctx.Err() == nil {
		s.log.Warn("could not forget old feed items", "err", err)
	}
	if len(fresh) > 0 {
		if b := s.matcher.Load(); b != nil {
			b.m.MatchNew(ctx, fresh)
		}
	}
	s.log.Info("feeds synced", "sources", asked, "failed", bad, "new", len(fresh))
	if asked > 0 && bad == asked && ctx.Err() == nil {
		return fmt.Errorf("sources.rss: every source failed; first: %w", firstFailure)
	}
	return nil
}

// ---- helpers for callers -----------------------------------------------------

// Plain returns the text of an error from this package, or a generic line.
func Plain(err error) string {
	var se *Error
	if errors.As(err, &se) {
		return se.Message
	}
	if err == nil {
		return ""
	}
	msg := strings.TrimSpace(err.Error())
	if msg == "" {
		return "The source did not answer."
	}
	return msg
}
