package decide

import "github.com/BaileyPillon/marquee/internal/parse"

// RankRelease says how good the quality in an already-parsed name is under this
// profile, as one number: the bigger, the better. It looks at the same things a
// search does, in the same order (picture size closest to the preferred one,
// then source, then HDR if the profile likes it, then a proper or repack), and
// leaves out what a file on disk no longer has (size fit, seeders, age).
//
// The importer uses it to decide whether a finished download is an upgrade of
// the file the library already has: it replaces the old one only when its rank
// is higher. Music has no picture, so only the profile's format list counts
// there; callers with music should compare formats themselves.
func (p Profile) RankRelease(r parse.Release) int {
	res := resolutionOf(r)
	rank := resDigit(p, res)*10000 + sourceDigit(r.Source)*1000
	if p.PreferHDR && len(r.HDR) > 0 {
		rank += 100
	}
	rank += properDigit(r) * 10
	return rank
}
