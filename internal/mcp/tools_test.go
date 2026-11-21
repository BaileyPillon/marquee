package mcp

import (
	"bytes"
	"context"
	"fmt"
	"log/slog"
	"net/http"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/BaileyPillon/marquee/internal/accounts"
	"github.com/BaileyPillon/marquee/internal/metadata"
	"github.com/BaileyPillon/marquee/internal/requests"
	"github.com/BaileyPillon/marquee/internal/sources"
)

func allScopes() []string { return accounts.AllScopes }

func TestToolsListFollowsTheKeysScopes(t *testing.T) {
	r := newRig(t)
	see, ask, dl := accounts.ScopeSee, accounts.ScopeAsk, accounts.ScopeDownloads
	cases := []struct {
		name   string
		who    accounts.User
		scopes []string
		want   []string
	}{
		{"a member with see and ask", r.mia, []string{see, ask},
			[]string{ToolSearchTitles, ToolLibraryLook, ToolCalendar, ToolMyAsks, ToolHealth, ToolAskForTitle}},
		{"a member with see", r.mia, []string{see},
			[]string{ToolSearchTitles, ToolLibraryLook, ToolCalendar, ToolMyAsks, ToolHealth}},
		{"a member with ask", r.mia, []string{ask}, []string{ToolAskForTitle}},
		{"an admin with see", r.admin, []string{see},
			[]string{ToolSearchTitles, ToolLibraryLook, ToolCalendar, ToolDownloads, ToolMyAsks, ToolHealth}},
		{"an admin with downloads", r.admin, []string{dl}, []string{ToolDownloads}},
		{"an admin with ask", r.admin, []string{ask}, []string{ToolAskForTitle}},
		{"the owner with everything", r.owner, allScopes(),
			[]string{ToolSearchTitles, ToolLibraryLook, ToolCalendar, ToolDownloads, ToolMyAsks, ToolHealth, ToolAskForTitle}},
		// Keys that can approve, delete, change settings and administer get no tool for any of it.
		{"the owner with only the powers", r.owner,
			[]string{accounts.ScopeSettings, accounts.ScopeDelete, accounts.ScopeAdmin, accounts.ScopeApprove}, []string{}},
	}
	for _, tc := range cases {
		got := r.toolNames(r.key(tc.who, tc.scopes...))
		if got == nil {
			got = []string{}
		}
		if !slices.Equal(got, tc.want) {
			t.Errorf("%s: tools = %v, want %v", tc.name, got, tc.want)
		}
	}

	// A member's key cannot carry the downloads tick at all; if a row says it does, the role still wins.
	if _, _, err := r.acc.CreateAPIKey(bg(), r.mia, "x", []string{dl}); err == nil {
		t.Error("a member's key accepted the downloads permission")
	}
	key := r.key(r.mia, see)
	if _, err := r.db.Exec(`UPDATE api_keys SET scopes = '["see","downloads","delete","approve"]' WHERE user_id = ?`, r.mia.ID); err != nil {
		t.Fatal(err)
	}
	if got := r.toolNames(key); slices.Contains(got, ToolDownloads) {
		t.Errorf("a member's key with a forced downloads tick lists %v", got)
	}
	if out := r.call(key, ToolDownloads, nil); !out.IsError {
		t.Errorf("a member's key ran downloads: %s", out.Text)
	}
}

func TestToolDefinitions(t *testing.T) {
	r := newRig(t)
	rep := r.rpc(r.key(r.owner, allScopes()...), "tools/list", nil)
	tools, _ := rep.result(t)["tools"].([]any)
	if len(tools) != 7 {
		t.Fatalf("%d tools, want exactly the 7 documented", len(tools))
	}
	forbidden := []string{"approve", "decline", "delete", "remove", "setting", "config", "pause", "resume", "invite", "key"}
	for _, x := range tools {
		tl := x.(map[string]any)
		name := tl["name"].(string)
		for _, bad := range forbidden {
			if strings.Contains(name, bad) {
				t.Errorf("tool %q looks like a power Claude must not have", name)
			}
		}
		if d, _ := tl["description"].(string); len(d) < 60 {
			t.Errorf("%s: description %q is too thin", name, d)
		}
		if title, _ := tl["title"].(string); title == "" {
			t.Errorf("%s: no title", name)
		}
		for _, k := range []string{"inputSchema", "outputSchema"} {
			s, _ := tl[k].(map[string]any)
			if s["type"] != "object" {
				t.Errorf("%s: %s = %v, want a JSON Schema object", name, k, tl[k])
			}
		}
		if _, ok := tl["annotations"].(map[string]any); !ok {
			t.Errorf("%s: no annotations", name)
		}
	}
	byName := func(n string) map[string]any {
		for _, x := range tools {
			if x.(map[string]any)["name"] == n {
				return x.(map[string]any)
			}
		}
		t.Fatalf("no tool %s", n)
		return nil
	}
	// Where it matters, the description says what Claude cannot do.
	for _, n := range []string{ToolAskForTitle, ToolMyAsks} {
		d := byName(n)["description"].(string)
		if !strings.Contains(d, "approve") {
			t.Errorf("%s: the description does not say Claude cannot approve: %q", n, d)
		}
	}
	if d := byName(ToolDownloads)["description"].(string); !strings.Contains(d, "Read-only") {
		t.Errorf("downloads: %q", d)
	}
	if ann := byName(ToolSearchTitles)["annotations"].(map[string]any); ann["readOnlyHint"] != true {
		t.Errorf("search_titles annotations = %v", ann)
	}
	if ann := byName(ToolAskForTitle)["annotations"].(map[string]any); ann["readOnlyHint"] != false {
		t.Errorf("ask_for_title annotations = %v", ann)
	}
	// Required arguments are declared.
	if req, _ := byName(ToolAskForTitle)["inputSchema"].(map[string]any)["required"].([]any); len(req) != 1 || req[0] != "quality" {
		t.Errorf("ask_for_title requires %v", req)
	}
}

