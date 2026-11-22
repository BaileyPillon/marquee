package importer

import (
	"context"
	"strings"
	"sync"
	"testing"

	"github.com/BaileyPillon/marquee/internal/downloads"
	"github.com/BaileyPillon/marquee/internal/grab"
	"github.com/BaileyPillon/marquee/internal/library"
	"github.com/BaileyPillon/marquee/internal/requests"
	"github.com/BaileyPillon/marquee/internal/search"
	"github.com/BaileyPillon/marquee/internal/titles"
)

const (
	nocturneOne = "Nocturne in B-flat minor, Op. 9 No. 1"
	nocturneTwo = "Nocturne in E-flat major, Op. 9 No. 2"
)

// albumNamed finds an album of the artist by name.
func albumNamed(t *testing.T, d *titles.Detail, name string) titles.Album {
	t.Helper()
	for _, a := range d.Albums {
		if a.Name == name {
			return a
		}
	}
	t.Fatalf("%s has no album %q", d.Name, name)
	return titles.Album{}
}

// grabAlbum sends a release for one album, the way the search does: the album is
// in the download's episode ids.
func (r *rig) grabAlbum(cp grab.Copy, artist *titles.Detail, release string, album titles.Album) int64 {
	r.t.Helper()
	return r.grabAs(cp, artist, release, grab.ProtocolTorrent, []int64{album.ID})
}

// musicRows lists the library rows of the music folder by path.
func (r *rig) musicRows() map[string]library.File {
	r.t.Helper()
	folders, err := r.lib.ListFolders(bg())
	if err != nil {
		r.t.Fatal(err)
	}
	for _, f := range folders {
		if f.Kind == library.KindMusic {
			return r.libraryFiles(f)
		}
	}
	r.t.Fatal("no music folder")
	return nil
}

func trackNamed(t *testing.T, d *titles.Detail, album, track string) titles.Track {
	t.Helper()
	for _, tr := range albumNamed(t, d, album).Tracks {
		if tr.Name == track {
			return tr
		}
	}
	t.Fatalf("no track %q on %q", track, album)
	return titles.Track{}
}

func TestAlbumImportMarksEachTrack(t *testing.T) {
	r := newRig(t).withImporter()
	artist := r.artist(chopinMBID)
	cp, reqID := r.ask(artist.ID, "hd", requests.Scope{All: true})
	nocturnesAlbum := albumNamed(t, artist, "Nocturnes")
	name := "Frédéric Chopin - Nocturnes (1998) [FLAC]"
	id := r.grabAlbum(cp, artist, name, nocturnesAlbum)
	r.complete(id, r.content(name, map[string]string{
		"01 - " + nocturneOne + ".flac": "one",
		"02 - " + nocturneTwo + ".flac": "two",
		"folder.jpg":                    "jpg",
	}))
	if d := r.download(id); d.State != downloads.StateImported {
		t.Fatalf("state = %s %q", d.State, d.Error)
	}
	rows := r.musicRows()
	for i, title := range []string{nocturneOne, nocturneTwo} {
		rel := "Frédéric Chopin/Nocturnes (1998)/" + []string{"01", "02"}[i] + " - " + title + ".flac"
		row, ok := rows[rel]
		if !ok {
			t.Fatalf("no row for %s: %v", rel, rows)
		}
		want := trackNamed(t, artist, "Nocturnes", title)
		if row.State != library.StateMatched || row.TitleID == nil || *row.TitleID != artist.ID || row.TrackID == nil || *row.TrackID != want.ID {
			t.Errorf("%s: %+v, want matched to track %d", rel, row, want.ID)
		}
		if row.Quality != "FLAC" {
			t.Errorf("%s: quality = %q", rel, row.Quality)
		}
	}
	if got := r.requestState(reqID); got != requests.StateAvailable {
		t.Errorf("request state = %s", got)
	}
	// What the library has is exactly what the artist page will show: both tracks.
	have, err := r.lib.TrackFiles(bg(), artist.ID)
	if err != nil || len(have) != 2 {
		t.Errorf("TrackFiles = %v, %v; want both tracks", have, err)
	}
}

