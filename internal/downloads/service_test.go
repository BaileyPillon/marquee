package downloads

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/BaileyPillon/marquee/internal/core/events"
	"github.com/BaileyPillon/marquee/internal/grab"
)

// ---- helpers -------------------------------------------------------------------

type collector struct{ c *events.Client }

func (e *env) collect() *collector {
	c, unsub := e.hub.Subscribe(Topic)
	e.t.Cleanup(unsub)
	return &collector{c: c}
}

// drain returns the events published since the last drain.
func (c *collector) drain() []map[string]any {
	var out []map[string]any
	for {
		select {
		case m := <-c.c.C():
			var v map[string]any
			if err := json.Unmarshal(m.Data, &v); err == nil {
				out = append(out, v)
			}
		default:
			return out
		}
	}
}

func types(evs []map[string]any) []string {
	var out []string
	for _, e := range evs {
		out = append(out, e["type"].(string))
	}
	return out
}

type recorder struct {
	mu      sync.Mutex
	ids     []int64
	doPanic bool
}

func (r *recorder) OnCompleted(_ context.Context, id int64) {
	r.mu.Lock()
	r.ids = append(r.ids, id)
	r.mu.Unlock()
	if r.doPanic {
		panic("importer exploded")
	}
}

func (r *recorder) got() []int64 {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]int64(nil), r.ids...)
}

func torrentRelease(magnetHash string) grab.Release {
	return grab.Release{
		IndexerID: 7, GUID: "guid-1", Title: "Metropolis.1927.1080p.BluRay.x264", Protocol: grab.ProtocolTorrent,
		MagnetURL: "magnet:?xt=urn:btih:" + magnetHash + "&dn=Metropolis", Size: 4 << 30, Quality: "Bluray-1080p",
	}
}

func usenetRelease() grab.Release {
	return grab.Release{
		IndexerID: 8, GUID: "guid-2", Title: "Metropolis.1927.1080p.BluRay.x264-GRP", Protocol: grab.ProtocolUsenet,
		DownloadURL: "https://nzb.invalid/getnzb/1.nzb?apikey=INDEXER-KEY", Size: 4 << 30,
	}
}

func (e *env) target(titleID int64) grab.Target {
	return grab.Target{CopyID: 11, TitleID: titleID, Kind: "movie", Quality: grab.QualityHD, EpisodeIDs: []int64{3, 4}}
}

// ---- Grab ----------------------------------------------------------------------

func TestGrabPicksTheClientByProtocolAndPriority(t *testing.T) {
	e := newEnv(t)
	title := e.addTitle("Metropolis", 1927)
	qbt := e.addClient("qb slow", KindQBittorrent, "http://qb:8080", func(c *ClientInput) { c.Priority = intp(5) })
	del := e.addClient("deluge fast", KindDeluge, "http://deluge:8112", func(c *ClientInput) { c.Priority = intp(2) })
	sab := e.addClient("sab", KindSABnzbd, "http://sab:8080", func(c *ClientInput) { c.Priority = intp(9) })
	e.addClient("qb off", KindQBittorrent, "http://qb-off:8080", func(c *ClientInput) {
		c.Priority = intp(0)
		c.Enabled = boolp(false)
	})
	ev := e.collect()

	id, err := e.svc.Grab(context.Background(), e.target(title), torrentRelease(bbbHex))
	if err != nil {
		t.Fatal(err)
	}
	if e.fake.at("http://deluge:8112").addedCount() != 1 || e.fake.at("http://qb:8080").addedCount() != 0 ||
		e.fake.at("http://qb-off:8080").addedCount() != 0 || e.fake.at("http://sab:8080").addedCount() != 0 {
		t.Fatal("the torrent did not go to the enabled torrent client with the lowest priority number")
	}
	d := e.download(id)
	if d.ClientID != del.ID || d.ClientRef != "http://deluge:8112#1" || d.Protocol != grab.ProtocolTorrent ||
		d.TitleID != title || d.CopyID != 11 || d.Kind != "movie" || d.Quality != grab.QualityHD ||
		len(d.EpisodeIDs) != 2 || d.EpisodeIDs[1] != 4 || d.ReleaseTitle != "Metropolis.1927.1080p.BluRay.x264" ||
		d.IndexerID != 7 || d.GUID != "guid-1" || d.Size != 4<<30 || d.State != StateQueued {
		t.Errorf("row = %+v", d)
	}
	if got := e.fake.at("http://deluge:8112"); got.added[0].Category != DefaultCategory || got.added[0].MagnetURL == "" || got.added[0].Paused {
		t.Errorf("add input = %+v", got.added[0])
	}

	// Usenet goes to the usenet client, whatever the priorities.
	id2, err := e.svc.Grab(context.Background(), e.target(title), usenetRelease())
	if err != nil {
		t.Fatal(err)
	}
	if d := e.download(id2); d.ClientID != sab.ID || d.Protocol != grab.ProtocolUsenet {
		t.Errorf("usenet row = %+v", d)
	}
	if in := e.fake.at("http://sab:8080").added[0]; in.NZBURL != usenetRelease().DownloadURL || in.Name != usenetRelease().Title {
		t.Errorf("sab add input = %+v", in)
	}
	_ = qbt

	if got := types(ev.drain()); len(got) != 2 || got[0] != "added" || got[1] != "added" {
		t.Errorf("events = %v, want two added", got)
	}
}

