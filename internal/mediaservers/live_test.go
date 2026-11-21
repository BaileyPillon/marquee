//go:build live

package mediaservers

// A live check against a real Jellyfin on this machine. It is not part of the
// normal test run: build it in with -tags live.
//
//	go test -tags live -run TestLiveJellyfin -v ./internal/mediaservers/
//
// It reads the address and API key from .dev-m6/jellyfin.txt at the repo root
// (gitignored; lines of key=value: url, apikey), or from the file named by
// MARQUEE_LIVE_JELLYFIN. The library folders are M:\MarqueeTest\media (movies
// and tv under it), or MARQUEE_LIVE_MEDIA. If .dev-m6/sample.mkv exists it is
// used as the film; otherwise a few made-up bytes are, which Jellyfin may not
// recognise as video.

import (
	"bytes"
	"errors"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/BaileyPillon/marquee/internal/core/outbound"
	"github.com/BaileyPillon/marquee/internal/titles"
)

func liveCreds(t *testing.T) (url, key string) {
	t.Helper()
	path := os.Getenv("MARQUEE_LIVE_JELLYFIN")
	if path == "" {
		path = filepath.Join("..", "..", ".dev-m6", "jellyfin.txt")
	}
	b, err := os.ReadFile(path)
	if err != nil {
		t.Skipf("no Jellyfin credentials at %s: %v", path, err)
	}
	for _, line := range strings.Split(string(b), "\n") {
		k, v, ok := strings.Cut(strings.TrimSpace(line), "=")
		if !ok {
			continue
		}
		switch k {
		case "url":
			url = v
		case "apikey":
			key = v
		}
	}
	if url == "" || key == "" {
		t.Skip("jellyfin.txt has no url or apikey")
	}
	return url, key
}

func TestLiveJellyfin(t *testing.T) {
	url, key := liveCreds(t)
	media := os.Getenv("MARQUEE_LIVE_MEDIA")
	if media == "" {
		media = `M:\MarqueeTest\media`
	}

	var logs bytes.Buffer
	log := slog.New(slog.NewTextHandler(&logs, &slog.HandlerOptions{Level: slog.LevelDebug}))
	e := newEnv(t)
	e.svc.hc = outbound.New("live", outbound.Options{Logger: log, HostRPS: 100, HostBurst: 100, MaxRetries: -1})
	v, err := e.svc.Add(bg(), Input{Name: str("Jellyfin (live)"), Kind: str("jellyfin"), URL: str(url), Token: str(key)})
	if err != nil {
		t.Fatal(err)
	}

	// 1. Test the connection.
	res, err := e.svc.Test(bg(), v.ID)
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("test connection: ok=%v version=%q error=%q", res.OK, res.Version, res.Error)
	for _, l := range res.Libraries {
		t.Logf("  library %q (%s): %v", l.Name, l.ID, l.Paths)
	}
	if !res.OK {
		t.Fatalf("the connection test failed: %s", res.Error)
	}
	var movies bool
	for _, l := range res.Libraries {
		for _, p := range l.Paths {
			if strings.EqualFold(p, filepath.Join(media, "movies")) {
				movies = true
			}
		}
	}
	if !movies {
		t.Fatalf("no library reads %s", filepath.Join(media, "movies"))
	}

	// 2. Put a tiny file where an import would.
	dir := filepath.Join(media, "movies", "Night of the Living Dead (1968)")
	file := filepath.Join(dir, "Night of the Living Dead (1968) Bluray-1080p.mkv")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	data := []byte("marquee live test: not really a film\x00\x01\x02")
	kind := "made-up bytes"
	if b, err := os.ReadFile(filepath.Join("..", "..", ".dev-m6", "sample.mkv")); err == nil {
		data, kind = b, "a real tiny mkv"
	}
	if err := os.WriteFile(file, data, 0o644); err != nil {
		t.Fatal(err)
	}
	t.Logf("wrote %s (%d bytes, %s)", file, len(data), kind)
	defer func() {
		_ = os.Remove(file)
		_ = os.Remove(dir)
	}()

	// 3. Tell Jellyfin.
	out := e.svc.Notify(bg(), Event{Kind: EventCreated, Paths: []string{file}})
	if len(out) != 1 || !out[0].OK {
		t.Fatalf("notify created: %+v", out)
	}
	t.Logf("notify created: ok, sent %v", out[0].Sent)

	// 4. Ask for the title's page. Jellyfin waits a while before it picks up a
	// change, and may not recognise a made-up file at all.
	title := titles.Title{ID: 1, Kind: titles.KindMovie, Name: "Night of the Living Dead", Year: 1968, TMDBID: 10331, IMDbID: "tt0063350"}
	deadline := time.Now().Add(110 * time.Second)
	var open *OpenResult
	for {
		open, err = e.svc.OpenURL(bg(), title)
		if err == nil || !errors.Is(err, ErrNotOnServer) || time.Now().After(deadline) {
			break
		}
		time.Sleep(5 * time.Second)
	}
	switch {
	case err == nil:
		t.Logf("OpenURL: %s (server %q)", open.URL, open.Server.Name)
		if !strings.Contains(open.URL, "/web/#/details?id=") {
			t.Errorf("unexpected page address %q", open.URL)
		}
	case errors.Is(err, ErrNotOnServer):
		t.Logf("OpenURL: the title is not on the server (404 not_found_on_server) after waiting; Jellyfin did not recognise %s", kind)
	default:
		t.Errorf("OpenURL failed: %v", err)
	}

	// 5. Clean up, and tell Jellyfin the file is gone.
	_ = os.Remove(file)
	_ = os.Remove(dir)
	gone := e.svc.Notify(bg(), Event{Kind: EventDeleted, Paths: []string{file}})
	if len(gone) != 1 || !gone[0].OK {
		t.Errorf("notify deleted: %+v", gone)
	}

	for _, l := range strings.Split(logs.String(), "\n") {
		if strings.Contains(l, "msg=outbound") {
			t.Log(l)
		}
	}
}
