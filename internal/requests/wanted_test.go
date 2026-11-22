package requests

import (
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/BaileyPillon/marquee/internal/grab"
	"github.com/BaileyPillon/marquee/internal/library"
	"github.com/BaileyPillon/marquee/internal/metadata"
)

// touch puts empty files in a library folder and scans it.
func (e *env) touch(f library.Folder, rels ...string) {
	e.t.Helper()
	for _, rel := range rels {
		p := filepath.Join(f.Path, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			e.t.Fatal(err)
		}
		if err := os.WriteFile(p, nil, 0o644); err != nil {
			e.t.Fatal(err)
		}
	}
	if _, err := e.lib.ScanNow(bg(), f.ID); err != nil {
		e.t.Fatal(err)
	}
}

// remove deletes files from disk and rescans, so the library marks them missing.
func (e *env) remove(f library.Folder, rels ...string) {
	e.t.Helper()
	for _, rel := range rels {
		if err := os.Remove(filepath.Join(f.Path, filepath.FromSlash(rel))); err != nil {
			e.t.Fatal(err)
		}
	}
	if _, err := e.lib.ScanNow(bg(), f.ID); err != nil {
		e.t.Fatal(err)
	}
}

type wantKey struct {
	title   int64
	quality string
}

// wanted returns what Wanted lists, keyed by title and quality.
func (e *env) wanted() map[wantKey]grab.Copy {
	e.t.Helper()
	list, err := e.svc.Wanted(bg())
	if err != nil {
		e.t.Fatal(err)
	}
	out := map[wantKey]grab.Copy{}
	for _, c := range list {
		out[wantKey{c.TitleID, c.Quality}] = c
	}
	if len(out) != len(list) {
		e.t.Fatalf("Wanted lists a copy twice: %+v", list)
	}
	return out
}

func TestWantedMovies(t *testing.T) {
	e := newEnv(t)
	movies, movies4k, _, _ := e.standardFolders()

	metro := e.mustAsk(e.owner, movie(19, QualityHD)).Request.Title.ID
	nos := e.mustAsk(e.owner, movie(20, QualityHD)).Request.Title.ID
	e.mustAsk(e.owner, movie(20, QualityUHD))

	// Nothing on disk: every copy is wanted, with its folder.
	got := e.wanted()
	if len(got) != 3 {
		t.Fatalf("Wanted = %v, want 3 copies", got)
	}
	if c := got[wantKey{nos, QualityUHD}]; c.FolderPath != movies4k.Path || c.FolderID != movies4k.ID || c.Kind != KindMovie || !c.Monitored {
		t.Errorf("the 4K copy = %+v", c)
	}
	if c := got[wantKey{metro, QualityHD}]; c.FolderPath != movies.Path {
		t.Errorf("the HD copy = %+v", c)
	}

	// A 1080p file satisfies the HD copy; a 2160p file satisfies the 4K copy
	// and does not satisfy the HD one.
	e.touch(movies, "Metropolis (1927)/Metropolis.1927.1080p.BluRay.x264-GRP.mkv")
	e.touch(movies4k, "Nosferatu (1922)/Nosferatu.1922.2160p.WEB-DL.x265-GRP.mkv")
	got = e.wanted()
	if _, ok := got[wantKey{metro, QualityHD}]; ok {
		t.Error("Metropolis HD is wanted, but a 1080p file is in the library")
	}
	if _, ok := got[wantKey{nos, QualityUHD}]; ok {
		t.Error("Nosferatu 4K is wanted, but a 2160p file is in the library")
	}
	if _, ok := got[wantKey{nos, QualityHD}]; !ok || len(got) != 1 {
		t.Errorf("Wanted = %v, want only Nosferatu HD", got)
	}

	// The other way round: a 2160p file does not make a 1080p copy; no
	// resolution at all counts as HD.
	e.touch(movies, "Nosferatu (1922)/Nosferatu (1922).mkv")
	if got = e.wanted(); len(got) != 0 {
		t.Errorf("Wanted = %v, want nothing", got)
	}

	// A file that goes away makes the copy wanted again.
	e.remove(movies, "Metropolis (1927)/Metropolis.1927.1080p.BluRay.x264-GRP.mkv")
	if got = e.wanted(); len(got) != 1 || got[wantKey{metro, QualityHD}].ID == 0 {
		t.Errorf("after a file went missing: %v, want Metropolis HD", got)
	}

	// Turning the title off in the catalog stops the search; so does turning
	// the copy off.
	if err := e.cat.SetMonitored(bg(), metro, false); err != nil {
		t.Fatal(err)
	}
	if got = e.wanted(); len(got) != 0 {
		t.Errorf("unmonitored title is still wanted: %v", got)
	}
	if err := e.cat.SetMonitored(bg(), metro, true); err != nil {
		t.Fatal(err)
	}
	if _, err := e.db.Exec(`UPDATE copies SET monitored = 0 WHERE title_id = ?`, metro); err != nil {
		t.Fatal(err)
	}
	if got = e.wanted(); len(got) != 0 {
		t.Errorf("unmonitored copy is still wanted: %v", got)
	}
}

