package downloads

import (
	"context"
	"crypto/sha1" //nolint:gosec // info-hashes are SHA-1
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/BaileyPillon/marquee/internal/grab"
)

const (
	bbbHex    = "dd8255ecdc7ca55fb0bbf81323d87062db1f6d1c"
	bbbBase32 = "3WBFL3G4PSSV7MF37AJSHWDQMLNR63I4"
)

func TestBencodeDecode(t *testing.T) {
	v, _, _, err := decodeBencode([]byte("d3:bar4:spam3:fooi42e4:listl1:ai-7ee4:nestd1:xi0eee"))
	if err != nil {
		t.Fatal(err)
	}
	m := v.(map[string]any)
	if m["bar"] != "spam" || m["foo"] != int64(42) {
		t.Errorf("scalars: %v", m)
	}
	l := m["list"].([]any)
	if len(l) != 2 || l[0] != "a" || l[1] != int64(-7) {
		t.Errorf("list = %v", l)
	}
	if n := m["nest"].(map[string]any); n["x"] != int64(0) {
		t.Errorf("nested = %v", n)
	}
	// Binary string, empty string and empty containers.
	v, _, _, err = decodeBencode([]byte("l4:\x00\xff\x01\x02" + "0:" + "le" + "de" + "e"))
	if err != nil {
		t.Fatal(err)
	}
	if l := v.([]any); len(l) != 4 || l[0] != "\x00\xff\x01\x02" || l[1] != "" {
		t.Errorf("binary list = %q", l)
	}
}

func TestBencodeRejectsBadInput(t *testing.T) {
	for name, in := range map[string]string{
		"empty":             "",
		"unknown type":      "x",
		"truncated int":     "i42",
		"empty int":         "ie",
		"negative zero":     "i-0e",
		"leading zero":      "i03e",
		"not a number":      "i4x2e",
		"int overflow":      "i99999999999999999999e",
		"short string":      "10:abc",
		"no colon":          "10",
		"bad length":        "01:a",
		"unterminated list": "li1e",
		"unterminated dict": "d1:ai1e",
		"non-string key":    "di1ei2ee",
		"missing value":     "d1:ae",
		"too deep":          strings.Repeat("l", 100) + strings.Repeat("e", 100),
	} {
		if _, _, _, err := decodeBencode([]byte(in)); err == nil {
			t.Errorf("%s: %q should be rejected", name, in)
		}
	}
}

func TestParseTorrentInfoHashIsSHA1OfTheRawInfoDict(t *testing.T) {
	info := tinyInfo("Big Buck Bunny.mkv", 276134947)
	meta, err := ParseTorrent(tinyTorrent(info))
	if err != nil {
		t.Fatal(err)
	}
	sum := sha1.Sum([]byte(info))
	if want := hex.EncodeToString(sum[:]); meta.InfoHash != want {
		t.Errorf("InfoHash = %s, want %s", meta.InfoHash, want)
	}
	if meta.InfoHash != strings.ToLower(meta.InfoHash) || len(meta.InfoHash) != 40 {
		t.Errorf("not lower-case 40-char hex: %q", meta.InfoHash)
	}
	if meta.Name != "Big Buck Bunny.mkv" || meta.Size != 276134947 {
		t.Errorf("meta = %+v", meta)
	}

	// A different name gives a different hash; the same info dict inside a
	// different wrapper gives the same one.
	other, _ := ParseTorrent(tinyTorrent(tinyInfo("Sintel.mkv", 1)))
	if other.InfoHash == meta.InfoHash {
		t.Error("two different torrents share a hash")
	}
	bare := "d4:info" + info + "e"
	again, err := ParseTorrent([]byte(bare))
	if err != nil || again.InfoHash != meta.InfoHash {
		t.Errorf("same info in another wrapper: %v %v", again, err)
	}
}

func TestParseTorrentMultiFileSizeAndNesting(t *testing.T) {
	// "files" is a list of dictionaries: the hash must cover them byte for byte.
	info := "d5:filesld6:lengthi100e4:pathl1:a5:b.mkveed6:lengthi50e4:pathl5:c.srteee4:name4:pack12:piece lengthi16384e6:pieces20:AAAAAAAAAAAAAAAAAAAAe"
	meta, err := ParseTorrent([]byte("d4:info" + info + "e"))
	if err != nil {
		t.Fatal(err)
	}
	sum := sha1.Sum([]byte(info))
	if meta.InfoHash != hex.EncodeToString(sum[:]) {
		t.Errorf("hash = %s", meta.InfoHash)
	}
	if meta.Size != 150 || meta.Name != "pack" {
		t.Errorf("meta = %+v", meta)
	}
}

