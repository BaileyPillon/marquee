package importer

import (
	"errors"
	"net/http"
	"os"
	"path/filepath"
	"reflect"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/BaileyPillon/marquee/internal/core/pathmap"
	"github.com/BaileyPillon/marquee/internal/decide"
	"github.com/BaileyPillon/marquee/internal/downloads"
	"github.com/BaileyPillon/marquee/internal/grab"
	"github.com/BaileyPillon/marquee/internal/library"
	"github.com/BaileyPillon/marquee/internal/requests"
	"github.com/BaileyPillon/marquee/internal/titles"
)

const (
	notldRelease = "Night.of.the.Living.Dead.1968.1080p.BluRay.x264-GRP"
	notldDir     = "Night of the Living Dead (1968)"
	notldBluray  = "Night of the Living Dead (1968) Bluray-1080p.mkv"
)

func epID(t *testing.T, d *titles.Detail, season, number int) int64 {
	t.Helper()
	for _, e := range d.Episodes {
		if e.Season == season && e.Number == number {
			return e.ID
		}
	}
	t.Fatalf("no S%02dE%02d in %s", season, number, d.Name)
	return 0
}

// notld makes the title, its request and copy, and a torrent for it with one
// film file named like the release. It returns what the tests need.
type movieJob struct {
	title   *titles.Detail
	cp      grab.Copy
	reqID   int64
	id      int64  // downloads row
	content string // the folder the client says it saved to
}

func (r *rig) notldJob(release string, files map[string]string, protocol string) movieJob {
	r.t.Helper()
	title := r.movie(notldTMDB)
	cp, reqID := r.ask(title.ID, "hd", requests.Scope{})
	id := r.grabAs(cp, title, release, protocol, nil)
	return movieJob{title: title, cp: cp, reqID: reqID, id: id, content: r.content(release, files)}
}

func (j movieJob) destAbs(r *rig) string {
	return filepath.Join(r.moviesDir, notldDir, notldBluray)
}

// ---- the happy path ------------------------------------------------------------------

func TestImportsAMovieFromATorrent(t *testing.T) {
	r := newRig(t).withImporter()
	r.addJellyfin()
	libEvents, cancel := r.hub.Subscribe(LibraryTopic)
	defer cancel()

	j := r.notldJob(notldRelease, map[string]string{
		notldRelease + ".mkv":        "the film",
		"Sample/sample.mkv":          "a sample",
		notldRelease + "-sample.mkv": "another sample",
		"release.nfo":                "info",
	}, grab.ProtocolTorrent)
	r.complete(j.id, j.content)

	dest := j.destAbs(r)
	if string(mustRead(t, dest)) != "the film" {
		t.Fatalf("the film is not at %s", dest)
	}
	src := filepath.Join(j.content, notldRelease+".mkv")
	a, _ := os.Stat(src)
	b, _ := os.Stat(dest)
	if !os.SameFile(a, b) {
		t.Error("a torrent's file should be hardlinked, so the client keeps seeding")
	}
	entries, _ := os.ReadDir(filepath.Join(r.moviesDir, notldDir))
	if len(entries) != 1 {
		t.Errorf("only the film should be in the library folder, got %d entries", len(entries))
	}

	d := r.download(j.id)
	if d.State != downloads.StateImported || d.Error != "" {
		t.Errorf("download = %s %q, want imported", d.State, d.Error)
	}

	rel := notldDir + "/" + notldBluray
	f, ok := r.libraryFiles(r.moviesFolder)[rel]
	if !ok {
		t.Fatalf("the library has no row for %s: %v", rel, r.libraryFiles(r.moviesFolder))
	}
	if f.State != library.StateMatched || f.TitleID == nil || *f.TitleID != j.title.ID || f.Quality != "Bluray-1080p" {
		t.Errorf("the library row = %+v", f)
	}
	// A scan afterwards finds nothing to change: the row was already right.
	sum, err := r.lib.ScanNow(bg(), r.moviesFolder.ID)
	if err != nil {
		t.Fatal(err)
	}
	if sum.Added != 0 || sum.Updated != 0 || sum.Unchanged != 1 || sum.Missing != 0 {
		t.Errorf("scan after import = %+v", sum)
	}
	if f := r.libraryFiles(r.moviesFolder)[rel]; f.State != library.StateMatched || f.TitleID == nil || *f.TitleID != j.title.ID {
		t.Errorf("after a scan the row = %+v", f)
	}

	if got := r.requestState(j.reqID); got != requests.StateAvailable {
		t.Errorf("request state = %s, want available", got)
	}
	if got := r.historyEvents(j.title.ID); !reflect.DeepEqual(got, []string{"requested", "approved", "imported"}) {
		t.Errorf("history = %v", got)
	}
	detail := r.historyItem(j.title.ID, "imported")
	if detail["release"] != notldRelease {
		t.Errorf("imported detail = %v", detail)
	}

	ups := r.jelly.updates(t)
	if len(ups) != 1 || ups[0]["UpdateType"] != "Created" ||
		ups[0]["Path"] != "/media/movies/"+notldDir+"/"+notldBluray {
		t.Errorf("Jellyfin was told %v", ups)
	}
	var refreshed []string
	for _, c := range r.jelly.all() {
		if c.token != "jellyfin-api-key-0123456789" {
			t.Errorf("a call to Jellyfin carried token %q", c.token)
		}
		if strings.HasSuffix(c.path, "/Refresh") {
			refreshed = append(refreshed, c.path)
		}
	}
	if !reflect.DeepEqual(refreshed, []string{"/Items/lib1/Refresh"}) {
		t.Errorf("the Movies library should have been asked to rescan: %v", refreshed)
	}

	select {
	case m := <-libEvents.C():
		if !strings.Contains(string(m.Data), `"imported"`) || !strings.Contains(string(m.Data), `"files":1`) {
			t.Errorf("library event = %s", m.Data)
		}
	case <-time.After(2 * time.Second):
		t.Error("no library event was published")
	}

	recs, err := r.imp.Status(bg(), 20)
	if err != nil || len(recs) != 1 {
		t.Fatalf("status = %v, %v", recs, err)
	}
	if rec := recs[0]; rec.Outcome != OutcomeImported || rec.FilesImported != 1 || rec.Title == nil || rec.Title.Name != "Night of the Living Dead" || rec.Manual {
		t.Errorf("status record = %+v", rec)
	}
	r.srch.noSearch(t)
}

