package requests

import (
	"encoding/json"
	"errors"
	"strings"
	"testing"
)

func jsonOf(t *testing.T, v any) map[string]any {
	t.Helper()
	b, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	var m map[string]any
	if err := json.Unmarshal(b, &m); err != nil {
		t.Fatal(err)
	}
	return m
}

// ---- unlimited --------------------------------------------------------------------

func TestUnlimitedPerPersonLimits(t *testing.T) {
	e := newEnv(t)
	e.standardFolders()

	// A default of 1 HD ask: Mia is stopped at the second.
	cfg := DefaultConfig()
	cfg.HDPerWeek = 1
	if err := SaveConfig(bg(), e.cfg, cfg); err != nil {
		t.Fatal(err)
	}
	e.mustAsk(e.mia, movie(1, QualityHD))
	var lim *LimitError
	if _, err := e.ask(e.mia, movie(2, QualityHD)); !errors.As(err, &lim) {
		t.Fatalf("second ask = %v, want a LimitError", err)
	}

	// The owner makes her unlimited (-1): no limit applies, however many she asks.
	e.setLimits(e.mia, LimitsPatch{HDPerWeek: intP(Unlimited)})
	for i := int64(2); i <= 4; i++ {
		if _, err := e.ask(e.mia, movie(i, QualityHD)); err != nil {
			t.Fatalf("ask %d: %v", i, err)
		}
	}

	people, err := e.svc.People(bg(), actorOf(e.owner))
	if err != nil {
		t.Fatal(err)
	}
	var mia, noah PersonInfo
	for _, p := range people.People {
		switch p.Name {
		case "Mia":
			mia = p
		case "Noah":
			noah = p
		}
	}
	if mia.Limits.Effective.HDPerWeek != nil || !mia.Limits.Effective.HDUnlimited || mia.Limits.Effective.UHDUnlimited {
		t.Errorf("Mia's effective limits = %+v", mia.Limits.Effective)
	}
	if mia.Usage.HD.Limit != nil || !mia.Usage.HD.Unlimited || mia.Usage.HD.Used != 4 || mia.Usage.UHD.Unlimited {
		t.Errorf("Mia's usage = %+v", mia.Usage)
	}
	// The API shape: null with the flag. Null with the flag off still means "default".
	over := jsonOf(t, mia.Limits.Overrides)
	if over["hdPerWeek"] != nil || over["hdUnlimited"] != true || over["uhdPerWeek"] != nil || over["uhdUnlimited"] != false {
		t.Errorf("Mia's overrides as JSON = %v", over)
	}
	use := jsonOf(t, mia.Usage.HD)
	if use["limit"] != nil || use["unlimited"] != true {
		t.Errorf("usage as JSON = %v", use)
	}
	if over := jsonOf(t, noah.Limits.Overrides); over["hdPerWeek"] != nil || over["hdUnlimited"] != false {
		t.Errorf("Noah has no override, but: %v", over)
	}
	if eff := jsonOf(t, noah.Limits.Effective); eff["hdPerWeek"] != float64(1) || eff["hdUnlimited"] != false {
		t.Errorf("Noah's effective limits as JSON = %v", eff)
	}

	// null goes back to the default, which is 1.
	e.setLimits(e.mia, LimitsPatch{HDPerWeek: clearInt()})
	if _, err := e.ask(e.mia, movie(5, QualityHD)); !errors.As(err, &lim) {
		t.Errorf("back on the default, an ask = %v, want a LimitError", err)
	}

	// Anything below -1 is a mistake; -1 and the top of the range are not.
	if _, err := e.svc.SetLimits(bg(), actorOf(e.owner), e.mia.ID, LimitsPatch{HDPerWeek: intP(-2)}); !isInvalid(err) {
		t.Errorf("-2 = %v, want an InvalidError", err)
	}
	if _, err := e.svc.SetLimits(bg(), actorOf(e.owner), e.mia.ID, LimitsPatch{HDPerWeek: intP(maxPerWeek)}); err != nil {
		t.Errorf("%d: %v", maxPerWeek, err)
	}
}

