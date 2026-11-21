package decide

import (
	"testing"

	"github.com/BaileyPillon/marquee/internal/parse"
)

func rankOf(p Profile, name string) int { return p.RankRelease(parse.ParseRelease(name)) }

func TestRankReleaseOrdersQualityTheWayASearchDoes(t *testing.T) {
	hd := Profile{Kind: KindMovie, Quality: "hd", AllowedRes: []int{1080, 720}, PreferredRes: 1080}
	order := []string{ // best first
		"Film.2020.1080p.REMUX.x264",
		"Film.2020.1080p.BluRay.x264",
		"Film.2020.1080p.WEB-DL.x264",
		"Film.2020.1080p.WEBRip.x264",
		"Film.2020.1080p.HDTV.x264",
		"Film.2020.720p.BluRay.x264", // a smaller picture ranks below every 1080p
		"Film.2020.720p.WEB-DL.x264",
		"Film.2020.720p.HDTV.x264",
		"Film.2020.DVDRip.x264",
	}
	for i := 1; i < len(order); i++ {
		hi, lo := rankOf(hd, order[i-1]), rankOf(hd, order[i])
		if hi <= lo {
			t.Errorf("%s (%d) should rank above %s (%d)", order[i-1], hi, order[i], lo)
		}
	}
}

func TestRankReleaseFollowsThePreferredResolution(t *testing.T) {
	uhd := Profile{Kind: KindMovie, Quality: "uhd", AllowedRes: []int{2160}, PreferredRes: 2160}
	hd := Profile{Kind: KindMovie, Quality: "hd", AllowedRes: []int{1080, 2160}, PreferredRes: 1080}
	a, b := "Film.2020.2160p.WEB-DL.x265", "Film.2020.1080p.WEB-DL.x264"
	if rankOf(uhd, a) <= rankOf(uhd, b) {
		t.Error("a 4K profile ranks 2160p first")
	}
	if rankOf(hd, a) >= rankOf(hd, b) {
		t.Error("an HD profile ranks 1080p first")
	}
	none := Profile{}
	if rankOf(none, a) <= rankOf(none, b) {
		t.Error("with no preference, sharper is better")
	}
}

func TestRankReleaseHDRAndProper(t *testing.T) {
	plain := Profile{AllowedRes: []int{2160}, PreferredRes: 2160}
	likes := Profile{AllowedRes: []int{2160}, PreferredRes: 2160, PreferHDR: true}
	hdr, sdr := "Film.2020.2160p.WEB-DL.HDR.x265", "Film.2020.2160p.WEB-DL.x265"
	if rankOf(plain, hdr) != rankOf(plain, sdr) {
		t.Error("a profile that does not care about HDR ranks both the same")
	}
	if rankOf(likes, hdr) <= rankOf(likes, sdr) {
		t.Error("a profile that likes HDR ranks it higher")
	}
	if rankOf(plain, "Film.2020.1080p.BluRay.PROPER.x264") <= rankOf(plain, "Film.2020.1080p.BluRay.x264") {
		t.Error("a proper release ranks above the one it fixes")
	}
	if rankOf(plain, "Film.2020.1080p.BluRay.x264") != rankOf(plain, "Film.2020.1080p.BluRay.x264-OTHERGROUP") {
		t.Error("the group does not matter")
	}
}

func TestRankReleaseReadsQualityNames(t *testing.T) {
	hd := Profile{AllowedRes: []int{1080, 720}, PreferredRes: 1080}
	// The importer ranks "Bluray-1080p" style names (a file's quality, an upgrade limit) the same way.
	if rankOf(hd, "Bluray-1080p") <= rankOf(hd, "WEBDL-1080p") || rankOf(hd, "WEBDL-1080p") <= rankOf(hd, "WEBDL-720p") {
		t.Errorf("Bluray-1080p %d, WEBDL-1080p %d, WEBDL-720p %d", rankOf(hd, "Bluray-1080p"), rankOf(hd, "WEBDL-1080p"), rankOf(hd, "WEBDL-720p"))
	}
	if rankOf(hd, "Remux-2160p") >= rankOf(hd, "Bluray-1080p") {
		t.Error("an HD profile prefers a 1080p BluRay over a 2160p remux")
	}
}
