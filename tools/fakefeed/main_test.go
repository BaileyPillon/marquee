package main

import (
	"bytes"
	"context"
	"encoding/xml"
	"errors"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	// The point of these tests: what fakefeed writes must be read by Marquee's own
	// Torznab code. Importing an internal package is allowed from inside the module.
	"github.com/BaileyPillon/marquee/internal/grab"
	"github.com/BaileyPillon/marquee/internal/sources"
)

// sintelEntry is the entry the example file must carry, character for character.
const sintelEntry = `{"title":"Sintel.2010.720p.WEB-DL.x264-Blender","link":"https://webtorrent.io/torrents/sintel.torrent","magnet":"","size":129241752,"seeders":50,"peers":5,"imdbid":"tt1727587","tmdbid":"45745","category":2040,"infohash":"","pubDate":"Sat, 21 Nov 2026 12:00:00 +0000"}`

type syncBuffer struct {
	mu sync.Mutex
	b  bytes.Buffer
}

func (s *syncBuffer) Write(p []byte) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.b.Write(p)
}

func (s *syncBuffer) String() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.b.String()
}

func exampleItems(t *testing.T) []item {
	t.Helper()
	items, err := loadItems("example-items.json")
	if err != nil {
		t.Fatal(err)
	}
	return items
}

// start serves items (and an optional key) on a test server and returns its /api
// address and the request log.
func start(t *testing.T, items []item, key string) (api string, logs *syncBuffer) {
	t.Helper()
	logs = &syncBuffer{}
	s := newServer(items, key, slog.New(slog.NewTextHandler(logs, nil)))
	s.now = func() time.Time { return time.Date(2026, 11, 22, 8, 30, 0, 0, time.UTC) }
	srv := httptest.NewServer(s.handler())
	t.Cleanup(srv.Close)
	return srv.URL + "/api", logs
}

func get(t *testing.T, api string, params url.Values) (status int, header http.Header, body []byte) {
	t.Helper()
	resp, err := http.Get(api + "?" + params.Encode())
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	body, err = io.ReadAll(resp.Body)
	if err != nil {
		t.Fatal(err)
	}
	return resp.StatusCode, resp.Header, body
}

// feed asks the server and reads the answer with Marquee's feed parser.
func feed(t *testing.T, api string, params url.Values) ([]sources.Item, error) {
	t.Helper()
	status, _, body := get(t, api, params)
	if status != 200 {
		t.Fatalf("%v: HTTP %d: %s", params, status, body)
	}
	return sources.ParseFeed(bytes.NewReader(body), sources.KindTorznab, 1)
}

func titles(items []sources.Item) []string {
	out := make([]string, len(items))
	for i, it := range items {
		out[i] = it.Title
	}
	return out
}

func fetchCaps(t *testing.T, api string) *sources.Caps {
	t.Helper()
	status, _, body := get(t, api, url.Values{"t": {"caps"}})
	if status != 200 {
		t.Fatalf("caps: HTTP %d", status)
	}
	c, err := sources.ParseCaps(bytes.NewReader(body), time.Now())
	if err != nil {
		t.Fatalf("Marquee cannot read the caps: %v\n%s", err, body)
	}
	return c
}

// ---- the example file ---------------------------------------------------------------

func TestExampleFile(t *testing.T) {
	raw, err := os.ReadFile("example-items.json")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(raw), sintelEntry) {
		t.Error("example-items.json lacks the Sintel entry, exactly as specified")
	}
	items := exampleItems(t)
	if len(items) < 2 {
		t.Fatalf("%d items, want a few", len(items))
	}
	// Only Blender's Creative Commons films, per SPEC.md's test-library rule.
	allowed := []string{"Sintel", "Big.Buck.Bunny", "Tears.of.Steel", "Cosmos.Laundromat", "Elephants.Dream", "Spring", "Agent.327"}
	for _, it := range items {
		ok := slices.ContainsFunc(allowed, func(a string) bool { return strings.HasPrefix(it.Title, a+".") })
		if !ok || !strings.HasSuffix(it.Title, "-Blender") {
			t.Errorf("%q is not a Blender open film release name", it.Title)
		}
		if !strings.HasPrefix(it.Link, "https://webtorrent.io/torrents/") {
			t.Errorf("%q links to %q", it.Title, it.Link)
		}
	}
}

