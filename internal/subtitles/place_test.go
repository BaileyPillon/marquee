package subtitles

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"unicode/utf16"
)

func TestSubtitlePath(t *testing.T) {
	cases := []struct {
		video, lang string
		hi          bool
		want        string
	}{
		{"/m/Movie (1999)/Movie (1999).mkv", "en", false, "/m/Movie (1999)/Movie (1999).en.srt"},
		{"/m/Movie (1999)/Movie (1999).mkv", "en", true, "/m/Movie (1999)/Movie (1999).en.hi.srt"},
		{"/m/Show/Season 01/Show - S01E02 - Name.mp4", "pt-BR", false, "/m/Show/Season 01/Show - S01E02 - Name.pt-br.srt"},
		{"/m/Dotted.Name.1999.1080p.mkv", "FR", false, "/m/Dotted.Name.1999.1080p.fr.srt"},
		{"/m/NoExtension", "de", false, "/m/NoExtension.de.srt"},
	}
	for _, tc := range cases {
		if got := SubtitlePath(filepath.FromSlash(tc.video), tc.lang, tc.hi); got != filepath.FromSlash(tc.want) {
			t.Errorf("SubtitlePath(%q, %q, %v) = %q, want %q", tc.video, tc.lang, tc.hi, got, tc.want)
		}
	}
}

func TestWriteSubtitleNewFile(t *testing.T) {
	p := filepath.Join(t.TempDir(), "Movie.en.srt")
	if err := WriteSubtitle(p, []byte(srtOne), false); err != nil {
		t.Fatal(err)
	}
	if b, _ := os.ReadFile(p); string(b) != srtOne {
		t.Errorf("content = %q", b)
	}
}

func TestWriteSubtitleNeverReplacesAForeignFile(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "Movie.en.srt")
	if err := os.WriteFile(p, []byte("someone else's subtitle"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := WriteSubtitle(p, []byte(srtOne), false); !errors.Is(err, ErrForeignFile) {
		t.Fatalf("err = %v, want ErrForeignFile", err)
	}
	if b, _ := os.ReadFile(p); string(b) != "someone else's subtitle" {
		t.Errorf("the foreign file was changed: %q", b)
	}
	// Not even a symbolic link in its place, whoever it points to.
	if runtimeSymlinks(t, dir) {
		link := filepath.Join(dir, "Other.en.srt")
		if err := os.Symlink(p, link); err == nil {
			if err := WriteSubtitle(link, []byte(srtOne), true); !errors.Is(err, ErrForeignFile) {
				t.Errorf("a symlink was written through: %v", err)
			}
		}
	}
	if entries, _ := os.ReadDir(dir); len(entries) > 2 {
		t.Errorf("leftover files: %v", entries)
	}
}

// runtimeSymlinks reports whether this machine lets the test make a symlink
// (Windows needs a privilege for it).
func runtimeSymlinks(t *testing.T, dir string) bool {
	t.Helper()
	probe := filepath.Join(dir, "probe-target")
	if err := os.WriteFile(probe, nil, 0o644); err != nil {
		return false
	}
	defer os.Remove(probe)
	link := filepath.Join(dir, "probe-link")
	if err := os.Symlink(probe, link); err != nil {
		return false
	}
	_ = os.Remove(link)
	return true
}

func TestWriteSubtitleReplacesOurOwnFile(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "Movie.en.srt")
	if err := WriteSubtitle(p, []byte("old"), false); err != nil {
		t.Fatal(err)
	}
	if err := WriteSubtitle(p, []byte(srtOne), true); err != nil {
		t.Fatal(err)
	}
	if b, _ := os.ReadFile(p); string(b) != srtOne {
		t.Errorf("content = %q", b)
	}
	if entries, _ := os.ReadDir(dir); len(entries) != 1 {
		t.Errorf("a temporary file was left behind: %v", entries)
	}
}

func TestWriteSubtitleMissingFolder(t *testing.T) {
	if err := WriteSubtitle(filepath.Join(t.TempDir(), "no", "such", "Movie.en.srt"), []byte(srtOne), false); err == nil {
		t.Error("a missing folder should be an error")
	}
}

func TestExtractSRT(t *testing.T) {
	// The biggest .srt of a zip, not the nfo beside it.
	data := zipOf(map[string]string{"readme.nfo": "hello", "a.srt": "small", "b.SRT": srtOne})
	got, err := ExtractSRT(data)
	if err != nil || string(got) != srtOne {
		t.Errorf("ExtractSRT = %q, %v", got, err)
	}
	// Not a zip: handed back as it is.
	if got, err := ExtractSRT([]byte(srtOne)); err != nil || string(got) != srtOne {
		t.Errorf("plain text: %q, %v", got, err)
	}
	// A zip with no subtitle.
	if _, err := ExtractSRT(zipOf(map[string]string{"x.nfo": "nothing"})); err == nil || !strings.Contains(err.Error(), "no .srt") {
		t.Errorf("no srt: %v", err)
	}
	// A broken zip.
	if _, err := ExtractSRT(append([]byte("PK\x03\x04"), bytes.Repeat([]byte{0xff}, 40)...)); err == nil {
		t.Error("a broken archive should be an error")
	}
	// An .srt that would unpack to more than a subtitle can be.
	big := zipOf(map[string]string{"huge.srt": strings.Repeat("1\n00:00:01,000 --> 00:00:02,000\nx\n\n", 200000)})
	if _, err := ExtractSRT(big); err == nil || !strings.Contains(err.Error(), "too big") {
		t.Errorf("an oversized subtitle: %v", err)
	}
	// Too many entries.
	many := map[string]string{}
	for i := 0; i < 250; i++ {
		many[strings.Repeat("a", i%7+1)+string(rune('A'+i%26))+string(rune('a'+i/26))+".txt"] = "x"
	}
	if _, err := ExtractSRT(zipOf(many)); err == nil {
		t.Error("an archive of hundreds of files should be refused")
	}
}

