package metadata

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/BaileyPillon/marquee/internal/core/outbound"
)

const caaUUID = "b0000000-0000-4000-8000-000000000101"

func jpegBytes(n int) []byte {
	return append([]byte{0xFF, 0xD8, 0xFF, 0xE0, 0x00, 0x10, 'J', 'F', 'I', 'F', 0x00}, bytes.Repeat([]byte{0x11}, n)...)
}

func pngBytes(n int) []byte {
	return append([]byte("\x89PNG\r\n\x1a\n"), bytes.Repeat([]byte{0x22}, n)...)
}

func serveBytes(b []byte) http.HandlerFunc {
	return func(w http.ResponseWriter, _ *http.Request) { _, _ = w.Write(b) }
}

// artEnv has one fake image host that plays TMDB's, TVmaze's and the Cover Art
// Archive's, each under its own path prefix, and a second host that must never
// be contacted.
func artEnv(t *testing.T, maxBytes int64) (*env, *fakeAPI, *fakeAPI) {
	t.Helper()
	img := newFakeAPI(t, nil)
	evil := newFakeAPI(t, nil)
	e := newEnv(t, Options{
		ArtworkMaxBytes: maxBytes,
		ArtworkBases: map[string]string{
			"tmdb":   img.URL() + "/t/p",
			"tvmaze": img.URL() + "/tv",
			"caa":    img.URL() + "/caa",
		},
	})
	return e, img, evil
}

func TestArtworkAllowlistAccepts(t *testing.T) {
	e, img, _ := artEnv(t, 0)
	base := img.URL()
	for _, tc := range []struct{ provider, rest, want string }{
		{"tmdb", "w500/abc.jpg", base + "/t/p/w500/abc.jpg"},
		{"tmdb", "original/Xy_Z-123.png", base + "/t/p/original/Xy_Z-123.png"},
		{"tmdb", "h632/abc.webp", base + "/t/p/h632/abc.webp"},
		{"tvmaze", "uploads/images/original_untouched/99/99001.jpg", base + "/tv/uploads/images/original_untouched/99/99001.jpg"},
		{"tvmaze", "uploads/images/medium_portrait/0/1.jpeg", base + "/tv/uploads/images/medium_portrait/0/1.jpeg"},
		{"caa", "release-group/" + caaUUID + "/front-500", base + "/caa/release-group/" + caaUUID + "/front-500"},
		{"caa", "release-group/" + caaUUID + "/front", base + "/caa/release-group/" + caaUUID + "/front"},
		{"caa", "release/" + caaUUID + "/front-250", base + "/caa/release/" + caaUUID + "/front-250"},
	} {
		got, err := e.svc.Artwork.upstreamURL(tc.provider, tc.rest)
		if err != nil || got != tc.want {
			t.Errorf("%s/%s = %q, %v; want %q", tc.provider, tc.rest, got, err, tc.want)
		}
	}
}

