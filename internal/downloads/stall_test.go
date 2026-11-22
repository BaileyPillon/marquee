package downloads

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/BaileyPillon/marquee/internal/settings"
)

// ---- helpers -------------------------------------------------------------------

type stallCall struct {
	id      int64
	reason  string
	removed bool
}

// stallRecorder is the stalled handler the importer is in the real program.
type stallRecorder struct {
	mu      sync.Mutex
	calls   []stallCall
	doPanic bool
}

func (r *stallRecorder) OnStalled(_ context.Context, id int64, reason string, removed bool) {
	r.mu.Lock()
	r.calls = append(r.calls, stallCall{id, reason, removed})
	r.mu.Unlock()
	if r.doPanic {
		panic("importer exploded")
	}
}

func (r *stallRecorder) got() []stallCall {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]stallCall(nil), r.calls...)
}

// stallEnv is a service with one qBittorrent client and one grabbed torrent.
type stallEnv struct {
	*env
	fake *fakeAdapter
	id   int64
	ref  string
	rec  *stallRecorder
	ev   *collector
}

func newStallEnv(t *testing.T) *stallEnv {
	t.Helper()
	e := newEnv(t)
	e.addClient("qb", KindQBittorrent, "http://qb:8080")
	s := &stallEnv{env: e, fake: e.fake.at("http://qb:8080"), rec: &stallRecorder{}}
	s.id, s.ref = e.grabOne("http://qb:8080", bbbHex)
	e.svc.SetStalledHandler(s.rec)
	s.ev = e.collect()
	return s
}

// settings stores the "downloads" section the way the Settings screen does.
func (s *stallEnv) settings(v Settings) {
	s.t.Helper()
	cfg := settings.New(s.db, s.box)
	if err := settings.SetAs(context.Background(), cfg, SettingsSection, v); err != nil {
		s.t.Fatal(err)
	}
	s.svc.cfg = cfg
}

// poll reports one item to the service and polls once.
func (s *stallEnv) poll(it Item) {
	s.t.Helper()
	it.Ref = s.ref
	s.fake.setItems(it)
	if err := s.svc.Poll(context.Background()); err != nil {
		s.t.Fatal(err)
	}
}

// pollFor polls once a minute for the given minutes, reporting the same item
// each time, and returns the minute at which the download stopped being active
// (0 if it never did).
func (s *stallEnv) pollFor(minutes int, it Item) int {
	s.t.Helper()
	for m := 1; m <= minutes; m++ {
		s.advance(time.Minute)
		s.poll(it)
		if st := s.download(s.id).State; st == StateFailed {
			return m
		}
	}
	return 0
}

func downloading(progress float64) Item {
	return Item{State: StateDownloading, Progress: progress, Size: 5000, ETA: -1}
}

func stalledAt(progress float64) Item {
	return Item{State: StateStalled, Progress: progress, Size: 5000, ETA: -1}
}

// ---- tests ---------------------------------------------------------------------

