package watchlists

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"

	"github.com/BaileyPillon/marquee/internal/core/outbound"
)

// clientFor is a PlexClient whose three services all answer from one handler.
func clientFor(t *testing.T, h http.HandlerFunc) *PlexClient {
	t.Helper()
	srv := httptest.NewServer(h)
	t.Cleanup(srv.Close)
	hc := outbound.New("test", outbound.Options{Logger: quiet(), MaxRetries: -1})
	return NewPlexClient(hc, PlexURLs{Plex: srv.URL + "/p", Discover: srv.URL + "/d", Community: srv.URL + "/c"})
}

func reply(body string) http.HandlerFunc {
	return func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(body))
	}
}

var authT = Auth{ClientID: "cid", Token: "tok"}

func TestPlexClientReadsIdsAsNumbersOrStrings(t *testing.T) {
	c := clientFor(t, reply(`{"id": "4321", "code": "ABCD", "authToken": null}`))
	if p, err := c.CreatePIN(bg(), authT); err != nil || p.ID != 4321 || p.Code != "ABCD" {
		t.Errorf("string id: %+v, %v", p, err)
	}
	c = clientFor(t, reply(`{"id": 4321, "code": "ABCD"}`))
	if p, err := c.CreatePIN(bg(), authT); err != nil || p.ID != 4321 {
		t.Errorf("number id: %+v, %v", p, err)
	}
	for _, bad := range []string{`{}`, `{"id": 1}`, `{"code": "X"}`, `not json`, `{"id": 0, "code": "X"}`} {
		c = clientFor(t, reply(bad))
		if _, err := c.CreatePIN(bg(), authT); err == nil {
			t.Errorf("CreatePIN accepted %q", bad)
		}
	}
	c = clientFor(t, reply(`{"id": 12, "uuid": "abc", "username": "", "title": "Bailey P"}`))
	if u, err := c.Account(bg(), authT); err != nil || u.ID != "12" || u.Username != "Bailey P" {
		t.Errorf("account: %+v, %v", u, err)
	}
	c = clientFor(t, reply(`{"username": "x"}`))
	if _, err := c.Account(bg(), authT); err == nil {
		t.Error("an account answer with no id was accepted")
	}
}

func TestPlexClientAnswersAreMappedWithoutLeakingTheToken(t *testing.T) {
	secret := "tok-very-secret"
	for status, check := range map[int]func(error) bool{
		401: func(err error) bool { return errors.Is(err, ErrPlexAuth) },
		403: func(err error) bool { return errors.Is(err, ErrPlexAuth) },
		500: func(err error) bool { var pe *plexError; return errors.As(err, &pe) && pe.Status == 500 },
	} {
		c := clientFor(t, func(w http.ResponseWriter, _ *http.Request) { http.Error(w, "no", status) })
		_, err := c.OwnerWatchlist(bg(), Auth{ClientID: "cid", Token: secret})
		if err == nil || !check(err) || strings.Contains(err.Error(), secret) {
			t.Errorf("status %d: err = %v", status, err)
		}
	}
	// The pin that Plex has forgotten.
	c := clientFor(t, http.NotFound)
	if _, err := c.CheckPIN(bg(), authT, 5, "X"); !errors.Is(err, ErrPinExpired) {
		t.Errorf("a pin Plex forgot = %v", err)
	}
	// A server that is not there: the error has no query string in it.
	c = clientFor(t, reply("{}"))
	c.discoverURL = "http://127.0.0.1:1"
	if _, err := c.OwnerWatchlist(bg(), Auth{ClientID: "cid", Token: secret}); err == nil || strings.Contains(err.Error(), secret) {
		t.Errorf("unreachable: %v", err)
	}
}

func TestOwnerWatchlistStopsWhenThePagesAreShortAndReadsKeysLeniently(t *testing.T) {
	calls := 0
	c := clientFor(t, func(w http.ResponseWriter, r *http.Request) {
		calls++
		// No totalSize, and fewer items than a page: one call is enough. An item
		// has no ratingKey (its key is read), another repeats, another has no id.
		reply(`{"MediaContainer": {"Metadata": [
			{"key": "/library/metadata/aaa", "type": "movie", "title": " Metropolis ", "year": 1927, "Guid": [{"id": "tmdb://19"}, {"id": ""}]},
			{"ratingKey": "aaa", "type": "movie", "title": "again"},
			{"ratingKey": "bbb", "type": "TV", "title": "A Show"},
			{"type": "movie", "title": "No id at all"}]}}`)(w, r)
	})
	items, err := c.OwnerWatchlist(bg(), authT)
	if err != nil {
		t.Fatal(err)
	}
	if calls != 1 || len(items) != 2 || items[0].Key != "aaa" || items[0].Type != "movie" || items[1].Key != "bbb" || items[1].Type != "show" ||
		!reflect.DeepEqual(items[0].Guids, []string{"tmdb://19"}) || items[0].Title != "Metropolis" {
		t.Errorf("calls %d, items %+v", calls, items)
	}
	if _, err := clientFor(t, reply(`<html>`)).OwnerWatchlist(bg(), authT); err == nil {
		t.Error("an answer that is not JSON was accepted")
	}
}

