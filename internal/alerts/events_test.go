package alerts

import (
	"context"
	"encoding/json"
	"fmt"
	"reflect"
	"sort"
	"strings"
	"testing"
	"time"
)

// messages are what the webhook channels were sent, as event -> message.
func (e *env) messages(path string) []map[string]any {
	e.t.Helper()
	var out []map[string]any
	for _, h := range e.rec.all() {
		if h.Path == path {
			var m map[string]any
			if err := json.Unmarshal(h.Body, &m); err != nil {
				e.t.Fatalf("not JSON: %q", h.Body)
			}
			out = append(out, m)
		}
	}
	return out
}

func (e *env) waitMessages(path string, n int) []map[string]any {
	e.t.Helper()
	waitFor(e.t, func() bool { return len(e.messages(path)) >= n })
	return e.messages(path)
}

// ---- who gets what -----------------------------------------------------------------

func TestAnEventGoesToEnabledChannelsThatWantIt(t *testing.T) {
	e := newEnv(t)
	e.webhookChannel("all")
	e.webhookChannel("created-only", EventRequestCreated)
	e.webhookChannel("failures", EventDownloadFailed, EventImportFailed)
	off := e.webhookChannel("off")
	if _, err := e.svc.Update(bg(), off.ID, Input{Enabled: ptr(false)}); err != nil {
		t.Fatal(err)
	}
	e.webhookChannel("none", []string{}...)

	e.svc.Notify(bg(), Event{Type: EventRequestCreated, Title: "New ask", Body: "x", Key: "1"})
	e.svc.Wait()
	counts := map[string]int{}
	for _, h := range e.rec.all() {
		counts[h.Path]++
	}
	if want := map[string]int{"/hook/all": 1, "/hook/created-only": 1}; !reflect.DeepEqual(counts, want) {
		t.Errorf("after request.created: %v, want %v", counts, want)
	}

	e.rec.reset()
	e.svc.Notify(bg(), Event{Type: EventDownloadFailed, Title: "Download failed", Body: "y", Key: "2"})
	e.svc.Wait()
	counts = map[string]int{}
	for _, h := range e.rec.all() {
		counts[h.Path]++
	}
	if want := map[string]int{"/hook/all": 1, "/hook/failures": 1}; !reflect.DeepEqual(counts, want) {
		t.Errorf("after download.failed: %v, want %v", counts, want)
	}

	// An event of a kind that does not exist goes nowhere.
	e.rec.reset()
	e.svc.Notify(bg(), Event{Type: "the.moon", Body: "z", Key: "3"})
	e.svc.Wait()
	if len(e.rec.all()) != 0 {
		t.Error("an unknown event was sent")
	}
}

func TestTheSameEventIsNotSentTwiceToAChannelWithinTenMinutes(t *testing.T) {
	e := newEnv(t)
	e.webhookChannel("a")
	e.webhookChannel("b")
	ev := Event{Type: EventRequestCreated, Title: "New ask", Body: "x", Key: "7"}

	e.svc.Notify(bg(), ev)
	e.svc.Notify(bg(), ev) // the same thing again at once
	e.svc.Wait()
	if n := len(e.rec.all()); n != 2 { // once for each channel
		t.Fatalf("%d sends, want 2 (one per channel)", n)
	}

	// Still within the window: nothing.
	e.clock.Advance(9*time.Minute + 59*time.Second)
	e.svc.Notify(bg(), ev)
	e.svc.Wait()
	if n := len(e.rec.all()); n != 2 {
		t.Errorf("%d sends after 9:59, want still 2", n)
	}

	// A different thing is not the same event; neither is the same thing as another type.
	e.svc.Notify(bg(), Event{Type: EventRequestCreated, Title: "New ask", Body: "x", Key: "8"})
	e.svc.Notify(bg(), Event{Type: EventRequestApproved, Title: "Approved", Body: "x", Key: "7"})
	e.svc.Wait()
	if n := len(e.rec.all()); n != 6 {
		t.Errorf("%d sends after two different events, want 6", n)
	}

	// Past the window it is sent again.
	e.clock.Advance(2 * time.Second)
	e.svc.Notify(bg(), ev)
	e.svc.Wait()
	if n := len(e.rec.all()); n != 8 {
		t.Errorf("%d sends after the window, want 8", n)
	}
}

func TestDedupeIsPerChannelAndAnEditedChannelStartsFresh(t *testing.T) {
	e := newEnv(t)
	a := e.webhookChannel("a", EventHealthProblem)
	ev := Event{Type: EventHealthProblem, Title: "Problem", Body: "x", Key: "disk"}
	e.svc.Notify(bg(), ev)
	e.svc.Wait()
	b := e.webhookChannel("b", EventHealthProblem) // made later: it has not had it yet
	e.svc.Notify(bg(), ev)
	e.svc.Wait()
	paths := map[string]int{}
	for _, h := range e.rec.all() {
		paths[h.Path]++
	}
	if paths["/hook/a"] != 1 || paths["/hook/b"] != 1 {
		t.Errorf("sends = %v", paths)
	}
	_ = a
	_ = b
}

