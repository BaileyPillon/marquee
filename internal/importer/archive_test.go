package importer

import (
	"archive/zip"
	"context"
	"encoding/base64"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const fixtureMKV = "Night.of.the.Living.Dead.1968.1080p.BluRay.x264-GRP.mkv"
const fixtureData = "marquee test fixture: a tiny pretend mkv."

// fixture7z and fixtureRar5 are tiny archives of fixtureMKV with fixtureData in
// it, stored without compression. Marquee has no way to make either format, so
// they were made once with 7-Zip ("7z a -mx0") and WinRAR ("rar a -ep -m0") and
// are kept here as bytes.
const fixture7z = "N3q8ryccAATkI37qKQAAAAAAAACqAAAAAAAAAMCbStdtYXJxdWVlIHRlc3QgZml4dHVyZTogYSB0aW55IHByZXRlbmQgbWt2LgEEBgABCSkABwsBAAEBAAwpAAgKAfRg7YEAAAUBEXEATgBpAGcAaAB0AC4AbwBmAC4AdABoAGUALgBMAGkAdgBpAG4AZwAuAEQAZQBhAGQALgAxADkANgA4AC4AMQAwADgAMABwAC4AQgBsAHUAUgBhAHkALgB4ADIANgA0AC0ARwBSAFAALgBtAGsAdgAAABkCAAAUCgEAsXMrF3153QEVBgEAIAAAAAAA"
const fixtureRar5 = "UmFyIRoHAQDz4YLrCwEFBwAGAQGAgIAAorTHhlMCAwupAASpACD0YO2BgAAAN05pZ2h0Lm9mLnRoZS5MaXZpbmcuRGVhZC4xOTY4LjEwODBwLkJsdVJheS54MjY0LUdSUC5ta3YKAwKxcysXfXndAW1hcnF1ZWUgdGVzdCBmaXh0dXJlOiBhIHRpbnkgcHJldGVuZCBta3YuHXdWUQMFBAA="

func writeB64(t *testing.T, p, b64 string) {
	t.Helper()
	b, err := base64.StdEncoding.DecodeString(b64)
	if err != nil {
		t.Fatal(err)
	}
	writeFile(t, p, b)
}

// makeZip writes a zip file with the given entries (name to content).
func makeZip(t *testing.T, p string, entries map[string]string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		t.Fatal(err)
	}
	f, err := os.Create(p)
	if err != nil {
		t.Fatal(err)
	}
	zw := zip.NewWriter(f)
	for name, data := range entries {
		w, err := zw.Create(name)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := w.Write([]byte(data)); err != nil {
			t.Fatal(err)
		}
	}
	if err := zw.Close(); err != nil {
		t.Fatal(err)
	}
	if err := f.Close(); err != nil {
		t.Fatal(err)
	}
}

func TestArchiveKind(t *testing.T) {
	cases := map[string]string{
		"a.zip": "zip", "A.ZIP": "zip", "a.7z": "7z", "a.7z.001": "7z", "a.rar": "rar",
		"a.part1.rar": "rar", "a.part01.rar": "rar", "a.part001.rar": "rar",
		"a.part2.rar": "", "a.part02.rar": "", "a.part10.rar": "", // only the first volume starts a set
		"a.r00": "", "a.r01": "", "a.7z.002": "", "a.mkv": "", "a.txt": "",
	}
	for name, want := range cases {
		if got := archiveKind(name); got != want {
			t.Errorf("archiveKind(%q) = %q, want %q", name, got, want)
		}
	}
}

func TestEntryPath(t *testing.T) {
	good := map[string]string{"a/b.mkv": "a/b.mkv", `a\b.mkv`: "a/b.mkv", "./a.mkv": "a.mkv", "a/../b.mkv": "b.mkv"}
	for in, want := range good {
		if got, ok := entryPath(in); !ok || got != want {
			t.Errorf("entryPath(%q) = %q, %v; want %q", in, got, ok, want)
		}
	}
	for _, bad := range []string{"", "../evil.mkv", "a/../../evil.mkv", "/etc/passwd", `\evil.mkv`, `C:\evil.mkv`, "x.mkv:stream", ".."} {
		if got, ok := entryPath(bad); ok {
			t.Errorf("entryPath(%q) = %q, should be refused", bad, got)
		}
	}
}

