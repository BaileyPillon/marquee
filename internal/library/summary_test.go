package library

import (
	"context"
	"testing"
)

// addRow inserts one file row straight into the table, so a test can say
// exactly which title, episodes and track a file belongs to.
type row struct {
	folder   int64
	rel      string
	size     int64
	quality  string
	title    any // nil or an id
	episodes any // nil or a JSON text such as "[1,2]"
	track    any
	state    string
	missing  any
}

func (e *testEnv) addRow(r row) {
	e.t.Helper()
	if r.state == "" {
		r.state = StateMatched
	}
	_, err := e.db.Exec(`INSERT INTO files (folder_id, rel_path, size, mtime, kind, quality, title_id, episode_ids, track_id,
		match_state, added_at, missing_since) VALUES (?, ?, ?, 0, 'movie', ?, ?, ?, ?, ?, '2026-01-01T00:00:00.000Z', ?)`,
		r.folder, r.rel, r.size, r.quality, r.title, r.episodes, r.track, r.state, r.missing)
	if err != nil {
		e.t.Fatal(err)
	}
}

func TestTitleFilesCountsMatchedFilesThatAreStillThere(t *testing.T) {
	e := newEnv(t)
	f, _ := e.addFolder(KindMovie)
	e.addRow(row{folder: f.ID, rel: "a1.mkv", size: 100, quality: "Bluray-1080p", title: 1})
	e.addRow(row{folder: f.ID, rel: "a2.mkv", size: 300, quality: "WEBDL-2160p", title: 1})
	e.addRow(row{folder: f.ID, rel: "a3.mkv", size: 50, quality: "Remux-2160p", title: 1, missing: "2026-02-01T00:00:00.000Z"}) // gone from disk
	e.addRow(row{folder: f.ID, rel: "a4.mkv", size: 70, quality: "HDTV-720p", title: 1, state: StateIgnored})                   // ignored
	e.addRow(row{folder: f.ID, rel: "u.mkv", size: 90, quality: "Bluray-1080p", state: StateUnmatched})                         // nobody's
	e.addRow(row{folder: f.ID, rel: "b.mkv", size: 40, quality: "WEBRip-720p", title: 3})

	got, err := e.svc.TitleFiles(context.Background(), []int64{1, 2, 3})
	if err != nil {
		t.Fatal(err)
	}
	if one := got[1]; one.Count != 2 || one.Bytes != 400 || one.Quality != "WEBDL-2160p" {
		t.Errorf("title 1 = %+v, want 2 files, 400 bytes, best WEBDL-2160p", one)
	}
	if _, has := got[2]; has {
		t.Errorf("a title with no file has an entry: %+v", got[2])
	}
	if three := got[3]; three.Count != 1 || three.Bytes != 40 || three.Quality != "WEBRip-720p" {
		t.Errorf("title 3 = %+v", three)
	}
	// Only the titles asked about.
	if only, _ := e.svc.TitleFiles(context.Background(), []int64{3}); len(only) != 1 {
		t.Errorf("asked about title 3 only: %+v", only)
	}
	if none, err := e.svc.TitleFiles(context.Background(), nil); err != nil || len(none) != 0 {
		t.Errorf("no ids: %v %v", none, err)
	}
}

func TestTitleFilesOnAManyTitlesPageUsesOneQueryPerBatch(t *testing.T) {
	e := newEnv(t)
	f, _ := e.addFolder(KindMovie)
	var ids []int64
	for i := int64(1); i <= 1200; i++ { // more than one batch
		ids = append(ids, i)
	}
	e.addRow(row{folder: f.ID, rel: "x.mkv", size: 5, quality: "Bluray-1080p", title: 1100})
	got, err := e.svc.TitleFiles(context.Background(), ids)
	if err != nil || len(got) != 1 || got[1100].Count != 1 {
		t.Errorf("got %+v, %v", got, err)
	}
}

