package library

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

func TestWantMediaAndSkipFolderNameAgreeWithTheScanner(t *testing.T) {
	yes := map[string]string{"a.mkv": "movie", "A.MP4": "show", "b.flac": "music", "c.mp3": "music"}
	for name, kind := range yes {
		if !WantMedia(name, kind) {
			t.Errorf("WantMedia(%q, %q) = false", name, kind)
		}
	}
	no := map[string]string{
		"a.nfo": "movie", ".hidden.mkv": "movie", "a.mkv.part": "movie", "a.!qb": "show",
		"Movie.Name.2019.1080p.BluRay.x264-GRP-sample.mkv": "movie", "sample.mkv": "show", "a.flac": "movie", "a.mkv": "music", "x.exe": "movie", "x.rar": "show",
	}
	for name, kind := range no {
		if WantMedia(name, kind) {
			t.Errorf("WantMedia(%q, %q) = true", name, kind)
		}
	}
	for _, d := range []string{"Sample", "samples", "Extras", "Featurettes", "@eaDir", ".git", "#recycle"} {
		if !SkipFolderName(d) {
			t.Errorf("SkipFolderName(%q) = false", d)
		}
	}
	if SkipFolderName("Season 1") || SkipFolderName("Movies") {
		t.Error("ordinary folders are not skipped")
	}
}

func TestImportFilesRecordsMatchedFilesWithoutAScan(t *testing.T) {
	e := newEnv(t)
	f, root := e.addFolder("show")
	mkFiles(t, root, "Pioneer One (2010)/Season 01/Pioneer One - S01E01 - Earthfall Bluray-1080p.mkv")
	rel := "Pioneer One (2010)/Season 01/Pioneer One - S01E01 - Earthfall Bluray-1080p.mkv"

	err := e.svc.ImportFiles(context.Background(), f.ID, []ImportedFile{{RelPath: rel, TitleID: 42, EpisodeIDs: []int64{7}, Quality: "Bluray-1080p"}})
	if err != nil {
		t.Fatal(err)
	}
	rows := e.files(f)
	row, ok := rows[rel]
	if !ok || len(rows) != 1 {
		t.Fatalf("rows = %v", keys(rows))
	}
	if row.State != StateMatched || row.TitleID == nil || *row.TitleID != 42 || !reflect.DeepEqual(row.EpisodeIDs, []int64{7}) ||
		row.Quality != "Bluray-1080p" || row.Kind != KindShow || row.MissingSince != nil {
		t.Errorf("row = %+v", row)
	}
	if !row.MTime.Equal(fixedTime) {
		t.Errorf("the modified time should come from the file on disk, got %v", row.MTime)
	}
	if len(row.Parsed) < 3 {
		t.Errorf("parsed = %s", row.Parsed)
	}

	// With no title source set, a scan would call this file unmatched; it must leave the imported row alone.
	sum := e.scan(f)
	if sum.Added != 0 || sum.Updated != 0 || sum.Unchanged != 1 {
		t.Errorf("scan after import = %+v", sum)
	}
	if after := e.files(f)[rel]; after.State != StateMatched || after.TitleID == nil || *after.TitleID != 42 {
		t.Errorf("a scan changed the imported row: %+v", after)
	}
	// Importing the same file again just refreshes the row.
	if err := e.svc.ImportFiles(context.Background(), f.ID, []ImportedFile{{RelPath: rel, TitleID: 42, EpisodeIDs: []int64{7}}}); err != nil {
		t.Fatal(err)
	}
	if n := len(e.files(f)); n != 1 {
		t.Errorf("rows = %d", n)
	}
}

func TestImportFilesMovieAndMusicKinds(t *testing.T) {
	e := newEnv(t)
	mf, mroot := e.addFolder("movie")
	mkFiles(t, mroot, "Metropolis (1927)/Metropolis (1927) DVD.mkv")
	if err := e.svc.ImportFiles(context.Background(), mf.ID, []ImportedFile{{RelPath: "Metropolis (1927)/Metropolis (1927) DVD.mkv", TitleID: 3}}); err != nil {
		t.Fatal(err)
	}
	row := e.files(mf)["Metropolis (1927)/Metropolis (1927) DVD.mkv"]
	if row.State != StateMatched || row.Quality != "DVD" || row.EpisodeIDs != nil {
		t.Errorf("movie row = %+v", row)
	}

	uf, uroot := e.addFolder("music")
	mkFiles(t, uroot, "Artist/Album (2000)/01 - Song.flac")
	if err := e.svc.ImportFiles(context.Background(), uf.ID, []ImportedFile{{RelPath: "Artist/Album (2000)/01 - Song.flac", TitleID: 9, Quality: "FLAC"}}); err != nil {
		t.Fatal(err)
	}
	if row := e.files(uf)["Artist/Album (2000)/01 - Song.flac"]; row.State != StateMatched || row.Kind != KindMusic || row.Quality != "FLAC" {
		t.Errorf("music row = %+v", row)
	}
}

