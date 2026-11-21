package requests

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/BaileyPillon/marquee/internal/accounts"
	"github.com/BaileyPillon/marquee/internal/metadata"
	"github.com/BaileyPillon/marquee/internal/titles"
)

// ---- approval rules --------------------------------------------------------

func TestOwnerAskIsApprovedAtOnce(t *testing.T) {
	e := newEnv(t)
	movies, _, _, _ := e.standardFolders()
	sub, unsub := e.hub.Subscribe(Topic)
	defer unsub()
	sr := newFakeSearcher()
	e.svc.SetSearcher(sr)

	// The title is not in the catalog yet: the owner names it by provider ids.
	res := e.mustAsk(e.owner, movie(19, QualityHD))
	if !res.AutoApproved || res.Request.State != StateApproved {
		t.Fatalf("owner ask = %+v, want approved at once", res)
	}
	it := res.Request
	if it.DecidedBy == nil || it.DecidedBy.ID != e.owner.ID || it.DecidedAt == nil {
		t.Errorf("decidedBy = %+v, decidedAt = %v; the owner answered their own ask", it.DecidedBy, it.DecidedAt)
	}
	if it.User == nil || it.User.ID != e.owner.ID || it.User.Name != "Bailey" {
		t.Errorf("user = %+v", it.User)
	}
	if it.Title.Name != "Metropolis" || it.Title.Year != 1927 || it.Title.Kind != KindMovie || it.Title.PosterRef == "" {
		t.Errorf("title = %+v", it.Title)
	}
	if it.Facts.FolderPath != movies.Path || it.Facts.SizeEstimate != nil {
		t.Errorf("facts = %+v, want folder %q and no size", it.Facts, movies.Path)
	}

	// The copy exists, in the movie folder, and the title is monitored.
	if n := e.count(`SELECT COUNT(*) FROM copies WHERE title_id = ? AND quality = 'hd' AND folder_id = ? AND monitored = 1`,
		it.Title.ID, movies.ID); n != 1 {
		t.Errorf("copy rows = %d, want 1", n)
	}
	if d, err := e.cat.Get(bg(), it.Title.ID); err != nil || !d.Monitored {
		t.Errorf("title monitored = %v, %v", d != nil && d.Monitored, err)
	}

	// The searcher heard about the copy, and the browsers heard about the ask.
	copyID := sr.wait(t)
	cp, err := e.svc.Copy(bg(), copyID)
	if err != nil || cp.TitleID != it.Title.ID || cp.Quality != QualityHD || cp.FolderPath != movies.Path {
		t.Errorf("Copy(%d) = %+v, %v", copyID, cp, err)
	}
	select {
	case m := <-sub.C():
		if m.Topic != Topic {
			t.Errorf("event topic = %q", m.Topic)
		}
	case <-time.After(time.Second):
		t.Fatal("no event was published")
	}
}

func TestAdminAskIsApprovedAtOnceEvenFor4K(t *testing.T) {
	e := newEnv(t)
	_, movies4k, _, _ := e.standardFolders()
	res := e.mustAsk(e.admin, movie(7, QualityUHD))
	if !res.AutoApproved || res.Request.State != StateApproved || res.Request.Facts.FolderPath != movies4k.Path {
		t.Fatalf("admin 4K ask = %+v", res)
	}
}

func TestMemberHDAutoApproval(t *testing.T) {
	e := newEnv(t)
	e.standardFolders()

	// On by default.
	res := e.mustAsk(e.mia, movie(1, QualityHD))
	if !res.AutoApproved || res.Request.State != StateApproved {
		t.Fatalf("default: %+v, want approved", res.Request)
	}
	if res.Request.DecidedBy != nil || res.Request.DecidedAt == nil {
		t.Errorf("the rules approved it: decidedBy = %+v decidedAt = %v", res.Request.DecidedBy, res.Request.DecidedAt)
	}

	// Her own setting turns it off.
	e.setLimits(e.mia, LimitsPatch{AutoApproveHD: boolP(false)})
	res = e.mustAsk(e.mia, movie(2, QualityHD))
	if res.AutoApproved || res.Request.State != StatePending {
		t.Fatalf("own setting off: %+v, want pending", res.Request)
	}

	// Clearing it goes back to the default (on); turning the default off then
	// makes everybody wait, and a person's own "on" beats the default.
	e.setLimits(e.mia, LimitsPatch{AutoApproveHD: clearBool()})
	c := DefaultConfig()
	c.AutoApproveHD = false
	if err := SaveConfig(bg(), e.cfg, c); err != nil {
		t.Fatal(err)
	}
	if res = e.mustAsk(e.mia, movie(3, QualityHD)); res.Request.State != StatePending {
		t.Fatalf("default off: %+v, want pending", res.Request)
	}
	if res = e.mustAsk(e.noah, movie(4, QualityHD)); res.Request.State != StatePending {
		t.Fatalf("default off, other person: %+v, want pending", res.Request)
	}
	e.setLimits(e.noah, LimitsPatch{AutoApproveHD: boolP(true)})
	if res = e.mustAsk(e.noah, movie(5, QualityHD)); res.Request.State != StateApproved {
		t.Fatalf("own setting on: %+v, want approved", res.Request)
	}
}

