package sources

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/BaileyPillon/marquee/internal/sources/sourcestest"
)

func asInvalid(err error) bool {
	var ie *InvalidError
	return errors.As(err, &ie)
}

// ---- the owner's list ------------------------------------------------------------

func TestCreateMasksAndSealsTheKey(t *testing.T) {
	e := newEnv(t)
	ix := e.add("Alpha", KindTorznab, "http://127.0.0.1:1/api", testKey, 2000)
	if ix.APIKey != "••••b1a0" {
		t.Errorf("masked key = %q, want ••••b1a0", ix.APIKey)
	}
	b, _ := json.Marshal(ix)
	noKey(t, "the JSON of a source", string(b), testKey)

	var sealed string
	if err := e.db.QueryRow(`SELECT api_key_sealed FROM indexers WHERE id = ?`, ix.ID).Scan(&sealed); err != nil {
		t.Fatal(err)
	}
	noKey(t, "the stored column", sealed, testKey)
	if plain, err := e.box.Open(sealed); err != nil || plain != testKey {
		t.Errorf("the sealed key does not open to the key: %q %v", plain, err)
	}

	list, err := e.svc.List(bg())
	if err != nil || len(list) != 1 {
		t.Fatalf("List: %v %d", err, len(list))
	}
	if list[0].APIKey != "••••b1a0" || !list[0].Enabled || list[0].Priority != DefaultPriority || list[0].Kind != KindTorznab {
		t.Errorf("listed source = %+v", list[0])
	}
}

func TestAShortKeyIsMaskedWithoutAnyOfItsEnd(t *testing.T) {
	e := newEnv(t)
	ix := e.add("Short", KindTorznab, "http://127.0.0.1:1/short", "abc123")
	if ix.APIKey != "••••" {
		t.Errorf("masked short key = %q: showing the end of a key that short gives away half of it", ix.APIKey)
	}
}

func TestCreateTakesTheKeyOutOfThePastedURL(t *testing.T) {
	e := newEnv(t)
	ix, err := e.svc.Create(bg(), Input{Name: "Pasted", Kind: "torznab", URL: "http://127.0.0.1:1/api?apikey=" + testKey + "&t=search"})
	if err != nil {
		t.Fatal(err)
	}
	if ix.URL != "http://127.0.0.1:1/api" {
		t.Errorf("url = %q, want the query dropped", ix.URL)
	}
	if ix.APIKey != "••••b1a0" {
		t.Errorf("the pasted key was not kept: %q", ix.APIKey)
	}
}

func TestCreateValidation(t *testing.T) {
	e := newEnv(t)
	bad := map[string]Input{
		"no name":        {Kind: "torznab", URL: "http://127.0.0.1:1/api"},
		"name too long":  {Name: strings.Repeat("x", 101), Kind: "torznab", URL: "http://127.0.0.1:1/api"},
		"bad kind":       {Name: "A", Kind: "rss", URL: "http://127.0.0.1:1/api"},
		"no url":         {Name: "A", Kind: "torznab"},
		"not http":       {Name: "A", Kind: "torznab", URL: "ftp://host/api"},
		"user in url":    {Name: "A", Kind: "torznab", URL: "http://user:pw@host/api"},
		"key with space": {Name: "A", Kind: "torznab", URL: "http://host/api", APIKey: "ab cd"},
		"bad category":   {Name: "A", Kind: "torznab", URL: "http://host/api", Categories: []int{5}},
		"bad priority":   {Name: "A", Kind: "torznab", URL: "http://host/api", Priority: 99},
	}
	for name, in := range bad {
		if _, err := e.svc.Create(bg(), in); !asInvalid(err) {
			t.Errorf("%s: err = %v, want an InvalidError", name, err)
		}
	}
	if _, err := e.svc.Create(bg(), Input{Name: "A", Kind: "torznab", URL: "http://host/api"}); err != nil {
		t.Fatal(err)
	}
	if _, err := e.svc.Create(bg(), Input{Name: "B", Kind: "newznab", URL: "http://host/api"}); !errors.Is(err, ErrDuplicate) {
		t.Errorf("a second source at the same address: err = %v, want ErrDuplicate", err)
	}
}

