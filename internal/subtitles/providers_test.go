package subtitles

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/BaileyPillon/marquee/internal/core/outbound"
	"github.com/BaileyPillon/marquee/internal/parse"
)

func testClient() *outbound.Client {
	return outbound.New("test", outbound.Options{Logger: quietLog(), HostRPS: 100000, HostBurst: 100000, MaxRetries: -1})
}

func newOS(t *testing.T, f *fakeOS, cfg OpenSubtitlesConfig) *OpenSubtitles {
	t.Helper()
	o := NewOpenSubtitles(testClient(), f.base(), testVersion)
	o.Configure(cfg)
	return o
}

var keyOnly = OpenSubtitlesConfig{Enabled: true, APIKey: testKey}
var withLogin = OpenSubtitlesConfig{Enabled: true, APIKey: testKey, Username: testUser, Password: testPassword}

func nolVideo() VideoInfo {
	return VideoInfo{FileID: 1, Kind: "movie", Title: "Night of the Living Dead", Year: 1968, IMDb: "tt0063350", TMDb: 10331,
		Hash: "8e245d9679d31e12", Parsed: parse.ParseRelease("Night of the Living Dead (1968) Bluray-1080p.mkv")}
}

// ---- OpenSubtitles ------------------------------------------------------------------

func TestOpenSubtitlesSearchParameters(t *testing.T) {
	f := newFakeOS(t)
	o := newOS(t, f, keyOnly)
	if _, err := o.Search(bg(), Query{Video: nolVideo(), Languages: []string{"en", "pt-br"}}); err != nil {
		t.Fatal(err)
	}
	q := f.lastSearch()
	want := map[string]string{
		"imdb_id": "63350", "tmdb_id": "10331", "languages": "en,pt-br", "moviehash": "8e245d9679d31e12", "type": "movie",
	}
	for k, v := range want {
		if q.Get(k) != v {
			t.Errorf("%s = %q, want %q (query %v)", k, q.Get(k), v, q)
		}
	}
	if q.Has("parent_imdb_id") || q.Has("season_number") || q.Has("query") {
		t.Errorf("a film search carries episode or text parameters: %v", q)
	}
	f.noBadHeaders() // Api-Key and "User-Agent: Marquee v1.2.3" on every call
}

func TestOpenSubtitlesSearchParameterOrder(t *testing.T) {
	// The API wants its query parameters in alphabetical order; a different order
	// makes it answer with a redirect.
	var raw string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		raw = r.URL.RawQuery
		_, _ = w.Write([]byte(`{"data":[]}`))
	}))
	defer srv.Close()
	o := NewOpenSubtitles(testClient(), srv.URL+"/api/v1", testVersion)
	o.Configure(keyOnly)
	v := nolVideo()
	v.Kind, v.Season, v.Episode, v.Title = "show", 1, 2, "Pioneer One"
	if _, err := o.Search(bg(), Query{Video: v, Languages: []string{"en"}}); err != nil {
		t.Fatal(err)
	}
	parts := strings.Split(raw, "&")
	for i := 1; i < len(parts); i++ {
		if parts[i-1] > parts[i] {
			t.Errorf("query %q is not in alphabetical order", raw)
		}
	}
}

func TestOpenSubtitlesEpisodeSearch(t *testing.T) {
	f := newFakeOS(t)
	o := newOS(t, f, keyOnly)
	v := VideoInfo{Kind: "show", Title: "Pioneer One", IMDb: "tt1621045", Season: 1, Episode: 2, Hash: "00000000000000aa"}
	if _, err := o.Search(bg(), Query{Video: v, Languages: []string{"en"}}); err != nil {
		t.Fatal(err)
	}
	q := f.lastSearch()
	for k, want := range map[string]string{
		"parent_imdb_id": "1621045", "season_number": "1", "episode_number": "2", "languages": "en", "moviehash": "00000000000000aa", "type": "episode",
	} {
		if q.Get(k) != want {
			t.Errorf("%s = %q, want %q (query %v)", k, q.Get(k), want, q)
		}
	}
	if q.Has("imdb_id") || q.Has("tmdb_id") {
		t.Errorf("an episode search uses the show's id as the parent: %v", q)
	}
}