func TestMember4KWaitsThenAlwaysAllow(t *testing.T) {
	e := newEnv(t)
	_, movies4k, _, _ := e.standardFolders()
	sr := newFakeSearcher()
	e.svc.SetSearcher(sr)

	res := e.mustAsk(e.mia, movie(10, QualityUHD))
	if res.AutoApproved || res.Request.State != StatePending {
		t.Fatalf("a member's 4K ask = %+v, want pending", res.Request)
	}
	// While it waits nothing is wanted, the title is not monitored, and the
	// request says where it would go.
	if n := e.count(`SELECT COUNT(*) FROM copies`); n != 0 {
		t.Errorf("%d copies before approval", n)
	}
	if d, _ := e.cat.Get(bg(), res.Request.Title.ID); d.Monitored {
		t.Error("the title is monitored before anyone said yes")
	}
	if res.Request.Facts.FolderPath != movies4k.Path {
		t.Errorf("folder shown while waiting = %q, want %q", res.Request.Facts.FolderPath, movies4k.Path)
	}
	if sr.count() != 0 {
		t.Error("searched before approval")
	}

	// A member cannot approve; the owner can, and ticks "always allow 4K".
	if _, err := e.svc.Approve(bg(), actorOf(e.mia), res.Request.ID, ApproveOptions{}); !errors.Is(err, ErrForbidden) {
		t.Errorf("a member approving: %v, want ErrForbidden", err)
	}
	it, err := e.svc.Approve(bg(), actorOf(e.owner), res.Request.ID, ApproveOptions{AlwaysAllowUHD: true})
	if err != nil {
		t.Fatal(err)
	}
	if it.State != StateApproved || it.DecidedBy == nil || it.DecidedBy.ID != e.owner.ID {
		t.Errorf("approved = %+v", it)
	}
	if d, _ := e.cat.Get(bg(), it.Title.ID); !d.Monitored {
		t.Error("the title is not monitored after approval")
	}
	sr.wait(t)

	// Her next 4K ask goes straight through; Noah's still waits.
	next := e.mustAsk(e.mia, movie(11, QualityUHD))
	if !next.AutoApproved || next.Request.State != StateApproved {
		t.Fatalf("after always-allow: %+v, want approved", next.Request)
	}
	if other := e.mustAsk(e.noah, movie(12, QualityUHD)); other.Request.State != StatePending {
		t.Errorf("someone else's 4K ask: %+v, want pending", other.Request)
	}

	// The people screen shows the flag as an override.
	people, err := e.svc.People(bg(), actorOf(e.owner))
	if err != nil {
		t.Fatal(err)
	}
	for _, p := range people.People {
		switch p.ID {
		case e.mia.ID:
			if p.Limits.Overrides.AutoApproveUHD == nil || !*p.Limits.Overrides.AutoApproveUHD || !p.AutoApprove.UHD {
				t.Errorf("Mia: %+v", p)
			}
		case e.noah.ID:
			if p.AutoApprove.UHD || p.Limits.Overrides.AutoApproveUHD != nil {
				t.Errorf("Noah: %+v", p)
			}
		}
	}
}

func TestMemberUHDAutoApprovalFollowsTheDefaultToo(t *testing.T) {
	e := newEnv(t)
	e.standardFolders()
	c := DefaultConfig()
	c.AutoApproveUHD = true
	if err := SaveConfig(bg(), e.cfg, c); err != nil {
		t.Fatal(err)
	}
	if res := e.mustAsk(e.mia, movie(1, QualityUHD)); res.Request.State != StateApproved {
		t.Errorf("default 4K auto-approve on: %+v", res.Request)
	}
	e.setLimits(e.noah, LimitsPatch{AutoApproveUHD: boolP(false)})
	if res := e.mustAsk(e.noah, movie(2, QualityUHD)); res.Request.State != StatePending {
		t.Errorf("own 4K setting off: %+v", res.Request)
	}
}

// ---- weekly limits ---------------------------------------------------------

func TestWeeklyLimitAndResetsAt(t *testing.T) {
	e := newEnv(t)
	e.standardFolders()
	e.setLimits(e.mia, LimitsPatch{AutoApproveUHD: boolP(true)}) // so the asks are approved and counted

	first := t0
	e.mustAsk(e.mia, movie(1, QualityUHD))
	e.clock.Advance(time.Hour)
	second := e.clock.Now()
	res := e.mustAsk(e.mia, movie(2, QualityUHD))
	if res.Usage.UHD.Used != 2 || res.Usage.UHD.Limit == nil || *res.Usage.UHD.Limit != 2 {
		t.Errorf("usage after two asks = %+v", res.Usage.UHD)
	}
	if res.Usage.UHD.ResetsAt == nil || !res.Usage.UHD.ResetsAt.Equal(first.Add(Window)) {
		t.Errorf("usage resetsAt = %v, want %v", res.Usage.UHD.ResetsAt, first.Add(Window))
	}
	if res.Usage.HD.Used != 0 || res.Usage.HD.Limit == nil || *res.Usage.HD.Limit != 10 || res.Usage.HD.ResetsAt != nil {
		t.Errorf("HD usage = %+v, want 0 of 10", res.Usage.HD)
	}

	// The third in the week is refused, with when a place frees up.
	e.clock.Advance(time.Hour)
	_, err := e.ask(e.mia, movie(3, QualityUHD))
	var lim *LimitError
	if !errors.As(err, &lim) {
		t.Fatalf("third 4K ask: %v, want a LimitError", err)
	}
	if lim.Used != 2 || lim.Limit != 2 || lim.Quality != QualityUHD || lim.ResetsAt == nil || !lim.ResetsAt.Equal(first.Add(Window)) {
		t.Errorf("limit error = %+v (resetsAt %v), want 2 of 2 resetting at %v", lim, lim.ResetsAt, first.Add(Window))
	}
	if n := e.count(`SELECT COUNT(*) FROM requests WHERE user_id = ?`, e.mia.ID); n != 2 {
		t.Errorf("%d requests stored, want 2", n)
	}
	// HD has its own count; the limit is per quality.
	if _, err := e.ask(e.mia, movie(4, QualityHD)); err != nil {
		t.Errorf("an HD ask with the 4K limit used up: %v", err)
	}
	// Other people are not affected.
	e.setLimits(e.noah, LimitsPatch{AutoApproveUHD: boolP(true)})
	if _, err := e.ask(e.noah, movie(5, QualityUHD)); err != nil {
		t.Errorf("another person's 4K ask: %v", err)
	}

	// The window rolls: until the first ask is 7 days old she is still full,
	// then one place is free (the second ask is still inside).
	e.clock.Advance(first.Add(Window).Sub(e.clock.Now()) - time.Second)
	if _, err := e.ask(e.mia, movie(6, QualityUHD)); !errors.As(err, &lim) {
		t.Fatalf("one second before the reset: %v, want a LimitError", err)
	}
	e.clock.Advance(time.Second)
	if _, err := e.ask(e.mia, movie(6, QualityUHD)); err != nil {
		t.Fatalf("at the reset: %v", err)
	}
	// Full again, and now the oldest ask in the window is the second one.
	_, err = e.ask(e.mia, movie(7, QualityUHD))
	if !errors.As(err, &lim) || lim.ResetsAt == nil || !lim.ResetsAt.Equal(second.Add(Window)) {
		t.Errorf("after the reset: %v (resetsAt %v), want a LimitError resetting at %v", err, lim.ResetsAt, second.Add(Window))
	}
}