// ---- Marquee reads what fakefeed writes ------------------------------------------------

func TestMarqueeReadsTheCaps(t *testing.T) {
	api, _ := start(t, exampleItems(t), "")
	c := fetchCaps(t, api)

	for _, mode := range []string{sources.ModeSearch, sources.ModeMovie, sources.ModeTV, sources.ModeMusic} {
		if !c.ModeAvailable(mode) {
			t.Errorf("mode %s is not available", mode)
		}
	}
	for _, p := range []struct{ mode, param string }{
		{sources.ModeSearch, "q"}, {sources.ModeMovie, "q"}, {sources.ModeMovie, "imdbid"}, {sources.ModeMovie, "tmdbid"},
		{sources.ModeTV, "q"}, {sources.ModeTV, "season"}, {sources.ModeTV, "ep"}, {sources.ModeMusic, "q"},
	} {
		if !c.Supports(p.mode, p.param) {
			t.Errorf("mode %s does not list %s", p.mode, p.param)
		}
	}
	var ids []int
	for _, cat := range c.Categories {
		ids = append(ids, cat.ID)
	}
	for _, want := range []int{2000, 2040, 2045, 3000, 5000} {
		if !slices.Contains(ids, want) {
			t.Errorf("category %d missing from %v", want, ids)
		}
	}
	if c.Title != "fakefeed" || c.LimitMax <= 0 || c.LimitDefault <= 0 {
		t.Errorf("caps = %+v", c)
	}
}

func TestMarqueeReadsTheFeed(t *testing.T) {
	src := exampleItems(t)
	api, _ := start(t, src, "")

	// No query at all is the RSS mode: everything comes back.
	items, err := feed(t, api, url.Values{"t": {"search"}})
	if err != nil {
		t.Fatalf("Marquee cannot read the feed: %v", err)
	}
	if len(items) != len(src) {
		t.Fatalf("%d items, want %d: %v", len(items), len(src), titles(items))
	}

	sintel := items[0]
	if sintel.Title != "Sintel.2010.720p.WEB-DL.x264-Blender" || sintel.Protocol != grab.ProtocolTorrent {
		t.Errorf("title/protocol: %+v", sintel)
	}
	if sintel.Size != 129241752 || sintel.Seeders != 50 || sintel.Peers != 5 {
		t.Errorf("size/seeders/peers = %d/%d/%d", sintel.Size, sintel.Seeders, sintel.Peers)
	}
	if sintel.IMDb != "tt1727587" || sintel.TMDb != 45745 {
		t.Errorf("ids = %q, %d", sintel.IMDb, sintel.TMDb)
	}
	if sintel.DownloadURL != "https://webtorrent.io/torrents/sintel.torrent" || sintel.MagnetURL != "" {
		t.Errorf("links = %q, %q", sintel.DownloadURL, sintel.MagnetURL)
	}
	if sintel.GUID != sintel.DownloadURL {
		t.Errorf("guid = %q", sintel.GUID)
	}
	if !slices.Contains(sintel.Categories, 2040) || !slices.Contains(sintel.Categories, 2000) {
		t.Errorf("categories = %v, want 2040 and its parent 2000", sintel.Categories)
	}
	if sintel.DownloadVolumeFactor == nil || *sintel.DownloadVolumeFactor != 0 ||
		sintel.UploadVolumeFactor == nil || *sintel.UploadVolumeFactor != 1 {
		t.Errorf("volume factors = %v, %v", sintel.DownloadVolumeFactor, sintel.UploadVolumeFactor)
	}
	if want := time.Date(2026, 11, 21, 12, 0, 0, 0, time.UTC); !sintel.PublishedAt.Equal(want) {
		t.Errorf("published = %s, want %s", sintel.PublishedAt, want)
	}
	for _, it := range items {
		if it.Size <= 0 || it.DownloadURL == "" || it.GUID == "" || it.PublishedAt.IsZero() {
			t.Errorf("incomplete item: %+v", it)
		}
	}
}