func TestArtworkAllowlistRejects(t *testing.T) {
	e, img, evil := artEnv(t, 0)
	badRef := "invalid_artwork"
	for _, tc := range []struct{ provider, rest, code string }{
		// Path traversal, plain and encoded.
		{"tmdb", "w500/../../secret.jpg", badRef},
		{"tmdb", "w500/..%2f..%2fx.jpg", badRef},
		{"tmdb", "w500/%2e%2e/x.jpg", badRef},
		{"tmdb", "w500\\..\\abc.jpg", badRef},
		{"tvmaze", "uploads/images/../../x.jpg", badRef},
		{"tvmaze", "uploads/images/..%2f/x.jpg", badRef},
		{"tvmaze", "uploads/images/a/../b.jpg", badRef},
		{"caa", "release-group/" + caaUUID + "/front-500/../../x", badRef},
		{"caa", "../x", badRef},
		// Another host, by any trick.
		{"tmdb", "//" + evil.URL() + "/w500/abc.jpg", badRef},
		{"tmdb", "w500/@" + evil.URL() + "/abc.jpg", badRef},
		{"tmdb", evil.URL() + "/abc.jpg", badRef},
		{"tmdb", "w500/abc.jpg?host=" + evil.URL(), badRef},
		{"tmdb", "w500/abc.jpg#x", badRef},
		{"tvmaze", "uploads/images/x@" + strings.TrimPrefix(evil.URL(), "http://") + ".jpg", badRef},
		{"caa", "release-group/" + caaUUID + "@" + strings.TrimPrefix(evil.URL(), "http://") + "/front", badRef},
		// Shapes the allowlist does not include.
		{"tmdb", "w500/abc.svg", badRef},
		{"tmdb", "w500/abc", badRef},
		{"tmdb", "bogus/abc.jpg", badRef},
		{"tmdb", "w500", badRef},
		{"tmdb", "", badRef},
		{"tvmaze", "x.jpg", badRef},
		{"tvmaze", "uploads/other/x.jpg", badRef},
		{"tvmaze", "uploads/images/x.exe", badRef},
		{"tvmaze", "uploads/images//x.jpg", badRef},
		{"tvmaze", "uploads/images/a b.jpg", badRef},
		{"tvmaze", "uploads/images/.hidden.jpg", badRef},
		{"caa", "release-group/not-a-uuid/front-500", badRef},
		{"caa", "release-group/" + caaUUID + "/back", badRef},
		{"caa", "release-group/" + caaUUID + "/front-999", badRef},
		{"caa", "artist/" + caaUUID + "/front", badRef},
		{"tmdb", "w500/" + strings.Repeat("a", 400) + ".jpg", badRef},
		// Providers that are not on the list.
		{"evil", "w500/abc.jpg", "not_found"},
		{"", "w500/abc.jpg", "not_found"},
		{"image.tmdb.org", "t/p/w500/abc.jpg", "not_found"},
		{"musicbrainz", "x.jpg", "not_found"},
		{"TMDB", "w500/abc.jpg", "not_found"},
	} {
		f, err := e.svc.Artwork.Open(bg(), tc.provider, tc.rest)
		if err == nil {
			f.Close()
			t.Errorf("%s/%q was allowed", tc.provider, tc.rest)
			continue
		}
		if CodeOf(err) != tc.code {
			t.Errorf("%s/%q: code %q, want %q (%v)", tc.provider, tc.rest, CodeOf(err), tc.code, err)
		}
	}
	if img.Total() != 0 || evil.Total() != 0 {
		t.Errorf("rejected refs reached a server: image host %d, other host %d", img.Total(), evil.Total())
	}
	entries, _ := os.ReadDir(e.dir)
	if len(entries) != 0 {
		t.Errorf("rejected refs left %d files in the cache", len(entries))
	}
}

func TestArtworkDownloadsOnceThenServesFromDisk(t *testing.T) {
	e, img, _ := artEnv(t, 0)
	body := jpegBytes(3000)
	img.Set("/t/p/w500/abc.jpg", serveBytes(body))

	for i := 0; i < 3; i++ {
		f, err := e.svc.Artwork.Open(bg(), "tmdb", "w500/abc.jpg")
		if err != nil {
			t.Fatalf("open %d: %v", i, err)
		}
		got, _ := io.ReadAll(f.File)
		f.Close()
		if !bytes.Equal(got, body) {
			t.Fatalf("open %d: body differs (%d bytes)", i, len(got))
		}
		if f.ContentType != "image/jpeg" || f.Size != int64(len(body)) || f.Tag == "" {
			t.Errorf("open %d: %+v", i, f)
		}
	}
	if n := img.Count("/t/p/w500/abc.jpg"); n != 1 {
		t.Errorf("upstream requests = %d, want 1", n)
	}

	// The file is named after the sha256 of the ref.
	sum := sha256.Sum256([]byte("tmdb/w500/abc.jpg"))
	name := hex.EncodeToString(sum[:])
	if _, err := os.Stat(filepath.Join(e.dir, name)); err != nil {
		t.Errorf("expected cache file %s: %v", name, err)
	}
	entries, _ := os.ReadDir(e.dir)
	if len(entries) != 1 {
		t.Errorf("cache dir holds %d entries (stray temp files?)", len(entries))
	}
	if e.svc.Artwork.CacheSize() != int64(len(body)) {
		t.Errorf("CacheSize = %d", e.svc.Artwork.CacheSize())
	}
	if st := e.svc.Status(bg()).Providers[ProviderArtwork]; st.LastOK == nil || st.LastError != "" {
		t.Errorf("artwork status = %+v", st)
	}
}

func TestArtworkFromEachProvider(t *testing.T) {
	e, img, _ := artEnv(t, 0)
	img.Set("/tv/uploads/images/original_untouched/99/99001.jpg", serveBytes(jpegBytes(10)))
	img.Set("/caa/release-group/"+caaUUID+"/front-500", serveBytes(pngBytes(10)))
	for _, tc := range []struct{ provider, rest, ct string }{
		{"tvmaze", "uploads/images/original_untouched/99/99001.jpg", "image/jpeg"},
		{"caa", "release-group/" + caaUUID + "/front-500", "image/png"},
	} {
		f, err := e.svc.Artwork.Open(bg(), tc.provider, tc.rest)
		if err != nil {
			t.Fatalf("%s: %v", tc.provider, err)
		}
		f.Close()
		if f.ContentType != tc.ct {
			t.Errorf("%s: content type %q", tc.provider, f.ContentType)
		}
	}
}