func TestDeclinedAndCancelledAsksDoNotCountAgainstTheLimit(t *testing.T) {
	e := newEnv(t)
	e.standardFolders()
	// Two waiting 4K asks use the whole week's allowance.
	a := e.mustAsk(e.mia, movie(1, QualityUHD)).Request
	b := e.mustAsk(e.mia, movie(2, QualityUHD)).Request
	if _, err := e.ask(e.mia, movie(3, QualityUHD)); err == nil {
		t.Fatal("the limit did not hold")
	}
	if _, err := e.svc.Decline(bg(), actorOf(e.owner), a.ID, "not this one"); err != nil {
		t.Fatal(err)
	}
	if _, err := e.ask(e.mia, movie(3, QualityUHD)); err != nil {
		t.Fatalf("after a decline: %v", err)
	}
	if _, err := e.ask(e.mia, movie(4, QualityUHD)); err == nil {
		t.Fatal("the limit did not hold again")
	}
	if _, err := e.svc.Cancel(bg(), actorOf(e.mia), b.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := e.ask(e.mia, movie(4, QualityUHD)); err != nil {
		t.Fatalf("after a cancel: %v", err)
	}
}

func TestLimitIsCheckedBeforeTheCatalogIsAsked(t *testing.T) {
	e := newEnv(t)
	e.standardFolders()
	e.setLimits(e.mia, LimitsPatch{HDPerWeek: intP(0)})
	// Not a title that exists (404 does not): the limit answers first, and
	// nothing is added to the catalog.
	_, err := e.ask(e.mia, movie(404, QualityHD))
	var lim *LimitError
	if !errors.As(err, &lim) || lim.Limit != 0 || lim.ResetsAt != nil {
		t.Fatalf("limit 0: %v (%+v), want a LimitError with no reset time", err, lim)
	}
	if n := e.count(`SELECT COUNT(*) FROM titles`); n != 0 {
		t.Errorf("%d titles added for an ask that was never going to be taken", n)
	}
}

func TestOwnerAndAdminHaveNoLimitUnlessSetByName(t *testing.T) {
	e := newEnv(t)
	e.standardFolders()
	for i := int64(1); i <= 4; i++ {
		res := e.mustAsk(e.owner, movie(i, QualityUHD)) // the member default is 2 a week
		if res.Usage.UHD.Limit != nil {
			t.Fatalf("owner usage limit = %v, want none", *res.Usage.UHD.Limit)
		}
	}
	e.setLimits(e.admin, LimitsPatch{UHDPerWeek: intP(1)})
	e.mustAsk(e.admin, movie(10, QualityUHD))
	if _, err := e.ask(e.admin, movie(11, QualityUHD)); err == nil {
		t.Error("a limit set for an admin by name was ignored")
	}
}

// ---- duplicates ------------------------------------------------------------

func TestDuplicateAsks(t *testing.T) {
	e := newEnv(t)
	e.standardFolders()
	first := e.mustAsk(e.mia, movie(1, QualityHD)).Request // approved by the rules

	// The same title at the same quality, by anyone, is a duplicate.
	for _, u := range []accounts.User{e.mia, e.noah, e.owner} {
		_, err := e.ask(u, movie(1, QualityHD))
		var dup *DuplicateError
		if !errors.As(err, &dup) || dup.Existing.ID != first.ID || dup.Existing.State != StateApproved {
			t.Fatalf("%s asking again: %v, want a DuplicateError for request %d", u.Name, err, first.ID)
		}
	}
	// By title id too.
	if _, err := e.ask(e.noah, AskInput{TitleID: first.Title.ID, Quality: QualityHD}); err == nil {
		t.Error("asking by titleId got past the duplicate check")
	}
	// The 4K copy is a different ask from the HD copy that exists.
	uhd := e.mustAsk(e.noah, movie(1, QualityUHD))
	if uhd.Request.Quality != QualityUHD || uhd.Request.ID == first.ID {
		t.Fatalf("4K ask while an HD copy exists: %+v", uhd.Request)
	}
	if n := e.count(`SELECT COUNT(*) FROM copies WHERE title_id = ?`, first.Title.ID); n != 1 {
		t.Errorf("%d copies; only the HD one has been approved so far", n)
	}
	// Now it is the 4K ask that is waiting, and it blocks another 4K ask.
	_, err := e.ask(e.admin, movie(1, QualityUHD))
	var dup *DuplicateError
	if !errors.As(err, &dup) || dup.Existing.State != StatePending {
		t.Fatalf("second 4K ask: %v", err)
	}
	// A member is not shown whose ask it is, or what they wrote.
	e.mustAsk(e.mia, AskInput{Kind: KindMovie, Provider: metadata.ProviderTMDB, ProviderID: "30", Quality: QualityUHD, Note: "for movie night"})
	_, err = e.ask(e.noah, movie(30, QualityUHD))
	if !errors.As(err, &dup) || dup.Existing.User != nil || dup.Existing.Note != "" || dup.Existing.DecidedBy != nil {
		t.Errorf("a member sees someone else's ask as %+v", dup.Existing)
	}
	_, err = e.ask(e.owner, movie(30, QualityUHD))
	if !errors.As(err, &dup) || dup.Existing.User == nil || dup.Existing.Note != "for movie night" {
		t.Errorf("the owner sees %+v", dup.Existing)
	}

	// A declined, cancelled or failed ask is not open: the title can be asked again.
	if _, err := e.svc.Decline(bg(), actorOf(e.owner), uhd.Request.ID, ""); err != nil {
		t.Fatal(err)
	}
	again := e.mustAsk(e.noah, movie(1, QualityUHD))
	if again.Request.ID == uhd.Request.ID {
		t.Error("the declined ask came back")
	}
	if _, err := e.svc.Cancel(bg(), actorOf(e.noah), again.Request.ID); err != nil {
		t.Fatal(err)
	}
	e.mustAsk(e.noah, movie(1, QualityUHD))
	if err := e.svc.SetState(bg(), first.ID, StateFailed); err != nil {
		t.Fatal(err)
	}
	e.mustAsk(e.noah, movie(1, QualityHD))
}

func TestConcurrentAsksForTheSameTitleMakeOneRequest(t *testing.T) {
	e := newEnv(t)
	e.standardFolders()
	people := []accounts.User{e.owner, e.admin, e.mia, e.noah}
	var (
		wg      sync.WaitGroup
		mu      sync.Mutex
		ok, dup int
	)
	for i := range 12 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, err := e.ask(people[i%len(people)], movie(1, QualityHD))
			mu.Lock()
			defer mu.Unlock()
			var d *DuplicateError
			switch {
			case err == nil:
				ok++
			case errors.As(err, &d):
				dup++
			default:
				t.Errorf("ask %d: %v", i, err)
			}
		}()
	}
	wg.Wait()
	if ok != 1 || dup != 11 {
		t.Errorf("%d asks went through and %d were duplicates, want 1 and 11", ok, dup)
	}
	if n := e.count(`SELECT COUNT(*) FROM requests`); n != 1 {
		t.Errorf("%d requests stored, want 1", n)
	}
}

