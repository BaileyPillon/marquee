package requests

import (
	"errors"
	"testing"
	"time"

	"github.com/BaileyPillon/marquee/internal/accounts"
	"github.com/BaileyPillon/marquee/internal/metadata"
	"github.com/BaileyPillon/marquee/internal/settings"
)

// ---- who sees what ---------------------------------------------------------

func TestMembersOnlySeeTheirOwnRequests(t *testing.T) {
	e := newEnv(t)
	e.standardFolders()
	mine := e.mustAsk(e.mia, movie(1, QualityHD)).Request
	his := e.mustAsk(e.noah, movie(2, QualityUHD)).Request
	own := e.mustAsk(e.owner, movie(3, QualityHD)).Request

	// A member's list holds only theirs, even when asking for everything or
	// naming someone else.
	for _, f := range []ListFilter{{}, {Mine: true}, {State: StatePending}, {State: StateApproved}} {
		res, err := e.svc.List(bg(), actorOf(e.noah), f)
		if err != nil {
			t.Fatal(err)
		}
		for _, it := range res.Items {
			if it.User == nil || it.User.ID != e.noah.ID {
				t.Errorf("Noah's list %+v holds %+v", f, it)
			}
		}
	}
	res, _ := e.svc.List(bg(), actorOf(e.noah), ListFilter{})
	if res.Total != 1 || len(res.Items) != 1 || res.Items[0].ID != his.ID {
		t.Errorf("Noah's list = %+v", res)
	}
	if _, err := e.svc.Get(bg(), actorOf(e.noah), mine.ID); !errors.Is(err, ErrNotFound) {
		t.Errorf("a member reading someone else's request: %v, want ErrNotFound", err)
	}
	if it, err := e.svc.Get(bg(), actorOf(e.noah), his.ID); err != nil || it.ID != his.ID {
		t.Errorf("a member reading their own: %+v, %v", it, err)
	}

	// The owner and admins see everyone's, unless they ask for their own.
	for _, u := range []accounts.User{e.owner, e.admin} {
		res, err := e.svc.List(bg(), actorOf(u), ListFilter{})
		if err != nil || res.Total != 3 {
			t.Fatalf("%s's list: %+v, %v", u.Name, res, err)
		}
		if _, err := e.svc.Get(bg(), actorOf(u), mine.ID); err != nil {
			t.Errorf("%s reading a member's request: %v", u.Name, err)
		}
	}
	res, _ = e.svc.List(bg(), actorOf(e.owner), ListFilter{Mine: true})
	if res.Total != 1 || res.Items[0].ID != own.ID {
		t.Errorf("the owner's own = %+v", res)
	}
	if res, _ = e.svc.List(bg(), actorOf(e.admin), ListFilter{Mine: true}); res.Total != 0 {
		t.Errorf("the admin has made no asks, but Mine = %+v", res)
	}
}

func TestListFilterOrderAndPaging(t *testing.T) {
	e := newEnv(t)
	e.standardFolders()
	var ids []int64
	for i := int64(1); i <= 5; i++ {
		ids = append(ids, e.mustAsk(e.owner, movie(i, QualityHD)).Request.ID)
		e.clock.Advance(time.Minute)
	}
	waiting := e.mustAsk(e.mia, movie(9, QualityUHD)).Request

	res, err := e.svc.List(bg(), actorOf(e.owner), ListFilter{})
	if err != nil || res.Total != 6 || res.Limit != DefaultLimit || res.Offset != 0 {
		t.Fatalf("list = %+v, %v", res, err)
	}
	if res.Items[0].ID != waiting.ID || res.Items[1].ID != ids[4] {
		t.Errorf("newest first expected, got ids %d, %d", res.Items[0].ID, res.Items[1].ID)
	}
	res, _ = e.svc.List(bg(), actorOf(e.owner), ListFilter{State: StatePending})
	if res.Total != 1 || res.Items[0].ID != waiting.ID {
		t.Errorf("pending = %+v", res)
	}
	res, _ = e.svc.List(bg(), actorOf(e.owner), ListFilter{State: StateDeclined})
	if res.Total != 0 || res.Items == nil || len(res.Items) != 0 {
		t.Errorf("declined = %+v, want an empty page", res)
	}
	res, _ = e.svc.List(bg(), actorOf(e.owner), ListFilter{Limit: 2, Offset: 2})
	if res.Total != 6 || len(res.Items) != 2 || res.Items[0].ID != ids[3] || res.Limit != 2 || res.Offset != 2 {
		t.Errorf("page = %+v", res)
	}
	if res, _ = e.svc.List(bg(), actorOf(e.owner), ListFilter{Limit: 5000}); res.Limit != MaxLimit {
		t.Errorf("limit = %d, want it capped at %d", res.Limit, MaxLimit)
	}
	for _, f := range []ListFilter{{State: "lost"}, {Offset: -1}} {
		if _, err := e.svc.List(bg(), actorOf(e.owner), f); !isInvalid(err) {
			t.Errorf("List(%+v) = %v, want an InvalidError", f, err)
		}
	}
	if _, err := e.svc.List(bg(), Actor{Role: "member"}, ListFilter{}); !errors.Is(err, ErrForbidden) {
		t.Errorf("a member with no id: %v", err)
	}
	if _, err := e.svc.Get(bg(), actorOf(e.owner), 9999); !errors.Is(err, ErrNotFound) {
		t.Errorf("Get(9999) = %v", err)
	}
}

