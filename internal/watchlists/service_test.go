package watchlists

import (
	"errors"
	"fmt"
	"net/url"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/BaileyPillon/marquee/internal/metadata"
)

// ---- linking (the PIN flow) ---------------------------------------------------

func TestLinkFollowsThePINFlow(t *testing.T) {
	e := newEnv(t)

	st, err := e.svc.Status(bg())
	if err != nil || st.Linked || st.User != nil || len(st.Friends) != 0 || st.LastResult.Skipped == nil {
		t.Fatalf("status before linking = %+v, %v", st, err)
	}

	l, err := e.svc.StartLink(bg())
	if err != nil {
		t.Fatal(err)
	}
	if l.PinID == 0 || l.Code != "CODE"+fmt.Sprint(l.PinID) {
		t.Errorf("link = %+v", l)
	}
	u, err := url.Parse(l.AuthURL)
	if err != nil || u.Host != "app.plex.tv" || u.Path != "/auth" {
		t.Fatalf("authUrl = %q", l.AuthURL)
	}
	// The fragment is "?clientID=..&code=..&context[device][product]=Marquee".
	for _, want := range []string{"clientID=marquee-", "code=" + l.Code, "context[device][product]=Marquee"} {
		if !strings.Contains(u.Fragment, want) {
			t.Errorf("authUrl fragment %q lacks %q", u.Fragment, want)
		}
	}

	// What Plex was told: a strong PIN from a product with a stable identifier.
	create := "POST /plextv/api/v2/pins"
	h := e.plex.headers[create]
	if h.Get("X-Plex-Product") != "Marquee" || !strings.HasPrefix(h.Get("X-Plex-Client-Identifier"), "marquee-") || h.Get("Accept") != "application/json" {
		t.Errorf("pin headers = %v", h)
	}
	if e.plex.queries[create] != "strong=true" {
		t.Errorf("pin query = %q", e.plex.queries[create])
	}
	clientID := h.Get("X-Plex-Client-Identifier")

	// Not yet.
	ok, err := e.svc.CheckLink(bg(), l.PinID, e.owner.ID)
	if err != nil || ok {
		t.Fatalf("before the owner agrees: %v, %v", ok, err)
	}
	if st, _ := e.svc.Status(bg()); st.Linked {
		t.Error("linked before the owner agreed")
	}
	if q := e.plex.queries["GET /plextv/api/v2/pins/"+fmt.Sprint(l.PinID)]; q != "code="+l.Code {
		t.Errorf("poll query = %q, want the strong pin's code", q)
	}

	e.plex.approve(l.PinID)
	ok, err = e.svc.CheckLink(bg(), l.PinID, e.owner.ID)
	if err != nil || !ok {
		t.Fatalf("after the owner agreed: %v, %v", ok, err)
	}
	// The token went to Plex to find out whose it is, and the name is kept.
	userHeaders := e.plex.headers["GET /plextv/api/v2/user"]
	if userHeaders.Get("X-Plex-Token") != e.plex.token || userHeaders.Get("X-Plex-Client-Identifier") != clientID {
		t.Errorf("account headers = %v", userHeaders)
	}
	st, _ = e.svc.Status(bg())
	if !st.Linked || st.User == nil || *st.User != "bailey-plex" {
		t.Errorf("status = %+v", st)
	}

	// The token is sealed in the database, never in the clear.
	var sealed, plexID string
	var userID int64
	if err := e.db.QueryRow(`SELECT token_sealed, plex_user_id, user_id FROM plex_account WHERE id = 1`).Scan(&sealed, &plexID, &userID); err != nil {
		t.Fatal(err)
	}
	if sealed == "" || strings.Contains(sealed, e.plex.token) || plexID != "1001" || userID != e.owner.ID {
		t.Errorf("stored account: sealed %q plex %q user %d", sealed, plexID, userID)
	}
	if opened, err := e.box.Open(sealed); err != nil || opened != e.plex.token {
		t.Errorf("sealed token opens to %q, %v", opened, err)
	}

	// Polling again after it went through still says yes (the UI may poll once more).
	if ok, err := e.svc.CheckLink(bg(), l.PinID, e.owner.ID); err != nil || !ok {
		t.Errorf("poll after success = %v, %v", ok, err)
	}

	// The identifier is stable: a second link request carries the same one, and it is in settings.
	if _, err := e.svc.StartLink(bg()); err != nil {
		t.Fatal(err)
	}
	var cs struct {
		ClientID string `json:"clientId"`
	}
	if found, err := e.cfg.Get(bg(), ClientSection, &cs); err != nil || !found || cs.ClientID != clientID {
		t.Errorf("client id in settings = %q (%v, %v), want %q", cs.ClientID, found, err, clientID)
	}

	// Unlinking forgets the account and the token.
	if err := e.svc.Unlink(bg()); err != nil {
		t.Fatal(err)
	}
	if st, _ := e.svc.Status(bg()); st.Linked {
		t.Error("still linked after Unlink")
	}
	if n := e.count(`SELECT COUNT(*) FROM plex_account`); n != 0 {
		t.Errorf("%d account rows after Unlink", n)
	}
	if _, err := e.svc.Sync(bg()); !errors.Is(err, ErrNotLinked) {
		t.Errorf("Sync after Unlink = %v, want ErrNotLinked", err)
	}
}