func TestUnknownAndForbiddenTools(t *testing.T) {
	r := newRig(t)
	owner := r.key(r.owner, allScopes()...)

	for _, name := range []string{"approve_request", "delete_title", "set_setting", "SEARCH_TITLES", ""} {
		rep := r.rpc(owner, "tools/call", map[string]any{"name": name, "arguments": map[string]any{}})
		if rep.Status != http.StatusOK || rep.errCode(t) != codeInvalidParams {
			t.Errorf("tool %q = %d %s, want a -32602 error", name, rep.Status, rep.Body)
		}
	}
	if msg := r.rpc(owner, "tools/call", map[string]any{"name": "approve_request"}).rpcError(t)["message"].(string); !strings.Contains(msg, "Unknown tool") {
		t.Errorf("message = %q", msg)
	}
	// Arguments that are not an object are a protocol error too.
	rep := r.rpc(owner, "tools/call", map[string]any{"name": "health", "arguments": []int{1}})
	if rep.errCode(t) != codeInvalidParams {
		t.Errorf("array arguments = %s", rep.Body)
	}
	// Missing arguments are fine for a tool that needs none.
	if out := r.mustCall(owner, ToolHealth, nil); out.Structured["version"] != "9.9.9-test" {
		t.Errorf("health = %v", out.Structured)
	}
	rep = r.rpc(owner, "tools/call", map[string]any{"name": "health"})
	if rep.result(t)["isError"] != false {
		t.Errorf("health without arguments = %s", rep.Body)
	}

	// A tool the key's scopes do not allow is a tool error (so Claude can tell the person), not a crash.
	seeOnly := r.key(r.mia, accounts.ScopeSee)
	out := r.call(seeOnly, ToolAskForTitle, map[string]any{"titleId": 1, "quality": "hd"})
	if !out.IsError || !strings.Contains(out.Text, `"ask" permission`) {
		t.Errorf("ask without the ask permission: %+v", out)
	}
	askOnly := r.key(r.mia, accounts.ScopeAsk)
	out = r.call(askOnly, ToolSearchTitles, map[string]any{"query": "metropolis"})
	if !out.IsError || !strings.Contains(out.Text, `"see" permission`) {
		t.Errorf("search without the see permission: %+v", out)
	}
}

// ---- search_titles ----------------------------------------------------------------------

