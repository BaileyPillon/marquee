package library

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"testing"
)

func mkdir(t *testing.T, parts ...string) string {
	t.Helper()
	p := filepath.Join(parts...)
	if err := os.MkdirAll(p, 0o755); err != nil {
		t.Fatal(err)
	}
	return p
}

func TestAddFolderValidation(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	base := t.TempDir()
	movies := mkdir(t, base, "media", "movies")
	shows := mkdir(t, base, "media", "shows")
	file := filepath.Join(base, "afile.txt")
	if err := os.WriteFile(file, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}

	cases := []struct {
		name       string
		kind, path string
		want       error
	}{
		{"bad kind", "game", movies, ErrInvalidKind},
		{"empty kind", "", movies, ErrInvalidKind},
		{"empty path", KindMovie, "", ErrInvalidPath},
		{"blank path", KindMovie, "   ", ErrInvalidPath},
		{"relative path", KindMovie, filepath.Join("media", "movies"), ErrInvalidPath},
		{"dot path", KindMovie, ".", ErrInvalidPath},
		{"missing folder", KindMovie, filepath.Join(base, "nowhere"), ErrPathNotFound},
		{"a file, not a folder", KindMovie, file, ErrNotDirectory},
	}
	for _, c := range cases {
		if _, err := e.svc.AddFolder(ctx, c.kind, c.path, ""); !errors.Is(err, c.want) {
			t.Errorf("%s: err = %v, want %v", c.name, err, c.want)
		}
	}
	if folders, _ := e.svc.Folders(ctx); len(folders) != 0 {
		t.Fatalf("a rejected folder was stored: %+v", folders)
	}

	// A good one, then the nesting rules against it.
	f, err := e.svc.AddFolder(ctx, KindMovie, movies, "Films")
	if err != nil || f.ID == 0 || f.Kind != KindMovie || f.Path != movies || f.Label != "Films" || f.CreatedAt.IsZero() {
		t.Fatalf("AddFolder = %+v, %v", f, err)
	}
	inside := mkdir(t, movies, "Silent")
	deep := mkdir(t, movies, "Silent", "1920s")
	for _, p := range []string{inside, deep} {
		if _, err := e.svc.AddFolder(ctx, KindMovie, p, ""); !errors.Is(err, ErrNested) {
			t.Errorf("inside %q: err = %v, want ErrNested", p, err)
		}
		if _, err := e.svc.AddFolder(ctx, KindShow, p, ""); !errors.Is(err, ErrNested) {
			t.Errorf("inside %q as another kind: err = %v, want ErrNested", p, err)
		}
	}
	if _, err := e.svc.AddFolder(ctx, KindMovie, filepath.Dir(movies), ""); !errors.Is(err, ErrContainsFolder) {
		t.Errorf("parent of a library folder: err = %v, want ErrContainsFolder", err)
	}
	if _, err := e.svc.AddFolder(ctx, KindMovie, movies, ""); !errors.Is(err, ErrDuplicate) {
		t.Errorf("same folder again: err = %v, want ErrDuplicate", err)
	}
	// The same folder written differently is still the same folder.
	if _, err := e.svc.AddFolder(ctx, KindMovie, movies+string(filepath.Separator), ""); !errors.Is(err, ErrDuplicate) {
		t.Errorf("trailing separator: err = %v, want ErrDuplicate", err)
	}
	if _, err := e.svc.AddFolder(ctx, KindMovie, filepath.Join(movies, "Silent", ".."), ""); !errors.Is(err, ErrDuplicate) {
		t.Errorf("with ..: err = %v, want ErrDuplicate", err)
	}
	// A sibling is fine, as is a folder whose name merely starts the same.
	if _, err := e.svc.AddFolder(ctx, KindShow, shows, ""); err != nil {
		t.Errorf("sibling: %v", err)
	}
	lookalike := mkdir(t, base, "media", "movies-4k")
	if _, err := e.svc.AddFolder(ctx, KindMovie, lookalike, "4K"); err != nil {
		t.Errorf("a name that only starts the same: %v", err)
	}
	if folders, _ := e.svc.Folders(ctx); len(folders) != 3 {
		t.Errorf("folders = %d, want 3", len(folders))
	}
}