func TestLinkWithAnExpiredPinSaysSo(t *testing.T) {
	e := newEnv(t)
	if _, err := e.svc.CheckLink(bg(), 999999, e.owner.ID); !errors.Is(err, ErrPinExpired) {
		t.Errorf("unknown pin = %v, want ErrPinExpired", err)
	}
}

func TestLinkRefusedByPlexIsAnError(t *testing.T) {
	e := newEnv(t)
	l, _ := e.svc.StartLink(bg())
	e.plex.approve(l.PinID)
	e.plex.mu.Lock()
	e.plex.approved[l.PinID] = "a-token-plex-will-refuse"
	e.plex.mu.Unlock()
	if _, err := e.svc.CheckLink(bg(), l.PinID, e.owner.ID); !errors.Is(err, ErrPlexAuth) {
		t.Errorf("err = %v, want ErrPlexAuth", err)
	}
	if st, _ := e.svc.Status(bg()); st.Linked {
		t.Error("linked with a token Plex refused")
	}
}

// ---- the owner's watchlist -----------------------------------------------------

func TestTheOwnersWatchlistBecomesAsksAsTheOwner(t *testing.T) {
	e := newEnv(t)
	e.link()
	e.shows.byIMDb["tt0903747"] = "169"
	e.plex.ownerWL = []Item{
		movie("m1", "Metropolis", 1927, "19"),
		show("s1", "Breaking Bad", 2008, "tt0903747"),
		movie("m2", "No Ids", 2001, ""), // Plex gave no TMDB id
		{Key: "x1", Title: "Some Album", Type: "artist"},
	}
	res := e.sync()
	if res.Asked != 2 || !reflect.DeepEqual(res.Skipped, map[string]int{SkipNoTMDBID: 1, SkipUnsupportedType: 1}) {
		t.Fatalf("result = %+v", res)
	}
	calls := e.asker.asked()
	if len(calls) != 2 {
		t.Fatalf("asks = %+v", calls)
	}
	want := []AskInput{
		{UserID: e.owner.ID, Role: "owner", Kind: "movie", Provider: "tmdb", ProviderID: "19", Quality: "hd", SourceRef: "plex:1001:m1"},
		{UserID: e.owner.ID, Role: "owner", Kind: "show", Provider: "tvmaze", ProviderID: "169", Quality: "hd", SourceRef: "plex:1001:s1"},
	}
	if !reflect.DeepEqual(calls, want) {
		t.Errorf("asks =\n%+v\nwant\n%+v", calls, want)
	}

	// The tables say what happened to each item.
	type row struct {
		key, reason string
		request     int64
	}
	rows, err := e.db.Query(`SELECT item_key, skipped_reason, COALESCE(request_id, 0) FROM watchlist_items WHERE source = 'plex' AND plex_user_id = '1001' ORDER BY item_key`)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	var got []row
	for rows.Next() {
		var r row
		if err := rows.Scan(&r.key, &r.reason, &r.request); err != nil {
			t.Fatal(err)
		}
		got = append(got, r)
	}
	wantRows := []row{{"m1", "", 101}, {"m2", SkipNoTMDBID, 0}, {"s1", "", 102}, {"x1", SkipUnsupportedType, 0}}
	if !reflect.DeepEqual(got, wantRows) {
		t.Errorf("items = %+v, want %+v", got, wantRows)
	}
	var guids, typ string
	var year int
	if err := e.db.QueryRow(`SELECT guids, type, year FROM watchlist_items WHERE item_key = 'm1'`).Scan(&guids, &typ, &year); err != nil ||
		!strings.Contains(guids, "tmdb://19") || typ != "movie" || year != 1927 {
		t.Errorf("m1 = %s %s %d %v", guids, typ, year, err)
	}

	// The last result and time are kept for the owner.
	st, _ := e.svc.Status(bg())
	if st.LastCheckedAt == nil || !st.LastCheckedAt.Equal(e.now) || st.LastResult.Asked != 2 || st.LastResult.Skipped[SkipNoTMDBID] != 1 || st.LastError != "" {
		t.Errorf("status = %+v", st)
	}
}