func TestUpdateKeepsTheKeyWhenTheMaskComesBack(t *testing.T) {
	e := newEnv(t)
	srv := sourcestest.NewServer(t, testKey)
	ix := e.add("Alpha", KindTorznab, srv.Feed(), testKey)

	masked := ix.APIKey
	name := "Alpha renamed"
	got, err := e.svc.Update(bg(), ix.ID, Patch{Name: &name, APIKey: &masked})
	if err != nil {
		t.Fatal(err)
	}
	if got.Name != name || got.APIKey != "••••b1a0" {
		t.Errorf("after update: %+v", got)
	}
	// The stored key still works against a server that wants it.
	res, err := e.svc.Test(bg(), ix.ID)
	if err != nil || !res.OK {
		t.Errorf("Test after a masked update: %+v %v (the key was lost)", res, err)
	}

	// A new key replaces it, and what was learned about the old one is forgotten.
	other := "ffffffffffffffffffffffffffff1234"
	got, err = e.svc.Update(bg(), ix.ID, Patch{APIKey: &other})
	if err != nil || got.APIKey != "••••1234" {
		t.Fatalf("new key: %+v %v", got, err)
	}
	if got.Caps != nil || got.LastOKAt != nil {
		t.Errorf("a new key should clear what was learned: caps %v lastOk %v", got.Caps, got.LastOKAt)
	}
	// Clearing the key.
	empty := ""
	got, _ = e.svc.Update(bg(), ix.ID, Patch{APIKey: &empty})
	if got.APIKey != "" {
		t.Errorf("cleared key shows %q", got.APIKey)
	}
}

func TestUpdateOtherFieldsAndMissingSource(t *testing.T) {
	e := newEnv(t)
	ix := e.add("Alpha", KindTorznab, "http://127.0.0.1:1/api", testKey)
	off, prio, cats := false, 5, []int{2000, 2000, 5040}
	got, err := e.svc.Update(bg(), ix.ID, Patch{Enabled: &off, Priority: &prio, Categories: &cats})
	if err != nil {
		t.Fatal(err)
	}
	if got.Enabled || got.Priority != 5 || len(got.Categories) != 2 {
		t.Errorf("updated = %+v (duplicate categories should collapse)", got)
	}
	if en, _ := e.svc.Enabled(bg()); len(en) != 0 {
		t.Errorf("a disabled source is listed as enabled: %v", en)
	}
	bad := 77
	if _, err := e.svc.Update(bg(), ix.ID, Patch{Priority: &bad}); !asInvalid(err) {
		t.Errorf("bad priority: %v", err)
	}
	if _, err := e.svc.Update(bg(), 999, Patch{Enabled: &off}); !errors.Is(err, ErrNotFound) {
		t.Errorf("missing source: %v", err)
	}
	if err := e.svc.Delete(bg(), ix.ID); err != nil {
		t.Fatal(err)
	}
	if err := e.svc.Delete(bg(), ix.ID); !errors.Is(err, ErrNotFound) {
		t.Errorf("second delete: %v", err)
	}
}

func TestListIsOrderedByPriority(t *testing.T) {
	e := newEnv(t)
	a := e.add("Slow", KindTorznab, "http://127.0.0.1:1/a", "")
	b := e.add("Best", KindTorznab, "http://127.0.0.1:1/b", "")
	p := 3
	if _, err := e.svc.Update(bg(), b.ID, Patch{Priority: &p}); err != nil {
		t.Fatal(err)
	}
	list, _ := e.svc.List(bg())
	if len(list) != 2 || list[0].ID != b.ID || list[1].ID != a.ID {
		t.Errorf("order = %v, want the lower priority number first", []string{list[0].Name, list[1].Name})
	}
}

// ---- Test ------------------------------------------------------------------------

func TestTestSucceedsAndLearnsCaps(t *testing.T) {
	e := newEnv(t)
	srv := sourcestest.NewServer(t, testKey)
	ix := e.add("Alpha", KindTorznab, srv.Feed(), testKey)
	res, err := e.svc.Test(bg(), ix.ID)
	if err != nil {
		t.Fatal(err)
	}
	if !res.OK || res.Caps == nil || !res.Caps.Supports(ModeMovie, "imdbid") {
		t.Fatalf("result = %+v", res)
	}
	for _, want := range []string{"Works", "movies", "TV", "IMDb", "TMDB", "TVmaze"} {
		if !strings.Contains(res.Message, want) {
			t.Errorf("summary %q lacks %q", res.Message, want)
		}
	}
	got, _ := e.svc.Get(bg(), ix.ID)
	if got.Caps == nil || got.LastOKAt == nil || got.LastError != "" {
		t.Errorf("the test should be recorded on the source: %+v", got)
	}
}

