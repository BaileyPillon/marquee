// Package sourcestest is a fake Torznab/Newznab indexer for tests: an httptest
// server that answers t=caps and the search modes with XML written here. It
// only uses public-domain and Creative Commons titles in its examples and no
// real tracker or site names. Nothing outside tests imports it.
package sourcestest

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"testing"
	"time"
)

// Item is one release in a fake feed.
type Item struct {
	Title    string
	GUID     string // defaults to the title
	Size     int64
	Seeders  int // negative: leave the attribute out
	Peers    int // negative: leave out
	Age      time.Duration
	IMDb     string // "tt0017136"
	TMDb     int
	TVMaze   int
	InfoHash string // 40 hex characters
	Magnet   bool   // add a magneturl attribute
	// MagnetOnly leaves the download link out: only a magnet link is given.
	MagnetOnly bool
	Usenet     bool // a Newznab item: no seeders, an NZB link
	Cats       []int
	DownFactor float64 // negative: leave out
	UpFactor   float64 // negative: leave out
}

// Torrent is a plain healthy torrent item.
func Torrent(title string, size int64, seeders int) Item {
	return Item{Title: title, Size: size, Seeders: seeders, Peers: -1, DownFactor: -1, UpFactor: -1}
}

// Opts shapes the capabilities a server reports.
type Opts struct {
	NoMovie    bool
	NoTV       bool
	MovieIDs   string // supportedParams of movie-search; default "q,imdbid,tmdbid"
	TVParams   string // supportedParams of tv-search; default "q,season,ep,imdbid,tvmazeid"
	SearchOnly bool   // only the plain search mode, with q
	Max        int    // limits max; default 100
}

// CapsXML renders a t=caps answer.
func CapsXML(o Opts) string {
	if o.MovieIDs == "" {
		o.MovieIDs = "q,imdbid,tmdbid"
	}
	if o.TVParams == "" {
		o.TVParams = "q,season,ep,imdbid,tvmazeid"
	}
	if o.Max == 0 {
		o.Max = 100
	}
	yes := func(avail bool) string {
		if avail {
			return "yes"
		}
		return "no"
	}
	movie := !o.NoMovie && !o.SearchOnly
	tv := !o.NoTV && !o.SearchOnly
	return fmt.Sprintf(`<?xml version="1.0" encoding="UTF-8"?>
<caps>
  <server title="Fake Indexer"/>
  <limits max="%d" default="50"/>
  <searching>
    <search available="yes" supportedParams="q"/>
    <tv-search available="%s" supportedParams="%s"/>
    <movie-search available="%s" supportedParams="%s"/>
    <music-search available="yes" supportedParams="q,artist,album"/>
  </searching>
  <categories>
    <category id="2000" name="Movies"><subcat id="2040" name="Movies/HD"/><subcat id="2045" name="Movies/UHD"/></category>
    <category id="5000" name="TV"><subcat id="5040" name="TV/HD"/></category>
    <category id="3000" name="Audio"/>
  </categories>
</caps>`, o.Max, yes(tv), o.TVParams, yes(movie), o.MovieIDs)
}

func esc(s string) string {
	r := strings.NewReplacer("&", "&amp;", "<", "&lt;", ">", "&gt;", `"`, "&quot;")
	return r.Replace(s)
}

// Feed renders items as a Torznab/Newznab RSS answer. base is the server's URL,
// used to build download links; key goes into them the way real feeds do.
func Feed(base, key string, items ...Item) string {
	var b strings.Builder
	b.WriteString(`<?xml version="1.0" encoding="UTF-8"?>
<rss version="2.0" xmlns:atom="http://www.w3.org/2005/Atom" xmlns:torznab="http://torznab.com/schemas/2015/feed" xmlns:newznab="http://www.newznab.com/DTD/2010/feeds/attributes/">
<channel><title>Fake Indexer</title>
`)
	now := time.Now().UTC()
	for _, it := range items {
		guid := it.GUID
		if guid == "" {
			guid = it.Title
		}
		pub := now.Add(-it.Age).Format(time.RFC1123Z)
		ns := "torznab"
		if it.Usenet {
			ns = "newznab"
		}
		link := fmt.Sprintf("%s/download/%s?apikey=%s", base, url.PathEscape(guid), key)
		magnet := ""
		if it.Magnet || it.MagnetOnly {
			hash := it.InfoHash
			if hash == "" {
				hash = "0123456789abcdef0123456789abcdef01234567"
			}
			magnet = "magnet:?xt=urn:btih:" + hash + "&dn=" + url.QueryEscape(it.Title) + "&tr=udp%3A%2F%2Ftracker.example.invalid%3A1337%2Fannounce%3Fpasskey%3Dsecretpass"
		}
		fmt.Fprintf(&b, "<item>\n<title>%s</title>\n<guid>%s</guid>\n<pubDate>%s</pubDate>\n", esc(it.Title), esc(guid), pub)
		if !it.MagnetOnly {
			fmt.Fprintf(&b, "<link>%s</link>\n<enclosure url=\"%s\" length=\"%d\" type=\"application/x-bittorrent\"/>\n",
				esc(link), esc(link), it.Size)
		}
		fmt.Fprintf(&b, "<size>%d</size>\n", it.Size)
		attr := func(name, value string) {
			fmt.Fprintf(&b, "<%s:attr name=\"%s\" value=\"%s\"/>\n", ns, name, esc(value))
		}
		attr("size", fmt.Sprint(it.Size))
		for _, c := range it.Cats {
			attr("category", fmt.Sprint(c))
		}
		if !it.Usenet && it.Seeders >= 0 {
			attr("seeders", fmt.Sprint(it.Seeders))
		}
		if !it.Usenet && it.Peers >= 0 {
			attr("peers", fmt.Sprint(it.Peers))
		}
		if it.InfoHash != "" && !it.Usenet {
			attr("infohash", it.InfoHash)
		}
		if magnet != "" {
			attr("magneturl", magnet)
		}
		if it.IMDb != "" {
			attr("imdb", strings.TrimPrefix(it.IMDb, "tt"))
		}
		if it.TMDb > 0 {
			attr("tmdbid", fmt.Sprint(it.TMDb))
		}
		if it.TVMaze > 0 {
			attr("tvmazeid", fmt.Sprint(it.TVMaze))
		}
		if it.DownFactor >= 0 {
			attr("downloadvolumefactor", fmt.Sprint(it.DownFactor))
		}
		if it.UpFactor >= 0 {
			attr("uploadvolumefactor", fmt.Sprint(it.UpFactor))
		}
		b.WriteString("</item>\n")
	}
	b.WriteString("</channel>\n</rss>\n")
	return b.String()
}

