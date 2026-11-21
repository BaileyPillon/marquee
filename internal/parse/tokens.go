package parse

import (
	"regexp"
	"strings"
	"unicode"
	"unicode/utf8"
)

// span is a half-open byte range of the scanned name.
type span struct{ start, end int }

func (a span) overlaps(b span) bool { return a.start < b.end && b.start < a.end }

// tok is one whole-word match: its range and the token regexp's own capture
// groups (empty string when a group did not take part).
type tok struct {
	span
	sub []string
}

// word compiles token so it only matches as a whole word: the characters on
// either side must not be letters or digits. The boundary characters are part
// of the regexp (not lookarounds, which RE2 lacks), so alternatives still
// backtrack when a boundary fails. Group 1 is the token itself.
func word(token string) *regexp.Regexp {
	return regexp.MustCompile(`(?i)(?:^|[^A-Za-z0-9])(` + token + `)(?:[^A-Za-z0-9]|$)`)
}

// findAll returns every whole-word match of re in s, left to right. Each search
// restarts at the end of the previous token, so the delimiter after one token
// can serve as the delimiter before the next ("DD5.1.AAC2.0").
func findAll(re *regexp.Regexp, s string) []tok {
	var out []tok
	for pos := 0; pos < len(s); {
		loc := re.FindStringSubmatchIndex(s[pos:])
		if loc == nil {
			break
		}
		t := tok{span: span{pos + loc[2], pos + loc[3]}}
		for i := 4; i+1 < len(loc); i += 2 {
			if loc[i] < 0 {
				t.sub = append(t.sub, "")
			} else {
				t.sub = append(t.sub, s[pos+loc[i]:pos+loc[i+1]])
			}
		}
		out = append(out, t)
		pos = t.end
	}
	return out
}

func isAlnumRune(r rune) bool { return unicode.IsLetter(r) || unicode.IsDigit(r) }

// onlyDelims reports whether s holds nothing but separators and brackets.
func onlyDelims(s string) bool {
	return strings.Trim(s, " .-_()[]{},+&") == ""
}

// collapseAcronyms turns "S.H.I.E.L.D." into "SHIELD" and "L.A.Confidential"
// into "LA.Confidential": runs of two or more single letters each followed by
// a dot lose those dots.
func collapseAcronyms(s string) string {
	rs := []rune(s)
	var b strings.Builder
	for i := 0; i < len(rs); {
		single := unicode.IsLetter(rs[i]) && i+1 < len(rs) && rs[i+1] == '.' && (i == 0 || !isAlnumRune(rs[i-1]))
		if !single {
			b.WriteRune(rs[i])
			i++
			continue
		}
		j, n := i, 0
		for j+1 < len(rs) && unicode.IsLetter(rs[j]) && rs[j+1] == '.' && (j == i || rs[j-1] == '.') {
			n++
			j += 2
		}
		if n < 2 {
			b.WriteRune(rs[i])
			i++
			continue
		}
		for k := i; k < j; k += 2 {
			b.WriteRune(rs[k])
		}
		// j is just past the last dot. Keep a dot as a separator only when a
		// word follows it directly.
		if j < len(rs) && isAlnumRune(rs[j]) {
			b.WriteRune('.')
		}
		i = j
	}
	return b.String()
}

// foldSpace collapses runs of whitespace to one space and trims the ends.
func foldSpace(s string) string {
	return strings.Join(strings.Fields(s), " ")
}

// lastRune returns the rune before position i of s, or 0 at the start.
func runeBefore(s string, i int) rune {
	if i <= 0 {
		return 0
	}
	r, _ := utf8.DecodeLastRuneInString(s[:i])
	return r
}

func runeAt(s string, i int) rune {
	if i >= len(s) {
		return 0
	}
	r, _ := utf8.DecodeRuneInString(s[i:])
	return r
}