func TestGrabNoClientForTheProtocol(t *testing.T) {
	e := newEnv(t)
	title := e.addTitle("Metropolis", 1927)
	ctx := context.Background()

	if _, err := e.svc.Grab(ctx, e.target(title), torrentRelease(bbbHex)); !errors.Is(err, ErrNoClient) {
		t.Errorf("no clients at all: %v", err)
	}
	e.addClient("sab", KindSABnzbd, "http://sab:8080")
	if _, err := e.svc.Grab(ctx, e.target(title), torrentRelease(bbbHex)); !errors.Is(err, ErrNoClient) {
		t.Errorf("only a usenet client for a torrent: %v", err)
	}
	e.addClient("qb off", KindQBittorrent, "http://qb:8080", func(c *ClientInput) { c.Enabled = boolp(false) })
	_, err := e.svc.Grab(ctx, e.target(title), torrentRelease(bbbHex))
	var de *Error
	if !errors.Is(err, ErrNoClient) || !errors.As(err, &de) || de.Code != "no_client_for_protocol" {
		t.Errorf("only a disabled torrent client: %v", err)
	}
	if _, err := e.svc.Grab(ctx, e.target(title), grab.Release{Title: "x", Protocol: "carrier-pigeon"}); !errors.Is(err, ErrReleaseUnavailable) {
		t.Errorf("odd protocol: %v", err)
	}
	// Nothing was tracked.
	if list, total, _ := e.svc.List(ctx, ListFilter{State: FilterAll}); total != 0 || len(list) != 0 {
		t.Errorf("rows left behind: %v", list)
	}
}

func TestGrabSavePath(t *testing.T) {
	e := newEnv(t)
	title := e.addTitle("Metropolis", 1927)
	e.addClient("qb", KindQBittorrent, "http://qb:8080", func(c *ClientInput) { c.SavePath = str(`M:\client-default`) })
	ctx := context.Background()

	tgt := e.target(title)
	if _, err := e.svc.Grab(ctx, tgt, torrentRelease(bbbHex)); err != nil {
		t.Fatal(err)
	}
	tgt.SavePath = `M:\from-target`
	if _, err := e.svc.Grab(ctx, tgt, torrentRelease(strings.Repeat("a", 40))); err != nil {
		t.Fatal(err)
	}
	added := e.fake.at("http://qb:8080").added
	if added[0].SavePath != `M:\client-default` || added[1].SavePath != `M:\from-target` {
		t.Errorf("save paths = %q, %q", added[0].SavePath, added[1].SavePath)
	}
}

func TestGrabFallsBackAndReportsErrors(t *testing.T) {
	e := newEnv(t)
	title := e.addTitle("Metropolis", 1927)
	first := e.addClient("first", KindQBittorrent, "http://first:8080", func(c *ClientInput) { c.Priority = intp(1) })
	e.addClient("second", KindDeluge, "http://second:8112", func(c *ClientInput) { c.Priority = intp(2) })
	ctx := context.Background()

	// The first client is down: the second one takes it, and the first one's
	// problem is on show.
	e.fake.at("http://first:8080").addErr = unreachable(errors.New("connection refused"))
	if _, err := e.svc.Grab(ctx, e.target(title), torrentRelease(bbbHex)); err != nil {
		t.Fatalf("fallback: %v", err)
	}
	if e.fake.at("http://second:8112").addedCount() != 1 {
		t.Error("the second client did not get it")
	}
	clients, _ := e.svc.Clients(ctx)
	for _, c := range clients {
		if c.ID == first.ID && (c.LastError == "" || c.LastErrorAt == nil || !strings.Contains(c.LastError, "could not reach")) {
			t.Errorf("first client's problem not recorded: %+v", c)
		}
		if c.Name == "second" && (c.LastOKAt == nil || c.LastError != "") {
			t.Errorf("second client not marked healthy: %+v", c)
		}
	}

	// Both down: client_unreachable.
	e.fake.at("http://second:8112").addErr = unreachable(errors.New("timeout"))
	_, err := e.svc.Grab(ctx, e.target(title), torrentRelease(strings.Repeat("b", 40)))
	var de *Error
	if !errors.Is(err, ErrClientUnreachable) || !errors.As(err, &de) || de.Code != "client_unreachable" {
		t.Errorf("both down: %v", err)
	}
	// Both refuse: client_rejected.
	e.fake.at("http://first:8080").addErr = ErrRejected
	e.fake.at("http://second:8112").addErr = ErrRejected
	if _, err := e.svc.Grab(ctx, e.target(title), torrentRelease(strings.Repeat("c", 40))); !errors.Is(err, ErrClientRejected) {
		t.Errorf("both refuse: %v", err)
	}
	// A bad sign-in counts as unreachable for Grab's purposes.
	e.fake.at("http://first:8080").addErr = ErrAuth
	e.fake.at("http://second:8112").addErr = ErrAuth
	if _, err := e.svc.Grab(ctx, e.target(title), torrentRelease(strings.Repeat("d", 40))); !errors.Is(err, ErrClientUnreachable) {
		t.Errorf("both refuse the login: %v", err)
	}
}

func TestGrabFetchesTheTorrentItselfThroughOutbound(t *testing.T) {
	e := newEnv(t)
	title := e.addTitle("Metropolis", 1927)
	e.addClient("qb", KindQBittorrent, "http://qb:8080")
	file := tinyTorrent(tinyInfo("Metropolis.mkv", 100))
	want, _ := ParseTorrent(file)

	srv, hits := indexer(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Query().Get("apikey") != "INDEXER-KEY" {
			http.Error(w, "no", http.StatusForbidden)
			return
		}
		_, _ = w.Write(file)
	})
	rel := grab.Release{Title: "Metropolis", Protocol: grab.ProtocolTorrent, DownloadURL: srv.URL + "/dl?apikey=INDEXER-KEY"}
	if _, err := e.svc.Grab(context.Background(), e.target(title), rel); err != nil {
		t.Fatal(err)
	}
	in := e.fake.at("http://qb:8080").added[0]
	if string(in.TorrentFile) != string(file) || in.MagnetURL != "" || in.NZBURL != "" {
		t.Errorf("the client was not handed the torrent file: %+v", in)
	}
	if hits.Load() != 1 {
		t.Errorf("indexer hits = %d", hits.Load())
	}
	if m, _ := ParseTorrent(in.TorrentFile); m.InfoHash != want.InfoHash {
		t.Error("hash mismatch")
	}
}

