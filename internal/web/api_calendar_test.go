package web

import (
	"context"
	"net/http"
	"strconv"
	"testing"
	"time"

	"github.com/BaileyPillon/marquee/internal/titles"
)

func calendarItems(t *testing.T, r reply) []map[string]any {
	t.Helper()
	if r.Status != http.StatusOK {
		t.Fatalf("calendar = %d %s", r.Status, r.Body)
	}
	return asSlice(t, r.JSON(t)["items"])
}

func TestCalendarListsEpisodesWithTheirStates(t *testing.T) {
	a := newM7App(t)
	owner := a.owner()
	member := a.member(owner, "Priya")

	// "Show 10" (rqShows) has S01E01 on 2005-01-01 and S02E01 on 2006-01-01.
	show, _, err := a.cat.Add(context.Background(), "show", "tvmaze", "10", titles.AddOptions{Monitored: true})
	if err != nil {
		t.Fatal(err)
	}
	showID := show.ID
	var ep1 int64
	for _, e := range show.Episodes {
		if e.Season == 1 {
			ep1 = e.ID
		}
	}
	folder, err := a.lib.AddFolder(context.Background(), "show", t.TempDir(), "")
	if err != nil {
		t.Fatal(err)
	}
	a.mainDB.Exec(`INSERT INTO files (folder_id, rel_path, size, mtime, kind, quality, title_id, episode_ids, match_state, added_at)
		VALUES (?, 'a.mkv', 5, 0, 'show', 'WEBDL-1080p', ?, ?, 'matched', '2026-01-01T00:00:00.000Z')`,
		folder.ID, showID, "["+strconv.FormatInt(ep1, 10)+"]")

	for name, c := range map[string]*client{"owner": owner, "member": member} { // any signed-in person
		items := calendarItems(t, c.send("GET", "/api/v1/calendar?from=2004-12-15&to=2005-01-15", nil))
		if len(items) != 1 {
			t.Fatalf("%s: items = %v", name, items)
		}
		first := items[0]
		if first["date"] != "2005-01-01" || first["titleId"] != float64(showID) || first["kind"] != "episode" || first["name"] != "Show 10" ||
			first["season"] != float64(1) || first["episode"] != float64(1) || first["episodeName"] != "One" || first["label"] != "S01E01" ||
			first["state"] != "available" || first["progress"] != nil {
			t.Errorf("%s: first = %v", name, first)
		}
		items = calendarItems(t, c.send("GET", "/api/v1/calendar?from=2005-12-15&to=2006-01-15", nil))
		if len(items) != 1 {
			t.Fatalf("%s: later items = %v", name, items)
		}
		if second := items[0]; second["date"] != "2006-01-01" || second["season"] != float64(2) || second["state"] != "missing" || second["label"] != "S02E01" {
			t.Errorf("%s: second = %v", name, second)
		}
	}

	// Only what is inside the range.
	if items := calendarItems(t, member.send("GET", "/api/v1/calendar?from=2005-12-01&to=2006-01-01", nil)); len(items) != 1 || items[0]["season"] != float64(2) {
		t.Errorf("narrow range = %v", items)
	}
	if items := calendarItems(t, member.send("GET", "/api/v1/calendar?from=2010-01-01&to=2010-02-01", nil)); items == nil || len(items) != 0 {
		t.Errorf("empty range = %v", items)
	}
	r := member.send("GET", "/api/v1/calendar?from=2010-01-01&to=2010-02-01", nil)
	if string(r.Body) != "{\"items\":[]}\n" {
		t.Errorf("an empty calendar encodes as %q", r.Body)
	}
}

