// Package grab holds the small set of types shared by the modules that decide
// what to fetch (requests, decide/sources) and the module that fetches it
// (downloads). It has no logic and imports nothing from Marquee, so any layer
// may depend on it.
package grab

import (
	"context"
	"time"
)

// Quality names a wanted copy of a title. A title can have one copy of each.
const (
	QualityHD  = "hd"  // up to 1080p
	QualityUHD = "uhd" // 2160p, kept as a separate copy in its own folder
)

// Protocols a release can be fetched over.
const (
	ProtocolTorrent = "torrent"
	ProtocolUsenet  = "usenet"
)

// Copy is one wanted version of a title (the HD copy or the 4K copy), created
// when a request is approved or the owner adds a title directly.
type Copy struct {
	ID         int64
	TitleID    int64
	Kind       string // movie | show | artist
	Quality    string // QualityHD | QualityUHD
	FolderID   int64  // library folder the finished files go to
	FolderPath string
	Monitored  bool
}

// CopySource is implemented by the requests module.
type CopySource interface {
	Copy(ctx context.Context, id int64) (Copy, error)
	// Wanted lists monitored copies that still have something missing.
	Wanted(ctx context.Context) ([]Copy, error)
}

// Release is one search result from a source, already parsed and scored.
type Release struct {
	IndexerID   int64
	GUID        string
	Title       string
	DownloadURL string // .torrent or .nzb link (may carry the source's key; never log it)
	MagnetURL   string
	InfoHash    string // lower-case hex, torrents only, when known
	Protocol    string // ProtocolTorrent | ProtocolUsenet
	Size        int64
	Seeders     int
	PublishedAt time.Time
	Quality     string // parse.QualityName, e.g. "Bluray-1080p"
	Score       int
	Reasons     []string // plain-words reasons shown to people
}

// Target says what a grab is for and where the result should end up.
type Target struct {
	CopyID     int64
	TitleID    int64
	Kind       string
	EpisodeIDs []int64 // empty for movies and whole-season packs resolved later
	Quality    string
	SavePath   string // download client save path, when the owner set one
}

// Grabber is implemented by the downloads module: send the release to a
// download client and start tracking it. Returns the downloads row id.
type Grabber interface {
	Grab(ctx context.Context, t Target, r Release) (downloadID int64, err error)
}

// Searcher is implemented by the decide/sources module: search every enabled
// source for a copy and grab the best acceptable release, if any.
type Searcher interface {
	SearchCopy(ctx context.Context, copyID int64) error
}

// CompletedHandler is implemented by the importer (M6): called once when a
// tracked download finishes in its client.
type CompletedHandler interface {
	OnCompleted(ctx context.Context, downloadID int64)
}

// StalledHandler is implemented by the importer, which owns the blocklist, the
// search and the title's history: called once when the downloads module gives up
// on a download that stopped making progress. The download is already marked
// failed (with reason) and, when removedFromClient, gone from its client. The
// handler blocklists the release, makes the search forget it and searches again.
type StalledHandler interface {
	OnStalled(ctx context.Context, downloadID int64, reason string, removedFromClient bool)
}