func TestConcurrentAsksCannotGoOverTheLimit(t *testing.T) {
	e := newEnv(t)
	e.standardFolders()
	e.setLimits(e.mia, LimitsPatch{HDPerWeek: intP(3)})
	for i := int64(1); i <= 10; i++ { // put the titles in the catalog up front
		if _, _, err := e.cat.Add(bg(), KindMovie, metadata.ProviderTMDB, itoa(i), titles.AddOptions{}); err != nil {
			t.Fatal(err)
		}
	}
	var (
		wg     sync.WaitGroup
		mu     sync.Mutex
		ok, ov int
	)
	for i := int64(1); i <= 10; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, err := e.ask(e.mia, movie(i, QualityHD))
			mu.Lock()
			defer mu.Unlock()
			var l *LimitError
			switch {
			case err == nil:
				ok++
			case errors.As(err, &l):
				ov++
			default:
				t.Errorf("ask %d: %v", i, err)
			}
		}()
	}
	wg.Wait()
	if ok != 3 || ov != 7 {
		t.Errorf("%d asks went through and %d hit the limit, want 3 and 7", ok, ov)
	}
}

// ---- titles that are not in the catalog yet --------------------------------

func TestAskForATitleNotInTheCatalog(t *testing.T) {
	e := newEnv(t)
	e.standardFolders()
	if n := e.count(`SELECT COUNT(*) FROM titles`); n != 0 {
		t.Fatalf("%d titles to start with", n)
	}

	// A member's 4K ask adds the title unmonitored, and it stays that way.
	res := e.mustAsk(e.mia, movie(19, QualityUHD))
	if res.Request.State != StatePending || res.Request.Title.Name != "Metropolis" {
		t.Fatalf("ask = %+v", res.Request)
	}
	d, err := e.cat.Get(bg(), res.Request.Title.ID)
	if err != nil || d.Monitored || d.TMDBID != 19 {
		t.Fatalf("catalog row after the ask: %+v, %v", d, err)
	}
	// Asking again for a title already in the catalog adds nothing new.
	e.mustAsk(e.noah, movie(19, QualityHD))
	if n := e.count(`SELECT COUNT(*) FROM titles`); n != 1 {
		t.Errorf("%d titles, want 1", n)
	}

	// What the catalog says about a title it cannot add comes back as it is.
	_, err = e.ask(e.mia, movie(404, QualityHD))
	if !errors.Is(err, metadata.ErrNotFound) {
		t.Errorf("unknown provider id: %v, want metadata not found", err)
	}
	_, err = e.ask(e.mia, AskInput{Kind: KindMovie, Provider: metadata.ProviderTVmaze, ProviderID: "5", Quality: QualityHD})
	var ie *titles.InvalidError
	if !errors.As(err, &ie) {
		t.Errorf("wrong provider for the kind: %v, want a titles.InvalidError", err)
	}
	_, err = e.ask(e.mia, AskInput{TitleID: 9999, Quality: QualityHD})
	if !errors.Is(err, titles.ErrNotFound) {
		t.Errorf("unknown titleId: %v, want titles.ErrNotFound", err)
	}
}

func TestAskInputIsChecked(t *testing.T) {
	e := newEnv(t)
	e.standardFolders()
	long := make([]byte, maxNoteRunes+1)
	for i := range long {
		long[i] = 'x'
	}
	cases := []struct {
		name string
		in   AskInput
		code string
	}{
		{"no quality", AskInput{Kind: KindMovie, Provider: metadata.ProviderTMDB, ProviderID: "1"}, "invalid_quality"},
		{"odd quality", movie(1, "4k"), "invalid_quality"},
		{"no title named", AskInput{Quality: QualityHD}, "invalid_request"},
		{"half a name", AskInput{Kind: KindMovie, Quality: QualityHD}, "invalid_request"},
		{"long note", AskInput{Kind: KindMovie, Provider: metadata.ProviderTMDB, ProviderID: "1", Quality: QualityHD, Note: string(long)}, "invalid_request"},
		{"4K music", AskInput{Kind: KindArtist, Provider: metadata.ProviderMusicBrainz, ProviderID: chopinMBID, Quality: QualityUHD}, "invalid_quality"},
	}
	for _, c := range cases {
		_, err := e.ask(e.mia, c.in)
		var ie *InvalidError
		if !errors.As(err, &ie) || ie.Code != c.code || ie.Message == "" {
			t.Errorf("%s: %v, want an InvalidError %q", c.name, err, c.code)
		}
	}
	if _, err := e.svc.Ask(bg(), Actor{UserID: e.mia.ID, Role: "member", Source: "carrier-pigeon"}, movie(1, QualityHD)); !isInvalid(err) {
		t.Errorf("odd source: %v", err)
	}
	if _, err := e.svc.Ask(bg(), Actor{Role: "member"}, movie(1, QualityHD)); !errors.Is(err, ErrForbidden) {
		t.Errorf("no user: %v", err)
	}
	if n := e.count(`SELECT COUNT(*) FROM requests`); n != 0 {
		t.Errorf("%d requests stored from bad asks", n)
	}
}