func TestVerificationReadsTheFolderWhenTheFileNameSaysNothing(t *testing.T) {
	r := newRig(t).withImporter()
	j := r.notldJob(notldRelease, map[string]string{"movie.mkv": "the film", "a1b2c3d4e5f60718.mkv": "an obfuscated one"}, grab.ProtocolTorrent)
	r.complete(j.id, j.content)
	// Both files are the same film in the same quality: the bigger one wins, the other is noted.
	if string(mustRead(t, j.destAbs(r))) != "an obfuscated one" {
		t.Fatalf("expected the bigger file at %s", j.destAbs(r))
	}
	if st := r.download(j.id).State; st != downloads.StateImported {
		t.Errorf("state = %s", st)
	}
}

func TestSingleFileDownloadUsesTheReleaseNameForQuality(t *testing.T) {
	r := newRig(t).withImporter()
	title := r.movie(notldTMDB)
	cp, _ := r.ask(title.ID, "hd", requests.Scope{})
	id := r.grabAs(cp, title, notldRelease, grab.ProtocolTorrent, nil)
	file := filepath.Join(r.dlDir, "video.mkv") // a loose file with a useless name, saved straight into the downloads folder
	writeFile(t, file, []byte("the film"))
	r.complete(id, file)
	if string(mustRead(t, filepath.Join(r.moviesDir, notldDir, notldBluray))) != "the film" {
		t.Fatalf("the film was not imported: %v", r.download(id))
	}
}

// ---- torrents and Usenet ----------------------------------------------------------------------

func TestUsenetIsMovedAndTheFolderTidied(t *testing.T) {
	r := newRig(t).withImporter()
	j := r.notldJob(notldRelease, map[string]string{
		notldRelease + ".mkv": "usenet film",
		"release.nfo":         "info",
		"poster.jpg":          "jpg",
	}, grab.ProtocolUsenet)
	r.complete(j.id, j.content)

	if string(mustRead(t, j.destAbs(r))) != "usenet film" {
		t.Fatal("the film is not in the library")
	}
	if exists(filepath.Join(j.content, notldRelease+".mkv")) {
		t.Error("a Usenet download is moved: the original should be gone")
	}
	if exists(j.content) {
		t.Error("the finished Usenet folder (inside the client's save folder) should be cleaned up")
	}
	if st := r.download(j.id).State; st != downloads.StateImported {
		t.Errorf("state = %s", st)
	}
}

func TestUsenetFolderIsKeptWhenSomethingWasLeftOut(t *testing.T) {
	r := newRig(t).withImporter()
	j := r.notldJob(notldRelease, map[string]string{
		notldRelease + ".mkv":                 "usenet film",
		"Unrelated.Film.2001.720p.WEB-DL.mkv": "something else",
	}, grab.ProtocolUsenet)
	r.complete(j.id, j.content)
	if string(mustRead(t, j.destAbs(r))) != "usenet film" {
		t.Fatal("the film is not in the library")
	}
	if !exists(filepath.Join(j.content, "Unrelated.Film.2001.720p.WEB-DL.mkv")) {
		t.Error("a file that was not imported must not be deleted with the folder")
	}
}

func TestATorrentIsNeverMovedEvenWhenLinkingFails(t *testing.T) {
	old := linkFile
	linkFile = func(string, string) error { return errors.New("cross-device link") }
	t.Cleanup(func() { linkFile = old })

	r := newRig(t).withImporter()
	j := r.notldJob(notldRelease, map[string]string{notldRelease + ".mkv": "the film"}, grab.ProtocolTorrent)
	r.complete(j.id, j.content)

	src := filepath.Join(j.content, notldRelease+".mkv")
	if string(mustRead(t, src)) != "the film" {
		t.Fatal("the torrent's file must still be there")
	}
	a, _ := os.Stat(src)
	b, err := os.Stat(j.destAbs(r))
	if err != nil {
		t.Fatal(err)
	}
	if os.SameFile(a, b) {
		t.Error("with the link failing the file should have been copied")
	}
	if st := r.download(j.id).State; st != downloads.StateImported {
		t.Errorf("state = %s", st)
	}
	d := r.historyItem(j.title.ID, "imported")
	files, _ := d["files"].([]any)
	if len(files) != 1 || files[0].(map[string]any)["mode"] != ModeCopy {
		t.Errorf("history should say the file was copied: %v", d)
	}
}

// ---- a bad release ------------------------------------------------------------------------------