// ErrorXML is the answer a source gives for a bad key.
func ErrorXML(code int, desc string) string {
	return fmt.Sprintf(`<?xml version="1.0" encoding="UTF-8"?><error code="%d" description="%s"/>`, code, esc(desc))
}

// Request is one call a server received.
type Request struct {
	Path  string
	Query url.Values
}

// Server is a fake indexer.
type Server struct {
	*httptest.Server
	Key  string
	Caps Opts

	// Search answers every t= other than caps. Nil answers an empty feed.
	Search func(q url.Values) []Item
	// Fail, when set, answers with that status instead of a feed.
	Fail int
	// PublicCaps lets t=caps through without the key, as some sources do.
	PublicCaps bool
	// Delay holds every search answer back, to see how many arrive at once.
	Delay time.Duration

	mu        sync.Mutex
	requests  []Request
	cur, peak int
}

// NewServer starts a fake indexer that wants the given key.
func NewServer(t testing.TB, key string) *Server {
	t.Helper()
	s := &Server{Key: key}
	s.Server = httptest.NewServer(http.HandlerFunc(s.serve))
	t.Cleanup(s.Close)
	return s
}

// URL of the feed endpoint (what the owner would paste).
func (s *Server) Feed() string { return s.Server.URL + "/api" }

func (s *Server) serve(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	s.mu.Lock()
	s.requests = append(s.requests, Request{Path: r.URL.Path, Query: q})
	s.mu.Unlock()
	if strings.HasPrefix(r.URL.Path, "/download/") {
		w.Header().Set("Content-Type", "application/x-bittorrent")
		_, _ = w.Write([]byte("d4:infod4:name4:fakee"))
		return
	}
	w.Header().Set("Content-Type", "application/xml")
	if s.Fail != 0 {
		w.WriteHeader(s.Fail)
		return
	}
	if s.Key != "" && q.Get("apikey") != s.Key && !(s.PublicCaps && q.Get("t") == "caps") {
		_, _ = w.Write([]byte(ErrorXML(100, "Incorrect user credentials")))
		return
	}
	if q.Get("t") == "caps" {
		_, _ = w.Write([]byte(CapsXML(s.Caps)))
		return
	}
	s.mu.Lock()
	s.cur++
	s.peak = max(s.peak, s.cur)
	s.mu.Unlock()
	defer func() {
		s.mu.Lock()
		s.cur--
		s.mu.Unlock()
	}()
	if s.Delay > 0 {
		time.Sleep(s.Delay)
	}
	var items []Item
	if s.Search != nil {
		items = s.Search(q)
	}
	_, _ = w.Write([]byte(Feed(s.Server.URL, s.Key, items...)))
}

// Requests returns the calls received so far, except t=caps ones unless
// withCaps is set.
func (s *Server) Requests(withCaps bool) []Request {
	s.mu.Lock()
	defer s.mu.Unlock()
	var out []Request
	for _, r := range s.requests {
		if !withCaps && (r.Query.Get("t") == "caps" || strings.HasPrefix(r.Path, "/download/")) {
			continue
		}
		out = append(out, r)
	}
	return out
}

// Peak is the most search calls that were being answered at the same moment.
func (s *Server) Peak() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.peak
}

// Searches counts the search calls (not caps, not downloads).
func (s *Server) Searches() int { return len(s.Requests(false)) }
