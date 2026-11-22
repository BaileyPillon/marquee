package importer

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/BaileyPillon/marquee/internal/downloads"
	"github.com/BaileyPillon/marquee/internal/grab"
	"github.com/BaileyPillon/marquee/internal/history"
	"github.com/BaileyPillon/marquee/internal/library"
	"github.com/BaileyPillon/marquee/internal/requests"
)

// ---- the import steps --------------------------------------------------------------------

func TestImportStepsAreReportedInOrder(t *testing.T) {
	r := newRig(t).withImporter()
	r.addJellyfin()
	events, cancel := r.hub.Subscribe(downloads.Topic)
	defer cancel()

	j := r.notldJob(notldRelease, map[string]string{notldRelease + ".mkv": "the film"}, grab.ProtocolTorrent)
	r.complete(j.id, j.content)

	var steps []string
	var sawTarget bool
drain:
	for {
		select {
		case m := <-events.C():
			var ev map[string]any
			if err := json.Unmarshal(m.Data, &ev); err != nil {
				t.Fatal(err)
			}
			if _, has := ev["importTarget"]; has {
				sawTarget = true
			}
			if s, _ := ev["importStep"].(string); s != "" && (len(steps) == 0 || steps[len(steps)-1] != s) {
				steps = append(steps, s)
			}
		default:
			break drain
		}
	}
	want := []string{"checking", "renaming", "moving", "rescanning", "done"}
	if !reflect.DeepEqual(steps, want) {
		t.Errorf("steps = %v, want %v", steps, want)
	}
	if sawTarget {
		t.Error("the event stream reaches every signed-in person; it must not carry the destination path")
	}

	d := r.download(j.id)
	if d.State != downloads.StateImported || d.ImportStep != downloads.StepDone {
		t.Errorf("download = %s step %q", d.State, d.ImportStep)
	}
	// One file: the target is where it went.
	if want := j.destAbs(r); d.ImportTarget != want {
		t.Errorf("target = %q, want %q", d.ImportTarget, want)
	}
	v, err := r.dl.Get(bg(), j.id)
	if err != nil || v.ImportStep != "done" || v.ImportTarget != d.ImportTarget {
		t.Errorf("view = %+v, %v", v, err)
	}
}

func TestImportStepIsOnlyAskedForWhileImporting(t *testing.T) {
	r := newRig(t).withImporter()
	j := r.notldJob(notldRelease, map[string]string{notldRelease + ".mkv": "the film"}, grab.ProtocolTorrent)
	// Queued, not importing: a late step is ignored, not an error.
	if err := r.dl.SetImportStep(bg(), j.id, downloads.StepMoving, "/x"); err != nil {
		t.Fatal(err)
	}
	if d := r.download(j.id); d.ImportStep != "" || d.ImportTarget != "" {
		t.Errorf("a step on a download that is not importing was kept: %q %q", d.ImportStep, d.ImportTarget)
	}
	if err := r.dl.SetImportStep(bg(), j.id, "dancing", ""); err == nil {
		t.Error("an unknown step must be refused")
	}
	if err := r.dl.SetImportStep(bg(), 9999, downloads.StepMoving, ""); err != nil {
		t.Errorf("no such download: %v", err) // nothing to write, nothing to say
	}
}

func TestAFailedImportKeepsTheStepItStoppedOn(t *testing.T) {
	r := newRig(t).withImporter()
	j := r.notldJob(notldRelease, map[string]string{"readme.txt": "nothing to see"}, grab.ProtocolTorrent)
	r.complete(j.id, j.content)
	d := r.download(j.id)
	if d.State != downloads.StateFailed || d.ImportStep != downloads.StepChecking || d.ImportTarget != "" {
		t.Fatalf("download = %s step %q target %q", d.State, d.ImportStep, d.ImportTarget)
	}
	// Trying again starts from the beginning: the old step is cleared at the start.
	writeFile(t, filepath.Join(j.content, notldRelease+".mkv"), []byte("the film"))
	if err := r.imp.Retry(bg(), j.id); err != nil {
		t.Fatal(err)
	}
	r.imp.Wait()
	if d := r.download(j.id); d.State != downloads.StateImported || d.ImportStep != downloads.StepDone || d.ImportTarget == "" {
		t.Errorf("after the retry: %s step %q target %q", d.State, d.ImportStep, d.ImportTarget)
	}
}