func TestWrongFilmIsBlocklistedAndTheCopyIsSearchedAgain(t *testing.T) {
	r := newRig(t).withImporter()
	j := r.notldJob("The.Matrix.1999.1080p.BluRay.x264-GRP", map[string]string{
		"The.Matrix.1999.1080p.BluRay.x264-GRP.mkv": "not the film we wanted",
	}, grab.ProtocolTorrent)
	r.complete(j.id, j.content)

	d := r.download(j.id)
	if d.State != downloads.StateFailed || !strings.HasPrefix(d.Error, FailPrefix) || !strings.Contains(d.Error, "The Matrix") {
		t.Fatalf("download = %s %q", d.State, d.Error)
	}
	if entries, _ := os.ReadDir(r.moviesDir); len(entries) != 0 {
		t.Errorf("nothing should have been imported, got %v", entries)
	}

	bl := r.blocklist()
	if len(bl) != 1 {
		t.Fatalf("blocklist = %+v", bl)
	}
	if b := bl[0]; b.TitleID != j.title.ID || b.GUID != "guid-The.Matrix.1999.1080p.BluRay.x264-GRP" ||
		b.InfoHash != d.ClientRef || b.ReleaseTitle != d.ReleaseTitle || !strings.HasPrefix(b.Reason, "Import failed: ") {
		t.Errorf("blocklist entry = %+v (client ref %s)", b, d.ClientRef)
	}
	if len(r.qbt.removals()) != 0 {
		t.Errorf("a wrong film is blocklisted, not deleted from the client: %v", r.qbt.removals())
	}

	if got := r.srch.waitSearch(t); got != j.cp.ID {
		t.Errorf("searched copy %d, want %d", got, j.cp.ID)
	}
	if fg := r.srch.forgets(); len(fg) != 1 || fg[0].copyID != j.cp.ID || !reflect.DeepEqual(fg[0].keys, []string{"movie"}) {
		t.Errorf("the search should forget the movie grab: %v", fg)
	}
	if got := r.requestState(j.reqID); got != requests.StateApproved {
		t.Errorf("request state = %s; a failed import leaves it approved while the search goes on", got)
	}
	h := r.historyItem(j.title.ID, "failed")
	if h["blocklisted"] != true || h["searchingAgain"] != true {
		t.Errorf("failed detail = %v", h)
	}
	recs, _ := r.imp.Status(bg(), 5)
	if len(recs) != 1 || recs[0].Outcome != OutcomeFailed || recs[0].Reason == "" {
		t.Errorf("status = %+v", recs)
	}
}

func TestFailuresCloseTogetherShareOneSearch(t *testing.T) {
	r := newRig(t).withImporter()
	r.imp.searchDelay = 600 * time.Millisecond
	title := r.movie(notldTMDB)
	cp, _ := r.ask(title.ID, "hd", requests.Scope{})
	for i, name := range []string{"The.Matrix.1999.1080p.BluRay.x264-AAA", "The.Matrix.1999.720p.BluRay.x264-BBB", "Wrong.Film.2001.720p.WEB-DL-CCC"} {
		id := r.grabAs(cp, title, name, grab.ProtocolTorrent, nil)
		r.complete(id, r.content(name, map[string]string{name + ".mkv": "x"}))
		if st := r.download(id).State; st != downloads.StateFailed {
			t.Fatalf("download %d state = %s", i, st)
		}
	}
	if len(r.blocklist()) != 3 {
		t.Errorf("each bad release should be blocklisted: %v", r.blocklist())
	}
	r.srch.waitSearch(t)
	r.srch.noSearch(t) // three failures, one search
}

func TestSearchAgainIsCappedPerDay(t *testing.T) {
	r := newRig(t).withImporter()
	r.imp.searchDelay = time.Millisecond
	for i := 0; i < maxSearchesPerDay; i++ {
		if !r.imp.scheduleSearch(7) {
			t.Fatalf("search %d should have been scheduled", i+1)
		}
		r.srch.waitSearch(t)
		time.Sleep(20 * time.Millisecond) // let the timer entry go
	}
	if r.imp.scheduleSearch(7) {
		t.Error("after a day's worth of searches the importer must stop searching this copy again")
	}
	if !r.imp.scheduleSearch(8) {
		t.Error("another copy is not affected")
	}
}

func TestADownloadWithNoUsableMediaIsRemovedFromTheClient(t *testing.T) {
	r := newRig(t).withImporter()
	j := r.notldJob(notldRelease, map[string]string{
		"Sample/" + notldRelease + "-sample.mkv": "tiny",
		"readme.txt":                             "go to example.com",
		"play.exe":                               "MZ",
	}, grab.ProtocolTorrent)
	r.complete(j.id, j.content)

	d := r.download(j.id)
	if d.State != downloads.StateFailed || !strings.Contains(d.Error, "No usable media") {
		t.Fatalf("download = %s %q", d.State, d.Error)
	}
	rm := r.qbt.removals()
	if len(rm) != 1 || !rm[0].deleteData || rm[0].ref != d.ClientRef {
		t.Errorf("a fake should be removed from the client with its data, got %v", rm)
	}
	if bl := r.blocklist(); len(bl) != 1 || !strings.Contains(bl[0].Reason, "No usable media") {
		t.Errorf("blocklist = %+v", bl)
	}
	r.srch.waitSearch(t)
	h := r.historyItem(j.title.ID, "failed")
	if h["removedFromClient"] != true {
		t.Errorf("failed detail = %v", h)
	}
}

func TestEpisodeThatWasNotAskedForIsAMismatch(t *testing.T) {
	r := newRig(t).withImporter()
	show := r.show(pioneerTVM)
	cp, _ := r.ask(show.ID, "hd", requests.Scope{Seasons: []int{1}})
	name := "Pioneer.One.S01E02.1080p.BluRay.x264-GRP"
	id := r.grabAs(cp, show, name, grab.ProtocolTorrent, []int64{epID(t, show, 1, 1)}) // asked for E01
	r.complete(id, r.content(name, map[string]string{name + ".mkv": "x"}))

	d := r.download(id)
	if d.State != downloads.StateFailed || !strings.Contains(d.Error, "not one of the episodes asked for") {
		t.Fatalf("download = %s %q", d.State, d.Error)
	}
	if fg := r.srch.forgets(); len(fg) != 1 || !reflect.DeepEqual(fg[0].keys, []string{"s01e01"}) {
		t.Errorf("the search should forget the episode the release was for: %v", fg)
	}
}