func TestADownloadThatMakesNoProgressFailsAfterThirtyMinutes(t *testing.T) {
	s := newStallEnv(t)
	s.poll(stalledAt(0.9997)) // the Internet Archive case: qBittorrent says stalledDL, 99.97%
	if d := s.download(s.id); d.State != StateStalled || d.Progress != 0.9997 {
		t.Fatalf("after the first poll: %+v", d)
	}
	s.ev.drain()

	// Twenty-nine minutes of the same progress: still waiting.
	if m := s.pollFor(29, stalledAt(0.9997)); m != 0 {
		t.Fatalf("gave up after %d minutes, want 30", m)
	}
	if len(s.rec.got()) != 0 {
		t.Fatal("the handler was told before the time was up")
	}

	// The thirtieth minute.
	s.advance(time.Minute)
	s.poll(stalledAt(0.9997))
	d := s.download(s.id)
	want := "Stuck at 99.97% for 30 minutes. The source can't finish it (often an Internet Archive torrent whose info files changed)."
	if d.State != StateFailed || d.Error != want {
		t.Fatalf("state = %s, error = %q\nwant failed and %q", d.State, d.Error, want)
	}
	if d.Progress != 0.9997 {
		t.Errorf("the progress it stopped at = %v", d.Progress)
	}

	calls := s.rec.got()
	if len(calls) != 1 || calls[0].id != s.id || calls[0].reason != want || !calls[0].removed {
		t.Fatalf("handler calls = %+v, want exactly one for download %d", calls, s.id)
	}
	// The data went with it by default.
	if len(s.fake.removed) != 1 || s.fake.removed[0] != s.ref || !s.fake.deleted[0] {
		t.Errorf("removed from the client: %v (deleteData %v), want the torrent and its data", s.fake.removed, s.fake.deleted)
	}
	if got := types(s.ev.drain()); len(got) != 1 || got[0] != "failed" {
		t.Errorf("events = %v, want one failed", got)
	}

	// Failed is final: more polls (and a client that still lists it) change nothing.
	for i := 0; i < 5; i++ {
		s.advance(time.Minute)
		s.poll(stalledAt(0.9997))
	}
	if n := len(s.rec.got()); n != 1 {
		t.Errorf("the handler was told %d times", n)
	}
	if len(s.fake.removed) != 1 {
		t.Errorf("removed %d times", len(s.fake.removed))
	}
	if e := s.download(s.id).Error; e != want {
		t.Errorf("error changed to %q", e)
	}
}

func TestGrowingProgressStartsTheStallClockOver(t *testing.T) {
	s := newStallEnv(t)
	s.poll(downloading(0.50))
	s.advance(20 * time.Minute)
	s.poll(downloading(0.50)) // 20 minutes without progress: not yet
	if s.download(s.id).State == StateFailed {
		t.Fatal("failed after 20 minutes")
	}
	s.advance(time.Minute)
	s.poll(downloading(0.51)) // it moved: the clock starts over
	if d := s.download(s.id); d.State != StateDownloading || !d.ProgressChangedAt.Equal(s.clock()) {
		t.Fatalf("after progress: %+v (clock %v)", d, s.clock())
	}

	// 29 minutes after the last growth is still fine; 30 is not.
	if m := s.pollFor(29, downloading(0.51)); m != 0 {
		t.Fatalf("gave up %d minutes after the last growth, want 30", m)
	}
	s.advance(time.Minute)
	s.poll(downloading(0.51))
	d := s.download(s.id)
	if d.State != StateFailed || !strings.HasPrefix(d.Error, "Stuck at 51% for 30 minutes.") {
		t.Fatalf("state = %s, error = %q", d.State, d.Error)
	}
	if n := len(s.rec.got()); n != 1 {
		t.Errorf("handler calls = %d", n)
	}
}

func TestAPausedDownloadNeverStalls(t *testing.T) {
	s := newStallEnv(t)
	s.poll(downloading(0.40))
	s.advance(15 * time.Minute)
	s.poll(stalledAt(0.40))

	// Paused for hours.
	if m := s.pollFor(6*60, Item{State: StatePaused, Progress: 0.40, ETA: -1}); m != 0 {
		t.Fatalf("a paused download failed after %d minutes", m)
	}
	if d := s.download(s.id); d.State != StatePaused {
		t.Fatalf("state = %s", d.State)
	}

	// Resumed: the 15 minutes it had stalled before do not count, nor do the hours
	// it was paused. It gets the whole window again.
	s.advance(time.Minute)
	s.poll(stalledAt(0.40))
	if m := s.pollFor(29, stalledAt(0.40)); m != 0 {
		t.Fatalf("gave up %d minutes after the resume, want 30", m)
	}
	if m := s.pollFor(2, stalledAt(0.40)); m == 0 {
		t.Fatal("a resumed download that stays stuck must still give up")
	}
	if n := len(s.rec.got()); n != 1 {
		t.Errorf("handler calls = %d", n)
	}
}