func TestImportTargetIsTheFileOrTheFolderTheyShare(t *testing.T) {
	root := filepath.Join("lib", "tv")
	plans := func(dests ...string) []plan {
		var out []plan
		for _, d := range dests {
			out = append(out, plan{dest: d})
		}
		return out
	}
	cases := []struct {
		name  string
		plans []plan
		want  string
	}{
		{"none", nil, ""},
		{"one file", plans("Show (2010)/Season 01/Show - S01E01.mkv"), filepath.Join(root, "Show (2010)", "Season 01", "Show - S01E01.mkv")},
		{"one season", plans("Show (2010)/Season 01/a.mkv", "Show (2010)/Season 01/b.mkv"), filepath.Join(root, "Show (2010)", "Season 01")},
		{"two seasons", plans("Show (2010)/Season 01/a.mkv", "Show (2010)/Season 02/b.mkv"), filepath.Join(root, "Show (2010)")},
		{"similar names", plans("Show (2010)/Season 1/a.mkv", "Show (2010)/Season 10/b.mkv"), filepath.Join(root, "Show (2010)")},
		{"nothing in common", plans("A/a.mkv", "B/b.mkv"), root},
	}
	for _, c := range cases {
		if got := importTarget(root, c.plans); got != c.want {
			t.Errorf("%s: %q, want %q", c.name, got, c.want)
		}
	}
}

// ---- the history says which servers were told ---------------------------------------------

func TestHistoryNamesTheMediaServersThatWereTold(t *testing.T) {
	r := newRig(t).withImporter()
	r.addJellyfin()
	j := r.notldJob(notldRelease, map[string]string{notldRelease + ".mkv": "the film"}, grab.ProtocolTorrent)
	r.complete(j.id, j.content)

	if got := r.historyItem(j.title.ID, "imported")["rescanned"]; !reflect.DeepEqual(got, []any{"Jellyfin"}) {
		t.Errorf("rescanned = %v", got)
	}
	items, err := r.hist.ForTitle(bg(), j.title.ID, 10)
	if err != nil {
		t.Fatal(err)
	}
	var line string
	for _, it := range items {
		if it.Event == history.Imported {
			line = it.Text
		}
	}
	if line != "Imported Bluray-1080p · Jellyfin rescanned" {
		t.Errorf("the line is %q", line)
	}
}

func TestHistoryDoesNotClaimARescanThatFailed(t *testing.T) {
	r := newRig(t).withImporter()
	r.addJellyfin()
	r.jelly.srv.Close()
	j := r.notldJob(notldRelease, map[string]string{notldRelease + ".mkv": "the film"}, grab.ProtocolTorrent)
	r.complete(j.id, j.content)
	if got, has := r.historyItem(j.title.ID, "imported")["rescanned"]; has {
		t.Errorf("Jellyfin was down but the history says rescanned: %v", got)
	}
}

func TestHistoryOfAnUpgradeNamesTheServersToo(t *testing.T) {
	r := newRig(t).withImporter()
	r.addJellyfin()
	title := r.movie(notldTMDB)
	cp, _ := r.ask(title.ID, "hd", requests.Scope{})
	r.seed(r.moviesFolder, notldDir+"/Night of the Living Dead (1968) WEBDL-720p.mkv", title.ID, "WEBDL-720p")
	id := r.grabAs(cp, title, notldRelease, grab.ProtocolTorrent, nil)
	r.complete(id, r.content(notldRelease, map[string]string{notldRelease + ".mkv": "the better one"}))

	if got := r.historyItem(title.ID, "upgraded")["rescanned"]; !reflect.DeepEqual(got, []any{"Jellyfin"}) {
		t.Errorf("rescanned = %v", got)
	}
	items, _ := r.hist.ForTitle(bg(), title.ID, 10)
	for _, it := range items {
		if it.Event == history.Upgraded && it.Text != "Upgraded from WEBDL-720p to Bluray-1080p · Jellyfin rescanned" {
			t.Errorf("the line is %q", it.Text)
		}
	}
}

// ---- renaming a file that is already in the library ------------------------------------------------

