package mcp

import (
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/BaileyPillon/marquee/internal/accounts"
	"github.com/BaileyPillon/marquee/internal/requests"
)

// askRig is a rig with a member's key that may see and ask.
type askRig struct {
	*rig
	key   string
	keyID int64
}

func newAskRig(t *testing.T, opts ...rigOpt) *askRig {
	t.Helper()
	r := newRig(t, opts...)
	k, secret, err := r.acc.CreateAPIKey(bg(), r.mia, "claude", []string{accounts.ScopeSee, accounts.ScopeAsk})
	if err != nil {
		t.Fatal(err)
	}
	return &askRig{rig: r, key: secret, keyID: k.ID}
}

func (r *rig) countRows(query string, args ...any) int {
	r.t.Helper()
	var n int
	if err := r.db.QueryRow(query, args...).Scan(&n); err != nil {
		r.t.Fatal(err)
	}
	return n
}

func (a *askRig) requestCount() int { return a.countRows(`SELECT COUNT(*) FROM requests`) }

func movieAsk(id any, quality string) map[string]any {
	return map[string]any{"kind": "movie", "providerId": fmt.Sprint(id), "quality": quality}
}

func with(m map[string]any, kv ...any) map[string]any {
	out := map[string]any{}
	for k, v := range m {
		out[k] = v
	}
	for i := 0; i+1 < len(kv); i += 2 {
		out[kv[i].(string)] = kv[i+1]
	}
	return out
}

func (a *askRig) plan(args map[string]any) toolResult {
	a.t.Helper()
	out := a.mustCall(a.key, ToolAskForTitle, args)
	if out.Structured["step"] != "plan" {
		a.t.Fatalf("the first call returned step %v: %v", out.Structured["step"], out.Structured)
	}
	return out
}

func TestAskForTitleTwoStepAsTheKeyUser(t *testing.T) {
	a := newAskRig(t)

	// Step one: a plan. Nothing changes.
	args := movieAsk(20, "hd") // Nosferatu, not in the catalog yet
	out := a.plan(args)
	s := out.Structured
	plan := s["plan"].(map[string]any)
	title := plan["title"].(map[string]any)
	token, _ := s["confirmToken"].(string)
	if s["canAsk"] != true || !strings.HasPrefix(token, "cf_") || len(token) < 20 || s["expiresAt"] != t0.Add(5*time.Minute).Format(time.RFC3339) {
		t.Fatalf("plan = %v", s)
	}
	if title["name"] != "Nosferatu" || title["year"] != float64(1922) || title["kind"] != "movie" || title["inCatalog"] != false || title["titleId"] != nil {
		t.Errorf("plan title = %v", title)
	}
	if plan["quality"] != "hd" || plan["needsOwnerApproval"] != false || plan["hourlyAsksLeft"] != float64(5) {
		t.Errorf("plan = %v", plan)
	}
	hd := plan["usage"].(map[string]any)["hd"].(map[string]any)
	if hd["used"] != float64(0) || hd["limit"] != float64(10) {
		t.Errorf("plan usage = %v", plan["usage"])
	}
	for _, want := range []string{`"Nosferatu" (1922) (movie) in HD`, "approved straight away", "0 of 10 HD asks", token, "Nothing has been asked yet", "only if they agree"} {
		if !strings.Contains(out.Text, want) {
			t.Errorf("plan text lacks %q:\n%s", want, out.Text)
		}
	}
	if a.requestCount() != 0 || a.countRows(`SELECT COUNT(*) FROM titles`) != 0 {
		t.Fatalf("planning changed something: %d requests, %d titles", a.requestCount(), a.countRows(`SELECT COUNT(*) FROM titles`))
	}

	// Step two: the same inputs and the token place the ask, as the key's person.
	done := a.mustCall(a.key, ToolAskForTitle, with(args, "confirmToken", token))
	d := done.Structured
	req := d["request"].(map[string]any)
	if d["step"] != "done" || d["autoApproved"] != true || req["state"] != "approved" || req["quality"] != "hd" || req["title"] != "Nosferatu" || req["kind"] != "movie" {
		t.Fatalf("done = %v", d)
	}
	if !strings.Contains(done.Text, "Asked for") || !strings.Contains(done.Text, "approved straight away") {
		t.Errorf("done text = %q", done.Text)
	}
	if d["usage"].(map[string]any)["hd"].(map[string]any)["used"] != float64(1) {
		t.Errorf("usage after = %v", d["usage"])
	}
	var userID, titleID int64
	var source, ref, state string
	if err := a.db.QueryRow(`SELECT user_id, title_id, source, source_ref, state FROM requests`).Scan(&userID, &titleID, &source, &ref, &state); err != nil {
		t.Fatal(err)
	}
	if userID != a.mia.ID || source != "mcp" || state != "approved" || ref != fmt.Sprintf("mcp-key-%d", a.keyID) {
		t.Errorf("request row = user %d source %q ref %q state %q", userID, source, ref, state)
	}
	if a.countRows(`SELECT COUNT(*) FROM titles WHERE tmdb_id = 20 AND kind = 'movie'`) != 1 || a.countRows(`SELECT COUNT(*) FROM copies WHERE title_id = ? AND quality = 'hd'`, titleID) != 1 {
		t.Error("the title and its copy were not created by the ask")
	}
	// It counts against the person's own weekly limit.
	if lim, _ := a.req.MyLimits(bg(), requests.Actor{UserID: a.mia.ID, Role: "member"}); lim.Usage.HD.Used != 1 {
		t.Errorf("weekly usage = %+v", lim.Usage)
	}

	// The same person sees it in my_asks.
	if my := a.mustCall(a.key, ToolMyAsks, nil); my.Structured["total"] != float64(1) {
		t.Errorf("my_asks = %v", my.Structured)
	}
}

