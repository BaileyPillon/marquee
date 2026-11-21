package library

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"sort"
	"strconv"
	"sync/atomic"
	"testing"
	"time"
)

func sorted(in []string) []string {
	out := slices.Clone(in)
	sort.Strings(out)
	return out
}

func TestScanMovieLayouts(t *testing.T) {
	e := newEnv(t)
	f, root := e.addFolder(KindMovie,
		// Plex: "Title (Year)/Title (Year).ext"
		"Metropolis (1927)/Metropolis (1927).mkv",
		"Metropolis (1927)/Metropolis (1927) - 720p.mp4", // a second version beside it
		"Nosferatu (1922)/Nosferatu.1922.1080p.BluRay.x264-GRP.mkv",
		"Undersea Kingdom (1936)/Undersea Kingdom (1936).avi",
		// Jellyfin: a flat file, or a folder with provider ids
		"Sintel (2010) [tmdbid-45745]/Sintel (2010).mkv",
		"Big.Buck.Bunny.2008.720p.BluRay.x264-GRP.mkv",
		"Tears of Steel (2012)/Tears of Steel (2012).m4v",
		"Night of the Living Dead (1968)/Night of the Living Dead (1968).ts",
		"Night of the Living Dead (1968)/Night.of.the.Living.Dead.1968.mov",
		"Sherlock Jr. (1924)/Sherlock Jr. (1924).wmv",
		"Sherlock Jr. (1924)/Sherlock Jr. (1924) - alt.webm",

		// Everything below must be skipped.
		"Tears of Steel (2012)/Extras/Making of.mkv",
		"Tears of Steel (2012)/Featurettes/Interview.mkv",
		"Tears of Steel (2012)/Behind The Scenes/Bts.mkv",
		"Tears of Steel (2012)/Sample/sample.mkv",
		"Tears of Steel (2012)/Samples/clip.mkv",
		"Tears of Steel (2012)/Tears.of.Steel.2012.1080p-sample.mkv",
		"Nosferatu (1922)/Nosferatu.1922.mkv.part",
		"Nosferatu (1922)/Nosferatu.1922.mkv.!qB",
		"Nosferatu (1922)/Nosferatu.1922.mkv.!QB",
		".hidden/Secret (2001).mkv",
		"Metropolis (1927)/.Metropolis.mkv",
		"._Metropolis (1927).mkv",
		"@eaDir/Metropolis.mkv",
		"#recycle/Metropolis.mkv",
		"$RECYCLE.BIN/Metropolis.mkv",
		"Metropolis (1927)/poster.jpg",
		"Metropolis (1927)/readme.txt",
		"Metropolis (1927)/Metropolis (1927).srt",
		"Metropolis (1927)/Metropolis (1927).nfo",
		"Metropolis (1927)/soundtrack.flac", // audio in a movie folder
	)
	sum := e.scan(f)
	want := []string{
		"Metropolis (1927)/Metropolis (1927).mkv",
		"Metropolis (1927)/Metropolis (1927) - 720p.mp4",
		"Nosferatu (1922)/Nosferatu.1922.1080p.BluRay.x264-GRP.mkv",
		"Undersea Kingdom (1936)/Undersea Kingdom (1936).avi",
		"Sintel (2010) [tmdbid-45745]/Sintel (2010).mkv",
		"Big.Buck.Bunny.2008.720p.BluRay.x264-GRP.mkv",
		"Tears of Steel (2012)/Tears of Steel (2012).m4v",
		"Night of the Living Dead (1968)/Night of the Living Dead (1968).ts",
		"Night of the Living Dead (1968)/Night.of.the.Living.Dead.1968.mov",
		"Sherlock Jr. (1924)/Sherlock Jr. (1924).wmv",
		"Sherlock Jr. (1924)/Sherlock Jr. (1924) - alt.webm",
	}
	rows := e.files(f)
	if got := sorted(keys(rows)); !slices.Equal(got, sorted(want)) {
		t.Fatalf("scanned files:\n got  %q\n want %q", got, sorted(want))
	}
	if sum.Seen != len(want) || sum.Added != len(want) || sum.Updated+sum.Missing+sum.Returned+sum.Unchanged != 0 {
		t.Errorf("summary = %+v, want %d added and nothing else", sum, len(want))
	}
	// No title source yet: every file is recorded, unmatched, with its parse.
	for rel, file := range rows {
		if file.State != StateUnmatched || file.TitleID != nil || file.EpisodeIDs != nil || file.Kind != KindMovie {
			t.Errorf("%s: state %q title %v kind %q", rel, file.State, file.TitleID, file.Kind)
		}
		if file.MTime.Unix() != fixedTime.Unix() || file.AddedAt.IsZero() || file.MissingSince != nil {
			t.Errorf("%s: mtime %v added %v missing %v", rel, file.MTime, file.AddedAt, file.MissingSince)
		}
	}
	nos := rows["Nosferatu (1922)/Nosferatu.1922.1080p.BluRay.x264-GRP.mkv"]
	var parsed map[string]any
	if err := json.Unmarshal(nos.Parsed, &parsed); err != nil || parsed["title"] != "Nosferatu" || parsed["year"] != float64(1922) {
		t.Errorf("parsed = %s (%v)", nos.Parsed, err)
	}
	if nos.Quality != "Bluray-1080p" {
		t.Errorf("quality = %q", nos.Quality)
	}
	if q := rows["Metropolis (1927)/Metropolis (1927) - 720p.mp4"].Quality; q != "Unknown-720p" {
		t.Errorf("quality = %q", q)
	}
	// The files on disk are untouched.
	if _, err := os.Stat(filepath.Join(root, "Nosferatu (1922)", "Nosferatu.1922.mkv.part")); err != nil {
		t.Errorf("scan touched a file: %v", err)
	}
}

