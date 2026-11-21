package pathmap

import "testing"

func TestApply(t *testing.T) {
	rules := []Rule{
		{From: "/downloads", To: `D:\dl`},
		{From: "/downloads/tv", To: `E:\tv`},
		{From: `M:\MarqueeTest\media`, To: "/media"},
	}
	cases := []struct {
		in, want string
		ok       bool
	}{
		{"/downloads/Movie.2020", `D:\dl\Movie.2020`, true},
		{"/downloads", `D:\dl`, true},
		{"/downloads/tv/Show.S01", `E:\tv\Show.S01`, true}, // the longest From wins
		{"/downloadsX/Movie", "/downloadsX/Movie", false},  // a folder name, not a prefix of text
		{`M:\MarqueeTest\media\movies\A (2020)\A (2020).mkv`, "/media/movies/A (2020)/A (2020).mkv", true},
		{`m:\marqueetest\MEDIA\movies`, "/media/movies", true}, // Windows paths ignore case
		{`M:/MarqueeTest/media/tv`, "/media/tv", true},         // either separator
		{"/elsewhere/x", "/elsewhere/x", false},
	}
	for _, c := range cases {
		got, ok := Apply(rules, c.in)
		if got != c.want || ok != c.ok {
			t.Errorf("Apply(%q) = %q, %v; want %q, %v", c.in, got, ok, c.want, c.ok)
		}
	}
	if got := Map(nil, "/x/y"); got != "/x/y" {
		t.Errorf("no rules should leave the path alone, got %q", got)
	}
}

func TestApplyUnixCaseMatters(t *testing.T) {
	rules := []Rule{{From: "/Data", To: "/mnt"}}
	if got, ok := Apply(rules, "/data/x"); ok {
		t.Errorf("a Unix path is case sensitive; got %q", got)
	}
	if got, _ := Apply(rules, "/Data/x"); got != "/mnt/x" {
		t.Errorf("got %q", got)
	}
}

func TestApplyDriveRoot(t *testing.T) {
	got, ok := Apply([]Rule{{From: "/data", To: `C:\`}}, "/data/a/b.mkv")
	if !ok || got != `C:\a\b.mkv` {
		t.Errorf("got %q, %v", got, ok)
	}
	got, _ = Apply([]Rule{{From: "/data", To: `C:\`}}, "/data")
	if got != `C:\` {
		t.Errorf("a bare drive keeps its slash, got %q", got)
	}
}

func TestWithinAndDir(t *testing.T) {
	if !Within(`C:\Media\Movies`, `c:\media\movies\A\a.mkv`) || !Within("/media", "/media") || Within("/media", "/media2/x") {
		t.Error("Within gave a wrong answer")
	}
	dirs := map[string]string{
		`C:\Media\a.mkv`:        `C:\Media`,
		`C:\a.mkv`:              `C:\`,
		"/media/movies/a.mkv":   "/media/movies",
		"/a.mkv":                "/",
		"a.mkv":                 ".",
		`\\nas\share\x\a.mkv`:   `\\nas\share\x`,
		"/media/movies/Folder/": "/media/movies",
	}
	for in, want := range dirs {
		if got := Dir(in); got != want {
			t.Errorf("Dir(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestParseEncodeClean(t *testing.T) {
	rules, err := Parse(`[{"from":"/a","to":"/b"}]`)
	if err != nil || len(rules) != 1 || rules[0].To != "/b" {
		t.Fatalf("Parse = %v, %v", rules, err)
	}
	if rules, _ := Parse(""); rules == nil || len(rules) != 0 {
		t.Errorf("empty text is an empty list, got %#v", rules)
	}
	if _, err := Parse("{"); err == nil {
		t.Error("broken JSON should fail")
	}
	if Encode(nil) != "[]" {
		t.Errorf("Encode(nil) = %q", Encode(nil))
	}
	if _, err := Clean([]Rule{{From: "/a"}}); err == nil {
		t.Error("a rule with one side should be refused")
	}
	if _, err := Clean([]Rule{{From: "/a\x00", To: "/b"}}); err == nil {
		t.Error("a control character should be refused")
	}
	if out, err := Clean([]Rule{{From: " /a ", To: " /b "}}); err != nil || out[0].From != "/a" || out[0].To != "/b" {
		t.Errorf("Clean should trim: %v, %v", out, err)
	}
}
