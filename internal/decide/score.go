package decide

import (
	"fmt"
	"slices"
	"strings"
	"time"
	"unicode"

	"github.com/BaileyPillon/marquee/internal/grab"
	"github.com/BaileyPillon/marquee/internal/parse"
	"github.com/BaileyPillon/marquee/internal/titles"
)

// Want says what the search is for. A movie needs nothing here.
//
// For a show, Season and Episodes name the episodes still missing in that
// season. One episode wanted accepts single-episode releases of it; two or more
// accept a whole-season pack as well. Season 0 with no Episodes means "no
// scope": every release of the show is judged on its own merits (used by the
// interactive search when no season was picked).
type Want struct {
	Season         int
	Episodes       []int // episode numbers of Season that are still missing
	SeasonEpisodes int   // how many episodes the season has, for a pack's size limit (0: use len(Episodes))
	Now            time.Time
}

// Release is what Score needs to know about one result of a search, apart from
// what is in its name.
type Release struct {
	IndexerID   int64
	GUID        string
	Title       string
	Protocol    string // grab.ProtocolTorrent | grab.ProtocolUsenet
	InfoHash    string
	Size        int64
	Seeders     int // -1: the feed did not say
	PublishedAt time.Time

	// Ids the source attached to the release.
	IMDb   string
	TMDb   int64
	TVMaze int64
	TVDb   int64

	// Blocked is the reason the owner blocklisted this release, or "".
	Blocked string
}

// Verdict is the decision about one release.
type Verdict struct {
	Accepted bool
	Score    int
	// Reasons are short plain-English lines. For an accepted release the first
	// is the headline ("Good fit: ..." or, after Rank, "Best match: ...").
	// A rejected release lists everything wrong with it.
	Reasons   []string
	Rejection string // the first reason it was rejected, "" when accepted

	headline string         // what follows "Good fit: "
	base     string         // the headline without its size clause
	source   string         // plain name of the source, for "ranks below" lines
	parts    [rankParts]int // the digits the score is made of, most important first
	size     int64
}

// The digits a score is built from, most important first.
const (
	partRes = iota
	partSource
	partHDR
	partWords
	partProper
	partSize
	partSeeders
	partAge
	rankParts
)

var partScale = [rankParts]int{1e8, 1e7, 1e6, 1e5, 1e4, 1e3, 1e2, 10}