func TestScanShowLayouts(t *testing.T) {
	e := newEnv(t)
	f, _ := e.addFolder(KindShow,
		// Jellyfin / Plex: Series (Year)/Season NN/Series - SxxEyy.ext
		"Pioneer One (2010)/Season 01/Pioneer One (2010) S01E01.mkv",
		"Pioneer One (2010)/Season 01/Pioneer One - S01E02 - Earthfall.mkv",
		"Pioneer One (2010)/Season 01/Pioneer.One.S01E03E04.720p.HDTV.x264-GRP.mkv",
		"Pioneer One (2010)/Season 1/Pioneer One - 1x05.avi",
		"Pioneer One (2010)/Specials/Pioneer One - S00E01 - Recap.mkv",
		"Undersea Kingdom (1936)/Season 01/Undersea Kingdom - S01E01 - Chapter 1.mp4",
		"Pioneer.One.S01E06.720p.mkv", // a loose file at the top

		// Skipped.
		"Pioneer One (2010)/Season 01/Extras/Behind the scenes.mkv",
		"Pioneer One (2010)/Season 01/Sample/sample.mkv",
		"Pioneer One (2010)/Season 01/Pioneer One - S01E01.mkv.part",
		"Pioneer One (2010)/Season 01/Pioneer One - S01E01.nfo",
		"Pioneer One (2010)/folder.jpg",
		"Pioneer One (2010)/Featurettes/x.mkv",
	)
	e.scan(f)
	want := []string{
		"Pioneer One (2010)/Season 01/Pioneer One (2010) S01E01.mkv",
		"Pioneer One (2010)/Season 01/Pioneer One - S01E02 - Earthfall.mkv",
		"Pioneer One (2010)/Season 01/Pioneer.One.S01E03E04.720p.HDTV.x264-GRP.mkv",
		"Pioneer One (2010)/Season 1/Pioneer One - 1x05.avi",
		"Pioneer One (2010)/Specials/Pioneer One - S00E01 - Recap.mkv",
		"Undersea Kingdom (1936)/Season 01/Undersea Kingdom - S01E01 - Chapter 1.mp4",
		"Pioneer.One.S01E06.720p.mkv",
	}
	rows := e.files(f)
	if got := sorted(keys(rows)); !slices.Equal(got, sorted(want)) {
		t.Fatalf("scanned files:\n got  %q\n want %q", got, sorted(want))
	}
	if rows[want[0]].Kind != KindShow {
		t.Errorf("kind = %q", rows[want[0]].Kind)
	}
	var parsed struct {
		Season   int   `json:"season"`
		Episodes []int `json:"episodes"`
	}
	if err := json.Unmarshal(rows[want[2]].Parsed, &parsed); err != nil || parsed.Season != 1 || !slices.Equal(parsed.Episodes, []int{3, 4}) {
		t.Errorf("parsed = %s (%v)", rows[want[2]].Parsed, err)
	}
}

