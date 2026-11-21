package downloads

import (
	"context"
	"errors"
	"reflect"
	"testing"

	"github.com/BaileyPillon/marquee/internal/core/pathmap"
)

func TestClientPathRulesAreStoredShownAndKept(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	rules := []pathmap.Rule{{From: " /downloads ", To: `D:\dl`}, {From: "/tv", To: `E:\tv`}}
	c := e.addClient("qb", KindQBittorrent, "http://qb:8080", func(in *ClientInput) { in.PathMap = &rules })
	want := []pathmap.Rule{{From: "/downloads", To: `D:\dl`}, {From: "/tv", To: `E:\tv`}}
	if !reflect.DeepEqual(c.PathMap, want) {
		t.Fatalf("path map = %+v", c.PathMap)
	}

	// A client with none shows an empty list, not null.
	plain := e.addClient("sab", KindSABnzbd, "http://sab:8080")
	if plain.PathMap == nil || len(plain.PathMap) != 0 {
		t.Errorf("no rules should be [], got %#v", plain.PathMap)
	}

	// Editing something else keeps the rules; sending a list replaces them; an empty list clears them.
	up, err := e.svc.UpdateClient(ctx, c.ID, ClientInput{Category: str("movies")})
	if err != nil || !reflect.DeepEqual(up.PathMap, want) {
		t.Errorf("rules after an unrelated edit: %+v, %v", up.PathMap, err)
	}
	one := []pathmap.Rule{{From: "/data", To: "/mnt/data"}}
	if up, err = e.svc.UpdateClient(ctx, c.ID, ClientInput{PathMap: &one}); err != nil || !reflect.DeepEqual(up.PathMap, one) {
		t.Errorf("replaced rules: %+v, %v", up.PathMap, err)
	}
	none := []pathmap.Rule{}
	if up, err = e.svc.UpdateClient(ctx, c.ID, ClientInput{PathMap: &none}); err != nil || len(up.PathMap) != 0 {
		t.Errorf("cleared rules: %+v, %v", up.PathMap, err)
	}

	got, err := e.svc.ClientPaths(ctx, c.ID)
	if err != nil || len(got.PathMap) != 0 {
		t.Errorf("ClientPaths = %+v, %v", got, err)
	}
	if _, err := e.svc.UpdateClient(ctx, c.ID, ClientInput{PathMap: &one, SavePath: str("/data/torrents")}); err != nil {
		t.Fatal(err)
	}
	got, _ = e.svc.ClientPaths(ctx, c.ID)
	if got.SavePath != "/data/torrents" || !reflect.DeepEqual(got.PathMap, one) {
		t.Errorf("ClientPaths = %+v", got)
	}
	if _, err := e.svc.ClientPaths(ctx, 999); !errors.Is(err, ErrClientNotFound) {
		t.Errorf("unknown client: %v", err)
	}
}

func TestBadClientPathRulesAreRefused(t *testing.T) {
	e := newEnv(t)
	c := e.addClient("qb", KindQBittorrent, "http://qb:8080")
	for _, rules := range [][]pathmap.Rule{
		{{From: "/a"}},
		{{To: "/b"}},
		{{From: "/a\x00", To: "/b"}},
	} {
		_, err := e.svc.UpdateClient(context.Background(), c.ID, ClientInput{PathMap: &rules})
		var ie *InvalidError
		if !errors.As(err, &ie) {
			t.Errorf("%+v: want an InvalidError, got %v", rules, err)
		}
	}
	tooMany := make([]pathmap.Rule, pathmap.MaxRules+1)
	for i := range tooMany {
		tooMany[i] = pathmap.Rule{From: "/a", To: "/b"}
	}
	if _, err := e.svc.UpdateClient(context.Background(), c.ID, ClientInput{PathMap: &tooMany}); err == nil {
		t.Error("too many rules should be refused")
	}
}
