package decide

import (
	"sort"

	"github.com/BaileyPillon/marquee/internal/parse"
)

// Entry is one scored release, ready to be ranked against the others.
type Entry struct {
	Release  Release
	Parsed   parse.Release
	Verdict  Verdict
	Priority int // the source's priority: lower wins a tie
	Ref      int // the caller's own index for this entry; Rank leaves it alone
}

// Rank orders entries best first and rewrites the reasons so they say how each
// one stands: the winner is the "Best match", the other acceptable ones say why
// they came second. Accepted releases come first, ordered by score (resolution
// closest to the preferred one, then source, HDR, proper or repack, size near
// the sweet spot, seeders, age), then by the source's priority, then by the
// number of seeders and how new it is. Rejected ones follow, the nearest misses
// first.
func Rank(entries []Entry) {
	sort.SliceStable(entries, func(i, j int) bool { return less(entries[i], entries[j]) })

	best := -1
	for i := range entries {
		if entries[i].Verdict.Accepted {
			best = i
			break
		}
	}
	if best < 0 {
		return
	}
	top := entries[best].Verdict
	for i := range entries {
		v := &entries[i].Verdict
		if !v.Accepted {
			continue
		}
		reasons := append([]string(nil), v.Reasons...)
		if i == best {
			reasons[0] = "Best match: " + v.headline
		} else {
			head := "Good fit: " + v.headline
			if top.size > 0 && v.size > 0 && v.size*2 < top.size {
				// A saving is worth more than saying it is under the limit.
				head = "Good fit: " + v.base + ", less than half the size"
			}
			reasons[0] = head
			reasons = append(reasons, rankedLower(top, *v))
		}
		v.Reasons = reasons
	}
}

func less(a, b Entry) bool {
	av, bv := a.Verdict, b.Verdict
	if av.Accepted != bv.Accepted {
		return av.Accepted
	}
	if av.Accepted {
		if av.Score != bv.Score {
			return av.Score > bv.Score
		}
		if a.Priority != b.Priority {
			return a.Priority < b.Priority
		}
	} else if len(av.Reasons) != len(bv.Reasons) {
		return len(av.Reasons) < len(bv.Reasons)
	}
	if a.Release.Seeders != b.Release.Seeders {
		return a.Release.Seeders > b.Release.Seeders
	}
	if !a.Release.PublishedAt.Equal(b.Release.PublishedAt) {
		return a.Release.PublishedAt.After(b.Release.PublishedAt)
	}
	return a.Release.GUID < b.Release.GUID
}

// rankedLower says in plain words why a release is not the best match: the
// first thing in which the best one beats it.
func rankedLower(best, v Verdict) string {
	for i := range v.parts {
		if best.parts[i] <= v.parts[i] {
			continue
		}
		switch i {
		case partRes:
			return "Ranked lower: the picture size is further from what your profile prefers"
		case partSource:
			if best.source != "" && v.source != "" {
				return "Ranked lower: " + v.source + " ranks below " + best.source
			}
			return "Ranked lower: its source ranks below the best match"
		case partHDR:
			return "Ranked lower: it has no HDR, and your profile prefers it"
		case partWords:
			return "Ranked lower: it has fewer of the words you prefer"
		case partProper:
			return "Ranked lower: the best match is a newer fix of the same release"
		case partSize:
			return "Ranked lower: its size is further from the sweet spot for your profile"
		case partSeeders:
			return "Ranked lower: fewer people are sharing it"
		case partAge:
			return "Ranked lower: it is older"
		}
	}
	return "Ranked lower: level with the best match, which comes from a source you ranked higher"
}
