package library

import (
	"context"
	"testing"

	"github.com/BaileyPillon/marquee/internal/parse"
)

// albumTitles is a TitleSource that also lists an artist's albums.
type albumTitles struct {
	*fakeTitles
	albums map[int64][]AlbumInfo
	calls  *int
}

func (a albumTitles) Albums(_ context.Context, id int64) ([]AlbumInfo, error) {
	*a.calls++
	return a.albums[id], nil
}

const chopinID = 77

func chopinTitles() (albumTitles, *int) {
	ft := newFakeTitles()
	ft.cands[CandidateArtist] = []Candidate{{ID: chopinID, Kind: CandidateArtist, Name: "Frédéric Chopin", AltNames: []string{"Chopin"}}}
	calls := new(int)
	return albumTitles{fakeTitles: ft, calls: calls, albums: map[int64][]AlbumInfo{chopinID: {
		{ID: 1, Name: "Nocturnes", Year: 1998, Tracks: []parse.CatalogTrack{
			{ID: 101, Disc: 1, Position: 1, Name: "Nocturne in B-flat minor, Op. 9 No. 1"},
			{ID: 102, Disc: 1, Position: 2, Name: "Nocturne in E-flat major, Op. 9 No. 2"},
		}},
		{ID: 2, Name: "Études", Year: 1985, Tracks: []parse.CatalogTrack{
			{ID: 201, Disc: 1, Position: 1, Name: "Étude in C major, Op. 10 No. 1"},
		}},
	}}}, calls
}

var chopinFiles = []string{
	"Frédéric Chopin/Nocturnes (1998) [FLAC]/01 - Nocturne in B-flat minor, Op. 9 No. 1.flac",
	"Frédéric Chopin/Nocturnes (1998) [FLAC]/02 - Nocturne in E-flat major, Op. 9 No. 2.flac",
	"Frédéric Chopin/Nocturnes (1998) [FLAC]/03 - A Bonus Nobody Listed.flac",
	"Frédéric Chopin/Études (1985) [FLAC]/01 - Étude in C major, Op. 10 No. 1.flac",
	"Frédéric Chopin/Ballades (2000) [FLAC]/01 - Ballade No. 1.flac",
}

func TestScanMatchesTracks(t *testing.T) {
	e := newEnv(t)
	ts, calls := chopinTitles()
	e.svc.SetTitleSource(ts)
	f, _ := e.addFolder(KindMusic, chopinFiles...)
	e.scan(f)

	rows := e.files(f)
	want := map[string]int64{
		chopinFiles[0]: 101, // by number and title
		chopinFiles[1]: 102,
		chopinFiles[3]: 201, // another album of the artist
		chopinFiles[2]: 0,   // track 3 is not in the catalog: matched to the artist only
		chopinFiles[4]: 0,   // an album the catalog does not have
	}
	for rel, track := range want {
		row, ok := rows[rel]
		if !ok || row.State != StateMatched || row.TitleID == nil || *row.TitleID != chopinID {
			t.Errorf("%s: not matched to the artist: %+v", rel, row)
			continue
		}
		switch {
		case track == 0 && row.TrackID != nil:
			t.Errorf("%s: track = %d, want none", rel, *row.TrackID)
		case track != 0 && (row.TrackID == nil || *row.TrackID != track):
			t.Errorf("%s: track = %v, want %d", rel, row.TrackID, track)
		}
	}
	if *calls != 1 {
		t.Errorf("the albums were listed %d times in one scan, want once", *calls)
	}
}

func TestScanRematchesTracksOnceAlbumsAreKnown(t *testing.T) {
	e := newEnv(t)
	ts, _ := chopinTitles()
	e.svc.SetTitleSource(ts.fakeTitles) // an artist with no albums loaded yet
	f, _ := e.addFolder(KindMusic, chopinFiles...)
	e.scan(f)
	for rel, row := range e.files(f) {
		if row.State != StateMatched || row.TrackID != nil {
			t.Fatalf("%s: %+v, want matched with no track", rel, row)
		}
	}

	e.svc.SetTitleSource(ts) // the albums arrive
	e.scan(f)
	rows := e.files(f)
	if r := rows[chopinFiles[0]]; r.TrackID == nil || *r.TrackID != 101 {
		t.Errorf("after the albums arrived: %+v", r)
	}
	if r := rows[chopinFiles[3]]; r.TrackID == nil || *r.TrackID != 201 {
		t.Errorf("after the albums arrived: %+v", r)
	}
}

func TestImportFilesRecordsTheTrack(t *testing.T) {
	e := newEnv(t)
	f, root := e.addFolder(KindMusic)
	rel := "Frédéric Chopin/Nocturnes (1998)/01 - Nocturne in B-flat minor, Op. 9 No. 1.flac"
	mkFiles(t, root, rel, "Frédéric Chopin/Nocturnes (1998)/02 - Two.flac")
	err := e.svc.ImportFiles(context.Background(), f.ID, []ImportedFile{
		{RelPath: rel, TitleID: chopinID, TrackID: 101},
		{RelPath: "Frédéric Chopin/Nocturnes (1998)/02 - Two.flac", TitleID: chopinID},
	})
	if err != nil {
		t.Fatal(err)
	}
	rows := e.files(f)
	if r := rows[rel]; r.State != StateMatched || r.TrackID == nil || *r.TrackID != 101 || r.TitleID == nil || *r.TitleID != chopinID {
		t.Errorf("imported with a track: %+v", r)
	}
	if r := rows["Frédéric Chopin/Nocturnes (1998)/02 - Two.flac"]; r.TrackID != nil {
		t.Errorf("imported without a track: %+v", r)
	}

	ids, err := e.svc.FileIDs(context.Background(), f.ID, []string{rel, "nope.flac", "../escape.flac"})
	if err != nil || len(ids) != 1 || ids[rel] != rows[rel].ID {
		t.Errorf("FileIDs = %v, %v; want only %q", ids, err, rel)
	}
}