// Score decides whether a release is acceptable for a title under a profile,
// and how good it is. It is pure: no I/O, no clock unless w.Now is zero.
func Score(p Profile, t titles.Title, w Want, parsed parse.Release, r Release) Verdict {
	reject := func(msgs ...string) Verdict {
		return Verdict{Reasons: msgs, Rejection: msgs[0]}
	}
	if r.Blocked != "" {
		return reject("You blocked this release before: " + r.Blocked)
	}
	if p.Kind == KindArtist || t.Kind == titles.KindArtist {
		return scoreMusic(p, t, w, r)
	}
	if parsed.IsSample {
		return reject("This is a sample file, not the full release")
	}
	if msg := junkSource(parsed.Source); msg != "" {
		return reject(msg)
	}
	if msg := titleMismatch(t, parsed, r); msg != "" {
		return reject(msg)
	}
	sc, msg := episodeScope(t, w, parsed)
	if msg != "" {
		return reject(msg)
	}

	var bad []string
	res := resolutionOf(parsed)
	switch {
	case res == 0:
		bad = append(bad, "Resolution not stated: the name does not say how sharp the picture is")
	case !slices.Contains(p.AllowedRes, res):
		bad = append(bad, fmt.Sprintf("Resolution not allowed: this is %dp, your profile takes %s", res, resList(p.AllowedRes)))
	}
	src := sourceName(parsed.Source)
	if len(p.AllowedSources) > 0 && !slices.Contains(p.AllowedSources, src) {
		if parsed.Source == "" {
			bad = append(bad, "Source not stated: the name does not say if it is a BluRay, WEB or TV capture")
		} else {
			bad = append(bad, fmt.Sprintf("Source not allowed: %s is not one your profile takes", sourcePlain(parsed.Source)))
		}
	}
	if word, ok := firstWord(p.RejectedWords, r.Title); ok {
		bad = append(bad, fmt.Sprintf("Has a word you rejected: %q", word))
	}

	// Size.
	eps := sc.sizeEpisodes
	limit := p.maxBytes(t.RuntimeMin, eps)
	if r.Size > 0 {
		if limit > 0 && r.Size > limit {
			bad = append(bad, tooBig(p, r.Size, limit, sc))
		}
		if lo := p.minBytes(); lo > 0 && r.Size < lo {
			bad = append(bad, fmt.Sprintf("Too small: %s is under your %s minimum", fmtSize(r.Size), fmtLimit(lo)))
		}
	}

	// Seeders (torrents only; a feed that does not say is given the benefit of the doubt).
	torrent := r.Protocol != grab.ProtocolUsenet
	if torrent && r.Seeders >= 0 && r.Seeders < p.MinSeeders {
		if r.Seeders == 0 {
			bad = append(bad, "No one is sharing it (0 seeders)")
		} else {
			bad = append(bad, fmt.Sprintf("Too few people sharing it (%d seeders, your profile wants at least %d)", r.Seeders, p.MinSeeders))
		}
	}
	if len(bad) > 0 {
		return reject(bad...)
	}

	// Accepted: work out how good it is.
	now := w.Now
	if now.IsZero() {
		now = time.Now()
	}
	v := Verdict{Accepted: true, size: r.Size, source: sourcePhrase(parsed.Source)}
	v.parts[partRes] = resDigit(p, res)
	v.parts[partSource] = sourceDigit(parsed.Source)
	if p.PreferHDR && len(parsed.HDR) > 0 {
		v.parts[partHDR] = 1
	}
	liked := matchingWords(p.PreferredWords, r.Title)
	v.parts[partWords] = min(len(liked), 9)
	v.parts[partProper] = properDigit(parsed)
	v.parts[partSize] = sizeFit(p, r.Size, limit) / 10
	v.parts[partSeeders] = seedersDigit(r, torrent)
	v.parts[partAge] = ageDigit(now, r.PublishedAt)
	v.Score = 1
	for i, d := range v.parts {
		v.Score += d * partScale[i]
	}

	base := fmt.Sprintf("%dp", res)
	if v.source != "" {
		base += " " + v.source
	}
	if sc.pack {
		base += " season pack"
	}
	if h := hdrLabel(parsed.HDR); h != "" {
		base += " with " + h
	}
	v.base, v.headline = base, base
	if limit > 0 && r.Size > 0 {
		if strings.Contains(base, " with ") {
			v.headline += ", under your size limit"
		} else {
			v.headline += " under your size limit"
		}
	}
	v.Reasons = []string{"Good fit: " + v.headline}
	if sc.pack {
		v.Reasons = append(v.Reasons, fmt.Sprintf("Covers all %d episodes you need in one download", len(w.Episodes)))
	}
	if sc.extra != "" {
		v.Reasons = append(v.Reasons, sc.extra)
	}
	if parsed.Proper || parsed.Repack {
		v.Reasons = append(v.Reasons, "Proper release: fixes problems with an earlier one")
	}
	if len(liked) > 0 {
		v.Reasons = append(v.Reasons, "Has "+quoteList(liked, "a word", "words")+" you prefer")
	}
	if torrent && r.Seeders >= 50 {
		v.Reasons = append(v.Reasons, fmt.Sprintf("Well shared (%d seeders)", r.Seeders))
	}
	return v
}

// ---- identity ----------------------------------------------------------------

