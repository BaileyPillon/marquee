package requests

import (
	"context"
	"errors"
	"reflect"
	"sync"
	"testing"

	"github.com/BaileyPillon/marquee/internal/history"
)

// recorded is a history recorder that keeps what it is given.
type recorded struct {
	mu      sync.Mutex
	entries []history.Entry
	err     error
}

func (r *recorded) Record(_ context.Context, e history.Entry) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.entries = append(r.entries, e)
	return r.err
}

func (r *recorded) events() []string {
	r.mu.Lock()
	defer r.mu.Unlock()
	var out []string
	for _, e := range r.entries {
		out = append(out, e.Event)
	}
	return out
}

func (e *env) stateOf(id int64) string {
	e.t.Helper()
	it, err := e.svc.Get(bg(), actorOf(e.owner), id)
	if err != nil {
		e.t.Fatal(err)
	}
	return it.State
}

func (e *env) copyOf(titleID int64, quality string) int64 {
	e.t.Helper()
	cp, err := e.svc.EnsureCopy(bg(), titleID, quality)
	if err != nil {
		e.t.Fatal(err)
	}
	return cp.ID
}

func TestSettleCopyMovesAMovieRequestToAvailableAndBack(t *testing.T) {
	e := newEnv(t)
	movies, _, _, _ := e.standardFolders()
	res := e.mustAsk(e.owner, movie(19, "hd")) // Metropolis (1927), approved at once
	id, title := res.Request.ID, res.Request.Title.ID
	copyID := e.copyOf(title, "hd")

	events, cancel := e.hub.Subscribe(Topic)
	defer cancel()

	if state, err := e.svc.SettleCopy(bg(), copyID); err != nil || state != StatePartial {
		t.Fatalf("nothing on disk yet: %s, %v", state, err)
	}
	if got := e.stateOf(id); got != StatePartial {
		t.Fatalf("request = %s", got)
	}

	e.touch(movies, "Metropolis (1927)/Metropolis (1927) Bluray-1080p.mkv")
	if state, err := e.svc.SettleCopy(bg(), copyID); err != nil || state != StateAvailable {
		t.Fatalf("the film is there: %s, %v", state, err)
	}
	if got := e.stateOf(id); got != StateAvailable {
		t.Fatalf("request = %s", got)
	}
	var seen []string
loop:
	for {
		select {
		case m := <-events.C():
			seen = append(seen, string(m.Data))
		default:
			break loop
		}
	}
	if len(seen) != 2 {
		t.Errorf("each change should be published: %v", seen)
	}

	// The file goes away: the next settle says partial again.
	e.remove(movies, "Metropolis (1927)/Metropolis (1927) Bluray-1080p.mkv")
	if state, _ := e.svc.SettleCopy(bg(), copyID); state != StatePartial || e.stateOf(id) != StatePartial {
		t.Errorf("after the file is gone: %s / %s", state, e.stateOf(id))
	}
}

func TestSettleCopyReadsTheShowByTheSeasonsAskedFor(t *testing.T) {
	e := newEnv(t)
	_, _, shows, _ := e.standardFolders()
	res := e.mustAsk(e.owner, show(99001, "hd", Scope{Seasons: []int{1}})) // Pioneer One, season 1 only
	id := res.Request.ID
	copyID := e.copyOf(res.Request.Title.ID, "hd")

	e.touch(shows, "Pioneer One (2010)/Season 01/Pioneer One - S01E01 - Earthfall Bluray-1080p.mkv")
	if state, err := e.svc.SettleCopy(bg(), copyID); err != nil || state != StatePartial || e.stateOf(id) != StatePartial {
		t.Fatalf("one of two episodes: %s, %v", state, err)
	}
	e.touch(shows, "Pioneer One (2010)/Season 01/Pioneer One - S01E02 - Anomalies Bluray-1080p.mkv")
	if state, err := e.svc.SettleCopy(bg(), copyID); err != nil || state != StateAvailable || e.stateOf(id) != StateAvailable {
		t.Fatalf("season 1 is complete (season 2 was not asked for): %s, %v", state, err)
	}
}

func TestSettleCopyLeavesWaitingAndDeclinedRequestsAlone(t *testing.T) {
	e := newEnv(t)
	movies, _, _, _ := e.standardFolders()
	e.mustAsk(e.owner, movie(19, "hd")) // creates the copy for Metropolis
	waiting := e.mustAsk(e.mia, movie(19, "uhd"))
	if waiting.Request.State != StatePending {
		t.Fatalf("a member's 4K ask should wait, got %s", waiting.Request.State)
	}
	e.touch(movies, "Metropolis (1927)/Metropolis (1927) Bluray-1080p.mkv")
	if _, err := e.svc.SettleCopy(bg(), e.copyOf(waiting.Request.Title.ID, "hd")); err != nil {
		t.Fatal(err)
	}
	if got := e.stateOf(waiting.Request.ID); got != StatePending {
		t.Errorf("a request that is waiting is not touched, got %s", got)
	}
	if _, err := e.svc.SettleCopy(bg(), 9999); !errors.Is(err, ErrCopyNotFound) {
		t.Errorf("unknown copy: %v", err)
	}
}