func TestWantedFiles(t *testing.T) {
	e := newEnv(t)
	movies, _, _, _ := e.standardFolders()
	metro := e.mustAsk(e.owner, movie(19, QualityHD)).Request.Title.ID

	// Files that are not matched to the title do not count.
	e.touch(movies, "Something Else (1999)/Something.Else.1999.1080p.BluRay.x264-GRP.mkv")
	if got := e.wanted(); len(got) != 1 {
		t.Fatalf("an unmatched file satisfied a copy: %v", got)
	}
	e.touch(movies, "Metropolis (1927)/Metropolis (1927).mkv")
	if got := e.wanted(); len(got) != 0 {
		t.Errorf("Wanted = %v after the file arrived", got)
	}
	// A copy whose title is deleted from the catalog goes with it.
	if err := e.cat.Delete(bg(), metro); err != nil {
		t.Fatal(err)
	}
	if n := e.count(`SELECT COUNT(*) FROM copies`); n != 0 {
		t.Errorf("%d copies left after the title was deleted", n)
	}
}

func TestWantedShows(t *testing.T) {
	e := newEnv(t)
	_, _, shows, _ := e.standardFolders()
	id := e.mustAsk(e.owner, show(99001, QualityHD, Scope{})).Request.Title.ID

	// Pioneer One has S01E01, S01E02 and S02E01 aired and wanted; S02E02 is in
	// the future and the special is not monitored.
	if got := e.wanted(); len(got) != 1 {
		t.Fatalf("Wanted = %v", got)
	}
	e.touch(shows, "Pioneer One (2010)/Season 01/Pioneer One (2010) S01E01.mkv")
	e.touch(shows, "Pioneer One (2010)/Season 01/Pioneer One (2010) S01E02.mkv")
	if got := e.wanted(); got[wantKey{id, QualityHD}].ID == 0 {
		t.Fatal("season 2 is missing, so the show is still wanted")
	}
	e.touch(shows, "Pioneer One (2010)/Season 02/Pioneer One (2010) S02E01.mkv")
	if got := e.wanted(); len(got) != 0 {
		t.Errorf("every aired episode has a file, but Wanted = %v", got)
	}

	// The same show at 4K: the HD files do not count.
	e.mustAsk(e.owner, show(99001, QualityUHD, Scope{Seasons: []int{1}}))
	got := e.wanted()
	if len(got) != 1 || got[wantKey{id, QualityUHD}].ID == 0 {
		t.Fatalf("Wanted = %v, want the 4K copy", got)
	}
	// The 4K ask was for season 1 only, so that is all the 4K copy needs.
	e.touch(shows, "Pioneer One (2010)/Season 01/Pioneer One (2010) S01E01 2160p.mkv")
	if got = e.wanted(); len(got) != 1 {
		t.Fatalf("one of two season 1 episodes: %v", got)
	}
	e.touch(shows, "Pioneer One (2010)/Season 01/Pioneer One (2010) S01E02 2160p.mkv")
	if got = e.wanted(); len(got) != 0 {
		t.Errorf("season 1 is in 4K, but Wanted = %v", got)
	}
}

