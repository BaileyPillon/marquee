package decide

import (
	"strings"
	"testing"
	"time"

	"github.com/BaileyPillon/marquee/internal/parse"
	"github.com/BaileyPillon/marquee/internal/titles"
)

// ranked scores a list of release names the way the search service does and
// returns them in rank order. mods change a release by name.
func ranked(p Profile, t titles.Title, w Want, names []string, mods map[string]func(*Release)) []Entry {
	w.Now = testNow
	var entries []Entry
	for i, name := range names {
		r := rel(name)
		if m := mods[name]; m != nil {
			m(&r)
		}
		pr := parse.ParseRelease(r.Title)
		entries = append(entries, Entry{Release: r, Parsed: pr, Verdict: Score(p, t, w, pr, r), Priority: 25, Ref: i})
	}
	Rank(entries)
	return entries
}

func order(entries []Entry) []string {
	var out []string
	for _, e := range entries {
		out = append(out, e.Release.Title)
	}
	return out
}

func wantOrder(t *testing.T, what string, got []Entry, want ...string) {
	t.Helper()
	g := order(got)
	if len(g) != len(want) {
		t.Fatalf("%s: got %d entries, want %d: %q", what, len(g), len(want), g)
	}
	for i := range want {
		if g[i] != want[i] {
			t.Errorf("%s: position %d is %q, want %q (full order %q)", what, i, g[i], want[i], g)
			return
		}
	}
}

func TestRankSourceTiers(t *testing.T) {
	// Same size, seeders and age everywhere: only the source differs.
	names := []string{
		"Metropolis.1927.2160p.HDTV.x265-GRP",
		"Metropolis.1927.2160p.WEBRip.x265-GRP",
		"Metropolis.1927.2160p.UHD.BluRay.REMUX.HEVC-GRP",
		"Metropolis.1927.2160p.WEB-DL.x265-GRP",
		"Metropolis.1927.2160p.BluRay.x265-GRP",
	}
	got := ranked(movie4K(), metropolis, Want{}, names, nil)
	wantOrder(t, "source tiers", got,
		"Metropolis.1927.2160p.UHD.BluRay.REMUX.HEVC-GRP",
		"Metropolis.1927.2160p.BluRay.x265-GRP",
		"Metropolis.1927.2160p.WEB-DL.x265-GRP",
		"Metropolis.1927.2160p.WEBRip.x265-GRP",
		"Metropolis.1927.2160p.HDTV.x265-GRP")
	for i, e := range got {
		if !e.Verdict.Accepted {
			t.Errorf("entry %d should be accepted: %v", i, e.Verdict.Reasons)
		}
		if e.Ref < 0 || e.Ref >= len(names) || names[e.Ref] != e.Release.Title {
			t.Errorf("Ref %d does not point back at %q", e.Ref, e.Release.Title)
		}
	}
}

func TestRankPreferredResolutionBeatsSourceTier(t *testing.T) {
	got := ranked(movieHD(), metropolis, Want{}, []string{
		"Metropolis.1927.720p.BluRay.x264-GRP",
		"Metropolis.1927.1080p.WEBRip.x264-GRP",
	}, nil)
	wantOrder(t, "resolution before source", got, "Metropolis.1927.1080p.WEBRip.x264-GRP", "Metropolis.1927.720p.BluRay.x264-GRP")
	if !strings.Contains(strings.Join(got[1].Verdict.Reasons, "|"), "the picture size is further from what your profile prefers") {
		t.Errorf("reasons = %q", got[1].Verdict.Reasons)
	}
}

func TestRankHDRWhenPreferred(t *testing.T) {
	names := []string{"Metropolis.1927.2160p.BluRay.x265-GRP", "Metropolis.1927.2160p.BluRay.HDR.x265-GRP"}
	got := ranked(movie4K(), metropolis, Want{}, names, nil)
	wantOrder(t, "HDR preferred", got, names[1], names[0])

	// With the preference off, HDR is no advantage: the better-shared one wins.
	noPref := with2(movie4K(), func(p *Profile) { p.PreferHDR = false })
	got = ranked(noPref, metropolis, Want{}, names, map[string]func(*Release){
		names[0]: func(r *Release) { r.Seeders = 400 },
	})
	wantOrder(t, "HDR not preferred", got, names[0], names[1])
}

