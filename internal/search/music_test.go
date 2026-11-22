package search

import (
	"errors"
	"fmt"
	"net/url"
	"strings"
	"testing"

	"github.com/BaileyPillon/marquee/internal/grab"
	"github.com/BaileyPillon/marquee/internal/sources/sourcestest"
)

// albumRow is an album to put in the catalog.
type albumRow struct {
	Name        string
	Year        int
	Tracks      int
	Unmonitored bool
}

// addAlbums puts albums (with that many tracks each) under an artist and returns
// their ids by name.
func (r *rig) addAlbums(artistID int64, albums ...albumRow) map[string]int64 {
	r.t.Helper()
	ids := map[string]int64{}
	for i, a := range albums {
		mon := 1
		if a.Unmonitored {
			mon = 0
		}
		res, err := r.db.Exec(`INSERT INTO albums (title_id, mbid, name, year, type, studio, monitored) VALUES (?, ?, ?, ?, 'album', 1, ?)`,
			artistID, fmt.Sprintf("b0000000-0000-4000-8000-%012d", 100+i), a.Name, a.Year, mon)
		if err != nil {
			r.t.Fatal(err)
		}
		id, _ := res.LastInsertId()
		ids[a.Name] = id
		for n := 1; n <= a.Tracks; n++ {
			if _, err := r.db.Exec(`INSERT INTO tracks (album_id, disc, position, name) VALUES (?, 1, ?, ?)`, id, n, fmt.Sprintf("Track %d", n)); err != nil {
				r.t.Fatal(err)
			}
		}
	}
	return ids
}

const mbSize = int64(1 << 20)

// chopinReleases answers a music search the way a source would: by album.
func chopinReleases(q url.Values) []sourcestest.Item {
	text := strings.ToLower(q.Get("album") + " " + q.Get("q"))
	switch {
	case strings.Contains(text, "nocturnes"):
		return []sourcestest.Item{
			tor("Frédéric Chopin - Nocturnes (1998) [MP3 320]", 150*mbSize),
			tor("Frédéric Chopin - Nocturnes (1998) [FLAC]", 600*mbSize),
			tor("Frédéric Chopin - Ballades (2001) [FLAC]", 500*mbSize),
			tor("Frédéric Chopin - Nocturnes (1998) [MP3 128]", 60*mbSize),
		}
	case strings.Contains(text, "tudes"):
		return []sourcestest.Item{
			tor("Frédéric Chopin - Études (1985) [FLAC 24bit]", 900*mbSize),
		}
	}
	return nil
}

func (r *rig) chopin(albums ...albumRow) (cp grab.Copy, ids map[string]int64) {
	r.t.Helper()
	id := r.addArtist("Frédéric Chopin")
	ids = r.addAlbums(id, albums...)
	return r.addCopy(1, id, "artist", grab.QualityHD), ids
}