// fileID is the id of the library row at a path.
func (r *rig) fileID(f library.Folder, rel string) int64 {
	r.t.Helper()
	row, ok := r.libraryFiles(f)[rel]
	if !ok {
		r.t.Fatalf("no row for %s in %v", rel, r.libraryFiles(f))
	}
	return row.ID
}

const (
	oddMovieRel = "Night.of.the.Living.Dead.1968.1080p.BluRay.mkv"
)

func TestRenameGivesAFileTheNameTheImporterWouldGive(t *testing.T) {
	r := newRig(t).withImporter()
	r.addJellyfin()
	title := r.movie(notldTMDB)
	oldAbs := r.seed(r.moviesFolder, oddMovieRel, title.ID, "Bluray-1080p")
	// A subtitle and a note that go with it, and one for another film that does not.
	writeFile(t, strings.TrimSuffix(oldAbs, ".mkv")+".en.srt", []byte("subtitles"))
	writeFile(t, strings.TrimSuffix(oldAbs, ".mkv")+".nfo", []byte("note"))
	otherSrt := filepath.Join(r.moviesDir, "Another Film (2000).en.srt")
	writeFile(t, otherSrt, []byte("not ours"))
	id := r.fileID(r.moviesFolder, oddMovieRel)

	res, err := r.imp.RenameFile(bg(), id)
	if err != nil {
		t.Fatal(err)
	}
	wantRel := notldDir + "/" + notldBluray
	if !res.Renamed || res.From != oddMovieRel || res.To != wantRel || res.File.RelPath != wantRel || res.File.ID != id {
		t.Fatalf("result = %+v", res)
	}
	newAbs := filepath.Join(r.moviesDir, notldDir, notldBluray)
	if exists(oldAbs) || string(mustRead(t, newAbs)) != "the old file" {
		t.Errorf("the file did not move: old exists %v", exists(oldAbs))
	}
	newStem := strings.TrimSuffix(newAbs, ".mkv")
	if string(mustRead(t, newStem+".en.srt")) != "subtitles" || string(mustRead(t, newStem+".nfo")) != "note" {
		t.Error("the subtitle and the note should have moved with the film")
	}
	if !exists(otherSrt) {
		t.Error("another film's subtitle must stay where it is")
	}
	// The row follows, with the same id, and a scan finds nothing to change.
	files := r.libraryFiles(r.moviesFolder)
	if _, old := files[oddMovieRel]; old || files[wantRel].ID != id || files[wantRel].State != library.StateMatched ||
		files[wantRel].TitleID == nil || *files[wantRel].TitleID != title.ID || files[wantRel].Quality != "Bluray-1080p" {
		t.Errorf("rows = %+v", files)
	}
	sum, err := r.lib.ScanNow(bg(), r.moviesFolder.ID)
	if err != nil {
		t.Fatal(err)
	}
	if sum.Added != 0 || sum.Missing != 0 || sum.Updated != 0 {
		t.Errorf("a scan after the rename = %+v", sum)
	}
	// History and Jellyfin.
	h := r.historyItem(title.ID, "renamed")
	if h["from"] != oddMovieRel || h["to"] != notldBluray || h["toPath"] != wantRel {
		t.Errorf("renamed detail = %v", h)
	}
	items, _ := r.hist.ForTitle(bg(), title.ID, 10)
	if items[0].Event != "renamed" || items[0].Text != "Renamed "+oddMovieRel+" to "+notldBluray {
		t.Errorf("history line = %+v", items[0])
	}
	var gone, made []string
	for _, u := range r.jelly.updates(t) {
		switch u["UpdateType"] {
		case "Deleted":
			gone = append(gone, u["Path"])
		case "Created":
			made = append(made, u["Path"])
		}
	}
	if len(gone) == 0 || !strings.HasSuffix(gone[0], oddMovieRel) || len(made) == 0 || !strings.HasSuffix(made[0], notldBluray) {
		t.Errorf("Jellyfin was told deleted %v, created %v", gone, made)
	}
	if len(res.Servers) == 0 || !res.Servers[0].OK {
		t.Errorf("servers = %+v", res.Servers)
	}
	if res.Servers[0].Server.Name != "Jellyfin" {
		t.Errorf("server = %+v", res.Servers[0])
	}

	// Again: already named right, nothing to do.
	again, err := r.imp.RenameFile(bg(), id)
	if err != nil || again.Renamed || again.To != wantRel || again.From != wantRel {
		t.Errorf("second rename = %+v, %v", again, err)
	}
	if n := len(r.historyEvents(title.ID)); n != 1 {
		t.Errorf("a rename that did nothing wrote history: %v", r.historyEvents(title.ID))
	}
}

