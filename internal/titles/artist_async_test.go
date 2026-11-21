package titles

import (
	"context"
	"errors"
	"strconv"
	"sync/atomic"
	"testing"
	"time"

	"github.com/BaileyPillon/marquee/internal/core/events"
	"github.com/BaileyPillon/marquee/internal/metadata"
)

// ---- studio albums are the default ----------------------------------------

// mixedArtist is an artist with one of each kind of release group.
func mixedArtist() *metadata.ArtistInfo {
	a := chopin()
	a.Albums = []metadata.AlbumInfo{
		{MBID: "b0000000-0000-4000-8000-000000000301", Title: "Studio One", Year: 2001, Type: "album"},
		{MBID: "b0000000-0000-4000-8000-000000000302", Title: "Studio Two", Year: 2003, Type: "Album", SecondaryTypes: []string{}},
		{MBID: "b0000000-0000-4000-8000-000000000303", Title: "The Film Score", Year: 2004, Type: "album", SecondaryTypes: []string{"Soundtrack"}},
		{MBID: "b0000000-0000-4000-8000-000000000304", Title: "Live at the Hall", Year: 2005, Type: "album", SecondaryTypes: []string{"Live"}},
		{MBID: "b0000000-0000-4000-8000-000000000305", Title: "Greatest Hits", Year: 2006, Type: "album", SecondaryTypes: []string{"Compilation"}},
		{MBID: "b0000000-0000-4000-8000-000000000306", Title: "Four Songs", Year: 2002, Type: "ep"},
		{MBID: "b0000000-0000-4000-8000-000000000307", Title: "One Song", Year: 2007, Type: "single"},
		{MBID: "b0000000-0000-4000-8000-000000000308", Title: "Live Hits", Year: 2008, Type: "album", SecondaryTypes: []string{"Live", "Compilation"}},
	}
	return a
}

func monitoredByName(d *Detail) map[string]bool {
	out := map[string]bool{}
	for _, a := range d.Albums {
		out[a.Name] = a.Monitored
	}
	return out
}

func TestOnlyStudioAlbumsStartMonitored(t *testing.T) {
	f := newFixture(t)
	f.artists.artists[chopinMBID] = mixedArtist()

	d := f.addArtist(chopinMBID, true)
	got := monitoredByName(d)
	want := map[string]bool{
		"Studio One": true, "Studio Two": true,
		"The Film Score": false, "Live at the Hall": false, "Greatest Hits": false,
		"Four Songs": false, "One Song": false, "Live Hits": false,
	}
	for name, w := range want {
		if got[name] != w {
			t.Errorf("album %q monitored = %v, want %v", name, got[name], w)
		}
	}
	if !d.Monitored {
		t.Error("the artist itself should be monitored")
	}
	for _, a := range d.Albums {
		if a.Studio != (a.Name == "Studio One" || a.Name == "Studio Two") {
			t.Errorf("album %q studio = %v", a.Name, a.Studio)
		}
	}
}

func TestAnArtistAddedUnmonitoredHasNoAlbumsMonitored(t *testing.T) {
	f := newFixture(t)
	f.artists.artists[chopinMBID] = mixedArtist()
	d := f.addArtist(chopinMBID, false)
	for name, on := range monitoredByName(d) {
		if on {
			t.Errorf("album %q is monitored on an unmonitored artist", name)
		}
	}
}

func TestMonitoringAnArtistTurnsOnStudioAlbumsOnlyAndOffTurnsOffAll(t *testing.T) {
	f := newFixture(t)
	f.artists.artists[chopinMBID] = mixedArtist()
	d := f.addArtist(chopinMBID, false)

	if err := f.svc.SetMonitored(bg(), d.ID, true); err != nil {
		t.Fatal(err)
	}
	got, _ := f.svc.Get(bg(), d.ID)
	m := monitoredByName(got)
	if !m["Studio One"] || !m["Studio Two"] || m["Four Songs"] || m["Greatest Hits"] || m["Live at the Hall"] || m["One Song"] {
		t.Errorf("after turning the artist on: %v", m)
	}

	// An album the person turned on by name stays on when the artist is turned on again.
	var ep int64
	for _, a := range got.Albums {
		if a.Name == "Four Songs" {
			ep = a.ID
		}
	}
	if err := f.svc.MonitorAlbums(bg(), d.ID, []int64{ep}); err != nil {
		t.Fatal(err)
	}
	if err := f.svc.SetMonitored(bg(), d.ID, true); err != nil {
		t.Fatal(err)
	}
	got, _ = f.svc.Get(bg(), d.ID)
	if !monitoredByName(got)["Four Songs"] {
		t.Error("the EP the person chose was switched off")
	}

	if err := f.svc.SetMonitored(bg(), d.ID, false); err != nil {
		t.Fatal(err)
	}
	got, _ = f.svc.Get(bg(), d.ID)
	for name, on := range monitoredByName(got) {
		if on {
			t.Errorf("album %q still monitored after turning the artist off", name)
		}
	}
}

