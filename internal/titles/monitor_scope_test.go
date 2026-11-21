package titles

import (
	"errors"
	"testing"
)

func TestMonitorSeasonsTurnsOnOnlyTheListedSeasons(t *testing.T) {
	f := newFixture(t)
	d := f.addShow("99001", false)
	if d.Monitored {
		t.Fatal("the show starts monitored")
	}
	if err := f.svc.MonitorSeasons(bg(), d.ID, []int{2, 9}); err != nil { // 9 is not a season of the show
		t.Fatal(err)
	}
	got, err := f.svc.Get(bg(), d.ID)
	if err != nil {
		t.Fatal(err)
	}
	if !got.Monitored {
		t.Error("the show itself is not monitored")
	}
	for _, s := range got.Seasons {
		if want := s.Number == 2; s.Monitored != want {
			t.Errorf("season %d monitored = %v, want %v", s.Number, s.Monitored, want)
		}
	}
	for _, e := range got.Episodes {
		if want := e.Season == 2; e.Monitored != want {
			t.Errorf("episode S%dE%d monitored = %v, want %v", e.Season, e.Number, e.Monitored, want)
		}
	}

	// Listing more seasons adds to what is on; nothing is switched off.
	if err := f.svc.MonitorSeasons(bg(), d.ID, []int{1}); err != nil {
		t.Fatal(err)
	}
	got, _ = f.svc.Get(bg(), d.ID)
	for _, s := range got.Seasons {
		if want := s.Number == 1 || s.Number == 2; s.Monitored != want {
			t.Errorf("after the second call: season %d monitored = %v, want %v", s.Number, s.Monitored, want)
		}
	}
	// An empty list only turns the show on.
	other := f.addShow("7", false)
	if err := f.svc.MonitorSeasons(bg(), other.ID, nil); err != nil {
		t.Fatal(err)
	}
	if got, _ = f.svc.Get(bg(), other.ID); !got.Monitored {
		t.Error("an empty list did not monitor the show")
	}
	if err := f.svc.MonitorSeasons(bg(), 9999, []int{1}); !errors.Is(err, ErrNotFound) {
		t.Errorf("no such title: %v, want ErrNotFound", err)
	}
}

func TestMonitorAlbumsTurnsOnOnlyTheListedAlbums(t *testing.T) {
	f := newFixture(t)
	d := f.addArtist(chopinMBID, false)
	if len(d.Albums) != 2 {
		t.Fatalf("%d albums", len(d.Albums))
	}
	pick := d.Albums[0].ID
	// An id that belongs to no album of this artist changes nothing.
	if err := f.svc.MonitorAlbums(bg(), d.ID, []int64{pick, 99999}); err != nil {
		t.Fatal(err)
	}
	got, _ := f.svc.Get(bg(), d.ID)
	if !got.Monitored {
		t.Error("the artist is not monitored")
	}
	for _, a := range got.Albums {
		if want := a.ID == pick; a.Monitored != want {
			t.Errorf("album %q monitored = %v, want %v", a.Name, a.Monitored, want)
		}
	}
	if err := f.svc.MonitorAlbums(bg(), 9999, []int64{1}); !errors.Is(err, ErrNotFound) {
		t.Errorf("no such title: %v, want ErrNotFound", err)
	}
}