func TestEachItemIsProcessedOnce(t *testing.T) {
	e := newEnv(t)
	e.link()
	e.plex.ownerWL = []Item{movie("m1", "Metropolis", 1927, "19"), movie("m2", "No Ids", 2001, "")}
	e.sync()
	if len(e.asker.asked()) != 1 {
		t.Fatalf("asks = %d", len(e.asker.asked()))
	}

	// Next check: nothing new, nothing asked again, nothing counted again.
	res := e.sync()
	if res.Asked != 0 || len(res.Skipped) != 0 || len(e.asker.asked()) != 1 {
		t.Errorf("second check = %+v, asks %d", res, len(e.asker.asked()))
	}
	// A new item on the list is the only one looked at.
	e.plex.ownerWL = append(e.plex.ownerWL, movie("m3", "Sintel", 2010, "45745"))
	res = e.sync()
	if res.Asked != 1 || len(e.asker.asked()) != 2 || e.asker.asked()[1].ProviderID != "45745" {
		t.Errorf("third check = %+v, asks %+v", res, e.asker.asked())
	}
	if n := e.count(`SELECT COUNT(*) FROM watchlist_items`); n != 3 {
		t.Errorf("%d item rows, want 3", n)
	}
}

func TestTheOwnersWatchlistIsReadPageByPage(t *testing.T) {
	e := newEnv(t)
	e.link()
	for i := 0; i < 120; i++ {
		e.plex.ownerWL = append(e.plex.ownerWL, movie(fmt.Sprintf("p%d", i), fmt.Sprintf("Film %d", i), 2000, fmt.Sprint(1000+i)))
	}
	res := e.sync()
	if res.Asked != 120 {
		t.Errorf("asked %d of 120 over several pages", res.Asked)
	}
	if n := e.plex.count("GET /discover/library/sections/watchlist/all"); n != 3 {
		t.Errorf("%d watchlist pages read, want 3", n)
	}
	// Paged with the container headers' query form, and with ids.
	q, _ := url.ParseQuery(e.plex.queries["GET /discover/library/sections/watchlist/all"])
	if q.Get("includeGuids") != "1" || q.Get("X-Plex-Token") != e.plex.token || q.Get("X-Plex-Container-Size") == "" {
		t.Errorf("watchlist query = %v", q)
	}
}

func TestMoviesWaitForATMDBKey(t *testing.T) {
	e := newEnv(t)
	e.link()
	e.setKey(false)
	e.plex.ownerWL = []Item{movie("m1", "Metropolis", 1927, "19"), show("s1", "Pioneer One", 2010, "tt1")}
	e.shows.byIMDb["tt1"] = "99001"
	res := e.sync()
	if res.Asked != 1 || res.Skipped[SkipTMDBKeyMissing] != 1 {
		t.Fatalf("without a key: %+v", res) // the show does not need the key
	}
	if len(e.asker.asked()) != 1 || e.asker.asked()[0].Kind != "show" {
		t.Errorf("asks = %+v", e.asker.asked())
	}
	// The owner adds the key: the movie is asked at the next check, once.
	e.setKey(true)
	res = e.sync()
	if res.Asked != 1 || len(res.Skipped) != 0 || e.asker.asked()[1].ProviderID != "19" {
		t.Errorf("with the key: %+v, asks %+v", res, e.asker.asked())
	}
	if res = e.sync(); res.Asked != 0 {
		t.Errorf("a third check asked again: %+v", res)
	}
}