func TestGrabHandlesAMagnetRedirectAndFallbacks(t *testing.T) {
	e := newEnv(t)
	title := e.addTitle("Metropolis", 1927)
	e.addClient("qb", KindQBittorrent, "http://qb:8080")
	ctx := context.Background()
	qb := e.fake.at("http://qb:8080")

	// The indexer's link redirects to a magnet link.
	redirector, _ := indexer(t, func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, "magnet:?xt=urn:btih:"+bbbHex+"&dn=X", http.StatusFound)
	})
	rel := grab.Release{Title: "Metropolis", Protocol: grab.ProtocolTorrent, DownloadURL: redirector.URL + "/dl?apikey=INDEXER-KEY"}
	if _, err := e.svc.Grab(ctx, e.target(title), rel); err != nil {
		t.Fatal(err)
	}
	if in := qb.added[0]; !strings.HasPrefix(in.MagnetURL, "magnet:?xt=urn:btih:"+bbbHex) || len(in.TorrentFile) != 0 {
		t.Errorf("redirect to magnet: %+v", in)
	}

	// The torrent link is dead but the release also has a magnet: use it.
	dead, _ := indexer(t, func(w http.ResponseWriter, r *http.Request) { http.NotFound(w, r) })
	rel = grab.Release{Title: "Metropolis", Protocol: grab.ProtocolTorrent, DownloadURL: dead.URL + "/dl?apikey=INDEXER-KEY",
		MagnetURL: "magnet:?xt=urn:btih:" + strings.Repeat("e", 40)}
	if _, err := e.svc.Grab(ctx, e.target(title), rel); err != nil {
		t.Fatal(err)
	}
	if in := qb.added[1]; in.MagnetURL != rel.MagnetURL || len(in.TorrentFile) != 0 {
		t.Errorf("magnet fallback: %+v", in)
	}

	// Dead link and no magnet: release_unavailable, and no client was bothered.
	rel.MagnetURL = ""
	_, err := e.svc.Grab(ctx, e.target(title), rel)
	var de *Error
	if !errors.Is(err, ErrReleaseUnavailable) || !errors.As(err, &de) || strings.Contains(err.Error(), "INDEXER-KEY") {
		t.Errorf("dead link: %v", err)
	}
	if qb.addedCount() != 2 {
		t.Error("a client was asked to add a release Marquee could not fetch")
	}
	// No link at all, a bad magnet, a bad NZB link.
	for name, r := range map[string]grab.Release{
		"nothing":    {Title: "x", Protocol: grab.ProtocolTorrent},
		"bad magnet": {Title: "x", Protocol: grab.ProtocolTorrent, MagnetURL: "magnet:?dn=x"},
	} {
		if _, err := e.svc.Grab(ctx, e.target(title), r); !errors.Is(err, ErrReleaseUnavailable) {
			t.Errorf("%s: %v", name, err)
		}
	}
	e.addClient("sab", KindSABnzbd, "http://sab:8080")
	if _, err := e.svc.Grab(ctx, e.target(title), grab.Release{Title: "x", Protocol: grab.ProtocolUsenet, DownloadURL: "ftp://x/y.nzb"}); !errors.Is(err, ErrReleaseUnavailable) {
		t.Errorf("bad nzb link: %v", err)
	}
}

func TestGrabSameReleaseTwiceIsTrackedOnce(t *testing.T) {
	e := newEnv(t)
	title := e.addTitle("Metropolis", 1927)
	e.addClient("qb", KindQBittorrent, "http://qb:8080")
	e.fake.at("http://qb:8080").fixedRef = bbbHex // the client says "that is the one I already have"
	ctx := context.Background()
	ev := e.collect()

	id1, err := e.svc.Grab(ctx, e.target(title), torrentRelease(bbbHex))
	if err != nil {
		t.Fatal(err)
	}
	id2, err := e.svc.Grab(ctx, e.target(title), torrentRelease(bbbHex))
	if err != nil || id2 != id1 {
		t.Fatalf("second grab: id %d (want %d), %v", id2, id1, err)
	}
	if _, total, _ := e.svc.List(ctx, ListFilter{State: FilterAll}); total != 1 {
		t.Errorf("rows = %d, want 1", total)
	}
	if got := types(ev.drain()); len(got) != 1 {
		t.Errorf("events = %v, want a single added", got)
	}

	// Once that download is over, grabbing it again starts a fresh life for the row.
	if err := e.svc.SetState(ctx, id1, StateImported, ""); err != nil {
		t.Fatal(err)
	}
	id3, err := e.svc.Grab(ctx, e.target(title), torrentRelease(bbbHex))
	if err != nil || id3 != id1 {
		t.Fatalf("re-grab: id %d, %v", id3, err)
	}
	if d := e.download(id1); d.State != StateQueued || d.Progress != 0 || d.CompletedAt != nil || d.Error != "" {
		t.Errorf("row after re-grab = %+v", d)
	}
}