func TestAQueuedDownloadDoesNotStallAndStartsItsClockWhenItStarts(t *testing.T) {
	s := newStallEnv(t)
	// The client holds it in its queue for two hours.
	if m := s.pollFor(120, Item{State: StateQueued, ETA: -1}); m != 0 {
		t.Fatalf("a queued download failed after %d minutes", m)
	}
	// Then it starts at 0%: the wait does not count against it.
	s.advance(time.Minute)
	s.poll(downloading(0))
	if d := s.download(s.id); d.State != StateDownloading {
		t.Fatalf("state = %s, error = %q", d.State, d.Error)
	}
	if m := s.pollFor(29, downloading(0)); m != 0 {
		t.Fatalf("gave up %d minutes after it started, want 30", m)
	}
	if m := s.pollFor(1, downloading(0)); m != 1 {
		t.Fatal("a download stuck at 0% must give up too")
	}
	if d := s.download(s.id); !strings.HasPrefix(d.Error, "Stuck at 0% for 30 minutes. The source has stopped sending it") {
		t.Errorf("error = %q", d.Error)
	}
}

func TestStallMinutesZeroTurnsItOff(t *testing.T) {
	s := newStallEnv(t)
	s.settings(Settings{StallMinutes: 0, RemoveStalled: true})
	s.poll(stalledAt(0.9997))
	if m := s.pollFor(24*60, stalledAt(0.9997)); m != 0 {
		t.Fatalf("with the check off a download failed after %d minutes", m)
	}
	if d := s.download(s.id); d.State != StateStalled || d.Error != "" {
		t.Errorf("download = %+v", d)
	}
	if len(s.rec.got()) != 0 || len(s.fake.removed) != 0 {
		t.Error("nothing should have been done")
	}
}

func TestAFinishedDownloadIsNotTouched(t *testing.T) {
	s := newStallEnv(t)
	s.poll(downloading(0.9997))
	done := Item{State: StateCompleted, Finished: true, Progress: 1, Size: 5000, ContentPath: "/dl/Movie", SavePath: "/dl"}
	s.advance(time.Minute)
	s.poll(done)
	if d := s.download(s.id); d.State != StateCompleted || d.Progress != 1 {
		t.Fatalf("finished: %+v", d)
	}
	// It seeds for a day; nothing about it is a stall.
	if m := s.pollFor(24*60, done); m != 0 {
		t.Fatalf("a finished download failed after %d minutes", m)
	}
	if d := s.download(s.id); d.State != StateCompleted || d.Error != "" {
		t.Errorf("download = %+v", d)
	}
	if len(s.rec.got()) != 0 || len(s.fake.removed) != 0 {
		t.Errorf("a finished download was handed to the stalled handler or removed: %v %v", s.rec.got(), s.fake.removed)
	}
}

func TestADownloadTheClientIsBusyWithIsNotStalled(t *testing.T) {
	s := newStallEnv(t)
	// Everything is downloaded and the client is moving it (qBittorrent's "moving").
	s.poll(downloading(1))
	if m := s.pollFor(180, downloading(1)); m != 0 {
		t.Fatalf("a fully downloaded one failed after %d minutes", m)
	}
	// SABnzbd unpacking a big post reports a fixed 0.99 and says it is working.
	s.poll(Item{State: StateDownloading, Progress: 0.99, Working: true, ETA: -1})
	if m := s.pollFor(180, Item{State: StateDownloading, Progress: 0.99, Working: true, ETA: -1}); m != 0 {
		t.Fatalf("a download being unpacked failed after %d minutes", m)
	}
	if len(s.rec.got()) != 0 {
		t.Error("the handler was told")
	}
}

func TestRemoveStalledFalseKeepsTheData(t *testing.T) {
	s := newStallEnv(t)
	s.settings(Settings{StallMinutes: 30, RemoveStalled: false})
	s.poll(stalledAt(0.9997))
	if m := s.pollFor(30, stalledAt(0.9997)); m != 30 {
		t.Fatalf("failed at minute %d, want 30", m)
	}
	if d := s.download(s.id); d.State != StateFailed {
		t.Fatalf("state = %s", d.State)
	}
	if len(s.fake.removed) != 0 {
		t.Errorf("removed %v from the client, but the owner wants it kept", s.fake.removed)
	}
	calls := s.rec.got()
	if len(calls) != 1 || calls[0].removed {
		t.Errorf("handler calls = %+v, want one that says nothing was removed", calls)
	}
}