func TestEnclosureAndRawShape(t *testing.T) {
	api, _ := start(t, exampleItems(t), "")
	status, header, body := get(t, api, url.Values{"t": {"movie"}, "imdbid": {"tt1727587"}})
	if status != 200 || !strings.HasPrefix(header.Get("Content-Type"), "application/rss+xml") {
		t.Fatalf("status %d, content type %q", status, header.Get("Content-Type"))
	}
	s := string(body)
	for _, want := range []string{
		`<rss version="2.0" xmlns:torznab="http://torznab.com/schemas/2015/feed">`,
		`<enclosure url="https://webtorrent.io/torrents/sintel.torrent" length="129241752" type="application/x-bittorrent"></enclosure>`,
		`<size>129241752</size>`,
		`<pubDate>Sat, 21 Nov 2026 12:00:00 +0000</pubDate>`,
		`<torznab:attr name="size" value="129241752">`,
		`<torznab:attr name="seeders" value="50">`,
		`<torznab:attr name="peers" value="5">`,
		`<torznab:attr name="category" value="2040">`,
		`<torznab:attr name="imdb" value="1727587">`,
		`<torznab:attr name="tmdbid" value="45745">`,
		`<torznab:attr name="downloadvolumefactor" value="0">`,
		`<torznab:attr name="uploadvolumefactor" value="1">`,
		`<torznab:response offset="0" total="1">`,
	} {
		if !strings.Contains(s, want) {
			t.Errorf("answer lacks %s\n%s", want, s)
		}
	}
	// Optional attributes are left out when the item has nothing for them.
	for _, absent := range []string{`name="infohash"`, `name="magneturl"`} {
		if strings.Contains(s, absent) {
			t.Errorf("answer has %s for an item without one", absent)
		}
	}
	// Well-formed XML as Go itself reads it, not only as Marquee does.
	d := xml.NewDecoder(bytes.NewReader(body))
	for {
		if _, err := d.Token(); err != nil {
			if err != io.EOF {
				t.Errorf("not well-formed XML: %v", err)
			}
			break
		}
	}
}

func TestMagnetOnlyItem(t *testing.T) {
	const hash = "0123456789abcdef0123456789abcdef01234567"
	magnet := "magnet:?xt=urn:btih:" + strings.ToUpper(hash) + "&dn=Big.Buck.Bunny.2008.1080p.WEB-DL.x264-Blender&tr=udp%3A%2F%2Ftracker.example%2Fannounce"
	items := []item{{Title: "Big.Buck.Bunny.2008.1080p.WEB-DL.x264-Blender", Magnet: magnet, Size: 1000, Seeders: 3,
		Category: 2045, InfoHash: strings.ToUpper(hash)}}
	for i := range items {
		if err := items[i].check(); err != nil {
			t.Fatal(err)
		}
	}
	api, _ := start(t, items, "")
	got, err := feed(t, api, url.Values{"t": {"search"}})
	if err != nil || len(got) != 1 {
		t.Fatalf("items = %v, err = %v", got, err)
	}
	it := got[0]
	if it.MagnetURL != magnet || it.DownloadURL != "" || it.InfoHash != hash {
		t.Errorf("magnet=%q download=%q hash=%q", it.MagnetURL, it.DownloadURL, it.InfoHash)
	}
	if !slices.Contains(it.Categories, 2045) {
		t.Errorf("categories = %v", it.Categories)
	}
	// An item with no date is stamped with the time it is asked for.
	if want := time.Date(2026, 11, 22, 8, 30, 0, 0, time.UTC); !it.PublishedAt.Equal(want) {
		t.Errorf("published = %s, want %s", it.PublishedAt, want)
	}
}

// ---- filtering ---------------------------------------------------------------------------

