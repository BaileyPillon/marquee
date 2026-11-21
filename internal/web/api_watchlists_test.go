package web

import (
	"net/http"
	"reflect"
	"strconv"
	"strings"
	"testing"

	"github.com/BaileyPillon/marquee/internal/watchlists"
)

func TestWatchlistRoutesAreForTheOwnerOnly(t *testing.T) {
	a := newM7App(t)
	owner := a.owner()
	member := a.member(owner, "Priya")
	admin := a.admin(owner, "Adam")
	ownerKey := a.key(owner, "see")         // no "settings" tick
	settingsKey := a.key(owner, "settings") // the tick

	routes := []struct{ method, path string }{
		{"GET", "/api/v1/watchlists"},
		{"POST", "/api/v1/watchlists/plex/link"},
		{"GET", "/api/v1/watchlists/plex/link/701"},
		{"DELETE", "/api/v1/watchlists/plex/link"},
		{"PUT", "/api/v1/watchlists/plex/friends/f1"},
		{"POST", "/api/v1/watchlists/check"},
	}
	for _, rt := range routes {
		if r := a.newClient().do(rt.method, rt.path, map[string]any{}); r.Status != http.StatusUnauthorized {
			t.Errorf("%s %s signed out = %d", rt.method, rt.path, r.Status)
		}
		for name, c := range map[string]*client{"member": member, "admin": admin} {
			if r := c.do(rt.method, rt.path, map[string]any{}); r.Status != http.StatusForbidden || r.errCode(t) != "forbidden" {
				t.Errorf("%s %s as %s = %d %s", rt.method, rt.path, name, r.Status, r.Body)
			}
		}
		if r := ownerKey.send(rt.method, rt.path, map[string]any{}); r.Status != http.StatusForbidden || r.errCode(t) != "insufficient_scope" {
			t.Errorf("%s %s with a key without the settings tick = %d %s", rt.method, rt.path, r.Status, r.Body)
		}
	}
	if r := settingsKey.send("GET", "/api/v1/watchlists", nil); r.Status != http.StatusOK {
		t.Errorf("GET with the settings tick = %d %s", r.Status, r.Body)
	}
}

func TestWatchlistsStatusBeforeLinking(t *testing.T) {
	a := newM7App(t)
	owner := a.owner()
	r := owner.send("GET", "/api/v1/watchlists", nil)
	if r.Status != http.StatusOK {
		t.Fatalf("status = %d %s", r.Status, r.Body)
	}
	plex := r.JSON(t)["plex"].(map[string]any)
	if plex["linked"] != false || plex["user"] != nil || plex["enabled"] != true || plex["includeFriends"] != true ||
		plex["autoCreateMembers"] != false || plex["quality"] != "hd" || plex["lastCheckedAt"] != nil || plex["lastError"] != "" {
		t.Errorf("plex = %v", plex)
	}
	if fr, ok := plex["friends"].([]any); !ok || len(fr) != 0 {
		t.Errorf("friends = %v, want an empty list", plex["friends"])
	}
	lr, _ := plex["lastResult"].(map[string]any)
	if lr["asked"] != float64(0) {
		t.Errorf("lastResult = %v", lr)
	}
	if sk, ok := lr["skipped"].(map[string]any); !ok || len(sk) != 0 {
		t.Errorf("skipped = %v, want an empty object", lr["skipped"])
	}
}