func TestBrokenArchiveBlocklistsTheRelease(t *testing.T) {
	r := newRig(t).withImporter()
	j := r.notldJob(notldRelease, map[string]string{notldRelease + ".zip": "not really a zip"}, grab.ProtocolTorrent)
	r.complete(j.id, j.content)
	d := r.download(j.id)
	if d.State != downloads.StateFailed || !strings.Contains(d.Error, "could not be unpacked") {
		t.Fatalf("download = %s %q", d.State, d.Error)
	}
	if len(r.blocklist()) != 1 {
		t.Error("the release should be blocklisted")
	}
	if exists(filepath.Join(r.dlDir, ".marquee-extract-"+itoa(j.id))) {
		t.Error("the unpack folder should be removed after a failure too")
	}
}

func itoa(n int64) string { return strconv.FormatInt(n, 10) }

// ---- archives -------------------------------------------------------------------------------------

func TestImportUnpacksArchives(t *testing.T) {
	cases := map[string]func(t *testing.T, dir string){
		"zip": func(t *testing.T, dir string) {
			makeZip(t, filepath.Join(dir, "release.zip"), map[string]string{
				fixtureMKV: fixtureData, "Sample/sample.mkv": "sample", "nfo/info.nfo": "x"})
		},
		"7z":  func(t *testing.T, dir string) { writeB64(t, filepath.Join(dir, "release.7z"), fixture7z) },
		"rar": func(t *testing.T, dir string) { writeB64(t, filepath.Join(dir, "release.rar"), fixtureRar5) },
	}
	for name, build := range cases {
		t.Run(name, func(t *testing.T) {
			r := newRig(t).withImporter()
			title := r.movie(notldTMDB)
			cp, reqID := r.ask(title.ID, "hd", requests.Scope{})
			id := r.grabAs(cp, title, notldRelease, grab.ProtocolTorrent, nil)
			content := filepath.Join(r.dlDir, notldRelease)
			build(t, content)
			r.complete(id, content)

			dest := filepath.Join(r.moviesDir, notldDir, notldBluray)
			if string(mustRead(t, dest)) != fixtureData {
				t.Fatalf("the unpacked film is not in the library (download: %+v)", r.download(id))
			}
			if st := r.download(id).State; st != downloads.StateImported {
				t.Errorf("state = %s", st)
			}
			if exists(filepath.Join(r.dlDir, ".marquee-extract-"+itoa(id))) {
				t.Error("the unpack folder next to the download should be removed")
			}
			var archive string
			for _, n := range []string{"release.zip", "release.7z", "release.rar"} {
				if exists(filepath.Join(content, n)) {
					archive = n
				}
			}
			if archive == "" {
				t.Error("the archive itself belongs to the torrent and must stay")
			}
			if got := r.requestState(reqID); got != requests.StateAvailable {
				t.Errorf("request state = %s", got)
			}
		})
	}
}

// ---- upgrades ------------------------------------------------------------------------------------------

// seed puts a film file in the library and records it, as a scan would have.
func (r *rig) seed(f library.Folder, rel string, titleID int64, quality string, episodes ...int64) string {
	r.t.Helper()
	p := filepath.Join(f.Path, filepath.FromSlash(rel))
	writeFile(r.t, p, []byte("the old file"))
	if err := r.lib.ImportFiles(bg(), f.ID, []library.ImportedFile{{RelPath: rel, TitleID: titleID, EpisodeIDs: episodes, Quality: quality}}); err != nil {
		r.t.Fatal(err)
	}
	return p
}

func TestABetterReleaseReplacesAWorseFile(t *testing.T) {
	r := newRig(t).withImporter()
	r.addJellyfin()
	title := r.movie(notldTMDB)
	cp, reqID := r.ask(title.ID, "hd", requests.Scope{})
	oldRel := notldDir + "/Night of the Living Dead (1968) WEBDL-720p.mkv"
	oldAbs := r.seed(r.moviesFolder, oldRel, title.ID, "WEBDL-720p")

	id := r.grabAs(cp, title, notldRelease, grab.ProtocolTorrent, nil)
	r.complete(id, r.content(notldRelease, map[string]string{notldRelease + ".mkv": "the better one"}))

	if exists(oldAbs) {
		t.Error("the worse file should have been removed")
	}
	if string(mustRead(t, filepath.Join(r.moviesDir, notldDir, notldBluray))) != "the better one" {
		t.Fatal("the better file is not in the library")
	}
	files := r.libraryFiles(r.moviesFolder)
	if len(files) != 1 {
		t.Errorf("the library should hold one file now: %v", files)
	}
	if _, old := files[oldRel]; old {
		t.Error("the old file's row should be gone")
	}
	if got := r.historyEvents(title.ID); !reflect.DeepEqual(got, []string{"requested", "approved", "upgraded"}) {
		t.Errorf("history = %v", got)
	}
	up := r.historyItem(title.ID, "upgraded")
	if up["from"] != "WEBDL-720p" || up["to"] != "Bluray-1080p" || up["replaced"] != oldRel {
		t.Errorf("upgraded detail = %v", up)
	}
	ups := r.jelly.updates(t)
	if len(ups) != 2 || ups[0]["UpdateType"] != "Created" || ups[1]["UpdateType"] != "Deleted" ||
		!strings.HasSuffix(ups[1]["Path"], "WEBDL-720p.mkv") {
		t.Errorf("Jellyfin should hear about the new file and the removed one: %v", ups)
	}
	if got := r.requestState(reqID); got != requests.StateAvailable {
		t.Errorf("request state = %s", got)
	}
}