// TitleMatches reports whether a release is about the title at all (name and
// year, or an id the source attached), and if not, why not in plain words.
// It does not look at quality or episodes.
func TitleMatches(t titles.Title, parsed parse.Release, r Release) (bool, string) {
	if t.Kind == titles.KindArtist {
		return artistMatches(t, r.Title)
	}
	msg := titleMismatch(t, parsed, r)
	return msg == "", msg
}

func titleMismatch(t titles.Title, parsed parse.Release, r Release) string {
	switch t.Kind {
	case titles.KindMovie:
		if parsed.IsTV() {
			return "This looks like a TV episode, not a movie"
		}
	case titles.KindShow:
		if !parsed.IsTV() {
			return "No season or episode in the name, so it cannot be matched to your show"
		}
	}

	// An id the source attached settles it: the same id is the same title, a
	// different one is a different title, whatever the name says.
	idMatch := false
	for _, c := range []struct {
		have, want string
		label      string
	}{
		{r.IMDb, t.IMDbID, "IMDb"},
	} {
		if c.have == "" || c.want == "" {
			continue
		}
		if strings.EqualFold(c.have, c.want) {
			idMatch = true
		} else {
			return fmt.Sprintf("Wrong title: the source says this is %s %s, not %s", c.label, c.have, c.want)
		}
	}
	for _, c := range []struct {
		have, want int64
		label      string
	}{
		{r.TMDb, t.TMDBID, "TMDB"}, {r.TVMaze, t.TVmazeID, "TVmaze"},
	} {
		if c.have <= 0 || c.want <= 0 {
			continue
		}
		if c.have == c.want {
			idMatch = true
		} else {
			return fmt.Sprintf("Wrong title: the source says this is %s %d, not %d", c.label, c.have, c.want)
		}
	}
	if idMatch {
		return ""
	}

	got := parse.Normalize(parsed.Title)
	if got == "" {
		return "Cannot read a title from this release name"
	}
	named := parse.Normalize(t.Name) == got
	for _, alt := range t.AltNames {
		if named {
			break
		}
		named = parse.Normalize(alt) == got
	}
	if !named {
		return fmt.Sprintf("Wrong title: this is %q, you want %q", parsed.Title, t.Name)
	}

	switch t.Kind {
	case titles.KindMovie:
		if t.Year > 0 {
			switch {
			case parsed.Year == 0:
				return fmt.Sprintf("No year in the name, so it cannot be told apart from other films called %q", t.Name)
			case abs(parsed.Year-t.Year) > 1:
				return fmt.Sprintf("Wrong year: this is from %d, you want the %d one", parsed.Year, t.Year)
			}
		}
	case titles.KindShow:
		if t.Year > 0 && parsed.Year > 0 && abs(parsed.Year-t.Year) > 1 {
			return fmt.Sprintf("Wrong year: this is the %d version, you want the one from %d", parsed.Year, t.Year)
		}
	}
	return ""
}

// ---- episodes ----------------------------------------------------------------

type scope struct {
	pack         bool
	sizeEpisodes int    // how many episodes the size limit scales with
	extra        string // a note for the reasons
}