func TestParseTorrentV2OnlyUsesTruncatedSHA256(t *testing.T) {
	info := "d9:file treed1:ad0:d6:lengthi5eeee12:meta versioni2e4:name1:a12:piece lengthi16384ee"
	meta, err := ParseTorrent([]byte("d4:info" + info + "e"))
	if err != nil {
		t.Fatal(err)
	}
	sum := sha256.Sum256([]byte(info))
	if want := hex.EncodeToString(sum[:20]); meta.InfoHash != want {
		t.Errorf("v2-only hash = %s, want %s", meta.InfoHash, want)
	}
}

func TestParseTorrentRejectsNonTorrents(t *testing.T) {
	for name, in := range map[string]string{
		"empty":           "",
		"html":            "<html>please sign in</html>",
		"dict no info":    "d3:foo3:bare",
		"info not a dict": "d4:info3:abce",
		"truncated":       "d4:infod4:name1:a",
	} {
		if _, err := ParseTorrent([]byte(in)); err == nil {
			t.Errorf("%s should be rejected", name)
		}
	}
}

func TestMagnetInfoHash(t *testing.T) {
	cases := []struct{ name, magnet, want string }{
		{"hex", "magnet:?xt=urn:btih:" + bbbHex + "&dn=Big+Buck+Bunny", bbbHex},
		{"upper hex", "magnet:?xt=urn:btih:" + strings.ToUpper(bbbHex), bbbHex},
		{"base32", "magnet:?xt=urn:btih:" + bbbBase32 + "&dn=x", bbbHex},
		{"lower base32", "magnet:?xt=urn:btih:" + strings.ToLower(bbbBase32), bbbHex},
		{"xt after other params", "magnet:?dn=x&tr=udp%3A%2F%2Ft.invalid%3A1&xt=urn:btih:" + bbbHex, bbbHex},
		{"upper-case scheme and key", "MAGNET:?XT=urn:btih:" + bbbHex, bbbHex},
		{"v2 only", "magnet:?xt=urn:btmh:1220" + strings.Repeat("ab", 32), strings.Repeat("ab", 20)},
		{"hybrid prefers btih", "magnet:?xt=urn:btmh:1220" + strings.Repeat("cd", 32) + "&xt=urn:btih:" + bbbHex, bbbHex},
	}
	for _, c := range cases {
		got, err := MagnetInfoHash(c.magnet)
		if err != nil || got != c.want {
			t.Errorf("%s: got %q, %v; want %q", c.name, got, err, c.want)
		}
	}
	for _, bad := range []string{
		"", "http://example.com/?xt=urn:btih:" + bbbHex, "magnet:?dn=x", "magnet:?xt=urn:btih:short",
		"magnet:?xt=urn:btih:" + strings.Repeat("z", 40), "magnet:?xt=urn:sha1:" + bbbHex,
		"magnet:?xt=urn:btih:" + strings.Repeat("1", 32), // 32 chars but not valid base32 ('1')
	} {
		if h, err := MagnetInfoHash(bad); err == nil {
			t.Errorf("%q should be rejected, got %q", bad, h)
		}
	}
}

// indexer is a fake torrent source.
func indexer(t *testing.T, handler http.HandlerFunc) (*httptest.Server, *atomic.Int32) {
	t.Helper()
	var hits atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits.Add(1)
		handler(w, r)
	}))
	t.Cleanup(srv.Close)
	return srv, &hits
}

func TestFetchTorrentFile(t *testing.T) {
	file := tinyTorrent(tinyInfo("a.mkv", 10))
	srv, _ := indexer(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Query().Get("apikey") != "SECRET-KEY" {
			http.Error(w, "no", http.StatusForbidden)
			return
		}
		w.Header().Set("Content-Type", "application/x-bittorrent")
		_, _ = w.Write(file)
	})
	f, err := fetchTorrent(context.Background(), testHTTP(), srv.URL+"/dl?apikey=SECRET-KEY")
	if err != nil {
		t.Fatal(err)
	}
	want, _ := ParseTorrent(file)
	if string(f.File) != string(file) || f.Meta.InfoHash != want.InfoHash || f.Magnet != "" {
		t.Errorf("fetched = %+v", f.Meta)
	}
}