func TestShowsAreFoundByIMDbThenByName(t *testing.T) {
	e := newEnv(t)
	e.link()
	e.shows.byIMDb["tt0001"] = "101"
	e.shows.byName["Pioneer One"] = []metadata.SearchHit{
		{ProviderID: "7", Name: "Pioneer One Reborn", Year: 2010},
		{ProviderID: "99001", Name: "Pioneer One", Year: 2010},
	}
	e.shows.byName["Marvel's Agents of S.H.I.E.L.D."] = []metadata.SearchHit{{ProviderID: "31", Name: "Marvels Agents of SHIELD", Year: 2013}}
	e.shows.byName["Remake"] = []metadata.SearchHit{{ProviderID: "40", Name: "Remake", Year: 1971}}
	e.plex.ownerWL = []Item{
		show("a", "Anything", 2000, "tt0001"),                  // by IMDb id
		show("b", "Pioneer One", 2010, "tt-unknown"),           // IMDb id unknown: by name
		show("c", "Pioneer One", 2010, ""),                     // no IMDb id: by name
		show("d", "Marvel's Agents of S.H.I.E.L.D.", 2013, ""), // punctuation does not matter
		show("e", "Remake", 2019, ""),                          // right name, wrong year
		show("f", "Never Heard Of It", 2019, ""),               // no hit at all
	}
	res := e.sync()
	if res.Asked != 4 || res.Skipped[SkipNoMatch] != 2 {
		t.Fatalf("result = %+v", res)
	}
	var ids []string
	for _, c := range e.asker.asked() {
		ids = append(ids, c.ProviderID)
	}
	if want := []string{"101", "99001", "99001", "31"}; !reflect.DeepEqual(ids, want) {
		t.Errorf("tvmaze ids = %v, want %v", ids, want)
	}
}

func TestAProviderThatDoesNotAnswerIsTriedAgainNextTime(t *testing.T) {
	e := newEnv(t)
	e.link()
	e.shows.err = &metadata.Error{Code: metadata.ErrUpstream.Code, Message: "down"}
	e.plex.ownerWL = []Item{show("a", "Pioneer One", 2010, "tt0001")}
	res := e.sync()
	if res.Asked != 0 || res.Skipped[SkipLookupFailed] != 1 {
		t.Fatalf("result = %+v", res)
	}
	e.shows.err = nil
	e.shows.byIMDb["tt0001"] = "99001"
	if res = e.sync(); res.Asked != 1 {
		t.Errorf("after TVmaze came back: %+v", res)
	}
}

// ---- limits and the answers asking gives ----------------------------------------

func TestLimitsApplyAndAPassingSkipIsRetried(t *testing.T) {
	e := newEnv(t)
	e.link()
	var blocked sync.Map
	blocked.Store("19", true)
	e.asker.errFor = func(in AskInput) error {
		if _, on := blocked.Load(in.ProviderID); on {
			return fmt.Errorf("ask: %w", ErrLimitReached)
		}
		return nil
	}
	e.plex.ownerWL = []Item{movie("m1", "Metropolis", 1927, "19"), movie("m2", "Sintel", 2010, "45745")}
	res := e.sync()
	if res.Asked != 1 || res.Skipped[SkipLimitReached] != 1 {
		t.Fatalf("with the limit used up: %+v", res)
	}
	// Still blocked: tried again, counted again, not asked.
	res = e.sync()
	if res.Asked != 0 || res.Skipped[SkipLimitReached] != 1 {
		t.Fatalf("still blocked: %+v", res)
	}
	// A place frees up.
	blocked.Delete("19")
	res = e.sync()
	if res.Asked != 1 || len(res.Skipped) != 0 {
		t.Fatalf("after the limit freed up: %+v", res)
	}
	if n := e.count(`SELECT COUNT(*) FROM watchlist_items WHERE request_id IS NOT NULL`); n != 2 {
		t.Errorf("%d items with an ask, want 2", n)
	}
}