func TestFilters(t *testing.T) {
	api, _ := start(t, exampleItems(t), "")
	const sintel = "Sintel.2010.720p.WEB-DL.x264-Blender"
	const bbb = "Big.Buck.Bunny.2008.1080p.WEB-DL.x264-Blender"
	cases := []struct {
		name   string
		params url.Values
		want   []string // nil: no items; the string "all" means every example item
	}{
		{"q substring", url.Values{"t": {"search"}, "q": {"sintel"}}, []string{sintel}},
		{"q is case-insensitive", url.Values{"t": {"search"}, "q": {"SiNtEl"}}, []string{sintel}},
		{"q with a space finds dots", url.Values{"t": {"search"}, "q": {"Sintel 2010"}}, []string{sintel}},
		{"q with dots", url.Values{"t": {"search"}, "q": {"big.buck"}}, []string{bbb}},
		{"q from the middle", url.Values{"t": {"search"}, "q": {"720p"}}, []string{sintel}},
		{"q matches several", url.Values{"t": {"search"}, "q": {"web-dl"}}, []string{"all"}},
		{"q with no match", url.Values{"t": {"search"}, "q": {"casablanca"}}, nil},
		{"empty q is everything", url.Values{"t": {"search"}, "q": {""}}, []string{"all"}},
		{"blank q is everything", url.Values{"t": {"search"}, "q": {"  "}}, []string{"all"}},
		{"imdbid with tt", url.Values{"t": {"movie"}, "imdbid": {"tt1727587"}}, []string{sintel}},
		{"imdbid digits, as Marquee sends it", url.Values{"t": {"movie"}, "imdbid": {"1727587"}}, []string{sintel}},
		{"imdbid zero-padded", url.Values{"t": {"movie"}, "imdbid": {"tt01727587"}}, []string{sintel}},
		{"imdbid that no item has", url.Values{"t": {"movie"}, "imdbid": {"tt0000001"}}, nil},
		{"tmdbid", url.Values{"t": {"movie"}, "tmdbid": {"45745"}}, []string{sintel}},
		{"tmdbid that no item has", url.Values{"t": {"movie"}, "tmdbid": {"1"}}, nil},
		{"either id is enough", url.Values{"t": {"movie"}, "imdbid": {"tt0000001"}, "tmdbid": {"45745"}}, []string{sintel}},
		{"ids and q must both fit", url.Values{"t": {"movie"}, "tmdbid": {"45745"}, "q": {"bunny"}}, nil},
		{"ids and q that agree", url.Values{"t": {"movie"}, "tmdbid": {"45745"}, "q": {"sintel"}}, []string{sintel}},
		{"a garbage imdbid matches nothing", url.Values{"t": {"movie"}, "imdbid": {"tt0000000x"}}, nil},
		{"an all-zero imdbid matches nothing, not items without ids", url.Values{"t": {"movie"}, "imdbid": {"tt0000000"}}, nil},
		{"movie mode with no query", url.Values{"t": {"movie"}}, []string{"all"}},
		{"tvsearch filters by q too", url.Values{"t": {"tvsearch"}, "q": {"sintel"}, "season": {"1"}, "ep": {"2"}}, []string{sintel}},
		{"music filters by q too", url.Values{"t": {"music"}, "q": {"sintel"}}, []string{sintel}},
		{"other params are accepted", url.Values{"t": {"search"}, "cat": {"2000,2040"}, "extended": {"1"}, "q": {"sintel"}}, []string{sintel}},
	}
	src := exampleItems(t)
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got, err := feed(t, api, c.params)
			if err != nil {
				t.Fatal(err)
			}
			want := c.want
			if len(want) == 1 && want[0] == "all" {
				want = nil
				for _, it := range src {
					want = append(want, it.Title)
				}
			}
			if !slices.Equal(titles(got), want) {
				t.Errorf("got %v, want %v", titles(got), want)
			}
		})
	}
}

func TestLimitAndOffset(t *testing.T) {
	api, _ := start(t, exampleItems(t), "")
	src := exampleItems(t)

	got, err := feed(t, api, url.Values{"t": {"search"}, "limit": {"2"}})
	if err != nil || len(got) != 2 || got[0].Title != src[0].Title {
		t.Fatalf("limit=2: %v %v", titles(got), err)
	}
	got, err = feed(t, api, url.Values{"t": {"search"}, "limit": {"2"}, "offset": {"2"}})
	if err != nil || len(got) != len(src)-2 || got[0].Title != src[2].Title {
		t.Fatalf("limit=2 offset=2: %v %v", titles(got), err)
	}
	got, err = feed(t, api, url.Values{"t": {"search"}, "offset": {"99"}})
	if err != nil || len(got) != 0 {
		t.Fatalf("offset past the end: %v %v", titles(got), err)
	}
	for _, bad := range []string{"0", "-3", "x", ""} {
		if got, err := feed(t, api, url.Values{"t": {"search"}, "limit": {bad}}); err != nil || len(got) != len(src) {
			t.Errorf("limit=%q: %d items, %v; want the default", bad, len(got), err)
		}
	}
	// The total is the number that matched, not the page.
	_, _, body := get(t, api, url.Values{"t": {"search"}, "limit": {"1"}})
	if !strings.Contains(string(body), `<torznab:response offset="0" total="4">`) {
		t.Errorf("total not reported:\n%s", body)
	}
}

