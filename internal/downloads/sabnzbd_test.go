package downloads

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"testing"
)

const sabKey = "sab-api-key-0123456789abcdef"

// fakeSAB is a SABnzbd API server.
type fakeSAB struct {
	t   *testing.T
	srv *httptest.Server

	mu      sync.Mutex
	key     string
	cats    map[string]bool
	queue   []map[string]any
	history []map[string]any
	calls   []url.Values
	addFail bool
	kbps    string
}

func newFakeSAB(t *testing.T) *fakeSAB {
	f := &fakeSAB{t: t, key: sabKey, cats: map[string]bool{"*": true}, kbps: "2048.0"}
	f.srv = httptest.NewServer(http.HandlerFunc(f.handle))
	t.Cleanup(f.srv.Close)
	return f
}

func (f *fakeSAB) adapter(t *testing.T, key string) *sabnzbd {
	t.Helper()
	a, err := newSABnzbd(AdapterConfig{Kind: KindSABnzbd, URL: f.srv.URL, APIKey: key, HTTP: testHTTP()})
	if err != nil {
		t.Fatal(err)
	}
	return a
}

func (f *fakeSAB) modes() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	var out []string
	for _, c := range f.calls {
		m := c.Get("mode")
		if n := c.Get("name"); n != "" && (m == "queue" || m == "history") {
			m += ":" + n
		}
		out = append(out, m)
	}
	return out
}

func (f *fakeSAB) lastCall(mode string) url.Values {
	f.mu.Lock()
	defer f.mu.Unlock()
	for i := len(f.calls) - 1; i >= 0; i-- {
		if f.calls[i].Get("mode") == mode {
			return f.calls[i]
		}
	}
	f.t.Fatalf("no %s call", mode)
	return nil
}

func (f *fakeSAB) handle(w http.ResponseWriter, r *http.Request) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if r.URL.Path != "/api" {
		http.NotFound(w, r)
		return
	}
	q := r.URL.Query()
	f.calls = append(f.calls, q)
	mode := q.Get("mode")
	if mode == "version" { // needs no key
		writeTestJSON(w, map[string]any{"version": "4.3.2"})
		return
	}
	if q.Get("apikey") != f.key {
		writeTestJSON(w, map[string]any{"status": false, "error": "API Key Incorrect"})
		return
	}
	switch mode {
	case "queue":
		switch q.Get("name") {
		case "delete":
			f.queue = dropSlot(f.queue, "nzo_id", q.Get("value"))
			writeTestJSON(w, map[string]any{"status": true, "nzo_ids": []string{q.Get("value")}})
		case "pause", "resume":
			writeTestJSON(w, map[string]any{"status": true})
		default:
			slots := f.queue
			if slots == nil {
				slots = []map[string]any{}
			}
			writeTestJSON(w, map[string]any{"queue": map[string]any{"status": "Downloading", "paused": false, "kbpersec": f.kbps, "slots": slots}})
		}
	case "history":
		if q.Get("name") == "delete" {
			f.history = dropSlot(f.history, "nzo_id", q.Get("value"))
			writeTestJSON(w, map[string]any{"status": true})
			return
		}
		slots := f.history
		if slots == nil {
			slots = []map[string]any{}
		}
		writeTestJSON(w, map[string]any{"history": map[string]any{"slots": slots}})
	case "addurl":
		if f.addFail {
			writeTestJSON(w, map[string]any{"status": false, "error": "Cannot fetch " + q.Get("name")})
			return
		}
		writeTestJSON(w, map[string]any{"status": true, "nzo_ids": []string{"SABnzbd_nzo_abc123"}})
	case "get_cats":
		var cats []string
		for c := range f.cats {
			cats = append(cats, c)
		}
		writeTestJSON(w, map[string]any{"categories": cats})
	case "set_config":
		f.cats[q.Get("name")] = true
		writeTestJSON(w, map[string]any{"config": map[string]any{}})
	default:
		writeTestJSON(w, map[string]any{"status": false, "error": "not implemented"})
	}
}

func dropSlot(slots []map[string]any, key, val string) []map[string]any {
	var out []map[string]any
	for _, s := range slots {
		if s[key] != val {
			out = append(out, s)
		}
	}
	return out
}

// ---- tests ---------------------------------------------------------------------

func TestSABTestChecksTheKeyNotJustTheVersion(t *testing.T) {
	f := newFakeSAB(t)
	v, err := f.adapter(t, sabKey).Test(context.Background())
	if err != nil || v != "4.3.2" {
		t.Fatalf("Test = %q, %v", v, err)
	}
	// mode=version needs no key, so a wrong key must still be caught.
	_, err = f.adapter(t, "wrong-key-999").Test(context.Background())
	if !errors.Is(err, ErrAuth) {
		t.Fatalf("err = %v, want ErrAuth", err)
	}
	if strings.Contains(err.Error(), "wrong-key-999") {
		t.Errorf("the key is in the error: %v", err)
	}
}