func TestImportFilesRefusesBadInputButKeepsTheGoodOnes(t *testing.T) {
	e := newEnv(t)
	f, root := e.addFolder("movie")
	mkFiles(t, root, "Good (2000)/Good (2000).mkv")
	outside := filepath.Join(filepath.Dir(root), "outside.mkv")
	if err := os.WriteFile(outside, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}

	err := e.svc.ImportFiles(context.Background(), f.ID, []ImportedFile{
		{RelPath: "Good (2000)/Good (2000).mkv", TitleID: 1},
		{RelPath: "../outside.mkv", TitleID: 1},
		{RelPath: "/etc/passwd", TitleID: 1},
		{RelPath: `C:\x.mkv`, TitleID: 1},
		{RelPath: "missing.mkv", TitleID: 1},
		{RelPath: "Good (2000)/Good (2000).mkv"}, // no title
		{RelPath: ""},
	})
	if err == nil {
		t.Fatal("bad paths should be reported")
	}
	if !errors.Is(err, ErrBadRelPath) {
		t.Errorf("error = %v", err)
	}
	if rows := e.files(f); len(rows) != 1 {
		t.Errorf("only the good file should be recorded: %v", keys(rows))
	}
	if err := e.svc.ImportFiles(context.Background(), 999, nil); !errors.Is(err, ErrNotFound) {
		t.Errorf("unknown folder: %v", err)
	}
}

func TestForgetFiles(t *testing.T) {
	e := newEnv(t)
	f, root := e.addFolder("movie")
	mkFiles(t, root, "A (2000)/A (2000) WEBDL-720p.mkv", "A (2000)/A (2000) Bluray-1080p.mkv")
	e.scan(f)
	if n := len(e.files(f)); n != 2 {
		t.Fatalf("rows = %d", n)
	}
	if err := e.svc.ForgetFiles(context.Background(), f.ID, []string{"A (2000)/A (2000) WEBDL-720p.mkv", "../nope", "not-a-row.mkv"}); err != nil {
		t.Fatal(err)
	}
	rows := e.files(f)
	if _, still := rows["A (2000)/A (2000) WEBDL-720p.mkv"]; still || len(rows) != 1 {
		t.Errorf("rows = %v", keys(rows))
	}
	if _, err := os.Stat(filepath.Join(root, "A (2000)", "A (2000) WEBDL-720p.mkv")); err != nil {
		t.Error("ForgetFiles never touches the disk")
	}
}

func TestFileFilterByTitle(t *testing.T) {
	e := newEnv(t)
	f, root := e.addFolder("movie")
	mkFiles(t, root, "A (2000)/A (2000).mkv", "B (2001)/B (2001).mkv", "C (2002)/C (2002).mkv")
	for i, rel := range []string{"A (2000)/A (2000).mkv", "B (2001)/B (2001).mkv", "C (2002)/C (2002).mkv"} {
		title := int64(10)
		if i == 2 {
			title = 20
		}
		if err := e.svc.ImportFiles(context.Background(), f.ID, []ImportedFile{{RelPath: rel, TitleID: title}}); err != nil {
			t.Fatal(err)
		}
	}
	list, total, err := e.svc.Files(context.Background(), FileFilter{FolderID: f.ID, TitleID: 10})
	if err != nil || total != 2 || len(list) != 2 {
		t.Errorf("title 10: %d of %d, %v", len(list), total, err)
	}
	if _, total, _ := e.svc.Files(context.Background(), FileFilter{TitleID: 99}); total != 0 {
		t.Errorf("an unknown title has no files, got %d", total)
	}
	if _, total, _ := e.svc.Files(context.Background(), FileFilter{}); total != 3 {
		t.Errorf("no title filter means all, got %d", total)
	}
}
