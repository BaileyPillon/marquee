//go:build live

// The live smoke test talks to a real qBittorrent. It is not part of the normal
// test run:
//
//	go test -tags live -run TestLive -v ./internal/downloads/
//
// It expects qBittorrent's WebUI at MARQUEE_QBT_URL (default
// http://127.0.0.1:8080) with user "admin" and the password in
// MARQUEE_QBT_PASSWORD_FILE (default <repo>/.dev-m5b/qbt-password.txt, which is
// not committed). The only content it touches is Big Buck Bunny, a Creative
// Commons film from the WebTorrent demo set, and it only ever adds it paused.
package downloads

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/BaileyPillon/marquee/internal/core/outbound"
)

const (
	liveMagnet   = "magnet:?xt=urn:btih:dd8255ecdc7ca55fb0bbf81323d87062db1f6d1c&dn=Big+Buck+Bunny"
	liveHash     = "dd8255ecdc7ca55fb0bbf81323d87062db1f6d1c"
	liveCategory = "marquee"
)

func liveSettings(t *testing.T) (url, user, password string) {
	t.Helper()
	url = os.Getenv("MARQUEE_QBT_URL")
	if url == "" {
		url = "http://127.0.0.1:8080"
	}
	file := os.Getenv("MARQUEE_QBT_PASSWORD_FILE")
	if file == "" {
		file = filepath.Join("..", "..", ".dev-m5b", "qbt-password.txt")
	}
	b, err := os.ReadFile(file)
	if err != nil {
		t.Skipf("no qBittorrent password file (%v); start the test qBittorrent first", err)
	}
	return url, "admin", strings.TrimSpace(string(b))
}

func liveHTTP() *outbound.Client {
	return outbound.New("live", outbound.Options{Logger: testLogger(), DefaultTimeout: 20 * time.Second})
}

func TestLiveQBittorrent(t *testing.T) {
	url, user, password := liveSettings(t)
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()

	a, err := newQBittorrent(AdapterConfig{Kind: KindQBittorrent, URL: url, Username: user, Password: password, HTTP: liveHTTP()})
	if err != nil {
		t.Fatal(err)
	}

	// Whatever happens, Big Buck Bunny does not stay in the client.
	t.Cleanup(func() {
		cctx, ccancel := context.WithTimeout(context.Background(), 20*time.Second)
		defer ccancel()
		_ = a.Remove(cctx, liveHash, true)
	})
	_ = a.Remove(ctx, liveHash, true) // a leftover from an earlier run

	// 1. Sign in and ask the version.
	version, err := a.Test(ctx)
	if err != nil {
		t.Fatalf("Test: %v", err)
	}
	t.Logf("signed in; qBittorrent version %s", version)

	// 2. Add the magnet paused. The category is created if it is missing.
	ref, err := a.Add(ctx, AddInput{MagnetURL: liveMagnet, Category: liveCategory, Paused: true})
	if err != nil {
		t.Fatalf("Add: %v", err)
	}
	if ref != liveHash {
		t.Fatalf("ref = %s, want %s", ref, liveHash)
	}
	t.Logf("added paused; ref %s", ref)

	status, body, err := a.do(ctx, qbtRequest{method: "GET", path: "/torrents/categories"})
	if err != nil || status != 200 || !strings.Contains(string(body), `"`+liveCategory+`"`) {
		t.Fatalf("category %q is not in qBittorrent: %d %s %v", liveCategory, status, body, err)
	}
	t.Logf("category %q exists", liveCategory)

	// 3. List it. qBittorrent needs a moment to show a new torrent.
	var item Item
	deadline := time.Now().Add(20 * time.Second)
	for {
		items, err := a.List(ctx, liveCategory)
		if err != nil {
			t.Fatalf("List: %v", err)
		}
		found := false
		for _, it := range items {
			if it.Ref == liveHash {
				item, found = it, true
			}
		}
		if found {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("the torrent never showed up in the %q list (%d items)", liveCategory, len(items))
		}
		time.Sleep(500 * time.Millisecond)
	}
	t.Logf("listed: state=%s finished=%v progress=%.2f name=%q save=%q", item.State, item.Finished, item.Progress, item.Name, item.SavePath)
	if item.State != StatePaused || item.Finished {
		t.Errorf("a paused magnet should list as paused, got state %q finished %v", item.State, item.Finished)
	}

	// 4. Pause (it is stopped already, so this only proves the endpoint works;
	// resuming would start the download, which this test never does).
	if err := a.Pause(ctx, ref); err != nil {
		t.Errorf("Pause: %v", err)
	}
	t.Logf("pause endpoint family: %d (1 = stop/start, 2 = pause/resume)", a.pauseAPI.Load())

	// 5. Delete it with its data and see it go.
	if err := a.Remove(ctx, ref, true); err != nil {
		t.Fatalf("Remove: %v", err)
	}
	deadline = time.Now().Add(20 * time.Second)
	for {
		items, err := a.List(ctx, liveCategory)
		if err != nil {
			t.Fatal(err)
		}
		gone := true
		for _, it := range items {
			if it.Ref == liveHash {
				gone = false
			}
		}
		if gone {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("the torrent is still listed after Remove")
		}
		time.Sleep(500 * time.Millisecond)
	}
	t.Log("removed with its data; list is clean")
}

// TestLiveClientThroughTheService checks the same qBittorrent through the
// service: the password is sealed, the client is masked, and Test works.
func TestLiveClientThroughTheService(t *testing.T) {
	url, user, password := liveSettings(t)
	e := newEnv(t)
	e.svc.factory = NewAdapter
	e.svc.hc = liveHTTP()

	c, err := e.svc.AddClient(context.Background(), ClientInput{
		Name: str("live qBittorrent"), Kind: str(KindQBittorrent), URL: str(url), Username: str(user), Password: str(password),
	})
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(c.Password, password) || !strings.HasPrefix(c.Password, "••••") {
		t.Errorf("password in the view = %q", c.Password)
	}
	res, err := e.svc.TestClient(context.Background(), c.ID)
	if err != nil || !res.OK {
		t.Fatalf("test = %+v, %v", res, err)
	}
	t.Logf("service test ok: version %s", res.Version)

	if err := e.svc.Poll(context.Background()); err != nil {
		t.Fatal(err)
	}
	sum, _ := e.svc.Summary(context.Background())
	if len(sum.Clients) != 1 || !sum.Clients[0].OK {
		t.Errorf("summary = %+v", sum)
	}
	t.Logf("poll ok; summary %+v", sum)
}