func TestScanMusicLayouts(t *testing.T) {
	e := newEnv(t)
	f, _ := e.addFolder(KindMusic,
		"Frédéric Chopin/Nocturnes (2012) [FLAC]/CD1/01 - Nocturne in E-flat major, Op. 9 No. 2.flac",
		"Frédéric Chopin/Nocturnes (2012) [FLAC]/CD1/02 - Nocturne in F minor, Op. 55 No. 1.flac",
		"Frédéric Chopin/Nocturnes (2012) [FLAC]/CD2/01 - Nocturne in C minor, Op. 48 No. 1.flac",
		"Chopin/Préludes (1999) [MP3 320]/03 - Prélude No. 3.mp3",
		"Chopin/Ballades/01 Ballade No. 1.m4a",
		"Chopin/Ballades/02 Ballade No. 2.aac",
		"Chopin/Ballades/03 Ballade No. 3.ogg",
		"Chopin/Ballades/04 Ballade No. 4.opus",
		"Chopin/Ballades/05 Ballade No. 5.wav",
		"Chopin/Ballades/06 Ballade No. 6.alac",

		// Skipped.
		"Frédéric Chopin/Nocturnes (2012) [FLAC]/cover.jpg",
		"Frédéric Chopin/Nocturnes (2012) [FLAC]/Nocturnes.cue",
		"Frédéric Chopin/Nocturnes (2012) [FLAC]/Nocturnes.log",
		"Frédéric Chopin/Nocturnes (2012) [FLAC]/01 - Nocturne.flac.part",
		"Frédéric Chopin/.hidden/01.flac",
		"Frédéric Chopin/Extras/01.flac",
		"Chopin/Ballades/movie.mkv", // video in a music folder
	)
	e.scan(f)
	rows := e.files(f)
	if len(rows) != 10 {
		t.Fatalf("got %d files, want 10: %q", len(rows), sorted(keys(rows)))
	}
	r := rows["Frédéric Chopin/Nocturnes (2012) [FLAC]/CD1/01 - Nocturne in E-flat major, Op. 9 No. 2.flac"]
	var got map[string]any
	if err := json.Unmarshal(r.Parsed, &got); err != nil {
		t.Fatal(err)
	}
	if got["artist"] != "Frédéric Chopin" || got["album"] != "Nocturnes" || got["format"] != "FLAC" ||
		got["disc"] != float64(1) || got["track"] != float64(1) || got["year"] != float64(2012) {
		t.Errorf("parsed = %s", r.Parsed)
	}
	if r.Quality != "FLAC" || r.Kind != KindMusic || r.State != StateUnmatched {
		t.Errorf("row = %+v", r)
	}
	if q := rows["Chopin/Préludes (1999) [MP3 320]/03 - Prélude No. 3.mp3"].Quality; q != "MP3-320kbps" {
		t.Errorf("quality = %q", q)
	}
}