func TestAnswersFromAskingBecomeSkipReasons(t *testing.T) {
	e := newEnv(t)
	e.link()
	e.asker.errFor = func(in AskInput) error {
		switch in.ProviderID {
		case "1":
			return ErrAlreadyAsked
		case "2":
			return ErrNoFolder
		case "3":
			return metadata.ErrTMDBKeyInvalid
		case "4":
			return metadata.ErrNotFound
		case "5":
			return errors.New("the catalog fell over")
		}
		return nil
	}
	for i := 1; i <= 5; i++ {
		e.plex.ownerWL = append(e.plex.ownerWL, movie(fmt.Sprintf("m%d", i), fmt.Sprintf("Film %d", i), 2000, fmt.Sprint(i)))
	}
	res := e.sync()
	want := map[string]int{SkipAlreadyAsked: 1, SkipNoLibraryFolder: 1, SkipTMDBKeyMissing: 1, SkipNotFound: 1, SkipAskFailed: 1}
	if res.Asked != 0 || !reflect.DeepEqual(res.Skipped, want) {
		t.Fatalf("result = %+v", res)
	}
	// "Already asked" and "not found" are final; the others pass and come back.
	e.asker.mu.Lock()
	e.asker.calls = nil
	e.asker.mu.Unlock()
	e.sync()
	var again []string
	for _, c := range e.asker.asked() {
		again = append(again, c.ProviderID)
	}
	if want := []string{"2", "3", "5"}; !reflect.DeepEqual(again, want) {
		t.Errorf("asked again for %v, want %v", again, want)
	}
}

// ---- friends -------------------------------------------------------------------

func friendSetup(e *env) (priya, omar PlexUser) {
	priya = PlexUser{ID: "fr-priya", Username: "priya_p", DisplayName: "Priya"}
	omar = PlexUser{ID: "fr-omar", Username: "omar77", DisplayName: "Omar"}
	e.plex.friends = []PlexUser{priya, omar}
	e.plex.friendWL[priya.ID] = []Item{
		{Key: "k1", Title: "Metropolis", Year: 1927, Type: "movie"},
		{Key: "k2", Title: "Pioneer One", Year: 2010, Type: "show"},
	}
	e.plex.friendWL[omar.ID] = []Item{{Key: "k3", Title: "Sintel", Year: 2010, Type: "movie"}}
	e.plex.guids["k1"] = []string{"tmdb://19", "imdb://tt0017136"}
	e.plex.guids["k2"] = []string{"imdb://tt1111111", "tvdb://81"}
	e.plex.guids["k3"] = []string{"tmdb://45745"}
	e.shows.byIMDb["tt1111111"] = "99001"
	return priya, omar
}

func TestAFriendsItemsBecomeAsksAsTheirLinkedPerson(t *testing.T) {
	e := newEnv(t)
	e.link()
	priya, omar := friendSetup(e)
	member := e.member("Priya")

	// Before anyone is linked, the friends are known but nothing is asked, and the
	// items are counted as skipped.
	res := e.sync()
	if res.Asked != 0 || res.Skipped[SkipUnlinkedFriend] != 3 {
		t.Fatalf("nobody linked: %+v", res)
	}
	st, _ := e.svc.Status(bg())
	if len(st.Friends) != 2 || st.Friends[0].Username != "omar77" || st.Friends[1].Username != "priya_p" ||
		st.Friends[0].LinkedUserID != nil || st.Friends[1].LinkedUserName != nil {
		t.Fatalf("friends = %+v", st.Friends)
	}
	if n := e.count(`SELECT COUNT(*) FROM watchlist_items WHERE plex_user_id IN ('fr-priya', 'fr-omar')`); n != 0 {
		t.Errorf("%d rows kept for unlinked friends' items", n)
	}

	// Link Priya.
	if err := e.svc.SetFriendLink(bg(), priya.ID, &member.ID); err != nil {
		t.Fatal(err)
	}
	st, _ = e.svc.Status(bg())
	var linked Friend
	for _, f := range st.Friends {
		if f.PlexUserID == priya.ID {
			linked = f
		}
	}
	if linked.LinkedUserID == nil || *linked.LinkedUserID != member.ID || linked.LinkedUserName == nil || *linked.LinkedUserName != "Priya" {
		t.Fatalf("linked friend = %+v", linked)
	}

	res = e.sync()
	if res.Asked != 2 || res.Skipped[SkipUnlinkedFriend] != 1 {
		t.Fatalf("Priya linked: %+v", res)
	}
	var priyaAsks []AskInput
	for _, c := range e.asker.asked() {
		priyaAsks = append(priyaAsks, c)
	}
	want := []AskInput{
		{UserID: member.ID, Role: "member", Kind: "movie", Provider: "tmdb", ProviderID: "19", Quality: "hd", SourceRef: "plex:fr-priya:k1"},
		{UserID: member.ID, Role: "member", Kind: "show", Provider: "tvmaze", ProviderID: "99001", Quality: "hd", SourceRef: "plex:fr-priya:k2"},
	}
	if !reflect.DeepEqual(priyaAsks, want) {
		t.Errorf("asks =\n%+v\nwant\n%+v", priyaAsks, want)
	}
	// The ids of a friend's items were read from the discover service, with the token.
	if n := e.plex.count("GET /discover/library/metadata/"); n != 2 {
		t.Errorf("%d item lookups, want 2", n)
	}
	if q := e.plex.queries["GET /discover/library/metadata/k1"]; !strings.Contains(q, "includeGuids=1") || !strings.Contains(q, "X-Plex-Token=") {
		t.Errorf("item query = %q", q)
	}

	// Next check: Priya's items are not asked again and not looked up again. Omar's wait.
	res = e.sync()
	if res.Asked != 0 || res.Skipped[SkipUnlinkedFriend] != 1 || e.plex.count("GET /discover/library/metadata/") != 2 || len(e.asker.asked()) != 2 {
		t.Errorf("second check = %+v", res)
	}

	// Link Omar: his one item is asked now, as him.
	omarUser := e.member("Omar")
	if err := e.svc.SetFriendLink(bg(), omar.ID, &omarUser.ID); err != nil {
		t.Fatal(err)
	}
	res = e.sync()
	if res.Asked != 1 || len(res.Skipped) != 0 {
		t.Fatalf("Omar linked: %+v", res)
	}
	if last := e.asker.asked()[2]; last.UserID != omarUser.ID || last.ProviderID != "45745" || last.SourceRef != "plex:fr-omar:k3" {
		t.Errorf("Omar's ask = %+v", last)
	}

	// Unlinking Priya stops her future items; the ones asked stay asked.
	if err := e.svc.SetFriendLink(bg(), priya.ID, nil); err != nil {
		t.Fatal(err)
	}
	e.plex.friendWL[priya.ID] = append(e.plex.friendWL[priya.ID], Item{Key: "k4", Title: "Sintel", Type: "movie"})
	res = e.sync()
	if res.Asked != 0 || res.Skipped[SkipUnlinkedFriend] != 3 {
		t.Errorf("after unlinking: %+v", res)
	}
}

