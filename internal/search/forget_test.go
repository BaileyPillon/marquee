package search

import (
	"io"
	"log/slog"
	"path/filepath"
	"reflect"
	"testing"
	"time"

	"github.com/BaileyPillon/marquee/internal/core/db"
	"github.com/BaileyPillon/marquee/internal/grab"
	"github.com/BaileyPillon/marquee/internal/titles"
)

func newForgetService(t *testing.T) *Service {
	t.Helper()
	d, err := db.Open(filepath.Join(t.TempDir(), db.MainFile))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { d.Close() })
	if _, err := db.Migrate(bg(), d, db.SetMain); err != nil {
		t.Fatal(err)
	}
	return New(d, nil, nil, nil, Options{Logger: slog.New(slog.NewTextHandler(io.Discard, nil)), Pace: -1})
}

func (s *Service) addGrab(t *testing.T, copyID int64, at time.Time, keys ...string) {
	t.Helper()
	if err := s.store.AddHistory(bg(), History{CopyID: copyID, At: at, Grabbed: len(keys), GrabbedFor: keys}); err != nil {
		t.Fatal(err)
	}
}

func TestKeysMatchWhatTheSearchRemembers(t *testing.T) {
	if MovieKey() != movieKey() || SeasonKey(1) != "s01" || EpisodeKey(1, 4) != "s01e04" || EpisodeKey(12, 101) != "s12e101" {
		t.Errorf("keys: %s %s %s", MovieKey(), SeasonKey(1), EpisodeKey(1, 4))
	}
}

func TestForgetGrabMakesTheSearchLookAgain(t *testing.T) {
	s := newForgetService(t)
	now := time.Now()
	s.addGrab(t, 1, now.Add(-time.Hour), "s01e01", "s01e02")
	s.addGrab(t, 1, now.Add(-30*time.Minute), "s01e03")
	s.addGrab(t, 2, now.Add(-time.Hour), "s01e01") // another copy
	s.addGrab(t, 1, now.Add(-48*time.Hour), "s01e09")

	since := now.Add(-grabMemory)
	got, err := s.store.RecentlyGrabbed(bg(), 1, since)
	if err != nil || !reflect.DeepEqual(got, map[string]bool{"s01e01": true, "s01e02": true, "s01e03": true}) {
		t.Fatalf("before: %v, %v", got, err)
	}

	if err := s.ForgetGrab(bg(), 1, []string{"S01E02", " s01e03 ", "never-grabbed"}); err != nil {
		t.Fatal(err)
	}
	got, _ = s.store.RecentlyGrabbed(bg(), 1, since)
	if !reflect.DeepEqual(got, map[string]bool{"s01e01": true}) {
		t.Errorf("after: %v", got)
	}
	if other, _ := s.store.RecentlyGrabbed(bg(), 2, since); !other["s01e01"] {
		t.Error("another copy's grabs must not be forgotten")
	}
	// A search left with nothing grabbed counts as having grabbed nothing, so it does not hold anything back.
	if err := s.ForgetGrab(bg(), 1, []string{"s01e01"}); err != nil {
		t.Fatal(err)
	}
	if got, _ := s.store.RecentlyGrabbed(bg(), 1, since); len(got) != 0 {
		t.Errorf("everything was forgotten, got %v", got)
	}
	hist, _ := s.History(bg(), 1, 10)
	for _, h := range hist {
		if h.Grabbed > 0 && len(h.GrabbedFor) == 0 {
			t.Errorf("a search with no keys left should not claim a grab: %+v", h)
		}
	}
	if err := s.ForgetGrab(bg(), 1, nil); err != nil {
		t.Errorf("no keys is not an error: %v", err)
	}
}

func TestAForgottenMovieIsNeededAgain(t *testing.T) {
	s := newForgetService(t)
	d := &titles.Detail{Title: titles.Title{ID: 4, Kind: titles.KindMovie, Name: "Film"}}
	c := grab.Copy{ID: 1, TitleID: 4, Kind: "movie", Quality: grab.QualityHD}
	s.addGrab(t, 1, time.Now().Add(-time.Minute), MovieKey())

	n, err := s.needFor(bg(), c, d)
	if err != nil || n.movie {
		t.Fatalf("a movie sent a minute ago is not needed yet: %+v, %v", n, err)
	}
	if err := s.ForgetGrab(bg(), 1, []string{MovieKey()}); err != nil {
		t.Fatal(err)
	}
	if n, _ := s.needFor(bg(), c, d); !n.movie {
		t.Error("after the importer made it forget the grab, the movie is needed again")
	}
}