func TestRescanSeesChangesMissingAndReturningFiles(t *testing.T) {
	e := newEnv(t)
	f, root := e.addFolder(KindMovie,
		"Metropolis (1927)/Metropolis (1927).mkv",
		"Nosferatu (1922)/Nosferatu (1922).mkv",
		"Sintel (2010)/Sintel (2010).mkv",
		"Big Buck Bunny (2008)/Big Buck Bunny (2008).mkv",
		"Tears of Steel (2012)/Tears of Steel (2012).mkv",
	)
	first := e.scan(f)
	if first.Added != 5 {
		t.Fatalf("first scan = %+v", first)
	}
	// Nothing changed: everything is unchanged and nothing is written twice.
	if sum := e.scan(f); sum.Unchanged != 5 || sum.Added+sum.Updated+sum.Missing+sum.Returned != 0 {
		t.Fatalf("second scan = %+v", sum)
	}

	path := func(rel string) string { return filepath.Join(root, filepath.FromSlash(rel)) }
	metropolis := "Metropolis (1927)/Metropolis (1927).mkv"
	nosferatu := "Nosferatu (1922)/Nosferatu (1922).mkv"
	sintel := "Sintel (2010)/Sintel (2010).mkv"
	bbb := "Big Buck Bunny (2008)/Big Buck Bunny (2008).mkv"

	// Size change, mtime-only change, a file that vanishes, a new file.
	if err := os.WriteFile(path(metropolis), []byte("longer now"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Chtimes(path(metropolis), fixedTime, fixedTime); err != nil {
		t.Fatal(err)
	}
	later := fixedTime.Add(time.Hour)
	if err := os.Chtimes(path(nosferatu), later, later); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(path(sintel)); err != nil {
		t.Fatal(err)
	}
	mkFiles(t, root, "Undersea Kingdom (1936)/Undersea Kingdom (1936).avi")

	sum := e.scan(f)
	if sum.Added != 1 || sum.Updated != 2 || sum.Missing != 1 || sum.Unchanged != 2 || sum.Returned != 0 || sum.Seen != 5 {
		t.Fatalf("third scan = %+v, want 1 added, 2 updated, 1 missing, 2 unchanged", sum)
	}
	rows := e.files(f)
	if rows[metropolis].Size != int64(len("longer now")) {
		t.Errorf("size = %d", rows[metropolis].Size)
	}
	if got := rows[nosferatu].MTime.UTC(); !got.Equal(later) {
		t.Errorf("mtime = %v, want %v", got, later)
	}
	if rows[sintel].MissingSince == nil {
		t.Error("a vanished file must get missing_since")
	}
	if len(rows) != 6 {
		t.Errorf("rows = %d, want 6 (the missing one stays)", len(rows))
	}

	// A file that is still gone is not reported missing again.
	if sum := e.scan(f); sum.Missing != 0 || sum.Unchanged != 5 {
		t.Errorf("fourth scan = %+v", sum)
	}
	// It comes back exactly as it was.
	mkFiles(t, root, sintel)
	sum = e.scan(f)
	if sum.Returned != 1 || sum.Unchanged != 5 || sum.Added+sum.Updated+sum.Missing != 0 {
		t.Errorf("fifth scan = %+v, want 1 returned", sum)
	}
	if e.files(f)[sintel].MissingSince != nil {
		t.Error("a returned file must lose missing_since")
	}

	// A file that returns changed is an update, and clears the flag too.
	if err := os.Remove(path(bbb)); err != nil {
		t.Fatal(err)
	}
	e.scan(f)
	if e.files(f)[bbb].MissingSince == nil {
		t.Fatal("not marked missing")
	}
	if err := os.WriteFile(path(bbb), []byte("new"), 0o644); err != nil {
		t.Fatal(err)
	}
	sum = e.scan(f)
	if sum.Updated != 1 || sum.Returned != 0 {
		t.Errorf("sixth scan = %+v, want 1 updated", sum)
	}
	if e.files(f)[bbb].MissingSince != nil {
		t.Error("an updated file must lose missing_since")
	}
}

func TestScanFailsWithoutMarkingFilesMissingWhenTheFolderIsGone(t *testing.T) {
	e := newEnv(t)
	f, root := e.addFolder(KindMovie, "Metropolis (1927)/Metropolis (1927).mkv")
	e.scan(f)
	moved := root + ".moved"
	if err := os.Rename(root, moved); err != nil {
		t.Fatal(err)
	}
	if _, err := e.svc.ScanNow(context.Background(), f.ID); err == nil {
		t.Fatal("scanning a folder that vanished should fail")
	}
	for rel, file := range e.files(f) {
		if file.MissingSince != nil {
			t.Errorf("%s was marked missing though the whole folder is just unreachable", rel)
		}
	}
	// When the drive is back, a scan is clean.
	if err := os.Rename(moved, root); err != nil {
		t.Fatal(err)
	}
	if sum := e.scan(f); sum.Unchanged != 1 || sum.Missing != 0 {
		t.Errorf("scan after return = %+v", sum)
	}
}

func TestIgnoredFilesStayIgnored(t *testing.T) {
	e := newEnv(t)
	ts := newFakeTitles()
	ts.cands[CandidateMovie] = []Candidate{{ID: 1, Kind: "movie", Name: "Metropolis", Year: 1927}}
	e.svc.SetTitleSource(ts)
	f, root := e.addFolder(KindMovie, "Metropolis (1927)/Metropolis (1927).mkv")
	e.scan(f)
	if _, err := e.db.Exec(`UPDATE files SET match_state = 'ignored', title_id = NULL`); err != nil {
		t.Fatal(err)
	}
	// Unchanged, then changed: still ignored, and never matched.
	e.scan(f)
	p := filepath.Join(root, "Metropolis (1927)", "Metropolis (1927).mkv")
	if err := os.WriteFile(p, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	sum := e.scan(f)
	if sum.Updated != 1 || sum.Matched != 0 {
		t.Errorf("summary = %+v", sum)
	}
	for _, file := range e.files(f) {
		if file.State != StateIgnored || file.TitleID != nil {
			t.Errorf("file = %+v", file)
		}
	}
}

func TestScanHonoursCancellation(t *testing.T) {
	e := newEnv(t)
	f, _ := e.addFolder(KindMovie, "Metropolis (1927)/Metropolis (1927).mkv")

	// Cancelled before it starts: nothing is written.
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := e.svc.ScanNow(ctx, f.ID); !errors.Is(err, context.Canceled) {
		t.Fatalf("err = %v, want context.Canceled", err)
	}
	if n := len(e.files(f)); n != 0 {
		t.Errorf("a cancelled scan wrote %d rows", n)
	}

	// Cancelled in the middle of a walk.
	started := make(chan struct{})
	e.svc.walk = func(ctx context.Context, _, _ string, _ func(int)) (walkResult, error) {
		close(started)
		<-ctx.Done()
		return walkResult{}, ctx.Err()
	}
	ctx, cancel = context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() {
		_, err := e.svc.ScanNow(ctx, f.ID)
		done <- err
	}()
	<-started
	cancel()
	select {
	case err := <-done:
		if !errors.Is(err, context.Canceled) {
			t.Errorf("err = %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("scan did not stop when its context was cancelled")
	}
	if e.svc.isRunning(f.ID) {
		t.Error("the folder is still marked as scanning")
	}
}

func TestWalkStopsWhenCancelled(t *testing.T) {
	root := t.TempDir()
	mkFiles(t, root, "a/1.mkv", "a/2.mkv", "b/3.mkv")
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := walkFolder(ctx, root, KindMovie, nil); !errors.Is(err, context.Canceled) {
		t.Errorf("err = %v", err)
	}
	if _, err := walkFolder(context.Background(), filepath.Join(root, "nope"), KindMovie, nil); err == nil {
		t.Error("a missing root must be an error")
	}
}

func TestWalkReportsProgress(t *testing.T) {
	root := t.TempDir()
	var rels []string
	for i := 0; i < progressEvery*2+3; i++ {
		rels = append(rels, filepath.ToSlash(filepath.Join("d", string(rune('a'+i%20)), "f"+strconv.Itoa(i)+".mkv")))
	}
	mkFiles(t, root, rels...)
	var seen []int
	res, err := walkFolder(context.Background(), root, KindMovie, func(n int) { seen = append(seen, n) })
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Files) != len(rels) || !slices.Equal(seen, []int{progressEvery, progressEvery * 2}) {
		t.Errorf("found %d files, progress %v", len(res.Files), seen)
	}
}

func TestAtMostTwoWalksAtOnce(t *testing.T) {
	e := newEnv(t)
	for i := 0; i < 6; i++ {
		e.addFolder(KindMovie, "x/x.mkv")
	}
	var cur, peak atomic.Int32
	e.svc.walk = func(ctx context.Context, root, kind string, progress func(int)) (walkResult, error) {
		n := cur.Add(1)
		for {
			p := peak.Load()
			if n <= p || peak.CompareAndSwap(p, n) {
				break
			}
		}
		time.Sleep(40 * time.Millisecond)
		cur.Add(-1)
		return walkResult{}, nil
	}
	sums, err := e.svc.ScanAll(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(sums) != 6 {
		t.Errorf("summaries = %d, want 6", len(sums))
	}
	if got := peak.Load(); got != 2 {
		t.Errorf("peak concurrent walks = %d, want exactly 2", got)
	}
}

func TestScanOfARunningFolderIsRefused(t *testing.T) {
	e := newEnv(t)
	f, _ := e.addFolder(KindMovie, "x/x.mkv")
	inside, release := make(chan struct{}), make(chan struct{})
	e.svc.walk = func(ctx context.Context, _, _ string, _ func(int)) (walkResult, error) {
		close(inside)
		<-release
		return walkResult{}, nil
	}
	done := make(chan error, 1)
	go func() {
		_, err := e.svc.ScanNow(context.Background(), f.ID)
		done <- err
	}()
	<-inside
	if _, err := e.svc.ScanNow(context.Background(), f.ID); !errors.Is(err, ErrScanRunning) {
		t.Errorf("second ScanNow err = %v, want ErrScanRunning", err)
	}
	started, running, err := e.svc.StartScan(context.Background(), f.ID)
	if err != nil || len(started) != 0 || !slices.Equal(running, []int64{f.ID}) {
		t.Errorf("StartScan = %v %v %v", started, running, err)
	}
	infos, err := e.svc.Folders(context.Background())
	if err != nil || len(infos) != 1 || !infos[0].Scanning {
		t.Errorf("Folders = %+v %v, want scanning", infos, err)
	}
	close(release)
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	if e.svc.isRunning(f.ID) {
		t.Error("still running after the scan ended")
	}
}

func TestStartScanRunsInTheBackgroundAndPublishesEvents(t *testing.T) {
	e := newEnv(t)
	f, _ := e.addFolder(KindMovie, "Metropolis (1927)/Metropolis (1927).mkv")
	client, unsubscribe := e.hub.Subscribe(ScanTopic)
	defer unsubscribe()

	started, running, err := e.svc.StartScan(context.Background(), f.ID)
	if err != nil || !slices.Equal(started, []int64{f.ID}) || len(running) != 0 {
		t.Fatalf("StartScan = %v %v %v", started, running, err)
	}
	var states []string
	var last ScanEvent
	timeout := time.After(10 * time.Second)
	for last.State != "done" {
		select {
		case msg := <-client.C():
			if msg.Topic != ScanTopic {
				t.Fatalf("topic = %q", msg.Topic)
			}
			if err := json.Unmarshal(msg.Data, &last); err != nil {
				t.Fatal(err)
			}
			states = append(states, last.State)
		case <-timeout:
			t.Fatalf("no done event; got %v", states)
		}
	}
	if states[0] != "started" || last.FolderID != f.ID || last.Summary == nil || last.Summary.Added != 1 || last.Seen != 1 {
		t.Errorf("events %v, last %+v", states, last)
	}
	if n := len(e.files(f)); n != 1 {
		t.Errorf("rows = %d", n)
	}

	if _, _, err := e.svc.StartScan(context.Background(), 9999); !errors.Is(err, ErrNotFound) {
		t.Errorf("unknown folder err = %v", err)
	}
}

func TestScanFailureIsPublished(t *testing.T) {
	e := newEnv(t)
	f, root := e.addFolder(KindMovie, "x/x.mkv")
	if err := os.RemoveAll(root); err != nil {
		t.Fatal(err)
	}
	client, unsubscribe := e.hub.Subscribe(ScanTopic)
	defer unsubscribe()
	if _, err := e.svc.ScanNow(context.Background(), f.ID); err == nil {
		t.Fatal("expected a failure")
	}
	var states []string
	for len(states) < 2 {
		select {
		case msg := <-client.C():
			var ev ScanEvent
			_ = json.Unmarshal(msg.Data, &ev)
			states = append(states, ev.State)
			if ev.State == "failed" && ev.Error == "" {
				t.Error("failed event without an error")
			}
		case <-time.After(5 * time.Second):
			t.Fatalf("events so far: %v", states)
		}
	}
	if !slices.Equal(states, []string{"started", "failed"}) {
		t.Errorf("states = %v", states)
	}
}

func TestScanAllAndTheScheduledJob(t *testing.T) {
	e := newEnv(t)
	a, aRoot := e.addFolder(KindMovie, "Metropolis (1927)/Metropolis (1927).mkv")
	b, _ := e.addFolder(KindShow, "Pioneer One/Season 1/Pioneer One - S01E01.mkv", "Pioneer One/Season 1/Pioneer One - S01E02.mkv")
	if err := e.svc.ScanJob(context.Background()); err != nil {
		t.Fatal(err)
	}
	if len(e.files(a)) != 1 || len(e.files(b)) != 2 {
		t.Errorf("rows: %d and %d", len(e.files(a)), len(e.files(b)))
	}
	sums, err := e.svc.ScanAll(context.Background())
	if err != nil || len(sums) != 2 {
		t.Fatalf("ScanAll = %v %v", sums, err)
	}
	for _, s := range sums {
		if s.Added != 0 || s.Unchanged == 0 {
			t.Errorf("rescan summary = %+v", s)
		}
	}
	// A folder that fails does not stop the others, and the error comes back.
	if err := os.RemoveAll(aRoot); err != nil {
		t.Fatal(err)
	}
	sums, err = e.svc.ScanAll(context.Background())
	if err == nil || len(sums) != 1 || sums[0].FolderID != b.ID {
		t.Errorf("ScanAll with one folder gone = %v %v", sums, err)
	}
}

func TestCloseStopsBackgroundScans(t *testing.T) {
	e := newEnv(t)
	f, _ := e.addFolder(KindMovie, "x/x.mkv")
	inside := make(chan struct{})
	e.svc.walk = func(ctx context.Context, _, _ string, _ func(int)) (walkResult, error) {
		close(inside)
		<-ctx.Done()
		return walkResult{}, ctx.Err()
	}
	if _, _, err := e.svc.StartScan(context.Background(), f.ID); err != nil {
		t.Fatal(err)
	}
	<-inside
	closed := make(chan struct{})
	go func() { e.svc.Close(); close(closed) }()
	select {
	case <-closed:
	case <-time.After(5 * time.Second):
		t.Fatal("Close did not stop the scan")
	}
	if _, _, err := e.svc.StartScan(context.Background(), f.ID); !errors.Is(err, ErrClosed) {
		t.Errorf("StartScan after Close = %v", err)
	}
}