func TestTestReportsAWrongKeyWithoutRevealingIt(t *testing.T) {
	e := newEnv(t)
	srv := sourcestest.NewServer(t, testKey)
	ix := e.add("Alpha", KindTorznab, srv.Feed(), "ffffffffffffffffffffffffffffffff")
	res, err := e.svc.Test(bg(), ix.ID)
	if err != nil {
		t.Fatal(err)
	}
	if res.OK || res.Code != CodeKeyRefused {
		t.Fatalf("result = %+v, want key_refused", res)
	}
	noKey(t, "the message", res.Message, "ffffffffffffffffffffffffffffffff")
	got, _ := e.svc.Get(bg(), ix.ID)
	if got.LastError == "" || got.LastErrorAt == nil {
		t.Errorf("the failure should show on the source: %+v", got)
	}
}

func TestTestFailsWhenOnlyTheSearchNeedsTheKey(t *testing.T) {
	// Some sources serve t=caps to anyone; the probe search proves the key.
	e := newEnv(t)
	srv := sourcestest.NewServer(t, testKey)
	srv.PublicCaps = true
	ix := e.add("Alpha", KindTorznab, srv.Feed(), "")
	res, _ := e.svc.Test(bg(), ix.ID)
	if res.OK || res.Code != CodeKeyRefused {
		t.Errorf("a source that refuses a key-less search must not pass: %+v", res)
	}
}

func TestTestFailureKinds(t *testing.T) {
	cases := map[string]struct {
		status int
		code   string
	}{
		"not found":    {404, CodeNotAFeed},
		"forbidden":    {403, CodeKeyRefused},
		"server error": {500, CodeSourceError},
		"slow down":    {429, CodeSlowDown},
	}
	for name, c := range cases {
		e := newEnv(t)
		srv := sourcestest.NewServer(t, testKey)
		srv.Fail = c.status
		ix := e.add("Alpha", KindTorznab, srv.Feed(), testKey)
		res, err := e.svc.Test(bg(), ix.ID)
		if err != nil || res.OK || res.Code != c.code {
			t.Errorf("%s: %+v %v, want code %s", name, res, err, c.code)
		}
		noKey(t, name+" message", res.Message, testKey)
	}
}

func TestTestUnreachableNeverLeaksTheKey(t *testing.T) {
	e := newEnv(t)
	srv := sourcestest.NewServer(t, testKey)
	addr := srv.Feed()
	srv.Close()
	ix := e.add("Gone", KindTorznab, addr, testKey)
	res, err := e.svc.Test(bg(), ix.ID)
	if err != nil || res.OK || res.Code != CodeUnreachable {
		t.Fatalf("result = %+v %v", res, err)
	}
	noKey(t, "the message", res.Message, testKey)
	got, _ := e.svc.Get(bg(), ix.ID)
	noKey(t, "last_error", got.LastError, testKey)
	noKey(t, "the log", e.logs.String(), testKey)
}

// ---- searching -------------------------------------------------------------------

func TestSearchUsesIDsThenFallsBackToText(t *testing.T) {
	e := newEnv(t)
	srv := sourcestest.NewServer(t, testKey)
	srv.Search = func(q url.Values) []sourcestest.Item {
		if q.Get("q") != "" { // only the text search finds it
			return []sourcestest.Item{sourcestest.Torrent("Metropolis.1927.1080p.BluRay.x264-GRP", 8<<30, 20)}
		}
		return nil
	}
	ix := e.add("Alpha", KindTorznab, srv.Feed(), testKey)
	items, err := e.svc.Search(bg(), ix.ID, Query{Media: MediaMovie, Text: "Metropolis", Year: 1927, IMDb: "tt0017136", TMDb: 19})
	if err != nil || len(items) != 1 {
		t.Fatalf("items %d err %v", len(items), err)
	}
	reqs := srv.Requests(false)
	if len(reqs) != 2 {
		t.Fatalf("%d search calls, want 2 (ids, then text)", len(reqs))
	}
	first, second := reqs[0].Query, reqs[1].Query
	if first.Get("t") != "movie" || first.Get("imdbid") != "0017136" || first.Get("tmdbid") != "19" || first.Has("q") {
		t.Errorf("first call = %v", first)
	}
	if second.Get("q") != "Metropolis 1927" || second.Has("imdbid") {
		t.Errorf("second call = %v", second)
	}
	if first.Get("apikey") != testKey {
		t.Error("the key must reach the source")
	}
	if items[0].IndexerID != ix.ID {
		t.Errorf("item indexer = %d", items[0].IndexerID)
	}
}