func TestOpenSubtitlesSearchByNameWhenThereAreNoIds(t *testing.T) {
	f := newFakeOS(t)
	o := newOS(t, f, keyOnly)
	if _, err := o.Search(bg(), Query{Video: VideoInfo{Kind: "movie", Title: "Sintel", Year: 2010}, Languages: []string{"en"}}); err != nil {
		t.Fatal(err)
	}
	q := f.lastSearch()
	if q.Get("query") != "sintel" || q.Get("year") != "2010" || q.Has("imdb_id") {
		t.Errorf("query = %v", q)
	}
}

func TestOpenSubtitlesResults(t *testing.T) {
	f := newFakeOS(t)
	f.items = func(url.Values) []osItem {
		return []osItem{
			{FileID: 11, Release: "Night.of.the.Living.Dead.1968.1080p.BluRay.x264-GRP", Language: "en", Downloads: 4321, HashMatch: true, Year: 1968},
			{FileID: 12, Release: "NOTLD.1968.WEB", Language: "pt-BR", HI: true, Downloads: 7, Year: 1968},
			{FileID: 13, Release: "Machine one", Language: "en", Machine: true},
			{FileID: 14, Release: "Disc two of a set", Language: "en", CDNumber: 2},
		}
	}
	o := newOS(t, f, keyOnly)
	got, err := o.Search(bg(), Query{Video: nolVideo(), Languages: []string{"en"}})
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 3 {
		t.Fatalf("%d results, want 3 (the second disc of a set is dropped): %+v", len(got), got)
	}
	a, b, c := got[0], got[1], got[2]
	if a.Provider != "opensubtitles" || a.ProviderID != "11" || a.Language != "en" || a.Downloads != 4321 || !a.HashMatch || !a.IDMatch ||
		a.HI || a.Year != 1968 || len(a.Releases) != 2 || a.Releases[0] != "Night.of.the.Living.Dead.1968.1080p.BluRay.x264-GRP" {
		t.Errorf("first = %+v", a)
	}
	if b.Language != "pt-br" || !b.HI || b.HashMatch {
		t.Errorf("second = %+v", b)
	}
	if !c.Machine {
		t.Errorf("third = %+v", c)
	}
}

func TestOpenSubtitlesErrors(t *testing.T) {
	f := newFakeOS(t)
	o := newOS(t, f, keyOnly)
	q := Query{Video: nolVideo(), Languages: []string{"en"}}
	var (
		auth *AuthError
		rl   *RateLimitError
		un   *UnavailableError
	)

	// No key: nothing is sent.
	none := NewOpenSubtitles(testClient(), f.base(), testVersion)
	if _, err := none.Search(bg(), q); !errors.As(err, &auth) || f.searchCount() != 0 {
		t.Errorf("no key: %v, %d searches", err, f.searchCount())
	}

	f.status = http.StatusUnauthorized
	if _, err := o.Search(bg(), q); !errors.As(err, &auth) || strings.Contains(err.Error(), testKey) {
		t.Errorf("401: %v", err)
	}
	f.status, f.retryAfter = http.StatusTooManyRequests, "7"
	if _, err := o.Search(bg(), q); !errors.As(err, &rl) || rl.RetryAfter != 7*time.Second {
		t.Errorf("429: %v", err)
	}
	f.status = http.StatusServiceUnavailable
	if _, err := o.Search(bg(), q); !errors.As(err, &un) || !strings.Contains(err.Error(), "503") {
		t.Errorf("503: %v", err)
	}
	f.status = http.StatusBadRequest
	if _, err := o.Search(bg(), q); !errors.As(err, &un) {
		t.Errorf("400: %v", err)
	}

	// An answer that is not JSON.
	bad := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { _, _ = w.Write([]byte("<html>")) }))
	defer bad.Close()
	ob := NewOpenSubtitles(testClient(), bad.URL, testVersion)
	ob.Configure(keyOnly)
	if _, err := ob.Search(bg(), q); !errors.As(err, &un) {
		t.Errorf("html: %v", err)
	}

	// A server that is not there.
	gone := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
	gone.Close()
	og := NewOpenSubtitles(testClient(), gone.URL, testVersion)
	og.Configure(keyOnly)
	if _, err := og.Search(bg(), q); !errors.As(err, &un) || strings.Contains(err.Error(), "127.0.0.1") {
		t.Errorf("unreachable: %v", err)
	}
}

