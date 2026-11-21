package importer

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
)

func writeFile(t *testing.T, p string, data []byte) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, data, 0o644); err != nil {
		t.Fatal(err)
	}
}

func mustRead(t *testing.T, p string) []byte {
	t.Helper()
	b, err := os.ReadFile(p)
	if err != nil {
		t.Fatal(err)
	}
	return b
}

func exists(p string) bool { _, err := os.Stat(p); return err == nil }

func TestTransferHardlinksOnTheSameVolume(t *testing.T) {
	dir := t.TempDir()
	src := filepath.Join(dir, "dl", "movie.mkv")
	dst := filepath.Join(dir, "lib", "Movie (2020)", "Movie (2020).mkv")
	writeFile(t, src, []byte("movie data"))

	mode, err := transfer(bg(), src, dst, MethodLink)
	if err != nil {
		t.Fatal(err)
	}
	if mode != ModeHardlink {
		t.Fatalf("mode = %s, want hardlink (a temp dir is one NTFS volume)", mode)
	}
	a, _ := os.Stat(src)
	b, _ := os.Stat(dst)
	if !os.SameFile(a, b) {
		t.Error("the destination should be a second name for the same data")
	}
	if !exists(src) {
		t.Error("a torrent's file must stay where it is, so it keeps seeding")
	}
	if exists(dst + partialSuffix) {
		t.Error("no partial file should be left")
	}
}

func TestTransferFallsBackToCopyWhenTheLinkFails(t *testing.T) {
	old := linkFile
	linkFile = func(string, string) error { return errors.New("invalid cross-device link") }
	t.Cleanup(func() { linkFile = old })

	dir := t.TempDir()
	src := filepath.Join(dir, "dl", "movie.mkv")
	dst := filepath.Join(dir, "lib", "movie.mkv")
	writeFile(t, src, bytes.Repeat([]byte("0123456789"), 300_000)) // a few MB, more than one copy buffer pass

	mode, err := transfer(bg(), src, dst, MethodLink)
	if err != nil {
		t.Fatal(err)
	}
	if mode != ModeCopy {
		t.Fatalf("mode = %s, want copy", mode)
	}
	if !bytes.Equal(mustRead(t, src), mustRead(t, dst)) {
		t.Error("the copy differs from the original")
	}
	a, _ := os.Stat(src)
	b, _ := os.Stat(dst)
	if os.SameFile(a, b) {
		t.Error("a copy must not be the same file")
	}
	if !a.ModTime().Equal(b.ModTime()) {
		t.Errorf("the copy should keep the modified time: %v vs %v", a.ModTime(), b.ModTime())
	}
	if !exists(src) {
		t.Error("the source of a torrent must never be moved, even when copying")
	}
	if exists(dst + partialSuffix) {
		t.Error("no partial file should be left")
	}
}

func TestTransferMoveTakesTheSourceAway(t *testing.T) {
	dir := t.TempDir()
	src := filepath.Join(dir, "usenet", "movie.mkv")
	dst := filepath.Join(dir, "lib", "A", "movie.mkv")
	writeFile(t, src, []byte("usenet data"))

	mode, err := transfer(bg(), src, dst, MethodMove)
	if err != nil {
		t.Fatal(err)
	}
	if mode != ModeMove {
		t.Fatalf("mode = %s, want move", mode)
	}
	if exists(src) {
		t.Error("a Usenet download is moved: the source should be gone")
	}
	if string(mustRead(t, dst)) != "usenet data" {
		t.Error("the destination has the wrong data")
	}
}

func TestTransferReplacesAnExistingFile(t *testing.T) {
	dir := t.TempDir()
	src := filepath.Join(dir, "new.mkv")
	dst := filepath.Join(dir, "lib", "movie.mkv")
	writeFile(t, src, []byte("the better one"))
	writeFile(t, dst, []byte("old"))
	if _, err := transfer(bg(), src, dst, MethodLink); err != nil {
		t.Fatal(err)
	}
	if string(mustRead(t, dst)) != "the better one" {
		t.Error("the old file should have been replaced")
	}
	// And a move over an existing file.
	writeFile(t, src, []byte("third"))
	if _, err := transfer(bg(), src, dst, MethodMove); err != nil {
		t.Fatal(err)
	}
	if string(mustRead(t, dst)) != "third" || exists(src) {
		t.Error("the move should have replaced the file and removed the source")
	}
}

func TestTransferLeavesNothingBehindOnFailure(t *testing.T) {
	old := linkFile
	linkFile = func(string, string) error { return errors.New("no links here") }
	t.Cleanup(func() { linkFile = old })

	dir := t.TempDir()
	dst := filepath.Join(dir, "lib", "movie.mkv")
	if _, err := transfer(bg(), filepath.Join(dir, "missing.mkv"), dst, MethodLink); err == nil {
		t.Fatal("a missing source should fail")
	}
	if exists(dst) || exists(dst+partialSuffix) {
		t.Error("a failed transfer must leave neither the file nor a partial one")
	}
}

func TestTransferStopsWhenCancelled(t *testing.T) {
	old := linkFile
	linkFile = func(string, string) error { return errors.New("no links here") }
	t.Cleanup(func() { linkFile = old })

	dir := t.TempDir()
	src := filepath.Join(dir, "dl", "movie.mkv")
	dst := filepath.Join(dir, "lib", "movie.mkv")
	writeFile(t, src, []byte("data"))
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := transfer(ctx, src, dst, MethodLink); !errors.Is(err, context.Canceled) {
		t.Fatalf("a cancelled copy should fail with the cancellation, got %v", err)
	}
	if exists(dst) || exists(dst+partialSuffix) {
		t.Error("a cancelled copy must leave nothing behind")
	}
	if !exists(src) {
		t.Error("the source stays")
	}
}

func TestRemoveEmptyDirs(t *testing.T) {
	root := filepath.Join(t.TempDir(), "rel")
	if err := os.MkdirAll(filepath.Join(root, "a", "b"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(root, "keep"), 0o755); err != nil {
		t.Fatal(err)
	}
	writeFile(t, filepath.Join(root, "keep", "x.nfo"), []byte("x"))
	removeEmptyDirs(root)
	if exists(filepath.Join(root, "a")) {
		t.Error("empty folders should go")
	}
	if !exists(filepath.Join(root, "keep", "x.nfo")) {
		t.Error("a folder with a file in it must stay")
	}
}