func TestAChannelIsHeldToThirtyMessagesAMinute(t *testing.T) {
	e := newEnv(t)
	e.webhookChannel("busy")
	for i := 0; i < 45; i++ {
		e.svc.Notify(bg(), Event{Type: EventRequestCreated, Title: "New ask", Body: "x", Key: fmt.Sprint(i)})
	}
	e.svc.Wait()
	if n := len(e.rec.all()); n != RateLimit {
		t.Fatalf("%d sends, want %d", n, RateLimit)
	}
	e.clock.Advance(61 * time.Second)
	e.svc.Notify(bg(), Event{Type: EventRequestCreated, Title: "New ask", Body: "x", Key: "later"})
	e.svc.Wait()
	if n := len(e.rec.all()); n != RateLimit+1 {
		t.Errorf("%d sends a minute later, want %d", n, RateLimit+1)
	}
}

func TestProblemsAreReportedOnceUntilTheyGoAndComeBack(t *testing.T) {
	e := newEnv(t)
	e.webhookChannel("h", EventHealthProblem)
	count := func() int { e.svc.Wait(); return len(e.rec.all()) }

	e.svc.ReportProblems(bg(), map[string]string{"client:1": "qBittorrent is not answering", "disk:2": "The movies folder is almost full"})
	if n := count(); n != 2 {
		t.Fatalf("first report: %d sends", n)
	}
	e.svc.ReportProblems(bg(), map[string]string{"client:1": "qBittorrent is not answering", "disk:2": "The movies folder is almost full"})
	if n := count(); n != 2 {
		t.Errorf("the same problems again: %d sends, want still 2", n)
	}
	e.svc.ReportProblems(bg(), map[string]string{"client:1": "qBittorrent is not answering"}) // the disk problem went away
	e.clock.Advance(11 * time.Minute)                                                         // long enough for the dedupe window to pass
	e.svc.ReportProblems(bg(), map[string]string{"client:1": "qBittorrent is not answering", "disk:2": "The movies folder is almost full"})
	if n := count(); n != 3 {
		t.Errorf("after the disk problem came back: %d sends, want 3", n)
	}
	var bodies []string
	for _, m := range e.messages("/hook/h") {
		bodies = append(bodies, m["message"].(string))
	}
	sort.Strings(bodies)
	if want := []string{"The movies folder is almost full", "The movies folder is almost full", "qBittorrent is not answering"}; !reflect.DeepEqual(bodies, want) {
		t.Errorf("bodies = %v, want %v", bodies, want)
	}
}

func TestCloseStopsTheSending(t *testing.T) {
	e := newEnv(t)
	e.webhookChannel("a")
	e.svc.Close()
	e.svc.Notify(bg(), Event{Type: EventRequestCreated, Title: "x", Body: "x", Key: "1"})
	if n := len(e.rec.all()); n != 0 {
		t.Errorf("%d sends after Close", n)
	}
}

// ---- listening to the event hub -------------------------------------------------------------

