package history

import (
	"encoding/json"
	"strconv"
	"strings"
)

// maxReleaseRunes is how much of a release name a history line shows.
const maxReleaseRunes = 60

// Text writes one history entry as a plain-English line, for example
// "Priya asked for 4K", "Approved by Bailey", "Grabbed Metropolis.1927.2160p...
// from Archive feed" or "Imported Bluray-1080p · Jellyfin rescanned".
//
// It reads only the entry's event and its detail object and never fails: a
// detail it does not understand still gives a line (the event's own name).
func Text(event string, detail json.RawMessage) string {
	var d map[string]any
	if len(detail) > 0 {
		if err := json.Unmarshal(detail, &d); err != nil {
			d = nil
		}
	}
	if d == nil {
		d = map[string]any{}
	}
	switch event {
	case Requested:
		return requestedText(d)
	case Approved:
		// A name is more to the point than "automatically": the owner's own asks are
		// approved at once, by the owner.
		switch {
		case str(d, "by") != "":
			return "Approved by " + str(d, "by")
		case boolOf(d, "auto"):
			return "Approved automatically"
		}
		return "Approved"
	case Declined:
		line := "Declined"
		if by := str(d, "by"); by != "" {
			line += " by " + by
		}
		if reason := str(d, "reason"); reason != "" {
			line += ": " + reason
		}
		return line
	case Grabbed:
		return grabbedText(d)
	case Imported:
		return importedText(d)
	case Upgraded:
		return upgradedText(d)
	case Failed:
		line := "Import failed"
		if boolOf(d, "stalled") { // the download gave up before there was anything to import
			line = "Download stalled"
		}
		if reason := str(d, "reason"); reason != "" {
			line += ": " + reason
		}
		var extra []string
		if boolOf(d, "blocklisted") {
			extra = append(extra, "release blocklisted")
		}
		if boolOf(d, "searchingAgain") {
			extra = append(extra, "searching again")
		}
		return strings.Join(append([]string{line}, extra...), " · ")
	case Renamed:
		from, to := str(d, "from"), str(d, "to")
		switch {
		case from != "" && to != "":
			return "Renamed " + from + " to " + to
		case to != "":
			return "Renamed to " + to
		}
		return "Renamed a file"
	case Deleted:
		name := str(d, "file")
		switch {
		case name == "":
			return "Deleted a file"
		case boolOf(d, "fromDisk"):
			return "Deleted " + name + " from disk"
		}
		return "Removed " + name + " from the library"
	}
	return event
}

func requestedText(d map[string]any) string {
	what := qualityWord(str(d, "quality"))
	line := "Asked for " + what
	if user := str(d, "user"); user != "" {
		line = user + " asked for " + what
	}
	if sc, ok := d["scope"].(map[string]any); ok {
		if seasons := intList(sc["seasons"]); len(seasons) > 0 {
			word := "season"
			if len(seasons) > 1 {
				word = "seasons"
			}
			parts := make([]string, len(seasons))
			for i, n := range seasons {
				parts[i] = strconv.Itoa(n)
			}
			line += " (" + word + " " + strings.Join(parts, ", ") + ")"
		} else if albums := intList(sc["albums"]); len(albums) > 0 {
			line += " (" + plural(len(albums), "album", "albums") + ")"
		}
	}
	switch str(d, "source") {
	case "watchlist":
		line += " from a Plex watchlist"
	case "mcp":
		line += " through Claude"
	}
	return line
}

func grabbedText(d map[string]any) string {
	release := shorten(str(d, "release"), maxReleaseRunes)
	line := "Grabbed a release"
	if release != "" {
		line = "Grabbed " + release
	}
	if from := str(d, "indexer"); from != "" {
		line += " from " + from
	}
	return line
}

func importedText(d map[string]any) string {
	files, _ := d["files"].([]any)
	var quality string
	for _, f := range files {
		if m, ok := f.(map[string]any); ok {
			if q := str(m, "quality"); q != "" {
				quality = q
				break
			}
		}
	}
	var parts []string
	switch {
	case len(files) == 0:
		parts = append(parts, "Already in the library")
	case len(files) == 1 && quality != "":
		parts = append(parts, "Imported "+quality)
	case len(files) == 1:
		parts = append(parts, "Imported 1 file")
	default:
		parts = append(parts, "Imported "+strconv.Itoa(len(files))+" files")
		if quality != "" {
			parts = append(parts, quality)
		}
	}
	if line := rescanned(d); line != "" {
		parts = append(parts, line)
	}
	return strings.Join(parts, " · ")
}

func upgradedText(d map[string]any) string {
	from, to := str(d, "from"), str(d, "to")
	var line string
	switch {
	case from != "" && to != "":
		line = "Upgraded from " + from + " to " + to
	case to != "":
		line = "Upgraded to " + to
	default:
		line = "Upgraded"
	}
	if r := rescanned(d); r != "" {
		line += " · " + r
	}
	return line
}

// rescanned says which media servers were told: "Jellyfin rescanned".
func rescanned(d map[string]any) string {
	names := strList(d["rescanned"])
	if len(names) == 0 {
		return ""
	}
	return joinWords(names) + " rescanned"
}

// joinWords is "A", "A and B", "A, B and C".
func joinWords(list []string) string {
	switch len(list) {
	case 0:
		return ""
	case 1:
		return list[0]
	}
	return strings.Join(list[:len(list)-1], ", ") + " and " + list[len(list)-1]
}

func qualityWord(q string) string {
	switch strings.ToLower(q) {
	case "uhd":
		return "4K"
	case "hd":
		return "HD"
	case "":
		return "a copy"
	}
	return q
}

func plural(n int, one, many string) string {
	if n == 1 {
		return "1 " + one
	}
	return strconv.Itoa(n) + " " + many
}

func shorten(s string, n int) string {
	r := []rune(strings.TrimSpace(s))
	if len(r) <= n {
		return string(r)
	}
	return string(r[:n]) + "…"
}

func str(d map[string]any, key string) string {
	s, _ := d[key].(string)
	return strings.TrimSpace(s)
}

func boolOf(d map[string]any, key string) bool {
	b, _ := d[key].(bool)
	return b
}

func intList(v any) []int {
	list, _ := v.([]any)
	var out []int
	for _, x := range list {
		if f, ok := x.(float64); ok {
			out = append(out, int(f))
		}
	}
	return out
}

func strList(v any) []string {
	list, _ := v.([]any)
	var out []string
	for _, x := range list {
		if s, ok := x.(string); ok && strings.TrimSpace(s) != "" {
			out = append(out, strings.TrimSpace(s))
		}
	}
	return out
}
