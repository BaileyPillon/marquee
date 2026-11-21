package sources

import (
	"bytes"
	"encoding/base32"
	"encoding/hex"
	"encoding/xml"
	"fmt"
	"io"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/BaileyPillon/marquee/internal/grab"
)

// maxFeedBytes caps how much of one answer is read.
const maxFeedBytes = 32 << 20

// Item is one release in a feed. The links are for the grab path only: they can
// carry the source's key, so they never reach JSON, logs or error text.
type Item struct {
	IndexerID   int64
	GUID        string
	Title       string
	Protocol    string // grab.ProtocolTorrent | grab.ProtocolUsenet
	Size        int64
	Seeders     int // -1: not stated
	Peers       int // -1: not stated
	Grabs       int // -1: not stated
	PublishedAt time.Time
	InfoHash    string // lower-case hex
	Categories  []int
	IMDb        string // "tt0017136"
	TMDb        int64
	TVMaze      int64
	TVDb        int64

	// DownloadVolumeFactor is 0 for a free-leech torrent, 1 for a normal one,
	// and nil when the feed did not say. UploadVolumeFactor likewise.
	DownloadVolumeFactor *float64
	UploadVolumeFactor   *float64

	// Attrs are the feed's own attributes, minus anything that is a link.
	Attrs map[string]string

	DownloadURL string `json:"-"`
	MagnetURL   string `json:"-"`
}

// Release converts the item for the grab contract. Quality, Score and Reasons
// are filled in by whoever scored it.
func (i Item) Release() grab.Release {
	return grab.Release{
		IndexerID: i.IndexerID, GUID: i.GUID, Title: i.Title,
		DownloadURL: i.DownloadURL, MagnetURL: i.MagnetURL, InfoHash: i.InfoHash,
		Protocol: i.Protocol, Size: i.Size, Seeders: max(i.Seeders, 0), PublishedAt: i.PublishedAt,
	}
}

type xmlFeed struct {
	XMLName xml.Name `xml:"rss"`
	Channel struct {
		Items []xmlItem `xml:"item"`
	} `xml:"channel"`
}

type xmlItem struct {
	Title     string   `xml:"title"`
	GUID      string   `xml:"guid"`
	Link      string   `xml:"link"`
	PubDate   string   `xml:"pubDate"`
	Size      string   `xml:"size"`
	Category  []string `xml:"category"`
	Enclosure struct {
		URL    string `xml:"url,attr"`
		Length string `xml:"length,attr"`
	} `xml:"enclosure"`
	Attrs []xmlAttr `xml:"attr"` // torznab:attr and newznab:attr share the local name
}

type xmlAttr struct {
	Name  string `xml:"name,attr"`
	Value string `xml:"value,attr"`
}

type xmlError struct {
	XMLName     xml.Name `xml:"error"`
	Code        string   `xml:"code,attr"`
	Description string   `xml:"description,attr"`
}

// rootName reads the name of the document's root element.
func rootName(b []byte) (string, error) {
	dec := xml.NewDecoder(bytes.NewReader(b))
	for {
		tok, err := dec.Token()
		if err != nil {
			return "", err
		}
		if se, ok := tok.(xml.StartElement); ok {
			return se.Name.Local, nil
		}
	}
}

// parseError turns a Torznab <error code="100" description="..."/> answer into
// an Error, or reports nil if the document is not one.
func parseError(b []byte) *Error {
	var x xmlError
	if err := xml.Unmarshal(b, &x); err != nil {
		return nil
	}
	code, _ := strconv.Atoi(strings.TrimSpace(x.Code))
	desc := strings.TrimSpace(x.Description)
	if len(desc) > 200 {
		desc = desc[:200]
	}
	switch {
	case code >= 100 && code <= 199:
		return failure(CodeKeyRefused, "The source refused the API key"+suffix(desc)+".")
	case code == 429 || strings.Contains(strings.ToLower(desc), "limit"):
		return failure(CodeSlowDown, "The source asked Marquee to slow down"+suffix(desc)+".")
	}
	return failure(CodeSourceError, "The source reported a problem"+suffix(desc)+".")
}

func suffix(desc string) string {
	if desc == "" {
		return ""
	}
	return " (" + desc + ")"
}

