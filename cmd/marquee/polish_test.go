package main

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/BaileyPillon/marquee/internal/grab"
	"github.com/BaileyPillon/marquee/internal/history"
)

func TestGrabbedHistoryNamesTheSourceTheReleaseCameFrom(t *testing.T) {
	rec := &recordedEntries{}
	names := func(_ context.Context, id int64) string {
		if id == 3 {
			return "Archive feed"
		}
		return ""
	}
	g := recordedGrabber(stubGrabber{id: 9}, rec, names)
	release := grab.Release{Title: "Metropolis.1927.2160p.BluRay.x265-GRP", Protocol: grab.ProtocolTorrent, IndexerID: 3}
	if _, err := g.Grab(context.Background(), grab.Target{TitleID: 4}, release); err != nil {
		t.Fatal(err)
	}
	d, _ := rec.entries[0].Detail.(map[string]any)
	if d["indexer"] != "Archive feed" {
		t.Errorf("detail = %v", d)
	}
	if got := history.Text(history.Grabbed, mustJSONOf(t, d)); got != "Grabbed Metropolis.1927.2160p.BluRay.x265-GRP from Archive feed" {
		t.Errorf("the history line is %q", got)
	}

	// A source that has no name (deleted since), and a release with no source, add nothing.
	rec.entries = nil
	for _, id := range []int64{0, 99} {
		release.IndexerID = id
		if _, err := g.Grab(context.Background(), grab.Target{TitleID: 4}, release); err != nil {
			t.Fatal(err)
		}
	}
	for _, e := range rec.entries {
		if d, _ := e.Detail.(map[string]any); d["indexer"] != nil {
			t.Errorf("detail = %v", d)
		}
	}
}

func TestIndexerNamesBeforeTheSourcesExist(t *testing.T) {
	var n indexerNames
	if got := n.name(context.Background(), 1); got != "" {
		t.Errorf("name before the sources were set = %q", got)
	}
}

func TestAnInvalidTrustedProxyListStopsTheStart(t *testing.T) {
	t.Setenv("MARQUEE_TRUSTED_PROXIES", "10.0.0.0/8, not-an-address")
	cfg := filepath.Join(t.TempDir(), "config")
	err := serve(context.Background(), newLogger(&lockedBuffer{}, "info"), "127.0.0.1:0", cfg)
	if err == nil || !strings.Contains(err.Error(), "MARQUEE_TRUSTED_PROXIES") || !strings.Contains(err.Error(), "not-an-address") {
		t.Errorf("err = %v, want one that names the variable and the entry", err)
	}
	if _, statErr := os.Stat(cfg); statErr != nil {
		t.Logf("config folder: %v", statErr)
	}
}

func mustJSONOf(t *testing.T, v any) json.RawMessage {
	t.Helper()
	b, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	return b
}