func TestGrabEndToEndWithQBittorrentAndAnIndexer(t *testing.T) {
	e := newEnv(t)
	e.realAdapters()
	title := e.addTitle("Metropolis", 1927)
	qbt := newFakeQBT(t)
	e.addClient("qb", KindQBittorrent, qbt.srv.URL, func(c *ClientInput) {
		c.Username = str("admin")
		c.Password = str("adminadmin")
		c.Category = str("marquee")
	})
	file := tinyTorrent(tinyInfo("Metropolis.1927.mkv", 4096))
	want, _ := ParseTorrent(file)
	srv, _ := indexer(t, func(w http.ResponseWriter, r *http.Request) { _, _ = w.Write(file) })
	rec := &recorder{}
	e.svc.SetCompletedHandler(rec)
	ctx := context.Background()

	id, err := e.svc.Grab(ctx, e.target(title), grab.Release{Title: "Metropolis", Protocol: grab.ProtocolTorrent, DownloadURL: srv.URL + "/x.torrent"})
	if err != nil {
		t.Fatal(err)
	}
	if d := e.download(id); d.ClientRef != want.InfoHash {
		t.Errorf("client_ref = %s, want the info-hash %s", d.ClientRef, want.InfoHash)
	}
	if add := qbt.lastAdd(t); string(add.File) != string(file) || add.Fields["category"] != "marquee" {
		t.Errorf("qBittorrent add = %+v", add.Fields)
	}

	qbt.setTorrents(map[string]any{"hash": want.InfoHash, "name": "Metropolis.1927.mkv", "state": "downloading", "category": "marquee",
		"progress": 0.5, "size": 4096, "amount_left": 2048, "dlspeed": 1000, "eta": 20, "save_path": "M:\\dl", "content_path": "M:\\dl\\Metropolis.1927.mkv"})
	if err := e.svc.Poll(ctx); err != nil {
		t.Fatal(err)
	}
	if d := e.download(id); d.State != StateDownloading || d.Progress != 0.5 || d.SpeedBps != 1000 || d.ETA != 20 {
		t.Errorf("mid-download = %+v", d)
	}
	qbt.setTorrents(map[string]any{"hash": want.InfoHash, "name": "Metropolis.1927.mkv", "state": "stalledUP", "category": "marquee",
		"progress": 1.0, "size": 4096, "amount_left": 0, "dlspeed": 0, "eta": 8640000, "save_path": "M:\\dl", "content_path": "M:\\dl\\Metropolis.1927.mkv"})
	if err := e.svc.Poll(ctx); err != nil {
		t.Fatal(err)
	}
	d := e.download(id)
	if d.State != StateCompleted || d.ContentPath != `M:\dl\Metropolis.1927.mkv` || d.CompletedAt == nil {
		t.Errorf("finished = %+v", d)
	}
	if got := rec.got(); len(got) != 1 || got[0] != id {
		t.Errorf("handler calls = %v", got)
	}
}

// ---- Poll ----------------------------------------------------------------------

// grabOne grabs a torrent through the fake client at url and returns its id and ref.
func (e *env) grabOne(url string, hash string) (int64, string) {
	e.t.Helper()
	title := e.addTitle("T-"+hash[:4], 2000)
	id, err := e.svc.Grab(context.Background(), e.target(title), torrentRelease(hash))
	if err != nil {
		e.t.Fatal(err)
	}
	return id, e.download(id).ClientRef
}

func TestPollMovesStatesAndCallsTheHandlerOnce(t *testing.T) {
	e := newEnv(t)
	e.addClient("qb", KindQBittorrent, "http://qb:8080", func(c *ClientInput) { c.Category = str("films") })
	fake := e.fake.at("http://qb:8080")
	id, ref := e.grabOne("http://qb:8080", bbbHex)
	rec := &recorder{}
	e.svc.SetCompletedHandler(rec)
	ev := e.collect()
	ctx := context.Background()
	poll := func() {
		t.Helper()
		if err := e.svc.Poll(ctx); err != nil {
			t.Fatal(err)
		}
	}

	// Downloading.
	fake.setItems(Item{Ref: ref, Name: "x", State: StateDownloading, Progress: 0.4, Size: 5000, SpeedBps: 1234, ETA: 90, SavePath: "/dl"})
	poll()
	d := e.download(id)
	if d.State != StateDownloading || d.Progress != 0.4 || d.SpeedBps != 1234 || d.ETA != 90 || d.Size != 5000 || d.SavePath != "/dl" {
		t.Errorf("after the first poll: %+v", d)
	}
	if got := fake.listCats[len(fake.listCats)-1]; got != "films" {
		t.Errorf("polled category %q, want the client's own", got)
	}
	evs := ev.drain()
	if got := types(evs); len(got) != 1 || got[0] != "progress" {
		t.Fatalf("events = %v", got)
	}
	items := evs[0]["items"].([]any)
	if first := items[0].(map[string]any); first["id"] != float64(id) || first["progress"] != 0.4 || first["speedBps"] != 1234.0 {
		t.Errorf("progress event = %v", first)
	}

	// Nothing changed: no write, no event.
	updated := d.UpdatedAt
	e.advance(5 * time.Second)
	poll()
	if got := e.download(id); !got.UpdatedAt.Equal(updated) {
		t.Error("an unchanged poll rewrote the row")
	}
	if evs := ev.drain(); len(evs) != 0 {
		t.Errorf("events for an unchanged poll: %v", evs)
	}

	// Stalled, then paused.
	fake.setItems(Item{Ref: ref, State: StateStalled, Progress: 0.4, ETA: -1})
	poll()
	if d := e.download(id); d.State != StateStalled || d.ETA != -1 {
		t.Errorf("stalled: %+v", d)
	}
	fake.setItems(Item{Ref: ref, State: StatePaused, Progress: 0.4, ETA: -1})
	poll()
	if d := e.download(id); d.State != StatePaused {
		t.Errorf("paused: %+v", d)
	}
	if len(rec.got()) != 0 {
		t.Fatal("handler called before the download finished")
	}
	ev.drain()

	// Finished.
	fake.setItems(Item{Ref: ref, State: StateCompleted, Finished: true, Progress: 1, Size: 5000, ContentPath: "/dl/Movie", SavePath: "/dl"})
	e.advance(5 * time.Second)
	poll()
	d = e.download(id)
	if d.State != StateCompleted || d.Progress != 1 || d.ContentPath != "/dl/Movie" || d.CompletedAt == nil ||
		!d.CompletedAt.Equal(e.clock()) {
		t.Errorf("finished: %+v", d)
	}
	if got := rec.got(); len(got) != 1 || got[0] != id {
		t.Fatalf("handler calls = %v, want exactly [%d]", got, id)
	}
	if got := types(ev.drain()); len(got) != 1 || got[0] != "completed" {
		t.Errorf("events = %v", got)
	}

	// Still seeding in the client for hours: polls go on, the handler is not told again.
	for i := 0; i < 5; i++ {
		e.advance(time.Minute)
		poll()
	}
	if got := rec.got(); len(got) != 1 {
		t.Errorf("handler calls after more polls = %v", got)
	}
	// The importer moves it on; the poll never drags it back.
	if err := e.svc.SetState(ctx, id, StateImporting, ""); err != nil {
		t.Fatal(err)
	}
	fake.setItems(Item{Ref: ref, State: StateDownloading, Progress: 0.1})
	poll()
	if d := e.download(id); d.State != StateImporting {
		t.Errorf("a poll moved an importing download to %s", d.State)
	}
	if len(rec.got()) != 1 {
		t.Error("handler called again")
	}
}