func episodeScope(t titles.Title, w Want, parsed parse.Release) (scope, string) {
	sc := scope{sizeEpisodes: 1}
	if t.Kind != titles.KindShow {
		return sc, ""
	}
	anyScope := w.Season == 0 && len(w.Episodes) == 0

	// Packs.
	if parsed.FullSeason && len(parsed.Episodes) == 0 {
		switch {
		case len(parsed.Seasons) > 1:
			return sc, fmt.Sprintf("Multi-season pack (seasons %d to %d): Marquee picks one season at a time",
				parsed.Seasons[0], parsed.Seasons[len(parsed.Seasons)-1])
		case parsed.Season == 0:
			return sc, "Whole-series pack: Marquee picks one season at a time"
		}
		sc.pack = true
		sc.sizeEpisodes = max(w.SeasonEpisodes, len(w.Episodes))
		if sc.sizeEpisodes == 0 {
			sc.sizeEpisodes = 10 // the season's size is not known: allow a typical one
		}
		if anyScope {
			return sc, ""
		}
		if parsed.Season != w.Season {
			return sc, fmt.Sprintf("Wrong season: this pack is season %d, you need season %d", parsed.Season, w.Season)
		}
		if len(w.Episodes) < 2 {
			return sc, "Season pack, but you only need " + wantLabel(w)
		}
		return sc, ""
	}

	if len(parsed.Episodes) == 0 {
		return sc, "Cannot tell which episode this is: the name has no season and episode numbers"
	}
	sc.sizeEpisodes = len(parsed.Episodes)
	if anyScope {
		return sc, ""
	}
	if parsed.Season != w.Season {
		return sc, fmt.Sprintf("Wrong episode: this is %s, you need %s", epLabel(parsed.Season, parsed.Episodes), wantLabel(w))
	}
	var extra []int
	hit := false
	for _, e := range parsed.Episodes {
		if slices.Contains(w.Episodes, e) {
			hit = true
		} else {
			extra = append(extra, e)
		}
	}
	if !hit {
		return sc, fmt.Sprintf("Wrong episode: this is %s, you need %s", epLabel(parsed.Season, parsed.Episodes), wantLabel(w))
	}
	if len(extra) > 0 {
		sc.extra = "Also contains " + epLabel(parsed.Season, extra)
	}
	return sc, ""
}

func epLabel(season int, eps []int) string {
	parts := make([]string, len(eps))
	for i, e := range eps {
		parts[i] = fmt.Sprintf("S%02dE%02d", season, e)
	}
	return strings.Join(parts, " and ")
}

// wantLabel names what is wanted: "S01E04", "S01E03, S01E04 or S01E05", or the
// season when there are many.
func wantLabel(w Want) string {
	switch n := len(w.Episodes); {
	case n == 1:
		return epLabel(w.Season, w.Episodes)
	case n >= 2 && n <= 4:
		parts := make([]string, n)
		for i, e := range w.Episodes {
			parts[i] = fmt.Sprintf("S%02dE%02d", w.Season, e)
		}
		return strings.Join(parts[:n-1], ", ") + " or " + parts[n-1]
	}
	return fmt.Sprintf("episodes of season %d", w.Season)
}

// ---- quality facts -----------------------------------------------------------

// junkSource rejects camera recordings and screeners everywhere.
func junkSource(s parse.Source) string {
	switch s {
	case parse.SourceCAM:
		return "Rejected: this is a camera recording (CAM) from a cinema"
	case parse.SourceTelesync:
		return "Rejected: this is a telesync (TS), a cinema recording with separate sound"
	case parse.SourceTelecine:
		return "Rejected: this is a telecine (TC) copy"
	case parse.SourceScreener:
		return "Rejected: this is a screener, a pre-release review copy"
	}
	return ""
}

// resolutionOf is the resolution a release counts as. A name that gives none
// still tells by its source (a BluRay or WEB release is 1080p unless it says
// otherwise, an HDTV capture 720p), the way the quality name does.
func resolutionOf(r parse.Release) int {
	switch res := r.Resolution; {
	case res > 2160: // 4320 (8K) is the sharpest size a profile can ask for
		return 2160
	case res == 1440:
		return 1080
	case res > 0:
		return res
	}
	switch r.Source {
	case parse.SourceRemux, parse.SourceBluray, parse.SourceWEBDL, parse.SourceWEBRip:
		return 1080
	case parse.SourceHDTV:
		return 720
	case parse.SourceDVD, parse.SourceSDTV, parse.SourceRawHD:
		return 480
	}
	return 0
}

func resList(res []int) string {
	res = slices.Sorted(slices.Values(res))
	parts := make([]string, len(res))
	for i, r := range res {
		parts[i] = fmt.Sprintf("%dp", r)
	}
	switch len(parts) {
	case 0:
		return "nothing"
	case 1:
		return parts[0]
	}
	return strings.Join(parts[:len(parts)-1], ", ") + " or " + parts[len(parts)-1]
}