func TestAlbumImportFromASceneFolder(t *testing.T) {
	r := newRig(t).withImporter()
	artist := r.artist(chopinMBID)
	cp, _ := r.ask(artist.ID, "hd", requests.Scope{All: true})
	// The folder and the files say nothing the path reader can split; the folder's
	// scene name does.
	name := "Frederic_Chopin-Nocturnes-CD-FLAC-1998-GRP"
	id := r.grabAlbum(cp, artist, name, albumNamed(t, artist, "Nocturnes"))
	r.complete(id, r.content(name, map[string]string{
		"01-nocturne_in_b-flat_minor,_op._9_no._1.flac": "one",
		"02-nocturne_in_e-flat_major,_op._9_no._2.flac": "two",
	}))
	if d := r.download(id); d.State != downloads.StateImported {
		t.Fatalf("state = %s %q", d.State, d.Error)
	}
	rows := r.musicRows()
	for rel, row := range rows {
		if !strings.HasPrefix(rel, "Frédéric Chopin/Nocturnes (1998)/0") || row.TrackID == nil {
			t.Errorf("%s: %+v", rel, row)
		}
	}
	if len(rows) != 2 {
		t.Errorf("rows = %v, want two tracks", rows)
	}
}

func TestTrackMatchingByTitleAndNumber(t *testing.T) {
	r := newRig(t).withImporter()
	artist := r.artist(chopinMBID)
	cp, _ := r.ask(artist.ID, "hd", requests.Scope{All: true})
	name := "Frédéric Chopin - Nocturnes (1998) [FLAC]"
	id := r.grabAlbum(cp, artist, name, albumNamed(t, artist, "Nocturnes"))
	r.complete(id, r.content(name, map[string]string{
		// A release that numbers its tracks one up from the catalog: the title wins.
		"02 - " + nocturneOne + ".flac": "one",
		// A track the catalog has no such title for, at a number it does have: it is
		// kept under its own title and does not claim the catalog's track.
		"03 - A Bonus Nobody Listed.flac": "bonus",
	}))
	if d := r.download(id); d.State != downloads.StateImported {
		t.Fatalf("state = %s %q", d.State, d.Error)
	}
	rows := r.musicRows()
	one := rows["Frédéric Chopin/Nocturnes (1998)/01 - "+nocturneOne+".flac"]
	if one.TrackID == nil || *one.TrackID != trackNamed(t, artist, "Nocturnes", nocturneOne).ID {
		t.Errorf("the title should win over the number: %+v (rows %v)", one, rows)
	}
	bonus, ok := rows["Frédéric Chopin/Nocturnes (1998)/03 - A Bonus Nobody Listed.flac"]
	if !ok || bonus.TrackID != nil || bonus.State != library.StateMatched {
		t.Errorf("the bonus track should be matched to the artist but to no track: %+v (rows %v)", bonus, rows)
	}
}

func TestMultiDiscAlbumNaming(t *testing.T) {
	r := newRig(t).withImporter()
	artist := r.artist(chopinMBID)
	al := albumNamed(t, artist, "Nocturnes")
	for pos, title := range map[int]string{1: "Waltz One", 2: "Waltz Two"} {
		if _, err := r.db.Exec(`INSERT INTO tracks (album_id, disc, position, name) VALUES (?, 2, ?, ?)`, al.ID, pos, title); err != nil {
			t.Fatal(err)
		}
	}
	cp, _ := r.ask(artist.ID, "hd", requests.Scope{All: true})
	name := "Frédéric Chopin - Nocturnes (1998) [FLAC]"
	id := r.grabAlbum(cp, artist, name, al)
	r.complete(id, r.content(name, map[string]string{
		"CD1/01 - " + nocturneOne + ".flac": "a",
		"CD2/01 - Waltz One.flac":           "b",
	}))
	if d := r.download(id); d.State != downloads.StateImported {
		t.Fatalf("state = %s %q", d.State, d.Error)
	}
	rows := r.musicRows()
	for _, rel := range []string{
		"Frédéric Chopin/Nocturnes (1998)/1-01 - " + nocturneOne + ".flac",
		"Frédéric Chopin/Nocturnes (1998)/2-01 - Waltz One.flac",
	} {
		if row, ok := rows[rel]; !ok || row.TrackID == nil {
			t.Errorf("%s: %+v (rows %v)", rel, row, rows)
		}
	}
}