func TestAskFor4KWaitsForTheOwner(t *testing.T) {
	a := newAskRig(t)
	// A title already in the catalog, asked for by its titleId.
	metropolis := a.addTitle("movie", "tmdb", "19")

	out := a.plan(map[string]any{"titleId": metropolis.ID, "quality": "uhd", "note": "for the big screen"})
	plan := out.Structured["plan"].(map[string]any)
	if plan["needsOwnerApproval"] != true || plan["title"].(map[string]any)["inCatalog"] != true || plan["title"].(map[string]any)["titleId"] != float64(metropolis.ID) {
		t.Errorf("plan = %v", plan)
	}
	if !strings.Contains(out.Text, "wait for the owner's yes") || !strings.Contains(out.Text, "4K") {
		t.Errorf("text = %q", out.Text)
	}
	done := a.mustCall(a.key, ToolAskForTitle, map[string]any{
		"titleId": metropolis.ID, "quality": "uhd", "note": "for the big screen", "confirmToken": out.Structured["confirmToken"]})
	req := done.Structured["request"].(map[string]any)
	if done.Structured["autoApproved"] != false || req["state"] != "pending" || req["note"] != "for the big screen" || req["quality"] != "uhd" {
		t.Errorf("done = %v", done.Structured)
	}
	if !strings.Contains(done.Text, "waiting for the owner's yes") {
		t.Errorf("text = %q", done.Text)
	}
	// Claude has no way to answer it: it is still pending, and only the owner's own API can approve.
	if a.countRows(`SELECT COUNT(*) FROM requests WHERE state = 'pending'`) != 1 || a.countRows(`SELECT COUNT(*) FROM copies`) != 0 {
		t.Error("the 4K ask should be waiting, with no copy yet")
	}
}

func TestAskAsAShowWithSeasons(t *testing.T) {
	a := newAskRig(t)
	args := map[string]any{"kind": "show", "provider": "tvmaze", "providerId": 99001, "quality": "HD", "seasons": []any{2, "1", 1}}
	out := a.plan(args)
	if got := out.Structured["plan"].(map[string]any)["scope"]; got != "seasons 1, 2" {
		t.Errorf("scope = %v", got)
	}
	done := a.mustCall(a.key, ToolAskForTitle, with(args, "confirmToken", out.Structured["confirmToken"]))
	if done.Structured["request"].(map[string]any)["scope"] != "seasons 1, 2" {
		t.Errorf("request = %v", done.Structured["request"])
	}
	// A show with no seasons named is asked for whole.
	out = a.plan(map[string]any{"kind": "show", "providerId": "99002", "quality": "hd"})
	if out.Structured["plan"].(map[string]any)["scope"] != "all seasons" {
		t.Errorf("plan = %v", out.Structured["plan"])
	}
}