func TestItemFacts(t *testing.T) {
	e := newEnv(t)
	movies, _, _, _ := e.standardFolders()
	res := e.mustAsk(e.mia, movie(1, QualityHD)).Request
	it, err := e.svc.Get(bg(), actorOf(e.mia), res.ID)
	if err != nil {
		t.Fatal(err)
	}
	if it.Facts.FolderPath != movies.Path || it.Facts.SizeEstimate != nil {
		t.Errorf("facts = %+v", it.Facts)
	}
	// Declined and cancelled asks go nowhere.
	d := e.mustAsk(e.mia, movie(2, QualityUHD)).Request
	dec, _ := e.svc.Decline(bg(), actorOf(e.owner), d.ID, "no")
	if dec.Facts.FolderPath != "" {
		t.Errorf("a declined ask shows the folder %q", dec.Facts.FolderPath)
	}
}

// ---- summary ---------------------------------------------------------------

func TestSummary(t *testing.T) {
	e := newEnv(t)
	e.standardFolders()
	e.mustAsk(e.mia, movie(1, QualityHD))  // approved by the rules
	e.mustAsk(e.mia, movie(2, QualityUHD)) // waiting
	e.mustAsk(e.noah, movie(3, QualityUHD))
	e.mustAsk(e.noah, movie(4, QualityUHD))
	e.mustAsk(e.owner, movie(5, QualityHD))

	sum, err := e.svc.Summary(bg(), actorOf(e.owner))
	if err != nil || sum.WaitingForYou != 3 || sum.Mine.Pending != 0 || sum.Mine.Approved != 1 {
		t.Errorf("owner summary = %+v, %v", sum, err)
	}
	if sum.Usage.HD.Used != 1 || sum.Usage.HD.Limit != nil || sum.Usage.UHD.Used != 0 {
		t.Errorf("owner usage = %+v", sum.Usage)
	}
	sum, _ = e.svc.Summary(bg(), actorOf(e.admin))
	if sum.WaitingForYou != 3 {
		t.Errorf("admin waitingForYou = %d, want 3", sum.WaitingForYou)
	}
	sum, err = e.svc.Summary(bg(), actorOf(e.mia))
	if err != nil || sum.WaitingForYou != 0 || sum.Mine.Pending != 1 || sum.Mine.Approved != 1 {
		t.Errorf("Mia's summary = %+v, %v", sum, err)
	}
	if sum.Usage.HD.Used != 1 || sum.Usage.HD.Limit == nil || *sum.Usage.HD.Limit != 10 ||
		sum.Usage.UHD.Used != 1 || sum.Usage.UHD.Limit == nil || *sum.Usage.UHD.Limit != 2 ||
		sum.Usage.UHD.ResetsAt == nil || !sum.Usage.UHD.ResetsAt.Equal(t0.Add(Window)) {
		t.Errorf("Mia's usage = %+v", sum.Usage)
	}
}

// ---- people and limits -----------------------------------------------------

