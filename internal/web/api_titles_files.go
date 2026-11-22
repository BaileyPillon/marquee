package web

import (
	"context"

	"github.com/BaileyPillon/marquee/internal/library"
	"github.com/BaileyPillon/marquee/internal/titles"
)

// What is on disk for a title, as the catalog endpoints show it. The catalog
// does not know about files and the library does not know about titles; this
// layer puts the two together, with the library's own summary queries (one
// query for a whole page of titles, one for a show's episodes, one for an
// artist's tracks).
//
// Every signed-in person sees these, like the rest of the catalog.

// titleFiles are the fields every title carries.
type titleFiles struct {
	// FileCount is how many matched files are on disk for the title.
	FileCount int `json:"fileCount"`
	// Quality is the best quality among them ("Bluray-1080p"), empty with none.
	Quality string `json:"quality"`
	// InLibrary is true when there is at least one file.
	InLibrary bool `json:"inLibrary"`
	// SizeBytes is the size of all of them together.
	SizeBytes int64 `json:"sizeBytes"`
}

func filesOf(s library.FileSummary) titleFiles {
	return titleFiles{FileCount: s.Count, Quality: s.Quality, InLibrary: s.Count > 0, SizeBytes: s.Bytes}
}

// titleItem is one row of the title list.
type titleItem struct {
	titles.Title
	titleFiles
}

type titleListView struct {
	Items  []titleItem `json:"items"`
	Total  int         `json:"total"`
	Limit  int         `json:"limit"`
	Offset int         `json:"offset"`
}

type episodeItem struct {
	titles.Episode
	HasFile bool   `json:"hasFile"`
	Quality string `json:"quality"`
	Size    int64  `json:"size"`
}

type trackItem struct {
	titles.Track
	HasFile bool   `json:"hasFile"`
	Quality string `json:"quality"`
	Size    int64  `json:"size"`
}

type albumItem struct {
	titles.Album
	Tracks []trackItem `json:"tracks"` // hides the Album's own list
	// InLibrary is true when the album has a file on disk: a track of it is matched,
	// or (a file scanned before the tracks were known) its folder names the album.
	InLibrary bool `json:"inLibrary"`
	// HaveTracks is how many of its tracks have a file.
	HaveTracks int `json:"haveTracks"`
}

// detailItem is a title with everything under it and what is on disk.
type detailItem struct {
	titles.Title
	titleFiles
	Seasons  []titles.Season `json:"seasons"`
	Episodes []episodeItem   `json:"episodes"`
	Albums   []albumItem     `json:"albums"`
	People   []titles.Person `json:"people"`
	// Hydrating is true while an artist's albums are still being loaded.
	Hydrating bool `json:"hydrating,omitempty"`
}

// listView adds the file facts to a page of titles.
func (s *Server) listView(ctx context.Context, res *titles.ListResult) (*titleListView, error) {
	out := &titleListView{Items: make([]titleItem, len(res.Items)), Total: res.Total, Limit: res.Limit, Offset: res.Offset}
	var sums map[int64]library.FileSummary
	if s.Library != nil && len(res.Items) > 0 {
		ids := make([]int64, len(res.Items))
		for i, t := range res.Items {
			ids[i] = t.ID
		}
		var err error
		if sums, err = s.Library.TitleFiles(ctx, ids); err != nil {
			return nil, err
		}
	}
	for i, t := range res.Items {
		out.Items[i] = titleItem{Title: t, titleFiles: filesOf(sums[t.ID])}
	}
	return out, nil
}

// detailView adds the file facts to a title, its episodes and its tracks.
func (s *Server) detailView(ctx context.Context, d *titles.Detail) (*detailItem, error) {
	out := &detailItem{
		Title: d.Title, Seasons: d.Seasons, People: d.People,
		Episodes:  make([]episodeItem, len(d.Episodes)),
		Albums:    make([]albumItem, len(d.Albums)),
		Hydrating: s.Titles != nil && s.Titles.Hydrating(d.ID),
	}
	var (
		eps    map[int64]library.FileSummary
		tracks map[int64]library.FileSummary
	)
	if s.Library != nil {
		sums, err := s.Library.TitleFiles(ctx, []int64{d.ID})
		if err != nil {
			return nil, err
		}
		out.titleFiles = filesOf(sums[d.ID])
		switch d.Kind {
		case titles.KindShow:
			if eps, err = s.Library.EpisodeFiles(ctx, d.ID); err != nil {
				return nil, err
			}
		case titles.KindArtist:
			if tracks, err = s.Library.TrackFiles(ctx, d.ID); err != nil {
				return nil, err
			}
		}
	}
	for i, e := range d.Episodes {
		f := eps[e.ID]
		out.Episodes[i] = episodeItem{Episode: e, HasFile: f.Count > 0, Quality: f.Quality, Size: f.Bytes}
	}
	// Which albums have a file, by the same rule the music search uses to say
	// what is missing.
	var onDisk map[int64]bool
	if d.Kind == titles.KindArtist && s.Requests != nil && s.Library != nil {
		var err error
		if onDisk, err = s.Requests.AlbumsOnDisk(ctx, d.ID); err != nil {
			return nil, err
		}
	}
	for i, a := range d.Albums {
		item := albumItem{Album: a, Tracks: make([]trackItem, len(a.Tracks))}
		for j, t := range a.Tracks {
			f := tracks[t.ID]
			item.Tracks[j] = trackItem{Track: t, HasFile: f.Count > 0, Quality: f.Quality, Size: f.Bytes}
			if f.Count > 0 {
				item.HaveTracks++
			}
		}
		item.InLibrary = onDisk[a.ID] || item.HaveTracks > 0
		out.Albums[i] = item
	}
	return out, nil
}

// artistAdded is the answer to adding an artist: the title, and whether its
// albums are still being loaded in the background.
type artistAdded struct {
	Title     *detailItem `json:"title"`
	Hydrating bool        `json:"hydrating"`
}