func TestAskAsTheOwner(t *testing.T) {
	r := newRig(t)
	key := r.key(r.owner, accounts.ScopeAsk, accounts.ScopeApprove, accounts.ScopeAdmin)
	out := r.mustCall(key, ToolAskForTitle, movieAsk(31, "uhd"))
	plan := out.Structured["plan"].(map[string]any)
	if plan["needsOwnerApproval"] != false {
		t.Errorf("the owner's own 4K ask should go straight through: %v", plan)
	}
	done := r.mustCall(key, ToolAskForTitle, with(movieAsk(31, "uhd"), "confirmToken", out.Structured["confirmToken"]))
	if done.Structured["request"].(map[string]any)["state"] != "approved" {
		t.Errorf("done = %v", done.Structured)
	}
	// Even with the approve tick, no tool answers anyone's ask.
	if names := r.toolNames(key); len(names) != 1 || names[0] != ToolAskForTitle {
		t.Errorf("the owner's approve+admin+ask key lists %v", names)
	}
}

func TestConfirmTokenIsSingleUse(t *testing.T) {
	a := newAskRig(t)
	args := movieAsk(20, "hd")
	token := a.plan(args).Structured["confirmToken"]

	a.mustCall(a.key, ToolAskForTitle, with(args, "confirmToken", token))
	if a.requestCount() != 1 {
		t.Fatalf("%d requests", a.requestCount())
	}
	again := a.call(a.key, ToolAskForTitle, with(args, "confirmToken", token))
	if !again.IsError || !strings.Contains(again.Text, "already used") || !strings.Contains(again.Text, "no second ask") {
		t.Errorf("reusing the token: %+v", again)
	}
	if a.requestCount() != 1 {
		t.Errorf("the reused token placed another ask: %d requests", a.requestCount())
	}
	// A new plan after that is refused with the reason (it is already asked for) and gets no token.
	out := a.plan(args)
	plan := out.Structured["plan"].(map[string]any)
	if out.Structured["canAsk"] != false || out.Structured["confirmToken"] != nil || plan["existingAsk"].(map[string]any)["yours"] != true ||
		!strings.Contains(out.Text, "You already asked for") {
		t.Errorf("planning an ask that exists: %v / %q", out.Structured, out.Text)
	}
}

func TestConfirmTokenIsBoundToItsInputs(t *testing.T) {
	a := newAskRig(t)
	args := movieAsk(20, "hd")
	token := a.plan(args).Structured["confirmToken"]

	for name, changed := range map[string]map[string]any{
		"another quality": with(args, "quality", "uhd"),
		"another title":   movieAsk(31, "hd"),
		"another note":    with(args, "note", "sneaky"),
		"seasons added":   with(args, "seasons", []any{1}),
		"by titleId":      {"titleId": 1, "quality": "hd"},
	} {
		out := a.call(a.key, ToolAskForTitle, with(changed, "confirmToken", token))
		if !out.IsError {
			t.Errorf("%s: the token was accepted: %v", name, out.Structured)
			continue
		}
		if name != "by titleId" && name != "seasons added" && !strings.Contains(out.Text, "different ask") {
			t.Errorf("%s: message = %q", name, out.Text)
		}
	}
	if a.requestCount() != 0 {
		t.Fatalf("a refused confirm placed %d asks", a.requestCount())
	}
	// Refusing it did not spend it: the right inputs still work.
	if out := a.call(a.key, ToolAskForTitle, with(args, "confirmToken", token)); out.IsError {
		t.Errorf("the token with its own inputs: %s", out.Text)
	}
	// The same inputs written another way (a number for the id, a different case) are still the same ask.
	token = a.plan(movieAsk(31, "uhd")).Structured["confirmToken"]
	if out := a.call(a.key, ToolAskForTitle, map[string]any{"kind": "MOVIE", "provider": "TMDB", "providerId": 31, "quality": "4K", "confirmToken": token}); out.IsError {
		t.Errorf("the same ask spelled differently: %s", out.Text)
	}
}

