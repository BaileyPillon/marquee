// Command fakefeed is a tiny Torznab server for local end-to-end tests and for
// people working on Marquee. It serves the releases listed in a JSON file at
// /api, in the same XML a real Torznab indexer (Jackett, Prowlarr) answers
// with, and nothing else. It never talks to the network except to answer
// requests, and it is not part of Marquee: standard library only, not linked
// into the program.
//
// Usage:
//
//	go run ./tools/fakefeed -items tools/fakefeed/example-items.json
//
// See README.md in this folder for the flags, the item fields and examples.
package main

import (
	"bytes"
	"context"
	"crypto/subtle"
	"encoding/json"
	"encoding/xml"
	"errors"
	"flag"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/url"
	"os"
	"os/signal"
	"regexp"
	"strconv"
	"strings"
	"syscall"
	"time"
	"unicode"
)

const (
	defaultAddr  = "127.0.0.1:9117"
	torznabNS    = "http://torznab.com/schemas/2015/feed"
	defaultLimit = 100 // items per answer when the request has no limit
	maxLimit     = 1000
)

func main() {
	if err := run(os.Args[1:], os.Stderr); err != nil {
		fmt.Fprintln(os.Stderr, "fakefeed:", err)
		os.Exit(1)
	}
}

func run(args []string, stderr io.Writer) error {
	flags := flag.NewFlagSet("fakefeed", flag.ContinueOnError)
	flags.SetOutput(stderr)
	addr := flags.String("addr", defaultAddr, "listen address")
	itemsPath := flags.String("items", "", "JSON file with the releases to serve, a list (empty: an empty feed)")
	apiKey := flags.String("apikey", "", "if set, /api answers Torznab error 100 unless the request has ?apikey=<this>")
	if err := flags.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return nil
		}
		return err
	}
	if flags.NArg() > 0 {
		return fmt.Errorf("unexpected argument %q", flags.Arg(0))
	}

	var items []item
	if *itemsPath != "" {
		var err error
		if items, err = loadItems(*itemsPath); err != nil {
			return err
		}
	}
	log := slog.New(slog.NewTextHandler(stderr, nil))
	if len(items) == 0 {
		log.Warn("serving an empty feed; give -items <file.json> to serve releases")
	}

	ln, err := net.Listen("tcp", *addr)
	if err != nil {
		return err
	}
	// The key itself is never printed, only whether one is needed.
	log.Info("fakefeed listening", "url", "http://"+ln.Addr().String()+"/api", "items", len(items), "apikey_required", *apiKey != "")

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	return serve(ctx, ln, newServer(items, *apiKey, log).handler())
}

// serve runs h on ln until ctx ends, then shuts down.
func serve(ctx context.Context, ln net.Listener, h http.Handler) error {
	srv := &http.Server{Handler: h, ReadHeaderTimeout: 10 * time.Second}
	errc := make(chan error, 1)
	go func() { errc <- srv.Serve(ln) }()
	select {
	case err := <-errc:
		if errors.Is(err, http.ErrServerClosed) {
			return nil
		}
		return err
	case <-ctx.Done():
	}
	shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := srv.Shutdown(shutdownCtx); err != nil {
		_ = srv.Close()
		return err
	}
	return nil
}

// ---- the releases ---------------------------------------------------------------

// item is one release in the JSON file. Only title and one of link or magnet are
// required.
type item struct {
	Title    string `json:"title"`
	Link     string `json:"link"`   // address of the .torrent file
	Magnet   string `json:"magnet"` // magnet: link; used as the link when link is empty
	Size     int64  `json:"size"`
	Seeders  int    `json:"seeders"`
	Peers    int    `json:"peers"`
	IMDbID   text   `json:"imdbid"` // "tt1727587" (or just the digits)
	TMDbID   text   `json:"tmdbid"` // "45745" (a string or a number)
	Category int    `json:"category"`
	InfoHash string `json:"infohash"` // 40 hex characters, or empty
	PubDate  string `json:"pubDate"`  // e.g. "Sat, 21 Nov 2026 12:00:00 +0000"; empty: when it is asked for

	published time.Time // PubDate, parsed by loadItems
}

// text reads a JSON string or a JSON number into a string, so "tmdbid":"45745" and
// "tmdbid":45745 both work.
type text string

func (t *text) UnmarshalJSON(b []byte) error {
	s := strings.TrimSpace(string(b))
	switch {
	case s == "null":
		*t = ""
	case strings.HasPrefix(s, `"`):
		var v string
		if err := json.Unmarshal(b, &v); err != nil {
			return err
		}
		*t = text(strings.TrimSpace(v))
	default:
		var n json.Number
		if err := json.Unmarshal(b, &n); err != nil {
			return errors.New("must be a string or a number")
		}
		*t = text(n.String())
	}
	return nil
}