func TestEpisodeFilesCountsAMultiEpisodeFileForEachEpisode(t *testing.T) {
	e := newEnv(t)
	f, _ := e.addFolder(KindShow)
	e.addRow(row{folder: f.ID, rel: "s01e01-02.mkv", size: 200, quality: "HDTV-720p", title: 7, episodes: "[11,12]"})
	e.addRow(row{folder: f.ID, rel: "s01e01.mkv", size: 120, quality: "Bluray-1080p", title: 7, episodes: "[11]"})
	e.addRow(row{folder: f.ID, rel: "s01e03.mkv", size: 80, quality: "WEBDL-1080p", title: 7, episodes: "[13]", missing: "2026-02-01T00:00:00.000Z"})
	e.addRow(row{folder: f.ID, rel: "other.mkv", size: 80, quality: "WEBDL-1080p", title: 8, episodes: "[14]"})
	e.addRow(row{folder: f.ID, rel: "noeps.mkv", size: 80, quality: "WEBDL-1080p", title: 7}) // matched, episodes unknown

	got, err := e.svc.EpisodeFiles(context.Background(), 7)
	if err != nil {
		t.Fatal(err)
	}
	if ep := got[11]; ep.Count != 2 || ep.Bytes != 320 || ep.Quality != "Bluray-1080p" {
		t.Errorf("episode 11 = %+v", ep)
	}
	if ep := got[12]; ep.Count != 1 || ep.Bytes != 200 || ep.Quality != "HDTV-720p" {
		t.Errorf("episode 12 = %+v", ep)
	}
	if _, has := got[13]; has {
		t.Error("a file that is gone from disk counts for its episode")
	}
	if _, has := got[14]; has {
		t.Error("another show's file counts")
	}
	if len(got) != 2 {
		t.Errorf("entries = %+v", got)
	}
}

func TestTrackFiles(t *testing.T) {
	e := newEnv(t)
	f, _ := e.addFolder(KindMusic)
	e.addRow(row{folder: f.ID, rel: "01.flac", size: 30, quality: "FLAC", title: 5, track: 101})
	e.addRow(row{folder: f.ID, rel: "01.mp3", size: 8, quality: "MP3-320kbps", title: 5, track: 101})
	e.addRow(row{folder: f.ID, rel: "02.mp3", size: 7, quality: "MP3-192kbps", title: 5, track: 102})
	e.addRow(row{folder: f.ID, rel: "03.mp3", size: 7, quality: "MP3-192kbps", title: 5}) // track unknown
	got, err := e.svc.TrackFiles(context.Background(), 5)
	if err != nil {
		t.Fatal(err)
	}
	if tr := got[101]; tr.Count != 2 || tr.Bytes != 38 || tr.Quality != "FLAC" {
		t.Errorf("track 101 = %+v", tr)
	}
	if tr := got[102]; tr.Count != 1 || tr.Quality != "MP3-192kbps" {
		t.Errorf("track 102 = %+v", tr)
	}
	if len(got) != 2 {
		t.Errorf("entries = %+v", got)
	}
}

func TestQualityRankOrdersBestLast(t *testing.T) {
	ordered := []string{
		"Unknown", "CAM", "TELESYNC", "TELECINE", "DVDSCR", "SDTV", "DVD",
		"HDTV-720p", "WEBRip-720p", "WEBDL-720p", "Bluray-720p",
		"Unknown-1080p", "HDTV-1080p", "WEBRip-1080p", "WEBDL-1080p", "Bluray-1080p", "Remux-1080p",
		"Bluray-2160p", "Remux-2160p",
	}
	for i := 1; i < len(ordered); i++ {
		if a, b := QualityRank(ordered[i-1]), QualityRank(ordered[i]); a >= b {
			t.Errorf("%q (%d) should rank below %q (%d)", ordered[i-1], a, ordered[i], b)
		}
	}
	// A better resolution beats a better source.
	if QualityRank("WEBDL-2160p") <= QualityRank("Remux-1080p") {
		t.Error("2160p WEB-DL should beat a 1080p remux")
	}
	music := []string{"Unknown", "MP3-128kbps", "MP3-320kbps", "FLAC", "FLAC-24bit"}
	for i := 1; i < len(music); i++ {
		if a, b := QualityRank(music[i-1]), QualityRank(music[i]); a >= b {
			t.Errorf("%q (%d) should rank below %q (%d)", music[i-1], a, music[i], b)
		}
	}
}
