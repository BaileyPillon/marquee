package sources

import (
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/BaileyPillon/marquee/internal/grab"
	"github.com/BaileyPillon/marquee/internal/sources/sourcestest"
)

func feedWith(items ...sourcestest.Item) func(url.Values) []sourcestest.Item {
	return func(url.Values) []sourcestest.Item { return items }
}

func TestSyncRSSRemembersSealsAndHandsOverNewItems(t *testing.T) {
	e := newEnv(t)
	srv := sourcestest.NewServer(t, testKey)
	one := sourcestest.Torrent("Metropolis.1927.1080p.BluRay.x264-GRP", 8<<30, 40)
	one.GUID, one.Magnet, one.InfoHash, one.IMDb = "g-metropolis", true, hashA, "tt0017136"
	two := sourcestest.Torrent("Pioneer.One.S01E04.720p.WEB-DL.x264-GRP", 1<<30, 12)
	two.GUID = "g-pioneer"
	srv.Search = feedWith(one, two)
	ix := e.add("Alpha", KindTorznab, srv.Feed(), testKey, 2000, 5000)

	m := &recordingMatcher{}
	e.svc.SetMatcher(m)
	if err := e.svc.SyncRSS(bg()); err != nil {
		t.Fatal(err)
	}
	if m.total() != 2 || len(m.calls) != 1 {
		t.Fatalf("matcher heard %d items in %d calls, want 2 in 1", m.total(), len(m.calls))
	}
	if n := e.count(`SELECT COUNT(*) FROM releases_seen WHERE indexer_id = ?`, ix.ID); n != 2 {
		t.Errorf("%d rows remembered, want 2", n)
	}
	// The latest-items call carries the owner's categories and no query text.
	q := srv.Requests(false)[0].Query
	if q.Get("cat") != "2000,5000" || q.Has("q") || q.Get("t") != "search" {
		t.Errorf("rss call = %v", q)
	}

	// The links are sealed in the database.
	var link, magnet, hash, raw string
	if err := e.db.QueryRow(`SELECT link_sealed, magnet, infohash, raw_attrs FROM releases_seen WHERE guid = 'g-metropolis'`).
		Scan(&link, &magnet, &hash, &raw); err != nil {
		t.Fatal(err)
	}
	for what, col := range map[string]string{"link_sealed": link, "magnet": magnet, "raw_attrs": raw} {
		noKey(t, what, col, testKey)
		for _, leak := range []string{"magnet:", "download", "secretpass", "http"} {
			if strings.Contains(col, leak) {
				t.Errorf("%s holds %q in the clear: %.200q", what, leak, col)
			}
		}
	}
	if hash != hashA {
		t.Errorf("infohash column = %q", hash)
	}

	// And they open again for a grab.
	got, ok, err := e.svc.SeenItem(bg(), ix.ID, "g-metropolis")
	if err != nil || !ok {
		t.Fatalf("SeenItem: %v %v", ok, err)
	}
	if !strings.Contains(got.DownloadURL, "/download/g-metropolis") || !strings.HasPrefix(got.MagnetURL, "magnet:?xt=urn:btih:") {
		t.Errorf("links after the round trip: %q %q", got.DownloadURL, got.MagnetURL)
	}
	if got.IMDb != "tt0017136" || got.Protocol != grab.ProtocolTorrent || got.Seeders != 40 || got.Title != one.Title {
		t.Errorf("item after the round trip = %+v", got)
	}
	if _, ok, _ := e.svc.SeenItem(bg(), ix.ID, "never-seen"); ok {
		t.Error("an unknown guid was found")
	}

	// A second sync sees the same items: nothing new reaches the matcher.
	if err := e.svc.SyncRSS(bg()); err != nil {
		t.Fatal(err)
	}
	if m.total() != 2 {
		t.Errorf("matcher heard %d items after the second sync, want still 2", m.total())
	}
	// A new item does.
	three := sourcestest.Torrent("Sintel.2010.1080p.BluRay.x264-GRP", 3<<30, 7)
	srv.Search = feedWith(one, two, three)
	if err := e.svc.SyncRSS(bg()); err != nil {
		t.Fatal(err)
	}
	if m.total() != 3 || m.calls[len(m.calls)-1][0].Title != three.Title {
		t.Errorf("matcher calls = %d items", m.total())
	}
	noKey(t, "the log", e.logs.String(), testKey)
}

func TestSyncRSSForgetsItemsAfterAWeek(t *testing.T) {
	e := newEnv(t)
	srv := sourcestest.NewServer(t, testKey)
	srv.Search = feedWith(sourcestest.Torrent("Sintel.2010.1080p.BluRay.x264-GRP", 3<<30, 7))
	ix := e.add("Alpha", KindTorznab, srv.Feed(), testKey)
	if err := e.svc.SyncRSS(bg()); err != nil {
		t.Fatal(err)
	}
	// Age the row past the keep time, and let the feed show something else.
	old := e.now.Add(-SeenKeep - time.Hour)
	if _, err := e.db.Exec(`UPDATE releases_seen SET seen_at = ?`, old.Format("2006-01-02T15:04:05.000Z")); err != nil {
		t.Fatal(err)
	}
	srv.Search = feedWith(sourcestest.Torrent("Metropolis.1927.1080p.BluRay.x264-GRP", 8<<30, 3))
	if err := e.svc.SyncRSS(bg()); err != nil {
		t.Fatal(err)
	}
	if n := e.count(`SELECT COUNT(*) FROM releases_seen WHERE indexer_id = ?`, ix.ID); n != 1 {
		t.Errorf("%d rows remembered, want just the fresh one", n)
	}
}

func TestSyncRSSOnlyFailsWhenEverySourceFails(t *testing.T) {
	e := newEnv(t)
	good := sourcestest.NewServer(t, testKey)
	good.Search = feedWith(sourcestest.Torrent("Sintel.2010.1080p.BluRay.x264-GRP", 3<<30, 7))
	bad := sourcestest.NewServer(t, testKey)
	bad.Fail = 503
	e.add("Good", KindTorznab, good.Feed(), testKey)
	e.add("Bad", KindTorznab, bad.Feed(), testKey)
	if err := e.svc.SyncRSS(bg()); err != nil {
		t.Errorf("one flaky source made the whole job fail: %v", err)
	}
	good.Fail = 500
	if err := e.svc.SyncRSS(bg()); err == nil {
		t.Error("with every source down the job should report it")
	} else {
		noKey(t, "the job error", err.Error(), testKey)
	}
}

func TestSyncRSSWithNothingSetUp(t *testing.T) {
	e := newEnv(t)
	if err := e.svc.SyncRSS(bg()); err != nil {
		t.Errorf("no sources is not an error: %v", err)
	}
}

func TestDeletingASourceForgetsItsItems(t *testing.T) {
	e := newEnv(t)
	srv := sourcestest.NewServer(t, testKey)
	srv.Search = feedWith(sourcestest.Torrent("Sintel.2010.1080p.BluRay.x264-GRP", 3<<30, 7))
	ix := e.add("Alpha", KindTorznab, srv.Feed(), testKey)
	if err := e.svc.SyncRSS(bg()); err != nil {
		t.Fatal(err)
	}
	if err := e.svc.Delete(bg(), ix.ID); err != nil {
		t.Fatal(err)
	}
	if n := e.count(`SELECT COUNT(*) FROM releases_seen`); n != 0 {
		t.Errorf("%d remembered items left for a deleted source", n)
	}
}