func TestStallSettingsComeFromTheDownloadsSection(t *testing.T) {
	// No section: the defaults (30 minutes, remove).
	s := newStallEnv(t)
	if got := s.svc.settings(context.Background()); got != (Settings{StallMinutes: 30, RemoveStalled: true}) {
		t.Errorf("defaults = %+v", got)
	}

	// A shorter wait.
	s.settings(Settings{StallMinutes: 10, RemoveStalled: true})
	s.poll(downloading(0.3))
	if m := s.pollFor(30, downloading(0.3)); m != 10 {
		t.Errorf("failed at minute %d, want 10", m)
	}

	// A section that names only one field keeps the default for the other.
	cfg := settings.New(s.db, s.box)
	if err := cfg.Set(context.Background(), SettingsSection, map[string]any{"stallMinutes": 45}); err != nil {
		t.Fatal(err)
	}
	s.svc.cfg = cfg
	if got := s.svc.settings(context.Background()); got != (Settings{StallMinutes: 45, RemoveStalled: true}) {
		t.Errorf("partial section = %+v", got)
	}
	// A nonsense value never gives a negative or endless wait.
	if err := cfg.Set(context.Background(), SettingsSection, map[string]any{"stallMinutes": -5, "removeStalled": false}); err != nil {
		t.Fatal(err)
	}
	if got := s.svc.settings(context.Background()); got != (Settings{StallMinutes: 0, RemoveStalled: false}) {
		t.Errorf("negative = %+v", got)
	}
	if err := cfg.Set(context.Background(), SettingsSection, map[string]any{"stallMinutes": 99999999}); err != nil {
		t.Fatal(err)
	}
	if got := s.svc.settings(context.Background()); got.StallMinutes != maxStallMinutes {
		t.Errorf("huge = %+v", got)
	}
}

func TestStallClockIsStoredAndReset(t *testing.T) {
	s := newStallEnv(t)
	start := s.clock()
	if d := s.download(s.id); !d.ProgressChangedAt.Equal(start) {
		t.Fatalf("a new download's clock = %v, want its start %v", d.ProgressChangedAt, start)
	}
	s.advance(5 * time.Minute)
	s.poll(downloading(0.2))
	if d := s.download(s.id); !d.ProgressChangedAt.Equal(s.clock()) {
		t.Errorf("after progress the clock = %v, want %v", d.ProgressChangedAt, s.clock())
	}
	// No progress: the clock stays, though speed and ETA move and are written.
	moved := s.clock()
	s.advance(5 * time.Minute)
	s.poll(Item{State: StateDownloading, Progress: 0.2, SpeedBps: 99, ETA: 600, Size: 5000})
	if d := s.download(s.id); !d.ProgressChangedAt.Equal(moved) || d.SpeedBps != 99 {
		t.Errorf("clock = %v (want %v), speed = %d", d.ProgressChangedAt, moved, d.SpeedBps)
	}

	// A row that is over and is grabbed again (the same torrent) starts clean.
	s.advance(time.Hour)
	if _, err := s.db.Exec(`UPDATE downloads SET state = 'failed' WHERE id = ?`, s.id); err != nil {
		t.Fatal(err)
	}
	title := s.addTitle("Again", 2001)
	s.fake.fixedRef = s.ref // the client hands back the same ref for the same torrent
	id, err := s.svc.Grab(context.Background(), s.target(title), torrentRelease(bbbHex))
	if err != nil || id != s.id {
		t.Fatalf("regrab: id %d err %v (want the row %d reused)", id, err, s.id)
	}
	if d := s.download(id); d.State != StateQueued || !d.ProgressChangedAt.Equal(s.clock()) {
		t.Errorf("regrabbed row = %s, clock %v (want %v)", d.State, d.ProgressChangedAt, s.clock())
	}
}