func TestGraphQLFriendsAndWatchlists(t *testing.T) {
	c := clientFor(t, func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.URL.Path != "/c" || r.Header.Get("Content-Type") != "application/json" ||
			r.Header.Get("X-Plex-Token") != "tok" || r.Header.Get("X-Plex-Product") != "Marquee" {
			http.Error(w, "bad request", 400)
			return
		}
		buf := make([]byte, 4096)
		n, _ := r.Body.Read(buf)
		body := string(buf[:n])
		switch {
		case strings.Contains(body, "allFriendsV2"):
			reply(`{"data": {"allFriendsV2": [
				{"user": {"id": "u1", "username": "one", "displayName": "One"}},
				{"user": {"id": 22, "username": "two", "displayName": ""}},
				{"user": {"id": "u1", "username": "dup"}},
				{"user": {"username": "no id"}}]}}`)(w, r)
		default: // a friend's watchlist, no pageInfo at all: one page
			reply(`{"data": {"user": {"watchlist": {"nodes": [
				{"id": "n1", "title": "A", "type": "MOVIE", "publicationYear": 1999},
				{"id": "n2", "title": "B", "type": "SHOW"},
				{"id": "n1", "title": "A again", "type": "MOVIE"}]}}}}`)(w, r)
		}
	})
	friends, err := c.Friends(bg(), authT)
	want := []PlexUser{{ID: "u1", Username: "one", DisplayName: "One"}, {ID: "22", Username: "two"}}
	if err != nil || !reflect.DeepEqual(friends, want) {
		t.Errorf("friends = %+v, %v", friends, err)
	}
	items, err := c.FriendWatchlist(bg(), authT, "u1")
	wantItems := []Item{{Key: "n1", Title: "A", Year: 1999, Type: "movie"}, {Key: "n2", Title: "B", Type: "show"}}
	if err != nil || !reflect.DeepEqual(items, wantItems) {
		t.Errorf("items = %+v, %v", items, err)
	}
}

func TestGraphQLErrorsAreErrors(t *testing.T) {
	c := clientFor(t, reply(`{"errors": [{"message": "Cannot query field allFriendsV2"}]}`))
	if _, err := c.Friends(bg(), authT); err == nil || !strings.Contains(err.Error(), "Cannot query") {
		t.Errorf("err = %v", err)
	}
	// Data next to errors is data.
	c = clientFor(t, reply(`{"data": {"allFriendsV2": [{"user": {"id": "a", "username": "A"}}]}, "errors": [{"message": "partial"}]}`))
	if f, err := c.Friends(bg(), authT); err != nil || len(f) != 1 {
		t.Errorf("partial = %+v, %v", f, err)
	}
	if _, err := clientFor(t, reply(`nonsense`)).Friends(bg(), authT); err == nil {
		t.Error("a non-JSON answer was accepted")
	}
}

func TestGuidsAndTypes(t *testing.T) {
	g := []string{"imdb://tt0133093", "TMDB://603", "tvdb://81189"}
	for scheme, want := range map[string]string{"tmdb": "603", "imdb": "tt0133093", "tvdb": "81189", "anidb": ""} {
		if got := guidValue(g, scheme); got != want {
			t.Errorf("guidValue(%q) = %q, want %q", scheme, got, want)
		}
	}
	for in, want := range map[string]string{"movie": "movie", "MOVIE": "movie", "show": "show", "TV": "show", "series": "show", "artist": "artist", " Movie ": "movie"} {
		if got := normalizeType(in); got != want {
			t.Errorf("normalizeType(%q) = %q, want %q", in, got, want)
		}
	}
	if got := withToken("https://x.test/a?b=1", Auth{Token: "t k"}, map[string][]string{"c": {"2"}}); got != "https://x.test/a?X-Plex-Token=t+k&b=1&c=2" {
		t.Errorf("withToken = %q", got)
	}
	if got := authURL("c id", "AB&C"); got != "https://app.plex.tv/auth#?clientID=c+id&code=AB%26C&context[device][product]=Marquee" {
		t.Errorf("authURL = %q", got)
	}
}

func TestNormalizeName(t *testing.T) {
	for _, tc := range []struct{ a, b string }{
		{"Marvel's Agents of S.H.I.E.L.D.", "Marvels Agents of SHIELD"},
		{"  The  Office (US) ", "the office us"},
		{"Pokémon", "POKÉMON"},
	} {
		if normalizeName(tc.a) != normalizeName(tc.b) {
			t.Errorf("%q and %q differ: %q vs %q", tc.a, tc.b, normalizeName(tc.a), normalizeName(tc.b))
		}
	}
	if normalizeName("A") == normalizeName("B") {
		t.Error("different names are the same")
	}
}