func TestAWorseOrEqualReleaseIsLeftOut(t *testing.T) {
	r := newRig(t).withImporter()
	title := r.movie(notldTMDB)
	cp, _ := r.ask(title.ID, "hd", requests.Scope{})
	oldRel := notldDir + "/" + notldBluray
	oldAbs := r.seed(r.moviesFolder, oldRel, title.ID, "Bluray-1080p")

	name := "Night.of.the.Living.Dead.1968.720p.WEB-DL.x264-GRP"
	id := r.grabAs(cp, title, name, grab.ProtocolTorrent, nil)
	r.complete(id, r.content(name, map[string]string{name + ".mkv": "worse"}))

	if string(mustRead(t, oldAbs)) != "the old file" {
		t.Error("the better file must stay as it is")
	}
	if exists(filepath.Join(r.moviesDir, notldDir, "Night of the Living Dead (1968) WEBDL-720p.mkv")) {
		t.Error("the worse file must not be added")
	}
	d := r.download(id)
	if d.State != downloads.StateImported || d.Error != "" {
		t.Errorf("a download the library does not need is still done: %s %q", d.State, d.Error)
	}
	recs, _ := r.imp.Status(bg(), 5)
	if len(recs) != 1 || recs[0].Outcome != OutcomeSkipped || recs[0].FilesImported != 0 || recs[0].FilesSkipped != 1 {
		t.Errorf("status = %+v", recs)
	}
	if len(r.blocklist()) != 0 {
		t.Error("nothing is wrong with the release, so it must not be blocklisted")
	}
	r.srch.noSearch(t)
	if got := r.historyEvents(title.ID); !reflect.DeepEqual(got, []string{"requested", "approved"}) {
		t.Errorf("history = %v", got)
	}
}

func TestUpgradeStopsAtTheProfilesLimit(t *testing.T) {
	r := newRig(t).withImporter()
	title := r.movie(notldTMDB)
	cp, _ := r.ask(title.ID, "hd", requests.Scope{})
	prof, err := r.dec.Profile(bg(), "movie", "hd")
	if err != nil {
		t.Fatal(err)
	}
	in := decide.InputOf(prof)
	limit := "WEBDL-1080p"
	in.UpgradeUntil = &limit
	if _, err := r.dec.UpdateProfile(bg(), prof.ID, in); err != nil {
		t.Fatal(err)
	}
	oldRel := notldDir + "/Night of the Living Dead (1968) WEBDL-1080p.mkv"
	oldAbs := r.seed(r.moviesFolder, oldRel, title.ID, "WEBDL-1080p")

	id := r.grabAs(cp, title, notldRelease, grab.ProtocolTorrent, nil) // a BluRay: better, but the library is at the limit
	r.complete(id, r.content(notldRelease, map[string]string{notldRelease + ".mkv": "better"}))
	if string(mustRead(t, oldAbs)) != "the old file" {
		t.Error("a file that meets the upgrade limit must not be replaced")
	}
	if exists(filepath.Join(r.moviesDir, notldDir, notldBluray)) {
		t.Error("the better file must not be added past the limit")
	}
}

func TestIdenticalFileAlreadyInPlaceIsAdopted(t *testing.T) {
	r := newRig(t).withImporter()
	j := r.notldJob(notldRelease, map[string]string{notldRelease + ".mkv": "the film"}, grab.ProtocolTorrent)
	// An earlier run put the file there and stopped before recording it.
	writeFile(t, j.destAbs(r), []byte("the film"))
	r.complete(j.id, j.content)

	if st := r.download(j.id).State; st != downloads.StateImported {
		t.Fatalf("state = %s", st)
	}
	if _, ok := r.libraryFiles(r.moviesFolder)[notldDir+"/"+notldBluray]; !ok {
		t.Error("the file that was already there should now be recorded in the library")
	}
	if got := r.requestState(j.reqID); got != requests.StateAvailable {
		t.Errorf("request state = %s", got)
	}
}

// ---- shows -----------------------------------------------------------------------------------------------

func TestShowEpisodesMoveTheRequestFromPartialToAvailable(t *testing.T) {
	r := newRig(t).withImporter()
	show := r.show(pioneerTVM)
	cp, reqID := r.ask(show.ID, "hd", requests.Scope{All: true})
	e11, e12, e21 := epID(t, show, 1, 1), epID(t, show, 1, 2), epID(t, show, 2, 1)

	one := func(name string, eps []int64, wantState string) {
		t.Helper()
		id := r.grabAs(cp, show, name, grab.ProtocolTorrent, eps)
		r.complete(id, r.content(name, map[string]string{name + ".mkv": name}))
		if d := r.download(id); d.State != downloads.StateImported {
			t.Fatalf("%s: %s %q", name, d.State, d.Error)
		}
		if got := r.requestState(reqID); got != wantState {
			t.Fatalf("after %s the request is %s, want %s", name, got, wantState)
		}
	}
	one("Pioneer.One.S01E01.1080p.BluRay.x264-GRP", []int64{e11}, requests.StatePartial)
	dest := filepath.Join(r.showsDir, "Pioneer One (2010)", "Season 01", "Pioneer One - S01E01 - Earthfall Bluray-1080p.mkv")
	if string(mustRead(t, dest)) != "Pioneer.One.S01E01.1080p.BluRay.x264-GRP" {
		t.Fatalf("S01E01 is not at %s", dest)
	}
	f := r.libraryFiles(r.showsFolder)["Pioneer One (2010)/Season 01/Pioneer One - S01E01 - Earthfall Bluray-1080p.mkv"]
	if f.State != library.StateMatched || !reflect.DeepEqual(f.EpisodeIDs, []int64{e11}) {
		t.Errorf("S01E01 row = %+v", f)
	}
	one("Pioneer.One.S01E02.1080p.BluRay.x264-GRP", []int64{e12}, requests.StatePartial) // season 2 is still missing
	one("Pioneer.One.S02E01.1080p.BluRay.x264-GRP", []int64{e21}, requests.StateAvailable)

	// A scan agrees with what the importer recorded.
	if _, err := r.lib.ScanNow(bg(), r.showsFolder.ID); err != nil {
		t.Fatal(err)
	}
	got := map[string][]int64{}
	for rel, f := range r.libraryFiles(r.showsFolder) {
		if f.State != library.StateMatched {
			t.Errorf("%s is %s after a scan", rel, f.State)
		}
		got[filepath.Base(rel)[:len("Pioneer One - S01E01")]] = f.EpisodeIDs
	}
	if len(got) != 3 {
		t.Errorf("rows after a scan: %v", got)
	}
}