func TestAskSourceIsRecorded(t *testing.T) {
	e := newEnv(t)
	e.standardFolders()
	res, err := e.svc.Ask(bg(), Actor{UserID: e.mia.ID, Role: "member", Source: SourceMCP}, movie(1, QualityHD))
	if err != nil || res.Request.Source != SourceMCP {
		t.Fatalf("mcp ask: %+v, %v", res, err)
	}
	if res = e.mustAsk(e.mia, movie(2, QualityHD)); res.Request.Source != SourceManual {
		t.Errorf("default source = %q", res.Request.Source)
	}
}

// ---- scope -----------------------------------------------------------------

func TestScopeOfShows(t *testing.T) {
	e := newEnv(t)
	e.standardFolders()

	// Seasons are sorted and listed once.
	res := e.mustAsk(e.owner, show(99001, QualityHD, Scope{Seasons: []int{2, 1, 2}}))
	if got := res.Request.Scope; got.All || len(got.Seasons) != 2 || got.Seasons[0] != 1 || got.Seasons[1] != 2 {
		t.Errorf("scope = %+v", got)
	}

	// Nothing named means all.
	if got := e.mustAsk(e.mia, show(7, QualityHD, Scope{})).Request.Scope; !got.All || len(got.Seasons) != 0 {
		t.Errorf("empty show scope = %+v, want all", got)
	}
	// A movie has no scope.
	movieAsk := movie(1, QualityHD)
	movieAsk.Scope = Scope{All: true}
	if got := e.mustAsk(e.mia, movieAsk).Request.Scope; got.All {
		t.Errorf("movie scope = %+v, want {}", got)
	}

	// Mistakes.
	for name, sc := range map[string]Scope{
		"no such season": {Seasons: []int{9}},
		"negative":       {Seasons: []int{-1}},
		"all and some":   {All: true, Seasons: []int{1}},
		"albums on show": {Albums: []int64{1}},
	} {
		_, err := e.ask(e.noah, show(99001, QualityUHD, sc))
		var ie *InvalidError
		if !errors.As(err, &ie) || ie.Code != "invalid_scope" {
			t.Errorf("%s: %v, want an InvalidError invalid_scope", name, err)
		}
	}
	if n := e.count(`SELECT COUNT(*) FROM requests WHERE user_id = ?`, e.noah.ID); n != 0 {
		t.Errorf("%d requests stored from bad scopes", n)
	}
}

func TestSeasonScopeMonitorsOnlyThoseSeasons(t *testing.T) {
	e := newEnv(t)
	e.standardFolders()
	r := e.mustAsk(e.owner, show(99001, QualityHD, Scope{Seasons: []int{2}}))
	d, err := e.cat.Get(bg(), r.Request.Title.ID)
	if err != nil {
		t.Fatal(err)
	}
	if !d.Monitored {
		t.Error("the show is not monitored")
	}
	for _, s := range d.Seasons {
		if want := s.Number == 2; s.Monitored != want {
			t.Errorf("season %d monitored = %v, want %v", s.Number, s.Monitored, want)
		}
	}
	for _, ep := range d.Episodes {
		if want := ep.Season == 2; ep.Monitored != want {
			t.Errorf("episode S%dE%d monitored = %v, want %v", ep.Season, ep.Number, ep.Monitored, want)
		}
	}

	// Asking for season 1 as well, at 4K, adds to what is monitored.
	e.mustAsk(e.owner, show(99001, QualityUHD, Scope{Seasons: []int{1}}))
	d, _ = e.cat.Get(bg(), r.Request.Title.ID)
	for _, s := range d.Seasons {
		if want := s.Number > 0; s.Monitored != want {
			t.Errorf("after the second ask: season %d monitored = %v, want %v", s.Number, s.Monitored, want)
		}
	}
}

func TestScopeOfArtists(t *testing.T) {
	e := newEnv(t)
	e.standardFolders()
	art := AskInput{Kind: KindArtist, Provider: metadata.ProviderMusicBrainz, ProviderID: chopinMBID, Quality: QualityHD}
	art.Scope = Scope{Albums: []int64{99999}}
	if _, err := e.ask(e.noah, art); !isInvalid(err) {
		t.Errorf("unknown album: %v", err)
	}
	art.Scope = Scope{Seasons: []int{1}}
	if _, err := e.ask(e.noah, art); !isInvalid(err) {
		t.Errorf("seasons on an artist: %v", err)
	}
	art.Scope = Scope{}
	if got := e.mustAsk(e.noah, art).Request.Scope; !got.All {
		t.Errorf("artist scope = %+v, want all", got)
	}
}

func TestArtistAlbumScopeMonitorsOnlyThoseAlbums(t *testing.T) {
	e := newEnv(t)
	e.standardFolders()
	d, _, err := e.cat.Add(bg(), KindArtist, metadata.ProviderMusicBrainz, chopinMBID, titles.AddOptions{Monitored: false})
	if err != nil {
		t.Fatal(err)
	}
	pick := d.Albums[0].ID
	res := e.mustAsk(e.owner, AskInput{TitleID: d.ID, Quality: QualityHD, Scope: Scope{Albums: []int64{pick}}})
	if len(res.Request.Scope.Albums) != 1 || res.Request.Scope.Albums[0] != pick {
		t.Fatalf("scope = %+v", res.Request.Scope)
	}
	got, _ := e.cat.Get(bg(), d.ID)
	if !got.Monitored {
		t.Error("the artist is not monitored")
	}
	for _, al := range got.Albums {
		if want := al.ID == pick; al.Monitored != want {
			t.Errorf("album %q monitored = %v, want %v", al.Name, al.Monitored, want)
		}
	}
}

// ---- folders ---------------------------------------------------------------