func TestAskEventsOnTheHubBecomePlainMessages(t *testing.T) {
	e := newEnv(t)
	e.webhookChannel("w")
	e.start()
	e.reqs.byID = map[int64]RequestInfo{
		1: {ID: 1, UserName: "Priya", TitleName: "Metropolis", Year: 1927, Kind: "movie", Quality: "uhd", State: "pending"},
		2: {ID: 2, UserName: "Priya", TitleName: "Pioneer One", Year: 2010, Kind: "show", Quality: "hd", State: "approved", DecidedByName: "Bailey"},
		3: {ID: 3, UserName: "Omar", TitleName: "Sintel", Year: 2010, Kind: "movie", Quality: "hd", State: "approved"}, // by the rules
		4: {ID: 4, UserName: "Priya", TitleName: "Chopin", Year: 1810, Kind: "artist", Quality: "hd", State: "declined", DecidedByName: "Bailey", DeclineReason: "  Too\nmany   already  "},
		5: {ID: 5, UserName: "Priya", TitleName: "Metropolis", Year: 1927, Kind: "movie", Quality: "uhd", State: "available"},
		6: {ID: 6, UserName: "Bailey", TitleName: "Sintel", Year: 2010, Kind: "movie", Quality: "hd", State: "approved", DecidedByName: "Bailey"}, // the owner's own
	}
	pub := func(event string, id int, state string) {
		e.hub.Publish("requests", map[string]any{"event": event, "id": id, "state": state})
	}
	pub("created", 1, "pending")
	pub("approved", 2, "approved")
	pub("approved", 3, "approved")
	pub("declined", 4, "declined")
	pub("state", 5, "available")
	pub("approved", 6, "approved")
	// Not alerts: a cancel, a state that is not "available", a request that cannot be read.
	pub("cancelled", 1, "cancelled")
	pub("state", 2, "failed")
	pub("created", 99, "pending")

	msgs := e.waitMessages("/hook/w", 6)
	got := map[string][]string{}
	for _, m := range msgs {
		got[m["event"].(string)] = append(got[m["event"].(string)], m["title"].(string)+" | "+m["message"].(string))
	}
	for k := range got {
		sort.Strings(got[k])
	}
	want := map[string][]string{
		EventRequestCreated: {"New ask | Priya asked for Metropolis (1927) in 4K — waiting for your yes"},
		EventRequestApproved: {
			"Added | Bailey added Sintel (2010) in HD",
			"Ask approved | Bailey approved Priya's ask for Pioneer One (2010) in HD",
			"Ask approved | Omar's ask for Sintel (2010) in HD was approved automatically",
		},
		EventRequestDeclined: {"Ask declined | Bailey declined Priya's ask for Chopin in HD: Too many already"},
		EventTitleAvailable:  {"Ready to watch | Metropolis (1927) in 4K is in the library, asked for by Priya"},
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("messages =\n%v\nwant\n%v", got, want)
	}
	time.Sleep(50 * time.Millisecond)
	if n := len(e.messages("/hook/w")); n != 6 {
		t.Errorf("%d messages, want 6 (the other events are not alerts)", n)
	}
	// A webhook gets the facts as data too.
	for _, m := range msgs {
		if m["event"] == EventRequestCreated {
			d := m["data"].(map[string]any)
			if d["requestId"] != float64(1) || d["user"] != "Priya" || d["quality"] != "uhd" || d["title"] != "Metropolis" {
				t.Errorf("data = %v", d)
			}
		}
	}
}

func TestDownloadAndImportFailuresOnTheHub(t *testing.T) {
	e := newEnv(t)
	e.webhookChannel("w")
	e.start()
	e.dls.byID = map[int64]DownloadInfo{
		1: {ID: 1, TitleID: 5, ReleaseTitle: "Metropolis.1927.1080p.BluRay-GRP", State: "failed", Error: "The download client reported an error."},
		2: {ID: 2, TitleID: 6, ReleaseTitle: "Sintel.2010.2160p.WEB-GRP", State: "failed", Error: "Import failed: there is no room in the movies folder"},
		3: {ID: 3, ReleaseTitle: "", State: "failed"},
	}
	pubFail := func(id int) {
		e.hub.Publish("downloads", map[string]any{"type": "failed", "id": id, "titleId": 5, "state": "failed", "progress": 0.5})
	}
	pubFail(1)
	pubFail(2)
	pubFail(3)
	// Not alerts: progress, a completion, a download that cannot be read.
	e.hub.Publish("downloads", map[string]any{"type": "progress", "items": []any{map[string]any{"id": 1}}})
	e.hub.Publish("downloads", map[string]any{"type": "completed", "id": 1, "state": "completed"})
	pubFail(77)

	msgs := e.waitMessages("/hook/w", 3)
	var lines []string
	for _, m := range msgs {
		lines = append(lines, m["event"].(string)+" | "+m["message"].(string))
	}
	sort.Strings(lines)
	want := []string{
		"download.failed | A download failed to download",
		"download.failed | Metropolis.1927.1080p.BluRay-GRP failed to download: The download client reported an error.",
		"import.failed | The import of Sintel.2010.2160p.WEB-GRP failed: there is no room in the movies folder",
	}
	if !reflect.DeepEqual(lines, want) {
		t.Errorf("messages =\n%v\nwant\n%v", lines, want)
	}
	time.Sleep(50 * time.Millisecond)
	if n := len(e.messages("/hook/w")); n != 3 {
		t.Errorf("%d messages, want 3", n)
	}
}