func TestCalendarMarksTheTitlesAPersonAskedFor(t *testing.T) {
	a := newM7App(t)
	owner := a.owner()
	member := a.member(owner, "Priya")
	asked, _, err := a.cat.Add(context.Background(), "show", "tvmaze", "11", titles.AddOptions{Monitored: true})
	if err != nil {
		t.Fatal(err)
	}
	showID := asked.ID
	if _, _, err := a.cat.Add(context.Background(), "show", "tvmaze", "12", titles.AddOptions{Monitored: true}); err != nil {
		t.Fatal(err)
	}
	a.mainDB.Exec(`INSERT INTO requests (user_id, title_id, kind, quality, state, created_at, updated_at)
		VALUES (?, ?, 'show', 'hd', 'pending', '2026-01-01T00:00:00.000Z', '2026-01-01T00:00:00.000Z')`, userID(t, member), showID)

	all := calendarItems(t, member.send("GET", "/api/v1/calendar?from=2005-01-01&to=2005-01-02", nil))
	if len(all) != 2 {
		t.Fatalf("all = %v", all)
	}
	for _, it := range all {
		if want := it["titleId"] == float64(showID); it["requestedByMe"] != want {
			t.Errorf("requestedByMe on %v = %v", it["name"], it["requestedByMe"])
		}
	}
	mine := calendarItems(t, member.send("GET", "/api/v1/calendar?from=2005-01-01&to=2005-01-02&mine=true", nil))
	if len(mine) != 1 || mine[0]["titleId"] != float64(showID) {
		t.Errorf("mine = %v", mine)
	}
	if r := member.send("GET", "/api/v1/calendar?mine=maybe", nil); r.Status != http.StatusBadRequest {
		t.Errorf("mine=maybe = %d", r.Status)
	}
}

func TestCalendarRangeLimitAndBadInput(t *testing.T) {
	a := newM7App(t)
	owner := a.owner()

	// 92 days are fine; 93 are not.
	if r := owner.send("GET", "/api/v1/calendar?from=2026-01-01&to=2026-04-03", nil); r.Status != http.StatusOK {
		t.Errorf("92 days = %d %s", r.Status, r.Body)
	}
	r := owner.send("GET", "/api/v1/calendar?from=2026-01-01&to=2026-04-04", nil)
	if r.Status != http.StatusBadRequest || r.errCode(t) != "range_too_large" {
		t.Fatalf("93 days = %d %s", r.Status, r.Body)
	}
	if e := r.JSON(t)["error"].(map[string]any); e["maxDays"] != float64(92) {
		t.Errorf("error = %v", e)
	}

	for path, code := range map[string]string{
		"/api/v1/calendar?from=2026-02-01&to=2026-01-01": "invalid_range",
		"/api/v1/calendar?from=yesterday":                "invalid_date",
		"/api/v1/calendar?from=2026-13-01":               "invalid_date",
		"/api/v1/calendar?to=01/02/2026":                 "invalid_date",
	} {
		if r := owner.send("GET", path, nil); r.Status != http.StatusBadRequest || r.errCode(t) != code {
			t.Errorf("%s = %d %s, want %s", path, r.Status, r.Body, code)
		}
	}

	// No dates at all: from today, for 30 days.
	if r := owner.send("GET", "/api/v1/calendar", nil); r.Status != http.StatusOK {
		t.Errorf("no dates = %d %s", r.Status, r.Body)
	}
	// Only from: a month ahead of it.
	if r := owner.send("GET", "/api/v1/calendar?from="+time.Now().Format("2006-01-02"), nil); r.Status != http.StatusOK {
		t.Errorf("only from = %d %s", r.Status, r.Body)
	}
	if r := a.newClient().send("GET", "/api/v1/calendar", nil); r.Status != http.StatusUnauthorized {
		t.Errorf("signed out = %d", r.Status)
	}
}

func TestCalendarNeedsTheSeeTick(t *testing.T) {
	a := newM7App(t)
	owner := a.owner()
	ask := a.key(owner, "ask")
	if r := ask.send("GET", "/api/v1/calendar", nil); r.Status != http.StatusForbidden || r.errCode(t) != "insufficient_scope" {
		t.Errorf("a key without see = %d %s", r.Status, r.Body)
	}
	see := a.key(owner, "see")
	if r := see.send("GET", "/api/v1/calendar", nil); r.Status != http.StatusOK {
		t.Errorf("a key with see = %d %s", r.Status, r.Body)
	}
}