func TestLinkingThePlexAccountOverTheAPI(t *testing.T) {
	a := newM7App(t)
	owner := a.owner()

	r := owner.do("POST", "/api/v1/watchlists/plex/link", nil)
	if r.Status != http.StatusOK {
		t.Fatalf("start = %d %s", r.Status, r.Body)
	}
	body := r.JSON(t)
	pin := int64(body["pinId"].(float64))
	if pin == 0 || body["code"] != "WXYZ" || !strings.HasPrefix(body["authUrl"].(string), "https://app.plex.tv/auth#?clientID=marquee-") ||
		!strings.Contains(body["authUrl"].(string), "&code=WXYZ&context[device][product]=Marquee") {
		t.Errorf("link = %v", body)
	}

	pinPath := "/api/v1/watchlists/plex/link/" + strconv.FormatInt(pin, 10)
	if r := owner.send("GET", pinPath, nil); r.Status != 200 || r.JSON(t)["linked"] != false {
		t.Errorf("before the owner agrees = %d %s", r.Status, r.Body)
	}
	a.plex.mu.Lock()
	a.plex.approved[pin] = true
	a.plex.mu.Unlock()
	if r := owner.send("GET", pinPath, nil); r.Status != 200 || r.JSON(t)["linked"] != true {
		t.Errorf("after the owner agrees = %d %s", r.Status, r.Body)
	}
	plex := owner.send("GET", "/api/v1/watchlists", nil).JSON(t)["plex"].(map[string]any)
	if plex["linked"] != true || plex["user"] != "bailey-plex" {
		t.Errorf("status after linking = %v", plex)
	}

	// Bad and expired pins.
	if r := owner.send("GET", "/api/v1/watchlists/plex/link/abc", nil); r.Status != http.StatusBadRequest || r.errCode(t) != "invalid_request" {
		t.Errorf("a pin that is not a number = %d %s", r.Status, r.Body)
	}
	if r := owner.send("GET", "/api/v1/watchlists/plex/link/99999", nil); r.Status != http.StatusGone || r.errCode(t) != "pin_expired" {
		t.Errorf("an unknown pin = %d %s", r.Status, r.Body)
	}

	// Unlink.
	if r := owner.do("DELETE", "/api/v1/watchlists/plex/link", nil); r.Status != http.StatusNoContent {
		t.Errorf("unlink = %d %s", r.Status, r.Body)
	}
	if plex := owner.send("GET", "/api/v1/watchlists", nil).JSON(t)["plex"].(map[string]any); plex["linked"] != false {
		t.Errorf("still linked: %v", plex)
	}
	if r := owner.do("POST", "/api/v1/watchlists/check", nil); r.Status != http.StatusConflict || r.errCode(t) != "not_linked" {
		t.Errorf("check when not linked = %d %s", r.Status, r.Body)
	}
}

func (a *m7App) linkPlex(owner *client) {
	a.t.Helper()
	r := owner.do("POST", "/api/v1/watchlists/plex/link", nil)
	pin := int64(r.JSON(a.t)["pinId"].(float64))
	a.plex.mu.Lock()
	a.plex.approved[pin] = true
	a.plex.mu.Unlock()
	if r := owner.send("GET", "/api/v1/watchlists/plex/link/"+strconv.FormatInt(pin, 10), nil); r.JSON(a.t)["linked"] != true {
		a.t.Fatalf("link = %s", r.Body)
	}
}