func TestApproveMakesSeparateCopiesInTheirOwnFolders(t *testing.T) {
	e := newEnv(t)
	movies, movies4k, shows, music := e.standardFolders()

	hd := e.mustAsk(e.owner, movie(1, QualityHD)).Request
	uhd := e.mustAsk(e.owner, movie(1, QualityUHD)).Request
	if hd.Title.ID != uhd.Title.ID {
		t.Fatal("the two asks are for different titles")
	}
	if hd.Facts.FolderPath != movies.Path || uhd.Facts.FolderPath != movies4k.Path {
		t.Errorf("folders: hd %q uhd %q, want %q and %q", hd.Facts.FolderPath, uhd.Facts.FolderPath, movies.Path, movies4k.Path)
	}
	var folders [2]int64
	rows, err := e.db.Query(`SELECT quality, folder_id FROM copies WHERE title_id = ? ORDER BY quality`, hd.Title.ID)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	i := 0
	for rows.Next() {
		var q string
		if err := rows.Scan(&q, &folders[i]); err != nil {
			t.Fatal(err)
		}
		i++
	}
	if i != 2 || folders[0] != movies.ID || folders[1] != movies4k.ID {
		t.Errorf("copy folders = %v (%d rows), want hd in %d and uhd in %d", folders, i, movies.ID, movies4k.ID)
	}

	if got := e.mustAsk(e.owner, show(7, QualityHD, Scope{})).Request.Facts.FolderPath; got != shows.Path {
		t.Errorf("show folder = %q, want %q", got, shows.Path)
	}
	art := e.mustAsk(e.owner, AskInput{Kind: KindArtist, Provider: metadata.ProviderMusicBrainz, ProviderID: chopinMBID, Quality: QualityHD})
	if art.Request.Facts.FolderPath != music.Path {
		t.Errorf("artist folder = %q, want %q", art.Request.Facts.FolderPath, music.Path)
	}
}

func TestFolderChoices(t *testing.T) {
	e := newEnv(t)
	first := e.addFolder("movie", "Films", "films")
	second := e.addFolder("movie", "More films", "more-films")
	if res := e.mustAsk(e.owner, movie(1, QualityHD)); res.Request.Facts.FolderPath != first.Path {
		t.Errorf("no setting: first folder expected, got %q", res.Request.Facts.FolderPath)
	}
	// No 4K-looking folder: the 4K copy shares the HD copy's folder.
	if res := e.mustAsk(e.owner, movie(1, QualityUHD)); res.Request.Facts.FolderPath != first.Path {
		t.Errorf("4K with no 4K folder: %q, want %q", res.Request.Facts.FolderPath, first.Path)
	}

	// A folder added later that has "uhd" in its path is picked for 4K.
	uhdFolder := e.addFolder("movie", "", "UHD-releases")
	if res := e.mustAsk(e.owner, movie(2, QualityUHD)); res.Request.Facts.FolderPath != uhdFolder.Path {
		t.Errorf("4K with a uhd folder: %q, want %q", res.Request.Facts.FolderPath, uhdFolder.Path)
	}
	// ...and HD does not take the 4K folder for the first folder.
	if res := e.mustAsk(e.owner, movie(3, QualityHD)); res.Request.Facts.FolderPath != first.Path {
		t.Errorf("HD: %q, want %q", res.Request.Facts.FolderPath, first.Path)
	}

	// The owner's choice wins, for each quality.
	c := DefaultConfig()
	c.Folders.Movie.HD, c.Folders.Movie.UHD = &second.ID, &second.ID
	if err := SaveConfig(bg(), e.cfg, c); err != nil {
		t.Fatal(err)
	}
	for _, q := range []string{QualityHD, QualityUHD} {
		if res := e.mustAsk(e.owner, movie(4, q)); res.Request.Facts.FolderPath != second.Path {
			t.Errorf("%s with a chosen folder: %q, want %q", q, res.Request.Facts.FolderPath, second.Path)
		}
	}
	// A choice that is no longer a movie folder is ignored.
	shows := e.addFolder("show", "Shows", "shows")
	c.Folders.Movie.HD = &shows.ID
	if err := SaveConfig(bg(), e.cfg, c); err != nil {
		t.Fatal(err)
	}
	if res := e.mustAsk(e.owner, movie(5, QualityHD)); res.Request.Facts.FolderPath != first.Path {
		t.Errorf("a stale choice: %q, want %q", res.Request.Facts.FolderPath, first.Path)
	}
}

func TestNoLibraryFolder(t *testing.T) {
	e := newEnv(t)
	// The owner's ask cannot be approved, so it is refused, and nothing is made.
	if _, err := e.ask(e.owner, movie(1, QualityHD)); !errors.Is(err, ErrNoFolder) {
		t.Fatalf("owner, no folders: %v, want ErrNoFolder", err)
	}
	if n := e.count(`SELECT COUNT(*) FROM requests`); n != 0 {
		t.Errorf("%d requests stored", n)
	}
	// A member's ask waits instead; the owner finds out when answering.
	res := e.mustAsk(e.mia, movie(1, QualityHD))
	if res.AutoApproved || res.Request.State != StatePending || res.Request.Facts.FolderPath != "" {
		t.Fatalf("member, no folders: %+v", res)
	}
	if _, err := e.svc.Approve(bg(), actorOf(e.owner), res.Request.ID, ApproveOptions{}); !errors.Is(err, ErrNoFolder) {
		t.Fatalf("approving with no folder: %v, want ErrNoFolder", err)
	}
	if it, _ := e.svc.Get(bg(), actorOf(e.owner), res.Request.ID); it.State != StatePending {
		t.Errorf("state after a failed approval = %q", it.State)
	}
	e.addFolder("movie", "Movies", "movies")
	if _, err := e.svc.Approve(bg(), actorOf(e.owner), res.Request.ID, ApproveOptions{}); err != nil {
		t.Errorf("approving once there is a folder: %v", err)
	}
}

func TestACopyWhoseFolderWasRemovedGetsOneAgain(t *testing.T) {
	e := newEnv(t)
	first := e.addFolder("movie", "Movies", "movies")
	res := e.mustAsk(e.owner, movie(1, QualityHD))
	if err := e.lib.RemoveFolder(bg(), first.ID); err != nil {
		t.Fatal(err)
	}
	var copyID int64
	if err := e.db.QueryRow(`SELECT id FROM copies`).Scan(&copyID); err != nil {
		t.Fatal(err)
	}
	cp, err := e.svc.Copy(bg(), copyID)
	if err != nil || cp.FolderID != 0 || cp.FolderPath != "" {
		t.Fatalf("copy with no folder: %+v, %v", cp, err)
	}
	if got, _ := e.svc.Get(bg(), actorOf(e.owner), res.Request.ID); got.Facts.FolderPath != "" {
		t.Errorf("request facts = %+v", got.Facts)
	}
	again := e.addFolder("movie", "New movies", "new-movies")
	cp, err = e.svc.Copy(bg(), copyID)
	if err != nil || cp.FolderID != again.ID || cp.FolderPath != again.Path {
		t.Fatalf("copy after a folder came back: %+v, %v", cp, err)
	}
	if _, err := e.svc.Copy(bg(), 999); !errors.Is(err, ErrCopyNotFound) {
		t.Errorf("Copy(999) = %v, want ErrCopyNotFound", err)
	}
}

