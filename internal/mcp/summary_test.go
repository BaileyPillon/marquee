package mcp

import (
	"strings"
	"testing"
)

// The settings screen shows each tool's one-sentence summary, not the long text
// written for Claude.
func TestEveryToolHasAShortSummaryForPeople(t *testing.T) {
	r := newRig(t)
	info := r.mcp.Info(bg(), r.owner, "https://marquee.example")
	if len(info.Tools) != 7 {
		t.Fatalf("tools = %+v", info.Tools)
	}
	for _, tl := range info.Tools {
		s := tl.Summary
		if s == "" {
			t.Errorf("%s has no summary", tl.Name)
			continue
		}
		if len([]rune(s)) > 80 {
			t.Errorf("%s: the summary is %d characters, want a short sentence: %q", tl.Name, len([]rune(s)), s)
		}
		if !strings.HasSuffix(s, ".") || strings.Count(s, ". ") > 0 {
			t.Errorf("%s: the summary should be one sentence ending in a full stop: %q", tl.Name, s)
		}
		if s == tl.Description || len(s) >= len(tl.Description) {
			t.Errorf("%s: the summary is not shorter than the description", tl.Name)
		}
	}
	// What Claude reads is not changed: the long description is still what tools/list says.
	tools := r.rpc(r.key(r.owner, allScopes()...), "tools/list", nil).result(t)["tools"].([]any)
	for _, raw := range tools {
		m := raw.(map[string]any)
		if _, has := m["summary"]; has {
			t.Errorf("tools/list shows a summary for %v; it is for the settings screen only", m["name"])
		}
		if d, _ := m["description"].(string); len(d) < 100 {
			t.Errorf("%v: the description Claude reads is short: %q", m["name"], d)
		}
	}
	// And the JSON of the info has it.
	if b := string(mustJSON(t, info)); !strings.Contains(b, `"summary":"Looks up movies, shows and artists by name."`) {
		t.Errorf("info JSON has no summary: %s", b)
	}
}