func TestCheckNowAndFriendLinks(t *testing.T) {
	a := newM7App(t)
	owner := a.owner()
	member := a.member(owner, "Priya")
	memberID := userID(t, member)
	a.linkPlex(owner)

	a.plex.items = []watchlists.Item{{Key: "m1", Title: "Metropolis", Year: 1927, Type: "movie", Guids: []string{"tmdb://19"}}}
	a.plex.friends = []watchlists.PlexUser{{ID: "fr-priya", Username: "priya_p", DisplayName: "Priya"}}
	a.plex.friendWL["fr-priya"] = []watchlists.Item{{Key: "k1", Title: "Sintel", Year: 2010, Type: "movie"}}
	a.plex.guids["k1"] = []string{"tmdb://45745"}

	// The first check: the owner's movie is asked; Priya is not linked, so her item is skipped.
	r := owner.do("POST", "/api/v1/watchlists/check", nil)
	if r.Status != http.StatusOK {
		t.Fatalf("check = %d %s", r.Status, r.Body)
	}
	res := r.JSON(t)
	if res["asked"] != float64(1) || !reflect.DeepEqual(res["skipped"], map[string]any{"unlinked_friend": float64(1)}) || res["checkedAt"] == nil {
		t.Errorf("check = %v", res)
	}
	plex := owner.send("GET", "/api/v1/watchlists", nil).JSON(t)["plex"].(map[string]any)
	friends := plex["friends"].([]any)
	if len(friends) != 1 {
		t.Fatalf("friends = %v", friends)
	}
	f := friends[0].(map[string]any)
	if f["plexUserId"] != "fr-priya" || f["username"] != "priya_p" || f["linkedUserId"] != nil || f["linkedUserName"] != nil {
		t.Errorf("friend = %v", f)
	}
	lr := plex["lastResult"].(map[string]any)
	if lr["asked"] != float64(1) || plex["lastCheckedAt"] == nil {
		t.Errorf("last result = %v, checked %v", lr, plex["lastCheckedAt"])
	}

	// Link Priya to her Marquee account. Missing key, bad values and unknown things are refused.
	path := "/api/v1/watchlists/plex/friends/fr-priya"
	if r := owner.do("PUT", path, map[string]any{}); r.Status != http.StatusBadRequest || r.errCode(t) != "invalid_request" {
		t.Errorf("no userId = %d %s", r.Status, r.Body)
	}
	if r := owner.do("PUT", path, map[string]any{"userId": "five"}); r.Status != http.StatusBadRequest {
		t.Errorf("userId as text = %d %s", r.Status, r.Body)
	}
	if r := owner.do("PUT", path, map[string]any{"userId": 0}); r.Status != http.StatusBadRequest {
		t.Errorf("userId 0 = %d %s", r.Status, r.Body)
	}
	if r := owner.do("PUT", "/api/v1/watchlists/plex/friends/nobody", map[string]any{"userId": memberID}); r.Status != http.StatusNotFound || r.errCode(t) != "not_found" {
		t.Errorf("unknown friend = %d %s", r.Status, r.Body)
	}
	if r := owner.do("PUT", path, map[string]any{"userId": 9999}); r.Status != http.StatusNotFound || r.errCode(t) != "not_found" {
		t.Errorf("unknown person = %d %s", r.Status, r.Body)
	}
	r = owner.do("PUT", path, map[string]any{"userId": memberID})
	if r.Status != http.StatusOK {
		t.Fatalf("link = %d %s", r.Status, r.Body)
	}
	if got := r.JSON(t); got["plexUserId"] != "fr-priya" || got["linkedUserId"] != float64(memberID) || got["linkedUserName"] != "Priya" {
		t.Errorf("friend = %v", got)
	}

	// The next check makes Priya's ask as Priya.
	res = owner.do("POST", "/api/v1/watchlists/check", nil).JSON(t)
	if res["asked"] != float64(1) || len(res["skipped"].(map[string]any)) != 0 {
		t.Errorf("second check = %v", res)
	}
	a.asker.mu.Lock()
	calls := append([]watchlists.AskInput(nil), a.asker.calls...)
	a.asker.mu.Unlock()
	if len(calls) != 2 || calls[0].Role != "owner" || calls[1].UserID != memberID || calls[1].Role != "member" || calls[1].ProviderID != "45745" {
		t.Errorf("asks = %+v", calls)
	}

	// Unlink with null.
	r = owner.do("PUT", path, map[string]any{"userId": nil})
	if got := r.JSON(t); r.Status != http.StatusOK || got["linkedUserId"] != nil || got["linkedUserName"] != nil {
		t.Errorf("unlink = %d %s", r.Status, r.Body)
	}
}

func TestSwitchedOffWatchlistsRefuseACheck(t *testing.T) {
	a := newM7App(t)
	owner := a.owner()
	a.linkPlex(owner)
	if r := owner.do("PUT", "/api/v1/settings/watchlists", map[string]any{"plex": map[string]any{"enabled": false}}); r.Status != 200 {
		t.Fatalf("settings = %d %s", r.Status, r.Body)
	}
	if r := owner.do("POST", "/api/v1/watchlists/check", nil); r.Status != http.StatusConflict || r.errCode(t) != "disabled" {
		t.Errorf("check while off = %d %s", r.Status, r.Body)
	}
	plex := owner.send("GET", "/api/v1/watchlists", nil).JSON(t)["plex"].(map[string]any)
	if plex["enabled"] != false || plex["linked"] != true {
		t.Errorf("status = %v", plex)
	}
	// And the settings it was read from can switch it back on, with other choices.
	owner.do("PUT", "/api/v1/settings/watchlists", map[string]any{"plex": map[string]any{"enabled": true, "includeFriends": false, "autoCreateMembers": true, "quality": "uhd"}})
	plex = owner.send("GET", "/api/v1/watchlists", nil).JSON(t)["plex"].(map[string]any)
	if plex["enabled"] != true || plex["includeFriends"] != false || plex["autoCreateMembers"] != true || plex["quality"] != "uhd" {
		t.Errorf("status = %v", plex)
	}
}