func TestToUTF8(t *testing.T) {
	utf16le := func(s string, bom bool) []byte {
		var b []byte
		if bom {
			b = []byte{0xFF, 0xFE}
		}
		for _, u := range utf16.Encode([]rune(s)) {
			b = append(b, byte(u), byte(u>>8))
		}
		return b
	}
	utf16be := func(s string) []byte {
		b := []byte{0xFE, 0xFF}
		for _, u := range utf16.Encode([]rune(s)) {
			b = append(b, byte(u>>8), byte(u))
		}
		return b
	}
	cases := []struct {
		name string
		in   []byte
		want string
	}{
		{"plain ASCII", []byte("Hello"), "Hello"},
		{"valid UTF-8 is kept", []byte("Café ñ — ok"), "Café ñ — ok"},
		{"a UTF-8 byte-order mark is dropped", append([]byte{0xEF, 0xBB, 0xBF}, "Café"...), "Café"},
		{"UTF-16 little endian", utf16le("Café “quoted”", true), "Café “quoted”"},
		{"UTF-16 big endian", utf16be("Café"), "Café"},
		{"Windows-1252 letters", []byte{'C', 'a', 'f', 0xE9, ' ', 0xF1}, "Café ñ"},
		{"Windows-1252 smart quotes and dashes", []byte{0x93, 'h', 'i', 0x94, ' ', 0x96, ' ', 0x92, 0x80}, "“hi” – ’€"},
		{"Windows-1252 Latin-1 range", []byte{0xFC, 0xDF}, "üß"},
	}
	for _, tc := range cases {
		if got := string(ToUTF8(tc.in)); got != tc.want {
			t.Errorf("%s: %q, want %q", tc.name, got, tc.want)
		}
	}
}

func TestCheckSubtitle(t *testing.T) {
	if err := CheckSubtitle([]byte(srtOne)); err != nil {
		t.Errorf("a subtitle: %v", err)
	}
	for name, in := range map[string]string{
		"empty": "", "spaces": "  \n ", "a web page": "<html><body>Error 500</body></html>", "plain text": "hello world",
	} {
		if err := CheckSubtitle([]byte(in)); !errors.Is(err, ErrNotSubtitle) {
			t.Errorf("%s: %v, want ErrNotSubtitle", name, err)
		}
	}
}

func TestNormalizeLanguage(t *testing.T) {
	for in, want := range map[string]string{
		"en": "en", " EN ": "en", "eng": "en", "fre": "fr", "pt_BR": "pt-br", "pt-BR": "pt-br", "zh-CN": "zh-cn", "": "",
	} {
		if got := normalizeLanguage(in); got != want {
			t.Errorf("normalizeLanguage(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestExternalSubtitles(t *testing.T) {
	dir := t.TempDir()
	video := filepath.Join(dir, "Movie (1999).mkv")
	for _, n := range []string{
		"Movie (1999).mkv", "Movie (1999).en.srt", "Movie (1999).en.hi.srt", "Movie (1999).eng.srt", "Movie (1999).fr.forced.srt",
		"Movie (1999).de.ass", "Movie (1999).srt", "Movie (1999) Extra.es.srt", "Other (1999).it.srt", "Movie (1999).pt-BR.srt",
		"Movie (1999).en.sdh.srt", "movie (1999).es.SRT", "Movie (1999).txt",
	} {
		if err := os.WriteFile(filepath.Join(dir, n), nil, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	got := map[string]External{}
	for _, x := range externalSubtitles(video, newDirCache()) {
		got[x.Name] = x
	}
	want := map[string]External{
		"Movie (1999).en.srt":        {Language: "en"},
		"Movie (1999).en.hi.srt":     {Language: "en", HI: true},
		"Movie (1999).en.sdh.srt":    {Language: "en", HI: true},
		"Movie (1999).eng.srt":       {Language: "en"},
		"Movie (1999).fr.forced.srt": {Language: "fr", Forced: true},
		"Movie (1999).de.ass":        {Language: "de"},
		"Movie (1999).pt-BR.srt":     {Language: "pt-br"},
		"movie (1999).es.SRT":        {Language: "es"},
	}
	for name, w := range want {
		g, ok := got[name]
		w.Name = name
		if !ok || g != w {
			t.Errorf("%s: %+v (found %v), want %+v", name, g, ok, w)
		}
	}
	for name := range got {
		if _, ok := want[name]; !ok && name != "Movie (1999).srt" {
			t.Errorf("%s should not be one of the video's subtitles", name)
		}
	}
	if x, ok := got["Movie (1999).srt"]; !ok || x.Language != "" {
		t.Errorf("a subtitle with no language: %+v, %v", x, ok)
	}
}