func TestSearchTitles(t *testing.T) {
	r := newRig(t)
	metropolis, pioneer, _ := r.seedLibrary()
	key := memberKey(r)

	results := func(out toolResult) []map[string]any {
		var list []map[string]any
		for _, x := range out.Structured["results"].([]any) {
			list = append(list, x.(map[string]any))
		}
		return list
	}

	// A title in the library: found in the catalog and at the provider, and listed once.
	out := r.mustCall(key, ToolSearchTitles, map[string]any{"query": "metropolis"})
	got := results(out)
	if len(got) != 1 {
		t.Fatalf("metropolis: %d results: %v", len(got), got)
	}
	h := got[0]
	if h["kind"] != "movie" || h["name"] != "Metropolis" || h["year"] != float64(1927) || h["inLibrary"] != true ||
		h["titleId"] != float64(metropolis.ID) || h["onDisk"] != true || h["source"] != "catalog" ||
		h["provider"] != "tmdb" || h["providerId"] != "19" {
		t.Errorf("metropolis = %v", h)
	}
	if !strings.Contains(out.Text, "Metropolis (1927), movie") || !strings.Contains(out.Text, fmt.Sprintf("titleId %d", metropolis.ID)) {
		t.Errorf("text = %q", out.Text)
	}
	if w := out.Structured["warnings"].([]any); len(w) != 0 {
		t.Errorf("warnings = %v", w)
	}

	// A title only the provider knows.
	got = results(r.mustCall(key, ToolSearchTitles, map[string]any{"query": "nosferatu", "kind": "movie"}))
	if len(got) != 1 || got[0]["inLibrary"] != false || got[0]["onDisk"] != false || got[0]["titleId"] != nil ||
		got[0]["providerId"] != "20" || got[0]["source"] != "provider" || got[0]["name"] != "Nosferatu" {
		t.Errorf("nosferatu = %v", got)
	}

	// A show, and an artist.
	got = results(r.mustCall(key, ToolSearchTitles, map[string]any{"query": "pioneer", "kind": "show"}))
	if len(got) != 1 || got[0]["titleId"] != float64(pioneer.ID) || got[0]["onDisk"] != true || got[0]["kind"] != "show" {
		t.Errorf("pioneer = %v", got)
	}
	got = results(r.mustCall(key, ToolSearchTitles, map[string]any{"query": "chopin"}))
	if len(got) != 1 || got[0]["kind"] != "artist" || got[0]["providerId"] != chopinMBID || got[0]["inLibrary"] != false {
		t.Errorf("chopin = %v", got)
	}

	// The kind narrows it, nothing found says so, and the limit caps it.
	out = r.mustCall(key, ToolSearchTitles, map[string]any{"query": "metropolis", "kind": "show"})
	if len(results(out)) != 0 || !strings.Contains(out.Text, "No matches") {
		t.Errorf("metropolis as a show = %q", out.Text)
	}
	if n := len(results(r.mustCall(key, ToolSearchTitles, map[string]any{"query": "e", "limit": 2}))); n != 2 {
		t.Errorf("limit 2 gave %d results", n)
	}
	if n := len(results(r.mustCall(key, ToolSearchTitles, map[string]any{"query": "e", "limit": "20"}))); n < 3 {
		t.Errorf("limit \"20\" (a string) gave %d results", n)
	}

	// A provider that fails is reported, and the catalog's answer is still there.
	r.movies.setErr(metadata.ErrTMDBKeyMissing)
	out = r.mustCall(key, ToolSearchTitles, map[string]any{"query": "metropolis"})
	warnings := out.Structured["warnings"].([]any)
	if len(results(out)) != 1 || len(warnings) != 1 || !strings.Contains(warnings[0].(string), "TMDB key") || !strings.Contains(out.Text, "TMDB key") {
		t.Errorf("with TMDB down: %v / %v", results(out), warnings)
	}
	r.movies.setErr(nil)

	// Bad input is a tool error Claude can read.
	for name, args := range map[string]map[string]any{
		"no query":   {},
		"blank":      {"query": "   "},
		"long":       {"query": strings.Repeat("a", 201)},
		"bad kind":   {"query": "x", "kind": "podcast"},
		"low limit":  {"query": "x", "limit": -1},
		"high limit": {"query": "x", "limit": 99},
		"bad type":   {"query": 5},
	} {
		if out := r.call(key, ToolSearchTitles, args); !out.IsError || out.Text == "" {
			t.Errorf("%s: %+v, want an error result", name, out)
		}
	}
}

// ---- library_lookup -----------------------------------------------------------------------

func TestLibraryLookupMovie(t *testing.T) {
	r := newRig(t)
	metropolis, _, _ := r.seedLibrary()
	key := memberKey(r)

	out := r.mustCall(key, ToolLibraryLook, map[string]any{"titleId": metropolis.ID})
	s := out.Structured
	files := s["files"].(map[string]any)
	copies := s["copies"].(map[string]any)
	if s["name"] != "Metropolis" || s["year"] != float64(1927) || s["kind"] != "movie" || s["onDisk"] != true ||
		files["count"] != float64(1) || files["bytes"] != float64(2048) || copies["hd"] != true || copies["uhd"] != false {
		t.Errorf("lookup = %v", s)
	}
	if q := s["qualitiesOnDisk"].([]any); len(q) != 1 || q[0] != "Bluray-1080p" {
		t.Errorf("qualitiesOnDisk = %v", q)
	}
	mf := s["movie"].(map[string]any)["files"].([]any)
	f := mf[0].(map[string]any)
	if len(mf) != 1 || f["fileName"] != "Metropolis.1927.1080p.BluRay.x264-GRP.mkv" || f["quality"] != "Bluray-1080p" ||
		f["sizeBytes"] != float64(2048) || f["copy"] != "hd" {
		t.Errorf("movie files = %v", mf)
	}
	if s["show"] != nil || s["artist"] != nil {
		t.Errorf("a movie carries show or artist data: %v", s)
	}
	if !strings.Contains(out.Text, "Bluray-1080p") || !strings.Contains(out.Text, "2.0 KiB") {
		t.Errorf("text = %q", out.Text)
	}
	// No file path leaks, only the file's name.
	if strings.Contains(string(mustJSON(t, s)), r.moviesDir) {
		t.Error("the result holds a path from the server's disk")
	}

	// A 4K file makes a second copy.
	r.putFile(r.movies4kDir, r.movies4kFolder, "Metropolis (1927)/Metropolis.1927.2160p.WEB-DL.x265-GRP.mkv", 4096)
	s = r.mustCall(key, ToolLibraryLook, map[string]any{"titleId": fmt.Sprint(metropolis.ID)}).Structured // the id as text
	copies = s["copies"].(map[string]any)
	if copies["hd"] != true || copies["uhd"] != true || s["files"].(map[string]any)["count"] != float64(2) {
		t.Errorf("with a 4K copy = %v", s)
	}
	var uhd int
	for _, x := range s["movie"].(map[string]any)["files"].([]any) {
		if x.(map[string]any)["copy"] == "uhd" && strings.Contains(x.(map[string]any)["quality"].(string), "2160p") {
			uhd++
		}
	}
	if uhd != 1 {
		t.Errorf("4K files = %d in %v", uhd, s["movie"])
	}

	// Not on disk yet.
	nos := r.addTitle("movie", "tmdb", "20")
	out = r.mustCall(key, ToolLibraryLook, map[string]any{"titleId": nos.ID})
	if out.Structured["onDisk"] != false || len(out.Structured["movie"].(map[string]any)["files"].([]any)) != 0 || !strings.Contains(out.Text, "Not on disk") {
		t.Errorf("nosferatu = %v / %q", out.Structured, out.Text)
	}
}