func TestOpenSubtitlesDownload(t *testing.T) {
	f := newFakeOS(t)
	f.files[11] = srtOne
	f.remaining = 19
	o := newOS(t, f, withLogin)
	res, err := o.Download(bg(), Candidate{ProviderID: "11"})
	if err != nil {
		t.Fatal(err)
	}
	if string(res.Data) != srtOne || res.Remaining == nil || *res.Remaining != 18 || !res.ResetAt.Equal(time.Date(2026, 11, 22, 0, 0, 0, 0, time.UTC)) {
		t.Errorf("result = %q remaining %v reset %v", res.Data, res.Remaining, res.ResetAt)
	}
	if got := f.downloaded(); len(got) != 1 || got[0] != 11 {
		t.Errorf("downloads = %v", got)
	}
	f.noBadHeaders()

	// The login is made once and its token kept for the next download.
	if _, err := o.Download(bg(), Candidate{ProviderID: "12"}); err != nil {
		t.Fatal(err)
	}
	if f.logins != 1 {
		t.Errorf("logins = %d, want 1", f.logins)
	}
	// A different account logs in again.
	o.Configure(OpenSubtitlesConfig{Enabled: true, APIKey: testKey, Username: testUser, Password: testPassword + " "})
	if _, err := o.Download(bg(), Candidate{ProviderID: "13"}); err == nil {
		t.Error("a wrong password should not download")
	}
	if f.logins != 2 {
		t.Errorf("logins = %d, want 2 after the account changed", f.logins)
	}
}

func TestOpenSubtitlesDownloadWithoutLogin(t *testing.T) {
	f := newFakeOS(t)
	o := newOS(t, f, keyOnly)
	if _, err := o.Download(bg(), Candidate{ProviderID: "11"}); err != nil {
		t.Fatal(err)
	}
	if f.logins != 0 {
		t.Errorf("logged in without a username: %d", f.logins)
	}
}

func TestOpenSubtitlesLoginDoesNotSendTheKeyElsewhere(t *testing.T) {
	// The login answer names another host ("evil.example.net"); the key and the
	// token never go there. The fake's own base is kept.
	f := newFakeOS(t)
	o := newOS(t, f, withLogin)
	if _, err := o.Download(bg(), Candidate{ProviderID: "11"}); err != nil {
		t.Fatal(err)
	}
	if got := f.downloaded(); len(got) != 1 {
		t.Errorf("the download did not reach the fake: %v", got)
	}
	// With the real base, only an opensubtitles.com host is taken.
	real := NewOpenSubtitles(testClient(), "", testVersion)
	if !real.loginBase {
		t.Fatal("the default base should accept the login's base_url")
	}
}

func TestOpenSubtitlesDownloadQuotaAndLimits(t *testing.T) {
	f := newFakeOS(t)
	o := newOS(t, f, keyOnly)
	var (
		q  *QuotaError
		rl *RateLimitError
		a  *AuthError
	)
	f.remaining = 0
	_, err := o.Download(bg(), Candidate{ProviderID: "11"})
	if !errors.As(err, &q) || !q.ResetAt.Equal(time.Date(2026, 11, 22, 0, 0, 0, 0, time.UTC)) {
		t.Errorf("quota: %v", err)
	}

	f.remaining = 100
	f.retryAfter = "3"
	f.downloadAs = func(int64) (int, string) { return http.StatusTooManyRequests, `{"message":"slow down"}` }
	if _, err := o.Download(bg(), Candidate{ProviderID: "11"}); !errors.As(err, &rl) || rl.RetryAfter != 3*time.Second {
		t.Errorf("429: %v", err)
	}
	f.downloadAs = func(int64) (int, string) { return http.StatusUnauthorized, `{"message":"bad token"}` }
	if _, err := o.Download(bg(), Candidate{ProviderID: "11"}); !errors.As(err, &a) {
		t.Errorf("401: %v", err)
	}
	f.downloadAs = nil

	// A bad id, a missing key.
	var inv *InvalidError
	if _, err := o.Download(bg(), Candidate{ProviderID: "x"}); !errors.As(err, &inv) {
		t.Errorf("bad id: %v", err)
	}
	if _, err := NewOpenSubtitles(testClient(), f.base(), testVersion).Download(bg(), Candidate{ProviderID: "1"}); !errors.As(err, &a) {
		t.Errorf("no key: %v", err)
	}
}