func TestArtworkRefusesNonImages(t *testing.T) {
	e, img, _ := artEnv(t, 0)
	img.Set("/t/p/w500/page.jpg", serveBytes([]byte("<!doctype html><title>Not an image</title>")))
	img.Set("/t/p/w500/svg.jpg", serveBytes([]byte(`<svg xmlns="http://www.w3.org/2000/svg"><script>alert(1)</script></svg>`)))
	img.Set("/t/p/w500/empty.jpg", serveBytes(nil))
	for _, name := range []string{"page", "svg", "empty"} {
		_, err := e.svc.Artwork.Open(bg(), "tmdb", "w500/"+name+".jpg")
		if !errors.Is(err, ErrUpstream) {
			t.Errorf("%s: err = %v, want upstream error", name, err)
		}
	}
	if entries, _ := os.ReadDir(e.dir); len(entries) != 0 {
		t.Errorf("%d non-image files were cached", len(entries))
	}
}

func TestArtworkNotFoundIsRememberedForAnHour(t *testing.T) {
	e, img, _ := artEnv(t, 0)
	path := "/caa/release-group/" + caaUUID + "/front-500"
	img.Set(path, httpStatus(http.StatusNotFound))
	ref := "release-group/" + caaUUID + "/front-500"

	for i := 0; i < 3; i++ {
		if _, err := e.svc.Artwork.Open(bg(), "caa", ref); !errors.Is(err, ErrNotFound) {
			t.Fatalf("open %d: %v", i, err)
		}
	}
	if n := img.Count(path); n != 1 {
		t.Errorf("a missing cover was asked for %d times, want 1", n)
	}

	// Later the album gets a cover, and the memory of the miss expires.
	img.Set(path, serveBytes(jpegBytes(50)))
	e.clock.Advance(61 * time.Minute)
	f, err := e.svc.Artwork.Open(bg(), "caa", ref)
	if err != nil {
		t.Fatalf("after an hour: %v", err)
	}
	f.Close()
	if n := img.Count(path); n != 2 {
		t.Errorf("requests = %d", n)
	}
}

func TestArtworkUpstreamErrorIsNotCached(t *testing.T) {
	e, img, _ := artEnv(t, 0)
	img.Set("/t/p/w500/abc.jpg", httpStatus(http.StatusInternalServerError))
	for i := 0; i < 2; i++ {
		if _, err := e.svc.Artwork.Open(bg(), "tmdb", "w500/abc.jpg"); !errors.Is(err, ErrUpstream) {
			t.Fatalf("call %d: %v", i, err)
		}
	}
	if n := img.Count("/t/p/w500/abc.jpg"); n != 2 {
		t.Errorf("a 500 must be retried next time, requests = %d", n)
	}
	if st := e.svc.Status(bg()).Providers[ProviderArtwork]; st.LastError == "" {
		t.Error("status should show the artwork error")
	}
	img.Set("/t/p/w500/abc.jpg", serveBytes(jpegBytes(5)))
	f, err := e.svc.Artwork.Open(bg(), "tmdb", "w500/abc.jpg")
	if err != nil {
		t.Fatal(err)
	}
	f.Close()
	if st := e.svc.Status(bg()).Providers[ProviderArtwork]; st.LastError != "" {
		t.Errorf("status after recovery: %+v", st)
	}
}

func TestArtworkPrunesOldestFirstOverTheCap(t *testing.T) {
	e, img, _ := artEnv(t, 7000)
	img.Set("/t/p/w500/new.jpg", serveBytes(jpegBytes(2000-11)))

	// Three files already in the cache, 2000 bytes each, of different ages.
	now := time.Now()
	for i, age := range []time.Duration{3 * time.Hour, 2 * time.Hour, time.Hour} {
		p := filepath.Join(e.dir, "seed"+string(rune('1'+i)))
		if err := os.WriteFile(p, bytes.Repeat([]byte{1}, 2000), 0o600); err != nil {
			t.Fatal(err)
		}
		if err := os.Chtimes(p, now.Add(-age), now.Add(-age)); err != nil {
			t.Fatal(err)
		}
	}
	if got := e.svc.Artwork.CacheSize(); got != 6000 {
		t.Fatalf("seeded size = %d", got)
	}

	f, err := e.svc.Artwork.Open(bg(), "tmdb", "w500/new.jpg") // 8000 > 7000: prune
	if err != nil {
		t.Fatal(err)
	}
	f.Close()

	exists := func(name string) bool { _, err := os.Stat(filepath.Join(e.dir, name)); return err == nil }
	if exists("seed1") {
		t.Error("the oldest file survived the prune")
	}
	if !exists("seed2") || !exists("seed3") {
		t.Error("newer files were removed")
	}
	if size := e.svc.Artwork.CacheSize(); size != 6000 || size > 7000 {
		t.Errorf("size after prune = %d, want 6000 (under the 7000 cap)", size)
	}
	// The image just fetched is still served.
	if f, err := e.svc.Artwork.Open(bg(), "tmdb", "w500/new.jpg"); err != nil {
		t.Errorf("the new file was pruned: %v", err)
	} else {
		f.Close()
	}
}