func TestLibraryLookupShow(t *testing.T) {
	r := newRig(t)
	_, pioneer, _ := r.seedLibrary()
	key := memberKey(r)

	out := r.mustCall(key, ToolLibraryLook, map[string]any{"titleId": pioneer.ID})
	show := out.Structured["show"].(map[string]any)
	seasons := map[int]map[string]any{}
	for _, x := range show["seasons"].([]any) {
		se := x.(map[string]any)
		seasons[int(se["season"].(float64))] = se
	}
	if len(seasons) != 3 {
		t.Fatalf("seasons = %v", show["seasons"])
	}
	ints := func(v any) []int {
		out := []int{}
		for _, n := range v.([]any) {
			out = append(out, int(n.(float64)))
		}
		return out
	}
	type want struct{ episodes, have, missing, upcoming int }
	for n, w := range map[int]want{0: {1, 0, 1, 0}, 1: {2, 2, 0, 0}, 2: {2, 0, 1, 1}} {
		se := seasons[n]
		got := want{int(se["episodes"].(float64)), int(se["have"].(float64)), int(se["missing"].(float64)), int(se["upcoming"].(float64))}
		if got != w {
			t.Errorf("season %d = %+v, want %+v", n, got, w)
		}
	}
	if !slices.Equal(ints(seasons[1]["haveEpisodes"]), []int{1, 2}) || len(ints(seasons[1]["missingEpisodes"])) != 0 {
		t.Errorf("season 1 episodes = %v / %v", seasons[1]["haveEpisodes"], seasons[1]["missingEpisodes"])
	}
	if !slices.Equal(ints(seasons[2]["missingEpisodes"]), []int{1}) || len(ints(seasons[2]["haveEpisodes"])) != 0 {
		t.Errorf("season 2 episodes = %v / %v", seasons[2]["haveEpisodes"], seasons[2]["missingEpisodes"])
	}
	next := show["nextEpisode"].(map[string]any)
	if next["season"] != float64(2) || next["episode"] != float64(2) || next["airDate"] != "2099-01-01" || next["name"] != "Not Yet" {
		t.Errorf("nextEpisode = %v", next)
	}
	if out.Structured["files"].(map[string]any)["count"] != float64(2) || out.Structured["status"] != "ended" {
		t.Errorf("show = %v", out.Structured)
	}
	for _, line := range []string{
		"Pioneer One (2010), show, ended",
		"Season 1: 2 of 2 episodes on disk (episodes 1-2)",
		"Season 2: 0 of 2 episodes on disk; missing (aired, not on disk): episodes 1; 1 still to air",
		"Specials: 0 of 1 episodes on disk",
		"Next to air: S02E02 Not Yet on 2099-01-01",
	} {
		if !strings.Contains(out.Text, line) {
			t.Errorf("text lacks %q:\n%s", line, out.Text)
		}
	}
}

func TestLibraryLookupArtistAndErrors(t *testing.T) {
	r := newRig(t)
	key := memberKey(r)
	chopin := r.addTitle("artist", "musicbrainz", chopinMBID)

	out := r.mustCall(key, ToolLibraryLook, map[string]any{"titleId": chopin.ID})
	albums := out.Structured["artist"].(map[string]any)["albums"].([]any)
	if len(albums) != 1 || albums[0].(map[string]any)["name"] != "Nocturnes" || albums[0].(map[string]any)["tracks"] != float64(2) {
		t.Errorf("albums = %v", albums)
	}

	for name, args := range map[string]map[string]any{
		"unknown":  {"titleId": 987654},
		"missing":  {},
		"zero":     {"titleId": 0},
		"negative": {"titleId": -4},
		"text":     {"titleId": "abc"},
	} {
		if out := r.call(key, ToolLibraryLook, args); !out.IsError || out.Text == "" {
			t.Errorf("%s: %+v, want an error result", name, out)
		}
	}
	if out := r.call(key, ToolLibraryLook, map[string]any{"titleId": 987654}); !strings.Contains(out.Text, "search_titles") {
		t.Errorf("the unknown-title message does not point to search_titles: %q", out.Text)
	}
}