var (
	reHex40 = regexp.MustCompile(`^[0-9a-fA-F]{40}$`)
	reDigit = regexp.MustCompile(`^[0-9]+$`)

	// The date layouts Marquee reads.
	pubLayouts = []string{time.RFC1123Z, time.RFC1123, time.RFC822Z, time.RFC822,
		"Mon, 2 Jan 2006 15:04:05 -0700", time.RFC3339}
)

// loadItems reads and checks the items file. A mistake names the item, so a typo
// in a test file is found here and not as a silently missing release.
func loadItems(path string) ([]item, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.DisallowUnknownFields()
	var items []item
	if err := dec.Decode(&items); err != nil {
		return nil, fmt.Errorf("%s: %w (it must be a JSON list of objects)", path, err)
	}
	if _, err := dec.Token(); err != io.EOF {
		return nil, fmt.Errorf("%s: unexpected data after the list", path)
	}
	for i := range items {
		if err := items[i].check(); err != nil {
			return nil, fmt.Errorf("%s: item %d (%q): %w", path, i+1, items[i].Title, err)
		}
	}
	return items, nil
}

func (it *item) check() error {
	it.Title = strings.TrimSpace(it.Title)
	it.Link = strings.TrimSpace(it.Link)
	it.Magnet = strings.TrimSpace(it.Magnet)
	it.InfoHash = strings.ToLower(strings.TrimSpace(it.InfoHash))
	switch {
	case it.Title == "":
		return errors.New("needs a title")
	case it.Link == "" && it.Magnet == "":
		return errors.New("needs a link or a magnet (Marquee ignores a release with nothing to fetch)")
	case it.Magnet != "" && !strings.HasPrefix(strings.ToLower(it.Magnet), "magnet:"):
		return errors.New(`magnet must start with "magnet:"`)
	case it.Size < 0 || it.Seeders < 0 || it.Peers < 0 || it.Category < 0:
		return errors.New("size, seeders, peers and category cannot be negative")
	case it.InfoHash != "" && !reHex40.MatchString(it.InfoHash):
		return errors.New("infohash must be 40 hex characters or empty")
	}
	if d := imdbKey(string(it.IMDbID)); d != "" && !reDigit.MatchString(d) {
		return errors.New(`imdbid must look like "tt1727587"`)
	}
	if p := strings.TrimSpace(it.PubDate); p != "" {
		for _, layout := range pubLayouts {
			if t, err := time.Parse(layout, p); err == nil {
				it.published = t.UTC()
				break
			}
		}
		if it.published.IsZero() {
			return errors.New(`pubDate must look like "Sat, 21 Nov 2026 12:00:00 +0000"`)
		}
	}
	return nil
}

// ---- matching -------------------------------------------------------------------

// words lower-cases text and turns every run of anything that is not a letter or
// digit (dots, dashes, underscores, spaces) into one space, so a search for
// "Sintel 2010" finds "Sintel.2010.720p.WEB-DL".
func words(s string) string {
	var b strings.Builder
	space := true
	for _, r := range strings.ToLower(s) {
		if unicode.IsLetter(r) || unicode.IsDigit(r) {
			b.WriteRune(r)
			space = false
		} else if !space {
			b.WriteByte(' ')
			space = true
		}
	}
	return strings.TrimSpace(b.String())
}

// imdbKey is an IMDb id as it is compared: lower case, no "tt", no leading zeros.
func imdbKey(s string) string {
	return strings.TrimLeft(strings.TrimPrefix(strings.ToLower(strings.TrimSpace(s)), "tt"), "0")
}

// imdbDigits is the id as Torznab writes it in the imdb attribute: digits only, at
// least seven of them. It is "" when the item has none.
func imdbDigits(s string) string {
	k := imdbKey(s)
	if k == "" {
		return ""
	}
	if n := 7 - len(k); n > 0 {
		k = strings.Repeat("0", n) + k
	}
	return k
}