func TestRefreshKeepsAnAlbumsChosenFlagButNewStudioAlbumsFollowTheArtist(t *testing.T) {
	f := newFixture(t)
	f.artists.artists[chopinMBID] = mixedArtist()
	d := f.addArtist(chopinMBID, true)

	next := mixedArtist()
	next.Albums = append(next.Albums,
		metadata.AlbumInfo{MBID: "b0000000-0000-4000-8000-000000000309", Title: "Studio Three", Year: 2010, Type: "album"},
		metadata.AlbumInfo{MBID: "b0000000-0000-4000-8000-000000000310", Title: "Remix Single", Year: 2011, Type: "single"})
	f.artists.artists[chopinMBID] = next
	after, err := f.svc.Refresh(bg(), d.ID)
	if err != nil {
		t.Fatal(err)
	}
	m := monitoredByName(after)
	if !m["Studio Three"] || m["Remix Single"] {
		t.Errorf("new albums after a refresh: %v", m)
	}
}

// ---- life span and release date --------------------------------------------

func TestTheArtistsLifeSpanAndAMoviesReleaseDateAreStoredAndReturned(t *testing.T) {
	f := newFixture(t)
	a := chopin()
	a.Begin, a.End = "1810-03-01", "1849-10-17"
	f.artists.artists[chopinMBID] = a
	d := f.addArtist(chopinMBID, true)
	if d.LifeSpan == nil || d.LifeSpan.Begin != "1810-03-01" || d.LifeSpan.End != "1849-10-17" {
		t.Fatalf("life span = %+v", d.LifeSpan)
	}
	got, _ := f.svc.Get(bg(), d.ID)
	if got.LifeSpan == nil || *got.LifeSpan != *d.LifeSpan {
		t.Errorf("Get life span = %+v", got.LifeSpan)
	}
	list, err := f.svc.List(bg(), ListFilter{Kind: KindArtist})
	if err != nil || len(list.Items) != 1 || list.Items[0].LifeSpan == nil || list.Items[0].LifeSpan.End != "1849-10-17" {
		t.Errorf("list = %+v, %v", list, err)
	}

	// A band still together has a begin and no end; a refresh keeps what it learns.
	b := chopin()
	b.Begin, b.End, b.Ended = "1998", "", false
	f.artists.artists[chopinMBID] = b
	after, err := f.svc.Refresh(bg(), d.ID)
	if err != nil {
		t.Fatal(err)
	}
	if after.LifeSpan == nil || after.LifeSpan.Begin != "1998" || after.LifeSpan.End != "" {
		t.Errorf("after refresh: %+v", after.LifeSpan)
	}

	mov := f.addMovie("19", true)
	if mov.ReleaseDate != "1927-01-10" {
		t.Errorf("release date = %q", mov.ReleaseDate)
	}
	if mov.LifeSpan != nil {
		t.Error("a movie has no life span")
	}
	if sh := f.addShow("99001", false); sh.LifeSpan != nil || sh.ReleaseDate != "" {
		t.Errorf("show: %+v %q", sh.LifeSpan, sh.ReleaseDate)
	}
}

// ---- adding an artist in the background -------------------------------------

// baseArtists is a provider that can answer with the artist alone at once while
// the full answer waits for gate.
type baseArtists struct {
	*fakeArtists
	gate    chan struct{}
	fullErr atomic.Pointer[error]
	full    atomic.Int32
	base    atomic.Int32
}

func (b *baseArtists) ArtistBase(ctx context.Context, id string) (*metadata.ArtistInfo, error) {
	b.base.Add(1)
	a, err := b.fakeArtists.Artist(ctx, id)
	if err != nil {
		return nil, err
	}
	a.Albums = nil
	return a, nil
}

func (b *baseArtists) Artist(ctx context.Context, id string) (*metadata.ArtistInfo, error) {
	b.full.Add(1)
	select {
	case <-b.gate:
	case <-ctx.Done():
		return nil, ctx.Err()
	}
	if e := b.fullErr.Load(); e != nil {
		return nil, *e
	}
	return b.fakeArtists.Artist(ctx, id)
}

type asyncFixture struct {
	*fixture
	svc *Service
	p   *baseArtists
	hub *events.Hub
	ev  <-chan events.Message
}

