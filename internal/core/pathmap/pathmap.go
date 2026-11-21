// Package pathmap translates a file path from one program's point of view to
// another's. A download client in a container sees "/downloads/Movie", Marquee
// on the host sees "D:\dl\Movie"; a media server in a container sees "/media"
// where Marquee sees "M:\MarqueeTest\media". One rule says "a path that starts
// with From starts with To instead".
//
// It is pure (no disk access) and handles both Windows and Unix spellings: the
// comparison ignores the difference between "\" and "/" and, for a Windows
// style prefix (a drive letter or a UNC share), upper and lower case. The
// rest of the path is rewritten with the separator the To side uses.
package pathmap

import (
	"encoding/json"
	"errors"
	"strings"
)

// Rule maps one folder to another.
type Rule struct {
	From string `json:"from"`
	To   string `json:"to"`
}

// Limits of what a person may save.
const (
	MaxRules   = 50
	MaxPathLen = 500
)

// Parse reads a rule list stored as JSON. Empty text is an empty list.
func Parse(text string) ([]Rule, error) {
	text = strings.TrimSpace(text)
	if text == "" || text == "null" {
		return []Rule{}, nil
	}
	var rules []Rule
	if err := json.Unmarshal([]byte(text), &rules); err != nil {
		return nil, err
	}
	if rules == nil {
		rules = []Rule{}
	}
	return rules, nil
}

// Encode writes a rule list as JSON for storage.
func Encode(rules []Rule) string {
	if rules == nil {
		rules = []Rule{}
	}
	b, err := json.Marshal(rules)
	if err != nil {
		return "[]"
	}
	return string(b)
}

// Clean checks and tidies a rule list a person typed: both sides are needed,
// control characters are refused, and a rule that maps a folder to itself is
// dropped.
func Clean(in []Rule) ([]Rule, error) {
	if len(in) > MaxRules {
		return nil, errors.New("too many path rules")
	}
	out := make([]Rule, 0, len(in))
	for _, r := range in {
		r.From, r.To = strings.TrimSpace(r.From), strings.TrimSpace(r.To)
		if r.From == "" || r.To == "" {
			return nil, errors.New("each path rule needs both a from and a to folder")
		}
		for _, p := range []string{r.From, r.To} {
			if len([]rune(p)) > MaxPathLen {
				return nil, errors.New("a path in a rule is too long")
			}
			for _, c := range p {
				if c < 0x20 || c == 0x7f {
					return nil, errors.New("a path in a rule has a control character in it")
				}
			}
		}
		out = append(out, r)
	}
	return out, nil
}

// sep is the separator a path uses: a backslash when it has one and no slash
// (a Windows path), a slash otherwise.
func sep(p string) string {
	if strings.Contains(p, `\`) && !strings.Contains(p, "/") {
		return `\`
	}
	return "/"
}

// isWindows reports whether a path looks like a Windows one: a drive letter, a
// UNC share, or only backslashes. Their prefixes compare without case.
func isWindows(p string) bool {
	if len(p) >= 2 && p[1] == ':' && ((p[0] >= 'a' && p[0] <= 'z') || (p[0] >= 'A' && p[0] <= 'Z')) {
		return true
	}
	return strings.HasPrefix(p, `\\`) || sep(p) == `\`
}

// split breaks a path into its parts, ignoring which separator was used and
// any empty parts. A leading separator (the root of a Unix path) is reported
// separately.
func split(p string) (rooted bool, parts []string) {
	p = strings.ReplaceAll(p, `\`, "/")
	rooted = strings.HasPrefix(p, "/")
	for _, part := range strings.Split(p, "/") {
		if part != "" {
			parts = append(parts, part)
		}
	}
	return rooted, parts
}

// Apply rewrites p with the first rule whose From is p or a folder above it.
// The longest From wins when several fit. ok is false when no rule fits (p is
// returned as it was).
func Apply(rules []Rule, p string) (mapped string, ok bool) {
	best := -1
	var bestLen int
	pRooted, pParts := split(p)
	for i, r := range rules {
		fRooted, fParts := split(r.From)
		if len(fParts) == 0 && !fRooted {
			continue
		}
		if fRooted != pRooted && !isWindows(r.From) {
			continue
		}
		if len(fParts) > len(pParts) {
			continue
		}
		fold := isWindows(r.From) || isWindows(p)
		match := true
		for j := range fParts {
			if fold {
				match = strings.EqualFold(fParts[j], pParts[j])
			} else {
				match = fParts[j] == pParts[j]
			}
			if !match {
				break
			}
		}
		if match && (best < 0 || len(fParts) > bestLen) {
			best, bestLen = i, len(fParts)
		}
	}
	if best < 0 {
		return p, false
	}
	r := rules[best]
	rest := pParts[bestLen:]
	toSep := sep(r.To)
	base := strings.TrimRight(r.To, `\/`) // "" when the To side is a root: "/" or "\"
	if len(rest) == 0 {
		if base == "" {
			return toSep, true
		}
		return base + trailingRoot(r.To, base), true
	}
	return base + toSep + strings.Join(rest, toSep), true
}

// trailingRoot keeps the separator of a bare drive ("C:\") that trimming took
// off; any other folder has none.
func trailingRoot(to, trimmed string) string {
	if len(trimmed) == 2 && trimmed[1] == ':' && len(to) > 2 {
		return to[2:3]
	}
	return ""
}

// Within reports whether p is the folder parent or somewhere below it, ignoring
// which separator each uses (and case, for Windows style paths).
func Within(parent, p string) bool {
	_, ok := Apply([]Rule{{From: parent, To: "/"}}, p)
	return ok
}

// Dir returns the folder a file path is in, keeping the path's own separator
// style.
func Dir(p string) string {
	s := sep(p)
	trimmed := strings.TrimRight(p, `\/`)
	i := strings.LastIndexAny(trimmed, `\/`)
	switch {
	case i < 0:
		return "."
	case i == 0:
		return s
	}
	d := trimmed[:i]
	if len(d) == 2 && d[1] == ':' {
		d += string(trimmed[i]) // a drive keeps its separator: "C:\"
	}
	return d
}

// Map rewrites p when a rule fits and returns it unchanged otherwise.
func Map(rules []Rule, p string) string {
	m, _ := Apply(rules, p)
	return m
}

// Reverse returns the rules turned around (To becomes From), for going back.
func Reverse(rules []Rule) []Rule {
	out := make([]Rule, len(rules))
	for i, r := range rules {
		out[i] = Rule{From: r.To, To: r.From}
	}
	return out
}