func TestUnlimitedDefaults(t *testing.T) {
	e := newEnv(t)
	e.standardFolders()

	// The default for every member can be unlimited too: written as the word, as -1,
	// or as the null-plus-flag the API shows.
	for name, section := range map[string]map[string]any{
		"word": {"hdPerWeek": "unlimited"},
		"-1":   {"hdPerWeek": -1},
		"flag": {"hdPerWeek": nil, "hdUnlimited": true},
		"case": {"hdPerWeek": " Unlimited "},
	} {
		if _, err := e.cfg.Put(bg(), SettingsSection, section); err != nil {
			t.Fatal(err)
		}
		c, err := LoadConfig(bg(), e.cfg)
		if err != nil || c.HDPerWeek != Unlimited || c.UHDPerWeek != 2 {
			t.Errorf("%s: config = %+v, %v", name, c, err)
		}
	}

	if _, err := e.cfg.Put(bg(), SettingsSection, map[string]any{"hdPerWeek": "unlimited"}); err != nil {
		t.Fatal(err)
	}
	for i := int64(1); i <= 12; i++ { // more than the usual default of 10
		if _, err := e.ask(e.mia, movie(i, QualityHD)); err != nil {
			t.Fatalf("ask %d with unlimited defaults: %v", i, err)
		}
	}
	people, err := e.svc.People(bg(), actorOf(e.owner))
	if err != nil {
		t.Fatal(err)
	}
	d := jsonOf(t, people.Defaults)
	if d["hdPerWeek"] != nil || d["hdUnlimited"] != true || d["uhdPerWeek"] != float64(2) || d["uhdUnlimited"] != false {
		t.Errorf("defaults as JSON = %v", d)
	}
	// 4K keeps its own limit.
	e.mustAsk(e.noah, movie(30, QualityUHD))
	e.mustAsk(e.noah, movie(31, QualityUHD))
	var lim *LimitError
	if _, err := e.ask(e.noah, movie(32, QualityUHD)); !errors.As(err, &lim) {
		t.Errorf("third 4K ask = %v, want a LimitError", err)
	}

	// Saving the Config round-trips an unlimited default.
	c := DefaultConfig()
	c.UHDPerWeek = Unlimited
	if err := SaveConfig(bg(), e.cfg, c); err != nil {
		t.Fatal(err)
	}
	got, err := LoadConfig(bg(), e.cfg)
	if err != nil || got.UHDPerWeek != Unlimited || got.HDPerWeek != 10 {
		t.Errorf("round trip = %+v, %v", got, err)
	}
	sec, _ := e.cfg.GetMasked(bg(), SettingsSection)
	if sec.Value["uhdPerWeek"] != nil || sec.Value["uhdUnlimited"] != true {
		t.Errorf("stored shape = %v", sec.Value)
	}
}

func TestNormalizeConfig(t *testing.T) {
	good, err := NormalizeConfig(map[string]any{"hdPerWeek": "unlimited", "uhdPerWeek": float64(3), "autoApproveUHD": true})
	if err != nil {
		t.Fatal(err)
	}
	if good["hdPerWeek"] != nil || good["hdUnlimited"] != true || good["uhdPerWeek"] != float64(3) ||
		good["uhdUnlimited"] != false || good["autoApproveUHD"] != true || good["autoApproveHD"] != true {
		t.Errorf("normalized = %v", good)
	}
	// Left out fields take their defaults.
	def, err := NormalizeConfig(map[string]any{})
	if err != nil || def["hdPerWeek"] != float64(10) || def["uhdPerWeek"] != float64(2) {
		t.Errorf("defaults = %v, %v", def, err)
	}
	for name, in := range map[string]map[string]any{
		"text":     {"hdPerWeek": "lots"},
		"fraction": {"hdPerWeek": 2.5},
		"negative": {"uhdPerWeek": -5},
		"huge":     {"uhdPerWeek": maxPerWeek + 1},
		"list":     {"hdPerWeek": []any{1}},
	} {
		if _, err := NormalizeConfig(in); !isInvalid(err) {
			t.Errorf("%s: %v, want an InvalidError", name, err)
		} else if ie := err.(*InvalidError); ie.Code != "invalid_limit" || !strings.Contains(ie.Message, "limit") {
			t.Errorf("%s: %+v", name, ie)
		}
	}
}