func TestDoubleEpisodeFileAndSpecials(t *testing.T) {
	r := newRig(t).withImporter()
	show := r.show(pioneerTVM)
	cp, reqID := r.ask(show.ID, "hd", requests.Scope{Seasons: []int{1}})

	name := "Pioneer.One.S01E01E02.720p.WEB-DL.x264-GRP"
	id := r.grabAs(cp, show, name, grab.ProtocolTorrent, []int64{epID(t, show, 1, 1), epID(t, show, 1, 2)})
	r.complete(id, r.content(name, map[string]string{name + ".mkv": "both"}))
	rel := "Pioneer One (2010)/Season 01/Pioneer One - S01E01-E02 - Earthfall + Anomalies WEBDL-720p.mkv"
	f, ok := r.libraryFiles(r.showsFolder)[rel]
	if !ok {
		t.Fatalf("double episode not found: %v", r.libraryFiles(r.showsFolder))
	}
	if !reflect.DeepEqual(f.EpisodeIDs, []int64{epID(t, show, 1, 1), epID(t, show, 1, 2)}) {
		t.Errorf("episode ids = %v", f.EpisodeIDs)
	}
	if got := r.requestState(reqID); got != requests.StateAvailable {
		t.Errorf("both episodes of the season came in one file, so the request is %s", got)
	}

	// A special goes to season 00.
	sp := "Pioneer.One.S00E01.720p.HDTV.x264-GRP"
	id = r.grabAs(cp, show, sp, grab.ProtocolTorrent, nil)
	r.complete(id, r.content(sp, map[string]string{sp + ".mkv": "special"}))
	if string(mustRead(t, filepath.Join(r.showsDir, "Pioneer One (2010)", "Season 00", "Pioneer One - S00E01 - Behind the Scenes HDTV-720p.mkv"))) != "special" {
		t.Errorf("the special is not in Season 00: %v", r.libraryFiles(r.showsFolder))
	}
}

func TestSeasonPackImportsEveryEpisodeAndNotesTheOnesItCannotPlace(t *testing.T) {
	r := newRig(t).withImporter()
	show := r.show(pioneerTVM)
	cp, reqID := r.ask(show.ID, "hd", requests.Scope{Seasons: []int{1}})
	name := "Pioneer.One.S01.1080p.BluRay.x264-GRP"
	id := r.grabAs(cp, show, name, grab.ProtocolTorrent, nil)
	r.complete(id, r.content(name, map[string]string{
		"Pioneer.One.S01E01.1080p.BluRay.x264-GRP.mkv": "e1",
		"Pioneer.One.S01E02.1080p.BluRay.x264-GRP.mkv": "e2",
		"Pioneer.One.S01E09.1080p.BluRay.x264-GRP.mkv": "no such episode",
		"Sample/pioneer.one.s01e01.sample.mkv":         "sample",
	}))
	if d := r.download(id); d.State != downloads.StateImported {
		t.Fatalf("state = %s %q", d.State, d.Error)
	}
	if n := len(r.libraryFiles(r.showsFolder)); n != 2 {
		t.Errorf("two episodes should be in the library, got %d", n)
	}
	recs, _ := r.imp.Status(bg(), 5)
	if len(recs) != 1 || recs[0].FilesImported != 2 || recs[0].FilesSkipped != 1 || !strings.Contains(string(recs[0].Detail), "S01E09 is not in the catalog") {
		t.Errorf("status = %+v (%s)", recs, recs[0].Detail)
	}
	if got := r.requestState(reqID); got != requests.StateAvailable {
		t.Errorf("request state = %s", got)
	}
	if len(r.blocklist()) != 0 {
		t.Error("a pack with some good episodes is not blocklisted")
	}
}

// ---- music --------------------------------------------------------------------------------------------------

func TestImportsAnAlbum(t *testing.T) {
	r := newRig(t).withImporter()
	artist := r.artist(chopinMBID)
	cp, reqID := r.ask(artist.ID, "hd", requests.Scope{All: true})
	name := "Frédéric Chopin - Nocturnes (1998) [FLAC]"
	id := r.grabAs(cp, artist, name, grab.ProtocolTorrent, nil)
	r.complete(id, r.content(name, map[string]string{
		"01 - Nocturne in B-flat minor, Op. 9 No. 1.flac": "one",
		"02 - Nocturne in E-flat major, Op. 9 No. 2.flac": "two",
		"cover.jpg": "jpg",
	}))
	if d := r.download(id); d.State != downloads.StateImported {
		t.Fatalf("state = %s %q", d.State, d.Error)
	}
	want := "Frédéric Chopin/Nocturnes (1998)/01 - Nocturne in B-flat minor, Op. 9 No. 1.flac"
	if string(mustRead(t, filepath.Join(r.musicDir, filepath.FromSlash(want)))) != "one" {
		t.Fatalf("track 1 is not at %s", want)
	}
	if _, ok := r.libraryFiles(library.Folder{ID: 3})[want]; !ok {
		t.Errorf("the library has no row for the track: %v", r.libraryFiles(library.Folder{ID: 3}))
	}
	if got := r.requestState(reqID); got != requests.StateAvailable {
		t.Errorf("request state = %s", got)
	}
}

// ---- finding the download ------------------------------------------------------------------------------------

func TestClientPathRulesTranslateTheContentPath(t *testing.T) {
	r := newRig(t).withImporter()
	rules := []pathmap.Rule{{From: "/downloads", To: r.dlDir}}
	if _, err := r.dl.UpdateClient(bg(), r.qbtView.ID, downloads.ClientInput{PathMap: &rules}); err != nil {
		t.Fatal(err)
	}
	j := r.notldJob(notldRelease, map[string]string{notldRelease + ".mkv": "the film"}, grab.ProtocolTorrent)
	d := r.download(j.id)
	r.qbt.finish(d.ClientRef, "/downloads/"+notldRelease, "/downloads") // as the client in its container sees it
	if err := r.dl.Poll(bg()); err != nil {
		t.Fatal(err)
	}
	r.imp.Wait()
	if st := r.download(j.id); st.State != downloads.StateImported {
		t.Fatalf("state = %s %q", st.State, st.Error)
	}
	if string(mustRead(t, j.destAbs(r))) != "the film" {
		t.Error("the film is not in the library")
	}
}