func TestSearchDoesNotRetryWhenIDsFoundSomething(t *testing.T) {
	e := newEnv(t)
	srv := sourcestest.NewServer(t, testKey)
	srv.Search = func(q url.Values) []sourcestest.Item {
		return []sourcestest.Item{sourcestest.Torrent("Sintel.2010.1080p.BluRay.x264-GRP", 3<<30, 5)}
	}
	ix := e.add("Alpha", KindTorznab, srv.Feed(), testKey)
	if _, err := e.svc.Search(bg(), ix.ID, Query{Media: MediaMovie, Text: "Sintel", IMDb: "tt1727587"}); err != nil {
		t.Fatal(err)
	}
	if n := srv.Searches(); n != 1 {
		t.Errorf("%d search calls, want 1", n)
	}
}

func TestCapsAreFetchedOnceAndCached(t *testing.T) {
	e := newEnv(t)
	srv := sourcestest.NewServer(t, testKey)
	ix := e.add("Alpha", KindTorznab, srv.Feed(), testKey)
	for range 3 {
		if _, err := e.svc.Search(bg(), ix.ID, Query{Media: MediaMovie, Text: "Sintel"}); err != nil {
			t.Fatal(err)
		}
	}
	caps := 0
	for _, r := range srv.Requests(true) {
		if r.Query.Get("t") == "caps" {
			caps++
		}
	}
	if caps != 1 {
		t.Errorf("%d caps calls, want 1", caps)
	}
	// A week later they are asked for again.
	e.now = e.now.Add(8 * 24 * time.Hour)
	if _, err := e.svc.Search(bg(), ix.ID, Query{Media: MediaMovie, Text: "Sintel"}); err != nil {
		t.Fatal(err)
	}
	caps = 0
	for _, r := range srv.Requests(true) {
		if r.Query.Get("t") == "caps" {
			caps++
		}
	}
	if caps != 2 {
		t.Errorf("%d caps calls after a week, want 2", caps)
	}
}

func TestSearchUsesTVParamsForShows(t *testing.T) {
	e := newEnv(t)
	srv := sourcestest.NewServer(t, testKey)
	ix := e.add("Alpha", KindTorznab, srv.Feed(), testKey)
	if _, err := e.svc.Search(bg(), ix.ID, Query{Media: MediaShow, Text: "Pioneer One", TVMaze: 99001, Season: 1, Episode: 4}); err != nil {
		t.Fatal(err)
	}
	q := srv.Requests(false)[0].Query
	if q.Get("t") != "tvsearch" || q.Get("season") != "1" || q.Get("ep") != "4" || q.Get("tvmazeid") != "99001" || q.Get("cat") != "5000" {
		t.Errorf("tv call = %v", q)
	}
}

func TestSearchAllIsParallelWithinTheLimit(t *testing.T) {
	e := newEnv(t)
	srv := sourcestest.NewServer(t, testKey)
	srv.Delay = 60 * time.Millisecond
	srv.Search = func(url.Values) []sourcestest.Item {
		return []sourcestest.Item{sourcestest.Torrent("Sintel.2010.1080p.BluRay.x264-GRP", 3<<30, 5)}
	}
	const n = 14
	for i := range n {
		e.add(fmt.Sprintf("Source %02d", i), KindTorznab, fmt.Sprintf("%s/api/%d", srv.URL, i), testKey)
	}
	outs, err := e.svc.SearchAll(bg(), Query{Media: MediaMovie, Text: "Sintel"})
	if err != nil {
		t.Fatal(err)
	}
	if len(outs) != n {
		t.Fatalf("%d outcomes, want %d", len(outs), n)
	}
	for _, o := range outs {
		if o.Err != nil || len(o.Items) != 1 {
			t.Errorf("%s: err %v items %d", o.Indexer.Name, o.Err, len(o.Items))
		}
	}
	if p := srv.Peak(); p < 2 || p > SearchConcurrency {
		t.Errorf("peak concurrency = %d, want between 2 and %d", p, SearchConcurrency)
	}
}