func TestFetchTorrentFollowsWebRedirects(t *testing.T) {
	file := tinyTorrent(tinyInfo("b.mkv", 10))
	srv, _ := indexer(t, func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/start":
			http.Redirect(w, r, "/middle", http.StatusFound)
		case "/middle":
			http.Redirect(w, r, "/file.torrent", http.StatusMovedPermanently)
		default:
			_, _ = w.Write(file)
		}
	})
	f, err := fetchTorrent(context.Background(), testHTTP(), srv.URL+"/start")
	if err != nil || len(f.File) == 0 {
		t.Fatalf("fetch: %v", err)
	}
}

func TestFetchTorrentMagnetRedirectIsNotFollowed(t *testing.T) {
	srv, hits := indexer(t, func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, "magnet:?xt=urn:btih:"+bbbHex+"&dn=Big+Buck+Bunny", http.StatusFound)
	})
	f, err := fetchTorrent(context.Background(), testHTTP(), srv.URL+"/get?apikey=SECRET-KEY")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(f.Magnet, "magnet:?xt=urn:btih:"+bbbHex) || len(f.File) != 0 {
		t.Errorf("fetched = %+v", f)
	}
	if hits.Load() != 1 {
		t.Errorf("indexer hit %d times, want 1", hits.Load())
	}
}

func TestFetchTorrentFailuresNameNoURLOrKey(t *testing.T) {
	cases := map[string]http.HandlerFunc{
		"html login page": func(w http.ResponseWriter, r *http.Request) { _, _ = w.Write([]byte("<html>sign in</html>")) },
		"404":             func(w http.ResponseWriter, r *http.Request) { http.NotFound(w, r) },
		"500":             func(w http.ResponseWriter, r *http.Request) { http.Error(w, "boom", 500) },
		"loop": func(w http.ResponseWriter, r *http.Request) {
			http.Redirect(w, r, r.URL.Path+"x?apikey=SECRET-KEY", http.StatusFound)
		},
		"redirect with no place to go": func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(http.StatusFound) },
		"redirect to ftp": func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Location", "ftp://example.invalid/a.torrent?apikey=SECRET-KEY")
			w.WriteHeader(http.StatusFound)
		},
		"huge": func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "application/x-bittorrent")
			chunk := []byte(strings.Repeat("d", 1<<20))
			for i := 0; i < 12; i++ {
				_, _ = w.Write(chunk)
			}
		},
	}
	for name, h := range cases {
		srv, _ := indexer(t, h)
		_, err := fetchTorrent(context.Background(), testHTTP(), srv.URL+"/dl?apikey=SECRET-KEY")
		if err == nil {
			t.Errorf("%s: want an error", name)
			continue
		}
		if strings.Contains(err.Error(), "SECRET-KEY") {
			t.Errorf("%s: the indexer key is in the error: %v", name, err)
		}
	}

	// A closed server: the connection error must not carry the key either.
	srv, _ := indexer(t, func(http.ResponseWriter, *http.Request) {})
	url := srv.URL + "/dl?apikey=SECRET-KEY"
	srv.Close()
	if _, err := fetchTorrent(context.Background(), testHTTP(), url); err == nil || strings.Contains(err.Error(), "SECRET-KEY") {
		t.Errorf("closed server: %v", err)
	}
}

func TestFetchTorrentRejectsNonWebLinks(t *testing.T) {
	for _, link := range []string{"file:///etc/passwd", "ftp://x/y.torrent", "not a url", ""} {
		if _, err := fetchTorrent(context.Background(), testHTTP(), link); err == nil {
			t.Errorf("%q should be rejected", link)
		}
	}
}

func TestFetchTorrentErrorsNameNoPartOfTheLink(t *testing.T) {
	// Private trackers put a passkey in the path, not only in the query.
	const passkey = "PASSKEY-0123456789"
	srv, _ := indexer(t, func(http.ResponseWriter, *http.Request) {})
	link := srv.URL + "/download.php/" + passkey + "/file.torrent"
	srv.Close() // connection refused: the error mentions the address Go dialled

	_, err := fetchTorrent(context.Background(), testHTTP(), link)
	if err == nil {
		t.Fatal("want an error")
	}
	if strings.Contains(err.Error(), passkey) || strings.Contains(err.Error(), "download.php") {
		t.Errorf("part of the link is in the error: %v", err)
	}
	// And through Grab, where it becomes a release_unavailable error.
	e := newEnv(t)
	e.addClient("qb", KindQBittorrent, "http://qb:8080")
	_, err = e.svc.Grab(context.Background(), e.target(e.addTitle("T", 2000)),
		grab.Release{Title: "x", Protocol: grab.ProtocolTorrent, DownloadURL: link})
	if !errors.Is(err, ErrReleaseUnavailable) || strings.Contains(err.Error(), passkey) {
		t.Errorf("grab error = %v", err)
	}
}
