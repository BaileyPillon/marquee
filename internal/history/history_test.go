package history

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"path/filepath"
	"testing"
	"time"

	"github.com/BaileyPillon/marquee/internal/core/db"
)

func bg() context.Context { return context.Background() }

func newService(t *testing.T) (*Service, *sql.DB, *time.Time) {
	t.Helper()
	d, err := db.Open(filepath.Join(t.TempDir(), db.MainFile))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { d.Close() })
	if _, err := db.Migrate(bg(), d, db.SetMain); err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, 11, 21, 12, 0, 0, 0, time.UTC)
	s := New(d, Options{Logger: slog.New(slog.NewTextHandler(io.Discard, nil)), Now: func() time.Time { now = now.Add(time.Minute); return now }})
	return s, d, &now
}

func TestRecordAndListNewestFirst(t *testing.T) {
	s, _, _ := newService(t)
	must := func(e Entry) {
		t.Helper()
		if err := s.Record(bg(), e); err != nil {
			t.Fatal(err)
		}
	}
	must(Entry{TitleID: 5, Event: Requested, Detail: map[string]any{"user": "Mia"}})
	must(Entry{TitleID: 5, CopyID: 9, Event: Approved})
	must(Entry{TitleID: 5, CopyID: 9, DownloadID: 3, Event: Grabbed, Detail: map[string]any{"release": "X.1080p"}})
	must(Entry{TitleID: 6, Event: Requested})

	items, err := s.ForTitle(bg(), 5, 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(items) != 3 || items[0].Event != Grabbed || items[1].Event != Approved || items[2].Event != Requested {
		t.Fatalf("items = %+v", items)
	}
	if items[0].CopyID == nil || *items[0].CopyID != 9 || items[0].DownloadID == nil || *items[0].DownloadID != 3 {
		t.Errorf("ids = %v %v", items[0].CopyID, items[0].DownloadID)
	}
	if items[2].CopyID != nil || items[2].DownloadID != nil {
		t.Error("ids that do not apply should be null")
	}
	var detail map[string]any
	if err := json.Unmarshal(items[0].Detail, &detail); err != nil || detail["release"] != "X.1080p" {
		t.Errorf("detail = %s, %v", items[0].Detail, err)
	}
	if string(items[1].Detail) != "{}" {
		t.Errorf("an entry with no detail stores {}, got %s", items[1].Detail)
	}
	if !items[0].At.After(items[1].At) {
		t.Error("newest first")
	}
	if other, _ := s.ForTitle(bg(), 6, 10); len(other) != 1 {
		t.Errorf("another title's history is separate: %v", other)
	}
	if none, err := s.ForTitle(bg(), 404, 10); err != nil || none == nil || len(none) != 0 {
		t.Errorf("no history is an empty list, not null: %v, %v", none, err)
	}
	if two, _ := s.ForTitle(bg(), 5, 2); len(two) != 2 {
		t.Errorf("limit not applied: %d", len(two))
	}
}

func TestEveryEventTheTableAllowsCanBeRecorded(t *testing.T) {
	s, _, _ := newService(t)
	for _, ev := range []string{Grabbed, Imported, Upgraded, Failed, Deleted, Renamed, Requested, Approved, Declined} {
		if err := s.Record(bg(), Entry{TitleID: 1, Event: ev}); err != nil {
			t.Errorf("%s: %v", ev, err)
		}
	}
}

func TestInvalidEntries(t *testing.T) {
	s, _, _ := newService(t)
	for _, e := range []Entry{{Event: Grabbed}, {TitleID: 1}, {TitleID: 1, Event: "exploded"}, {TitleID: -3, Event: Imported}} {
		if err := s.Record(bg(), e); !errors.Is(err, ErrInvalid) {
			t.Errorf("%+v: %v", e, err)
		}
	}
	if err := s.Record(bg(), Entry{TitleID: 1, Event: Imported, Detail: make(chan int)}); err == nil {
		t.Error("a detail that cannot be JSON should fail")
	}
}

func TestNoteNeverFailsTheCaller(t *testing.T) {
	var nilService *Service
	nilService.Note(bg(), Entry{TitleID: 1, Event: Imported}) // must not panic
	s, _, _ := newService(t)
	s.Note(bg(), Entry{Event: "bad"}) // logged, not returned
	s.Note(bg(), Entry{TitleID: 1, Event: Imported})
	if items, _ := s.ForTitle(bg(), 1, 10); len(items) != 1 {
		t.Errorf("items = %v", items)
	}
}

func TestLimitIsCapped(t *testing.T) {
	s, _, _ := newService(t)
	if err := s.Record(bg(), Entry{TitleID: 1, Event: Imported}); err != nil {
		t.Fatal(err)
	}
	if items, err := s.ForTitle(bg(), 1, 100000); err != nil || len(items) != 1 {
		t.Errorf("%v, %v", items, err)
	}
}
