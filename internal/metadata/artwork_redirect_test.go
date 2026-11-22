package metadata

import (
	"errors"
	"net/http"
	"os"
	"strings"
	"testing"

	"github.com/BaileyPillon/marquee/internal/core/outbound"
)

// A host name other than the one the fake image host is on (127.0.0.1), which the
// artwork proxy's redirect rules must treat as a different host.
func otherHost(url string) string { return strings.Replace(url, "127.0.0.1", "localhost", 1) }

func TestArtworkRedirectsStayOnTheProvidersHost(t *testing.T) {
	e, img, evil := artEnv(t, 0)
	ref := "release-group/" + caaUUID + "/front-500"

	// The Cover Art Archive sends a cover on to where it is stored: here the same
	// host, standing in for archive.org.
	img.Set("/caa/release-group/"+caaUUID+"/front-500", func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, "/stored/cover.png", http.StatusTemporaryRedirect)
	})
	img.Set("/stored/cover.png", serveBytes(pngBytes(20)))
	f, err := e.svc.Artwork.Open(bg(), "caa", ref)
	if err != nil {
		t.Fatalf("a redirect that stays on the host: %v", err)
	}
	f.Close()
	if f.ContentType != "image/png" {
		t.Errorf("content type %q", f.ContentType)
	}

	// A redirect to any other host is not followed, and nothing there is contacted.
	evil.Set("/steal.png", serveBytes(pngBytes(20)))
	img.Set("/caa/release-group/"+caaUUID+"/front-250", func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, otherHost(evil.URL())+"/steal.png", http.StatusTemporaryRedirect)
	})
	if _, err := e.svc.Artwork.Open(bg(), "caa", "release-group/"+caaUUID+"/front-250"); !errors.Is(err, ErrUpstream) ||
		!errors.Is(err, outbound.ErrRedirectNotAllowed) {
		t.Errorf("a redirect to another host: %v, want an upstream error saying the redirect is not allowed", err)
	}
	if n := evil.Count("/steal.png"); n != 0 {
		t.Errorf("the other host was contacted %d times", n)
	}
	if st := e.svc.Status(bg()).Providers[ProviderArtwork]; st.LastError == "" {
		t.Error("the artwork status should show the refused redirect")
	}
	if entries := cacheFiles(t, e.dir); entries != 1 {
		t.Errorf("%d files cached, want only the first cover", entries)
	}

	// TMDB and TVmaze images are never sent on to another host either.
	img.Set("/t/p/w500/moved.jpg", func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, otherHost(evil.URL())+"/steal.png", http.StatusFound)
	})
	if _, err := e.svc.Artwork.Open(bg(), "tmdb", "w500/moved.jpg"); !errors.Is(err, outbound.ErrRedirectNotAllowed) {
		t.Errorf("tmdb redirect to another host: %v", err)
	}
	img.Set("/tv/uploads/images/original_untouched/1/2.jpg", func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, otherHost(evil.URL())+"/steal.png", http.StatusFound)
	})
	if _, err := e.svc.Artwork.Open(bg(), "tvmaze", "uploads/images/original_untouched/1/2.jpg"); !errors.Is(err, outbound.ErrRedirectNotAllowed) {
		t.Errorf("tvmaze redirect to another host: %v", err)
	}
	if n := evil.Count("/steal.png"); n != 0 {
		t.Errorf("the other host was contacted %d times", n)
	}
}

func cacheFiles(t *testing.T, dir string) int {
	t.Helper()
	n := 0
	list, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	for _, entry := range list {
		if !strings.HasPrefix(entry.Name(), "tmp-") {
			n++
		}
	}
	return n
}

func TestCAAMayOnlyBeSentOnToTheInternetArchive(t *testing.T) {
	// The default Cover Art Archive rule: its own host, archive.org and what is below it.
	e := newEnv(t, Options{})
	rule := e.svc.Artwork.rules["caa"]
	want := []string{"coverartarchive.org", "archive.org", "*.archive.org"}
	if strings.Join(rule.redirects, ",") != strings.Join(want, ",") {
		t.Errorf("caa redirects = %v, want %v", rule.redirects, want)
	}
	if got := strings.Join(CAARedirectHosts, ","); got != "coverartarchive.org,archive.org,*.archive.org" {
		t.Errorf("CAARedirectHosts = %s", got)
	}
	// The others may only stay on their own host.
	if got := e.svc.Artwork.rules["tmdb"].redirects; len(got) != 1 || got[0] != "image.tmdb.org" {
		t.Errorf("tmdb redirects = %v", got)
	}
	if got := e.svc.Artwork.rules["tvmaze"].redirects; len(got) != 1 || got[0] != "static.tvmaze.com" {
		t.Errorf("tvmaze redirects = %v", got)
	}
}