func TestEmptyFeedIsStillAFeed(t *testing.T) {
	api, _ := start(t, nil, "")
	got, err := feed(t, api, url.Values{"t": {"search"}})
	if err != nil || len(got) != 0 {
		t.Errorf("items = %v, err = %v", got, err)
	}
}

// What Marquee itself would send, built from the caps fakefeed gives it.
func TestMarqueeSearchesBuiltFromTheCaps(t *testing.T) {
	api, _ := start(t, exampleItems(t), "")
	caps := fetchCaps(t, api)

	run := func(q sources.Query) []sources.Item {
		t.Helper()
		mode, params, _ := sources.BuildSearch(caps, q, []int{2000})
		items, err := feed(t, api, params)
		if err != nil {
			t.Fatalf("%s %v: %v", mode, params, err)
		}
		return items
	}

	byIDs := run(sources.Query{Media: sources.MediaMovie, IMDb: "tt1727587", TMDb: 45745, Limit: 50})
	if len(byIDs) != 1 || byIDs[0].TMDb != 45745 {
		t.Errorf("by ids: %v", titles(byIDs))
	}
	// The retry Marquee makes when an id search finds nothing: title and year as text.
	byText := run(sources.Query{Media: sources.MediaMovie, Text: "Sintel", Year: 2010, NoIDs: true})
	if len(byText) != 1 || byText[0].IMDb != "tt1727587" {
		t.Errorf("by text and year: %v", titles(byText))
	}
	if got := run(sources.Query{Media: sources.MediaMovie, Text: "Casablanca", Year: 1942, NoIDs: true}); len(got) != 0 {
		t.Errorf("a title that is not there: %v", titles(got))
	}
	if got := run(sources.Query{Media: sources.MediaShow, Text: "Sintel", Season: 1, Episode: 2, NoIDs: true}); len(got) != 1 {
		t.Errorf("tv search: %v", titles(got))
	}
	if got := run(sources.Query{Media: sources.MediaArtist, Text: "Sintel"}); len(got) != 1 {
		t.Errorf("music search: %v", titles(got))
	}
	// The latest-items feed Marquee polls.
	if got := run(sources.Query{Limit: 100}); len(got) != len(exampleItems(t)) {
		t.Errorf("rss: %v", titles(got))
	}
}

// ---- keys, errors and the log ------------------------------------------------------------

func TestAPIKey(t *testing.T) {
	const key = "s3cr3t-k3y-value"
	api, logs := start(t, exampleItems(t), key)

	for _, params := range []url.Values{
		{"t": {"caps"}},
		{"t": {"search"}},
		{"t": {"movie"}, "imdbid": {"1727587"}},
		{"t": {"search"}, "apikey": {"wrong"}},
		{"t": {"caps"}, "apikey": {key + "x"}},
		{"t": {"search"}, "apikey": {""}},
		{"t": {"search"}, "api_key": {key}}, // only apikey counts
	} {
		status, _, body := get(t, api, params)
		var xe errorDoc
		if status != 200 || xml.Unmarshal(body, &xe) != nil || xe.Code != 100 {
			t.Errorf("%v: status %d, body %s; want Torznab error 100", params, status, body)
		}
		// And Marquee reads it as a refused key.
		_, err := sources.ParseFeed(bytes.NewReader(body), sources.KindTorznab, 1)
		var se *sources.Error
		if !errors.As(err, &se) || se.Code != sources.CodeKeyRefused {
			t.Errorf("%v: Marquee read it as %v", params, err)
		}
	}

	for _, params := range []url.Values{
		{"t": {"caps"}, "apikey": {key}},
		{"t": {"search"}, "apikey": {key}, "q": {"sintel"}},
	} {
		status, _, body := get(t, api, params)
		if status != 200 || bytes.Contains(body, []byte("<error")) {
			t.Errorf("%v with the right key: status %d, body %s", params, status, body)
		}
	}
	got, err := feed(t, api, url.Values{"t": {"search"}, "apikey": {key}})
	if err != nil || len(got) != len(exampleItems(t)) {
		t.Errorf("with the key: %v %v", titles(got), err)
	}

	// The log says what was asked and never the key, whether it was right or wrong.
	log := logs.String()
	for _, secret := range []string{key, key + "x", "apikey=wrong"} {
		if strings.Contains(log, secret) {
			t.Errorf("the log has %q:\n%s", secret, log)
		}
	}
	if !strings.Contains(log, "apikey=REDACTED") || !strings.Contains(log, "q=sintel") {
		t.Errorf("the log should show the request with the key redacted:\n%s", log)
	}
	if n := strings.Count(log, "msg=request"); n != 7+2+1 {
		t.Errorf("%d request lines, want 10:\n%s", n, log)
	}
}