// ---- calendar ----------------------------------------------------------------------------------

func TestCalendar(t *testing.T) {
	r := newRig(t)
	_, _, weekly := r.seedLibrary()
	key := memberKey(r)

	episodes := func(out toolResult) []string {
		var names []string
		for _, x := range out.Structured["episodes"].([]any) {
			e := x.(map[string]any)
			names = append(names, fmt.Sprintf("%v %v S%02.0fE%02.0f", e["airDate"], e["show"], e["season"], e["episode"]))
		}
		return names
	}

	out := r.mustCall(key, ToolCalendar, nil) // a week by default
	if out.Structured["from"] != "2026-11-20" || out.Structured["to"] != "2026-11-26" || out.Structured["days"] != float64(7) {
		t.Errorf("window = %v", out.Structured)
	}
	want := []string{day(1) + " Weekly Show S01E02", day(3) + " Weekly Show S01E03"}
	if got := episodes(out); !slices.Equal(got, want) {
		t.Errorf("a week = %v, want %v", got, want)
	}
	first := out.Structured["episodes"].([]any)[0].(map[string]any)
	if first["titleId"] != float64(weekly.ID) || first["name"] != "Tomorrow" {
		t.Errorf("first = %v", first)
	}
	if !strings.Contains(out.Text, "2 episodes") || !strings.Contains(out.Text, "Weekly Show S01E02 Tomorrow") {
		t.Errorf("text = %q", out.Text)
	}

	if got := episodes(r.mustCall(key, ToolCalendar, map[string]any{"days": 1})); len(got) != 0 {
		t.Errorf("today = %v", got)
	}
	if out := r.mustCall(key, ToolCalendar, map[string]any{"days": 1}); !strings.Contains(out.Text, "No episodes") {
		t.Errorf("an empty day: %q", out.Text)
	}
	if got := episodes(r.mustCall(key, ToolCalendar, map[string]any{"days": 30})); len(got) != 3 {
		t.Errorf("30 days = %v", got)
	}
	// The ends of the range are included.
	if got := episodes(r.mustCall(key, ToolCalendar, map[string]any{"days": 2})); len(got) != 1 {
		t.Errorf("2 days = %v (tomorrow is the last day)", got)
	}
	for _, days := range []any{0.5, -1, 31, "soon"} {
		if out := r.call(key, ToolCalendar, map[string]any{"days": days}); !out.IsError {
			t.Errorf("days %v: want an error result, got %q", days, out.Text)
		}
	}

	// A show that is not monitored is not on the calendar.
	if err := r.cat.SetMonitored(bg(), weekly.ID, false); err != nil {
		t.Fatal(err)
	}
	if got := episodes(r.mustCall(key, ToolCalendar, map[string]any{"days": 30})); len(got) != 0 {
		t.Errorf("an unmonitored show: %v", got)
	}
	// Time moves the window.
	if err := r.cat.SetMonitored(bg(), weekly.ID, true); err != nil {
		t.Fatal(err)
	}
	r.clock.Advance(72 * time.Hour)
	// Midweek airs today now (the first day is included); Far Away is a week and a day out.
	if got := episodes(r.mustCall(key, ToolCalendar, nil)); !slices.Equal(got, []string{day(3) + " Weekly Show S01E03"}) {
		t.Errorf("three days later = %v", got)
	}
	if got := episodes(r.mustCall(key, ToolCalendar, map[string]any{"days": 8})); len(got) != 2 {
		t.Errorf("three days later, 8 days = %v", got)
	}
}

// ---- downloads ----------------------------------------------------------------------------------