func TestRankPropersAndRepacks(t *testing.T) {
	names := []string{
		"Metropolis.1927.1080p.BluRay.x264-GRP",
		"Metropolis.1927.1080p.BluRay.REPACK.x264-GRP",
		"Metropolis.1927.1080p.BluRay.PROPER.x264-GRP",
		"Metropolis.1927.1080p.BluRay.REPACK2.x264-GRP",
	}
	got := ranked(movieHD(), metropolis, Want{}, names, nil)
	// A proper and a first repack fix the same amount; a second repack is a newer fix, and the original is last.
	if got[0].Release.Title != names[3] || got[3].Release.Title != names[0] {
		t.Errorf("order = %q", order(got))
	}
	if got[1].Verdict.Score != got[2].Verdict.Score {
		t.Errorf("a proper and a repack should tie: %d vs %d", got[1].Verdict.Score, got[2].Verdict.Score)
	}
	if !strings.Contains(strings.Join(got[0].Verdict.Reasons, "|"), "Proper release") {
		t.Errorf("reasons = %q", got[0].Verdict.Reasons)
	}
}

func TestRankPrefersTheSizeNearTheSweetSpot(t *testing.T) {
	// The HD profile allows 20 GB; the sweet spot is 70 percent of that.
	names := []string{"Metropolis.1927.1080p.BluRay.x264-AAA", "Metropolis.1927.1080p.BluRay.x264-BBB", "Metropolis.1927.1080p.BluRay.x264-CCC"}
	got := ranked(movieHD(), metropolis, Want{}, names, map[string]func(*Release){
		names[0]: func(r *Release) { r.Size = 2 * gbSize },
		names[1]: func(r *Release) { r.Size = 14 * gbSize },
		names[2]: func(r *Release) { r.Size = 20 * gbSize },
	})
	wantOrder(t, "size fit", got, names[1], names[2], names[0])
	if !strings.Contains(strings.Join(got[1].Verdict.Reasons, "|"), "sweet spot") {
		t.Errorf("reasons = %q", got[1].Verdict.Reasons)
	}
}

func TestRankSeedersThenAge(t *testing.T) {
	a, b, c := "Metropolis.1927.1080p.BluRay.x264-AAA", "Metropolis.1927.1080p.BluRay.x264-BBB", "Metropolis.1927.1080p.BluRay.x264-CCC"
	got := ranked(movieHD(), metropolis, Want{}, []string{a, b, c}, map[string]func(*Release){
		a: func(r *Release) { r.Seeders = 5 },
		b: func(r *Release) { r.Seeders = 500 },
		c: func(r *Release) { r.Seeders = 60 },
	})
	wantOrder(t, "seeders", got, b, c, a)

	// Inside one seeder bucket the exact number still decides.
	got = ranked(movieHD(), metropolis, Want{}, []string{a, b}, map[string]func(*Release){
		a: func(r *Release) { r.Seeders = 100 },
		b: func(r *Release) { r.Seeders = 110 },
	})
	wantOrder(t, "exact seeders", got, b, a)

	// All else equal, the newer one wins.
	got = ranked(movieHD(), metropolis, Want{}, []string{a, b}, map[string]func(*Release){
		a: func(r *Release) { r.PublishedAt = testNow.Add(-40 * 24 * time.Hour) },
		b: func(r *Release) { r.PublishedAt = testNow.Add(-2 * time.Hour) },
	})
	wantOrder(t, "age", got, b, a)
}

func TestRankPriorityBreaksTiesOnly(t *testing.T) {
	a, b := "Metropolis.1927.1080p.BluRay.x264-AAA", "Metropolis.1927.1080p.BluRay.x264-BBB"
	mk := func(seedA, seedB, prioA, prioB int) []Entry {
		var out []Entry
		for i, p := range []struct {
			name        string
			seeds, prio int
		}{{a, seedA, prioA}, {b, seedB, prioB}} {
			r := rel(p.name)
			r.Seeders = p.seeds
			pr := parse.ParseRelease(r.Title)
			out = append(out, Entry{Release: r, Parsed: pr, Verdict: Score(movieHD(), metropolis, Want{Now: testNow}, pr, r), Priority: p.prio, Ref: i})
		}
		Rank(out)
		return out
	}
	// Same score: the source with the lower priority number wins, even with fewer seeders.
	wantOrder(t, "priority tie", mk(20, 21, 25, 3), b, a)
	wantOrder(t, "priority tie reversed", mk(21, 20, 3, 25), a, b)
	// A clearly better-shared release beats a better-ranked source.
	wantOrder(t, "seeders beat priority", mk(5, 500, 3, 25), b, a)
}