func TestOtherSecretsAreNotLogged(t *testing.T) {
	api, logs := start(t, exampleItems(t), "")
	get(t, api, url.Values{"t": {"search"}, "passkey": {"pk-1234"}, "token": {"tok-5678"}, "RSSKey": {"rk-9"}, "q": {"sintel"}})
	log := logs.String()
	for _, secret := range []string{"pk-1234", "tok-5678", "rk-9"} {
		if strings.Contains(log, secret) {
			t.Errorf("the log has %q:\n%s", secret, log)
		}
	}
	if !strings.Contains(log, "q=sintel") {
		t.Errorf("the log lost the search:\n%s", log)
	}
}

func TestErrorsAndOtherPaths(t *testing.T) {
	api, logs := start(t, exampleItems(t), "")

	for _, c := range []struct {
		params url.Values
		code   int
	}{
		{url.Values{}, 200},
		{url.Values{"t": {"nonsense"}}, 202},
		{url.Values{"t": {"<script>"}}, 202},
	} {
		status, _, body := get(t, api, c.params)
		var xe errorDoc
		if status != 200 || xml.Unmarshal(body, &xe) != nil || xe.Code != c.code {
			t.Errorf("%v: status %d, body %s; want error %d", c.params, status, body, c.code)
		}
		if bytes.Contains(body, []byte("<script>")) {
			t.Errorf("the answer echoes the request: %s", body)
		}
		if _, err := sources.ParseFeed(bytes.NewReader(body), sources.KindTorznab, 1); err == nil {
			t.Errorf("%v: Marquee accepted an error as a feed", c.params)
		}
	}

	root := strings.TrimSuffix(api, "/api")
	for path, want := range map[string]int{"/": 404, "/api/": 404, "/nope": 404, "/api/v2.0/indexers/all": 404} {
		resp, err := http.Get(root + path)
		if err != nil {
			t.Fatal(err)
		}
		resp.Body.Close()
		if resp.StatusCode != want {
			t.Errorf("GET %s = %d, want %d", path, resp.StatusCode, want)
		}
	}
	resp, err := http.Post(api+"?t=search", "text/plain", strings.NewReader("x"))
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusMethodNotAllowed {
		t.Errorf("POST = %d, want 405", resp.StatusCode)
	}
	if log := logs.String(); !strings.Contains(log, "result=404") || !strings.Contains(log, "result=405") {
		t.Errorf("rejected requests are not logged:\n%s", log)
	}
}

// ---- the items file ----------------------------------------------------------------------

func writeItems(t *testing.T, content string) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), "items.json")
	if err := os.WriteFile(p, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	return p
}

