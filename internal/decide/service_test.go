package decide

import (
	"context"
	"database/sql"
	"errors"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/BaileyPillon/marquee/internal/core/db"
	"github.com/BaileyPillon/marquee/internal/grab"
)

func bg() context.Context { return context.Background() }

func newService(t *testing.T) (*Service, *sql.DB) {
	t.Helper()
	d, err := db.Open(filepath.Join(t.TempDir(), "marquee.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { d.Close() })
	if _, err := db.Migrate(bg(), d, db.SetMain); err != nil {
		t.Fatal(err)
	}
	return New(NewStore(d), Options{Now: func() time.Time { return testNow }}), d
}

// addTitle puts a catalog row in, which the blocklist's foreign key needs.
func addTitle(t *testing.T, d *sql.DB, name string) int64 {
	t.Helper()
	ts := db.TS(testNow)
	res, err := d.Exec(`INSERT INTO titles (kind, name, sort_name, added_at, updated_at, refreshed_at) VALUES ('movie', ?, ?, ?, ?, ?)`,
		name, name, ts, ts, ts)
	if err != nil {
		t.Fatal(err)
	}
	id, _ := res.LastInsertId()
	return id
}

func isInvalid(err error) bool {
	var ie *InvalidError
	return errors.As(err, &ie)
}

func TestSeededProfiles(t *testing.T) {
	s, _ := newService(t)
	list, err := s.Profiles(bg())
	if err != nil {
		t.Fatal(err)
	}
	if len(list) != 5 {
		t.Fatalf("%d profiles, want 5", len(list))
	}
	type row struct {
		name, kind, quality string
	}
	var got []row
	for _, p := range list {
		got = append(got, row{p.Name, p.Kind, p.Quality})
		if p.MinSeeders != 1 {
			t.Errorf("%s/%s: min seeders = %d, want 1", p.Kind, p.Quality, p.MinSeeders)
		}
		if p.CreatedAt.IsZero() || p.ID == 0 {
			t.Errorf("%s/%s: %+v", p.Kind, p.Quality, p)
		}
	}
	want := []row{{"HD", "movie", "hd"}, {"4K", "movie", "uhd"}, {"HD", "show", "hd"}, {"4K", "show", "uhd"}, {"Lossless", "artist", "hd"}}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("profiles = %v, want %v", got, want)
	}

	hd, _ := s.Profile(bg(), KindMovie, grab.QualityHD)
	if hd.PreferredRes != 1080 || !reflect.DeepEqual(hd.AllowedRes, []int{1080, 720}) || hd.MaxSizeMB == nil || *hd.MaxSizeMB != 20*1024 || hd.PreferHDR {
		t.Errorf("movie HD = %+v", hd)
	}
	uhd, _ := s.Profile(bg(), KindMovie, grab.QualityUHD)
	if uhd.PreferredRes != 2160 || !reflect.DeepEqual(uhd.AllowedRes, []int{2160}) || uhd.MaxSizeMB == nil || *uhd.MaxSizeMB != 60*1024 || !uhd.PreferHDR {
		t.Errorf("movie 4K = %+v", uhd)
	}
	show, _ := s.Profile(bg(), KindShow, grab.QualityHD)
	if show.PreferredRes != 1080 || !reflect.DeepEqual(show.AllowedRes, []int{1080, 720}) || show.MaxSizePerHourMB == nil || *show.MaxSizePerHourMB != 4*1024 || show.MaxSizeMB != nil {
		t.Errorf("show HD = %+v", show)
	}
	show4k, _ := s.Profile(bg(), KindShow, grab.QualityUHD)
	if !reflect.DeepEqual(show4k.AllowedRes, []int{2160}) || show4k.MaxSizePerHourMB != nil {
		t.Errorf("show 4K = %+v", show4k)
	}
	music, _ := s.Profile(bg(), KindArtist, grab.QualityHD)
	if music.Name != "Lossless" || !reflect.DeepEqual(music.AllowedSources, []string{"FLAC", "MP3-320"}) || len(music.AllowedRes) != 0 {
		t.Errorf("artist = %+v", music)
	}
	if _, err := s.Profile(bg(), KindArtist, grab.QualityUHD); !errors.Is(err, ErrNotFound) {
		t.Errorf("there is no 4K music profile: %v", err)
	}

	// Camera copies are refused by every seeded profile, whatever it lists.
	for _, p := range list {
		if p.Kind == KindArtist {
			continue
		}
		for _, name := range []string{"CAM", "TELESYNC", "TELECINE", "DVDSCR"} {
			r := rel("Metropolis.1927.1080p." + name + ".x264-GRP")
			if p.Quality == grab.QualityUHD {
				r.Title = strings.Replace(r.Title, "1080p", "2160p", 1)
			}
			if v := Score(p, metropolis, Want{}, parseOf(r.Title), r); v.Accepted {
				t.Errorf("%s/%s accepted a %s release", p.Kind, p.Quality, name)
			}
		}
	}
}

func TestUpdateProfile(t *testing.T) {
	s, _ := newService(t)
	hd, _ := s.Profile(bg(), KindMovie, grab.QualityHD)

	in := InputOf(hd)
	in.Name = "Small HD"
	in.MaxSizeMB = ip(8000)
	in.MinSizeMB = ip(500)
	in.PreferHDR = true
	in.MinSeeders = 4
	in.RejectedWords = []string{" HC ", "hc", "Korsub", ""}
	in.PreferredWords = []string{"Criterion"}
	in.AllowedRes = []int{1080, 720, 720}
	upgrade := "Bluray-1080p"
	in.UpgradeUntil = &upgrade
	got, err := s.UpdateProfile(bg(), hd.ID, in)
	if err != nil {
		t.Fatal(err)
	}
	if got.Name != "Small HD" || *got.MaxSizeMB != 8000 || *got.MinSizeMB != 500 || !got.PreferHDR || got.MinSeeders != 4 {
		t.Errorf("updated = %+v", got)
	}
	if !reflect.DeepEqual(got.RejectedWords, []string{"HC", "Korsub"}) || !reflect.DeepEqual(got.AllowedRes, []int{720, 1080}) {
		t.Errorf("lists were not tidied: rejected %v res %v", got.RejectedWords, got.AllowedRes)
	}
	if got.UpgradeUntil == nil || *got.UpgradeUntil != "Bluray-1080p" || got.Kind != KindMovie || got.Quality != grab.QualityHD {
		t.Errorf("updated = %+v (kind and quality never change)", got)
	}
	again, _ := s.Profile(bg(), KindMovie, grab.QualityHD)
	if again.Name != "Small HD" {
		t.Error("the change was not stored")
	}

	// A size can be cleared by sending nil.
	in = InputOf(got)
	in.MaxSizeMB, in.UpgradeUntil = nil, nil
	if got, err = s.UpdateProfile(bg(), hd.ID, in); err != nil || got.MaxSizeMB != nil || got.UpgradeUntil != nil {
		t.Errorf("cleared = %+v %v", got, err)
	}
}

func TestUpdateProfileValidation(t *testing.T) {
	s, _ := newService(t)
	hd, _ := s.Profile(bg(), KindMovie, grab.QualityHD)
	music, _ := s.Profile(bg(), KindArtist, grab.QualityHD)
	tweak := func(p Profile, f func(*ProfileInput)) ProfileInput { in := InputOf(p); f(&in); return in }
	bad := map[string]struct {
		id int64
		in ProfileInput
	}{
		"no name":              {hd.ID, tweak(hd, func(i *ProfileInput) { i.Name = " " })},
		"no resolutions":       {hd.ID, tweak(hd, func(i *ProfileInput) { i.AllowedRes = nil })},
		"unknown resolution":   {hd.ID, tweak(hd, func(i *ProfileInput) { i.AllowedRes = []int{1080, 900} })},
		"preferred not listed": {hd.ID, tweak(hd, func(i *ProfileInput) { i.PreferredRes = 2160 })},
		"unknown source":       {hd.ID, tweak(hd, func(i *ProfileInput) { i.AllowedSources = []string{"VHS"} })},
		"min above max":        {hd.ID, tweak(hd, func(i *ProfileInput) { i.MinSizeMB = ip(30000) })},
		"negative size":        {hd.ID, tweak(hd, func(i *ProfileInput) { i.MaxSizeMB = ip(-1) })},
		"seeders out of range": {hd.ID, tweak(hd, func(i *ProfileInput) { i.MinSeeders = -2 })},
		"word too long":        {hd.ID, tweak(hd, func(i *ProfileInput) { i.RejectedWords = []string{strings.Repeat("x", 41)} })},
		"too many words": {hd.ID, tweak(hd, func(i *ProfileInput) {
			i.RejectedWords = make([]string, 51)
			for j := range i.RejectedWords {
				i.RejectedWords[j] = string(rune('a'+j%26)) + string(rune('a'+j/26))
			}
		})},
		"music: unknown format": {music.ID, tweak(music, func(i *ProfileInput) { i.AllowedSources = []string{"WAV"} })},
		"music: no format":      {music.ID, tweak(music, func(i *ProfileInput) { i.AllowedSources = nil })},
	}
	for name, c := range bad {
		if _, err := s.UpdateProfile(bg(), c.id, c.in); !isInvalid(err) {
			t.Errorf("%s: err = %v, want an InvalidError", name, err)
		}
	}
	if _, err := s.UpdateProfile(bg(), 999, InputOf(hd)); !errors.Is(err, ErrNotFound) {
		t.Errorf("missing profile: %v", err)
	}
	// The same name as another profile of the same kind.
	uhd, _ := s.Profile(bg(), KindMovie, grab.QualityUHD)
	if _, err := s.UpdateProfile(bg(), uhd.ID, tweak(uhd, func(i *ProfileInput) { i.Name = "HD" })); !errors.Is(err, ErrDuplicateName) {
		t.Errorf("duplicate name: %v", err)
	}
	// Another kind may reuse a name (the seeds do: HD for movies and for shows).
	if _, err := s.UpdateProfile(bg(), uhd.ID, tweak(uhd, func(i *ProfileInput) { i.Name = "Lossless" })); err != nil {
		t.Errorf("a name used by another kind: %v", err)
	}
	// Nothing above changed the stored profile.
	if p, _ := s.Profile(bg(), KindMovie, grab.QualityHD); p.Name != "HD" || *p.MaxSizeMB != 20480 {
		t.Errorf("a refused update changed the profile: %+v", p)
	}
}

func TestMusicProfileKeepsItsFormatsInOrder(t *testing.T) {
	s, _ := newService(t)
	music, _ := s.Profile(bg(), KindArtist, grab.QualityHD)
	in := InputOf(music)
	in.AllowedSources = []string{"mp3-320", "flac", "FLAC"}
	in.AllowedRes = []int{1080} // not meaningful for music: ignored
	got, err := s.UpdateProfile(bg(), music.ID, in)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(got.AllowedSources, []string{"MP3-320", "FLAC"}) || len(got.AllowedRes) != 0 {
		t.Errorf("music profile = %+v", got)
	}
	// MP3 now ranks first.
	a := Score(got, chopin, Want{Now: testNow}, parseOf(""), with(rel("Chopin - Etudes [FLAC]"), nil2))
	b := Score(got, chopin, Want{Now: testNow}, parseOf(""), with(rel("Chopin - Etudes MP3 320kbps"), nil2))
	if !a.Accepted || !b.Accepted || b.Score <= a.Score {
		t.Errorf("the first format in the list should rank first: flac %d mp3 %d", a.Score, b.Score)
	}
}

func nil2(*Release) {}

// ---- blocklist ---------------------------------------------------------------------

func TestBlocklist(t *testing.T) {
	s, d := newService(t)
	title := addTitle(t, d, "Metropolis")
	other := addTitle(t, d, "Sintel")

	e1, err := s.Block(bg(), BlockEntry{TitleID: title, GUID: " g-1 ", InfoHash: strings.ToUpper(hashA), ReleaseTitle: "Metropolis.1927.1080p.BluRay.x264-GRP", Reason: "wrong cut"})
	if err != nil {
		t.Fatal(err)
	}
	if e1.ID == 0 || e1.GUID != "g-1" || e1.InfoHash != hashA || !e1.CreatedAt.Equal(testNow) {
		t.Errorf("entry = %+v (guid trimmed, hash lower-cased, stamped)", e1)
	}
	if _, err := s.Block(bg(), BlockEntry{TitleID: title, ReleaseTitle: "Metropolis.1927.720p.WEB-DL.x264-GRP"}); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Block(bg(), BlockEntry{TitleID: other, GUID: "g-9", Reason: strings.Repeat("é", 400)}); err != nil {
		t.Fatal(err)
	}
	for name, e := range map[string]BlockEntry{
		"no title":   {GUID: "g"},
		"no release": {TitleID: title},
	} {
		if _, err := s.Block(bg(), e); !isInvalid(err) {
			t.Errorf("%s: err = %v, want an InvalidError", name, err)
		}
	}

	list, total, err := s.Blocklist(bg(), 10, 0)
	if err != nil || total != 3 || len(list) != 3 {
		t.Fatalf("list %d total %d err %v", len(list), total, err)
	}
	if list[0].TitleID != other || len([]rune(list[0].Reason)) != 300 {
		t.Errorf("newest first, with a long reason cut to 300 runes: %+v", list[0])
	}
	if page, total, _ := s.Blocklist(bg(), 1, 1); len(page) != 1 || total != 3 {
		t.Errorf("paging: %d of %d", len(page), total)
	}

	// The index finds a release by guid, by info hash, or by exact title.
	idx, err := s.BlocksFor(bg(), title)
	if err != nil {
		t.Fatal(err)
	}
	checks := []struct {
		guid, hash, title string
		blocked           bool
		reason            string
	}{
		{"g-1", "", "x", true, "wrong cut"},
		{"nope", hashA, "x", true, "wrong cut"},
		{"nope", "", "metropolis.1927.1080p.bluray.x264-grp", false, ""}, // the first entry has a guid: title alone does not match it
		{"nope", "", "Metropolis.1927.720p.WEB-DL.x264-GRP", true, "no reason was given"},
		{"nope", "", "something else", false, ""},
		{"g-9", "", "x", false, ""}, // blocked for another title
	}
	for _, c := range checks {
		reason, blocked := idx.Reason(c.guid, c.hash, c.title)
		if blocked != c.blocked || reason != c.reason {
			t.Errorf("Reason(%q, %q, %q) = %q %v, want %q %v", c.guid, c.hash, c.title, reason, blocked, c.reason, c.blocked)
		}
	}
	var nilIdx *BlockIndex
	if _, blocked := nilIdx.Reason("g-1", "", ""); blocked {
		t.Error("a nil index blocks nothing")
	}

	// Unblocking.
	if err := s.Unblock(bg(), e1.ID); err != nil {
		t.Fatal(err)
	}
	if err := s.Unblock(bg(), e1.ID); !errors.Is(err, ErrNotFound) {
		t.Errorf("second unblock: %v", err)
	}
	idx, _ = s.BlocksFor(bg(), title)
	if _, blocked := idx.Reason("g-1", hashA, ""); blocked {
		t.Error("an unblocked release is still blocked")
	}
}

func TestBlockedReleaseIsRejectedWithTheReason(t *testing.T) {
	s, d := newService(t)
	title := addTitle(t, d, "Metropolis")
	if _, err := s.Block(bg(), BlockEntry{TitleID: title, GUID: "g-1", Reason: "burned-in subtitles"}); err != nil {
		t.Fatal(err)
	}
	idx, _ := s.BlocksFor(bg(), title)
	r := rel("Metropolis.1927.1080p.BluRay.x264-GRP")
	r.GUID = "g-1"
	if reason, blocked := idx.Reason(r.GUID, r.InfoHash, r.Title); blocked {
		r.Blocked = reason
	}
	v := score(movieHD(), metropolis, Want{}, r)
	if v.Accepted || v.Rejection != "You blocked this release before: burned-in subtitles" {
		t.Errorf("verdict = %+v", v)
	}
}

func TestDeletingATitleClearsItsBlocklist(t *testing.T) {
	s, d := newService(t)
	title := addTitle(t, d, "Metropolis")
	if _, err := s.Block(bg(), BlockEntry{TitleID: title, GUID: "g-1"}); err != nil {
		t.Fatal(err)
	}
	if _, err := d.Exec(`DELETE FROM titles WHERE id = ?`, title); err != nil {
		t.Fatal(err)
	}
	if _, total, _ := s.Blocklist(bg(), 10, 0); total != 0 {
		t.Errorf("%d blocklist rows left for a deleted title", total)
	}
}