func TestImportFetchesTracksNobodyLoadedYet(t *testing.T) {
	r := newRig(t).withImporter()
	artist := r.artist(chopinMBID)
	cp, _ := r.ask(artist.ID, "hd", requests.Scope{All: true})
	al := albumNamed(t, artist, "Nocturnes")
	// The albums are there but their tracks were never loaded.
	if _, err := r.db.Exec(`DELETE FROM tracks WHERE album_id = ?`, al.ID); err != nil {
		t.Fatal(err)
	}
	name := "Frédéric Chopin - Nocturnes (1998) [FLAC]"
	id := r.grabAlbum(cp, artist, name, al)
	r.complete(id, r.content(name, map[string]string{
		"01 - nocturne in b-flat minor.flac": "one", // a looser spelling than the catalog's
	}))
	if d := r.download(id); d.State != downloads.StateImported {
		t.Fatalf("state = %s %q", d.State, d.Error)
	}
	d, err := r.cat.Get(bg(), artist.ID)
	if err != nil || len(albumNamed(t, d, "Nocturnes").Tracks) != 2 {
		t.Fatalf("the importer should have loaded the tracks: %+v, %v", albumNamed(t, d, "Nocturnes").Tracks, err)
	}
	// Named by the catalog now, and the track is marked.
	rel := "Frédéric Chopin/Nocturnes (1998)/01 - " + nocturneOne + ".flac"
	row, ok := r.musicRows()[rel]
	if !ok || row.TrackID == nil {
		t.Errorf("rows = %v, want %s with a track", r.musicRows(), rel)
	}
}

func TestImportWithNoTrackListStillWorksByFileName(t *testing.T) {
	r := newRig(t).withImporter(func(d *Deps) { d.Titles = noTrackLoading{real: d.Titles} })
	artist := r.artist(chopinMBID)
	cp, _ := r.ask(artist.ID, "hd", requests.Scope{All: true})
	if _, err := r.db.Exec(`DELETE FROM tracks`); err != nil {
		t.Fatal(err)
	}
	name := "Frédéric Chopin - Nocturnes (1998) [FLAC]"
	id := r.grabAs(cp, artist, name, grab.ProtocolTorrent, nil)
	r.complete(id, r.content(name, map[string]string{"07 - Anything At All.flac": "x"}))
	if d := r.download(id); d.State != downloads.StateImported {
		t.Fatalf("state = %s %q", d.State, d.Error)
	}
	row, ok := r.musicRows()["Frédéric Chopin/Nocturnes (1998)/07 - Anything At All.flac"]
	if !ok || row.TrackID != nil {
		t.Errorf("rows = %v", r.musicRows())
	}
}

// noTrackLoading hides EnsureTracks from the importer: a catalog that cannot
// fetch tracks.
type noTrackLoading struct{ real Catalog }

func (n noTrackLoading) Get(ctx context.Context, id int64) (*titles.Detail, error) {
	return n.real.Get(ctx, id)
}

func TestAnAlbumThatIsNotInTheCatalogIsTurnedDown(t *testing.T) {
	r := newRig(t).withImporter()
	artist := r.artist(chopinMBID)
	cp, _ := r.ask(artist.ID, "hd", requests.Scope{All: true})
	al := albumNamed(t, artist, "Nocturnes")
	// The release is labelled as the album asked for, but the folder holds another.
	id := r.grabAlbum(cp, artist, "Frédéric Chopin - Nocturnes (1998) [FLAC]", al)
	r.complete(id, r.content("Frédéric Chopin - Ballades (2001) [FLAC]", map[string]string{"01 - Ballade No. 1.flac": "x"}))
	d := r.download(id)
	if d.State != downloads.StateFailed || !strings.Contains(d.Error, "Cannot tell which album") {
		t.Fatalf("state = %s %q", d.State, d.Error)
	}
	if len(r.blocklist()) != 1 {
		t.Errorf("a release holding another album should be blocklisted: %v", r.blocklist())
	}
	// The search is told to forget the album, so it looks again.
	f := r.srch.forgets()
	if len(f) != 1 || f[0].copyID != cp.ID || len(f[0].keys) != 1 || f[0].keys[0] != search.AlbumKey(al.ID) {
		t.Errorf("forgets = %+v, want the album's key %q", f, search.AlbumKey(al.ID))
	}
	if got := r.srch.waitSearch(t); got != cp.ID {
		t.Errorf("searched copy %d again, want %d", got, cp.ID)
	}
	if len(r.musicRows()) != 0 {
		t.Errorf("nothing should have been imported: %v", r.musicRows())
	}
}