func TestMissingDownloadSaysToAddAPathRuleAndIsNotBlocklisted(t *testing.T) {
	r := newRig(t).withImporter()
	j := r.notldJob(notldRelease, map[string]string{notldRelease + ".mkv": "the film"}, grab.ProtocolTorrent)
	d := r.download(j.id)
	r.qbt.finish(d.ClientRef, "/data/torrents/"+notldRelease, "/data/torrents") // a path Marquee cannot see
	if err := r.dl.Poll(bg()); err != nil {
		t.Fatal(err)
	}
	r.imp.Wait()
	d = r.download(j.id)
	if d.State != downloads.StateFailed || !strings.Contains(d.Error, "path rule") {
		t.Fatalf("download = %s %q", d.State, d.Error)
	}
	if len(r.blocklist()) != 0 || len(r.qbt.removals()) != 0 {
		t.Error("a path problem is not the release's fault: nothing is blocklisted or removed")
	}
	r.srch.noSearch(t)
}

func TestDownloadInsideTheLibraryIsRefused(t *testing.T) {
	r := newRig(t).withImporter()
	j := r.notldJob(notldRelease, map[string]string{notldRelease + ".mkv": "the film"}, grab.ProtocolTorrent)
	inside := filepath.Join(r.moviesDir, "already-here")
	writeFile(t, filepath.Join(inside, notldRelease+".mkv"), []byte("x"))
	r.finish(j.id, inside)
	r.imp.Wait()
	d := r.download(j.id)
	if d.State != downloads.StateFailed || !strings.Contains(d.Error, "overlap") {
		t.Fatalf("download = %s %q", d.State, d.Error)
	}
}

func TestCopyWithoutAFolderFailsWithoutBlame(t *testing.T) {
	r := newRig(t).withImporter()
	j := r.notldJob(notldRelease, map[string]string{notldRelease + ".mkv": "the film"}, grab.ProtocolTorrent)
	if err := r.lib.RemoveFolder(bg(), r.moviesFolder.ID); err != nil {
		t.Fatal(err)
	}
	r.complete(j.id, j.content)
	d := r.download(j.id)
	if d.State != downloads.StateFailed || !strings.Contains(d.Error, "no library folder") {
		t.Fatalf("download = %s %q", d.State, d.Error)
	}
	if len(r.blocklist()) != 0 {
		t.Error("a missing folder is not the release's fault")
	}
}

// ---- the job and retries ------------------------------------------------------------------------------------------

func TestPendingPicksUpDownloadsThatFinishedBeforeTheHandlerWasSet(t *testing.T) {
	r := newRig(t) // no importer yet: the poll marks the download completed and tells no one
	j := r.notldJob(notldRelease, map[string]string{notldRelease + ".mkv": "the film"}, grab.ProtocolTorrent)
	r.finish(j.id, j.content)
	if st := r.download(j.id).State; st != downloads.StateCompleted {
		t.Fatalf("state = %s, want completed and waiting", st)
	}

	r.withImporter()
	if err := r.imp.Pending(bg()); err != nil {
		t.Fatal(err)
	}
	r.imp.Wait()
	if st := r.download(j.id); st.State != downloads.StateImported {
		t.Fatalf("state = %s %q", st.State, st.Error)
	}
	if string(mustRead(t, j.destAbs(r))) != "the film" {
		t.Error("the film is not in the library")
	}
	// A second pass finds nothing to do.
	if err := r.imp.Pending(bg()); err != nil {
		t.Fatal(err)
	}
	r.imp.Wait()
	if recs, _ := r.imp.Status(bg(), 10); len(recs) != 1 {
		t.Errorf("one import expected, got %d", len(recs))
	}
}

func TestPendingRetriesAFailedImportOnceAndOnlyAfterAWhile(t *testing.T) {
	r := newRig(t).withImporter()
	j := r.notldJob(notldRelease, map[string]string{notldRelease + ".mkv": "the film"}, grab.ProtocolTorrent)
	late := filepath.Join(r.dlDir, "not-there-yet")
	r.finish(j.id, late)
	r.imp.Wait()
	if st := r.download(j.id).State; st != downloads.StateFailed {
		t.Fatalf("state = %s", st)
	}

	if err := r.imp.Pending(bg()); err != nil { // too soon
		t.Fatal(err)
	}
	r.imp.Wait()
	if recs, _ := r.imp.Status(bg(), 10); len(recs) != 1 {
		t.Fatalf("a failure should not be retried at once, got %d imports", len(recs))
	}

	// The files turn up (the share was mounted late) and the job tries again.
	r.advance(5 * time.Minute)
	writeFile(t, filepath.Join(late, notldRelease+".mkv"), []byte("the film"))
	if err := r.imp.Pending(bg()); err != nil {
		t.Fatal(err)
	}
	r.imp.Wait()
	if st := r.download(j.id); st.State != downloads.StateImported {
		t.Fatalf("after the retry the state is %s %q", st.State, st.Error)
	}
	recs, _ := r.imp.Status(bg(), 10)
	if len(recs) != 2 || recs[0].Outcome != OutcomeImported || recs[1].Outcome != OutcomeFailed {
		t.Errorf("status = %+v", recs)
	}
}