func TestLinkingErrors(t *testing.T) {
	e := newEnv(t)
	e.link()
	priya, omar := friendSetup(e)
	e.sync() // learns the friends
	m := e.member("Priya")

	if err := e.svc.SetFriendLink(bg(), "nobody", &m.ID); !errors.Is(err, ErrUnknownFriend) {
		t.Errorf("unknown friend = %v", err)
	}
	if err := e.svc.SetFriendLink(bg(), priya.ID, ptr(int64(9999))); !errors.Is(err, ErrUserNotFound) {
		t.Errorf("unknown person = %v", err)
	}
	if err := e.svc.SetFriendLink(bg(), priya.ID, &m.ID); err != nil {
		t.Fatal(err)
	}
	if err := e.svc.SetFriendLink(bg(), omar.ID, &m.ID); !errors.Is(err, ErrUserAlreadyLinked) {
		t.Errorf("one person for two friends = %v", err)
	}
	// Moving a friend to another person replaces the link.
	other := e.member("Priya Two")
	if err := e.svc.SetFriendLink(bg(), priya.ID, &other.ID); err != nil {
		t.Fatal(err)
	}
	if n := e.count(`SELECT COUNT(*) FROM plex_links WHERE plex_user_id = 'fr-priya' AND user_id = ?`, other.ID); n != 1 {
		t.Error("the link did not move")
	}
	// A person who is removed takes the link with them (cascade), so the friend is unlinked.
	if _, err := e.db.Exec(`DELETE FROM users WHERE id = ?`, other.ID); err != nil {
		t.Fatal(err)
	}
	st, _ := e.svc.Status(bg())
	for _, f := range st.Friends {
		if f.PlexUserID == priya.ID && f.LinkedUserID != nil {
			t.Errorf("a deleted person is still linked: %+v", f)
		}
	}
}

func ptr[T any](v T) *T { return &v }