func TestSearchAllKeepsGoingWhenOneSourceFails(t *testing.T) {
	e := newEnv(t)
	good := sourcestest.NewServer(t, testKey)
	good.Search = func(url.Values) []sourcestest.Item {
		return []sourcestest.Item{sourcestest.Torrent("Sintel.2010.1080p.BluRay.x264-GRP", 3<<30, 5)}
	}
	bad := sourcestest.NewServer(t, testKey)
	bad.Fail = 500
	a := e.add("Good", KindTorznab, good.Feed(), testKey)
	b := e.add("Bad", KindTorznab, bad.Feed(), testKey)
	off := false
	c := e.add("Off", KindTorznab, "http://127.0.0.1:1/api", testKey)
	if _, err := e.svc.Update(bg(), c.ID, Patch{Enabled: &off}); err != nil {
		t.Fatal(err)
	}
	outs, err := e.svc.SearchAll(bg(), Query{Media: MediaMovie, Text: "Sintel"})
	if err != nil || len(outs) != 2 {
		t.Fatalf("outcomes %d err %v (a disabled source must not be asked)", len(outs), err)
	}
	byName := map[string]Outcome{}
	for _, o := range outs {
		byName[o.Indexer.Name] = o
	}
	if o := byName["Good"]; o.Err != nil || len(o.Items) != 1 {
		t.Errorf("good source: %+v", o)
	}
	o := byName["Bad"]
	var se *Error
	if !errors.As(o.Err, &se) || se.Code != CodeSourceError {
		t.Errorf("bad source error = %v", o.Err)
	}
	// Both are recorded on the sources.
	ga, _ := e.svc.Get(bg(), a.ID)
	gb, _ := e.svc.Get(bg(), b.ID)
	if ga.LastOKAt == nil || ga.LastError != "" || gb.LastError == "" || gb.LastErrorAt == nil {
		t.Errorf("recorded state: good %+v bad %+v", ga, gb)
	}
}

func TestASourceLimitedToTVIsNotAskedForMovies(t *testing.T) {
	e := newEnv(t)
	srv := sourcestest.NewServer(t, testKey)
	e.add("TV only", KindTorznab, srv.Feed(), testKey, 5000, 5040)
	outs, err := e.svc.SearchAll(bg(), Query{Media: MediaMovie, Text: "Sintel"})
	if err != nil || len(outs) != 1 || !outs[0].Skipped || outs[0].Err != nil {
		t.Fatalf("outcomes = %+v err %v", outs, err)
	}
	if n := len(srv.Requests(true)); n != 0 {
		t.Errorf("%d calls made to a source that does not carry movies", n)
	}
	// Asked for a show it is searched, with only its own categories.
	if _, err := e.svc.SearchAll(bg(), Query{Media: MediaShow, Text: "Pioneer One", Season: 1}); err != nil {
		t.Fatal(err)
	}
	got := srv.Requests(false)
	if len(got) == 0 || got[0].Query.Get("cat") != "5000,5040" {
		t.Errorf("show call = %v", got)
	}
}

func TestTheKeyIsNeverLogged(t *testing.T) {
	e := newEnv(t)
	srv := sourcestest.NewServer(t, testKey)
	srv.Search = func(url.Values) []sourcestest.Item {
		return []sourcestest.Item{sourcestest.Torrent("Sintel.2010.1080p.BluRay.x264-GRP", 3<<30, 5)}
	}
	ix := e.add("Alpha", KindTorznab, srv.Feed(), testKey)
	if _, err := e.svc.Test(bg(), ix.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := e.svc.SearchAll(bg(), Query{Media: MediaMovie, Text: "Sintel", IMDb: "tt1727587"}); err != nil {
		t.Fatal(err)
	}
	if err := e.svc.SyncRSS(bg()); err != nil {
		t.Fatal(err)
	}
	logs := e.logs.String()
	if !strings.Contains(logs, "outbound") {
		t.Fatal("expected debug lines from the outbound client; the test is not checking anything")
	}
	noKey(t, "the log", logs, testKey)
	if strings.Contains(logs, "apikey") {
		t.Errorf("a query string reached the log: %.300q", logs)
	}
}

func TestSearchOfAnUnreadableKeyAsksForItAgain(t *testing.T) {
	e := newEnv(t)
	ix := e.add("Alpha", KindTorznab, "http://127.0.0.1:1/api", testKey)
	if _, err := e.db.Exec(`UPDATE indexers SET api_key_sealed = 'bm90IHNlYWxlZA==' WHERE id = ?`, ix.ID); err != nil {
		t.Fatal(err)
	}
	_, err := e.svc.Search(bg(), ix.ID, Query{Media: MediaMovie, Text: "Sintel"})
	var se *Error
	if !errors.As(err, &se) || se.Code != CodeKeyRefused || !strings.Contains(se.Message, "Enter it again") {
		t.Errorf("err = %v", err)
	}
}