func TestConfirmTokenIsBoundToTheKey(t *testing.T) {
	a := newAskRig(t)
	args := movieAsk(20, "hd")
	token := a.plan(args).Structured["confirmToken"]

	// Another key of the same person, and a key of another person, cannot use it.
	sibling := a.rig.key(a.mia, accounts.ScopeSee, accounts.ScopeAsk)
	noah := a.rig.key(a.noah, accounts.ScopeSee, accounts.ScopeAsk)
	for name, key := range map[string]string{"another key of the same person": sibling, "another person's key": noah} {
		out := a.call(key, ToolAskForTitle, with(args, "confirmToken", token))
		if !out.IsError || !strings.Contains(out.Text, "not valid") {
			t.Errorf("%s: %+v", name, out)
		}
	}
	if a.requestCount() != 0 {
		t.Fatal("a token of another key placed an ask")
	}
	// Made up, and empty-ish.
	for _, bogus := range []string{"cf_madeupmadeupmadeupmade", "x", "   cf_   "} {
		if out := a.call(a.key, ToolAskForTitle, with(args, "confirmToken", bogus)); !out.IsError || !strings.Contains(out.Text, "not valid") {
			t.Errorf("token %q: %+v", bogus, out)
		}
	}
}

func TestConfirmTokenExpires(t *testing.T) {
	a := newAskRig(t)
	args := movieAsk(20, "hd")
	token := a.plan(args).Structured["confirmToken"]

	a.clock.Advance(5*time.Minute - time.Second)
	if out := a.call(a.key, ToolAskForTitle, with(args, "confirmToken", token)); out.IsError {
		t.Fatalf("a token one second before it expires: %s", out.Text)
	}
	token = a.plan(movieAsk(31, "hd")).Structured["confirmToken"]
	a.clock.Advance(5 * time.Minute)
	out := a.call(a.key, ToolAskForTitle, with(movieAsk(31, "hd"), "confirmToken", token))
	if !out.IsError || !strings.Contains(out.Text, "expired") {
		t.Errorf("an expired token: %+v", out)
	}
	if a.requestCount() != 1 {
		t.Errorf("%d requests, want 1", a.requestCount())
	}
}

func TestAskHourlyLimit(t *testing.T) {
	a := newAskRig(t)
	var early any // a sixth plan, made while the key still has a place left

	for i := range AsksPerHour {
		if i == AsksPerHour-1 {
			early = a.plan(movieAsk(100, "hd")).Structured["confirmToken"]
		}
		args := movieAsk(31+i, "hd")
		plan := a.plan(args)
		if got := plan.Structured["plan"].(map[string]any)["hourlyAsksLeft"]; got != float64(AsksPerHour-i) {
			t.Errorf("ask %d: asks left = %v", i+1, got)
		}
		a.mustCall(a.key, ToolAskForTitle, with(args, "confirmToken", plan.Structured["confirmToken"]))
		a.clock.Advance(time.Minute)
	}
	if a.requestCount() != AsksPerHour {
		t.Fatalf("%d asks placed", a.requestCount())
	}

	// The sixth is refused when it is planned, with no token, and when it is confirmed.
	out := a.plan(movieAsk(101, "hd"))
	if out.Structured["canAsk"] != false || out.Structured["confirmToken"] != nil || out.Structured["plan"].(map[string]any)["hourlyAsksLeft"] != float64(0) ||
		!strings.Contains(out.Text, "5 asks in the last hour") || !strings.Contains(out.Text, "No ask was placed") {
		t.Errorf("planning over the hourly limit: %v / %q", out.Structured, out.Text)
	}
	over := a.call(a.key, ToolAskForTitle, with(movieAsk(100, "hd"), "confirmToken", early))
	if !over.IsError || !strings.Contains(over.Text, "5 asks in the last hour") {
		t.Errorf("confirming over the hourly limit: %+v", over)
	}
	if a.requestCount() != AsksPerHour {
		t.Fatalf("an ask got through the hourly limit: %d", a.requestCount())
	}
	// Another key has its own hourly allowance.
	other := a.rig.key(a.mia, accounts.ScopeSee, accounts.ScopeAsk)
	if p := a.mustCall(other, ToolAskForTitle, movieAsk(102, "hd")); p.Structured["canAsk"] != true {
		t.Errorf("another key's plan = %v", p.Structured)
	}

	// The hour slides: the first ask was 5 minutes ago now, and leaves the window after 60.
	a.clock.Advance(55 * time.Minute) // an hour since the first
	plan := a.plan(movieAsk(101, "hd"))
	if plan.Structured["canAsk"] != true || plan.Structured["plan"].(map[string]any)["hourlyAsksLeft"] != float64(1) {
		t.Errorf("after the first ask left the hour: %v", plan.Structured)
	}
	if out := a.call(a.key, ToolAskForTitle, with(movieAsk(101, "hd"), "confirmToken", plan.Structured["confirmToken"])); out.IsError {
		t.Errorf("an ask once there is room: %s", out.Text)
	}
}