// filter returns the items that fit the request. imdbid and tmdbid, when given,
// match exactly and either one is enough; q, when given, must also be a
// case-insensitive substring of the title (see words). With none of them, every
// item is returned: that is the RSS mode.
func (s *server) filter(q url.Values) []item {
	needle := words(q.Get("q"))
	imdbGiven := strings.TrimSpace(q.Get("imdbid")) != ""
	tmdbGiven := strings.TrimSpace(q.Get("tmdbid")) != ""
	imdb := imdbKey(q.Get("imdbid"))
	tmdb := strings.TrimSpace(q.Get("tmdbid"))
	out := make([]item, 0, len(s.items))
	for _, it := range s.items {
		if imdbGiven || tmdbGiven {
			// An id the request names but the item lacks never matches (so an all-zero
			// imdbid, which has no key, finds nothing rather than every item).
			byIMDb := imdbGiven && imdb != "" && imdbKey(string(it.IMDbID)) == imdb
			byTMDb := tmdbGiven && string(it.TMDbID) == tmdb
			if !byIMDb && !byTMDb {
				continue
			}
		}
		if needle != "" && !strings.Contains(words(it.Title), needle) {
			continue
		}
		out = append(out, it)
	}
	return out
}

// ---- the server -------------------------------------------------------------------

type server struct {
	items  []item
	apiKey string
	log    *slog.Logger
	now    func() time.Time
}

func newServer(items []item, apiKey string, log *slog.Logger) *server {
	return &server{items: items, apiKey: apiKey, log: log, now: time.Now}
}

func (s *server) handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("/api", s.serveAPI)
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		http.NotFound(w, r)
		s.logRequest(r, "404")
	})
	return mux
}

func (s *server) serveAPI(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet && r.Method != http.MethodHead {
		w.Header().Set("Allow", "GET, HEAD")
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		s.logRequest(r, "405")
		return
	}
	s.logRequest(r, s.respond(w, r.URL.Query()))
}

// respond answers one /api request and says in a few words what it did, for the log.
func (s *server) respond(w http.ResponseWriter, q url.Values) string {
	if s.apiKey != "" && subtle.ConstantTimeCompare([]byte(q.Get("apikey")), []byte(s.apiKey)) != 1 {
		return writeError(w, 100, "Incorrect user credentials")
	}
	switch t := q.Get("t"); t {
	case "caps":
		writeXML(w, "application/xml", []byte(xml.Header+capsXML))
		return "caps"
	case "search", "movie", "tvsearch", "music":
		return s.search(w, q)
	case "":
		return writeError(w, 200, "Missing parameter (t)")
	default:
		return writeError(w, 202, "No such function")
	}
}

// logRequest writes one line per request. Query values that carry a key are
// replaced, so the log can be pasted into a bug report.
func (s *server) logRequest(r *http.Request, result string) {
	s.log.Info("request", "remote", r.RemoteAddr, "method", r.Method, "path", r.URL.Path,
		"query", redactQuery(r.URL.Query()), "result", result)
}

// secretParams are the query parameters that can hold a key or a passkey.
var secretParams = map[string]bool{"apikey": true, "api_key": true, "key": true, "passkey": true,
	"pass_key": true, "rsskey": true, "rss_key": true, "authkey": true, "torrent_pass": true, "token": true}

func redactQuery(q url.Values) string {
	safe := make(url.Values, len(q))
	for name, vals := range q {
		if secretParams[strings.ToLower(name)] {
			safe[name] = []string{"REDACTED"}
		} else {
			safe[name] = vals
		}
	}
	return safe.Encode()
}

// ---- answers ----------------------------------------------------------------------

// capsXML is the t=caps answer: the four search modes with the parameters the
// filter understands (season and ep are listed so Marquee sends them natively;
// they are not used to filter), and the categories the test files use.
const capsXML = `<caps>
  <server version="1.0" title="fakefeed" strapline="Torznab test server"/>
  <limits max="1000" default="100"/>
  <searching>
    <search available="yes" supportedParams="q"/>
    <tv-search available="yes" supportedParams="q,season,ep,imdbid,tmdbid"/>
    <movie-search available="yes" supportedParams="q,imdbid,tmdbid"/>
    <music-search available="yes" supportedParams="q"/>
  </searching>
  <categories>
    <category id="2000" name="Movies">
      <subcat id="2040" name="Movies/HD"/>
      <subcat id="2045" name="Movies/UHD"/>
    </category>
    <category id="3000" name="Audio"/>
    <category id="5000" name="TV"/>
  </categories>
</caps>
`

type errorDoc struct {
	XMLName     xml.Name `xml:"error"`
	Code        int      `xml:"code,attr"`
	Description string   `xml:"description,attr"`
}

// Torznab errors are an <error> document in a normal answer, as Jackett does.
func writeError(w http.ResponseWriter, code int, description string) string {
	body, _ := xml.Marshal(errorDoc{Code: code, Description: description})
	writeXML(w, "application/xml", append([]byte(xml.Header), body...))
	return fmt.Sprintf("error %d", code)
}