// ---- the owner's name and the title filter ------------------------------------------

func TestSummaryNamesTheOwnerToEveryone(t *testing.T) {
	e := newEnv(t)
	for _, u := range []struct {
		name string
		act  Actor
	}{{"owner", actorOf(e.owner)}, {"admin", actorOf(e.admin)}, {"member", actorOf(e.mia)}} {
		sum, err := e.svc.Summary(bg(), u.act)
		if err != nil {
			t.Fatal(err)
		}
		if sum.Owner == nil || sum.Owner.Name != "Bailey" {
			t.Errorf("%s: owner = %+v", u.name, sum.Owner)
		}
		m := jsonOf(t, sum)
		if o, _ := m["owner"].(map[string]any); o["name"] != "Bailey" {
			t.Errorf("%s: JSON owner = %v", u.name, m["owner"])
		}
		// A member's summary still has none of the owner's numbers.
		if u.name == "member" && sum.WaitingForYou != 0 {
			t.Errorf("a member sees %d waiting", sum.WaitingForYou)
		}
	}
}

func TestListByTitle(t *testing.T) {
	e := newEnv(t)
	e.standardFolders()
	a := e.mustAsk(e.owner, movie(1, QualityHD)).Request
	b := e.mustAsk(e.mia, movie(2, QualityUHD)).Request
	c := e.mustAsk(e.owner, movie(2, QualityHD)).Request

	res, err := e.svc.List(bg(), actorOf(e.owner), ListFilter{TitleID: b.Title.ID})
	if err != nil || res.Total != 2 || len(res.Items) != 2 {
		t.Fatalf("by title: %+v, %v", res, err)
	}
	for _, it := range res.Items {
		if it.Title.ID != b.Title.ID || (it.ID != b.ID && it.ID != c.ID) {
			t.Errorf("an item of another title: %+v", it)
		}
	}
	if res, _ := e.svc.List(bg(), actorOf(e.owner), ListFilter{TitleID: a.Title.ID}); res.Total != 1 || res.Items[0].ID != a.ID {
		t.Errorf("by the first title: %+v", res)
	}
	if res, _ := e.svc.List(bg(), actorOf(e.owner), ListFilter{TitleID: 9999}); res.Total != 0 || res.Items == nil || len(res.Items) != 0 {
		t.Errorf("no such title: %+v", res)
	}
	// Combined with the state, and a member still only sees their own.
	if res, _ := e.svc.List(bg(), actorOf(e.owner), ListFilter{TitleID: b.Title.ID, State: StatePending}); res.Total != 1 || res.Items[0].ID != b.ID {
		t.Errorf("by title and state: %+v", res)
	}
	if res, _ := e.svc.List(bg(), actorOf(e.noah), ListFilter{TitleID: b.Title.ID}); res.Total != 0 {
		t.Errorf("Noah must not see Mia's ask: %+v", res)
	}
	if res, _ := e.svc.List(bg(), actorOf(e.mia), ListFilter{TitleID: b.Title.ID}); res.Total != 1 || res.Items[0].ID != b.ID {
		t.Errorf("Mia's own ask: %+v", res)
	}
	if _, err := e.svc.List(bg(), actorOf(e.owner), ListFilter{TitleID: -1}); !isInvalid(err) {
		t.Errorf("a negative title id = %v, want an InvalidError", err)
	}
}