func TestAutoCreateMembersMakesAPersonForAnUnlinkedFriend(t *testing.T) {
	e := newEnv(t)
	e.link()
	priya, omar := friendSetup(e)
	e.member("Priya") // the name is taken already
	e.setConfig(Config{Plex: PlexConfig{Enabled: true, IncludeFriends: true, AutoCreateMembers: true, Quality: "hd"}})

	res := e.sync()
	if res.Asked != 3 || len(res.Skipped) != 0 {
		t.Fatalf("result = %+v", res)
	}
	// Priya's name was taken, so the new person is "Priya (Plex)"; Omar is "Omar".
	names := map[string]string{}
	rows, err := e.db.Query(`SELECT l.plex_user_id, u.name || '/' || u.role FROM plex_links l JOIN users u ON u.id = l.user_id`)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	for rows.Next() {
		var id, name string
		if err := rows.Scan(&id, &name); err != nil {
			t.Fatal(err)
		}
		names[id] = name
	}
	if names[priya.ID] != "Priya (Plex)/member" || names[omar.ID] != "Omar/member" {
		t.Errorf("created members = %v", names)
	}
	// The asks were made as them.
	byRef := map[string]AskInput{}
	for _, c := range e.asker.asked() {
		byRef[c.SourceRef] = c
	}
	if byRef["plex:fr-omar:k3"].Role != "member" || byRef["plex:fr-omar:k3"].UserID == 0 {
		t.Errorf("Omar's ask = %+v", byRef["plex:fr-omar:k3"])
	}

	// Switched off (the default), a friend who is not linked gets no person.
	e2 := newEnv(t)
	e2.link()
	friendSetup(e2)
	e2.sync()
	if n := e2.count(`SELECT COUNT(*) FROM plex_links`); n != 0 {
		t.Errorf("%d links made with autoCreateMembers off", n)
	}
}

func TestFriendsAreLeftOutWhenTheOwnerSaysSo(t *testing.T) {
	e := newEnv(t)
	e.link()
	friendSetup(e)
	e.plex.ownerWL = []Item{movie("m1", "Metropolis", 1927, "19")}
	e.setConfig(Config{Plex: PlexConfig{Enabled: true, IncludeFriends: false, Quality: "uhd"}})
	res := e.sync()
	if res.Asked != 1 || len(res.Skipped) != 0 || e.plex.count("POST /community") != 0 {
		t.Errorf("result = %+v, community calls %d", res, e.plex.count("POST /community"))
	}
	if c := e.asker.asked()[0]; c.Quality != "uhd" {
		t.Errorf("quality = %q, want the configured uhd", c.Quality)
	}
}

func TestAFriendWatchlistIsReadPageByPage(t *testing.T) {
	e := newEnv(t)
	e.link()
	friend := PlexUser{ID: "fr-big", Username: "big"}
	e.plex.friends = []PlexUser{friend}
	for i := 0; i < 120; i++ {
		key := fmt.Sprintf("b%d", i)
		e.plex.friendWL[friend.ID] = append(e.plex.friendWL[friend.ID], Item{Key: key, Title: key, Type: "movie"})
		e.plex.guids[key] = []string{"tmdb://" + fmt.Sprint(5000+i)}
	}
	m := e.member("Big")
	e.sync() // learn the friend
	if err := e.svc.SetFriendLink(bg(), friend.ID, &m.ID); err != nil {
		t.Fatal(err)
	}
	if res := e.sync(); res.Asked != 120 {
		t.Errorf("asked %d of 120", res.Asked)
	}
}

// ---- failures, the job, settings -----------------------------------------------

func TestPlexRefusingTheTokenIsReportedToTheOwner(t *testing.T) {
	e := newEnv(t)
	e.link()
	e.plex.ownerWL = []Item{movie("m1", "Metropolis", 1927, "19")}
	e.plex.mu.Lock()
	e.plex.token = "a-different-token" // the owner revoked ours
	e.plex.mu.Unlock()
	if _, err := e.svc.Sync(bg()); !errors.Is(err, ErrPlexAuth) {
		t.Fatalf("err = %v, want ErrPlexAuth", err)
	}
	st, _ := e.svc.Status(bg())
	if !strings.Contains(st.LastError, "Link the account again") || st.LastCheckedAt == nil {
		t.Errorf("status = %+v", st)
	}
	if e.plex.count("POST /community") != 0 {
		t.Error("friends were tried with a token Plex refused")
	}
	if len(e.asker.asked()) != 0 {
		t.Error("asked with a refused token")
	}
}