func TestArtworkPruneKeepsRemovingUntilUnderTheCap(t *testing.T) {
	e, img, _ := artEnv(t, 5000)
	img.Set("/t/p/w500/big.jpg", serveBytes(jpegBytes(4000)))
	now := time.Now()
	for i := 1; i <= 4; i++ {
		p := filepath.Join(e.dir, "seed"+string(rune('0'+i)))
		_ = os.WriteFile(p, bytes.Repeat([]byte{1}, 1000), 0o600)
		_ = os.Chtimes(p, now.Add(-time.Duration(10-i)*time.Hour), now.Add(-time.Duration(10-i)*time.Hour))
	}
	f, err := e.svc.Artwork.Open(bg(), "tmdb", "w500/big.jpg") // 4000 + 4011 > 5000
	if err != nil {
		t.Fatal(err)
	}
	f.Close()
	if size := e.svc.Artwork.CacheSize(); size > 5000 {
		t.Errorf("size = %d, still over the 5000 cap", size)
	}
	// 4000 + 4011 bytes against a 5000 cap: pruning goes on oldest-first until
	// it is under 90 percent of the cap, which takes all four seeds.
	for _, gone := range []string{"seed1", "seed2", "seed3", "seed4"} {
		if _, err := os.Stat(filepath.Join(e.dir, gone)); err == nil {
			t.Errorf("%s should have been pruned", gone)
		}
	}
	if f, err := e.svc.Artwork.Open(bg(), "tmdb", "w500/big.jpg"); err != nil {
		t.Errorf("the new file was pruned: %v", err)
	} else {
		f.Close()
	}
}

func TestArtworkConcurrentRequestsShareOneDownload(t *testing.T) {
	e, img, _ := artEnv(t, 0)
	release := make(chan struct{})
	img.Set("/t/p/w500/abc.jpg", func(w http.ResponseWriter, r *http.Request) {
		<-release
		_, _ = w.Write(jpegBytes(100))
	})
	var wg sync.WaitGroup
	errs := make(chan error, 6)
	for i := 0; i < 6; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			f, err := e.svc.Artwork.Open(bg(), "tmdb", "w500/abc.jpg")
			if err == nil {
				f.Close()
			}
			errs <- err
		}()
	}
	time.Sleep(50 * time.Millisecond) // let them all queue up behind the first
	close(release)
	wg.Wait()
	close(errs)
	for err := range errs {
		if err != nil {
			t.Errorf("open: %v", err)
		}
	}
	if n := img.Count("/t/p/w500/abc.jpg"); n != 1 {
		t.Errorf("upstream requests = %d, want 1", n)
	}
}

func TestArtworkNonImageFileInCacheIsNeverServed(t *testing.T) {
	e, img, _ := artEnv(t, 0)
	img.Set("/t/p/w500/abc.jpg", serveBytes(jpegBytes(20)))
	// Something that is not an image sits where the cache file belongs.
	sum := sha256.Sum256([]byte("tmdb/w500/abc.jpg"))
	p := filepath.Join(e.dir, hex.EncodeToString(sum[:]))
	if err := os.WriteFile(p, []byte("<script>alert(1)</script>"), 0o600); err != nil {
		t.Fatal(err)
	}
	f, err := e.svc.Artwork.Open(bg(), "tmdb", "w500/abc.jpg")
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	if f.ContentType != "image/jpeg" {
		t.Errorf("served %q from a corrupted cache file", f.ContentType)
	}
	if img.Count("/t/p/w500/abc.jpg") != 1 {
		t.Error("the bad cache file was not replaced by a fresh download")
	}
}

func TestArtworkNewCreatesTheCacheDirectory(t *testing.T) {
	e, _, _ := artEnv(t, 0)
	if fi, err := os.Stat(e.dir); err != nil || !fi.IsDir() {
		t.Fatalf("cache dir not created: %v", err)
	}
	if _, err := New(e.db, outbound.New("1.2.3", outbound.Options{}), Options{}); err == nil {
		t.Error("New without an artwork directory should fail")
	}
}