func writeXML(w http.ResponseWriter, contentType string, body []byte) {
	w.Header().Set("Content-Type", contentType+"; charset=utf-8")
	_, _ = w.Write(body)
}

// The RSS document. Go writes a tag name containing a colon as it is, which is how
// the torznab: prefix is produced; the namespace is declared on <rss>.
type rssDoc struct {
	XMLName xml.Name   `xml:"rss"`
	Version string     `xml:"version,attr"`
	NS      string     `xml:"xmlns:torznab,attr"`
	Channel rssChannel `xml:"channel"`
}

type rssChannel struct {
	Title       string          `xml:"title"`
	Description string          `xml:"description"`
	Response    torznabResponse `xml:"torznab:response"`
	Items       []rssItem       `xml:"item"`
}

type torznabResponse struct {
	Offset int `xml:"offset,attr"`
	Total  int `xml:"total,attr"`
}

type rssItem struct {
	Title     string        `xml:"title"`
	GUID      string        `xml:"guid"`
	Link      string        `xml:"link"`
	PubDate   string        `xml:"pubDate"`
	Size      int64         `xml:"size"`
	Enclosure enclosure     `xml:"enclosure"`
	Attrs     []torznabAttr `xml:"torznab:attr"`
}

type enclosure struct {
	URL    string `xml:"url,attr"`
	Length int64  `xml:"length,attr"`
	Type   string `xml:"type,attr"`
}

type torznabAttr struct {
	Name  string `xml:"name,attr"`
	Value string `xml:"value,attr"`
}

// search answers t=search, movie, tvsearch and music alike: the same filter, the
// same item shape.
func (s *server) search(w http.ResponseWriter, q url.Values) string {
	matched := s.filter(q)
	total := len(matched)

	offset := 0
	if n, err := strconv.Atoi(q.Get("offset")); err == nil && n > 0 {
		offset = min(n, total)
	}
	limit := defaultLimit
	if n, err := strconv.Atoi(q.Get("limit")); err == nil && n > 0 {
		limit = min(n, maxLimit)
	}
	page := matched[offset:min(offset+limit, total)]

	doc := rssDoc{Version: "2.0", NS: torznabNS, Channel: rssChannel{
		Title:       "fakefeed",
		Description: "Torznab test server",
		Response:    torznabResponse{Offset: offset, Total: total},
		Items:       make([]rssItem, 0, len(page)),
	}}
	for _, it := range page {
		doc.Channel.Items = append(doc.Channel.Items, s.rssItem(it))
	}
	body, err := xml.MarshalIndent(doc, "", "  ")
	if err != nil {
		return writeError(w, 900, "Could not write the feed")
	}
	writeXML(w, "application/rss+xml", append([]byte(xml.Header), body...))
	return fmt.Sprintf("%d items", len(page))
}

func (s *server) rssItem(it item) rssItem {
	link := it.Link
	if link == "" {
		link = it.Magnet
	}
	published := it.published
	if published.IsZero() {
		published = s.now()
	}
	size := strconv.FormatInt(it.Size, 10)

	attrs := []torznabAttr{
		{"size", size},
		{"seeders", strconv.Itoa(it.Seeders)},
		{"peers", strconv.Itoa(it.Peers)},
	}
	if it.InfoHash != "" {
		attrs = append(attrs, torznabAttr{"infohash", it.InfoHash})
	}
	if it.Magnet != "" {
		attrs = append(attrs, torznabAttr{"magneturl", it.Magnet})
	}
	if it.Category > 0 {
		// Like a real indexer: the parent category too, so a search for 2000 finds a 2040.
		if parent := it.Category / 1000 * 1000; parent != it.Category {
			attrs = append(attrs, torznabAttr{"category", strconv.Itoa(parent)})
		}
		attrs = append(attrs, torznabAttr{"category", strconv.Itoa(it.Category)})
	}
	if d := imdbDigits(string(it.IMDbID)); d != "" {
		attrs = append(attrs, torznabAttr{"imdb", d})
	}
	if it.TMDbID != "" {
		attrs = append(attrs, torznabAttr{"tmdbid", string(it.TMDbID)})
	}
	attrs = append(attrs,
		torznabAttr{"downloadvolumefactor", "0"},
		torznabAttr{"uploadvolumefactor", "1"},
	)

	return rssItem{
		Title:     it.Title,
		GUID:      link,
		Link:      link,
		PubDate:   published.UTC().Format(time.RFC1123Z),
		Size:      it.Size,
		Enclosure: enclosure{URL: link, Length: it.Size, Type: "application/x-bittorrent"},
		Attrs:     attrs,
	}
}
