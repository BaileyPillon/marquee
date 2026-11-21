package parse

import (
	"strings"
	"unicode"
)

// foldTable maps lower-case accented Latin letters to plain ASCII. The standard
// library has no Unicode decomposition and the project takes no new
// dependencies, so the common letters are listed by hand.
var foldTable = func() map[rune]string {
	m := map[rune]string{}
	add := func(base, chars string) {
		for _, c := range chars {
			m[c] = base
		}
	}
	add("a", "àáâãäåāăąǎǟǡǻȁȃȧạảấầẩẫậắằẳẵặ")
	add("c", "çćĉċč")
	add("d", "ďđð")
	add("e", "èéêëēĕėęěȅȇȩẹẻẽếềểễệ")
	add("g", "ĝğġģ")
	add("h", "ĥħ")
	add("i", "ìíîïĩīĭįıǐȉȋỉị")
	add("j", "ĵ")
	add("k", "ķ")
	add("l", "ĺļľŀł")
	add("n", "ñńņňŉ")
	add("o", "òóôõöøōŏőǒǫȍȏȯọỏốồổỗộớờởỡợơ")
	add("r", "ŕŗř")
	add("s", "śŝşšș")
	add("t", "ţťŧț")
	add("u", "ùúûüũūŭůűųǔȕȗụủứừửữựư")
	add("w", "ŵ")
	add("y", "ýÿŷỳỵỷỹ")
	add("z", "źżž")
	m['ß'] = "ss"
	m['æ'] = "ae"
	m['œ'] = "oe"
	m['þ'] = "th"
	m['ĳ'] = "ij"
	return m
}()

// Normalize reduces a title to a form two spellings of the same name share:
// lower case, accents folded (Fröhlich becomes frohlich, Préludes becomes
// preludes), & read as "and", apostrophes dropped, other punctuation turned
// into spaces, a leading "the", "a" or "an" removed, and spaces collapsed.
func Normalize(title string) string {
	t := strings.ToLower(collapseAcronyms(title))
	var b strings.Builder
	b.Grow(len(t))
	for _, r := range t {
		switch {
		case r == '&':
			b.WriteString(" and ")
		case r == '\'' || r == '’' || r == '‘' || r == '`' || r == '´' || r == 'ʼ':
			// "don't" and "dont" are the same title
		case unicode.Is(unicode.Mn, r):
			// a combining accent from an decomposed spelling
		default:
			if f, ok := foldTable[r]; ok {
				b.WriteString(f)
			} else if unicode.IsLetter(r) || unicode.IsDigit(r) {
				b.WriteRune(r)
			} else {
				b.WriteByte(' ')
			}
		}
	}
	words := strings.Fields(b.String())
	if len(words) > 1 {
		switch words[0] {
		case "the", "a", "an":
			words = words[1:]
		}
	}
	return strings.Join(words, " ")
}