// ParseFeed reads a Torznab or Newznab RSS answer. kind decides the protocol.
// A document that is not a feed gives an Error with code not_a_feed.
func ParseFeed(r io.Reader, kind string, indexerID int64) ([]Item, error) {
	body, err := io.ReadAll(io.LimitReader(r, maxFeedBytes))
	if err != nil {
		return nil, failure(CodeUnreachable, "The answer from the source was cut short.")
	}
	return parseFeedBytes(body, kind, indexerID)
}

func parseFeedBytes(body []byte, kind string, indexerID int64) ([]Item, error) {
	root, err := rootName(body)
	if err != nil {
		return nil, failure(CodeNotAFeed, "The address answered, but not with a Torznab or Newznab feed.")
	}
	switch root {
	case "error":
		if e := parseError(body); e != nil {
			return nil, e
		}
		return nil, failure(CodeSourceError, "The source reported a problem.")
	case "rss":
	default:
		return nil, failure(CodeNotAFeed, "The address answered, but not with a Torznab or Newznab feed.")
	}
	var f xmlFeed
	if err := xml.Unmarshal(body, &f); err != nil {
		return nil, failure(CodeNotAFeed, "The feed from the source could not be read.")
	}
	proto := Protocol(kind)
	items := make([]Item, 0, len(f.Channel.Items))
	for _, x := range f.Channel.Items {
		if it, ok := x.item(proto, indexerID); ok {
			items = append(items, it)
		}
	}
	return items, nil
}

var (
	reDigits  = regexp.MustCompile(`\d+`)
	reBTIH    = regexp.MustCompile(`(?i)xt=urn:btih:([0-9a-z]{32,40})`)
	hex40     = regexp.MustCompile(`^[0-9a-f]{40}$`)
	pubLayout = []string{time.RFC1123Z, time.RFC1123, time.RFC822Z, time.RFC822,
		"Mon, 2 Jan 2006 15:04:05 -0700", "Mon, 2 Jan 2006 15:04:05 MST", time.RFC3339, "2006-01-02 15:04:05"}
)

func (x xmlItem) item(proto string, indexerID int64) (Item, bool) {
	title := strings.TrimSpace(x.Title)
	if title == "" {
		return Item{}, false
	}
	it := Item{
		IndexerID: indexerID, Title: title, Protocol: proto,
		Seeders: -1, Peers: -1, Grabs: -1, Attrs: map[string]string{},
	}

	var catAttrs []string
	for _, a := range x.Attrs {
		name := strings.ToLower(strings.TrimSpace(a.Name))
		val := strings.TrimSpace(a.Value)
		if name == "" {
			continue
		}
		if name == "category" {
			catAttrs = append(catAttrs, val)
		}
		// Links can carry the source's key or a tracker passkey: keep them out of
		// the attribute copy that is stored and shown.
		if strings.Contains(name, "url") || strings.Contains(name, "link") {
			if name == "magneturl" && val != "" {
				it.MagnetURL = val
			}
			continue
		}
		if prev, ok := it.Attrs[name]; ok && name == "category" {
			it.Attrs[name] = prev + "," + val
		} else {
			it.Attrs[name] = val
		}
	}
	attr := func(names ...string) string {
		for _, n := range names {
			if v, ok := it.Attrs[n]; ok && v != "" {
				return v
			}
		}
		return ""
	}
	num := func(names ...string) int64 {
		n, err := strconv.ParseInt(attr(names...), 10, 64)
		if err != nil {
			return -1
		}
		return n
	}

	// Links.
	link := strings.TrimSpace(x.Link)
	enc := strings.TrimSpace(x.Enclosure.URL)
	for _, l := range []string{enc, link} {
		switch {
		case strings.HasPrefix(strings.ToLower(l), "magnet:"):
			if it.MagnetURL == "" {
				it.MagnetURL = l
			}
		case l != "" && it.DownloadURL == "":
			it.DownloadURL = l
		}
	}

	// Identity. A guid is shown to people and stored in the clear, so it never
	// keeps a key that a source (or the link it falls back to) put in it.
	it.GUID = cleanGUID(x.GUID)
	if it.GUID == "" {
		switch {
		case it.DownloadURL != "":
			it.GUID = cleanGUID(it.DownloadURL)
		case it.MagnetURL != "":
			it.GUID = "magnet:" + hashFromMagnet(it.MagnetURL)
			if it.GUID == "magnet:" {
				it.GUID = title
			}
		default:
			it.GUID = title
		}
	}

	// Numbers.
	it.Size = num("size")
	if it.Size < 0 {
		it.Size, _ = strconv.ParseInt(strings.TrimSpace(x.Size), 10, 64)
	}
	if it.Size <= 0 {
		it.Size, _ = strconv.ParseInt(strings.TrimSpace(x.Enclosure.Length), 10, 64)
	}
	if it.Size < 0 {
		it.Size = 0
	}
	if n := num("seeders"); n >= 0 {
		it.Seeders = int(n)
	}
	if n := num("peers"); n >= 0 {
		it.Peers = int(n)
	}
	if n := num("grabs"); n >= 0 {
		it.Grabs = int(n)
	}
	if v, err := strconv.ParseFloat(attr("downloadvolumefactor"), 64); err == nil {
		it.DownloadVolumeFactor = &v
	}
	if v, err := strconv.ParseFloat(attr("uploadvolumefactor"), 64); err == nil {
		it.UploadVolumeFactor = &v
	}

	it.readIDs()

	// Categories: the attributes say numbers; a bare <category> may too.
	for _, c := range append(catAttrs, x.Category...) {
		if n, err := strconv.Atoi(strings.TrimSpace(c)); err == nil && n > 0 {
			dup := false
			for _, have := range it.Categories {
				dup = dup || have == n
			}
			if !dup {
				it.Categories = append(it.Categories, n)
			}
		}
	}

	// Torrent identity.
	if it.Protocol == grab.ProtocolTorrent {
		it.InfoHash = normalizeHash(attr("infohash"))
		if it.InfoHash == "" {
			it.InfoHash = hashFromMagnet(it.MagnetURL)
		}
	}

	// Date.
	if p := strings.TrimSpace(x.PubDate); p != "" {
		for _, layout := range pubLayout {
			if t, err := time.Parse(layout, p); err == nil {
				it.PublishedAt = t.UTC()
				break
			}
		}
	}
	if it.DownloadURL == "" && it.MagnetURL == "" {
		return Item{}, false // nothing to fetch
	}
	return it, true
}