func TestAddFolderLabels(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	base := t.TempDir()
	a := mkdir(t, base, "Open Movies")
	b := mkdir(t, base, "b")
	c := mkdir(t, base, "c")
	if f, err := e.svc.AddFolder(ctx, KindMovie, a, ""); err != nil || f.Label != "Open Movies" {
		t.Errorf("default label = %q, %v", f.Label, err)
	}
	if f, err := e.svc.AddFolder(ctx, KindMovie, b, "  Trimmed  "); err != nil || f.Label != "Trimmed" {
		t.Errorf("label = %q, %v", f.Label, err)
	}
	if f, err := e.svc.AddFolder(ctx, KindMovie, c, strings.Repeat("x", 500)); err != nil || len([]rune(f.Label)) != maxLabelRunes {
		t.Errorf("long label = %d runes, %v", len([]rune(f.Label)), err)
	}
}

func TestNestingThroughASymlink(t *testing.T) {
	base := t.TempDir()
	real := mkdir(t, base, "real")
	link := filepath.Join(base, "link")
	if err := os.Symlink(real, link); err != nil {
		t.Skipf("cannot make symlinks here: %v", err)
	}
	e := newEnv(t)
	ctx := context.Background()
	if _, err := e.svc.AddFolder(ctx, KindMovie, real, ""); err != nil {
		t.Fatal(err)
	}
	if _, err := e.svc.AddFolder(ctx, KindMovie, link, ""); !errors.Is(err, ErrDuplicate) {
		t.Errorf("a link to a library folder: err = %v, want ErrDuplicate", err)
	}
}

func TestWithin(t *testing.T) {
	sep := string(filepath.Separator)
	root := filepath.Join(sep, "a", "b")
	if runtime.GOOS == "windows" {
		root = `C:\a\b`
	}
	for _, c := range []struct {
		child string
		want  bool
	}{
		{root, true},
		{root + sep + "c", true},
		{root + sep + "c" + sep + "d", true},
		{root + "x", false},
		{root + sep + ".." + sep + "b2", false},
		{filepath.Dir(root), false},
	} {
		if got := within(root, filepath.Clean(c.child)); got != c.want {
			t.Errorf("within(%q, %q) = %v, want %v", root, c.child, got, c.want)
		}
	}
	if runtime.GOOS == "windows" && !within(`C:\Media`, `c:\media\Movies`) {
		t.Error("Windows paths are compared without regard to case")
	}
	if runtime.GOOS == "windows" && within(`C:\Media`, `D:\Media\Movies`) {
		t.Error("different drives never nest")
	}
}