func TestSearchCopyArtistGrabsEachMissingAlbum(t *testing.T) {
	r := newRig(t)
	cp, ids := r.chopin(albumRow{Name: "Nocturnes", Year: 1998, Tracks: 21}, albumRow{Name: "Études", Year: 1985, Tracks: 12})
	r.serve(chopinReleases)

	if err := r.svc.SearchCopy(bg(), cp.ID); err != nil {
		t.Fatal(err)
	}
	calls := r.grabber.all()
	if len(calls) != 2 {
		t.Fatalf("grabs = %v, want one release for each album", r.grabber.titlesGrabbed())
	}
	// Études (1985) is searched first (oldest), then Nocturnes (1998).
	want := []struct {
		title string
		album int64
		q     string
	}{
		{"Frédéric Chopin - Études (1985) [FLAC 24bit]", ids["Études"], "FLAC-24bit"},
		{"Frédéric Chopin - Nocturnes (1998) [FLAC]", ids["Nocturnes"], "FLAC"},
	}
	for i, w := range want {
		c := calls[i]
		if c.Release.Title != w.title || c.Release.Quality != w.q {
			t.Errorf("grab %d = %q (%s), want %q (%s)", i, c.Release.Title, c.Release.Quality, w.title, w.q)
		}
		tg := c.Target
		if tg.Kind != "artist" || tg.CopyID != cp.ID || tg.TitleID != cp.TitleID || len(tg.EpisodeIDs) != 1 || tg.EpisodeIDs[0] != w.album {
			t.Errorf("grab %d target = %+v, want the album %d in its episode ids", i, tg, w.album)
		}
		if len(c.Release.Reasons) == 0 || !strings.HasPrefix(c.Release.Reasons[0], "Best match") {
			t.Errorf("grab %d reasons = %q", i, c.Release.Reasons)
		}
	}

	// The sources were asked the way a music search is asked.
	var sawNocturnes bool
	for _, req := range r.srv.Requests(false) {
		q := req.Query
		if q.Get("t") != "music" || q.Get("artist") != "Frédéric Chopin" || q.Get("cat") != "3000" || q.Get("album") == "" {
			t.Errorf("a search was %v, want t=music with artist, album and cat=3000", q)
		}
		if q.Get("album") == "Nocturnes" {
			sawNocturnes = true
		}
	}
	if !sawNocturnes {
		t.Error("no search named the album Nocturnes")
	}

	h := r.history(cp.ID)
	if len(h) != 1 || h[0].Grabbed != 2 {
		t.Fatalf("history = %+v", h)
	}
	keys := strings.Join(h[0].GrabbedFor, ",")
	if !strings.Contains(keys, AlbumKey(ids["Nocturnes"])) || !strings.Contains(keys, AlbumKey(ids["Études"])) {
		t.Errorf("grabbed_for = %v, want both album keys", h[0].GrabbedFor)
	}
	if n := r.count(`SELECT COUNT(*) FROM album_searches WHERE copy_id = ? AND grabbed = 1`, cp.ID); n != 2 {
		t.Errorf("album_searches rows = %d, want 2", n)
	}

	// A second search does not send them again: they were just sent.
	r.grabber.mu.Lock()
	r.grabber.calls = nil
	r.grabber.mu.Unlock()
	before := r.srv.Searches()
	if err := r.svc.SearchCopy(bg(), cp.ID); err != nil {
		t.Fatal(err)
	}
	if len(r.grabber.all()) != 0 || r.srv.Searches() != before {
		t.Error("albums sent a moment ago were searched for again")
	}
	if h := r.history(cp.ID); !strings.HasPrefix(h[0].Note, "Nothing to look for") {
		t.Errorf("second note = %q", h[0].Note)
	}
}

func TestSearchCopyArtistSkipsWhatIsOnDiskOrUnwanted(t *testing.T) {
	r := newRig(t)
	cp, ids := r.chopin(
		albumRow{Name: "Nocturnes", Year: 1998, Tracks: 21},
		albumRow{Name: "Études", Year: 1985, Tracks: 12},
		albumRow{Name: "Ballades", Year: 2001, Tracks: 4, Unmonitored: true},
	)
	r.shelf.have[cp.ID] = Have{AlbumIDs: []int64{ids["Nocturnes"]}} // already in the library
	r.serve(chopinReleases)
	if err := r.svc.SearchCopy(bg(), cp.ID); err != nil {
		t.Fatal(err)
	}
	if got := r.grabber.titlesGrabbed(); len(got) != 1 || !strings.Contains(got[0], "Études") {
		t.Errorf("grabs = %v, want only Études (Nocturnes is on disk, Ballades is not monitored)", got)
	}
	for _, req := range r.srv.Requests(false) {
		if a := req.Query.Get("album"); a != "Études" {
			t.Errorf("searched for %q, want only Études", a)
		}
	}
}

func TestSearchCopyArtistNothingAcceptable(t *testing.T) {
	r := newRig(t)
	cp, _ := r.chopin(albumRow{Name: "Nocturnes", Year: 1998, Tracks: 21})
	r.always(
		tor("Frédéric Chopin - Ballades (2001) [FLAC]", 500*mbSize),          // another album
		tor("Frédéric Chopin - Nocturnes (1998) [MP3 128]", 60*mbSize),       // too low
		tor("Frédéric Chopin - Discography (1990-2015) [FLAC]", 9000*mbSize), // a pack
		tor("Frédéric Chopin - Nocturnes (1998) [FLAC]", 40*mbSize),          // far too small for 21 tracks
	)
	if err := r.svc.SearchCopy(bg(), cp.ID); err != nil {
		t.Fatal(err)
	}
	if len(r.grabber.all()) != 0 {
		t.Fatalf("grabbed %v, want nothing", r.grabber.titlesGrabbed())
	}
	h := r.history(cp.ID)
	if len(h) != 1 || h[0].Grabbed != 0 || h[0].Results != 4 || !strings.Contains(h[0].Note, `Nothing acceptable yet for "Nocturnes"`) {
		t.Errorf("history = %+v", h)
	}
	// Nothing was sent, so a later search looks again.
	r.always(tor("Frédéric Chopin - Nocturnes (1998) [FLAC]", 600*mbSize))
	if err := r.svc.SearchCopy(bg(), cp.ID); err != nil || len(r.grabber.all()) != 1 {
		t.Errorf("retry: %v, %d grabs", err, len(r.grabber.all()))
	}
}