func TestRenameRemovesTheFolderItEmptied(t *testing.T) {
	r := newRig(t).withImporter()
	title := r.movie(notldTMDB)
	oldRel := "downloads left over/" + oddMovieRel
	r.seed(r.moviesFolder, oldRel, title.ID, "Bluray-1080p")
	if _, err := r.imp.RenameFile(bg(), r.fileID(r.moviesFolder, oldRel)); err != nil {
		t.Fatal(err)
	}
	if exists(filepath.Join(r.moviesDir, "downloads left over")) {
		t.Error("the folder the rename emptied should be gone")
	}
	if !exists(r.moviesDir) {
		t.Error("the library folder itself must never be removed")
	}
}

func TestRenameNeverOverwritesADifferentFile(t *testing.T) {
	r := newRig(t).withImporter()
	title := r.movie(notldTMDB)
	r.seed(r.moviesFolder, oddMovieRel, title.ID, "Bluray-1080p")
	taken := filepath.Join(r.moviesDir, notldDir, notldBluray)
	writeFile(t, taken, []byte("somebody else's file"))

	_, err := r.imp.RenameFile(bg(), r.fileID(r.moviesFolder, oddMovieRel))
	if !errors.Is(err, ErrNameTaken) {
		t.Fatalf("err = %v, want ErrNameTaken", err)
	}
	if string(mustRead(t, taken)) != "somebody else's file" || !exists(filepath.Join(r.moviesDir, oddMovieRel)) {
		t.Error("nothing may change when the name is taken")
	}
	if got := r.historyEvents(title.ID); len(got) != 0 {
		t.Errorf("history = %v", got)
	}
}