func TestRankPutsAcceptedFirstThenNearestMisses(t *testing.T) {
	names := []string{
		"Metropolis.1927.1080p.BluRay.x264-AAA", // accepted
		"Metropolis.1927.2160p.BluRay.x265-BBB", // wrong resolution
		"Metropolis.1927.CAM.x264-CCC",          // one reason, but a camera copy
		"Metropolis.1927.2160p.CAM.x264-DDD",    // camera copy
		"Sintel.2010.1080p.BluRay.x264-EEE",     // wrong title
	}
	got := ranked(movieHD(), metropolis, Want{}, names, map[string]func(*Release){
		names[1]: func(r *Release) { r.Size = 30 * gbSize }, // also too big: two reasons
	})
	if !got[0].Verdict.Accepted || got[0].Release.Title != names[0] {
		t.Fatalf("first = %q", got[0].Release.Title)
	}
	for _, e := range got[1:] {
		if e.Verdict.Accepted {
			t.Errorf("%q should be rejected", e.Release.Title)
		}
	}
	// The release with two things wrong is nearest to last among the rejected.
	if last := got[len(got)-1]; last.Release.Title != names[1] {
		t.Errorf("last = %q (%v), want the one with two reasons", last.Release.Title, last.Verdict.Reasons)
	}
}

func TestRankWithNothingAcceptedKeepsTheRejections(t *testing.T) {
	got := ranked(movieHD(), metropolis, Want{}, []string{"Metropolis.1927.CAM.x264-AAA", "Metropolis.1927.TELESYNC.x264-BBB"}, nil)
	if len(got) != 2 || got[0].Verdict.Accepted || got[0].Verdict.Rejection == "" {
		t.Errorf("entries = %+v", got)
	}
	Rank(nil) // must not panic
}

func TestRankWritesTheReasonsInPlainWords(t *testing.T) {
	remux := "Metropolis.1927.2160p.UHD.BluRay.REMUX.HEVC-GRP"
	encode := "Metropolis.1927.2160p.BluRay.HDR.x265-GRP"
	webdl := "Metropolis.1927.2160p.WEB-DL.HDR.x265-GRP"
	big := "Metropolis.1927.2160p.BluRay.x265-HUGE"
	got := ranked(movie4K(), metropolis, Want{}, []string{encode, remux, webdl, big}, map[string]func(*Release){
		remux:  func(r *Release) { r.Size = 40 * gbSize },
		encode: func(r *Release) { r.Size = 15 * gbSize },
		webdl:  func(r *Release) { r.Size = 25 * gbSize },
		big:    func(r *Release) { r.Size = 68*gbSize + gbSize/10 },
	})
	wantOrder(t, "reasons", got, remux, encode, webdl, big)

	if got[0].Verdict.Reasons[0] != "Best match: 2160p remux under your size limit" {
		t.Errorf("best = %q", got[0].Verdict.Reasons)
	}
	want1 := []string{"Good fit: 2160p BluRay encode with HDR, less than half the size", "Ranked lower: BluRay encode ranks below remux"}
	if len(got[1].Verdict.Reasons) != 2 || got[1].Verdict.Reasons[0] != want1[0] || got[1].Verdict.Reasons[1] != want1[1] {
		t.Errorf("second = %q, want %q", got[1].Verdict.Reasons, want1)
	}
	// 25 GB is not under half of 40 GB: no "less than half".
	if r := got[2].Verdict.Reasons[0]; r != "Good fit: 2160p WEB-DL with HDR, under your size limit" {
		t.Errorf("third = %q", r)
	}
	if got[3].Verdict.Rejection != "Too big: 68.1 GB is over your 60 GB limit" {
		t.Errorf("fourth = %q", got[3].Verdict.Reasons)
	}
	// Ranking rewrote only the lead line; the verdict stays accepted with its score.
	for _, e := range got[:3] {
		if !e.Verdict.Accepted || e.Verdict.Score == 0 {
			t.Errorf("%q lost its verdict: %+v", e.Release.Title, e.Verdict)
		}
	}
}

func TestRankedLowerSaysWhichThingDecided(t *testing.T) {
	best := Verdict{source: "remux"}
	for part, want := range map[int]string{
		partSeeders: "Ranked lower: fewer people are sharing it",
		partAge:     "Ranked lower: it is older",
		partHDR:     "Ranked lower: it has no HDR, and your profile prefers it",
		partWords:   "Ranked lower: it has fewer of the words you prefer",
		partProper:  "Ranked lower: the best match is a newer fix of the same release",
	} {
		b, v := best, Verdict{}
		b.parts[part] = 5
		if got := rankedLower(b, v); got != want {
			t.Errorf("part %d: %q, want %q", part, got, want)
		}
	}
	if got := rankedLower(best, Verdict{}); !strings.Contains(got, "level with the best match") {
		t.Errorf("a tie: %q", got)
	}
}