func TestPollCompletedHandlerExactlyOnceUnderConcurrentPolls(t *testing.T) {
	e := newEnv(t)
	e.addClient("qb", KindQBittorrent, "http://qb:8080")
	fake := e.fake.at("http://qb:8080")
	rec := &recorder{}
	e.svc.SetCompletedHandler(rec)
	var ids []int64
	for i := 0; i < 5; i++ {
		id, ref := e.grabOne("http://qb:8080", strings.Repeat(string(rune('a'+i)), 40))
		ids = append(ids, id)
		fake.items = append(fake.items, Item{Ref: ref, State: StateCompleted, Finished: true, Progress: 1, ContentPath: "/dl/" + ref})
	}
	var wg sync.WaitGroup
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_ = e.svc.Poll(context.Background())
		}()
	}
	wg.Wait()
	got := rec.got()
	if len(got) != len(ids) {
		t.Fatalf("handler calls = %v, want one per download (%d)", got, len(ids))
	}
	seen := map[int64]int{}
	for _, id := range got {
		seen[id]++
	}
	for _, id := range ids {
		if seen[id] != 1 {
			t.Errorf("download %d announced %d times", id, seen[id])
		}
	}
}

func TestPollSurvivesAMissingOrPanickingHandler(t *testing.T) {
	e := newEnv(t)
	e.addClient("qb", KindQBittorrent, "http://qb:8080")
	fake := e.fake.at("http://qb:8080")
	idA, refA := e.grabOne("http://qb:8080", strings.Repeat("a", 40))
	idB, refB := e.grabOne("http://qb:8080", strings.Repeat("b", 40))
	fake.setItems(
		Item{Ref: refA, State: StateCompleted, Finished: true, Progress: 1},
		Item{Ref: refB, State: StateCompleted, Finished: true, Progress: 1},
	)
	// No handler at all (nil-safe): both complete.
	if err := e.svc.Poll(context.Background()); err != nil {
		t.Fatal(err)
	}
	if e.download(idA).State != StateCompleted || e.download(idB).State != StateCompleted {
		t.Error("downloads did not complete without a handler")
	}

	// A handler that panics must not stop the rest of the poll.
	e2 := newEnv(t)
	e2.addClient("qb", KindQBittorrent, "http://qb:8080")
	f2 := e2.fake.at("http://qb:8080")
	idA, refA = e2.grabOne("http://qb:8080", strings.Repeat("a", 40))
	idB, refB = e2.grabOne("http://qb:8080", strings.Repeat("b", 40))
	f2.setItems(
		Item{Ref: refA, State: StateCompleted, Finished: true, Progress: 1},
		Item{Ref: refB, State: StateCompleted, Finished: true, Progress: 1},
	)
	rec := &recorder{doPanic: true}
	e2.svc.SetCompletedHandler(rec)
	if err := e2.svc.Poll(context.Background()); err != nil {
		t.Fatalf("poll failed because the handler panicked: %v", err)
	}
	if e2.download(idA).State != StateCompleted || e2.download(idB).State != StateCompleted || len(rec.got()) != 2 {
		t.Errorf("states %s/%s, handler saw %v", e2.download(idA).State, e2.download(idB).State, rec.got())
	}
}

func TestPollMarksClientErrorsFailed(t *testing.T) {
	e := newEnv(t)
	e.addClient("qb", KindQBittorrent, "http://qb:8080")
	fake := e.fake.at("http://qb:8080")
	id, ref := e.grabOne("http://qb:8080", bbbHex)
	rec := &recorder{}
	e.svc.SetCompletedHandler(rec)
	ev := e.collect()

	fake.setItems(Item{Ref: ref, State: StateFailed, Progress: 0.3, Error: "qBittorrent cannot find the downloaded files."})
	if err := e.svc.Poll(context.Background()); err != nil {
		t.Fatal(err)
	}
	d := e.download(id)
	if d.State != StateFailed || d.Error != "qBittorrent cannot find the downloaded files." || d.Progress != 0.3 {
		t.Errorf("failed row = %+v", d)
	}
	if got := types(ev.drain()); len(got) != 1 || got[0] != "failed" {
		t.Errorf("events = %v", got)
	}
	if len(rec.got()) != 0 {
		t.Error("handler called for a failed download")
	}
	// Failed is final: a later poll that lists it as downloading changes nothing.
	fake.setItems(Item{Ref: ref, State: StateDownloading, Progress: 0.9})
	_ = e.svc.Poll(context.Background())
	if e.download(id).State != StateFailed {
		t.Error("a failed download came back")
	}
}