func TestAFriendWhoCannotBeReadDoesNotStopTheOthers(t *testing.T) {
	e := newEnv(t)
	e.link()
	priya, omar := friendSetup(e)
	_ = priya
	e.plex.friendWL[priya.ID] = nil
	m := e.member("Omar")
	e.sync()
	if err := e.svc.SetFriendLink(bg(), omar.ID, &m.ID); err != nil {
		t.Fatal(err)
	}
	// Omar's item ids cannot be read once, then can.
	delete(e.plex.guids, "k3")
	res := e.sync()
	if res.Asked != 0 || res.Skipped[SkipLookupFailed] != 1 {
		t.Fatalf("result = %+v", res)
	}
	e.plex.guids["k3"] = []string{"tmdb://45745"}
	if res = e.sync(); res.Asked != 1 {
		t.Errorf("after the ids came back: %+v", res)
	}
}

func TestSyncJobDoesNothingWhenNotLinkedOrSwitchedOff(t *testing.T) {
	e := newEnv(t)
	if err := e.svc.SyncJob(bg()); err != nil {
		t.Errorf("not linked: %v", err)
	}
	e.link()
	e.plex.ownerWL = []Item{movie("m1", "Metropolis", 1927, "19")}
	e.setConfig(Config{Plex: PlexConfig{Enabled: false, Quality: "hd"}})
	if err := e.svc.SyncJob(bg()); err != nil || len(e.asker.asked()) != 0 {
		t.Errorf("switched off: %v, asks %d", err, len(e.asker.asked()))
	}
	if _, err := e.svc.Sync(bg()); !errors.Is(err, ErrDisabled) {
		t.Errorf("Sync when switched off = %v", err)
	}
	e.setConfig(Config{Plex: PlexConfig{Enabled: true, Quality: "hd"}})
	if err := e.svc.SyncJob(bg()); err != nil || len(e.asker.asked()) != 1 {
		t.Errorf("switched on: %v, asks %d", err, len(e.asker.asked()))
	}
}

func TestOnlyOneCheckRunsAtATime(t *testing.T) {
	e := newEnv(t)
	e.link()
	e.plex.ownerWL = []Item{movie("m1", "Metropolis", 1927, "19")}
	e.plex.hold = make(chan struct{})
	e.plex.entered = make(chan struct{}, 1)
	done := make(chan error, 1)
	go func() { _, err := e.svc.Sync(bg()); done <- err }()
	select {
	case <-e.plex.entered:
	case <-time.After(5 * time.Second):
		t.Fatal("the first check never reached Plex")
	}
	if _, err := e.svc.Sync(bg()); !errors.Is(err, ErrRunning) {
		t.Errorf("a second check while one runs = %v, want ErrRunning", err)
	}
	if err := e.svc.SyncJob(bg()); err != nil {
		t.Errorf("the job while a check runs = %v, want nil", err)
	}
	close(e.plex.hold)
	if err := <-done; err != nil {
		t.Errorf("first check: %v", err)
	}
}

func TestConfigDefaultsAndOverrides(t *testing.T) {
	e := newEnv(t)
	c, err := LoadConfig(bg(), e.cfg)
	if err != nil || !reflect.DeepEqual(c, Config{Plex: PlexConfig{Enabled: true, IncludeFriends: true, AutoCreateMembers: false, Quality: "hd"}}) {
		t.Fatalf("defaults = %+v, %v", c, err)
	}
	// A section that sets only some fields keeps the defaults for the rest.
	if err := e.cfg.Set(bg(), SettingsSection, map[string]any{"plex": map[string]any{"includeFriends": false, "quality": "8k"}}); err != nil {
		t.Fatal(err)
	}
	c, _ = LoadConfig(bg(), e.cfg)
	if !c.Plex.Enabled || c.Plex.IncludeFriends || c.Plex.Quality != "hd" {
		t.Errorf("partial = %+v (an unknown quality is hd)", c)
	}
	if err := e.cfg.Set(bg(), SettingsSection, map[string]any{"plex": map[string]any{"quality": "uhd", "autoCreateMembers": true}}); err != nil {
		t.Fatal(err)
	}
	if c, _ = LoadConfig(bg(), e.cfg); c.Plex.Quality != "uhd" || !c.Plex.AutoCreateMembers {
		t.Errorf("overrides = %+v", c)
	}
}

func TestAnItemWithoutAnIDIsIgnored(t *testing.T) {
	e := newEnv(t)
	e.link()
	e.plex.ownerWL = []Item{{Title: "No key", Type: "movie", Guids: []string{"tmdb://1"}}, movie("m1", "Metropolis", 1927, "19")}
	if res := e.sync(); res.Asked != 1 {
		t.Errorf("result = %+v", res)
	}
}