func sourceName(s parse.Source) string {
	if s == parse.SourceUnknown {
		return "Unknown"
	}
	return string(s)
}

// sourcePlain names a source for a rejection line.
func sourcePlain(s parse.Source) string {
	switch s {
	case parse.SourceRemux:
		return "a remux"
	case parse.SourceBluray:
		return "a BluRay encode"
	case parse.SourceWEBDL:
		return "WEB-DL"
	case parse.SourceWEBRip:
		return "WEBRip"
	case parse.SourceHDTV:
		return "an HDTV capture"
	case parse.SourceSDTV:
		return "an SD TV capture"
	case parse.SourceDVD:
		return "a DVD rip"
	case parse.SourceRawHD:
		return "a raw HD broadcast capture"
	}
	return string(s)
}

// sourcePhrase is how a source reads inside "2160p remux with HDR".
func sourcePhrase(s parse.Source) string {
	switch s {
	case parse.SourceRemux:
		return "remux"
	case parse.SourceBluray:
		return "BluRay encode"
	case parse.SourceWEBDL:
		return "WEB-DL"
	case parse.SourceWEBRip:
		return "WEBRip"
	case parse.SourceHDTV:
		return "HDTV"
	case parse.SourceDVD:
		return "DVD rip"
	case parse.SourceSDTV:
		return "SDTV"
	case parse.SourceRawHD:
		return "raw HD capture"
	}
	return ""
}

func hdrLabel(h []string) string {
	has := func(want string) bool { return slices.Contains(h, want) }
	switch {
	case has("HDR10+"):
		return "HDR10+"
	case has("DV"):
		return "Dolby Vision"
	case has("HDR10"), has("HDR"):
		return "HDR"
	case has("HLG"):
		return "HLG"
	}
	return ""
}

// ---- words -------------------------------------------------------------------

// spaced lower-cases a name and turns every run of non-letters and non-digits
// into one space, with a space at each end, so a word can be found whole.
func spaced(s string) string {
	var b strings.Builder
	b.WriteByte(' ')
	space := true
	for _, r := range strings.ToLower(s) {
		if unicode.IsLetter(r) || unicode.IsDigit(r) {
			b.WriteRune(r)
			space = false
		} else if !space {
			b.WriteByte(' ')
			space = true
		}
	}
	if !space {
		b.WriteByte(' ')
	}
	return b.String()
}

func hasWord(haystack, word string) bool {
	w := strings.TrimSpace(spaced(word))
	return w != "" && strings.Contains(haystack, " "+w+" ")
}

func firstWord(words []string, title string) (string, bool) {
	h := spaced(title)
	for _, w := range words {
		if hasWord(h, w) {
			return w, true
		}
	}
	return "", false
}

func matchingWords(words []string, title string) []string {
	h := spaced(title)
	var out []string
	for _, w := range words {
		if hasWord(h, w) {
			out = append(out, w)
		}
	}
	return out
}

func quoteList(words []string, one, many string) string {
	q := make([]string, len(words))
	for i, w := range words {
		q[i] = fmt.Sprintf("%q", w)
	}
	label := many
	if len(words) == 1 {
		label = one
	}
	return label + " " + strings.Join(q, ", ")
}

// ---- size --------------------------------------------------------------------

const gb = 1 << 30

func fmtSize(b int64) string {
	switch {
	case b >= gb:
		return fmt.Sprintf("%.1f GB", float64(b)/gb)
	case b >= mb:
		return fmt.Sprintf("%.0f MB", float64(b)/mb)
	}
	return fmt.Sprintf("%d KB", b/1024)
}

// fmtLimit writes a limit the way an owner typed it: "60 GB", "2.5 GB", "800 MB".
func fmtLimit(b int64) string {
	if b >= gb {
		g := float64(b) / gb
		if g == float64(int64(g)) {
			return fmt.Sprintf("%d GB", int64(g))
		}
		return fmt.Sprintf("%.1f GB", g)
	}
	return fmt.Sprintf("%d MB", b/mb)
}