func TestRenameNeedsAMatchAndAFile(t *testing.T) {
	r := newRig(t).withImporter()
	title := r.movie(notldTMDB)

	// A file the scanner could not match.
	stray := filepath.Join(r.moviesDir, "holiday video.mkv")
	writeFile(t, stray, []byte("x"))
	if _, err := r.lib.ScanNow(bg(), r.moviesFolder.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := r.imp.RenameFile(bg(), r.fileID(r.moviesFolder, "holiday video.mkv")); !errors.Is(err, ErrNotMatched) {
		t.Errorf("unmatched: %v, want ErrNotMatched", err)
	}
	if !exists(stray) {
		t.Error("an unmatched file must be left alone")
	}

	// A matched file that has vanished from the disk.
	abs := r.seed(r.moviesFolder, oddMovieRel, title.ID, "Bluray-1080p")
	if err := os.Remove(abs); err != nil {
		t.Fatal(err)
	}
	if _, err := r.imp.RenameFile(bg(), r.fileID(r.moviesFolder, oddMovieRel)); !errors.Is(err, ErrFileMissing) {
		t.Errorf("missing: %v, want ErrFileMissing", err)
	}

	if _, err := r.imp.RenameFile(bg(), 424242); !errors.Is(err, library.ErrNotFound) {
		t.Errorf("no such file: %v", err)
	}
}

func TestRenameOfAnEpisodeUsesTheSeasonFolderAndTheEpisodeName(t *testing.T) {
	r := newRig(t).withImporter()
	show := r.show(pioneerTVM)
	oldRel := "Pioneer.One.S01E01.mkv"
	r.seed(r.showsFolder, oldRel, show.ID, "WEBDL-720p", epID(t, show, 1, 1))
	res, err := r.imp.RenameFile(bg(), r.fileID(r.showsFolder, oldRel))
	if err != nil {
		t.Fatal(err)
	}
	want := "Pioneer One (2010)/Season 01/Pioneer One - S01E01 - Earthfall WEBDL-720p.mkv"
	if res.To != want || !exists(filepath.Join(r.showsDir, filepath.FromSlash(want))) {
		t.Errorf("to = %q, want %q", res.To, want)
	}
	// The row still knows its episode.
	row := r.libraryFiles(r.showsFolder)[want]
	if len(row.EpisodeIDs) != 1 || row.EpisodeIDs[0] != epID(t, show, 1, 1) {
		t.Errorf("row = %+v", row)
	}
}

func TestNameForAMusicTrack(t *testing.T) {
	r := newRig(t).withImporter()
	artist := r.artist(chopinMBID)
	det, err := r.cat.Get(bg(), artist.ID)
	if err != nil || len(det.Albums) == 0 || len(det.Albums[0].Tracks) == 0 {
		t.Fatalf("artist = %+v, %v", det, err)
	}
	tr := det.Albums[0].Tracks[0]
	got, err := NameFor(det, library.File{RelPath: "x/y/track.flac", TrackID: &tr.ID, Quality: "FLAC"})
	want := "Frédéric Chopin/Nocturnes (1998)/01 - Nocturne in B-flat minor, Op. 9 No. 1.flac"
	if err != nil || got != want {
		t.Errorf("NameFor = %q, %v, want %q", got, err, want)
	}
	if _, err := NameFor(det, library.File{RelPath: "a.flac"}); !errors.Is(err, ErrNotMatched) {
		t.Errorf("a track with no id: %v", err)
	}
	missing := int64(999999)
	if _, err := NameFor(det, library.File{RelPath: "a.flac", TrackID: &missing}); !errors.Is(err, ErrNotMatched) {
		t.Errorf("an unknown track: %v", err)
	}
}

// ---- deleting ------------------------------------------------------------------------------------------

func TestDeleteRemovesTheFileTheRowAndTellsTheServers(t *testing.T) {
	r := newRig(t).withImporter()
	r.addJellyfin()
	title := r.movie(notldTMDB)
	rel := notldDir + "/" + notldBluray
	abs := r.seed(r.moviesFolder, rel, title.ID, "Bluray-1080p")
	id := r.fileID(r.moviesFolder, rel)

	res, err := r.imp.DeleteFile(bg(), id, true)
	if err != nil {
		t.Fatal(err)
	}
	if !res.FromDisk || res.WasMissing || res.Path != rel {
		t.Errorf("result = %+v", res)
	}
	if exists(abs) {
		t.Error("the file should be gone from the disk")
	}
	if exists(filepath.Dir(abs)) {
		t.Error("the film's folder was emptied and should be gone too")
	}
	if !exists(r.moviesDir) {
		t.Error("the library folder must stay")
	}
	if _, still := r.libraryFiles(r.moviesFolder)[rel]; still {
		t.Error("the row should be gone")
	}
	h := r.historyItem(title.ID, "deleted")
	if h["file"] != notldBluray || h["fromDisk"] != true || h["quality"] != "Bluray-1080p" {
		t.Errorf("deleted detail = %v", h)
	}
	items, _ := r.hist.ForTitle(bg(), title.ID, 5)
	if items[0].Text != "Deleted "+notldBluray+" from disk" {
		t.Errorf("history line = %q", items[0].Text)
	}
	ups := r.jelly.updates(t)
	if len(ups) != 1 || ups[0]["UpdateType"] != "Deleted" || !strings.HasSuffix(ups[0]["Path"], notldBluray) {
		t.Errorf("Jellyfin was told %v", ups)
	}
	if len(res.Servers) != 1 || !res.Servers[0].OK {
		t.Errorf("servers = %+v", res.Servers)
	}
	if _, err := r.imp.DeleteFile(bg(), id, true); !errors.Is(err, library.ErrNotFound) {
		t.Errorf("deleting twice: %v", err)
	}
}

func TestDeleteWithoutTheDiskOnlyForgetsTheRow(t *testing.T) {
	r := newRig(t).withImporter()
	r.addJellyfin()
	title := r.movie(notldTMDB)
	rel := notldDir + "/" + notldBluray
	abs := r.seed(r.moviesFolder, rel, title.ID, "Bluray-1080p")

	res, err := r.imp.DeleteFile(bg(), r.fileID(r.moviesFolder, rel), false)
	if err != nil || res.FromDisk {
		t.Fatalf("result = %+v, %v", res, err)
	}
	if !exists(abs) {
		t.Error("the file must stay on the disk")
	}
	if _, still := r.libraryFiles(r.moviesFolder)[rel]; still {
		t.Error("the row should be gone")
	}
	if len(r.jelly.updates(t)) != 0 {
		t.Error("the media server still has the file: it is not told it is gone")
	}
	if h := r.historyItem(title.ID, "deleted"); h["fromDisk"] != false {
		t.Errorf("deleted detail = %v", h)
	}
	// The next scan finds the file again, as the API says it will.
	sum, err := r.lib.ScanNow(bg(), r.moviesFolder.ID)
	if err != nil || sum.Added != 1 {
		t.Errorf("scan = %+v, %v", sum, err)
	}
}

func TestDeleteOfAFileThatIsAlreadyGone(t *testing.T) {
	r := newRig(t).withImporter()
	title := r.movie(notldTMDB)
	rel := notldDir + "/" + notldBluray
	abs := r.seed(r.moviesFolder, rel, title.ID, "Bluray-1080p")
	if err := os.Remove(abs); err != nil {
		t.Fatal(err)
	}
	res, err := r.imp.DeleteFile(bg(), r.fileID(r.moviesFolder, rel), true)
	if err != nil {
		t.Fatalf("a missing file is not an error: %v", err)
	}
	if !res.WasMissing || !res.FromDisk {
		t.Errorf("result = %+v", res)
	}
	if _, still := r.libraryFiles(r.moviesFolder)[rel]; still {
		t.Error("the row should be gone")
	}
}

func TestDeleteRefusesAPathOutsideTheLibraryFolder(t *testing.T) {
	r := newRig(t).withImporter()
	title := r.movie(notldTMDB)
	outside := filepath.Join(filepath.Dir(r.moviesDir), "precious.mkv") // next to the folders, not in one
	writeFile(t, outside, []byte("do not touch"))
	inShows := filepath.Join(r.showsDir, "other library.mkv")
	writeFile(t, inShows, []byte("another folder's file"))

	r.seed(r.moviesFolder, "placeholder.mkv", title.ID, "Bluray-1080p")
	id := r.fileID(r.moviesFolder, "placeholder.mkv")

	for _, bad := range []string{
		"../precious.mkv",
		"..\\precious.mkv",
		"a/../../precious.mkv",
		"../tv/other library.mkv",
		outside,                       // an absolute path
		"/etc/passwd",                 // rooted
		"C:\\Windows\\win.ini",        // a drive
		"",                            // nothing
		".",                           // the folder itself
		"sub/../..",                   // the folder's parent
		"//server/share/precious.mkv", // a network path
	} {
		// The database says something it never should: a row whose path leaves the folder.
		if _, err := r.db.Exec(`UPDATE files SET rel_path = ? WHERE id = ?`, bad, id); err != nil {
			t.Fatal(err)
		}
		if _, err := r.imp.DeleteFile(bg(), id, true); !errors.Is(err, ErrOutsideLibrary) {
			t.Errorf("delete %q: %v, want ErrOutsideLibrary", bad, err)
		}
		if _, err := r.imp.RenameFile(bg(), id); !errors.Is(err, ErrOutsideLibrary) && !errors.Is(err, ErrNotMatched) {
			t.Errorf("rename %q: %v, want ErrOutsideLibrary", bad, err)
		}
		if string(mustRead(t, outside)) != "do not touch" || string(mustRead(t, inShows)) != "another folder's file" {
			t.Fatalf("a file outside the folder was touched (path %q)", bad)
		}
	}
	// And the row was not dropped by a refused delete.
	var n int
	if err := r.db.QueryRow(`SELECT COUNT(*) FROM files WHERE id = ?`, id).Scan(&n); err != nil || n != 1 {
		t.Errorf("the row of a refused delete: %d, %v", n, err)
	}
}

func TestDeleteAndRenameRefuseAFolderThatIsReallyALinkElsewhere(t *testing.T) {
	r := newRig(t).withImporter()
	title := r.movie(notldTMDB)
	elsewhere := t.TempDir()
	victim := filepath.Join(elsewhere, "precious.mkv")
	writeFile(t, victim, []byte("do not touch"))
	link := filepath.Join(r.moviesDir, "linked")
	if err := os.Symlink(elsewhere, link); err != nil {
		t.Skipf("cannot make a symbolic link here: %v", err)
	}
	// The scanner records the file under the link folder as a row.
	if err := r.lib.ImportFiles(bg(), r.moviesFolder.ID, []library.ImportedFile{{RelPath: "linked/precious.mkv", TitleID: title.ID, Quality: "Bluray-1080p"}}); err != nil {
		t.Fatal(err)
	}
	id := r.fileID(r.moviesFolder, "linked/precious.mkv")
	if _, err := r.imp.DeleteFile(bg(), id, true); !errors.Is(err, ErrOutsideLibrary) {
		t.Errorf("delete through a link: %v, want ErrOutsideLibrary", err)
	}
	if _, err := r.imp.RenameFile(bg(), id); !errors.Is(err, ErrOutsideLibrary) {
		t.Errorf("rename through a link: %v, want ErrOutsideLibrary", err)
	}
	if string(mustRead(t, victim)) != "do not touch" {
		t.Error("a file outside the library was touched through a link")
	}
}

func TestDeleteAndRenameWaitForARunningImport(t *testing.T) {
	r := newRig(t).withImporter()
	title := r.movie(notldTMDB)
	rel := notldDir + "/" + notldBluray
	abs := r.seed(r.moviesFolder, rel, title.ID, "Bluray-1080p")
	id := r.fileID(r.moviesFolder, rel)

	r.imp.work.Lock() // an import is moving files
	_, delErr := r.imp.DeleteFile(bg(), id, true)
	_, renErr := r.imp.RenameFile(bg(), id)
	r.imp.work.Unlock()
	if !errors.Is(delErr, ErrImporting) || !errors.Is(renErr, ErrImporting) {
		t.Errorf("delete %v, rename %v; want ErrImporting", delErr, renErr)
	}
	if !exists(abs) {
		t.Error("nothing may be deleted while an import runs")
	}
	if _, err := r.imp.DeleteFile(bg(), id, true); err != nil {
		t.Errorf("after the import: %v", err)
	}
}

func TestSafeJoin(t *testing.T) {
	root := filepath.Join(t.TempDir(), "lib")
	ok := map[string]string{
		"a.mkv":         filepath.Join(root, "a.mkv"),
		"Film (1999)/a": filepath.Join(root, "Film (1999)", "a"),
		"a/./b.mkv":     filepath.Join(root, "a", "b.mkv"),
		"a\\b.mkv":      filepath.Join(root, "a", "b.mkv"),
		"a/b/../c.mkv":  filepath.Join(root, "a", "c.mkv"),
	}
	for rel, want := range ok {
		if got, err := safeJoin(root, rel); err != nil || got != want {
			t.Errorf("safeJoin(%q) = %q, %v, want %q", rel, got, err, want)
		}
	}
	for _, rel := range []string{"", " ", ".", "..", "../x", "a/../../x", "/x", "\\x", "C:\\x", "c:x", "a/..", "../lib2/x"} {
		if got, err := safeJoin(root, rel); !errors.Is(err, ErrOutsideLibrary) {
			t.Errorf("safeJoin(%q) = %q, %v, want ErrOutsideLibrary", rel, got, err)
		}
	}
}

func TestPublishedLibraryEventForFileActions(t *testing.T) {
	r := newRig(t).withImporter()
	events, cancel := r.hub.Subscribe(LibraryTopic)
	defer cancel()
	title := r.movie(notldTMDB)
	r.seed(r.moviesFolder, oddMovieRel, title.ID, "Bluray-1080p")
	id := r.fileID(r.moviesFolder, oddMovieRel)
	if _, err := r.imp.RenameFile(bg(), id); err != nil {
		t.Fatal(err)
	}
	if _, err := r.imp.DeleteFile(bg(), id, true); err != nil {
		t.Fatal(err)
	}
	var got []string
	for i := 0; i < 2; i++ {
		select {
		case m := <-events.C():
			var ev struct {
				Event string `json:"event"`
				Title int64  `json:"titleId"`
			}
			if err := json.Unmarshal(m.Data, &ev); err != nil || ev.Title != title.ID {
				t.Fatalf("event = %s, %v", m.Data, err)
			}
			got = append(got, ev.Event)
		case <-time.After(2 * time.Second):
			t.Fatalf("only %v arrived", got)
		}
	}
	if !reflect.DeepEqual(got, []string{"renamed", "deleted"}) {
		t.Errorf("events = %v", got)
	}
}
