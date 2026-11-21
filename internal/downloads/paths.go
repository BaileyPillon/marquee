package downloads

import (
	"context"

	"github.com/BaileyPillon/marquee/internal/core/pathmap"
)

// ClientPaths is what the importer needs to find where a client put its files.
type ClientPaths struct {
	// SavePath is the folder the owner told the client to save in ("" = the
	// client's own default), as the client sees it.
	SavePath string
	// PathMap turns the client's folders into Marquee's.
	PathMap []pathmap.Rule
}

// ClientPaths returns the path settings of one client. A client that no longer
// exists gives ErrClientNotFound.
func (s *Service) ClientPaths(ctx context.Context, clientID int64) (ClientPaths, error) {
	c, err := s.store.getClient(ctx, clientID)
	if err != nil {
		return ClientPaths{}, err
	}
	return ClientPaths{SavePath: c.SavePath, PathMap: c.PathMap}, nil
}
