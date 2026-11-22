package importer

import (
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/BaileyPillon/marquee/internal/downloads"
	"github.com/BaileyPillon/marquee/internal/grab"
	"github.com/BaileyPillon/marquee/internal/requests"
	"github.com/BaileyPillon/marquee/internal/search"
)

// stall makes the torrent client report a download as stuck at progress.
func (r *rig) stall(id int64, progress float64) downloads.Download {
	r.t.Helper()
	d := r.download(id)
	r.qbt.mu.Lock()
	it := r.qbt.items[d.ClientRef]
	it.State, it.Progress = downloads.StateStalled, progress
	r.qbt.mu.Unlock()
	return d
}

func (r *rig) poll() {
	r.t.Helper()
	if err := r.dl.Poll(bg()); err != nil {
		r.t.Fatal(err)
	}
}

// The real case: an Internet Archive torrent sits at 99.97% in stalledDL. The
// downloads module fails it; the importer finishes the job: blocklist, forget the
// grab, search again, and say so in the history.
func TestAStalledDownloadIsBlocklistedAndTheCopyIsSearchedAgain(t *testing.T) {
	r := newRig(t).withImporter()
	title := r.movie(notldTMDB)
	cp, reqID := r.ask(title.ID, "hd", requests.Scope{})
	id := r.grabAs(cp, title, notldRelease, grab.ProtocolTorrent, nil)
	d := r.stall(id, 0.9997)

	r.poll()
	if st := r.download(id).State; st != downloads.StateStalled {
		t.Fatalf("after the first poll: %s", st)
	}
	r.advance(29 * time.Minute)
	r.poll()
	if st := r.download(id).State; st == downloads.StateFailed {
		t.Fatal("failed after 29 minutes")
	}
	if len(r.blocklist()) != 0 {
		t.Fatal("blocklisted before the time was up")
	}

	r.advance(time.Minute)
	r.poll()
	got := r.download(id)
	wantReason := "Stuck at 99.97% for 30 minutes. The source can't finish it (often an Internet Archive torrent whose info files changed)."
	if got.State != downloads.StateFailed || got.Error != wantReason {
		t.Fatalf("download = %s %q", got.State, got.Error)
	}

	bl := r.blocklist()
	if len(bl) != 1 {
		t.Fatalf("blocklist = %+v", bl)
	}
	if b := bl[0]; b.TitleID != title.ID || b.GUID != "guid-"+notldRelease || b.InfoHash != d.ClientRef ||
		b.ReleaseTitle != notldRelease || b.Reason != "Stalled: "+wantReason {
		t.Errorf("blocklist entry = %+v (client ref %s)", b, d.ClientRef)
	}
	if rm := r.qbt.removals(); len(rm) != 1 || rm[0].ref != d.ClientRef || !rm[0].deleteData {
		t.Errorf("removals = %v, want the torrent with its data", rm)
	}

	if copyID := r.srch.waitSearch(t); copyID != cp.ID {
		t.Errorf("searched copy %d, want %d", copyID, cp.ID)
	}
	if fg := r.srch.forgets(); len(fg) != 1 || fg[0].copyID != cp.ID || !reflect.DeepEqual(fg[0].keys, []string{search.MovieKey()}) {
		t.Errorf("the search should forget the movie grab: %v", fg)
	}
	if st := r.requestState(reqID); st != requests.StateApproved {
		t.Errorf("request state = %s; the search goes on, so it stays approved", st)
	}

	h := r.historyItem(title.ID, "failed")
	if h["stalled"] != true || h["blocklisted"] != true || h["searchingAgain"] != true || h["removedFromClient"] != true ||
		h["reason"] != wantReason || h["release"] != notldRelease {
		t.Errorf("history detail = %v", h)
	}

	// Once: later polls neither blocklist again nor search again.
	for i := 0; i < 3; i++ {
		r.advance(time.Minute)
		r.poll()
	}
	if len(r.blocklist()) != 1 {
		t.Errorf("blocklist grew: %+v", r.blocklist())
	}
	r.srch.noSearch(t)
}

func TestAStalledEpisodeMakesTheSearchForgetThatEpisode(t *testing.T) {
	r := newRig(t).withImporter()
	show := r.show(pioneerTVM)
	cp, _ := r.ask(show.ID, "hd", requests.Scope{Seasons: []int{1}})
	name := "Pioneer.One.S01E01.1080p.BluRay.x264-GRP"
	id := r.grabAs(cp, show, name, grab.ProtocolTorrent, []int64{epID(t, show, 1, 1)})
	r.stall(id, 0.4)
	r.poll()
	r.advance(30 * time.Minute)
	r.poll()

	if d := r.download(id); d.State != downloads.StateFailed || !strings.HasPrefix(d.Error, "Stuck at 40% for 30 minutes.") {
		t.Fatalf("download = %s %q", d.State, d.Error)
	}
	if bl := r.blocklist(); len(bl) != 1 || !strings.HasPrefix(bl[0].Reason, "Stalled: Stuck at 40%") {
		t.Errorf("blocklist = %+v", bl)
	}
	r.srch.waitSearch(t)
	if fg := r.srch.forgets(); len(fg) != 1 || !reflect.DeepEqual(fg[0].keys, []string{search.EpisodeKey(1, 1)}) {
		t.Errorf("forgets = %v, want just S01E01", fg)
	}
}

// Stalls that come close together share one search, like failed imports do.
func TestStalledDownloadsCloseTogetherShareOneSearch(t *testing.T) {
	r := newRig(t).withImporter()
	r.imp.searchDelay = 600 * time.Millisecond
	title := r.movie(notldTMDB)
	cp, _ := r.ask(title.ID, "hd", requests.Scope{})
	a := r.grabAs(cp, title, "Night.of.the.Living.Dead.1968.1080p.BluRay.x264-AAA", grab.ProtocolTorrent, nil)
	b := r.grabAs(cp, title, "Night.of.the.Living.Dead.1968.720p.BluRay.x264-BBB", grab.ProtocolTorrent, nil)
	r.stall(a, 0.9997)
	r.stall(b, 0.5)
	r.poll()
	r.advance(30 * time.Minute)
	r.poll()
	for _, id := range []int64{a, b} {
		if st := r.download(id).State; st != downloads.StateFailed {
			t.Fatalf("download %d: %s", id, st)
		}
	}
	if len(r.blocklist()) != 2 {
		t.Errorf("both releases should be blocklisted: %+v", r.blocklist())
	}
	r.srch.waitSearch(t)
	r.srch.noSearch(t)
}