func TestSettleCopyOnAnArtist(t *testing.T) {
	e := newEnv(t)
	_, _, _, music := e.standardFolders()
	res := e.mustAsk(e.owner, AskInput{Kind: KindArtist, Provider: "musicbrainz", ProviderID: chopinMBID, Quality: QualityHD})
	copyID := e.copyOf(res.Request.Title.ID, "hd")
	// The artist has two monitored albums; one of them has a file, so the request
	// is partial (TestSettleArtistFollowsTheAlbums goes on to the second album).
	e.touch(music, "Frédéric Chopin/Nocturnes (1998)/01 - Nocturne 1.flac")
	if state, err := e.svc.SettleCopy(bg(), copyID); err != nil || state != StatePartial {
		t.Errorf("%s, %v", state, err)
	}
}

func TestRecorderGetsAsksApprovalsAndDeclines(t *testing.T) {
	e := newEnv(t)
	e.standardFolders()
	rec := &recorded{}
	e.svc.SetRecorder(rec)

	// The owner's own ask is approved at once: requested, then approved.
	own := e.mustAsk(e.owner, movie(19, "hd"))
	if got := rec.events(); !reflect.DeepEqual(got, []string{"requested", "approved"}) {
		t.Fatalf("events = %v", got)
	}
	first := rec.entries[0]
	if first.TitleID != own.Request.Title.ID || first.CopyID == 0 {
		t.Errorf("entry = %+v", first)
	}
	d := first.Detail.(map[string]any)
	if d["user"] != "Bailey" || d["quality"] != "hd" || d["requestId"] != own.Request.ID || d["autoApproved"] != true {
		t.Errorf("requested detail = %v", d)
	}
	if a := rec.entries[1].Detail.(map[string]any); a["auto"] != true {
		t.Errorf("approved detail = %v", a)
	}

	// A member's 4K ask waits (only "requested"); the owner then approves it.
	rec.entries = nil
	waiting := e.mustAsk(e.mia, movie(20, "uhd"))
	if got := rec.events(); !reflect.DeepEqual(got, []string{"requested"}) {
		t.Fatalf("a waiting ask is only requested: %v", got)
	}
	if rec.entries[0].CopyID != 0 {
		t.Error("a waiting ask has no copy yet")
	}
	if _, err := e.svc.Approve(bg(), actorOf(e.owner), waiting.Request.ID, ApproveOptions{}); err != nil {
		t.Fatal(err)
	}
	if got := rec.events(); !reflect.DeepEqual(got, []string{"requested", "approved"}) {
		t.Fatalf("events = %v", got)
	}
	ap := rec.entries[1]
	if ap.CopyID == 0 || ap.Detail.(map[string]any)["by"] != "Bailey" || ap.Detail.(map[string]any)["auto"] != false {
		t.Errorf("approved entry = %+v", ap)
	}

	// A decline.
	rec.entries = nil
	no := e.mustAsk(e.mia, movie(21, "uhd"))
	if _, err := e.svc.Decline(bg(), actorOf(e.admin), no.Request.ID, "Too big"); err != nil {
		t.Fatal(err)
	}
	if got := rec.events(); !reflect.DeepEqual(got, []string{"requested", "declined"}) {
		t.Fatalf("events = %v", got)
	}
	dec := rec.entries[1].Detail.(map[string]any)
	if dec["reason"] != "Too big" || dec["by"] != "Alice" {
		t.Errorf("declined detail = %v", dec)
	}
}

func TestARecorderThatFailsOrIsMissingNeverBreaksAnAsk(t *testing.T) {
	e := newEnv(t)
	e.standardFolders()
	if _, err := e.ask(e.owner, movie(19, "hd")); err != nil { // no recorder at all
		t.Fatal(err)
	}
	e.svc.SetRecorder(&recorded{err: errors.New("the history is broken")})
	if _, err := e.ask(e.owner, movie(20, "hd")); err != nil {
		t.Fatalf("a failing history must not fail the ask: %v", err)
	}
	e.svc.SetRecorder(nil)
	if _, err := e.ask(e.owner, movie(21, "hd")); err != nil {
		t.Fatal(err)
	}
}