func TestExtractZip(t *testing.T) {
	dir := t.TempDir()
	src := filepath.Join(dir, "dl", "Release")
	makeZip(t, filepath.Join(src, "release.zip"), map[string]string{
		"Night.of.the.Living.Dead.1968/" + fixtureMKV:     fixtureData,
		"Night.of.the.Living.Dead.1968/Sample/sample.mkv": "sample",
		"Night.of.the.Living.Dead.1968/readme.nfo":        "text",
		"Night.of.the.Living.Dead.1968/cover.jpg":         "jpg",
		"../evil.mkv":                  "escape",
		"nested/../../evil2.mkv":       "escape",
		"Extras/behind the scenes.mkv": "extra",
	})
	stage := filepath.Join(dir, "stage")
	files, err := extractArchives(context.Background(), src, stage, "movie")
	if err != nil {
		t.Fatal(err)
	}
	if len(files) != 1 {
		t.Fatalf("extracted %v, want only the film (not the sample, extras, text or escaping entries)", files)
	}
	if got := string(mustRead(t, files[0])); got != fixtureData {
		t.Errorf("content = %q", got)
	}
	if !strings.HasPrefix(files[0], stage) {
		t.Errorf("%s is outside the stage folder", files[0])
	}
	if exists(filepath.Join(dir, "evil.mkv")) || exists(filepath.Join(dir, "evil2.mkv")) || exists(filepath.Join(filepath.Dir(stage), "evil.mkv")) {
		t.Error("an entry escaped the stage folder")
	}
}

func TestExtract7z(t *testing.T) {
	dir := t.TempDir()
	src := filepath.Join(dir, "dl", "rel")
	writeB64(t, filepath.Join(src, "movie.7z"), fixture7z)
	files, err := extractArchives(context.Background(), src, filepath.Join(dir, "stage"), "movie")
	if err != nil {
		t.Fatal(err)
	}
	if len(files) != 1 || filepath.Base(files[0]) != fixtureMKV {
		t.Fatalf("extracted %v", files)
	}
	if got := string(mustRead(t, files[0])); got != fixtureData {
		t.Errorf("content = %q", got)
	}
}

func TestExtractRar(t *testing.T) {
	dir := t.TempDir()
	src := filepath.Join(dir, "dl", "rel")
	writeB64(t, filepath.Join(src, "movie.rar"), fixtureRar5)
	files, err := extractArchives(context.Background(), src, filepath.Join(dir, "stage"), "movie")
	if err != nil {
		t.Fatal(err)
	}
	if len(files) != 1 || filepath.Base(files[0]) != fixtureMKV {
		t.Fatalf("extracted %v", files)
	}
	if got := string(mustRead(t, files[0])); got != fixtureData {
		t.Errorf("content = %q", got)
	}
}

func TestExtractWorksOnASingleArchiveFile(t *testing.T) {
	dir := t.TempDir()
	zipPath := filepath.Join(dir, "dl", "one.zip")
	makeZip(t, zipPath, map[string]string{fixtureMKV: fixtureData})
	files, err := extractArchives(context.Background(), zipPath, filepath.Join(dir, "stage"), "movie")
	if err != nil || len(files) != 1 {
		t.Fatalf("files = %v, err = %v", files, err)
	}
}

func TestExtractBrokenArchiveIsAnError(t *testing.T) {
	dir := t.TempDir()
	src := filepath.Join(dir, "dl")
	writeFile(t, filepath.Join(src, "bad.zip"), []byte("this is not a zip file"))
	if _, err := extractArchives(context.Background(), src, filepath.Join(dir, "stage"), "movie"); err == nil || !strings.Contains(err.Error(), "bad.zip") {
		t.Errorf("a damaged archive should be an error that names it, got %v", err)
	}
	writeFile(t, filepath.Join(src, "bad.7z"), []byte("7z but not really"))
	if _, err := extractArchives(context.Background(), filepath.Join(src, "bad.7z"), filepath.Join(dir, "stage2"), "movie"); err == nil {
		t.Error("a damaged 7z should be an error")
	}
	writeFile(t, filepath.Join(src, "bad.rar"), []byte("Rar! but not really"))
	if _, err := extractArchives(context.Background(), filepath.Join(src, "bad.rar"), filepath.Join(dir, "stage3"), "movie"); err == nil {
		t.Error("a damaged rar should be an error")
	}
}

func TestExtractStopsWhenCancelled(t *testing.T) {
	dir := t.TempDir()
	src := filepath.Join(dir, "dl")
	makeZip(t, filepath.Join(src, "a.zip"), map[string]string{fixtureMKV: fixtureData})
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := extractArchives(ctx, src, filepath.Join(dir, "stage"), "movie"); err == nil {
		t.Error("a cancelled context should stop the extraction")
	}
}