func newAsyncFixture(t *testing.T) *asyncFixture {
	t.Helper()
	f := newFixture(t)
	hub := events.NewHub(nil)
	t.Cleanup(hub.Close)
	c, unsub := hub.Subscribe(Topic)
	t.Cleanup(unsub)
	p := &baseArtists{fakeArtists: f.artists, gate: make(chan struct{})}
	f.artists.artists[chopinMBID] = mixedArtist()
	svc := NewService(f.db, Providers{Movies: f.movies, Shows: f.shows, Artists: p}, Options{Now: f.clock.Now, Hub: hub})
	t.Cleanup(svc.Close)
	return &asyncFixture{fixture: f, svc: svc, p: p, hub: hub, ev: c.C()}
}

func (a *asyncFixture) next(t *testing.T) string {
	t.Helper()
	select {
	case m := <-a.ev:
		return string(m.Data)
	case <-time.After(5 * time.Second):
		t.Fatal("no titles event")
		return ""
	}
}

func TestAddAsyncStoresTheArtistAtOnceAndLoadsTheAlbumsInTheBackground(t *testing.T) {
	a := newAsyncFixture(t)
	start := time.Now()
	d, created, hydrating, err := a.svc.AddAsync(bg(), KindArtist, metadata.ProviderMusicBrainz, chopinMBID, AddOptions{Monitored: true})
	if err != nil {
		t.Fatal(err)
	}
	if time.Since(start) > 2*time.Second {
		t.Errorf("AddAsync waited for the albums (%v)", time.Since(start))
	}
	if !created || !hydrating || d.Name != "Frédéric Chopin" || len(d.Albums) != 0 || !d.Monitored {
		t.Fatalf("answer = created %v hydrating %v %+v", created, hydrating, d.Title)
	}
	if d.LifeSpan == nil {
		t.Error("the base artist row has no life span")
	}
	if !a.svc.Hydrating(d.ID) {
		t.Error("Hydrating is false while the load waits")
	}
	if a.fixture.count(`SELECT COUNT(*) FROM albums`) != 0 {
		t.Error("albums were stored before the load finished")
	}

	// Adding the same artist again while it loads is the same artist, still loading.
	d2, created2, hydrating2, err := a.svc.AddAsync(bg(), KindArtist, metadata.ProviderMusicBrainz, chopinMBID, AddOptions{Monitored: true})
	if err != nil || created2 || !hydrating2 || d2.ID != d.ID {
		t.Fatalf("second add = %v %v %v %v", d2, created2, hydrating2, err)
	}
	waitFor(t, func() bool { return a.p.full.Load() >= 1 })
	if a.p.full.Load() != 1 {
		t.Errorf("the albums were requested %d times, want 1", a.p.full.Load())
	}

	close(a.p.gate)
	ev := a.next(t)
	if ev != `{"event":"hydrated","id":`+strconv.FormatInt(d.ID, 10)+`}` {
		t.Errorf("event = %s", ev)
	}
	got, err := a.svc.Get(bg(), d.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(got.Albums) != 8 {
		t.Fatalf("albums = %d, want 8", len(got.Albums))
	}
	m := monitoredByName(got)
	if !m["Studio One"] || !m["Studio Two"] || m["Four Songs"] || m["Live at the Hall"] {
		t.Errorf("monitored = %v", m)
	}
	if a.svc.Hydrating(d.ID) {
		t.Error("still hydrating after the event")
	}
	// A finished artist answers like Add does.
	_, created3, hydrating3, err := a.svc.AddAsync(bg(), KindArtist, metadata.ProviderMusicBrainz, chopinMBID, AddOptions{})
	if err != nil || created3 || hydrating3 {
		t.Errorf("third add = %v %v %v", created3, hydrating3, err)
	}
}

func TestAddAsyncKeepsAMonitoringChangeMadeWhileTheAlbumsLoad(t *testing.T) {
	a := newAsyncFixture(t)
	d, _, _, err := a.svc.AddAsync(bg(), KindArtist, metadata.ProviderMusicBrainz, chopinMBID, AddOptions{Monitored: true})
	if err != nil {
		t.Fatal(err)
	}
	if err := a.svc.SetMonitored(bg(), d.ID, false); err != nil {
		t.Fatal(err)
	}
	close(a.p.gate)
	a.next(t)
	got, _ := a.svc.Get(bg(), d.ID)
	if got.Monitored {
		t.Error("the artist was switched on again")
	}
	for name, on := range monitoredByName(got) {
		if on {
			t.Errorf("album %q monitored for an artist turned off during the load", name)
		}
	}
}

func TestAddAsyncFailureIsAnEventAndTheNextAddTriesAgain(t *testing.T) {
	a := newAsyncFixture(t)
	boom := errors.New("musicbrainz is down")
	a.p.fullErr.Store(&boom)
	close(a.p.gate)

	d, _, hydrating, err := a.svc.AddAsync(bg(), KindArtist, metadata.ProviderMusicBrainz, chopinMBID, AddOptions{Monitored: true})
	if err != nil || !hydrating {
		t.Fatalf("add = %v %v", hydrating, err)
	}
	if ev := a.next(t); ev != `{"event":"hydrate_failed","id":`+strconv.FormatInt(d.ID, 10)+`}` {
		t.Errorf("event = %s", ev)
	}
	if got, _ := a.svc.Get(bg(), d.ID); len(got.Albums) != 0 {
		t.Errorf("albums after a failed load: %d", len(got.Albums))
	}

	// The artist is in the catalog without albums; adding it again loads them.
	a.p.fullErr.Store(nil)
	_, created, hydrating, err := a.svc.AddAsync(bg(), KindArtist, metadata.ProviderMusicBrainz, chopinMBID, AddOptions{Monitored: true})
	if err != nil || created || !hydrating {
		t.Fatalf("retry = %v %v %v", created, hydrating, err)
	}
	if ev := a.next(t); ev != `{"event":"hydrated","id":`+strconv.FormatInt(d.ID, 10)+`}` {
		t.Errorf("event = %s", ev)
	}
	if got, _ := a.svc.Get(bg(), d.ID); len(got.Albums) != 8 {
		t.Errorf("albums = %d", len(got.Albums))
	}
}

func TestAddAsyncLeavesMoviesAndShowsSynchronous(t *testing.T) {
	a := newAsyncFixture(t)
	for _, tc := range []struct {
		kind, provider, id string
	}{
		{KindMovie, metadata.ProviderTMDB, "19"},
		{KindShow, metadata.ProviderTVmaze, "99001"},
	} {
		d, created, hydrating, err := a.svc.AddAsync(bg(), tc.kind, tc.provider, tc.id, AddOptions{Monitored: true})
		if err != nil || !created || hydrating || d.Kind != tc.kind {
			t.Errorf("%s: %v %v %v %v", tc.kind, d, created, hydrating, err)
		}
	}
}

func TestAddAsyncChecksItsInputBeforeAnyRequest(t *testing.T) {
	a := newAsyncFixture(t)
	for name, tc := range map[string]struct{ provider, id string }{
		"wrong provider": {metadata.ProviderTMDB, chopinMBID},
		"not a uuid":     {metadata.ProviderMusicBrainz, "chopin"},
	} {
		if _, _, _, err := a.svc.AddAsync(bg(), KindArtist, tc.provider, tc.id, AddOptions{}); !isInvalid(err) {
			t.Errorf("%s: err = %v, want invalid", name, err)
		}
	}
	if a.p.base.Load() != 0 {
		t.Error("a bad id reached the provider")
	}
	if _, _, _, err := a.svc.AddAsync(bg(), KindArtist, metadata.ProviderMusicBrainz, "a1b2c3d4-0000-4000-8000-0000000000ff", AddOptions{}); !errors.Is(err, metadata.ErrNotFound) {
		t.Errorf("unknown artist = %v", err)
	}
}

func TestAddAsyncWithAProviderThatCannotAnswerQuicklyAddsAsAddDoes(t *testing.T) {
	f := newFixture(t) // fakeArtists has no ArtistBase
	d, created, hydrating, err := f.svc.AddAsync(bg(), KindArtist, metadata.ProviderMusicBrainz, chopinMBID, AddOptions{Monitored: true})
	if err != nil || !created || hydrating || len(d.Albums) != 2 {
		t.Fatalf("add = %d albums, created %v hydrating %v, %v", len(d.Albums), created, hydrating, err)
	}
}

func TestCloseStopsAHydrationThatIsWaiting(t *testing.T) {
	a := newAsyncFixture(t)
	if _, _, _, err := a.svc.AddAsync(bg(), KindArtist, metadata.ProviderMusicBrainz, chopinMBID, AddOptions{Monitored: true}); err != nil {
		t.Fatal(err)
	}
	done := make(chan struct{})
	go func() { a.svc.Close(); close(done) }()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("Close did not return while a load was waiting")
	}
	select {
	case m := <-a.ev:
		t.Errorf("an event went out after Close: %s", m.Data)
	default:
	}
	if _, _, hydrating, err := a.svc.AddAsync(bg(), KindArtist, metadata.ProviderMusicBrainz, chopinMBID, AddOptions{}); err != nil || hydrating {
		t.Errorf("after Close: hydrating %v, %v", hydrating, err)
	}
}

func waitFor(t *testing.T, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for !cond() {
		if time.Now().After(deadline) {
			t.Fatal("timed out waiting")
		}
		time.Sleep(5 * time.Millisecond)
	}
}