func TestLibraryAndHealthEventsOnTheHub(t *testing.T) {
	e := newEnv(t)
	e.webhookChannel("w")
	e.start()
	e.hub.Publish("library", map[string]any{"event": "imported", "titleId": 5, "files": 1}) // good news, not an alert
	e.hub.Publish("library", map[string]any{"event": "import_failed", "titleId": 5, "downloadId": 9, "message": "No video file in the download"})
	e.hub.Publish("health", map[string]any{"event": "problem", "key": "client:1", "message": "qBittorrent is not answering"})
	e.hub.Publish("health", map[string]any{"event": "ok", "key": "client:1"})
	e.hub.Publish("health", map[string]any{"event": "problem", "key": "empty"}) // no message: nothing to say

	msgs := e.waitMessages("/hook/w", 2)
	var lines []string
	for _, m := range msgs {
		lines = append(lines, m["event"].(string)+" | "+m["message"].(string))
	}
	sort.Strings(lines)
	want := []string{"health.problem | qBittorrent is not answering", "import.failed | An import failed: No video file in the download"}
	if !reflect.DeepEqual(lines, want) {
		t.Errorf("messages = %v, want %v", lines, want)
	}
	time.Sleep(50 * time.Millisecond)
	if n := len(e.messages("/hook/w")); n != 2 {
		t.Errorf("%d messages, want 2", n)
	}
}

func TestTheSameHubEventTwiceIsOneMessage(t *testing.T) {
	e := newEnv(t)
	e.webhookChannel("w")
	e.start()
	e.reqs.byID = map[int64]RequestInfo{1: {ID: 1, UserName: "Priya", TitleName: "Metropolis", Year: 1927, Kind: "movie", Quality: "hd"}}
	for i := 0; i < 3; i++ {
		e.hub.Publish("requests", map[string]any{"event": "created", "id": 1, "state": "pending"})
	}
	e.waitMessages("/hook/w", 1)
	time.Sleep(100 * time.Millisecond)
	if n := len(e.messages("/hook/w")); n != 1 {
		t.Errorf("%d messages for one ask, want 1", n)
	}
}

func TestRunWithoutAHubReturnsAtOnce(t *testing.T) {
	e := newEnv(t)
	svc := NewService(e.db, e.box, e.svc.snd.hc, Options{})
	done := make(chan struct{})
	go func() { svc.Run(bg()); close(done) }()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("Run without a hub did not return")
	}
}

func TestRunStopsWhenItsContextEndsAndWhenTheHubCloses(t *testing.T) {
	e := newEnv(t)
	ctx, cancel := context.WithCancel(bg())
	done := make(chan struct{})
	go func() { e.svc.Run(ctx); close(done) }()
	waitFor(t, func() bool { return e.hub.Len() == 1 })
	cancel()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("Run did not stop when its context ended")
	}
	waitFor(t, func() bool { return e.hub.Len() == 0 }) // it unsubscribed

	e2 := newEnv(t)
	done2 := make(chan struct{})
	go func() { e2.svc.Run(bg()); close(done2) }()
	waitFor(t, func() bool { return e2.hub.Len() == 1 })
	e2.hub.Close()
	select {
	case <-done2:
	case <-time.After(2 * time.Second):
		t.Fatal("Run did not stop when the hub closed")
	}
}

func TestAListenerTheHubDropsForBeingSlowIsReplaced(t *testing.T) {
	e := newEnv(t, rigOpts{hubBuffer: 1})
	e.webhookChannel("w")
	e.start()
	e.reqs.byID = map[int64]RequestInfo{
		1: {ID: 1, UserName: "Priya", TitleName: "One", Year: 2001, Kind: "movie", Quality: "hd"},
		2: {ID: 2, UserName: "Priya", TitleName: "Two", Year: 2002, Kind: "movie", Quality: "hd"},
		3: {ID: 3, UserName: "Priya", TitleName: "Three", Year: 2003, Kind: "movie", Quality: "hd"},
		4: {ID: 4, UserName: "Priya", TitleName: "Four", Year: 2004, Kind: "movie", Quality: "hd"},
	}
	// The listener is busy with the first event while two more arrive: its
	// buffer (one) overflows and the hub drops it.
	gate := make(chan struct{})
	e.reqs.mu.Lock()
	e.reqs.gate = gate
	e.reqs.mu.Unlock()
	pub := func(id int) {
		e.hub.Publish("requests", map[string]any{"event": "created", "id": id, "state": "pending"})
	}
	pub(1)
	waitFor(t, func() bool { e.reqs.mu.Lock(); defer e.reqs.mu.Unlock(); return e.reqs.calls >= 1 })
	pub(2)
	pub(3)
	waitFor(t, func() bool { return e.hub.Len() == 0 }) // dropped
	close(gate)

	// It comes back by itself, and later events arrive.
	waitFor(t, func() bool { return e.hub.Len() == 1 })
	pub(4)
	var all []string
	waitFor(t, func() bool {
		all = nil
		for _, m := range e.messages("/hook/w") {
			all = append(all, m["message"].(string))
		}
		return strings.Contains(strings.Join(all, "\n"), "asked for Four (2004)")
	})
	if !strings.Contains(strings.Join(all, "\n"), "asked for One (2001)") {
		t.Errorf("messages = %v", all)
	}
}