func TestWantedShowWithoutEpisodesNeedsAFile(t *testing.T) {
	e := newEnv(t)
	_, _, shows, _ := e.standardFolders()
	e.mustAsk(e.owner, show(7, QualityHD, Scope{})) // one aired episode: S01E01
	if got := e.wanted(); len(got) != 1 {
		t.Fatalf("Wanted = %v", got)
	}
	e.touch(shows, "Show 7/Season 01/Show 7 S01E01.mkv")
	if got := e.wanted(); len(got) != 0 {
		t.Errorf("Wanted = %v", got)
	}
}

func TestWantedArtists(t *testing.T) {
	e := newEnv(t)
	_, _, _, music := e.standardFolders()
	art := e.mustAsk(e.owner, AskInput{Kind: KindArtist, Provider: metadata.ProviderMusicBrainz, ProviderID: chopinMBID, Quality: QualityHD}).Request.Title.ID
	if got := e.wanted(); len(got) != 1 || got[wantKey{art, QualityHD}].Kind != KindArtist {
		t.Fatalf("Wanted = %v", got)
	}
	// One album of the two has a file: the other is still missing.
	e.touch(music, "Frédéric Chopin/Nocturnes (2012) [FLAC]/CD1/01 - Nocturne in E-flat major, Op. 9 No. 2.flac")
	if got := e.wanted(); len(got) != 1 {
		t.Errorf("Wanted = %v after one album arrived, want the artist still wanted for the other", got)
	}
	e.touch(music, "Frédéric Chopin/Études (1985) [FLAC]/01 - Etude.flac")
	if got := e.wanted(); len(got) != 0 {
		t.Errorf("Wanted = %v after both albums arrived", got)
	}
}

// artistWith asks for the fake Chopin (two studio albums: Nocturnes, one track;
// Études, none loaded) and returns his title id and the album ids by name.
func (e *env) artistWith() (int64, map[string]int64) {
	e.t.Helper()
	id := e.mustAsk(e.owner, AskInput{Kind: KindArtist, Provider: metadata.ProviderMusicBrainz, ProviderID: chopinMBID, Quality: QualityHD}).Request.Title.ID
	d, err := e.cat.Get(bg(), id)
	if err != nil {
		e.t.Fatal(err)
	}
	albums := map[string]int64{}
	for _, a := range d.Albums {
		albums[a.Name] = a.ID
	}
	return id, albums
}

func TestWantedArtistAlbums(t *testing.T) {
	e := newEnv(t)
	_, _, _, music := e.standardFolders()
	art, albums := e.artistWith()
	if len(albums) != 2 {
		t.Fatalf("albums = %v", albums)
	}

	// An album the owner does not want is not missing: turn Études off.
	if _, err := e.db.Exec(`UPDATE albums SET monitored = 0 WHERE id = ?`, albums["Études"]); err != nil {
		t.Fatal(err)
	}
	if got := e.wanted(); got[wantKey{art, QualityHD}].ID == 0 {
		t.Fatal("Nocturnes is monitored and missing, but the artist is not wanted")
	}

	// A file the scanner could only match to the artist counts for the album its
	// path names, even a deluxe edition of it.
	e.touch(music, "Frédéric Chopin/Nocturnes (Deluxe Edition) (2012) [FLAC]/01 - Anything.flac")
	if got := e.wanted(); len(got) != 0 {
		t.Errorf("Nocturnes has a file, Études is not wanted, but Wanted = %v", got)
	}

	// Turn Études back on: it is missing, so the artist is wanted again.
	if _, err := e.db.Exec(`UPDATE albums SET monitored = 1 WHERE id = ?`, albums["Études"]); err != nil {
		t.Fatal(err)
	}
	if got := e.wanted(); got[wantKey{art, QualityHD}].ID == 0 {
		t.Fatal("Études is monitored and missing, but the artist is not wanted")
	}

	// A file with a track id (what the importer writes) counts through the track.
	var trackID int64
	if err := e.db.QueryRow(`SELECT id FROM tracks WHERE album_id = ? LIMIT 1`, albums["Nocturnes"]).Scan(&trackID); err != nil {
		t.Fatal(err)
	}
	if _, err := e.db.Exec(`INSERT INTO tracks (album_id, disc, position, name) VALUES (?, 1, 1, 'Études one')`, albums["Études"]); err != nil {
		t.Fatal(err)
	}
	var etude int64
	if err := e.db.QueryRow(`SELECT id FROM tracks WHERE album_id = ?`, albums["Études"]).Scan(&etude); err != nil {
		t.Fatal(err)
	}
	e.touch(music, "Unsorted/whatever.flac")
	if err := e.lib.ImportFiles(bg(), music.ID, []library.ImportedFile{{RelPath: "Unsorted/whatever.flac", TitleID: art, TrackID: etude}}); err != nil {
		t.Fatal(err)
	}
	if got := e.wanted(); len(got) != 0 {
		t.Errorf("both albums have files, one through its track id, but Wanted = %v", got)
	}

	// The track's file going away makes the artist wanted again.
	e.remove(music, "Unsorted/whatever.flac")
	if got := e.wanted(); got[wantKey{art, QualityHD}].ID == 0 {
		t.Error("a file went missing, but the artist is not wanted")
	}

	on, err := e.svc.AlbumsOnDisk(bg(), art)
	if err != nil || !on[albums["Nocturnes"]] || on[albums["Études"]] {
		t.Errorf("AlbumsOnDisk = %v, %v; want only Nocturnes", on, err)
	}
}

