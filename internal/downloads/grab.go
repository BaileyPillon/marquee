package downloads

import (
	"context"
	"errors"
	"net/url"
	"strings"

	"github.com/BaileyPillon/marquee/internal/grab"
)

// kindsFor lists the client kinds that download a protocol.
func kindsFor(protocol string) []string {
	switch protocol {
	case grab.ProtocolTorrent:
		return []string{KindQBittorrent, KindDeluge, KindTransmission}
	case grab.ProtocolUsenet:
		return []string{KindSABnzbd, KindNZBGet}
	}
	return nil
}

// prepared is a release made ready to hand to a client.
type prepared struct {
	torrent []byte
	magnet  string
	nzb     string
}

// prepare fetches a torrent file (or finds the magnet link) once, before any
// client is tried. A torrent link is fetched by Marquee itself: it may need the
// indexer's key, and the client may not be able to reach the indexer at all.
func (s *Service) prepare(ctx context.Context, r grab.Release) (prepared, error) {
	if r.Protocol == grab.ProtocolUsenet {
		u, err := url.Parse(strings.TrimSpace(r.DownloadURL))
		if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
			return prepared{}, wrap(ErrReleaseUnavailable, errors.New("the release has no NZB link"))
		}
		return prepared{nzb: strings.TrimSpace(r.DownloadURL)}, nil
	}

	magnet := strings.TrimSpace(r.MagnetURL)
	if strings.TrimSpace(r.DownloadURL) != "" {
		f, err := fetchTorrent(ctx, s.hc, strings.TrimSpace(r.DownloadURL))
		switch {
		case err == nil && f.Magnet != "":
			magnet = f.Magnet // the indexer's link turned out to be a magnet
		case err == nil:
			return prepared{torrent: f.File}, nil
		case magnet == "":
			return prepared{}, wrap(ErrReleaseUnavailable, err)
		default:
			s.log.Debug("could not fetch the torrent file; using the magnet link", "err", err)
		}
	}
	if magnet == "" {
		return prepared{}, wrap(ErrReleaseUnavailable, errors.New("the release has no download link"))
	}
	if _, err := MagnetInfoHash(magnet); err != nil {
		return prepared{}, wrap(ErrReleaseUnavailable, err)
	}
	return prepared{magnet: magnet}, nil
}

// Grab sends a release to a download client and starts tracking it. The client
// is the enabled one for the release's protocol with the lowest priority
// number; if it does not answer, the next one is tried. It returns the id of
// the downloads row.
//
// Errors carry a stable code (see Error): no_client_for_protocol,
// client_unreachable, client_rejected, release_unavailable.
func (s *Service) Grab(ctx context.Context, t grab.Target, r grab.Release) (int64, error) {
	kinds := kindsFor(r.Protocol)
	if len(kinds) == 0 {
		return 0, wrap(ErrReleaseUnavailable, errors.New("the release is neither a torrent nor an NZB"))
	}
	clients, err := s.store.enabledClients(ctx, kinds...)
	if err != nil {
		return 0, err
	}
	// Transmission and NZBGet have no adapter yet: a client of that kind cannot be used.
	usable := clients[:0]
	for _, c := range clients {
		if SupportedKind(c.Kind) {
			usable = append(usable, c)
		}
	}
	if len(usable) == 0 {
		return 0, ErrNoClient
	}
	prep, err := s.prepare(ctx, r)
	if err != nil {
		return 0, err
	}

	var lastErr error
	for _, c := range usable {
		a, err := s.adapter(c)
		if err != nil {
			lastErr = err
			s.clientFailedNow(ctx, c.ID, err)
			continue
		}
		savePath := t.SavePath
		if savePath == "" {
			savePath = c.SavePath
		}
		cctx, cancel := context.WithTimeout(ctx, clientTimeout)
		ref, err := a.Add(cctx, AddInput{
			TorrentFile: prep.torrent, MagnetURL: prep.magnet, NZBURL: prep.nzb,
			Name: r.Title, Category: c.Category, SavePath: savePath,
		})
		cancel()
		if err != nil {
			lastErr = err
			s.clientFailedNow(ctx, c.ID, err)
			continue
		}
		_ = s.store.clientOK(context.WithoutCancel(ctx), c.ID, s.now())

		quality := t.Quality
		if quality == "" {
			quality = r.Quality
		}
		d := Download{
			ClientID: c.ID, ClientRef: ref, Protocol: r.Protocol,
			TitleID: t.TitleID, CopyID: t.CopyID, Kind: t.Kind, Quality: quality, EpisodeIDs: t.EpisodeIDs,
			ReleaseTitle: r.Title, IndexerID: r.IndexerID, GUID: r.GUID, Size: r.Size,
			State: StateQueued,
		}
		id, existing, err := s.store.insertDownload(context.WithoutCancel(ctx), d, s.now())
		if err != nil {
			// The client has it but Marquee cannot track it: take it back out.
			rctx, rcancel := context.WithTimeout(context.WithoutCancel(ctx), clientTimeout)
			_ = a.Remove(rctx, ref, true)
			rcancel()
			return 0, err
		}
		s.active.Store(true)
		if !existing {
			d.ID = id
			s.publish("added", d)
		}
		return id, nil
	}

	if errors.Is(lastErr, ErrRejected) {
		return 0, wrap(ErrClientRejected, lastErr)
	}
	return 0, wrap(ErrClientUnreachable, lastErr)
}

// clientFailedNow records a client problem where the owner will see it.
func (s *Service) clientFailedNow(ctx context.Context, id int64, err error) {
	if e := s.store.clientFailed(context.WithoutCancel(ctx), id, PlainError(err), s.now()); e != nil {
		s.log.Warn("could not record a download client problem", "client", id, "err", e)
	}
}