// secretParams are query parameters that carry a key or a passkey.
var secretParams = []string{"apikey", "api_key", "key", "passkey", "pass_key", "rsskey", "rss_key", "authkey",
	"torrent_pass", "token"}

// cleanGUID trims a feed's guid and, when it is an address, drops the query
// parameters that hold a key. The rest of the address still tells two releases
// apart.
func cleanGUID(raw string) string {
	raw = strings.TrimSpace(raw)
	if !strings.Contains(raw, "://") || !strings.Contains(raw, "?") {
		return raw
	}
	u, err := url.Parse(raw)
	if err != nil {
		return raw[:strings.IndexByte(raw, '?')] // cannot read it: keep only what is before the query
	}
	q := u.Query()
	for _, name := range secretParams {
		q.Del(name)
	}
	u.RawQuery = q.Encode()
	u.User = nil
	return u.String()
}

// readIDs fills the provider ids from the item's attributes.
func (it *Item) readIDs() {
	attr := func(names ...string) string {
		for _, n := range names {
			if v := strings.TrimSpace(it.Attrs[n]); v != "" {
				return v
			}
		}
		return ""
	}
	if d := reDigits.FindString(attr("imdb", "imdbid")); d != "" {
		if n, err := strconv.Atoi(d); err == nil && n > 0 {
			it.IMDb = fmt.Sprintf("tt%07d", n)
		}
	}
	id := func(names ...string) int64 {
		n, err := strconv.ParseInt(attr(names...), 10, 64)
		if err != nil || n < 0 {
			return 0
		}
		return n
	}
	it.TMDb = id("tmdbid", "tmdb")
	it.TVMaze = id("tvmazeid", "tvmaze")
	it.TVDb = id("tvdbid", "tvdb")
}

// normalizeHash returns a 40-character lower-case hex info hash, or "".
func normalizeHash(h string) string {
	h = strings.ToLower(strings.TrimSpace(h))
	if hex40.MatchString(h) {
		return h
	}
	return ""
}

// hashFromMagnet reads the btih out of a magnet link, converting the base32
// spelling to hex.
func hashFromMagnet(magnet string) string {
	m := reBTIH.FindStringSubmatch(magnet)
	if m == nil {
		return ""
	}
	h := m[1]
	if len(h) == 40 {
		return normalizeHash(h)
	}
	raw, err := base32.StdEncoding.DecodeString(strings.ToUpper(h))
	if err != nil || len(raw) != 20 {
		return ""
	}
	return hex.EncodeToString(raw)
}