func TestEnsureCopy(t *testing.T) {
	e := newEnv(t)
	_, movies4k, _, _ := e.standardFolders()
	d, _, err := e.cat.Add(bg(), KindMovie, metadata.ProviderTMDB, "1", titles.AddOptions{})
	if err != nil {
		t.Fatal(err)
	}
	cp, err := e.svc.EnsureCopy(bg(), d.ID, QualityUHD)
	if err != nil || cp.Quality != QualityUHD || cp.FolderPath != movies4k.Path || !cp.Monitored || cp.Kind != KindMovie {
		t.Fatalf("EnsureCopy = %+v, %v", cp, err)
	}
	again, err := e.svc.EnsureCopy(bg(), d.ID, QualityUHD)
	if err != nil || again.ID != cp.ID {
		t.Errorf("second EnsureCopy = %+v, %v; want the same copy", again, err)
	}
	if _, err := e.svc.EnsureCopy(bg(), d.ID, "8k"); !isInvalid(err) {
		t.Errorf("bad quality: %v", err)
	}
	if _, err := e.svc.EnsureCopy(bg(), 999, QualityHD); !errors.Is(err, titles.ErrNotFound) {
		t.Errorf("no such title: %v", err)
	}
}

// ---- answering -------------------------------------------------------------

func TestDecline(t *testing.T) {
	e := newEnv(t)
	e.standardFolders()
	sub, unsub := e.hub.Subscribe(Topic)
	defer unsub()
	sr := newFakeSearcher()
	e.svc.SetSearcher(sr)

	res := e.mustAsk(e.mia, movie(10, QualityUHD))
	<-sub.C() // "created"

	if _, err := e.svc.Decline(bg(), actorOf(e.noah), res.Request.ID, "no"); !errors.Is(err, ErrForbidden) {
		t.Errorf("a member declining: %v, want ErrForbidden", err)
	}
	it, err := e.svc.Decline(bg(), actorOf(e.admin), res.Request.ID, "  Too big for the disk.  ")
	if err != nil {
		t.Fatal(err)
	}
	if it.State != StateDeclined || it.DeclineReason != "Too big for the disk." ||
		it.DecidedBy == nil || it.DecidedBy.ID != e.admin.ID || it.DecidedAt == nil {
		t.Errorf("declined = %+v", it)
	}
	select {
	case m := <-sub.C():
		if string(m.Data) != `{"event":"declined","id":`+itoa(it.ID)+`,"state":"declined"}` {
			t.Errorf("event data = %s", m.Data)
		}
	case <-time.After(time.Second):
		t.Fatal("no event for the decline")
	}
	// It stays declined: no copy, no search, no second answer.
	if n := e.count(`SELECT COUNT(*) FROM copies`); n != 0 {
		t.Errorf("%d copies after a decline", n)
	}
	if _, err := e.svc.Approve(bg(), actorOf(e.owner), it.ID, ApproveOptions{}); !errors.Is(err, ErrNotPending) {
		t.Errorf("approving a declined ask: %v, want ErrNotPending", err)
	}
	if _, err := e.svc.Decline(bg(), actorOf(e.owner), it.ID, ""); !errors.Is(err, ErrNotPending) {
		t.Errorf("declining twice: %v, want ErrNotPending", err)
	}
	if _, err := e.svc.Decline(bg(), actorOf(e.owner), 9999, ""); !errors.Is(err, ErrNotFound) {
		t.Errorf("declining nothing: %v, want ErrNotFound", err)
	}
	if sr.count() != 0 {
		t.Error("searched for a declined ask")
	}
	// The person sees the reason.
	mine, err := e.svc.Get(bg(), actorOf(e.mia), it.ID)
	if err != nil || mine.DeclineReason == "" {
		t.Errorf("the requester's view = %+v, %v", mine, err)
	}
}

func TestApproveTwice(t *testing.T) {
	e := newEnv(t)
	e.standardFolders()
	res := e.mustAsk(e.mia, movie(10, QualityUHD))
	if _, err := e.svc.Approve(bg(), actorOf(e.owner), res.Request.ID, ApproveOptions{}); err != nil {
		t.Fatal(err)
	}
	if _, err := e.svc.Approve(bg(), actorOf(e.admin), res.Request.ID, ApproveOptions{}); !errors.Is(err, ErrNotPending) {
		t.Errorf("second approval: %v, want ErrNotPending", err)
	}
	if _, err := e.svc.Approve(bg(), actorOf(e.owner), 9999, ApproveOptions{}); !errors.Is(err, ErrNotFound) {
		t.Errorf("approving nothing: %v", err)
	}
}