func TestLoadItemsAcceptsWhatItShould(t *testing.T) {
	items, err := loadItems(writeItems(t, `[
	  {"title":"A.2010","link":"http://x/a.torrent","tmdbid":123,"imdbid":"1727587","pubDate":"2026-11-21T12:00:00Z"},
	  {"title":" B.2011 ","magnet":"MAGNET:?xt=urn:btih:abc","infohash":"0123456789ABCDEF0123456789ABCDEF01234567","pubDate":"Sat, 21 Nov 2026 12:00:00 GMT"},
	  {"title":"C.2012","link":"http://x/c.torrent","tmdbid":null}
	]`))
	if err != nil {
		t.Fatal(err)
	}
	if len(items) != 3 {
		t.Fatalf("%d items", len(items))
	}
	if items[0].TMDbID != "123" || items[0].published.Hour() != 12 {
		t.Errorf("number tmdbid / RFC 3339 date: %+v", items[0])
	}
	if items[1].Title != "B.2011" || items[1].InfoHash != "0123456789abcdef0123456789abcdef01234567" {
		t.Errorf("trimmed title / lower-case hash: %+v", items[1])
	}
	if items[2].TMDbID != "" {
		t.Errorf("null tmdbid = %q", items[2].TMDbID)
	}
	if got, err := loadItems(writeItems(t, `[]`)); err != nil || len(got) != 0 {
		t.Errorf("an empty list: %v %v", got, err)
	}
}

func TestLoadItemsRejectsMistakes(t *testing.T) {
	cases := []struct{ name, content, want string }{
		{"no title", `[{"link":"http://x/a.torrent"}]`, "needs a title"},
		{"no link or magnet", `[{"title":"A"}]`, "needs a link or a magnet"},
		{"bad magnet", `[{"title":"A","magnet":"http://x"}]`, "magnet must start"},
		{"bad infohash", `[{"title":"A","link":"http://x","infohash":"abc"}]`, "infohash must be 40 hex"},
		{"bad imdbid", `[{"title":"A","link":"http://x","imdbid":"tt12ab"}]`, "imdbid must look like"},
		{"bad pubDate", `[{"title":"A","link":"http://x","pubDate":"yesterday"}]`, "pubDate must look like"},
		{"negative size", `[{"title":"A","link":"http://x","size":-1}]`, "cannot be negative"},
		{"unknown field", `[{"title":"A","link":"http://x","seeds":5}]`, "unknown field"},
		{"tmdbid is a bool", `[{"title":"A","link":"http://x","tmdbid":true}]`, "string or a number"},
		{"size is a string", `[{"title":"A","link":"http://x","size":"big"}]`, "size"},
		{"not a list", `{"title":"A"}`, "JSON list"},
		{"trailing data", `[] []`, "unexpected data"},
		{"not json", `hello`, "JSON list"},
		{"the second item is named", `[{"title":"A","link":"http://x"},{"title":"B"}]`, `item 2 ("B")`},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			_, err := loadItems(writeItems(t, c.content))
			if err == nil || !strings.Contains(err.Error(), c.want) {
				t.Errorf("error = %v, want one containing %q", err, c.want)
			}
		})
	}
	if _, err := loadItems(filepath.Join(t.TempDir(), "missing.json")); err == nil {
		t.Error("a missing file should be an error")
	}
}

// ---- running it ----------------------------------------------------------------------------

func TestRunFlagErrors(t *testing.T) {
	var out bytes.Buffer
	if err := run([]string{"-nope"}, &out); err == nil {
		t.Error("an unknown flag should be an error")
	}
	if err := run([]string{"stray"}, &out); err == nil {
		t.Error("a stray argument should be an error")
	}
	if err := run([]string{"-h"}, &out); err != nil {
		t.Errorf("-h: %v", err)
	}
	if err := run([]string{"-items", filepath.Join(t.TempDir(), "missing.json")}, &out); err == nil {
		t.Error("a missing items file should be an error")
	}
	if err := run([]string{"-items", writeItems(t, `[{"title":"A"}]`)}, &out); err == nil {
		t.Error("a bad items file should be an error")
	}
	if err := run([]string{"-addr", "not-an-address"}, &out); err == nil {
		t.Error("a bad address should be an error")
	}
}

func TestServeAnswersAndStops(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	s := newServer(exampleItems(t), "", slog.New(slog.NewTextHandler(io.Discard, nil)))
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- serve(ctx, ln, s.handler()) }()

	api := "http://" + ln.Addr().String() + "/api"
	if got, err := feed(t, api, url.Values{"t": {"search"}, "q": {"tears of steel"}}); err != nil || len(got) != 1 {
		t.Errorf("served: %v %v", titles(got), err)
	}
	cancel()
	select {
	case err := <-done:
		if err != nil {
			t.Errorf("serve returned %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("serve did not stop")
	}
}
