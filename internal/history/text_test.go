package history

import (
	"encoding/json"
	"strings"
	"testing"
)

func textOf(t *testing.T, event string, detail any) string {
	t.Helper()
	b, err := json.Marshal(detail)
	if err != nil {
		t.Fatal(err)
	}
	return Text(event, b)
}

func TestTextForEveryEvent(t *testing.T) {
	cases := []struct {
		name   string
		event  string
		detail any
		want   string
	}{
		{"asked for 4K", Requested, map[string]any{"user": "Priya", "quality": "uhd"}, "Priya asked for 4K"},
		{"asked for HD", Requested, map[string]any{"user": "Mia", "quality": "hd"}, "Mia asked for HD"},
		{"no name", Requested, map[string]any{"quality": "uhd"}, "Asked for 4K"},
		{"seasons", Requested, map[string]any{"user": "Mia", "quality": "hd", "scope": map[string]any{"seasons": []int{1, 2}}},
			"Mia asked for HD (seasons 1, 2)"},
		{"one season", Requested, map[string]any{"user": "Mia", "quality": "hd", "scope": map[string]any{"seasons": []int{3}}},
			"Mia asked for HD (season 3)"},
		{"albums", Requested, map[string]any{"user": "Mia", "quality": "hd", "scope": map[string]any{"albums": []int{4, 5}}},
			"Mia asked for HD (2 albums)"},
		{"watchlist", Requested, map[string]any{"user": "Mia", "quality": "hd", "source": "watchlist"}, "Mia asked for HD from a Plex watchlist"},
		{"through claude", Requested, map[string]any{"user": "Mia", "quality": "hd", "source": "mcp"}, "Mia asked for HD through Claude"},

		{"approved by", Approved, map[string]any{"by": "Bailey", "quality": "uhd"}, "Approved by Bailey"},
		{"approved auto", Approved, map[string]any{"auto": true, "quality": "hd"}, "Approved automatically"},
		{"the owner's own ask", Approved, map[string]any{"auto": true, "by": "Bailey"}, "Approved by Bailey"},
		{"approved bare", Approved, map[string]any{}, "Approved"},
		{"declined", Declined, map[string]any{"by": "Bailey", "reason": "too big"}, "Declined by Bailey: too big"},
		{"declined bare", Declined, map[string]any{}, "Declined"},

		{"grabbed", Grabbed, map[string]any{"release": "Metropolis.1927.2160p.BluRay.x265-GRP", "indexer": "Archive feed"},
			"Grabbed Metropolis.1927.2160p.BluRay.x265-GRP from Archive feed"},
		{"grabbed no indexer", Grabbed, map[string]any{"release": "Film.1080p"}, "Grabbed Film.1080p"},
		{"grabbed nothing", Grabbed, map[string]any{}, "Grabbed a release"},

		{"imported", Imported, map[string]any{"files": []any{map[string]any{"quality": "Bluray-1080p"}}, "rescanned": []string{"Jellyfin"}},
			"Imported Bluray-1080p · Jellyfin rescanned"},
		{"imported two servers", Imported, map[string]any{"files": []any{map[string]any{"quality": "WEBDL-1080p"}}, "rescanned": []string{"Jellyfin", "Plex"}},
			"Imported WEBDL-1080p · Jellyfin and Plex rescanned"},
		{"imported three servers", Imported, map[string]any{"files": []any{map[string]any{"quality": "WEBDL-1080p"}}, "rescanned": []string{"A", "B", "C"}},
			"Imported WEBDL-1080p · A, B and C rescanned"},
		{"imported no server", Imported, map[string]any{"files": []any{map[string]any{"quality": "Bluray-720p"}}}, "Imported Bluray-720p"},
		{"imported many", Imported, map[string]any{"files": []any{map[string]any{"quality": "WEBDL-1080p"}, map[string]any{"quality": "WEBDL-1080p"}}},
			"Imported 2 files · WEBDL-1080p"},
		{"imported none new", Imported, map[string]any{"files": []any{}, "alreadyThere": 1}, "Already in the library"},

		{"upgraded", Upgraded, map[string]any{"from": "WEBDL-720p", "to": "Bluray-1080p", "rescanned": []string{"Jellyfin"}},
			"Upgraded from WEBDL-720p to Bluray-1080p · Jellyfin rescanned"},
		{"upgraded to", Upgraded, map[string]any{"to": "Bluray-1080p"}, "Upgraded to Bluray-1080p"},

		{"failed", Failed, map[string]any{"reason": "No usable media files", "blocklisted": true, "searchingAgain": true},
			"Import failed: No usable media files · release blocklisted · searching again"},
		{"failed bare", Failed, map[string]any{}, "Import failed"},

		{"renamed", Renamed, map[string]any{"from": "a.mkv", "to": "A (1999) Bluray-1080p.mkv"}, "Renamed a.mkv to A (1999) Bluray-1080p.mkv"},
		{"renamed bare", Renamed, map[string]any{}, "Renamed a file"},
		{"deleted from disk", Deleted, map[string]any{"file": "A.mkv", "fromDisk": true}, "Deleted A.mkv from disk"},
		{"removed row only", Deleted, map[string]any{"file": "A.mkv"}, "Removed A.mkv from the library"},
		{"deleted bare", Deleted, map[string]any{}, "Deleted a file"},
	}
	for _, c := range cases {
		if got := textOf(t, c.event, c.detail); got != c.want {
			t.Errorf("%s: got %q, want %q", c.name, got, c.want)
		}
	}
}

func TestTextLongReleaseIsCut(t *testing.T) {
	long := "Metropolis.1927." + strings.Repeat("x", 200)
	got := textOf(t, Grabbed, map[string]any{"release": long, "indexer": "Archive feed"})
	if !strings.HasPrefix(got, "Grabbed Metropolis.1927.") || !strings.Contains(got, "… from Archive feed") {
		t.Errorf("got %q", got)
	}
	if n := len([]rune(got)); n > 100 {
		t.Errorf("line is %d characters", n)
	}
}

func TestTextNeverFails(t *testing.T) {
	for _, raw := range []string{``, `null`, `[]`, `"just text"`, `{"files": "no"}`, `{"scope": 7}`, `{bad json`} {
		for _, ev := range []string{Grabbed, Imported, Upgraded, Failed, Deleted, Renamed, Requested, Approved, Declined, "something-new"} {
			if got := Text(ev, json.RawMessage(raw)); got == "" {
				t.Errorf("Text(%q, %q) is empty", ev, raw)
			}
		}
	}
	if got := Text("something-new", nil); got != "something-new" {
		t.Errorf("an unknown event is shown as it is: %q", got)
	}
}

func TestForTitleFillsText(t *testing.T) {
	s, _, _ := newService(t)
	if err := s.Record(bg(), Entry{TitleID: 5, Event: Requested, Detail: map[string]any{"user": "Priya", "quality": "uhd"}}); err != nil {
		t.Fatal(err)
	}
	if err := s.Record(bg(), Entry{TitleID: 5, Event: Approved, Detail: map[string]any{"by": "Bailey"}}); err != nil {
		t.Fatal(err)
	}
	items, err := s.ForTitle(bg(), 5, 10)
	if err != nil || len(items) != 2 {
		t.Fatalf("%v, %v", items, err)
	}
	if items[0].Text != "Approved by Bailey" || items[1].Text != "Priya asked for 4K" {
		t.Errorf("texts = %q, %q", items[0].Text, items[1].Text)
	}
	b, _ := json.Marshal(items[0])
	if !strings.Contains(string(b), `"text":"Approved by Bailey"`) {
		t.Errorf("text is not in the JSON: %s", b)
	}
}