func TestPollDetectsRemovedDownloads(t *testing.T) {
	e := newEnv(t)
	e.addClient("qb", KindQBittorrent, "http://qb:8080")
	fake := e.fake.at("http://qb:8080")
	id, ref := e.grabOne("http://qb:8080", bbbHex)
	ev := e.collect()
	ctx := context.Background()
	poll := func() { t.Helper(); _ = e.svc.Poll(ctx) }

	fake.setItems(Item{Ref: ref, State: StateDownloading, Progress: 0.2})
	poll()
	ev.drain()

	// Gone from the client: one poll is not enough (a client restarting shows an
	// empty list), two in a row are.
	fake.setItems()
	poll()
	if e.download(id).State != StateDownloading {
		t.Fatal("marked removed after a single miss")
	}
	// It comes back: the count starts over.
	fake.setItems(Item{Ref: ref, State: StateDownloading, Progress: 0.3})
	poll()
	fake.setItems()
	poll()
	if e.download(id).State != StateDownloading {
		t.Fatal("misses were not reset when it reappeared")
	}
	poll()
	d := e.download(id)
	if d.State != StateRemoved || d.Progress != 0.3 {
		t.Fatalf("after two misses in a row: %+v", d)
	}
	if got := types(ev.drain()); got[len(got)-1] != "removed" {
		t.Errorf("events = %v", got)
	}
	// Removed is final and no longer polled.
	fake.setItems(Item{Ref: ref, State: StateDownloading})
	poll()
	if e.download(id).State != StateRemoved {
		t.Error("removed came back")
	}
}

func TestPollIgnoresADownClientAndItsOwnExtras(t *testing.T) {
	e := newEnv(t)
	c := e.addClient("qb", KindQBittorrent, "http://qb:8080")
	fake := e.fake.at("http://qb:8080")
	id, ref := e.grabOne("http://qb:8080", bbbHex)
	ctx := context.Background()

	// The client does not answer for many polls: nothing is marked removed or
	// failed, and the problem is on the client.
	fake.setListErr(unreachable(errors.New("connection refused")))
	for i := 0; i < 5; i++ {
		if err := e.svc.Poll(ctx); err != nil {
			t.Fatal(err)
		}
	}
	if d := e.download(id); d.State != StateQueued {
		t.Errorf("state after a client outage = %s", d.State)
	}
	clients, _ := e.svc.Clients(ctx)
	if clients[0].LastError == "" || clients[0].LastErrorAt == nil {
		t.Errorf("outage not recorded: %+v", clients[0])
	}
	// It recovers, with a download of its own that Marquee never added.
	fake.setListErr(nil)
	fake.setItems(
		Item{Ref: ref, State: StateDownloading, Progress: 0.7},
		Item{Ref: "someone-elses", State: StateCompleted, Finished: true, Progress: 1},
	)
	if err := e.svc.Poll(ctx); err != nil {
		t.Fatal(err)
	}
	if d := e.download(id); d.State != StateDownloading || d.Progress != 0.7 {
		t.Errorf("after recovery: %+v", d)
	}
	if _, total, _ := e.svc.List(ctx, ListFilter{State: FilterAll}); total != 1 {
		t.Errorf("rows = %d: a download Marquee did not add was tracked", total)
	}
	clients, _ = e.svc.Clients(ctx)
	if clients[0].LastError != "" || clients[0].LastOKAt == nil || clients[0].ID != c.ID {
		t.Errorf("recovery not recorded: %+v", clients[0])
	}

	// Disabled clients are not polled.
	off := false
	if _, err := e.svc.UpdateClient(ctx, c.ID, ClientInput{Enabled: &off}); err != nil {
		t.Fatal(err)
	}
	n := len(fake.listCats)
	_ = e.svc.Poll(ctx)
	if len(fake.listCats) != n {
		t.Error("a disabled client was polled")
	}
}

func TestPollJobIsDueEveryFiveSecondsWhileBusyAndOnceAMinuteWhenIdle(t *testing.T) {
	e := newEnv(t)
	e.addClient("qb", KindQBittorrent, "http://qb:8080")
	fake := e.fake.at("http://qb:8080")
	ctx := context.Background()
	lists := func() int { fake.mu.Lock(); defer fake.mu.Unlock(); return len(fake.listCats) }
	tick := func(d time.Duration) int {
		e.advance(d)
		before := lists()
		if err := e.svc.PollJob(ctx); err != nil {
			t.Fatal(err)
		}
		return lists() - before
	}

	// Idle, nothing tracked: the very first tick polls (to learn that it is idle)...
	if n := tick(0); n != 1 {
		t.Fatalf("first tick polled %d times", n)
	}
	// ...and then it takes a minute.
	for _, step := range []time.Duration{5, 10, 20, 20} { // 5, 15, 35, 55 seconds in
		if n := tick(step * time.Second); n != 0 {
			t.Fatalf("idle poll ran %v after the last one", step)
		}
	}
	if n := tick(5 * time.Second); n != 1 { // 60 s
		t.Fatalf("idle minute poll did not run: %d", n)
	}

	// A grab makes it busy: from then on it polls every 5 seconds, so the next
	// tick that is 5 s (give or take a little jitter) after the last poll runs.
	_, ref := e.grabOne("http://qb:8080", bbbHex)
	fake.setItems(Item{Ref: ref, State: StateDownloading, Progress: 0.1})
	if n := tick(1 * time.Second); n != 0 {
		t.Fatalf("busy poll ran 1 s after the last one: %d", n)
	}
	if n := tick(3600 * time.Millisecond); n != 1 { // 4.6 s: a hair early still counts
		t.Fatalf("a slightly early tick was skipped: %d", n)
	}
	if n := tick(2 * time.Second); n != 0 {
		t.Fatalf("busy poll ran after 2 s: %d", n)
	}
	if n := tick(3 * time.Second); n != 1 { // 5 s since the last one
		t.Fatalf("busy poll did not run after 5 s: %d", n)
	}
	if n := tick(5 * time.Second); n != 1 {
		t.Fatalf("busy poll did not keep its 5 s pace: %d", n)
	}
	// The download finishes in the client; once nothing is tracked it goes back to a minute.
	fake.setItems(Item{Ref: ref, State: StateCompleted, Finished: true, Progress: 1})
	if n := tick(5 * time.Second); n != 1 {
		t.Fatalf("poll that sees the finish did not run: %d", n)
	}
	if n := tick(5 * time.Second); n != 0 {
		t.Fatalf("idle again, but it still polled after 5 s: %d", n)
	}
}

// ---- owner actions ---------------------------------------------------------------

