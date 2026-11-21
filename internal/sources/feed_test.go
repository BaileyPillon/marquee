package sources

import (
	"errors"
	"strings"
	"testing"

	"github.com/BaileyPillon/marquee/internal/grab"
	"github.com/BaileyPillon/marquee/internal/sources/sourcestest"
)

const hashA = "0123456789abcdef0123456789abcdef01234567"

func parseFeed(t *testing.T, xml, kind string) []Item {
	t.Helper()
	items, err := ParseFeed(strings.NewReader(xml), kind, 7)
	if err != nil {
		t.Fatalf("ParseFeed: %v", err)
	}
	return items
}

func TestParseTorrentItem(t *testing.T) {
	it := sourcestest.Torrent("Metropolis.1927.1080p.BluRay.x264-GRP", 8<<30, 42)
	it.Peers, it.IMDb, it.TMDb, it.InfoHash, it.Magnet = 50, "tt0017136", 19, strings.ToUpper(hashA), true
	it.Cats, it.DownFactor, it.UpFactor, it.GUID = []int{2000, 2040}, 0, 1, "metropolis-1"
	items := parseFeed(t, sourcestest.Feed("http://idx.example.invalid", testKey, it), KindTorznab)
	if len(items) != 1 {
		t.Fatalf("%d items", len(items))
	}
	got := items[0]
	if got.IndexerID != 7 || got.GUID != "metropolis-1" || got.Title != it.Title || got.Protocol != grab.ProtocolTorrent {
		t.Errorf("identity wrong: %+v", got)
	}
	if got.Size != 8<<30 || got.Seeders != 42 || got.Peers != 50 || got.Grabs != -1 {
		t.Errorf("numbers wrong: size %d seeders %d peers %d grabs %d", got.Size, got.Seeders, got.Peers, got.Grabs)
	}
	if got.InfoHash != hashA {
		t.Errorf("info hash = %q, want it lower-cased", got.InfoHash)
	}
	if got.IMDb != "tt0017136" || got.TMDb != 19 {
		t.Errorf("ids: imdb %q tmdb %d", got.IMDb, got.TMDb)
	}
	if len(got.Categories) != 2 || got.Categories[0] != 2000 || got.Categories[1] != 2040 {
		t.Errorf("categories = %v", got.Categories)
	}
	if got.DownloadVolumeFactor == nil || *got.DownloadVolumeFactor != 0 || got.UploadVolumeFactor == nil || *got.UploadVolumeFactor != 1 {
		t.Errorf("volume factors = %v %v", got.DownloadVolumeFactor, got.UploadVolumeFactor)
	}
	if got.PublishedAt.IsZero() {
		t.Error("pubDate was not read")
	}
	if !strings.Contains(got.DownloadURL, "/download/metropolis-1") || !strings.HasPrefix(got.MagnetURL, "magnet:?xt=urn:btih:") {
		t.Errorf("links: %q %q", got.DownloadURL, got.MagnetURL)
	}
	// The attribute copy that gets stored and shown holds no link.
	for k, v := range got.Attrs {
		if strings.Contains(k, "url") || strings.Contains(v, "magnet:") || strings.Contains(v, "apikey") {
			t.Errorf("attribute %s=%q looks like a link", k, v)
		}
	}
	if got.Attrs["seeders"] != "42" {
		t.Errorf("attrs = %v", got.Attrs)
	}
}

func TestParseUsenetItem(t *testing.T) {
	it := sourcestest.Item{Title: "Sintel.2010.1080p.WEB-DL.x264-GRP", Size: 3 << 30, Usenet: true, Seeders: 5, Peers: -1,
		DownFactor: -1, UpFactor: -1, IMDb: "tt1727587"}
	items := parseFeed(t, sourcestest.Feed("http://idx.example.invalid", testKey, it), KindNewznab)
	if len(items) != 1 {
		t.Fatalf("%d items", len(items))
	}
	got := items[0]
	if got.Protocol != grab.ProtocolUsenet {
		t.Errorf("protocol = %q, want usenet", got.Protocol)
	}
	if got.Seeders != -1 || got.InfoHash != "" {
		t.Errorf("a usenet item has no seeders or info hash: %d %q", got.Seeders, got.InfoHash)
	}
	if got.DownloadURL == "" || got.MagnetURL != "" || got.IMDb != "tt1727587" {
		t.Errorf("item = %+v", got)
	}
}

func TestParseMagnetOnlyItemIsStillATorrent(t *testing.T) {
	it := sourcestest.Torrent("Night.of.the.Living.Dead.1968.720p.BluRay.x264-GRP", 2<<30, 9)
	it.MagnetOnly = true
	items := parseFeed(t, sourcestest.Feed("http://idx.example.invalid", testKey, it), KindTorznab)
	if len(items) != 1 {
		t.Fatalf("%d items", len(items))
	}
	got := items[0]
	if got.Protocol != grab.ProtocolTorrent {
		t.Errorf("protocol = %q, want torrent", got.Protocol)
	}
	if got.DownloadURL != "" {
		t.Errorf("DownloadURL = %q, want none", got.DownloadURL)
	}
	if !strings.HasPrefix(got.MagnetURL, "magnet:") || got.InfoHash != hashA {
		t.Errorf("magnet %q hash %q (the hash comes out of the magnet link)", got.MagnetURL, got.InfoHash)
	}
	rel := got.Release()
	if rel.Protocol != grab.ProtocolTorrent || rel.MagnetURL == "" || rel.Seeders != 9 {
		t.Errorf("grab.Release = %+v", rel)
	}
}

