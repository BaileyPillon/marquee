package parse

import "strconv"

// QualityName is the short quality label of a release, "Source-Resolution":
// Bluray-1080p, WEBDL-2160p, Remux-2160p, HDTV-720p. Sources that have no
// resolution of their own (DVD, CAM, Raw-HD, SDTV) are named alone. A release
// whose source is not stated is "Unknown-1080p" (or "Unknown" with no
// resolution either).
func QualityName(r Release) string {
	switch r.Source {
	case SourceRemux:
		return "Remux-" + resLabel(r.Resolution, 1080)
	case SourceBluray:
		return "Bluray-" + resLabel(r.Resolution, 1080)
	case SourceWEBDL:
		return "WEBDL-" + resLabel(r.Resolution, 1080)
	case SourceWEBRip:
		return "WEBRip-" + resLabel(r.Resolution, 1080)
	case SourceHDTV:
		if r.Resolution >= 720 {
			return "HDTV-" + resLabel(r.Resolution, 720)
		}
		return "SDTV"
	case SourceSDTV:
		return "SDTV"
	case SourceDVD:
		return "DVD"
	case SourceRawHD:
		return "Raw-HD"
	case SourceCAM:
		return "CAM"
	case SourceTelesync:
		return "TELESYNC"
	case SourceTelecine:
		return "TELECINE"
	case SourceScreener:
		return "DVDSCR"
	}
	if r.Resolution > 0 {
		return "Unknown-" + resLabel(r.Resolution, 0)
	}
	return "Unknown"
}

func resLabel(res, def int) string {
	if res == 0 {
		res = def
	}
	return strconv.Itoa(res) + "p"
}