func TestPendingRetriesOnlyOnce(t *testing.T) {
	r := newRig(t).withImporter()
	j := r.notldJob(notldRelease, map[string]string{notldRelease + ".mkv": "the film"}, grab.ProtocolTorrent)
	r.finish(j.id, filepath.Join(r.dlDir, "never-there"))
	r.imp.Wait()
	for i := 0; i < 3; i++ {
		r.advance(5 * time.Minute)
		if err := r.imp.Pending(bg()); err != nil {
			t.Fatal(err)
		}
		r.imp.Wait()
	}
	if recs, _ := r.imp.Status(bg(), 10); len(recs) != 2 {
		t.Errorf("one try and one retry expected, got %d imports", len(recs))
	}
	if st := r.download(j.id).State; st != downloads.StateFailed {
		t.Errorf("state = %s", st)
	}
}

func TestPendingDoesNotRetryABadRelease(t *testing.T) {
	r := newRig(t).withImporter()
	j := r.notldJob("The.Matrix.1999.1080p.BluRay.x264-GRP", map[string]string{"The.Matrix.1999.1080p.BluRay.x264-GRP.mkv": "x"}, grab.ProtocolTorrent)
	r.complete(j.id, j.content)
	r.advance(5 * time.Minute)
	if err := r.imp.Pending(bg()); err != nil {
		t.Fatal(err)
	}
	r.imp.Wait()
	if recs, _ := r.imp.Status(bg(), 10); len(recs) != 1 || len(r.blocklist()) != 1 {
		t.Errorf("a blocklisted release must not be imported again: %d imports, %d blocked", len(recs), len(r.blocklist()))
	}
}

func TestPendingPicksUpAnAbandonedImportAtStartup(t *testing.T) {
	r := newRig(t).withImporter()
	j := r.notldJob(notldRelease, map[string]string{notldRelease + ".mkv": "the film"}, grab.ProtocolTorrent)
	r.finish(j.id, j.content)
	r.imp.Wait()
	// Pretend a crash left it half done.
	if _, err := r.db.Exec(`UPDATE downloads SET state = 'importing' WHERE id = ?`, j.id); err != nil {
		t.Fatal(err)
	}
	if _, err := r.db.Exec(`UPDATE imports SET outcome = 'running', finished_at = NULL`); err != nil {
		t.Fatal(err)
	}
	r.imp.firstRun = true
	if err := r.imp.Pending(bg()); err != nil {
		t.Fatal(err)
	}
	r.imp.Wait()
	if st := r.download(j.id).State; st != downloads.StateImported {
		t.Errorf("state = %s", st)
	}
	recs, _ := r.imp.Status(bg(), 10)
	if len(recs) != 2 || recs[1].Outcome != OutcomeFailed || !strings.Contains(recs[1].Reason, "stopped") {
		t.Errorf("the interrupted attempt should be closed as failed: %+v", recs)
	}
}

func TestManualRetry(t *testing.T) {
	r := newRig(t).withImporter()
	j := r.notldJob(notldRelease, map[string]string{notldRelease + ".mkv": "the film"}, grab.ProtocolTorrent)
	missing := filepath.Join(r.dlDir, "later")
	r.finish(j.id, missing)
	r.imp.Wait()
	writeFile(t, filepath.Join(missing, notldRelease+".mkv"), []byte("the film"))

	if err := r.imp.Retry(bg(), j.id); err != nil {
		t.Fatal(err)
	}
	r.imp.Wait()
	if st := r.download(j.id); st.State != downloads.StateImported {
		t.Fatalf("state = %s %q", st.State, st.Error)
	}
	recs, _ := r.imp.Status(bg(), 10)
	if len(recs) != 2 || !recs[0].Manual {
		t.Errorf("the second import should be marked manual: %+v", recs)
	}
	if err := r.imp.Retry(bg(), j.id); !errors.Is(err, ErrWrongState) {
		t.Errorf("an imported download cannot be imported again, got %v", err)
	}
	if err := r.imp.Retry(bg(), 9999); !errors.Is(err, downloads.ErrNotFound) {
		t.Errorf("unknown download: %v", err)
	}
}

func TestSeveralCompletionNoticesMakeOneImport(t *testing.T) {
	r := newRig(t).withImporter()
	j := r.notldJob(notldRelease, map[string]string{notldRelease + ".mkv": "the film"}, grab.ProtocolTorrent)
	r.finish(j.id, j.content)
	for i := 0; i < 5; i++ {
		r.imp.OnCompleted(bg(), j.id)
	}
	r.imp.Wait()
	if recs, _ := r.imp.Status(bg(), 10); len(recs) != 1 {
		t.Errorf("one import expected, got %d", len(recs))
	}
}

// ---- servers -------------------------------------------------------------------------------------------------------------

func TestAMediaServerThatIsDownDoesNotUndoTheImport(t *testing.T) {
	r := newRig(t).withImporter()
	r.addJellyfin()
	r.jelly.srv.Close()
	j := r.notldJob(notldRelease, map[string]string{notldRelease + ".mkv": "the film"}, grab.ProtocolTorrent)
	r.complete(j.id, j.content)
	if st := r.download(j.id); st.State != downloads.StateImported {
		t.Fatalf("state = %s %q", st.State, st.Error)
	}
	list, err := r.srvs.List(bg())
	if err != nil || len(list) != 1 || list[0].LastError == "" {
		t.Errorf("the problem should be recorded on the server: %+v, %v", list, err)
	}
}

func TestJellyfinFallsBackToAFullRescan(t *testing.T) {
	r := newRig(t).withImporter()
	r.addJellyfin()
	r.jelly.mu.Lock()
	r.jelly.updatedStatus = http.StatusNotFound // an older server without Library/Media/Updated
	r.jelly.mu.Unlock()
	j := r.notldJob(notldRelease, map[string]string{notldRelease + ".mkv": "the film"}, grab.ProtocolTorrent)
	r.complete(j.id, j.content)
	var refresh int
	for _, c := range r.jelly.all() {
		if c.path == "/Library/Refresh" {
			refresh++
		}
	}
	if refresh != 1 {
		t.Errorf("the server should have been asked for a full rescan once, got %d", refresh)
	}
}