func TestOpenSubtitlesBadDownloadLinks(t *testing.T) {
	var un *UnavailableError
	for name, link := range map[string]string{"empty": "", "file scheme": "file:///etc/passwd", "no host": "https:///x.srt", "junk": "::::"} {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			_, _ = w.Write([]byte(`{"link":"` + link + `","remaining":5}`))
		}))
		o := NewOpenSubtitles(testClient(), srv.URL, testVersion)
		o.Configure(keyOnly)
		if _, err := o.Download(bg(), Candidate{ProviderID: "1"}); !errors.As(err, &un) {
			t.Errorf("%s: %v", name, err)
		}
		srv.Close()
	}
	// A link that answers with an error, and one that is far too big.
	f := newFakeOS(t)
	f.linkBody[11] = []byte(strings.Repeat("x", maxSubtitleBytes+10))
	if _, err := newOS(t, f, keyOnly).Download(bg(), Candidate{ProviderID: "11"}); !errors.As(err, &un) {
		t.Errorf("oversized: %v", err)
	}
}

func TestOpenSubtitlesTest(t *testing.T) {
	f := newFakeOS(t)
	if err := newOS(t, f, withLogin).Test(bg()); err != nil {
		t.Errorf("good key and login: %v", err)
	}
	var a *AuthError
	if err := newOS(t, f, OpenSubtitlesConfig{APIKey: testKey, Username: testUser, Password: "wrong"}).Test(bg()); !errors.As(err, &a) ||
		!strings.Contains(err.Error(), "username and password") {
		t.Errorf("bad password: %v", err)
	}
	if err := NewOpenSubtitles(testClient(), f.base(), testVersion).Test(bg()); !errors.As(err, &a) {
		t.Errorf("no key: %v", err)
	}
	f.status = http.StatusUnauthorized
	if err := newOS(t, f, keyOnly).Test(bg()); !errors.As(err, &a) {
		t.Errorf("a key the service refuses: %v", err)
	}
}

func TestImdbNumber(t *testing.T) {
	for in, want := range map[string]int64{"tt0063350": 63350, "TT1": 1, "133093": 133093, "": 0, "ttx": 0, "tt0": 0} {
		if got := imdbNumber(in); got != want {
			t.Errorf("imdbNumber(%q) = %d, want %d", in, got, want)
		}
	}
}

// ---- Podnapisi ------------------------------------------------------------------------

func newPod(f *fakePod) *Podnapisi { return NewPodnapisi(testClient(), f.URL, testVersion) }

func TestPodnapisiSearch(t *testing.T) {
	f := newFakePod(t)
	f.entries = func(q url.Values) []map[string]any {
		return []map[string]any{
			{"id": "abc", "language": "en", "year": 1968, "releases": []string{"Night.of.the.Living.Dead.1968.1080p.BluRay-GRP", "NOTLD.1968.720p"},
				"stats": map[string]any{"downloads": 321}, "flags": []string{"hearing_impaired"}},
			{"id": "def", "language": "pb", "year": "1968", "release": "NOTLD.1968", "downloads": "12"},
			{"language": "en"}, // no id: dropped
		}
	}
	p := newPod(f)
	got, err := p.Search(bg(), Query{Video: nolVideo(), Languages: []string{"en", "pt-br"}})
	if err != nil {
		t.Fatal(err)
	}
	f.mu.Lock()
	searches, accepts := f.searches, f.accepts
	f.mu.Unlock()
	if len(searches) != 2 {
		t.Fatalf("%d searches, want one per language", len(searches))
	}
	for _, a := range accepts {
		if a != "application/json" {
			t.Errorf("Accept = %q", a)
		}
	}
	q := searches[0]
	if q.Get("keywords") != "Night of the Living Dead" || q.Get("year") != "1968" || q.Get("language") != "en" || q.Has("seasons") {
		t.Errorf("first search = %v", q)
	}
	if searches[1].Get("language") != "pb" {
		t.Errorf("Brazilian Portuguese is pb at Podnapisi, got %v", searches[1])
	}
	if len(got) != 4 { // two good entries per language
		t.Fatalf("%d results: %+v", len(got), got)
	}
	a := got[0]
	if a.Provider != "podnapisi" || a.ProviderID != "abc" || a.Language != "en" || !a.HI || a.Downloads != 321 || a.Year != 1968 ||
		len(a.Releases) != 2 || a.HashMatch {
		t.Errorf("first = %+v", a)
	}
	if b := got[1]; b.Language != "pt-br" || b.Downloads != 12 || b.Year != 1968 || len(b.Releases) != 1 || b.HI {
		t.Errorf("second = %+v", b)
	}
}