func TestSearchCopyArtistRetriesWithText(t *testing.T) {
	r := newRig(t)
	cp, _ := r.chopin(albumRow{Name: "Nocturnes", Year: 1998, Tracks: 21})
	// A source that knows nothing under artist and album but finds the text.
	r.serve(func(q url.Values) []sourcestest.Item {
		if q.Has("q") {
			return []sourcestest.Item{tor("Frédéric Chopin - Nocturnes (1998) [FLAC]", 600*mbSize)}
		}
		return nil
	})
	if err := r.svc.SearchCopy(bg(), cp.ID); err != nil {
		t.Fatal(err)
	}
	if got := r.grabber.titlesGrabbed(); len(got) != 1 {
		t.Fatalf("grabs = %v", got)
	}
	reqs := r.srv.Requests(false)
	if len(reqs) != 2 || !reqs[0].Query.Has("artist") || reqs[1].Query.Get("q") != "Frédéric Chopin Nocturnes" || reqs[1].Query.Has("artist") {
		t.Errorf("requests = %+v, want a structured search then a text one", reqs)
	}
}

func TestSearchCopyArtistRotatesThroughManyAlbums(t *testing.T) {
	r := newRig(t)
	var albums []albumRow
	for i := 0; i < maxAlbumsPerRun+3; i++ {
		albums = append(albums, albumRow{Name: fmt.Sprintf("Album %02d", i), Year: 1990 + i, Tracks: 10})
	}
	cp, ids := r.chopin(albums...)
	r.always() // nothing found anywhere

	searched := func() map[string]bool {
		out := map[string]bool{}
		for _, req := range r.srv.Requests(false) {
			if a := req.Query.Get("album"); a != "" {
				out[a] = true
			}
		}
		return out
	}
	if err := r.svc.SearchCopy(bg(), cp.ID); err != nil {
		t.Fatal(err)
	}
	first := searched()
	if len(first) != maxAlbumsPerRun || first["Album 12"] {
		t.Fatalf("the first run searched %d albums %v, want %d (the oldest)", len(first), first, maxAlbumsPerRun)
	}
	if h := r.history(cp.ID); !strings.Contains(h[0].Note, "Stopped after 12 albums") {
		t.Errorf("note = %q", h[0].Note)
	}

	// The next run starts with the three it has not tried.
	r.skew += 7 * 60 * 60 * 1e9 // seven hours later
	if err := r.svc.SearchCopy(bg(), cp.ID); err != nil {
		t.Fatal(err)
	}
	all := searched()
	for _, name := range []string{"Album 12", "Album 13", "Album 14"} {
		if !all[name] {
			t.Errorf("%s was never searched; searched %d albums", name, len(all))
		}
	}
	if n := r.count(`SELECT COUNT(*) FROM album_searches WHERE copy_id = ?`, cp.ID); n != len(ids) {
		t.Errorf("album_searches rows = %d, want %d", n, len(ids))
	}
}