func TestPeopleAndLimits(t *testing.T) {
	e := newEnv(t)
	e.standardFolders()
	e.mustAsk(e.mia, movie(1, QualityHD))
	e.mustAsk(e.mia, movie(2, QualityUHD))

	people, err := e.svc.People(bg(), actorOf(e.admin))
	if err != nil {
		t.Fatal(err)
	}
	if len(people.People) != 4 || people.Defaults.HDPerWeek != 10 || people.Defaults.UHDPerWeek != 2 ||
		!people.Defaults.AutoApproveHD || people.Defaults.AutoApproveUHD {
		t.Fatalf("people = %+v", people)
	}
	byName := map[string]PersonInfo{}
	for _, p := range people.People {
		byName[p.Name] = p
	}
	own, mia := byName["Bailey"], byName["Mia"]
	if own.Role != "owner" || own.Limits.Effective.HDPerWeek != nil || !own.AutoApprove.HD || !own.AutoApprove.UHD {
		t.Errorf("owner = %+v", own)
	}
	if mia.Role != "member" || mia.Disabled || mia.Limits.Effective.HDPerWeek == nil || *mia.Limits.Effective.HDPerWeek != 10 ||
		*mia.Limits.Effective.UHDPerWeek != 2 || !mia.AutoApprove.HD || mia.AutoApprove.UHD ||
		mia.Limits.Overrides != (LimitOverrides{}) {
		t.Errorf("Mia = %+v", mia)
	}
	if mia.Usage.HD.Used != 1 || mia.Usage.UHD.Used != 1 || *mia.Usage.UHD.Limit != 2 {
		t.Errorf("Mia's usage = %+v", mia.Usage)
	}
	if _, err := e.svc.People(bg(), actorOf(e.mia)); !errors.Is(err, ErrForbidden) {
		t.Errorf("a member listing people: %v", err)
	}

	// Setting some overrides leaves the others alone; null clears.
	info, err := e.svc.SetLimits(bg(), actorOf(e.owner), e.mia.ID,
		LimitsPatch{HDPerWeek: intP(25), AutoApproveUHD: boolP(true)})
	if err != nil {
		t.Fatal(err)
	}
	o := info.Limits.Overrides
	if o.HDPerWeek == nil || *o.HDPerWeek != 25 || o.UHDPerWeek != nil || o.AutoApproveHD != nil || o.AutoApproveUHD == nil || !*o.AutoApproveUHD {
		t.Errorf("overrides = %+v", o)
	}
	if *info.Limits.Effective.HDPerWeek != 25 || *info.Limits.Effective.UHDPerWeek != 2 || !info.AutoApprove.UHD {
		t.Errorf("effective = %+v", info.Limits.Effective)
	}
	if info.Usage.HD.Used != 1 || info.Name != "Mia" {
		t.Errorf("info = %+v", info)
	}
	info, err = e.svc.SetLimits(bg(), actorOf(e.owner), e.mia.ID, LimitsPatch{UHDPerWeek: intP(0), HDPerWeek: clearInt()})
	if err != nil {
		t.Fatal(err)
	}
	o = info.Limits.Overrides
	if o.HDPerWeek != nil || o.UHDPerWeek == nil || *o.UHDPerWeek != 0 || o.AutoApproveUHD == nil || !*o.AutoApproveUHD {
		t.Errorf("overrides after a clear = %+v", o)
	}
	if *info.Limits.Effective.HDPerWeek != 10 || *info.Limits.Effective.UHDPerWeek != 0 {
		t.Errorf("effective after a clear = %+v", info.Limits.Effective)
	}
	// The defaults move everybody who has no override.
	c := DefaultConfig()
	c.HDPerWeek = 4
	if err := SaveConfig(bg(), e.cfg, c); err != nil {
		t.Fatal(err)
	}
	people, _ = e.svc.People(bg(), actorOf(e.owner))
	for _, p := range people.People {
		if p.Name == "Noah" && *p.Limits.Effective.HDPerWeek != 4 {
			t.Errorf("Noah's HD limit = %d, want the new default 4", *p.Limits.Effective.HDPerWeek)
		}
	}

	// Mistakes and permissions.
	// (-1 is not a mistake any more: it is Unlimited, see TestUnlimitedLimits.)
	for _, p := range []LimitsPatch{{HDPerWeek: intP(-2)}, {UHDPerWeek: intP(maxPerWeek + 1)}} {
		if _, err := e.svc.SetLimits(bg(), actorOf(e.owner), e.mia.ID, p); !isInvalid(err) {
			t.Errorf("SetLimits(%+v) = %v, want an InvalidError", p, err)
		}
	}
	if _, err := e.svc.SetLimits(bg(), actorOf(e.owner), 9999, LimitsPatch{HDPerWeek: intP(1)}); !errors.Is(err, ErrUserNotFound) {
		t.Errorf("no such person: %v", err)
	}
	for _, u := range []accounts.User{e.admin, e.mia} {
		if _, err := e.svc.SetLimits(bg(), actorOf(u), e.noah.ID, LimitsPatch{HDPerWeek: intP(1)}); !errors.Is(err, ErrForbidden) {
			t.Errorf("%s changing limits: %v, want ErrForbidden", u.Name, err)
		}
	}
	// A limit of 0 means no asks of that quality at all.
	if _, err := e.ask(e.mia, movie(6, QualityUHD)); err == nil {
		t.Error("a limit of 0 let an ask through")
	}
}