func TestDiskSpace(t *testing.T) {
	sp, err := DiskSpace(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if sp.Free == 0 || sp.Total == 0 || sp.Free > sp.Total {
		t.Errorf("DiskSpace = %+v, want 0 < free <= total", sp)
	}
	if _, err := DiskSpace(filepath.Join(t.TempDir(), "does", "not", "exist")); err == nil {
		t.Error("a missing path should be an error")
	}
}

func TestFoldersReportCountsAndFreeSpace(t *testing.T) {
	e := newEnv(t)
	ts := movieTitles()
	e.svc.SetTitleSource(ts)
	f, root := e.addFolder(KindMovie,
		"Metropolis (1927)/Metropolis (1927).mkv", "Nosferatu (1922)/Nosferatu (1922).mkv", "Unknown (2001)/Unknown (2001).mkv")
	empty, _ := e.addFolder(KindShow)
	e.scan(f)
	if err := os.WriteFile(filepath.Join(root, "Metropolis (1927)", "Metropolis (1927).mkv"), []byte("12345"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(filepath.Join(root, "Unknown (2001)", "Unknown (2001).mkv")); err != nil {
		t.Fatal(err)
	}
	e.scan(f)

	infos, err := e.svc.Folders(context.Background())
	if err != nil || len(infos) != 2 {
		t.Fatalf("Folders = %+v, %v", infos, err)
	}
	got := infos[0]
	if got.ID != f.ID || got.Kind != KindMovie || got.Path != root {
		t.Errorf("folder = %+v", got.Folder)
	}
	if got.Files != 2 || got.Matched != 2 || got.Unmatched != 0 || got.Missing != 1 || got.Bytes != 5 || got.Ignored != 0 {
		t.Errorf("counts = %+v, want 2 files (5 bytes), 2 matched, 1 missing", got.FolderCounts)
	}
	if got.FreeBytes == nil || *got.FreeBytes == 0 || got.TotalBytes == nil || *got.TotalBytes < *got.FreeBytes {
		t.Errorf("free %v total %v", got.FreeBytes, got.TotalBytes)
	}
	if got.Scanning {
		t.Error("nothing is scanning")
	}
	if infos[1].ID != empty.ID || infos[1].Files != 0 {
		t.Errorf("empty folder = %+v", infos[1])
	}

	// A folder whose disk cannot be read has no free-space numbers, and still lists.
	if err := os.RemoveAll(root); err != nil {
		t.Fatal(err)
	}
	infos, err = e.svc.Folders(context.Background())
	if err != nil || infos[0].FreeBytes != nil || infos[0].TotalBytes != nil {
		t.Errorf("gone folder = %+v, %v", infos[0], err)
	}
}

func TestRemoveFolderDropsRowsButNeverFiles(t *testing.T) {
	e := newEnv(t)
	f, root := e.addFolder(KindMovie, "Metropolis (1927)/Metropolis (1927).mkv")
	other, _ := e.addFolder(KindMovie, "Sintel (2010)/Sintel (2010).mkv")
	e.scan(f)
	e.scan(other)
	if err := e.svc.RemoveFolder(context.Background(), f.ID); err != nil {
		t.Fatal(err)
	}
	if n := len(e.files(f)); n != 0 {
		t.Errorf("%d rows left for a removed folder", n)
	}
	if n := len(e.files(other)); n != 1 {
		t.Errorf("another folder lost its rows: %d", n)
	}
	if _, err := os.Stat(filepath.Join(root, "Metropolis (1927)", "Metropolis (1927).mkv")); err != nil {
		t.Errorf("removing a folder deleted a file: %v", err)
	}
	if err := e.svc.RemoveFolder(context.Background(), f.ID); !errors.Is(err, ErrNotFound) {
		t.Errorf("removing twice: %v", err)
	}
	if _, err := e.svc.Folder(context.Background(), f.ID); !errors.Is(err, ErrNotFound) {
		t.Errorf("Folder after removal: %v", err)
	}
	// The path can be added again.
	if _, err := e.svc.AddFolder(context.Background(), KindShow, root, ""); err != nil {
		t.Errorf("re-adding: %v", err)
	}
}

func TestListFilesFilters(t *testing.T) {
	e := newEnv(t)
	e.svc.SetTitleSource(movieTitles())
	a, aRoot := e.addFolder(KindMovie,
		"Metropolis (1927)/Metropolis (1927).mkv", "Nosferatu (1922)/Nosferatu (1922).mkv", "Zed (2001)/Zed (2001).mkv", "Yak (2002)/Yak (2002).mkv")
	b, _ := e.addFolder(KindMovie, "Sintel (2010)/Sintel (2010).mkv", "Nobody (1999)/Nobody (1999).mkv")
	e.scan(a)
	e.scan(b)
	if err := os.Remove(filepath.Join(aRoot, "Yak (2002)", "Yak (2002).mkv")); err != nil {
		t.Fatal(err)
	}
	e.scan(a)
	ctx := context.Background()

	count := func(f FileFilter) (int, int) {
		t.Helper()
		list, total, err := e.svc.Files(ctx, f)
		if err != nil {
			t.Fatal(err)
		}
		return len(list), total
	}
	if n, total := count(FileFilter{}); n != 6 || total != 6 {
		t.Errorf("all = %d/%d, want 6 (missing rows included)", n, total)
	}
	if n, total := count(FileFilter{State: StateMatched}); n != 3 || total != 3 {
		t.Errorf("matched = %d/%d, want 3", n, total)
	}
	if n, total := count(FileFilter{State: StateUnmatched}); n != 2 || total != 2 {
		t.Errorf("unmatched = %d/%d, want 2 (the missing one is not listed)", n, total)
	}
	if n, total := count(FileFilter{State: "missing"}); n != 1 || total != 1 {
		t.Errorf("missing = %d/%d, want 1", n, total)
	}
	if n, total := count(FileFilter{State: StateIgnored}); n != 0 || total != 0 {
		t.Errorf("ignored = %d/%d", n, total)
	}
	if n, total := count(FileFilter{FolderID: b.ID}); n != 2 || total != 2 {
		t.Errorf("folder b = %d/%d", n, total)
	}
	if n, total := count(FileFilter{FolderID: a.ID, State: StateUnmatched}); n != 1 || total != 1 {
		t.Errorf("folder a unmatched = %d/%d", n, total)
	}
	// Paging: a page of two out of six, in a stable order.
	page1, total, _ := e.svc.Files(ctx, FileFilter{Limit: 2})
	page2, _, _ := e.svc.Files(ctx, FileFilter{Limit: 2, Offset: 2})
	page3, _, _ := e.svc.Files(ctx, FileFilter{Limit: 2, Offset: 4})
	if total != 6 || len(page1) != 2 || len(page2) != 2 || len(page3) != 2 {
		t.Fatalf("pages = %d %d %d of %d", len(page1), len(page2), len(page3), total)
	}
	var seen []string
	for _, p := range [][]File{page1, page2, page3} {
		for _, f := range p {
			seen = append(seen, f.RelPath)
		}
	}
	if len(slices.Compact(sorted(seen))) != 6 {
		t.Errorf("pages overlap: %q", seen)
	}
	if rest, _, _ := e.svc.Files(ctx, FileFilter{Limit: 2, Offset: 99}); len(rest) != 0 {
		t.Errorf("past the end = %d rows", len(rest))
	}
	if list, _, _ := e.svc.Files(ctx, FileFilter{}); len(list) == 0 || list[0].EpisodeIDs != nil {
		t.Error("EpisodeIDs must be nil for a movie")
	}
}

func TestStats(t *testing.T) {
	e := newEnv(t)
	ts := newFakeTitles()
	ts.cands[CandidateMovie] = []Candidate{{ID: 1, Kind: "movie", Name: "Metropolis", Year: 1927}}
	ts.cands[CandidateShow] = []Candidate{{ID: 20, Kind: "show", Name: "Pioneer One", Year: 2010}}
	ts.eps[epKey{20, 1, 1}] = 201
	e.svc.SetTitleSource(ts)

	mov, movRoot := e.addFolder(KindMovie, "Metropolis (1927)/Metropolis (1927).mkv", "Nobody (1999)/Nobody (1999).mkv")
	shw, _ := e.addFolder(KindShow, "Pioneer One (2010)/Season 1/Pioneer One - S01E01.mkv", "Pioneer One (2010)/Season 1/Pioneer One - S01E02.mkv")
	mus, _ := e.addFolder(KindMusic, "Chopin/Ballades/01 Ballade.flac")
	for _, f := range []Folder{mov, shw, mus} {
		e.scan(f)
	}
	// Give the files sizes: 10, 20, 30, 40, 50 bytes by row order.
	rows, _, _ := e.svc.Files(context.Background(), FileFilter{})
	for i, r := range rows {
		if _, err := e.db.Exec(`UPDATE files SET size = ? WHERE id = ?`, (i+1)*10, r.ID); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.Remove(filepath.Join(movRoot, "Nobody (1999)", "Nobody (1999).mkv")); err != nil {
		t.Fatal(err)
	}
	e.scan(mov)

	st, err := e.svc.Stats(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if st.Files != 4 || st.Missing != 1 {
		t.Errorf("stats = %+v, want 4 files and 1 missing", st)
	}
	if st.States[StateMatched] != 2 || st.States[StateUnmatched] != 2 || st.States[StateIgnored] != 0 {
		t.Errorf("states = %v", st.States)
	}
	for _, kind := range []string{KindMovie, KindShow, KindMusic} {
		if _, ok := st.Kinds[kind]; !ok {
			t.Errorf("kind %s missing from %+v", kind, st.Kinds)
		}
	}
	if k := st.Kinds[KindMovie]; k.Files != 1 || k.Matched != 1 || k.Missing != 1 || k.Unmatched != 0 {
		t.Errorf("movie stats = %+v", k)
	}
	if k := st.Kinds[KindShow]; k.Files != 2 || k.Matched != 1 || k.Unmatched != 1 {
		t.Errorf("show stats = %+v", k)
	}
	if k := st.Kinds[KindMusic]; k.Files != 1 || k.Unmatched != 1 {
		t.Errorf("music stats = %+v", k)
	}
	var sum int64
	for _, k := range st.Kinds {
		sum += k.Bytes
	}
	if sum != st.Bytes || st.Bytes == 0 {
		t.Errorf("bytes: kinds add up to %d, total %d", sum, st.Bytes)
	}

	// An empty library has zeroes, not missing keys.
	empty, err := newEnv(t).svc.Stats(context.Background())
	if err != nil || empty.Files != 0 || len(empty.Kinds) != 3 || len(empty.States) != 3 {
		t.Errorf("empty stats = %+v, %v", empty, err)
	}
}