func TestMusicByAnotherArtistIsTurnedDown(t *testing.T) {
	r := newRig(t).withImporter()
	artist := r.artist(chopinMBID)
	cp, _ := r.ask(artist.ID, "hd", requests.Scope{All: true})
	name := "Johann Bach - Nocturnes (1998) [FLAC]"
	id := r.grabAs(cp, artist, name, grab.ProtocolTorrent, nil)
	r.complete(id, r.content(name, map[string]string{"01 - Something.flac": "x"}))
	if d := r.download(id); d.State != downloads.StateFailed || !strings.Contains(d.Error, `This is by "Johann Bach"`) {
		t.Fatalf("state = %s %q", d.State, d.Error)
	}
}

// ---- the after-import hook -------------------------------------------------------------------

type hookCalls struct {
	mu   sync.Mutex
	runs [][]int64
}

func (h *hookCalls) AfterImport(_ context.Context, ids []int64) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.runs = append(h.runs, append([]int64(nil), ids...))
}

func (h *hookCalls) all() [][]int64 {
	h.mu.Lock()
	defer h.mu.Unlock()
	return append([][]int64(nil), h.runs...)
}

func TestAfterImportHookGetsTheNewFileIDs(t *testing.T) {
	r := newRig(t).withImporter()
	hook := &hookCalls{}
	r.imp.SetAfterImport(hook)

	j := r.notldJob(notldRelease, map[string]string{notldRelease + ".mkv": "the film"}, grab.ProtocolTorrent)
	r.complete(j.id, j.content)
	if st := r.download(j.id); st.State != downloads.StateImported {
		t.Fatalf("state = %s %q", st.State, st.Error)
	}

	runs := hook.all()
	if len(runs) != 1 || len(runs[0]) != 1 {
		t.Fatalf("hook runs = %v, want one call with the one file", runs)
	}
	files, _, err := r.lib.Files(bg(), library.FileFilter{FolderID: r.moviesFolder.ID})
	if err != nil || len(files) != 1 || files[0].ID != runs[0][0] {
		t.Errorf("the hook got file %v; the library has %+v (%v)", runs[0], files, err)
	}
}

func TestAfterImportHookSkipsMusicAndFailures(t *testing.T) {
	r := newRig(t).withImporter()
	hook := &hookCalls{}
	r.imp.SetAfterImport(hook)

	artist := r.artist(chopinMBID)
	cp, _ := r.ask(artist.ID, "hd", requests.Scope{All: true})
	name := "Frédéric Chopin - Nocturnes (1998) [FLAC]"
	id := r.grabAlbum(cp, artist, name, albumNamed(t, artist, "Nocturnes"))
	r.complete(id, r.content(name, map[string]string{"01 - " + nocturneOne + ".flac": "x"}))
	if d := r.download(id); d.State != downloads.StateImported {
		t.Fatalf("state = %s %q", d.State, d.Error)
	}
	if runs := hook.all(); len(runs) != 0 {
		t.Errorf("music is not given to the hook: %v", runs)
	}

	// A hook that is gone changes nothing.
	r.imp.SetAfterImport(nil)
	j := r.notldJob(notldRelease, map[string]string{notldRelease + ".mkv": "the film"}, grab.ProtocolTorrent)
	r.complete(j.id, j.content)
	if st := r.download(j.id); st.State != downloads.StateImported {
		t.Fatalf("state = %s", st.State)
	}
	if runs := hook.all(); len(runs) != 0 {
		t.Errorf("a removed hook was called: %v", runs)
	}
}

func TestNeedsTracks(t *testing.T) {
	mk := func(tracksPerAlbum ...int) *titles.Detail {
		d := &titles.Detail{}
		for i, n := range tracksPerAlbum {
			a := titles.Album{ID: int64(i + 1)}
			a.Tracks = make([]titles.Track, n)
			d.Albums = append(d.Albums, a)
		}
		return d
	}
	cases := []struct {
		name   string
		d      *titles.Detail
		albums []int64
		want   bool
	}{
		{"no albums", mk(), nil, false},
		{"the album asked for has none", mk(0, 5), []int64{1}, true},
		{"the album asked for has some", mk(0, 5), []int64{2}, false},
		{"no album named, none has tracks", mk(0, 0), nil, true},
		{"no album named, one has tracks", mk(0, 5), nil, false},
		{"an album id the artist does not have", mk(0), []int64{9}, false},
	}
	for _, c := range cases {
		if got := needsTracks(c.d, c.albums); got != c.want {
			t.Errorf("%s: needsTracks = %v, want %v", c.name, got, c.want)
		}
	}
}