// ---- settings --------------------------------------------------------------

func TestConfigDefaultsAndMerge(t *testing.T) {
	e := newEnv(t)
	c, err := LoadConfig(bg(), e.cfg)
	if err != nil || c != DefaultConfig() {
		t.Fatalf("nothing stored: %+v, %v", c, err)
	}
	if c.HDPerWeek != 10 || c.UHDPerWeek != 2 || !c.AutoApproveHD || c.AutoApproveUHD ||
		c.Folders.Movie.HD != nil || c.Folders.Movie.UHD != nil || c.Folders.Show.UHD != nil || c.Folders.Artist.HD != nil {
		t.Errorf("defaults = %+v", c)
	}

	// A section the owner saved from the settings screen, with some fields left out.
	if _, err := e.cfg.Put(bg(), SettingsSection, map[string]any{
		"hdPerWeek": 3,
		"folders":   map[string]any{"movie": map[string]any{"uhd": 7}},
	}); err != nil {
		t.Fatal(err)
	}
	c, err = LoadConfig(bg(), e.cfg)
	if err != nil || c.HDPerWeek != 3 || c.UHDPerWeek != 2 || !c.AutoApproveHD || c.Folders.Movie.UHD == nil || *c.Folders.Movie.UHD != 7 || c.Folders.Movie.HD != nil {
		t.Errorf("merged = %+v, %v", c, err)
	}

	// Saving and loading round-trip, and the stored shape is the documented one.
	want := DefaultConfig()
	id := int64(4)
	want.Folders.Artist.HD = &id
	if err := SaveConfig(bg(), e.cfg, want); err != nil {
		t.Fatal(err)
	}
	got, _ := LoadConfig(bg(), e.cfg)
	if got.Folders.Artist.HD == nil || *got.Folders.Artist.HD != 4 || got.HDPerWeek != 10 {
		t.Errorf("round trip = %+v", got)
	}
	sec, err := e.cfg.GetMasked(bg(), SettingsSection)
	if err != nil {
		t.Fatal(err)
	}
	folders, _ := sec.Value["folders"].(map[string]any)
	movieFolders, _ := folders["movie"].(map[string]any)
	if _, ok := movieFolders["hd"]; !ok || sec.Value["autoApproveUHD"] != false || sec.Value["uhdPerWeek"] != float64(2) {
		t.Errorf("stored section = %+v", sec.Value)
	}

	// A section that does not read gives the defaults and an error; the service
	// still works with the defaults.
	if _, err := e.cfg.Put(bg(), SettingsSection, map[string]any{"hdPerWeek": "ten"}); err != nil {
		t.Fatal(err)
	}
	c, err = LoadConfig(bg(), e.cfg)
	if err == nil || c != DefaultConfig() {
		t.Errorf("bad section: %+v, %v", c, err)
	}
	if got := e.svc.Config(bg()); got != DefaultConfig() {
		t.Errorf("Config on a bad section = %+v", got)
	}
	// A negative limit is not a limit.
	if _, err := e.cfg.Put(bg(), SettingsSection, map[string]any{"hdPerWeek": -5}); err != nil {
		t.Fatal(err)
	}
	if c, _ = LoadConfig(bg(), e.cfg); c.HDPerWeek != 10 {
		t.Errorf("negative limit became %d", c.HDPerWeek)
	}
	if c, err := LoadConfig(bg(), (*settings.Service)(nil)); err != nil || c != DefaultConfig() {
		t.Errorf("no settings service: %+v, %v", c, err)
	}
}