func TestDownloads(t *testing.T) {
	r := newRig(t)
	owner := r.key(r.owner, accounts.ScopeSee)

	out := r.mustCall(owner, ToolDownloads, nil)
	if out.Structured["count"] != float64(0) || !strings.Contains(out.Text, "Nothing is downloading") || len(out.Structured["downloads"].([]any)) != 0 {
		t.Errorf("an empty queue = %v / %q", out.Structured, out.Text)
	}

	metropolis, _, _ := r.seedLibrary()
	r.seedDownload(metropolis.ID)

	out = r.mustCall(owner, ToolDownloads, nil)
	list := out.Structured["downloads"].([]any)
	if out.Structured["count"] != float64(1) || len(list) != 1 {
		t.Fatalf("downloads = %v", out.Structured)
	}
	d := list[0].(map[string]any)
	if d["title"] != "Metropolis (1927)" || d["state"] != "downloading" || d["progressPercent"] != 42.5 || d["etaSeconds"] != float64(720) ||
		d["sizeBytes"] != float64(4<<30) || d["speedBytesPerSec"] != float64(3<<20) || d["quality"] != "hd" ||
		d["releaseTitle"] != "Metropolis.1927.1080p.BluRay.x264-GRP" {
		t.Errorf("download = %v", d)
	}
	if out.Structured["summary"].(map[string]any)["downloading"] != float64(1) {
		t.Errorf("summary = %v", out.Structured["summary"])
	}
	for _, s := range []string{"Metropolis (1927) HD: downloading, 42.5% at 3.0 MiB/s, about 12 minutes left"} {
		if !strings.Contains(out.Text, s) {
			t.Errorf("text lacks %q: %q", s, out.Text)
		}
	}

	// The downloads tick alone is enough for an admin; a plain see is enough for an admin too.
	for _, key := range []string{r.key(r.admin, accounts.ScopeDownloads), r.key(r.admin, accounts.ScopeSee)} {
		if out := r.mustCall(key, ToolDownloads, nil); out.Structured["count"] != float64(1) {
			t.Errorf("an admin's key: %v", out.Structured)
		}
	}
	// A member's key never sees downloads.
	if out := r.call(memberKey(r), ToolDownloads, nil); !out.IsError || !strings.Contains(out.Text, "downloads") {
		t.Errorf("a member's key: %+v", out)
	}
	// Finished downloads are not "active".
	if _, err := r.db.Exec(`UPDATE downloads SET state = 'imported'`); err != nil {
		t.Fatal(err)
	}
	if out := r.mustCall(owner, ToolDownloads, nil); out.Structured["count"] != float64(0) {
		t.Errorf("after import = %v", out.Structured)
	}
}

// ---- my_asks --------------------------------------------------------------------------------------

func TestMyAsks(t *testing.T) {
	r := newRig(t)
	mia := memberKey(r)
	miaActor := requests.Actor{UserID: r.mia.ID, Role: "member", Source: requests.SourceMCP}
	if _, err := r.req.Ask(bg(), miaActor, requests.AskInput{Kind: "movie", Provider: "tmdb", ProviderID: "20", Quality: "hd"}); err != nil {
		t.Fatal(err)
	}
	r.clock.Advance(time.Second)
	if _, err := r.req.Ask(bg(), miaActor, requests.AskInput{Kind: "movie", Provider: "tmdb", ProviderID: "31", Quality: "uhd", Note: "for movie night"}); err != nil {
		t.Fatal(err)
	}
	if _, err := r.req.Ask(bg(), requests.Actor{UserID: r.noah.ID, Role: "member"}, requests.AskInput{Kind: "movie", Provider: "tmdb", ProviderID: "32", Quality: "hd"}); err != nil {
		t.Fatal(err)
	}

	out := r.mustCall(mia, ToolMyAsks, nil)
	asks := out.Structured["asks"].([]any)
	if out.Structured["total"] != float64(2) || len(asks) != 2 {
		t.Fatalf("mia's asks = %v", out.Structured)
	}
	newest, oldest := asks[0].(map[string]any), asks[1].(map[string]any)
	if newest["quality"] != "uhd" || newest["state"] != "pending" || newest["title"] != "Movie 31" || newest["note"] != "for movie night" || newest["kind"] != "movie" {
		t.Errorf("newest = %v", newest)
	}
	if oldest["quality"] != "hd" || oldest["state"] != "approved" || oldest["title"] != "Nosferatu" || oldest["year"] != float64(1922) {
		t.Errorf("oldest = %v", oldest)
	}
	for _, s := range []string{"2 asks", "Movie 31 (1991), movie, 4K: waiting for the owner's yes", "Nosferatu (1922), movie, HD: approved"} {
		if !strings.Contains(out.Text, s) {
			t.Errorf("text lacks %q: %q", s, out.Text)
		}
	}

	// A state filter and a limit.
	out = r.mustCall(mia, ToolMyAsks, map[string]any{"state": "pending"})
	if out.Structured["total"] != float64(1) || out.Structured["asks"].([]any)[0].(map[string]any)["title"] != "Movie 31" {
		t.Errorf("pending = %v", out.Structured)
	}
	out = r.mustCall(mia, ToolMyAsks, map[string]any{"limit": 1})
	if out.Structured["total"] != float64(2) || len(out.Structured["asks"].([]any)) != 1 || !strings.Contains(out.Text, "showing the newest 1") {
		t.Errorf("limit 1 = %v / %q", out.Structured, out.Text)
	}
	if out := r.mustCall(mia, ToolMyAsks, map[string]any{"state": "declined"}); !strings.Contains(out.Text, "no declined asks") {
		t.Errorf("none declined: %q", out.Text)
	}

	// Only your own: nobody else's asks, and the owner's key lists the owner's.
	noah := r.mustCall(r.key(r.noah, accounts.ScopeSee), ToolMyAsks, nil)
	if noah.Structured["total"] != float64(1) || noah.Structured["asks"].([]any)[0].(map[string]any)["title"] != "Movie 32" {
		t.Errorf("noah's asks = %v", noah.Structured)
	}
	if owner := r.mustCall(r.key(r.owner, accounts.ScopeSee), ToolMyAsks, nil); owner.Structured["total"] != float64(0) || !strings.Contains(owner.Text, "not asked for anything") {
		t.Errorf("the owner's own asks = %v", owner.Structured)
	}
	for _, args := range []map[string]any{{"state": "bogus"}, {"limit": 0 - 1}, {"limit": 51}} {
		if out := r.call(mia, ToolMyAsks, args); !out.IsError {
			t.Errorf("%v: want an error result", args)
		}
	}
}