func TestAFailedAskGivesItsPlaceBack(t *testing.T) {
	a := newAskRig(t)
	args := movieAsk(20, "hd")
	token := a.plan(args).Structured["confirmToken"]

	// Somebody else asks first, so Mia's confirm is refused as a duplicate.
	if _, err := a.req.Ask(bg(), requests.Actor{UserID: a.noah.ID, Role: "member"}, requests.AskInput{Kind: "movie", Provider: "tmdb", ProviderID: "20", Quality: "hd"}); err != nil {
		t.Fatal(err)
	}
	out := a.call(a.key, ToolAskForTitle, with(args, "confirmToken", token))
	if !out.IsError || !strings.Contains(out.Text, "already asked for") {
		t.Fatalf("a duplicate at confirm: %+v", out)
	}
	if left, _ := a.mcp.asks.left(a.keyID, a.clock.Now()); left != AsksPerHour {
		t.Errorf("places left after a refused ask = %d, want %d", left, AsksPerHour)
	}
	if a.countRows(`SELECT COUNT(*) FROM requests WHERE user_id = ?`, a.mia.ID) != 0 {
		t.Error("Mia has an ask she did not get")
	}
}

func TestAskPlanSaysWhyNot(t *testing.T) {
	a := newAskRig(t)
	args := movieAsk(20, "hd")

	// Somebody else's ask: said without whose it is.
	if _, err := a.req.Ask(bg(), requests.Actor{UserID: a.noah.ID, Role: "member"}, requests.AskInput{Kind: "movie", Provider: "tmdb", ProviderID: "20", Quality: "hd"}); err != nil {
		t.Fatal(err)
	}
	out := a.plan(args)
	existing := out.Structured["plan"].(map[string]any)["existingAsk"].(map[string]any)
	if out.Structured["canAsk"] != false || existing["yours"] != false || !strings.Contains(out.Text, "Someone has already asked") || strings.Contains(out.Text, "Noah") {
		t.Errorf("someone else's ask: %v / %q", out.Structured, out.Text)
	}
	// Another quality is a separate copy, so it can still be asked for.
	if p := a.plan(with(args, "quality", "uhd")); p.Structured["canAsk"] != true {
		t.Errorf("4K of a title asked for in HD: %v", p.Structured)
	}

	// An available ask.
	if _, err := a.db.Exec(`UPDATE requests SET state = 'available'`); err != nil {
		t.Fatal(err)
	}
	if out := a.plan(args); out.Structured["canAsk"] != false || !strings.Contains(out.Text, "already available in HD") {
		t.Errorf("an available title: %q", out.Text)
	}

	// The weekly limit, from the person's own settings.
	owner := requests.Actor{UserID: a.owner.ID, Role: "owner"}
	zero := 0
	if _, err := a.req.SetLimits(bg(), owner, a.mia.ID, requests.LimitsPatch{HDPerWeek: requests.Patch[int]{Set: true, Value: &zero}}); err != nil {
		t.Fatal(err)
	}
	out = a.plan(movieAsk(31, "hd"))
	hd := out.Structured["plan"].(map[string]any)["usage"].(map[string]any)["hd"].(map[string]any)
	if out.Structured["canAsk"] != false || hd["limit"] != float64(0) || !strings.Contains(out.Text, "used all 0 of your HD asks") || out.Structured["confirmToken"] != nil {
		t.Errorf("a weekly limit of 0: %v / %q", out.Structured, out.Text)
	}
}

func TestAskWeeklyLimitStopsTheConfirmToo(t *testing.T) {
	a := newAskRig(t)
	args := movieAsk(20, "hd")
	token := a.plan(args).Structured["confirmToken"]

	owner := requests.Actor{UserID: a.owner.ID, Role: "owner"}
	zero := 0
	if _, err := a.req.SetLimits(bg(), owner, a.mia.ID, requests.LimitsPatch{HDPerWeek: requests.Patch[int]{Set: true, Value: &zero}}); err != nil {
		t.Fatal(err)
	}
	out := a.call(a.key, ToolAskForTitle, with(args, "confirmToken", token))
	if !out.IsError || !strings.Contains(out.Text, "used all 0 of your HD asks") {
		t.Errorf("confirm over the weekly limit: %+v", out)
	}
	if a.requestCount() != 0 {
		t.Error("an ask got past the person's weekly limit")
	}
	if left, _ := a.mcp.asks.left(a.keyID, a.clock.Now()); left != AsksPerHour {
		t.Errorf("the hourly place was not given back: %d", left)
	}
}