func tooBig(p Profile, size, limit int64, sc scope) string {
	perHour := p.MaxSizePerHourMB != nil && *p.MaxSizePerHourMB > 0
	if perHour && (p.MaxSizeMB == nil || *p.MaxSizeMB <= 0 || limit < int64(*p.MaxSizeMB)*mb) {
		what := "this episode"
		switch {
		case sc.pack:
			what = fmt.Sprintf("a season of %d episodes", sc.sizeEpisodes)
		case sc.sizeEpisodes > 1:
			what = fmt.Sprintf("these %d episodes", sc.sizeEpisodes)
		}
		return fmt.Sprintf("Too big: %s is over your limit of %s for %s (%s an hour)",
			fmtSize(size), fmtLimit(limit), what, fmtLimit(int64(*p.MaxSizePerHourMB)*mb))
	}
	return fmt.Sprintf("Too big: %s is over your %s limit", fmtSize(size), fmtLimit(limit))
}

// ---- score digits ------------------------------------------------------------

func ladderIndex(res int) int {
	i := slices.Index(resolutionLadder, res)
	return max(i, 0)
}

// resDigit is 9 for the preferred resolution and less the further away it is.
// With no preference, sharper is better.
func resDigit(p Profile, res int) int {
	if p.PreferredRes > 0 {
		if res == p.PreferredRes {
			return 9
		}
		return max(8-2*abs(ladderIndex(res)-ladderIndex(p.PreferredRes)), 1)
	}
	return 1 + 2*ladderIndex(res)
}

func sourceDigit(s parse.Source) int {
	switch s {
	case parse.SourceRemux:
		return 9
	case parse.SourceBluray:
		return 7
	case parse.SourceWEBDL:
		return 6
	case parse.SourceWEBRip:
		return 5
	case parse.SourceHDTV:
		return 4
	case parse.SourceUnknown:
		return 3
	case parse.SourceDVD:
		return 2
	case parse.SourceSDTV, parse.SourceRawHD:
		return 1
	}
	return 0
}

func properDigit(r parse.Release) int {
	n := 0
	if r.Proper {
		n++
	}
	if r.Repack {
		n += max(r.RepackVersion, 1)
	}
	return min(n, 9)
}

// sizeFit is 0 to 99: how close a size is to the profile's sweet spot, which is
// 70 percent of the biggest size it allows (or twice its smallest, if only that
// is set). No limits, or an unknown size, is neutral.
func sizeFit(p Profile, size, maxBytes int64) int {
	var sweet float64
	switch lo := p.minBytes(); {
	case maxBytes > 0:
		sweet = 0.7 * float64(maxBytes)
	case lo > 0:
		sweet = 2 * float64(lo)
	default:
		return 50
	}
	if size <= 0 {
		return 50
	}
	d := (float64(size) - sweet) / sweet
	if d < 0 {
		d = -d
	}
	return max(99-int(99*d), 0)
}

func seedersDigit(r Release, torrent bool) int {
	switch {
	case !torrent:
		return 5
	case r.Seeders < 0:
		return 3
	}
	d := 0
	for n := r.Seeders + 1; n > 1 && d < 9; n >>= 1 {
		d++
	}
	return d
}

func ageDigit(now, published time.Time) int {
	if published.IsZero() {
		return 3
	}
	age := now.Sub(published)
	switch {
	case age < 24*time.Hour:
		return 9
	case age < 3*24*time.Hour:
		return 8
	case age < 7*24*time.Hour:
		return 7
	case age < 30*24*time.Hour:
		return 6
	case age < 90*24*time.Hour:
		return 5
	case age < 365*24*time.Hour:
		return 4
	}
	return 3
}

func abs(n int) int {
	if n < 0 {
		return -n
	}
	return n
}