func TestPauseResumeRemove(t *testing.T) {
	e := newEnv(t)
	e.addClient("qb", KindQBittorrent, "http://qb:8080")
	fake := e.fake.at("http://qb:8080")
	id, ref := e.grabOne("http://qb:8080", bbbHex)
	ctx := context.Background()
	ev := e.collect()

	v, err := e.svc.Pause(ctx, id)
	if err != nil || v.State != StatePaused {
		t.Fatalf("Pause: %+v, %v", v, err)
	}
	if len(fake.paused) != 1 || fake.paused[0] != ref {
		t.Errorf("client paused %v", fake.paused)
	}
	if _, err := e.svc.Pause(ctx, id); !errors.Is(err, ErrInvalidState) {
		t.Errorf("pausing a paused download: %v", err)
	}
	v, err = e.svc.Resume(ctx, id)
	if err != nil || v.State != StateQueued || len(fake.resumed) != 1 {
		t.Fatalf("Resume: %+v, %v", v, err)
	}
	if _, err := e.svc.Resume(ctx, id); !errors.Is(err, ErrInvalidState) {
		t.Errorf("resuming a running download: %v", err)
	}
	if got := types(ev.drain()); strings.Join(got, ",") != "state,state" {
		t.Errorf("events = %v", got)
	}

	// A client that fails the call leaves the download as it was.
	fake.actErr = unreachable(errors.New("down"))
	if _, err := e.svc.Pause(ctx, id); !errors.Is(err, ErrClientUnreachable) {
		t.Errorf("pause on a dead client: %v", err)
	}
	if e.download(id).State != StateQueued {
		t.Error("state changed although the client did not pause it")
	}
	if err := e.svc.Remove(ctx, id, true); !errors.Is(err, ErrClientUnreachable) {
		t.Errorf("remove on a dead client: %v", err)
	}
	fake.actErr = nil

	if err := e.svc.Remove(ctx, id, true); err != nil {
		t.Fatal(err)
	}
	if fake.removed[len(fake.removed)-1] != ref || !fake.deleted[len(fake.deleted)-1] {
		t.Errorf("client removal = %v %v", fake.removed, fake.deleted)
	}
	d := e.download(id)
	if d.State != StateRemoved {
		t.Errorf("state = %s", d.State)
	}
	n := len(fake.removed)
	if err := e.svc.Remove(ctx, id, false); err != nil || len(fake.removed) != n {
		t.Errorf("removing twice: %v, client calls %d -> %d", err, n, len(fake.removed))
	}
	for _, f := range []func() error{
		func() error { _, err := e.svc.Pause(ctx, 999); return err },
		func() error { _, err := e.svc.Resume(ctx, 999); return err },
		func() error { return e.svc.Remove(ctx, 999, false) },
	} {
		if err := f(); !errors.Is(err, ErrNotFound) {
			t.Errorf("unknown id: %v", err)
		}
	}
}

func TestListFiltersJoinsAndPages(t *testing.T) {
	e := newEnv(t)
	e.addClient("qb", KindQBittorrent, "http://qb:8080")
	ctx := context.Background()
	metro := e.addTitle("Metropolis", 1927)

	idActive, _ := e.svc.Grab(ctx, e.target(metro), torrentRelease(strings.Repeat("1", 40)))
	idDone, _ := e.svc.Grab(ctx, e.target(metro), torrentRelease(strings.Repeat("2", 40)))
	idFailed, _ := e.svc.Grab(ctx, e.target(metro), torrentRelease(strings.Repeat("3", 40)))
	idGone, _ := e.svc.Grab(ctx, e.target(metro), torrentRelease(strings.Repeat("4", 40)))
	_ = e.svc.SetState(ctx, idDone, StateImported, "")
	_ = e.svc.SetState(ctx, idFailed, StateFailed, "no")
	_ = e.svc.SetState(ctx, idGone, StateRemoved, "")
	idWaiting, _ := e.svc.Grab(ctx, e.target(metro), torrentRelease(strings.Repeat("5", 40)))
	_ = e.svc.SetState(ctx, idWaiting, StateCompleted, "")

	ids := func(f ListFilter) []int64 {
		t.Helper()
		list, _, err := e.svc.List(ctx, f)
		if err != nil {
			t.Fatal(err)
		}
		var out []int64
		for _, v := range list {
			out = append(out, v.ID)
		}
		return out
	}
	eq := func(got []int64, want ...int64) bool {
		if len(got) != len(want) {
			return false
		}
		for i := range got {
			if got[i] != want[i] {
				return false
			}
		}
		return true
	}
	if got := ids(ListFilter{}); !eq(got, idWaiting, idActive) {
		t.Errorf("default (active) = %v", got)
	}
	if got := ids(ListFilter{State: FilterActive}); !eq(got, idWaiting, idActive) {
		t.Errorf("active = %v", got)
	}
	if got := ids(ListFilter{State: FilterHistory}); len(got) != 3 {
		t.Errorf("history = %v", got)
	}
	list, total, _ := e.svc.List(ctx, ListFilter{State: FilterAll})
	if total != 5 || len(list) != 5 || list[0].ID != idWaiting {
		t.Errorf("all = %d/%d, first %d", len(list), total, list[0].ID)
	}
	// Pages.
	page, total, _ := e.svc.List(ctx, ListFilter{State: FilterAll, Limit: 2, Offset: 3})
	if total != 5 || len(page) != 2 {
		t.Errorf("page = %d of %d", len(page), total)
	}
	if page, _, _ := e.svc.List(ctx, ListFilter{State: FilterAll, Limit: 2, Offset: 99}); len(page) != 0 {
		t.Errorf("past the end = %v", page)
	}
	if _, _, err := e.svc.List(ctx, ListFilter{State: "sideways"}); err == nil {
		t.Error("bad state accepted")
	}

	// The shape of one item.
	v, err := e.svc.Get(ctx, idActive)
	if err != nil {
		t.Fatal(err)
	}
	if v.Title == nil || v.Title.ID != metro || v.Title.Name != "Metropolis" || v.Title.Year != 1927 ||
		v.Title.Kind != "movie" || v.Title.PosterRef != "tmdb/w500/poster.jpg" {
		t.Errorf("title = %+v", v.Title)
	}
	if v.Client.Name != "qb" || v.Client.Kind != KindQBittorrent || v.State != StateQueued || v.Quality != "hd" ||
		v.ReleaseTitle == "" || v.ETASeconds != nil || v.CopyID == nil || *v.CopyID != 11 || len(v.EpisodeIDs) != 2 ||
		v.Size != 4<<30 || v.CompletedAt != nil {
		t.Errorf("view = %+v", v)
	}
	// A title that has been deleted leaves the history readable.
	if _, err := e.db.Exec(`DELETE FROM titles WHERE id = ?`, metro); err != nil {
		t.Fatal(err)
	}
	if v, err := e.svc.Get(ctx, idActive); err != nil || v.Title != nil {
		t.Errorf("after deleting the title: %+v, %v", v.Title, err)
	}
	if _, err := e.svc.Get(ctx, 12345); !errors.Is(err, ErrNotFound) {
		t.Errorf("unknown id: %v", err)
	}
}