// ---- health ----------------------------------------------------------------------------------------

func TestHealth(t *testing.T) {
	r := newRig(t, func(d *Deps) {
		d.MediaServers = func(context.Context) []Component {
			return []Component{{Name: "Jellyfin", Kind: "jellyfin", OK: false, Detail: "the key was refused"}}
		}
	})
	metropolis, _, _ := r.seedLibrary()
	r.seedDownload(metropolis.ID)
	no := false
	if _, err := r.src.Create(bg(), sources.Input{Name: "Torznab A", Kind: "torznab", URL: "http://127.0.0.1:9/api", APIKey: "abcdef123456"}); err != nil {
		t.Fatal(err)
	}
	if _, err := r.src.Create(bg(), sources.Input{Name: "Old feed", Kind: "newznab", URL: "http://127.0.0.1:9/n", APIKey: "k", Enabled: &no}); err != nil {
		t.Fatal(err)
	}

	for _, who := range []struct {
		name string
		key  string
	}{{"the owner", r.key(r.owner, accounts.ScopeSee)}, {"an admin", r.key(r.admin, accounts.ScopeSee)}} {
		out := r.mustCall(who.key, ToolHealth, nil)
		s := out.Structured
		if s["status"] != "ok" || s["version"] != "9.9.9-test" || s["uptimeSeconds"] != float64(3*3600) || s["database"].(map[string]any)["ok"] != true {
			t.Errorf("%s: health = %v", who.name, s)
		}
		clients := s["clients"].([]any)
		if len(clients) != 1 || clients[0].(map[string]any)["name"] != "qbit" || clients[0].(map[string]any)["ok"] != true || clients[0].(map[string]any)["kind"] != "qbittorrent" {
			t.Errorf("%s: clients = %v", who.name, clients)
		}
		srcs := s["sources"].([]any)
		if len(srcs) != 1 || srcs[0].(map[string]any)["name"] != "Torznab A" || srcs[0].(map[string]any)["ok"] != false || srcs[0].(map[string]any)["detail"] != "not checked yet" {
			t.Errorf("%s: sources = %v (a disabled source must not be listed)", who.name, srcs)
		}
		media := s["mediaServers"].([]any)
		if len(media) != 1 || media[0].(map[string]any)["name"] != "Jellyfin" || media[0].(map[string]any)["ok"] != false {
			t.Errorf("%s: media servers = %v", who.name, media)
		}
		for _, line := range []string{"Marquee 9.9.9-test has been up for 3.0 hours; database ok.", "Download clients: qbit (ok).",
			"Sources: Torznab A (not answering: not checked yet).", "Media servers: Jellyfin (not answering: the key was refused)."} {
			if !strings.Contains(out.Text, line) {
				t.Errorf("%s: text lacks %q: %q", who.name, line, out.Text)
			}
		}
	}

	// A member learns only that Marquee is up: nothing about what is connected.
	out := r.mustCall(memberKey(r), ToolHealth, nil)
	for _, k := range []string{"clients", "sources", "mediaServers"} {
		if _, has := out.Structured[k]; has {
			t.Errorf("a member's health shows %q", k)
		}
	}
	if out.Structured["version"] != "9.9.9-test" || strings.Contains(out.Text, "qbit") || strings.Contains(out.Text, "Jellyfin") {
		t.Errorf("member health = %v / %q", out.Structured, out.Text)
	}

	// Nothing connected: owner's lists are empty, not missing.
	bare := newRig(t)
	out = bare.mustCall(bare.key(bare.owner, accounts.ScopeSee), ToolHealth, nil)
	for _, k := range []string{"clients", "sources", "mediaServers"} {
		if l, ok := out.Structured[k].([]any); !ok || len(l) != 0 {
			t.Errorf("bare %s = %v", k, out.Structured[k])
		}
	}
	if !strings.Contains(out.Text, "Download clients: none connected.") {
		t.Errorf("text = %q", out.Text)
	}
}

