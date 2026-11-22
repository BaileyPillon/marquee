package main

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"slices"
	"testing"

	"github.com/BaileyPillon/marquee/internal/core/db"
	"github.com/BaileyPillon/marquee/internal/grab"
	"github.com/BaileyPillon/marquee/internal/library"
	"github.com/BaileyPillon/marquee/internal/metadata"
	"github.com/BaileyPillon/marquee/internal/search"
	"github.com/BaileyPillon/marquee/internal/titles"
)

type fakeShelf struct{ have search.Have }

func (f fakeShelf) Have(context.Context, grab.Copy) (search.Have, error) { return f.have, nil }

type fakeOnDisk struct {
	on  map[int64]bool
	err error
}

func (f fakeOnDisk) AlbumsOnDisk(context.Context, int64) (map[int64]bool, error) { return f.on, f.err }

func TestMusicShelfAsksTheRequestsModuleOnlyForArtists(t *testing.T) {
	m := &musicShelf{base: fakeShelf{have: search.Have{Movie: true}}, albums: fakeOnDisk{on: map[int64]bool{5: true, 6: false, 7: true}}}
	h, err := m.Have(context.Background(), grab.Copy{Kind: titles.KindArtist, TitleID: 1})
	slices.Sort(h.AlbumIDs)
	if err != nil || !slices.Equal(h.AlbumIDs, []int64{5, 7}) || h.Movie {
		t.Errorf("artist: %+v, %v; want albums 5 and 7 only", h, err)
	}
	if h, err := m.Have(context.Background(), grab.Copy{Kind: titles.KindMovie, TitleID: 2}); err != nil || !h.Movie || len(h.AlbumIDs) != 0 {
		t.Errorf("movie: %+v, %v; want the wrapped shelf's answer", h, err)
	}
	m.albums = fakeOnDisk{err: errors.New("boom")}
	if _, err := m.Have(context.Background(), grab.Copy{Kind: titles.KindArtist}); err == nil {
		t.Error("an error must come back")
	}
}

type chopinProvider struct{}

func (chopinProvider) SearchArtists(context.Context, string) ([]metadata.SearchHit, error) {
	return nil, nil
}

func (chopinProvider) Artist(context.Context, string) (*metadata.ArtistInfo, error) {
	return &metadata.ArtistInfo{MBID: "a1b2c3d4-0000-4000-8000-000000000001", Name: "Frédéric Chopin", Albums: []metadata.AlbumInfo{
		{MBID: "b0000000-0000-4000-8000-000000000101", Title: "Nocturnes", Year: 1998, Type: "album", Tracks: []metadata.TrackInfo{
			{Disc: 1, Position: 1, Title: "Nocturne One"}, {Disc: 1, Position: 2, Title: "Nocturne Two"}}},
	}}, nil
}

// wireMusic makes a scan match a track to the catalog's track, through the catalog's albums.
func TestWireMusicLetsTheLibraryMatchTracks(t *testing.T) {
	ctx := context.Background()
	d, err := db.Open(filepath.Join(t.TempDir(), db.MainFile))
	if err != nil {
		t.Fatal(err)
	}
	defer d.Close()
	if _, err := db.Migrate(ctx, d, db.SetMain); err != nil {
		t.Fatal(err)
	}
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	cat := titles.NewService(d, titles.Providers{Artists: chopinProvider{}}, titles.Options{Logger: log})
	defer cat.Close()
	art, _, err := cat.Add(ctx, titles.KindArtist, metadata.ProviderMusicBrainz, "a1b2c3d4-0000-4000-8000-000000000001", titles.AddOptions{Monitored: true})
	if err != nil {
		t.Fatal(err)
	}
	lib := newLibrary(d, nil, cat, log)
	defer lib.Close()

	// As newLibrary leaves it, the scan matches the artist only.
	root := t.TempDir()
	rel := "Frédéric Chopin/Nocturnes (1998)/02 - Nocturne Two.flac"
	if err := os.MkdirAll(filepath.Dir(filepath.Join(root, rel)), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, rel), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	f, err := lib.AddFolder(ctx, "music", root, "Music")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := lib.ScanNow(ctx, f.ID); err != nil {
		t.Fatal(err)
	}
	files, _, _ := lib.Files(ctx, library.FileFilter{FolderID: f.ID})
	if len(files) != 1 || files[0].TitleID == nil || files[0].TrackID != nil {
		t.Fatalf("before wireMusic: %+v", files)
	}

	// wireMusic gives the library the albums: the next scan finds the track.
	lib.SetTitleSource(albumSource{titleSource: titleSource{cat: cat}, cat: cat})
	if _, err := lib.ScanNow(ctx, f.ID); err != nil {
		t.Fatal(err)
	}
	files, _, _ = lib.Files(ctx, library.FileFilter{FolderID: f.ID})
	detail, _ := cat.Get(ctx, art.ID)
	want := detail.Albums[0].Tracks[1].ID
	if len(files) != 1 || files[0].TrackID == nil || *files[0].TrackID != want {
		t.Errorf("after: %+v, want track %d", files, want)
	}

	albums, err := albumSource{titleSource: titleSource{cat: cat}, cat: cat}.Albums(ctx, art.ID)
	if err != nil || len(albums) != 1 || albums[0].Name != "Nocturnes" || albums[0].Year != 1998 || len(albums[0].Tracks) != 2 ||
		albums[0].Tracks[1].Name != "Nocturne Two" || albums[0].Tracks[1].Position != 2 || albums[0].ID != detail.Albums[0].ID {
		t.Errorf("Albums = %+v, %v", albums, err)
	}
}