func TestSummary(t *testing.T) {
	e := newEnv(t)
	ok := e.addClient("healthy", KindQBittorrent, "http://ok:8080")
	bad := e.addClient("broken", KindDeluge, "http://bad:8112")
	e.addClient("never asked", KindSABnzbd, "http://sab:8080")
	e.addClient("off", KindQBittorrent, "http://off:8080", func(c *ClientInput) { c.Enabled = boolp(false) })
	ctx := context.Background()
	title := e.addTitle("Metropolis", 1927)

	add := func(hash string, state string) {
		id, err := e.svc.Grab(ctx, e.target(title), torrentRelease(strings.Repeat(hash, 40)))
		if err != nil {
			t.Fatal(err)
		}
		e.forceState(id, state)
	}
	add("1", StateQueued)
	add("2", StateQueued)
	add("3", StateDownloading)
	add("4", StateStalled)
	add("5", StateCompleted)
	add("6", StateImporting)
	add("7", StateImported)
	add("8", StateFailed)
	add("9", StatePaused)

	e.fake.at("http://bad:8112").setListErr(unreachable(errors.New("down")))
	e.svc.Poll(ctx)
	sum, err := e.svc.Summary(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if sum.Queued != 2 || sum.Downloading != 2 || sum.Importing != 2 {
		t.Errorf("counts = %+v", sum)
	}
	if len(sum.Clients) != 3 {
		t.Fatalf("clients = %+v (disabled ones are left out)", sum.Clients)
	}
	byID := map[int64]ClientHealth{}
	for _, c := range sum.Clients {
		byID[c.ID] = c
	}
	if h := byID[ok.ID]; !h.OK || h.LastError != "" {
		t.Errorf("healthy = %+v", h)
	}
	if h := byID[bad.ID]; h.OK || h.LastError == "" {
		t.Errorf("broken = %+v", h)
	}
}

func TestPollDoesNotRewriteClientHealthEveryTime(t *testing.T) {
	e := newEnv(t)
	e.addClient("qb", KindQBittorrent, "http://qb:8080")
	ctx := context.Background()
	lastOK := func() *time.Time {
		cs, _ := e.svc.Clients(ctx)
		return cs[0].LastOKAt
	}
	_ = e.svc.Poll(ctx)
	first := lastOK()
	if first == nil {
		t.Fatal("first poll did not record the client as healthy")
	}
	e.advance(10 * time.Second)
	_ = e.svc.Poll(ctx)
	if got := lastOK(); got == nil || !got.Equal(*first) {
		t.Errorf("last_ok_at rewritten within a minute: %v -> %v", first, got)
	}
	e.advance(time.Minute)
	_ = e.svc.Poll(ctx)
	if got := lastOK(); got == nil || !got.After(*first) {
		t.Errorf("last_ok_at never refreshed: %v -> %v", first, got)
	}

	// A client that keeps failing keeps its first error time.
	fake := e.fake.at("http://qb:8080")
	fake.setListErr(unreachable(errors.New("refused")))
	_ = e.svc.Poll(ctx)
	cs, _ := e.svc.Clients(ctx)
	since := cs[0].LastErrorAt
	e.advance(30 * time.Second)
	_ = e.svc.Poll(ctx)
	cs, _ = e.svc.Clients(ctx)
	if since == nil || cs[0].LastErrorAt == nil || !cs[0].LastErrorAt.Equal(*since) {
		t.Errorf("last_error_at moved while the same error went on: %v -> %v", since, cs[0].LastErrorAt)
	}
}

func TestPausedDownloadsDoNotKeepThePollBusy(t *testing.T) {
	e := newEnv(t)
	e.addClient("qb", KindQBittorrent, "http://qb:8080")
	fake := e.fake.at("http://qb:8080")
	id, ref := e.grabOne("http://qb:8080", bbbHex)
	fake.setItems(Item{Ref: ref, State: StatePaused, Progress: 0.2, ETA: -1})
	ctx := context.Background()
	_ = e.svc.Poll(ctx)
	if e.download(id).State != StatePaused {
		t.Fatal("not paused")
	}
	// Still tracked (a resume from the client's own page is noticed), but at the slow pace.
	lists := func() int { fake.mu.Lock(); defer fake.mu.Unlock(); return len(fake.listCats) }
	before := lists()
	e.advance(6 * time.Second)
	_ = e.svc.PollJob(ctx)
	if lists() != before {
		t.Error("polled at the fast pace for a paused download")
	}
	fake.setItems(Item{Ref: ref, State: StateDownloading, Progress: 0.3})
	e.advance(time.Minute)
	_ = e.svc.PollJob(ctx)
	if d := e.download(id); d.State != StateDownloading {
		t.Errorf("a resume made in the client was not noticed: %s", d.State)
	}
}