func TestWantedArtistWithoutAlbumsYetNeedsAFile(t *testing.T) {
	e := newEnv(t)
	_, _, _, music := e.standardFolders()
	art, _ := e.artistWith()
	if _, err := e.db.Exec(`DELETE FROM albums WHERE title_id = ?`, art); err != nil {
		t.Fatal(err)
	}
	if got := e.wanted(); len(got) != 1 {
		t.Fatalf("Wanted = %v", got)
	}
	e.touch(music, "Frédéric Chopin/Nocturnes (2012) [FLAC]/01 - X.flac")
	if got := e.wanted(); len(got) != 0 {
		t.Errorf("an artist with no albums loaded and one file is still wanted: %v", got)
	}
}

func TestSettleArtistFollowsTheAlbums(t *testing.T) {
	e := newEnv(t)
	_, _, _, music := e.standardFolders()
	art, _ := e.artistWith()
	cp, err := e.svc.EnsureCopy(bg(), art, QualityHD)
	if err != nil {
		t.Fatal(err)
	}
	e.touch(music, "Frédéric Chopin/Nocturnes (1998) [FLAC]/01 - Nocturne 1.flac")
	state, err := e.svc.SettleCopy(bg(), cp.ID)
	if err != nil || state != StatePartial {
		t.Errorf("one album of two: SettleCopy = %q, %v; want partial", state, err)
	}
	e.touch(music, "Frédéric Chopin/Études (1985) [FLAC]/01 - Etude.flac")
	state, err = e.svc.SettleCopy(bg(), cp.ID)
	if err != nil || state != StateAvailable {
		t.Errorf("both albums: SettleCopy = %q, %v; want available", state, err)
	}
}

func TestWantedWithNothingApproved(t *testing.T) {
	e := newEnv(t)
	e.standardFolders()
	list, err := e.svc.Wanted(bg())
	if err != nil || list == nil || len(list) != 0 {
		t.Errorf("Wanted = %v, %v; want an empty list", list, err)
	}
	// A waiting ask is not wanted.
	e.mustAsk(e.mia, movie(1, QualityUHD))
	if list, _ = e.svc.Wanted(bg()); len(list) != 0 {
		t.Errorf("a waiting ask is wanted: %v", list)
	}
}

func TestFileQuality(t *testing.T) {
	for label, want := range map[string]string{
		"Bluray-2160p": QualityUHD, "WEBDL-2160p": QualityUHD, "Remux-2160p": QualityUHD, "Unknown-2160p": QualityUHD,
		"Bluray-1080p": QualityHD, "HDTV-720p": QualityHD, "DVD": QualityHD, "Unknown": QualityHD, "": QualityHD, "FLAC": QualityHD,
	} {
		if got := fileQuality(label); got != want {
			t.Errorf("fileQuality(%q) = %q, want %q", label, got, want)
		}
	}
}

func TestCopyNotFound(t *testing.T) {
	e := newEnv(t)
	if _, err := e.svc.Copy(bg(), 1); !errors.Is(err, ErrCopyNotFound) {
		t.Errorf("Copy = %v", err)
	}
}