// ---- recent calls, the list on the settings screen, and the log -----------------------------------

func TestInfoAndRecentCalls(t *testing.T) {
	r := newRig(t)
	metropolis, _, _ := r.seedLibrary()
	mia, owner := memberKey(r), r.key(r.owner, allScopes()...)

	r.mustCall(mia, ToolSearchTitles, map[string]any{"query": "metropolis"})
	r.clock.Advance(time.Second)
	r.mustCall(owner, ToolHealth, nil)
	r.clock.Advance(time.Second)
	r.call(mia, ToolLibraryLook, map[string]any{"titleId": 424242}) // fails
	r.clock.Advance(time.Second)
	r.mustCall(mia, ToolLibraryLook, map[string]any{"titleId": metropolis.ID})
	// Things that are not tool calls are not listed.
	r.rpc(mia, "tools/list", nil)
	r.rpc(mia, "ping", nil)

	info := r.mcp.Info(bg(), r.owner, "https://marquee.example")
	if !info.Enabled || info.URL != "https://marquee.example/mcp" {
		t.Errorf("info = %+v", info)
	}
	if len(info.Tools) != 7 {
		t.Errorf("the owner's tools = %v", info.Tools)
	}
	for _, tl := range info.Tools {
		if tl.Name == "" || tl.Description == "" || !slices.Contains(allScopes(), tl.Scope) {
			t.Errorf("tool info = %+v", tl)
		}
	}
	scopeOf := map[string]string{}
	for _, tl := range info.Tools {
		scopeOf[tl.Name] = tl.Scope
	}
	if scopeOf[ToolAskForTitle] != "ask" || scopeOf[ToolDownloads] != "downloads" || scopeOf[ToolHealth] != "see" {
		t.Errorf("scopes = %v", scopeOf)
	}

	// The owner sees everyone's calls, newest first.
	var seq []string
	for _, c := range info.RecentCalls {
		seq = append(seq, fmt.Sprintf("%s/%s/%v", c.UserName, c.Tool, c.OK))
	}
	want := []string{"Mia/library_lookup/true", "Mia/library_lookup/false", "Bailey/health/true", "Mia/search_titles/true"}
	if !slices.Equal(seq, want) {
		t.Errorf("owner's recent calls = %v, want %v", seq, want)
	}
	if info.RecentCalls[0].At.Before(info.RecentCalls[3].At) {
		t.Error("recent calls are not newest first")
	}

	// A member sees only their own, and only the tools their role allows.
	mine := r.mcp.Info(bg(), r.mia, "http://x")
	if len(mine.RecentCalls) != 3 || mine.RecentCalls[0].UserName != "Mia" {
		t.Errorf("mia's calls = %+v", mine.RecentCalls)
	}
	var names []string
	for _, tl := range mine.Tools {
		names = append(names, tl.Name)
	}
	if slices.Contains(names, ToolDownloads) || len(names) != 6 {
		t.Errorf("a member's tools = %v", names)
	}
	if got := r.mcp.Info(bg(), r.noah, "http://x").RecentCalls; len(got) != 0 {
		t.Errorf("noah's calls = %+v", got)
	}
	// An admin is not the owner: they see their own.
	if got := r.mcp.Info(bg(), r.admin, "http://x").RecentCalls; len(got) != 0 {
		t.Errorf("the admin's calls = %+v", got)
	}

	// The JSON the settings screen gets.
	b := string(mustJSON(t, info))
	for _, k := range []string{`"enabled":true`, `"url":"https://marquee.example/mcp"`, `"recentCalls":[{"at":"`, `"userName":"Mia"`, `"ok":false`, `"scope":"see"`} {
		if !strings.Contains(b, k) {
			t.Errorf("info JSON lacks %s: %s", k, b)
		}
	}
	if strings.Contains(b, "424242") || strings.Contains(b, "metropolis") {
		t.Errorf("the recent calls leak arguments: %s", b)
	}
}

func TestCallsAreLoggedWithoutArguments(t *testing.T) {
	var buf bytes.Buffer
	log := slog.New(slog.NewTextHandler(&lockedWriter{w: &buf}, nil))
	r := newRig(t, func(d *Deps) { d.Log = log })
	key := memberKey(r)

	r.mustCall(key, ToolSearchTitles, map[string]any{"query": "zzsecretqueryzz"})
	r.call(key, ToolLibraryLook, map[string]any{"titleId": 31337})

	logs := buf.String()
	for _, want := range []string{"tool=search_titles", "user=Mia", "ok=true", "ms=", "tool=library_lookup", "ok=false"} {
		if !strings.Contains(logs, want) {
			t.Errorf("the log lacks %q:\n%s", want, logs)
		}
	}
	for _, leak := range []string{"zzsecretqueryzz", "31337", "mq_"} {
		if strings.Contains(logs, leak) {
			t.Errorf("the log holds %q:\n%s", leak, logs)
		}
	}
}