func TestSABUnreachableErrorHasNoKey(t *testing.T) {
	f := newFakeSAB(t)
	a := f.adapter(t, sabKey)
	f.srv.Close()
	_, err := a.Test(context.Background())
	if !errors.Is(err, ErrUnreachable) {
		t.Fatalf("err = %v", err)
	}
	if strings.Contains(err.Error(), sabKey) || strings.Contains(PlainError(err), sabKey) {
		t.Errorf("the API key leaked into the error: %v", err)
	}
	// Not into a generic error either: wrong kind of server, bad status.
	for name, h := range map[string]http.HandlerFunc{
		"html":   func(w http.ResponseWriter, r *http.Request) { _, _ = w.Write([]byte("<html>")) },
		"status": func(w http.ResponseWriter, r *http.Request) { http.Error(w, "x", http.StatusInternalServerError) },
	} {
		srv := httptest.NewServer(h)
		a, _ := newSABnzbd(AdapterConfig{URL: srv.URL, APIKey: sabKey, HTTP: testHTTP()})
		_, err := a.Test(context.Background())
		srv.Close()
		if err == nil || strings.Contains(err.Error(), sabKey) {
			t.Errorf("%s: %v", name, err)
		}
	}
}

func TestSABAdd(t *testing.T) {
	f := newFakeSAB(t)
	a := f.adapter(t, sabKey)
	nzb := "https://indexer.invalid/getnzb/abc.nzb?i=1&r=INDEXER-KEY"

	ref, err := a.Add(context.Background(), AddInput{NZBURL: nzb, Name: "Some.Movie.2020.1080p", Category: "marquee", Paused: true})
	if err != nil || ref != "SABnzbd_nzo_abc123" {
		t.Fatalf("Add = %q, %v", ref, err)
	}
	q := f.lastCall("addurl")
	for k, v := range map[string]string{
		"name": nzb, "cat": "marquee", "nzbname": "Some.Movie.2020.1080p", "output": "json", "apikey": sabKey, "priority": "-2",
	} {
		if q.Get(k) != v {
			t.Errorf("%s = %q, want %q", k, q.Get(k), v)
		}
	}
	// The category was not there: it was created, so downloads do not land in the default one.
	if !f.cats["marquee"] {
		t.Error("category was not created")
	}
	// Second add: the category exists now, and is not made again; not paused.
	f.mu.Lock()
	f.calls = nil
	f.mu.Unlock()
	if _, err := a.Add(context.Background(), AddInput{NZBURL: nzb, Category: "marquee"}); err != nil {
		t.Fatal(err)
	}
	if got := f.modes(); len(got) != 1 || got[0] != "addurl" {
		t.Errorf("second add calls = %v", got)
	}
	if f.lastCall("addurl").Has("priority") {
		t.Error("priority sent for a download that should start")
	}
}

func TestSABAddRefusedDoesNotLeakTheLink(t *testing.T) {
	f := newFakeSAB(t)
	f.addFail = true // SABnzbd's error text echoes the link, which carries the indexer's key
	_, err := f.adapter(t, sabKey).Add(context.Background(), AddInput{NZBURL: "https://indexer.invalid/x.nzb?r=INDEXER-KEY", Category: "marquee"})
	if !errors.Is(err, ErrRejected) {
		t.Fatalf("err = %v, want ErrRejected", err)
	}
	if strings.Contains(err.Error(), "INDEXER-KEY") || strings.Contains(PlainError(err), "INDEXER-KEY") {
		t.Errorf("the indexer key leaked: %v", err)
	}
	if _, err := f.adapter(t, sabKey).Add(context.Background(), AddInput{}); !errors.Is(err, ErrRejected) {
		t.Errorf("nothing to add: %v", err)
	}
}