func TestCancel(t *testing.T) {
	e := newEnv(t)
	e.standardFolders()
	sub, unsub := e.hub.Subscribe(Topic)
	defer unsub()

	waiting := e.mustAsk(e.mia, movie(10, QualityUHD)).Request
	approved := e.mustAsk(e.mia, movie(11, QualityHD)).Request // approved by the rules
	<-sub.C()
	<-sub.C()

	// Not theirs: a member is told there is no such request; an admin is refused.
	if _, err := e.svc.Cancel(bg(), actorOf(e.noah), waiting.ID); !errors.Is(err, ErrNotFound) {
		t.Errorf("another member cancelling: %v, want ErrNotFound", err)
	}
	if _, err := e.svc.Cancel(bg(), actorOf(e.admin), waiting.ID); !errors.Is(err, ErrForbidden) {
		t.Errorf("an admin cancelling someone else's: %v, want ErrForbidden", err)
	}
	// Once approved, the requester can no longer cancel it.
	if _, err := e.svc.Cancel(bg(), actorOf(e.mia), approved.ID); !errors.Is(err, ErrNotCancellable) {
		t.Errorf("cancelling an approved ask: %v, want ErrNotCancellable", err)
	}
	// The requester can, while it waits.
	it, err := e.svc.Cancel(bg(), actorOf(e.mia), waiting.ID)
	if err != nil || it.State != StateCancelled {
		t.Fatalf("cancel = %+v, %v", it, err)
	}
	select {
	case m := <-sub.C():
		if string(m.Data) != `{"event":"cancelled","id":`+itoa(it.ID)+`,"state":"cancelled"}` {
			t.Errorf("event data = %s", m.Data)
		}
	case <-time.After(time.Second):
		t.Fatal("no event for the cancel")
	}
	if _, err := e.svc.Cancel(bg(), actorOf(e.mia), waiting.ID); !errors.Is(err, ErrNotCancellable) {
		t.Errorf("cancelling twice: %v", err)
	}
	if _, err := e.svc.Approve(bg(), actorOf(e.owner), waiting.ID, ApproveOptions{}); !errors.Is(err, ErrNotPending) {
		t.Errorf("approving a cancelled ask: %v", err)
	}
	// The owner may cancel an approved one; its copy stays.
	it, err = e.svc.Cancel(bg(), actorOf(e.owner), approved.ID)
	if err != nil || it.State != StateCancelled {
		t.Fatalf("owner cancel = %+v, %v", it, err)
	}
	if n := e.count(`SELECT COUNT(*) FROM copies`); n != 1 {
		t.Errorf("%d copies after the owner cancelled; the copy should stay", n)
	}
	if _, err := e.svc.Cancel(bg(), actorOf(e.owner), 9999); !errors.Is(err, ErrNotFound) {
		t.Errorf("cancelling nothing: %v", err)
	}
	// Once declined, even the owner cannot cancel.
	other := e.mustAsk(e.noah, movie(12, QualityUHD)).Request
	if _, err := e.svc.Decline(bg(), actorOf(e.owner), other.ID, ""); err != nil {
		t.Fatal(err)
	}
	if _, err := e.svc.Cancel(bg(), actorOf(e.owner), other.ID); !errors.Is(err, ErrNotCancellable) {
		t.Errorf("cancelling a declined ask: %v", err)
	}
}

func TestSetState(t *testing.T) {
	e := newEnv(t)
	e.standardFolders()
	sub, unsub := e.hub.Subscribe(Topic)
	defer unsub()
	approved := e.mustAsk(e.owner, movie(1, QualityHD)).Request
	waiting := e.mustAsk(e.mia, movie(2, QualityUHD)).Request
	<-sub.C()
	<-sub.C()

	for _, st := range []string{StatePartial, StateFailed, StateAvailable, StateApproved} {
		if err := e.svc.SetState(bg(), approved.ID, st); err != nil {
			t.Fatalf("SetState(%s): %v", st, err)
		}
		it, _ := e.svc.Get(bg(), actorOf(e.owner), approved.ID)
		if it.State != st {
			t.Errorf("state = %q, want %q", it.State, st)
		}
		select {
		case <-sub.C():
		case <-time.After(time.Second):
			t.Fatalf("no event for %s", st)
		}
	}
	if err := e.svc.SetState(bg(), waiting.ID, StateAvailable); !errors.Is(err, ErrBadState) {
		t.Errorf("a waiting ask cannot become available: %v", err)
	}
	if err := e.svc.SetState(bg(), approved.ID, StateDeclined); !errors.Is(err, ErrBadState) {
		t.Errorf("target declined: %v", err)
	}
	if err := e.svc.SetState(bg(), 9999, StateFailed); !errors.Is(err, ErrNotFound) {
		t.Errorf("no such request: %v", err)
	}
}

// ---- the searcher ----------------------------------------------------------

func TestSearcherIsCalledOnApprove(t *testing.T) {
	e := newEnv(t)
	e.standardFolders()

	// No searcher yet: approving just works.
	if res := e.mustAsk(e.owner, movie(1, QualityHD)); res.Request.State != StateApproved {
		t.Fatalf("without a searcher: %+v", res.Request)
	}

	sr := newFakeSearcher()
	e.svc.SetSearcher(sr)
	waiting := e.mustAsk(e.mia, movie(2, QualityUHD)).Request
	if sr.count() != 0 {
		t.Fatal("searched for an ask that is still waiting")
	}
	if _, err := e.svc.Approve(bg(), actorOf(e.owner), waiting.ID, ApproveOptions{}); err != nil {
		t.Fatal(err)
	}
	copyID := sr.wait(t)
	cp, err := e.svc.Copy(bg(), copyID)
	if err != nil || cp.TitleID != waiting.Title.ID || cp.Quality != QualityUHD {
		t.Errorf("the searcher was given copy %d = %+v, %v", copyID, cp, err)
	}
	// An approval by the rules searches too, and a failing searcher changes nothing.
	sr.err = errors.New("no sources yet")
	res := e.mustAsk(e.mia, movie(3, QualityHD))
	sr.wait(t)
	if res.Request.State != StateApproved {
		t.Errorf("a failing searcher broke the approval: %+v", res.Request)
	}
	// Taking it away again.
	e.svc.SetSearcher(nil)
	e.mustAsk(e.mia, movie(4, QualityHD))
	time.Sleep(50 * time.Millisecond)
	if sr.count() != 2 {
		t.Errorf("searcher called %d times, want 2", sr.count())
	}
}

func TestCloseWaitsForSearches(t *testing.T) {
	e := newEnv(t)
	e.standardFolders()
	release := make(chan struct{})
	done := make(chan struct{})
	e.svc.SetSearcher(blockingSearcher{release: release, done: done})
	e.mustAsk(e.owner, movie(1, QualityHD))

	closed := make(chan struct{})
	go func() { e.svc.Close(); close(closed) }()
	select {
	case <-done: // the search saw its context cancelled by Close
	case <-time.After(3 * time.Second):
		t.Fatal("Close did not cancel the running search")
	}
	select {
	case <-closed:
	case <-time.After(3 * time.Second):
		t.Fatal("Close did not return")
	}
	// After Close nothing new starts.
	e.svc.search(1)
}

type blockingSearcher struct {
	release chan struct{}
	done    chan struct{}
}

func (b blockingSearcher) SearchCopy(ctx context.Context, _ int64) error {
	select {
	case <-ctx.Done():
	case <-b.release:
	}
	close(b.done)
	return ctx.Err()
}