func TestAskInputIsChecked(t *testing.T) {
	a := newAskRig(t)
	chopin := a.addTitle("artist", "musicbrainz", chopinMBID)
	for name, args := range map[string]map[string]any{
		"no quality":                      {"kind": "movie", "providerId": "20"},
		"a quality that is not":           movieAsk(20, "8k"),
		"no title":                        {"quality": "hd"},
		"a kind and no id":                {"kind": "movie", "quality": "hd"},
		"an id and no kind":               {"providerId": "20", "quality": "hd"},
		"an unknown kind":                 {"kind": "podcast", "providerId": "1", "quality": "hd"},
		"a provider that does not fit":    {"kind": "movie", "provider": "tvmaze", "providerId": "20", "quality": "hd"},
		"a movie id that is not a number": movieAsk("tt0017136", "hd"),
		"an artist id that is not a UUID": {"kind": "artist", "providerId": "123", "quality": "hd"},
		"a title the provider lacks":      movieAsk(404, "hd"),
		"a show the provider lacks":       {"kind": "show", "providerId": "404", "quality": "hd"},
		"a title Marquee does not track":  {"titleId": 987654, "quality": "hd"},
		"a negative id":                   {"titleId": -1, "quality": "hd"},
		"4K music":                        {"titleId": chopin.ID, "quality": "uhd"},
		"seasons of a movie":              with(movieAsk(20, "hd"), "seasons", []any{1}),
		"a negative season":               {"kind": "show", "providerId": "99001", "quality": "hd", "seasons": []any{-1}},
		"a note that is too long":         with(movieAsk(20, "hd"), "note", strings.Repeat("n", 501)),
		"a quality of the wrong type":     {"kind": "movie", "providerId": "20", "quality": 4},
	} {
		out := a.call(a.key, ToolAskForTitle, args)
		if !out.IsError || out.Text == "" || out.Structured != nil {
			t.Errorf("%s: want an error result, got %+v", name, out)
		}
	}
	if a.requestCount() != 0 || a.countRows(`SELECT COUNT(*) FROM titles WHERE kind <> 'artist'`) != 0 {
		t.Error("a refused input changed something")
	}
	// A season the show does not have is caught by the requests module when confirming.
	args := map[string]any{"kind": "show", "providerId": "99001", "quality": "hd", "seasons": []any{9}}
	plan := a.plan(args)
	out := a.call(a.key, ToolAskForTitle, with(args, "confirmToken", plan.Structured["confirmToken"]))
	if !out.IsError || !strings.Contains(out.Text, "no season 9") {
		t.Errorf("an unknown season at confirm: %+v", out)
	}
	if left, _ := a.mcp.asks.left(a.keyID, a.clock.Now()); left != AsksPerHour {
		t.Errorf("the place was not given back after a refused season: %d", left)
	}
}

func TestAskWithoutAFolderForTheKind(t *testing.T) {
	// An owner's ask would be approved, but there is nowhere to put it: Claude is told what to ask the owner.
	r := newRig(t)
	for _, f := range []int64{r.moviesFolder.ID, r.movies4kFolder.ID} {
		if err := r.lib.RemoveFolder(bg(), f); err != nil {
			t.Fatal(err)
		}
	}
	key := r.key(r.owner, accounts.ScopeAsk)
	plan := r.mustCall(key, ToolAskForTitle, movieAsk(20, "hd"))
	out := r.call(key, ToolAskForTitle, with(movieAsk(20, "hd"), "confirmToken", plan.Structured["confirmToken"]))
	if !out.IsError || !strings.Contains(out.Text, "no library folder") {
		t.Errorf("no folder: %+v", out)
	}
	if r.countRows(`SELECT COUNT(*) FROM requests`) != 0 {
		t.Error("an ask was stored with nowhere to go")
	}
}