func TestSABListQueueAndHistory(t *testing.T) {
	f := newFakeSAB(t)
	f.queue = []map[string]any{
		{"nzo_id": "q1", "filename": "Downloading.One", "status": "Downloading", "percentage": "25", "mb": "1000.00", "mbleft": "750.00", "cat": "marquee", "timeleft": "0:10:05"},
		{"nzo_id": "q2", "filename": "Downloading.Two", "status": "Downloading", "percentage": 10, "mb": 500, "mbleft": 450, "cat": "marquee", "timeleft": "0:01:00"},
		{"nzo_id": "q3", "filename": "Waiting", "status": "Queued", "percentage": "0", "mb": "300", "mbleft": "300", "cat": "Marquee", "timeleft": "0:00:00"},
		{"nzo_id": "q4", "filename": "On.Hold", "status": "Paused", "percentage": "50", "mb": "100", "mbleft": "50", "cat": "marquee", "timeleft": "0:00:00"},
		{"nzo_id": "q5", "filename": "Fetching", "status": "Fetching", "percentage": "0", "mb": "0", "mbleft": "0", "cat": "marquee", "timeleft": "0:00:00"},
		{"nzo_id": "other", "filename": "Not.Ours", "status": "Downloading", "percentage": "1", "mb": "1", "mbleft": "1", "cat": "tv", "timeleft": "0:00:10"},
	}
	f.history = []map[string]any{
		{"nzo_id": "h1", "name": "Done.Movie", "status": "Completed", "storage": `D:\complete\marquee\Done.Movie`, "category": "marquee", "bytes": 1048576},
		{"nzo_id": "h2", "name": "Broken", "status": "Failed", "fail_message": "Aborted, cannot be completed - see https://sabnzbd.org/not-complete?key=SECRET", "category": "marquee", "bytes": "2048"},
		{"nzo_id": "h3", "name": "Unpacking", "status": "Extracting", "category": "marquee", "bytes": 4096},
		{"nzo_id": "h4", "name": "Old.Other", "status": "Completed", "storage": "/x", "category": "movies", "bytes": 5},
	}
	items, err := f.adapter(t, sabKey).List(context.Background(), "marquee")
	if err != nil {
		t.Fatal(err)
	}
	by := map[string]Item{}
	for _, it := range items {
		by[it.Ref] = it
	}
	if len(items) != 8 || by["other"].Ref != "" || by["h4"].Ref != "" {
		t.Fatalf("got %d items %v (other categories must be left out)", len(items), by)
	}

	q1 := by["q1"]
	if q1.State != StateDownloading || q1.Progress != 0.25 || q1.Size != 1000<<20 || q1.Done != 250<<20 || q1.ETA != 605 {
		t.Errorf("q1 = %+v", q1)
	}
	if q1.SpeedBps != 2048*1024 {
		t.Errorf("q1 speed = %d, want the queue's speed on the first active download", q1.SpeedBps)
	}
	if by["q2"].SpeedBps != 0 {
		t.Errorf("q2 speed = %d; SABnzbd reports one speed for the queue", by["q2"].SpeedBps)
	}
	if by["q2"].Progress != 0.1 || by["q2"].ETA != 60 {
		t.Errorf("q2 (numbers instead of strings) = %+v", by["q2"])
	}
	if by["q3"].State != StateQueued || by["q3"].ETA != -1 {
		t.Errorf("q3 = %+v", by["q3"])
	}
	if by["q4"].State != StatePaused {
		t.Errorf("q4 = %+v", by["q4"])
	}
	if by["q5"].State != StateDownloading {
		t.Errorf("q5 = %+v", by["q5"])
	}

	h1 := by["h1"]
	if !h1.Finished || h1.State != StateCompleted || h1.Progress != 1 || h1.ContentPath != `D:\complete\marquee\Done.Movie` ||
		h1.SavePath != `D:\complete\marquee` || h1.Size != 1048576 {
		t.Errorf("h1 = %+v", h1)
	}
	h2 := by["h2"]
	if h2.State != StateFailed || h2.Finished || !strings.Contains(h2.Error, "cannot be completed") || strings.Contains(h2.Error, "SECRET") || strings.Contains(h2.Error, "http") {
		t.Errorf("h2 = %+v", h2)
	}
	if h3 := by["h3"]; h3.State != StateDownloading || h3.Finished {
		t.Errorf("h3 (post-processing) = %+v", h3)
	}

	// The queue is read before the history, so a download moving from one to the
	// other can never fall between the two reads.
	modes := f.modes()
	if len(modes) != 2 || modes[0] != "queue" || modes[1] != "history" {
		t.Errorf("calls = %v", modes)
	}
	if f.lastCall("queue").Get("apikey") != sabKey {
		t.Error("queue call without the key")
	}
}

func TestSABRemovePauseResume(t *testing.T) {
	f := newFakeSAB(t)
	f.queue = []map[string]any{{"nzo_id": "q1", "filename": "x", "status": "Downloading", "cat": "marquee"}}
	a := f.adapter(t, sabKey)
	ctx := context.Background()
	if err := a.Pause(ctx, "q1"); err != nil {
		t.Fatal(err)
	}
	if err := a.Resume(ctx, "q1"); err != nil {
		t.Fatal(err)
	}
	if err := a.Remove(ctx, "q1", true); err != nil {
		t.Fatal(err)
	}
	if got := strings.Join(f.modes(), ","); got != "queue:pause,queue:resume,queue:delete,history:delete" {
		t.Errorf("calls = %s", got)
	}
	if q := f.lastCall("history"); q.Get("value") != "q1" || q.Get("del_files") != "1" {
		t.Errorf("history delete = %v", q)
	}
	if len(f.queue) != 0 {
		t.Error("still in the queue")
	}
	// Without deleteData the files are left alone.
	f.mu.Lock()
	f.calls = nil
	f.mu.Unlock()
	if err := a.Remove(ctx, "q1", false); err != nil {
		t.Fatal(err)
	}
	if f.lastCall("queue").Has("del_files") {
		t.Error("del_files sent without deleteData")
	}
}

func TestParseSabTime(t *testing.T) {
	for in, want := range map[string]int{
		"0:10:05": 605, "0:00:00": 0, "1:00:00": 3600, "30:00:00": 108000, "1:02:03:04": 93784,
		"": 0, "garbage": 0, "1:xx": 0, "5": 0, "-1:00": 0,
	} {
		if got := parseSabTime(in); got != want {
			t.Errorf("parseSabTime(%q) = %d, want %d", in, got, want)
		}
	}
}