func TestPodnapisiEpisodeSearch(t *testing.T) {
	f := newFakePod(t)
	p := newPod(f)
	v := VideoInfo{Kind: "show", Title: "Pioneer One", Year: 2010, Season: 2, Episode: 5}
	if _, err := p.Search(bg(), Query{Video: v, Languages: []string{"en"}}); err != nil {
		t.Fatal(err)
	}
	f.mu.Lock()
	q := f.searches[0]
	f.mu.Unlock()
	if q.Get("keywords") != "Pioneer One" || q.Get("seasons") != "2" || q.Get("episodes") != "5" || q.Has("year") {
		t.Errorf("query = %v", q)
	}
}

func TestPodnapisiDownloadUnzipsTheSRT(t *testing.T) {
	f := newFakePod(t)
	f.zips["abc"] = zipOf(map[string]string{"Night.of.the.Living.Dead.srt": srtOne, "info.nfo": "from podnapisi"})
	res, err := newPod(f).Download(bg(), Candidate{ProviderID: "abc"})
	if err != nil || string(res.Data) != srtOne || res.Remaining != nil {
		t.Errorf("Download = %q, %v, remaining %v", res.Data, err, res.Remaining)
	}
	if got := f.downloaded(); len(got) != 1 || got[0] != "abc" {
		t.Errorf("downloads = %v", got)
	}
	// A plain srt answer is accepted too.
	f.zips["raw"] = []byte(srtOne)
	if res, err := newPod(f).Download(bg(), Candidate{ProviderID: "raw"}); err != nil || string(res.Data) != srtOne {
		t.Errorf("plain: %q, %v", res.Data, err)
	}
}

func TestPodnapisiErrors(t *testing.T) {
	f := newFakePod(t)
	p := newPod(f)
	var (
		un *UnavailableError
		rl *RateLimitError
		in *InvalidError
	)
	f.zips["nozip"] = zipOf(map[string]string{"a.nfo": "x"})
	if _, err := p.Download(bg(), Candidate{ProviderID: "nozip"}); !errors.As(err, &un) || !strings.Contains(err.Error(), "no .srt") {
		t.Errorf("a zip with no srt: %v", err)
	}
	for _, id := range []string{"", "a/b", "a b", "a?b"} {
		if _, err := p.Download(bg(), Candidate{ProviderID: id}); !errors.As(err, &in) {
			t.Errorf("id %q: %v", id, err)
		}
	}
	f.status = http.StatusTooManyRequests
	if _, err := p.Search(bg(), Query{Video: nolVideo(), Languages: []string{"en"}}); !errors.As(err, &rl) {
		t.Errorf("429: %v", err)
	}
	f.status = http.StatusInternalServerError
	if _, err := p.Search(bg(), Query{Video: nolVideo(), Languages: []string{"en"}}); !errors.As(err, &un) || !strings.Contains(err.Error(), "500") {
		t.Errorf("500: %v", err)
	}
	// One language failing is not the whole search failing.
	f.status = 0
	calls := 0
	f.entries = func(q url.Values) []map[string]any {
		calls++
		return []map[string]any{{"id": "x", "language": "en"}}
	}
	if got, err := p.Search(bg(), Query{Video: nolVideo(), Languages: []string{"en", "fr"}}); err != nil || len(got) == 0 {
		t.Errorf("search: %v, %d results", err, len(got))
	}
	if err := p.Test(bg()); err != nil {
		t.Errorf("Test: %v", err)
	}
	f.status = http.StatusInternalServerError
	if err := p.Test(bg()); !errors.As(err, &un) {
		t.Errorf("Test when down: %v", err)
	}
	// A server that is not there.
	gone := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
	gone.Close()
	if _, err := NewPodnapisi(testClient(), gone.URL, testVersion).Search(bg(), Query{Video: nolVideo(), Languages: []string{"en"}}); !errors.As(err, &un) {
		t.Errorf("unreachable: %v", err)
	}
}

func TestPodLanguageMapping(t *testing.T) {
	for in, want := range map[string]string{"en": "en", "pt-br": "pb", "zh-cn": "zh", "FR": "fr"} {
		if got := podLanguage(in); got != want {
			t.Errorf("podLanguage(%q) = %q, want %q", in, got, want)
		}
	}
	if podFromLanguage("pb") != "pt-br" || podFromLanguage("EN") != "en" {
		t.Error("podFromLanguage")
	}
}
