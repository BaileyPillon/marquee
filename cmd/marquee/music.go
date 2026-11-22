package main

import (
	"context"

	"github.com/BaileyPillon/marquee/internal/grab"
	"github.com/BaileyPillon/marquee/internal/library"
	"github.com/BaileyPillon/marquee/internal/parse"
	"github.com/BaileyPillon/marquee/internal/requests"
	"github.com/BaileyPillon/marquee/internal/search"
	"github.com/BaileyPillon/marquee/internal/titles"
)

// Music joins three modules that do not import each other:
//
//   - the library matches a scanned track to the catalog's track (files.track_id),
//     so it needs an artist's albums and tracks: albumSource gives it them;
//   - the search says which albums of an artist are missing, so it needs to know
//     which have a file: musicShelf asks the requests module, which makes the same
//     reading Wanted does;
//   - the importer, the pages and the search all read an artist download's episode
//     ids as album ids (grab.Target has no album field).
var (
	_ library.AlbumSource = albumSource{}
	_ search.Shelf        = (*musicShelf)(nil)
)

// albumSource is the catalog as the library matches music against it: the
// artists (what the library already had) and now their albums and tracks.
type albumSource struct {
	titleSource
	cat *titles.Service
}

func (a albumSource) Albums(ctx context.Context, titleID int64) ([]library.AlbumInfo, error) {
	d, err := a.cat.Get(ctx, titleID)
	if err != nil {
		return nil, err
	}
	out := make([]library.AlbumInfo, len(d.Albums))
	for i, al := range d.Albums {
		info := library.AlbumInfo{ID: al.ID, Name: al.Name, Year: al.Year, Tracks: make([]parse.CatalogTrack, len(al.Tracks))}
		for j, t := range al.Tracks {
			info.Tracks[j] = parse.CatalogTrack{ID: t.ID, Disc: t.Disc, Position: t.Position, Name: t.Name}
		}
		out[i] = info
	}
	return out, nil
}

// albumsOnDisk is the part of the requests module the music search needs.
type albumsOnDisk interface {
	AlbumsOnDisk(ctx context.Context, titleID int64) (map[int64]bool, error)
}

var _ albumsOnDisk = (*requests.Service)(nil)

// musicShelf says what an artist's copy already has, album by album, and leaves
// movies and shows to the shelf it wraps.
type musicShelf struct {
	base   search.Shelf
	albums albumsOnDisk
}

func (m *musicShelf) Have(ctx context.Context, c grab.Copy) (search.Have, error) {
	if c.Kind != titles.KindArtist {
		return m.base.Have(ctx, c)
	}
	on, err := m.albums.AlbumsOnDisk(ctx, c.TitleID)
	if err != nil {
		return search.Have{}, err
	}
	var h search.Have
	for id, ok := range on {
		if ok {
			h.AlbumIDs = append(h.AlbumIDs, id)
		}
	}
	return h, nil
}

// wireMusic gives the library the albums to match tracks against and the search
// the albums that are on disk. Call it once the library, the catalog, the
// requests and the release search all exist.
func wireMusic(lib *library.Service, cat *titles.Service, rf *releaseFinding, reqs *requests.Service) {
	lib.SetTitleSource(albumSource{titleSource: titleSource{cat: cat}, cat: cat})
	rf.Search.SetShelf(&musicShelf{base: &libraryShelf{lib: lib}, albums: reqs})
}