func TestMagnetHashInBase32IsConvertedToHex(t *testing.T) {
	// 20 zero bytes are 32 base32 "A"s.
	magnet := "magnet:?xt=urn:btih:" + strings.Repeat("A", 32) + "&dn=x"
	if got := hashFromMagnet(magnet); got != strings.Repeat("0", 40) {
		t.Errorf("hash = %q", got)
	}
	if got := hashFromMagnet("magnet:?xt=urn:btih:nothex"); got != "" {
		t.Errorf("a bad hash gave %q", got)
	}
}

func TestParseFeedWithoutGUIDAndWithoutLinks(t *testing.T) {
	xml := `<rss version="2.0" xmlns:torznab="http://torznab.com/schemas/2015/feed"><channel>
	<item><title>Pioneer.One.S01E04.720p.WEB-DL.x264-GRP</title><link>https://idx.example.invalid/get/9</link></item>
	<item><title>No links at all</title></item>
	<item><title></title><link>https://idx.example.invalid/get/10</link></item>
	</channel></rss>`
	items := parseFeed(t, xml, KindTorznab)
	if len(items) != 1 {
		t.Fatalf("got %d items, want 1 (the ones with no title or no link are dropped)", len(items))
	}
	if items[0].GUID != "https://idx.example.invalid/get/9" {
		t.Errorf("guid = %q, want the link", items[0].GUID)
	}
	if items[0].Seeders != -1 || items[0].Size != 0 {
		t.Errorf("unstated numbers: seeders %d size %d", items[0].Seeders, items[0].Size)
	}
}

func TestParseFeedErrors(t *testing.T) {
	_, err := ParseFeed(strings.NewReader(sourcestest.ErrorXML(100, "Incorrect user credentials")), KindTorznab, 1)
	var se *Error
	if !errors.As(err, &se) || se.Code != CodeKeyRefused {
		t.Fatalf("error = %v, want a key_refused Error", err)
	}
	if !strings.Contains(se.Message, "Incorrect user credentials") {
		t.Errorf("message %q should carry the source's own words", se.Message)
	}

	_, err = ParseFeed(strings.NewReader("<html><body>Just a moment...</body></html>"), KindTorznab, 1)
	if !errors.As(err, &se) || se.Code != CodeNotAFeed {
		t.Errorf("html error = %v, want not_a_feed", err)
	}
	_, err = ParseFeed(strings.NewReader("this is not xml"), KindTorznab, 1)
	if !errors.As(err, &se) || se.Code != CodeNotAFeed {
		t.Errorf("garbage error = %v, want not_a_feed", err)
	}
	_, err = ParseFeed(strings.NewReader(sourcestest.ErrorXML(500, "Daily limit reached")), KindTorznab, 1)
	if !errors.As(err, &se) || se.Code != CodeSlowDown {
		t.Errorf("limit error = %v, want slow_down", err)
	}
}

func TestParseImdbSpellings(t *testing.T) {
	for in, want := range map[string]string{"tt0017136": "tt0017136", "0017136": "tt0017136", "17136": "tt0017136", "": ""} {
		it := Item{Attrs: map[string]string{"imdb": in}}
		it.readIDs()
		if it.IMDb != want {
			t.Errorf("imdb %q -> %q, want %q", in, it.IMDb, want)
		}
	}
}

func TestAGUIDNeverKeepsAKey(t *testing.T) {
	xml := `<rss version="2.0" xmlns:torznab="http://torznab.com/schemas/2015/feed"><channel>
	<item><title>Sintel.2010.1080p.BluRay.x264-GRP</title>
	  <guid>https://idx.example.invalid/details?id=77&amp;apikey=SECRETKEY123&amp;t=x</guid>
	  <link>https://idx.example.invalid/get/77?apikey=SECRETKEY123</link></item>
	<item><title>Metropolis.1927.1080p.BluRay.x264-GRP</title>
	  <link>https://idx.example.invalid/dl?id=78&amp;passkey=SECRETKEY123</link></item>
	<item><title>Night.of.the.Living.Dead.1968.720p.BluRay.x264-GRP</title>
	  <torznab:attr name="magneturl" value="magnet:?xt=urn:btih:0123456789abcdef0123456789abcdef01234567&amp;tr=http%3A%2F%2Ft.example.invalid%2Fann%3Fpasskey%3DSECRETKEY123"/></item>
	</channel></rss>`
	items := parseFeed(t, xml, KindTorznab)
	if len(items) != 3 {
		t.Fatalf("%d items", len(items))
	}
	for _, it := range items {
		if strings.Contains(it.GUID, "SECRETKEY123") || strings.Contains(strings.ToLower(it.GUID), "apikey") || strings.Contains(strings.ToLower(it.GUID), "passkey") {
			t.Errorf("guid %q carries a key", it.GUID)
		}
	}
	if items[0].GUID != "https://idx.example.invalid/details?id=77&t=x" {
		t.Errorf("guid = %q: the rest of the address should stay, it tells releases apart", items[0].GUID)
	}
	if items[1].GUID != "https://idx.example.invalid/dl?id=78" {
		t.Errorf("fallback guid = %q", items[1].GUID)
	}
	if items[2].GUID != "magnet:"+hashA {
		t.Errorf("magnet-only guid = %q", items[2].GUID)
	}
	// The link itself is kept, for the grab.
	if !strings.Contains(items[0].DownloadURL, "SECRETKEY123") {
		t.Errorf("the download link lost its key: %q", items[0].DownloadURL)
	}
}