func TestPickFolder(t *testing.T) {
	f := func(id int64, label, path string) libraryFolder {
		return libraryFolder{ID: id, Kind: "movie", Label: label, Path: path}
	}
	movies := []libraryFolder{f(1, "Movies", "/media/hd/movies"), f(2, "Movies", "/media/4K/movies"), f(3, "Docs", "/media/docs")}
	cases := []struct {
		name    string
		folders []libraryFolder
		quality string
		want    int64
	}{
		{"hd: first", movies, QualityHD, 1},
		{"uhd: a 4K parent folder", movies, QualityUHD, 2},
		{"hd skips a 4K folder listed first", []libraryFolder{movies[1], movies[0]}, QualityHD, 1},
		{"uhd: named folder before a mere parent", []libraryFolder{f(1, "Films", "/4k/films"), f(2, "Films UHD", "/4k/uhd-films")}, QualityUHD, 2},
		{"uhd: label counts", []libraryFolder{f(1, "Movies", "/a"), f(2, "Ultra HD (UHD)", "/b")}, QualityUHD, 2},
		{"uhd: no 4K folder, same as hd", []libraryFolder{f(1, "Movies", "/a"), f(2, "More", "/b")}, QualityUHD, 1},
		{"hd: every folder is 4K, first not named for it", []libraryFolder{f(1, "Films", "/4k/films"), f(2, "Movies 4K", "/4k/movies")}, QualityHD, 1},
		{"hd: only a 4K folder", []libraryFolder{f(5, "4K", "/x")}, QualityHD, 5},
		{"uhd: only a 4K folder", []libraryFolder{f(5, "4K", "/x")}, QualityUHD, 5},
	}
	for _, c := range cases {
		got, ok := pickFolder(DefaultConfig(), KindMovie, c.quality, c.folders)
		if !ok || got.ID != c.want {
			t.Errorf("%s: picked %d (%v), want %d", c.name, got.ID, ok, c.want)
		}
	}
	// Folders of another kind are never picked.
	if _, ok := pickFolder(DefaultConfig(), KindShow, QualityHD, movies); ok {
		t.Error("a show went to a movie folder")
	}
	if _, ok := pickFolder(DefaultConfig(), KindMovie, QualityHD, nil); ok {
		t.Error("a folder came from nowhere")
	}
	// Artists go to music folders.
	music := []libraryFolder{{ID: 9, Kind: "music", Label: "Music", Path: "/music"}}
	if got, ok := pickFolder(DefaultConfig(), KindArtist, QualityHD, music); !ok || got.ID != 9 {
		t.Errorf("artist folder = %+v, %v", got, ok)
	}
}

// ---- the schema ------------------------------------------------------------

func TestOpenRequestsAreUniquePerTitleAndQuality(t *testing.T) {
	e := newEnv(t)
	e.standardFolders()
	res := e.mustAsk(e.owner, movie(1, QualityHD))
	ins := func(quality, state string) error {
		_, err := e.db.Exec(`INSERT INTO requests (user_id, title_id, kind, quality, state, created_at, updated_at)
			VALUES (?, ?, 'movie', ?, ?, '2026-11-20T12:00:00.000Z', '2026-11-20T12:00:00.000Z')`,
			e.mia.ID, res.Request.Title.ID, quality, state)
		return err
	}
	if err := ins("hd", "pending"); err == nil {
		t.Error("the index let a second open request in")
	}
	if err := ins("uhd", "pending"); err != nil {
		t.Errorf("another quality: %v", err)
	}
	if err := ins("hd", "declined"); err != nil {
		t.Errorf("a closed request: %v", err)
	}
	if err := ins("hd", "nonsense"); err == nil {
		t.Error("the state check let nonsense in")
	}
	if _, err := e.db.Exec(`INSERT INTO copies (title_id, quality, created_at) VALUES (?, 'hd', 'x')`, res.Request.Title.ID); err == nil {
		t.Error("two HD copies of one title")
	}
	if _, err := e.db.Exec(`INSERT INTO copies (title_id, quality, created_at) VALUES (?, '4k', 'x')`, res.Request.Title.ID); err == nil {
		t.Error("the quality check let 4k in")
	}
	// Removing a title removes its asks and copies; removing a library folder
	// leaves the copy without a folder.
	if err := e.cat.Delete(bg(), res.Request.Title.ID); err != nil {
		t.Fatal(err)
	}
	if n := e.count(`SELECT COUNT(*) FROM requests`) + e.count(`SELECT COUNT(*) FROM copies`); n != 0 {
		t.Errorf("%d rows left after the title was deleted", n)
	}
}

func TestMetadataErrorsAreNotWrapped(t *testing.T) {
	// The web layer relies on errors.Is working through Ask.
	e := newEnv(t)
	e.standardFolders()
	_, err := e.ask(e.owner, show(404, QualityHD, Scope{}))
	if !errors.Is(err, metadata.ErrNotFound) {
		t.Errorf("unknown show: %v", err)
	}
}