func TestStallHandlerThatPanicsDoesNotStopThePoll(t *testing.T) {
	s := newStallEnv(t)
	s.rec.doPanic = true
	s.poll(stalledAt(0.9997))
	if m := s.pollFor(30, stalledAt(0.9997)); m != 30 {
		t.Fatalf("failed at minute %d, want 30", m)
	}
	if n := len(s.rec.got()); n != 1 {
		t.Errorf("handler calls = %d", n)
	}
}

func TestStallWithoutAHandlerStillFailsTheDownload(t *testing.T) {
	s := newStallEnv(t)
	s.svc.SetStalledHandler(nil)
	s.poll(stalledAt(0.5))
	if m := s.pollFor(30, stalledAt(0.5)); m != 30 {
		t.Fatalf("failed at minute %d, want 30", m)
	}
	if d := s.download(s.id); d.State != StateFailed || !strings.Contains(d.Error, "Stuck at 50%") {
		t.Errorf("download = %+v", d)
	}
}

func TestAStalledDownloadThatCannotBeRemovedStillFailsAndSearchesAgain(t *testing.T) {
	s := newStallEnv(t)
	s.fake.actErr = errors.New("boom")
	s.poll(stalledAt(0.9997))
	if m := s.pollFor(30, stalledAt(0.9997)); m != 30 {
		t.Fatalf("failed at minute %d, want 30", m)
	}
	calls := s.rec.got()
	if len(calls) != 1 || calls[0].removed {
		t.Errorf("handler calls = %+v, want one that says it was not removed", calls)
	}
}

func TestStallText(t *testing.T) {
	cases := []struct {
		progress float64
		idle     time.Duration
		want     string
	}{
		{0.9997, 30 * time.Minute, "Stuck at 99.97% for 30 minutes. The source can't finish it (often an Internet Archive torrent whose info files changed)."},
		{0.99, 30 * time.Minute, "Stuck at 99.00% for 30 minutes. The source can't finish it (often an Internet Archive torrent whose info files changed)."},
		{0.99999, time.Hour, "Stuck at 99.99% for 60 minutes. The source can't finish it (often an Internet Archive torrent whose info files changed)."}, // never 100%
		{0.9999, 90 * time.Minute, "Stuck at 99.99% for 90 minutes. The source can't finish it (often an Internet Archive torrent whose info files changed)."},
		{0.985, 45 * time.Minute, "Stuck at 98% for 45 minutes. The source has stopped sending it (no seeds or sources are left)."},
		{0.4, time.Minute, "Stuck at 40% for 1 minute. The source has stopped sending it (no seeds or sources are left)."},
		{0, 3 * time.Hour, "Stuck at 0% for 3 hours. The source has stopped sending it (no seeds or sources are left)."},
	}
	for _, c := range cases {
		if got := stallText(c.progress, c.idle); got != c.want {
			t.Errorf("stallText(%v, %v) =\n%q\nwant\n%q", c.progress, c.idle, got, c.want)
		}
	}
}

func TestNormalizeSettings(t *testing.T) {
	out, err := NormalizeSettings(map[string]any{"stallMinutes": float64(45), "removeStalled": false, "extra": "kept"})
	if err != nil || out["stallMinutes"] != 45 || out["removeStalled"] != false || out["extra"] != "kept" {
		t.Errorf("good input: %v %v", out, err)
	}
	if out, err := NormalizeSettings(map[string]any{}); err != nil || len(out) != 0 {
		t.Errorf("empty input: %v %v", out, err)
	}
	if out, err := NormalizeSettings(map[string]any{"stallMinutes": float64(0)}); err != nil || out["stallMinutes"] != 0 {
		t.Errorf("zero must be allowed (it turns the check off): %v %v", out, err)
	}
	for _, bad := range []map[string]any{
		{"stallMinutes": float64(-1)},
		{"stallMinutes": 2.5},
		{"stallMinutes": float64(maxStallMinutes + 1)},
		{"stallMinutes": "30"},
		{"removeStalled": "yes"},
		{"removeStalled": float64(1)},
	} {
		_, err := NormalizeSettings(bad)
		var ie *InvalidError
		if !errors.As(err, &ie) || ie.Message == "" {
			t.Errorf("NormalizeSettings(%v) = %v, want an InvalidError", bad, err)
		}
	}
}