func TestSearchAlbum(t *testing.T) {
	r := newRig(t)
	cp, ids := r.chopin(albumRow{Name: "Nocturnes", Year: 1998, Tracks: 21}, albumRow{Name: "Études", Year: 1985, Tracks: 12})
	r.serve(chopinReleases)

	res, err := r.svc.SearchAlbum(bg(), cp.TitleID, ids["Nocturnes"])
	if err != nil {
		t.Fatal(err)
	}
	if !res.Grabbed || res.Release != "Frédéric Chopin - Nocturnes (1998) [FLAC]" || res.Quality != "FLAC" || res.DownloadID == 0 || res.Found != 4 {
		t.Errorf("result = %+v", res)
	}
	calls := r.grabber.all()
	if len(calls) != 1 || len(calls[0].Target.EpisodeIDs) != 1 || calls[0].Target.EpisodeIDs[0] != ids["Nocturnes"] || calls[0].Target.Kind != "artist" {
		t.Errorf("grab = %+v", calls)
	}
	for _, req := range r.srv.Requests(false) {
		if req.Query.Get("album") != "Nocturnes" {
			t.Errorf("searched %v, want only the Nocturnes album", req.Query)
		}
	}
	if h := r.history(cp.ID); len(h) != 1 || h[0].Grabbed != 1 || h[0].GrabbedFor[0] != AlbumKey(ids["Nocturnes"]) {
		t.Errorf("history = %+v", h)
	}

	// Nothing acceptable for an album: not an error, the result says why.
	r.always(tor("Frédéric Chopin - Ballades (2001) [FLAC]", 500*mbSize))
	res, err = r.svc.SearchAlbum(bg(), cp.TitleID, ids["Études"])
	if err != nil || res.Grabbed || res.Found != 1 || !strings.Contains(res.Note, `Wrong album: this is "Ballades", not "Études"`) {
		t.Errorf("no luck: %+v, %v", res, err)
	}
	r.always()
	if res, err = r.svc.SearchAlbum(bg(), cp.TitleID, ids["Études"]); err != nil || res.Grabbed || res.Note != "Nothing found" {
		t.Errorf("empty: %+v, %v", res, err)
	}
}

func TestSearchAlbumErrors(t *testing.T) {
	r := newRig(t)
	cp, ids := r.chopin(albumRow{Name: "Nocturnes", Year: 1998, Tracks: 21})
	movie := r.addMovie("Metropolis", 1927, "tt0017136", 19, 153)
	var ie *InvalidError

	if _, err := r.svc.SearchAlbum(bg(), cp.TitleID, 99999); !errors.As(err, &ie) {
		t.Errorf("an album the artist does not have: %v", err)
	}
	if _, err := r.svc.SearchAlbum(bg(), movie, ids["Nocturnes"]); !errors.As(err, &ie) {
		t.Errorf("a movie has no albums: %v", err)
	}
	if _, err := r.svc.SearchAlbum(bg(), 424242, 1); err == nil {
		t.Error("an unknown title should be an error")
	}
	r.svc.SetGrabber(nil)
	if _, err := r.svc.SearchAlbum(bg(), cp.TitleID, ids["Nocturnes"]); !errors.Is(err, ErrNoGrabber) {
		t.Errorf("no grabber: %v", err)
	}
	r.svc.SetGrabber(r.grabber)

	// No copy of the artist yet.
	r.copies.mu.Lock()
	delete(r.copies.copies, cp.ID)
	r.copies.mu.Unlock()
	if _, err := r.svc.SearchAlbum(bg(), cp.TitleID, ids["Nocturnes"]); !errors.Is(err, ErrNoCopy) {
		t.Errorf("no copy: %v", err)
	}
	r.copies.mu.Lock()
	r.copies.copies[cp.ID] = cp
	r.copies.mu.Unlock()

	// No sources.
	if err := r.src.Delete(bg(), r.ix.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := r.svc.SearchAlbum(bg(), cp.TitleID, ids["Nocturnes"]); !errors.Is(err, ErrNoSources) {
		t.Errorf("no sources: %v", err)
	}
}

func TestInteractiveForAnArtistNamesTheMusicFormat(t *testing.T) {
	r := newRig(t)
	cp, _ := r.chopin(albumRow{Name: "Nocturnes", Year: 1998, Tracks: 21})
	r.always(tor("Frédéric Chopin - Nocturnes (1998) [FLAC]", 600*mbSize), tor("Frédéric Chopin - Ballades (2001) [MP3 320]", 150*mbSize))
	res, err := r.svc.Interactive(bg(), cp.TitleID, "hd", 0, 0)
	if err != nil || len(res.Results) != 2 {
		t.Fatalf("Interactive = %+v, %v", res, err)
	}
	formats := map[string]bool{}
	for _, x := range res.Results {
		formats[x.Quality] = true
	}
	if !formats["FLAC"] || !formats["MP3-320kbps"] {
		t.Errorf("qualities = %v, want the music formats", formats)
	}
}
