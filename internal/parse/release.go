// Package parse reads the facts out of media names: release titles, file and
// folder names, and music paths. It is pure (no I/O) and sits at the bottom of
// the module stack, so the library scanner uses it today and release picking
// will use it later.
package parse

import (
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"
)

// Source is where a release was ripped or captured from.
type Source string

// The sources ParseRelease recognises. The empty Source means "not stated".
const (
	SourceUnknown  Source = ""
	SourceBluray   Source = "BluRay"
	SourceRemux    Source = "Remux"
	SourceWEBDL    Source = "WEB-DL"
	SourceWEBRip   Source = "WEBRip"
	SourceHDTV     Source = "HDTV"
	SourceSDTV     Source = "SDTV"
	SourceDVD      Source = "DVD"
	SourceRawHD    Source = "Raw-HD"
	SourceCAM      Source = "CAM"
	SourceTelesync Source = "Telesync"
	SourceTelecine Source = "Telecine"
	SourceScreener Source = "Screener"
)

// Video codec names used in Release.Codec.
const (
	CodecH264  = "H.264" // x264, H.264, AVC
	CodecH265  = "H.265" // x265, H.265, HEVC
	CodecAV1   = "AV1"
	CodecXviD  = "XviD"
	CodecDivX  = "DivX"
	CodecVC1   = "VC-1"
	CodecMPEG2 = "MPEG-2"
	CodecVP9   = "VP9"
)

// Release is everything ParseRelease could read from one name. A field that is
// not in the name stays at its zero value.
type Release struct {
	Title string `json:"title,omitempty"` // cleaned: no dots, brackets or tags
	Year  int    `json:"year,omitempty"`

	// TV. Season is only meaningful together with Episodes or FullSeason; a
	// season of 0 with episodes is the "specials" season (S00E01).
	Season          int    `json:"season,omitempty"`
	Seasons         []int  `json:"seasons,omitempty"`         // a multi-season pack (S01-S03)
	Episodes        []int  `json:"episodes,omitempty"`        // S01E01E02 and S01E01-E03 give several
	AbsoluteEpisode int    `json:"absoluteEpisode,omitempty"` // anime "Title - 07"
	AirDate         string `json:"airDate,omitempty"`         // YYYY-MM-DD, daily shows
	FullSeason      bool   `json:"fullSeason,omitempty"`      // a whole season or series, no single episode
	Complete        bool   `json:"complete,omitempty"`        // the word "Complete" appeared
	Version         int    `json:"version,omitempty"`         // anime "07v2" gives 2

	Resolution int      `json:"resolution,omitempty"` // 480 576 720 1080 2160 (4320)
	Source     Source   `json:"source,omitempty"`
	Codec      string   `json:"codec,omitempty"`
	HDR        []string `json:"hdr,omitempty"`      // HDR10+ HDR10 HDR DV HLG
	Audio      []string `json:"audio,omitempty"`    // AAC AC3 EAC3 DTS DTS-HD DTS-HD MA DTS-X TrueHD Atmos FLAC Opus MP3 PCM
	Channels   string   `json:"channels,omitempty"` // "2.0", "5.1", "7.1"
	Edition    string   `json:"edition,omitempty"`  // "Director's Cut", "Extended", ...
	Service    string   `json:"service,omitempty"`  // streaming tag: AMZN, NF, DSNP, ...

	Proper        bool `json:"proper,omitempty"`
	Repack        bool `json:"repack,omitempty"`
	RepackVersion int  `json:"repackVersion,omitempty"` // REPACK = 1, REPACK2 = 2
	Real          bool `json:"real,omitempty"`

	Languages []string `json:"languages,omitempty"`
	Group     string   `json:"group,omitempty"`
	IsSample  bool     `json:"isSample,omitempty"`
	Container string   `json:"container,omitempty"` // file extension, lower case, no dot
}

// IsTV reports whether the name carries any episode information.
func (r Release) IsTV() bool {
	return len(r.Episodes) > 0 || r.AirDate != "" || r.AbsoluteEpisode > 0 || r.FullSeason
}

// ---- recognised words ------------------------------------------------------

var (
	reWWW = regexp.MustCompile(`(?i)^\[?\s*www\.[a-z0-9.\-]+\s*\]?\s*[-–]?\s*`)

	reTagWords = word(`1080p|720p|480p|576p|2160p|4k|uhd|bd|bdrip|blu[ .\-]?ray|web[ .\-]?dl|web[ .\-]?rip|web|hdtv|dvd|dvdrip|remux|x264|x265|h[ .\-]?26[45]|hevc|avc|av1|flac|aac|10[ .\-]?bit|hi10p?`)
	reTagFull  = regexp.MustCompile(`(?i)^(?:(?:19|20)\d\d|\d{3,4}[pi]|[0-9a-f]{8})$`)

	reSE         = word(`s(\d{1,4})[ .\-]?e(\d{1,4})((?:[ .]?[-+&]?[ .]?e\d{1,4}|-\d{1,3})*)`)
	reSeasonEp   = word(`season[ .\-]?(\d{1,4})[ .\-,]*(?:episode|ep)[ .\-]?(\d{1,4})((?:-\d{1,3})*)`)
	reNxNN       = word(`(\d{1,2})x(\d{2,3})((?:[-x]\d{2,3})*)`)
	reSeasonPack = word(`s(\d{1,4})(?:-s?(\d{1,4}))?`)
	reSeasonWord = word(`seasons?[ .\-]?(\d{1,2})(?:[ .\-]*(?:-|to|&|and)[ .\-]*(?:seasons?[ .\-]?)?(\d{1,2}))?`)
	reAirYMD     = word(`((?:19|20)\d\d)[ .\-/](\d{2})[ .\-/](\d{2})`)
	reAirDMY     = word(`(\d{2})[ .\-/](\d{2})[ .\-/]((?:19|20)\d\d)`)
	reAbsDash    = word(`-[ ]+(\d{1,4})(?:v(\d))?`)
	reAbsEp      = word(`(?:episode|ep)[ .\-]?(\d{1,4})(?:v(\d))?`)
	reAbsE       = word(`e(\d{2,4})(?:v(\d))?`)
	reEpCont     = regexp.MustCompile(`(?i)(?:[ .]*([-+&x])[ .]*|[ .]*)e?(\d{1,4})`)

	reYearRange = word(`((?:19|20)\d\d)[ ]?[-–][ ]?((?:19|20)\d\d)`)
	reYear      = word(`(?:19|20)\d\d`)
	reProvider  = word(`[\[{](?:tmdb|imdb|tvdb)(?:id)?[ \-=]?[a-z0-9]+[\]}]`)

	reResP    = word(`(4320|2160|1440|1080|720|576|480)([pi])`)
	reResDims = word(`(\d{3,4})x(\d{3,4})`)
	reRes4K   = word(`4k|uhd|8k`)

	reCodecs = []struct {
		re    *regexp.Regexp
		codec string
	}{
		{word(`x[ .\-]?265|h[ .\-]?265|hevc`), CodecH265},
		{word(`x[ .\-]?264|h[ .\-]?264|avc`), CodecH264},
		{word(`av1`), CodecAV1},
		{word(`xvid`), CodecXviD},
		{word(`divx`), CodecDivX},
		{word(`vc[ .\-]?1`), CodecVC1},
		{word(`mpeg[ .\-]?2|h[ .\-]?262`), CodecMPEG2},
		{word(`vp9`), CodecVP9},
	}

	reService = word(`amzn|nflx|nf|dsnp|hmax|atvp|hulu|pcok|pmtp|crav`)

	reChannels = word(`(?:[12]\.[01]|5\.[01]|6\.1|7\.[01])`)
	reComplete = word(`complete(?:[ .\-]?(?:series|seasons?|collection|pack))?`)
	reProper   = word(`proper`)
	reRepack   = word(`(?:repack|rerip)(\d)?`)
	reReal     = word(`real`)

	reSampleTail = regexp.MustCompile(`(?i)[-. ]sample$`)
	reGroup      = regexp.MustCompile(`^[A-Za-z0-9]{2,20}$`)
)

type srcPattern struct {
	re  *regexp.Regexp
	src Source
}

// Strong sources end the title; the remux pattern is handled on its own.
var (
	reRemux      = word(`((?:bd|uhd|blu[ .\-]?ray|dvd)[ .\-]?)?remux`)
	strongSource = []srcPattern{
		{word(`dvd[ .\-]?scr(?:eener)?|screener|bd[ .\-]?scr`), SourceScreener},
		{word(`blu[ .\-]?ray|bd[ .\-]?rip|brrip|bdmv|bd(?:25|50|66|100)|uhd[ .\-]?bd`), SourceBluray},
		{word(`web[ .\-]?rip|hd[ .\-]?rip`), SourceWEBRip},
		{word(`web[ .\-]?dl(?:rip)?`), SourceWEBDL},
		{word(`hd[ .\-]?tv(?:rip)?`), SourceHDTV},
		{word(`pdtv|dsr(?:ip)?|tvrip|sdtv|dvb|satrip`), SourceSDTV},
		{word(`dvd[ .\-]?rip|dvd[ .\-]?r|dvd5|dvd9|dvdmux|dvd`), SourceDVD},
		{word(`telesync|hd[ .\-]?ts`), SourceTelesync},
		{word(`telecine|hd[ .\-]?tc`), SourceTelecine},
		{word(`raw[ .\-]?hd`), SourceRawHD},
	}
	// Weak sources only count after the title has ended ("Charlotte's Web").
	weakSource = []srcPattern{
		{word(`web`), SourceWEBDL},
		{word(`bd`), SourceBluray},
		{word(`cam(?:rip)?|hd[ .\-]?cam`), SourceCAM},
	}
)

type labelPattern struct {
	re     *regexp.Regexp
	label  string
	strong bool
}

// audio patterns, most specific first. Each takes an optional channel count
// glued on ("DD5.1", "AAC.2.0", "DTS-HD.MA.7.1").
const chanSuffix = `(?:[ .\-]?([1-9][ .][0-2]))?`

var audioPatterns = []labelPattern{
	{word(`dts[ .\-]?hd[ .\-]?(?:ma|master[ .\-]?audio)` + chanSuffix), "DTS-HD MA", true},
	{word(`dts[ .\-]?hd[ .\-]?(?:hra|hr)` + chanSuffix), "DTS-HD", true},
	{word(`dts[ .\-]?hd` + chanSuffix), "DTS-HD", true},
	{word(`dts[ .\-:]?x` + chanSuffix), "DTS-X", true},
	{word(`dts(?:[ .\-]?es)?` + chanSuffix), "DTS", true},
	{word(`true[ .\-]?hd` + chanSuffix), "TrueHD", true},
	{word(`(?:e[ .\-]?ac[ .\-]?3|ddp|dd[ .\-]?\+)` + chanSuffix), "EAC3", true},
	{word(`(?:ac[ .\-]?3|dolby[ .\-]?digital)` + chanSuffix), "AC3", true},
	{word(`aac(?:[ .\-]?lc)?` + chanSuffix), "AAC", true},
	{word(`flac` + chanSuffix), "FLAC", true},
	{word(`atmos` + chanSuffix), "Atmos", false},
	{word(`dd` + chanSuffix), "AC3", false},
	{word(`opus` + chanSuffix), "Opus", false},
	{word(`mp3` + chanSuffix), "MP3", false},
	{word(`l?pcm` + chanSuffix), "PCM", false},
}

var hdrPatterns = []labelPattern{
	{word(`hdr10\+|hdr10plus`), "HDR10+", true},
	{word(`hdr[ .\-]?10`), "HDR10", true},
	{word(`hdr`), "HDR", true},
	{word(`dolby[ .\-]?vision|dovi`), "DV", true},
	{word(`hlg`), "HLG", true},
	{word(`dv`), "DV", false},
}

// editions: pull says how willing the word is to be cut off the end of the
// title when it sits directly before the first marker (2 = always, 0 = never,
// because the words also turn up in real titles).
var editionPatterns = []struct {
	re    *regexp.Regexp
	label string
	pull  int
}{
	{word(`director['’´]?s?[ .\-]?cut`), "Director's Cut", 2},
	{word(`extended(?:[ .\-]?(?:cut|edition|version))?`), "Extended", 2},
	{word(`unrated(?:[ .\-]?(?:cut|edition|version))?`), "Unrated", 2},
	{word(`uncut`), "Uncut", 2},
	{word(`theatrical(?:[ .\-]?(?:cut|edition|version))?`), "Theatrical", 2},
	{word(`remaster(?:ed)?(?:[ .\-]?(?:edition|version))?`), "Remastered", 2},
	{word(`imax(?:[ .\-]?enhanced)?`), "IMAX", 2},
	{word(`criterion(?:[ .\-]?collection)?`), "Criterion", 2},
	{word(`special[ .\-]?edition`), "Special Edition", 2},
	{word(`ultimate[ .\-]?(?:cut|edition)`), "Ultimate Edition", 2},
	{word(`collector['’]?s[ .\-]?edition`), "Collector's Edition", 2},
	{word(`restored`), "Restored", 2},
	{word(`colou?ri[sz]ed`), "Colorized", 2},
	{word(`despecialized`), "Despecialized", 2},
	{word(`open[ .\-]?matte`), "Open Matte", 2},
	{word(`final[ .\-]?cut`), "Final Cut", 0},
	{word(`redux`), "Redux", 0},
	{word(`(?:\d+(?:st|nd|rd|th)[ .\-]?)?anniversary(?:[ .\-]?edition)?`), "Anniversary Edition", 0},
	{word(`3d`), "3D", 0},
}

// languages, keyed by the word with separators removed.
var langWords = map[string]string{
	"english": "English", "french": "French", "truefrench": "French", "vff": "French", "vfq": "French",
	"vf2": "French", "vfi": "French", "german": "German", "deutsch": "German", "spanish": "Spanish",
	"castellano": "Spanish", "latino": "Spanish", "italian": "Italian", "portuguese": "Portuguese",
	"dutch": "Dutch", "swedish": "Swedish", "norwegian": "Norwegian", "danish": "Danish",
	"finnish": "Finnish", "polish": "Polish", "russian": "Russian", "japanese": "Japanese",
	"korean": "Korean", "chinese": "Chinese", "mandarin": "Chinese", "cantonese": "Chinese",
	"hindi": "Hindi", "turkish": "Turkish", "arabic": "Arabic", "hebrew": "Hebrew", "greek": "Greek",
	"czech": "Czech", "hungarian": "Hungarian", "thai": "Thai", "vietnamese": "Vietnamese",
	"ukrainian": "Ukrainian", "romanian": "Romanian", "nordic": "Nordic", "multi": "Multi",
	"dual": "Dual", "dualaudio": "Dual",
}

var reLang = func() *regexp.Regexp {
	// Dual Audio is the only two-word entry; everything else is one word.
	var words []string
	for k := range langWords {
		if k == "dualaudio" {
			continue
		}
		words = append(words, k)
	}
	sort.Strings(words)
	return word(strings.Join(words, "|") + `|dual[ .\-]?audio|multi[ .\-]?subs?`)
}()

// ---- the parser ------------------------------------------------------------

type weakTok struct {
	span
	pull   int // see editionPatterns
	pulled bool
	apply  func()
}

type labelHit struct {
	pos   int
	label string
}

type srcHit struct {
	pos int
	src Source
}

type parser struct {
	s          string
	r          *Release
	titleStart int
	titleEnd   int

	claims  []span // every token already taken by something
	anchors []int  // markers that end the title (year, episode, ...)
	quality []int  // quality markers (resolution, source, codec, ...)
	weak    []weakTok

	epAnchor   int // start of the episode/air-date/pack marker, -1 when none
	pack       bool
	interlaced bool
	srcHits    []srcHit
	hdrHits    []labelHit
	audioHits  []labelHit
	edHits     []labelHit
	langHits   []labelHit
}

func (p *parser) free(sp span) bool {
	for _, c := range p.claims {
		if c.overlaps(sp) {
			return false
		}
	}
	return true
}

func (p *parser) claim(sp span) { p.claims = append(p.claims, sp) }

// find returns the whole-word matches of re that overlap nothing claimed yet.
func (p *parser) find(re *regexp.Regexp) []tok {
	var out []tok
	for _, t := range findAll(re, p.s) {
		if p.free(t.span) {
			out = append(out, t)
		}
	}
	return out
}

func (p *parser) strong(t tok) {
	p.claim(t.span)
	p.quality = append(p.quality, t.start)
}

func atoi(s string) int {
	n, _ := strconv.Atoi(s)
	return n
}

// ParseRelease reads a file name, a folder name or a release title. A path is
// fine: only its last element is read (a Sample or Samples folder in it marks
// the result as a sample).
func ParseRelease(name string) Release {
	var r Release
	base, dirs := splitPath(name)
	stem, ext := splitExt(base)
	r.Container = ext
	for _, d := range dirs {
		if l := strings.ToLower(strings.TrimSpace(d)); l == "sample" || l == "samples" {
			r.IsSample = true
		}
	}
	s := strings.TrimSpace(strings.ReplaceAll(stem, "_", " "))
	p := &parser{s: s, r: &r, epAnchor: -1}
	p.leading()
	p.episodes()
	p.providerIDs()
	p.resolution()
	p.sources()
	p.codec()
	p.hdr()
	p.audio()
	p.service()
	p.year()
	p.collectWeak()
	p.title()
	p.finish()
	return r
}

var knownExt = func() map[string]bool {
	m := map[string]bool{}
	for _, e := range strings.Fields(`mkv mp4 m4v avi mov ts m2ts mts wmv webm mpg mpeg ogm ogv flv divx vob iso
		flac mp3 m4a aac ogg opus wav alac wma ape wv aiff aif
		srt ass ssa sub idx vtt nfo`) {
		m[e] = true
	}
	return m
}()

func splitPath(name string) (base string, dirs []string) {
	parts := strings.Split(strings.ReplaceAll(name, `\`, "/"), "/")
	for len(parts) > 0 && parts[len(parts)-1] == "" {
		parts = parts[:len(parts)-1]
	}
	if len(parts) == 0 {
		return "", nil
	}
	return parts[len(parts)-1], parts[:len(parts)-1]
}

func splitExt(base string) (stem, ext string) {
	i := strings.LastIndexByte(base, '.')
	if i <= 0 {
		return base, ""
	}
	if e := strings.ToLower(base[i+1:]); knownExt[e] {
		return base[:i], e
	}
	return base, ""
}

// tagLike reports whether the text of a leading [bracket] is quality tags
// rather than a release group.
func tagLike(c string) bool {
	c = strings.TrimSpace(c)
	if c == "" {
		return true
	}
	return reTagFull.MatchString(c) || reTagWords.MatchString(c)
}

// leading finds where the title starts: after a site prefix and any leading
// [bracket] groups. The first bracket that is not a quality tag is the group.
func (p *parser) leading() {
	s := p.s
	pos := 0
	if loc := reWWW.FindStringIndex(s); loc != nil {
		pos = loc[1]
	}
	for pos < len(s) {
		for pos < len(s) && s[pos] == ' ' {
			pos++
		}
		if pos >= len(s) || s[pos] != '[' {
			break
		}
		end := strings.IndexByte(s[pos:], ']')
		if end < 0 {
			break
		}
		c := s[pos+1 : pos+end]
		if !tagLike(c) {
			if p.r.Group != "" {
				break // a second group-looking bracket is part of the title
			}
			p.r.Group = strings.TrimSpace(c)
		}
		pos += end + 1
	}
	for pos < len(s) && (s[pos] == ' ' || s[pos] == '-' || s[pos] == '.') {
		pos++
	}
	p.titleStart = pos
}

// ---- episodes --------------------------------------------------------------

// episodeList turns the first number and the glued-on continuation
// ("E02", "-E03", "-03", "x06") into a list; a hyphen makes a range.
func episodeList(first, cont string) []int {
	eps := []int{atoi(first)}
	for _, m := range reEpCont.FindAllStringSubmatch(cont, -1) {
		n := atoi(m[2])
		prev := eps[len(eps)-1]
		if m[1] == "-" && n > prev && n-prev <= 100 {
			for v := prev + 1; v <= n; v++ {
				eps = append(eps, v)
			}
		} else if n != prev {
			eps = append(eps, n)
		}
		if len(eps) > 200 {
			break
		}
	}
	return eps
}

func (p *parser) episodes() {
	r := p.r
	// SxxEyy, Season N Episode M, 1x05: the earliest marker wins.
	type hit struct {
		t      tok
		season string
		first  string
		cont   string
	}
	var best *hit
	consider := func(re *regexp.Regexp) {
		for _, t := range p.find(re) {
			h := hit{t: t, season: t.sub[0], first: t.sub[1], cont: t.sub[2]}
			if best == nil || h.t.start < best.t.start {
				best = &h
			}
			return
		}
	}
	consider(reSE)
	consider(reSeasonEp)
	consider(reNxNN)
	if best != nil {
		r.Season = atoi(best.season)
		r.Episodes = episodeList(best.first, best.cont)
		p.claim(best.t.span)
		p.anchors = append(p.anchors, best.t.start)
		p.epAnchor = best.t.start
		return
	}

	// Daily shows.
	for _, t := range p.find(reAirYMD) {
		if d, ok := validDate(atoi(t.sub[0]), atoi(t.sub[1]), atoi(t.sub[2])); ok {
			r.AirDate = d
			p.claim(t.span)
			p.anchors = append(p.anchors, t.start)
			p.epAnchor = t.start
			return
		}
	}
	for _, t := range p.find(reAirDMY) {
		a, b, y := atoi(t.sub[0]), atoi(t.sub[1]), atoi(t.sub[2])
		var d string
		var ok bool
		switch {
		case a > 12 && b <= 12:
			d, ok = validDate(y, b, a)
		case b > 12 && a <= 12:
			d, ok = validDate(y, a, b)
		}
		if ok {
			r.AirDate = d
			p.claim(t.span)
			p.anchors = append(p.anchors, t.start)
			p.epAnchor = t.start
			return
		}
	}

	// Season packs: S01, S01-S03, Season 1, Seasons 1-3.
	var packHit *tok
	var packFrom, packTo string
	for _, re := range []*regexp.Regexp{reSeasonPack, reSeasonWord} {
		for _, t := range p.find(re) {
			if packHit == nil || t.start < packHit.start {
				tt := t
				packHit, packFrom, packTo = &tt, t.sub[0], t.sub[1]
			}
			break
		}
	}
	if packHit != nil {
		from := atoi(packFrom)
		r.Season = from
		if packTo != "" {
			if to := atoi(packTo); to > from && to-from < 100 {
				for v := from; v <= to; v++ {
					r.Seasons = append(r.Seasons, v)
				}
			}
		}
		p.pack = true
		p.claim(packHit.span)
		p.anchors = append(p.anchors, packHit.start)
		p.epAnchor = packHit.start
		return
	}

	// Anime absolute numbering: "Title - 07", "Title - 07v2", "Title Ep07".
	for _, re := range []*regexp.Regexp{reAbsDash, reAbsEp, reAbsE} {
		for _, t := range p.find(re) {
			n := atoi(t.sub[0])
			if n == 0 || (len(t.sub[0]) == 4 && n >= 1900 && n <= 2099) {
				continue // a year, not an episode
			}
			r.AbsoluteEpisode = n
			if t.sub[1] != "" {
				r.Version = atoi(t.sub[1])
			}
			p.claim(t.span)
			p.anchors = append(p.anchors, t.start)
			p.epAnchor = t.start
			return
		}
	}
}

// providerIDs notices Jellyfin/Plex style "[tmdbid-123]" and "{imdb-tt123}"
// tags. They are not read; they only end the title.
func (p *parser) providerIDs() {
	for _, t := range p.find(reProvider) {
		p.claim(t.span)
		p.anchors = append(p.anchors, t.start)
	}
}

func validDate(y, m, d int) (string, bool) {
	t := time.Date(y, time.Month(m), d, 0, 0, 0, 0, time.UTC)
	if t.Year() != y || int(t.Month()) != m || t.Day() != d {
		return "", false
	}
	return t.Format("2006-01-02"), true
}

// ---- quality tokens --------------------------------------------------------

func (p *parser) resolution() {
	r := p.r
	for _, t := range p.find(reResP) {
		p.strong(t)
		if r.Resolution == 0 {
			r.Resolution = atoi(t.sub[0])
			p.interlaced = strings.EqualFold(t.sub[1], "i")
		}
	}
	for _, t := range p.find(reResDims) {
		if h := heightFromDims(atoi(t.sub[0]), atoi(t.sub[1])); h > 0 {
			p.strong(t)
			if r.Resolution == 0 {
				r.Resolution = h
			}
		}
	}
	for _, t := range p.find(reRes4K) {
		p.strong(t)
		if r.Resolution == 0 {
			if strings.HasPrefix(p.s[t.start:t.end], "8") {
				r.Resolution = 4320
			} else {
				r.Resolution = 2160
			}
		}
	}
}

func heightFromDims(w, h int) int {
	switch {
	case w >= 3800 || (h >= 2000 && h <= 2200):
		return 2160
	case w >= 1900 || (h >= 1000 && h <= 1100):
		return 1080
	case w >= 1260 || (h >= 700 && h <= 736):
		return 720
	case h == 576:
		return 576
	case w >= 600 && h >= 240 && h <= 500:
		return 480
	}
	return 0
}

func (p *parser) sources() {
	for _, t := range p.find(reRemux) {
		p.strong(t)
		src := SourceRemux
		if strings.HasPrefix(strings.ToLower(t.sub[0]), "dvd") {
			src = SourceDVD
		}
		p.srcHits = append(p.srcHits, srcHit{t.start, src})
	}
	for _, sp := range strongSource {
		for _, t := range p.find(sp.re) {
			p.strong(t)
			p.srcHits = append(p.srcHits, srcHit{t.start, sp.src})
		}
	}
}

func (p *parser) codec() {
	first := len(p.s) + 1 // if several are named, the first one wins
	for _, c := range reCodecs {
		for _, t := range p.find(c.re) {
			p.strong(t)
			if t.start < first {
				first, p.r.Codec = t.start, c.codec
			}
		}
	}
}

func (p *parser) hdr() {
	for _, h := range hdrPatterns {
		if !h.strong {
			continue
		}
		for _, t := range p.find(h.re) {
			p.strong(t)
			p.hdrHits = append(p.hdrHits, labelHit{t.start, h.label})
		}
	}
}

func (p *parser) audio() {
	for _, a := range audioPatterns {
		if !a.strong {
			continue
		}
		for _, t := range p.find(a.re) {
			p.strong(t)
			p.audioHits = append(p.audioHits, labelHit{t.start, a.label})
			if p.r.Channels == "" && len(t.sub) > 0 && t.sub[0] != "" {
				p.r.Channels = strings.Replace(t.sub[0], " ", ".", 1)
			}
		}
	}
}

func (p *parser) service() {
	for _, t := range p.find(reService) {
		p.strong(t)
		if p.r.Service == "" {
			p.r.Service = strings.ToUpper(p.s[t.start:t.end])
			if p.r.Service == "NFLX" {
				p.r.Service = "NF"
			}
		}
	}
}

// ---- year ------------------------------------------------------------------

type yearCand struct {
	span
	year  int
	paren bool
}

func (p *parser) year() {
	var cands []yearCand
	isParen := func(sp span) bool {
		b, a := runeBefore(p.s, sp.start), runeAt(p.s, sp.end)
		return (b == '(' && a == ')') || (b == '[' && a == ']')
	}
	ranges := findAll(reYearRange, p.s)
	for _, t := range ranges {
		if p.free(t.span) && t.start >= p.titleStart {
			cands = append(cands, yearCand{t.span, atoi(t.sub[0]), isParen(t.span)})
		}
	}
	for _, t := range findAll(reYear, p.s) {
		inRange := false
		for _, rg := range ranges {
			if rg.overlaps(t.span) {
				inRange = true
			}
		}
		if !inRange && p.free(t.span) && t.start >= p.titleStart {
			cands = append(cands, yearCand{t.span, atoi(p.s[t.start:t.end]), isParen(t.span)})
		}
	}
	if len(cands) == 0 {
		return
	}
	sort.Slice(cands, func(i, j int) bool { return cands[i].start < cands[j].start })

	// Years only count before the first episode or quality marker; the year
	// that is part of the name ("2001 A Space Odyssey") is not the release year.
	limit := len(p.s)
	if p.epAnchor >= 0 {
		limit = p.epAnchor
	}
	for _, q := range p.quality {
		if q >= p.titleStart && q < limit {
			limit = q
		}
	}
	var eligible []yearCand
	for _, c := range cands {
		if c.start < limit {
			eligible = append(eligible, c)
		}
	}
	if len(eligible) == 0 && p.epAnchor < 0 {
		for _, c := range cands {
			if c.paren {
				eligible = append(eligible, c)
				break
			}
		}
	}
	if len(eligible) == 0 {
		return
	}
	var chosen *yearCand
	for i := range eligible {
		if eligible[i].paren {
			chosen = &eligible[i] // the first parenthesised year
			break
		}
	}
	if chosen == nil {
		if len(eligible) == 1 && eligible[0].start == p.titleStart {
			return // the only number is the title itself: "2012 1080p BluRay"
		}
		chosen = &eligible[len(eligible)-1]
	}
	p.r.Year = chosen.year
	p.claim(chosen.span)
	p.anchors = append(p.anchors, chosen.start)
}

// ---- weak tokens, title, finish -------------------------------------------

func (p *parser) addWeak(t tok, pull int, apply func()) {
	p.weak = append(p.weak, weakTok{span: t.span, pull: pull, apply: apply})
}

func (p *parser) collectWeak() {
	r := p.r
	for _, e := range editionPatterns {
		for _, t := range p.find(e.re) {
			label := e.label
			p.addWeak(t, e.pull, func() { p.edHits = append(p.edHits, labelHit{t.start, label}) })
		}
	}
	for _, t := range p.find(reLang) {
		key := strings.ToLower(strings.Map(func(c rune) rune {
			if c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' {
				return c
			}
			return -1
		}, p.s[t.start:t.end]))
		label, ok := langWords[key]
		if !ok {
			if strings.HasPrefix(key, "multi") {
				label = "Multi"
			} else {
				continue
			}
		}
		p.addWeak(t, 1, func() { p.langHits = append(p.langHits, labelHit{t.start, label}) })
	}
	for _, t := range p.find(reProper) {
		p.addWeak(t, 1, func() { r.Proper = true })
	}
	for _, t := range p.find(reRepack) {
		v := 1
		if t.sub[0] != "" {
			v = atoi(t.sub[0])
		}
		p.addWeak(t, 1, func() {
			r.Repack = true
			if r.RepackVersion == 0 || v > r.RepackVersion {
				r.RepackVersion = v
			}
		})
	}
	for _, t := range p.find(reReal) {
		p.addWeak(t, 0, func() { r.Real = true })
	}
	for _, t := range p.find(reComplete) {
		p.addWeak(t, 2, func() { r.Complete = true })
	}
	for _, sp := range weakSource {
		for _, t := range p.find(sp.re) {
			src := sp.src
			p.addWeak(t, 0, func() { p.srcHits = append(p.srcHits, srcHit{t.start, src}) })
		}
	}
	for _, a := range audioPatterns {
		if a.strong {
			continue
		}
		for _, t := range p.find(a.re) {
			label := a.label
			p.addWeak(t, 0, func() {
				p.audioHits = append(p.audioHits, labelHit{t.start, label})
				if r.Channels == "" && len(t.sub) > 0 && t.sub[0] != "" {
					r.Channels = strings.Replace(t.sub[0], " ", ".", 1)
				}
			})
		}
	}
	for _, h := range hdrPatterns {
		if h.strong {
			continue
		}
		for _, t := range p.find(h.re) {
			label := h.label
			p.addWeak(t, 0, func() { p.hdrHits = append(p.hdrHits, labelHit{t.start, label}) })
		}
	}
	for _, t := range p.find(reChannels) {
		p.addWeak(t, 0, func() {
			if r.Channels == "" {
				r.Channels = p.s[t.start:t.end]
			}
		})
	}
}

// title works out where the title ends, applies the weak tokens that sit after
// it, and cleans the text before it.
func (p *parser) title() {
	best, fromAnchor := len(p.s), false
	for _, a := range p.anchors {
		if a >= p.titleStart && a < best {
			best, fromAnchor = a, true
		}
	}
	for _, q := range p.quality {
		if q >= p.titleStart && q < best {
			best, fromAnchor = q, false
		}
	}
	// Words like PROPER or MULTi that sit right before the first marker belong
	// to the tags, not to the title.
	need := 1
	if fromAnchor {
		need = 2
	}
	for {
		idx := -1
		for i, w := range p.weak {
			if !w.pulled && w.pull >= need && w.start >= p.titleStart && w.end <= best && onlyDelims(p.s[w.end:best]) {
				idx = i
				break
			}
		}
		if idx < 0 {
			break
		}
		p.weak[idx].pulled = true
		best = p.weak[idx].start
	}
	p.titleEnd = best

	sort.SliceStable(p.weak, func(i, j int) bool { return p.weak[i].start < p.weak[j].start })
	for _, w := range p.weak {
		if w.start >= best && p.free(w.span) {
			p.claim(w.span)
			w.apply()
		}
	}
	if best > p.titleStart {
		p.r.Title = cleanTitle(p.s[p.titleStart:best])
	}
}

// cleanTitle turns "Pioneer.One." or "Mr. Robot (" into a plain title.
func cleanTitle(region string) string {
	dotEnd := strings.HasSuffix(region, ".")
	t := collapseAcronyms(region)
	// A dot not followed by a space separates words ("Pioneer.One"); a dot
	// followed by a space is part of the name ("Mr. Robot", "Sherlock Jr.").
	rs := []rune(t)
	for i, c := range rs {
		if c == '.' && i+1 < len(rs) && rs[i+1] != ' ' {
			rs[i] = ' '
		}
	}
	t = foldSpace(string(rs))
	t = strings.Trim(t, " ([{-_,")
	t = strings.TrimLeft(t, ".")
	if dotEnd {
		t = strings.TrimRight(t, ".")
	}
	return strings.TrimSpace(t)
}

func (p *parser) finish() {
	r := p.r

	// Source: a remux beats everything; otherwise the first one named.
	if len(p.srcHits) > 0 {
		sort.SliceStable(p.srcHits, func(i, j int) bool { return p.srcHits[i].pos < p.srcHits[j].pos })
		r.Source = p.srcHits[0].src
		for _, h := range p.srcHits {
			if h.src == SourceRemux {
				r.Source = SourceRemux
				break
			}
		}
	}
	// HDTV captured as MPEG-2 or as a transport stream is raw broadcast video.
	if r.Source == SourceHDTV && (r.Codec == CodecMPEG2 || (p.interlaced && (r.Container == "ts" || r.Container == "m2ts"))) {
		r.Source = SourceRawHD
	}

	r.HDR = dedupe(p.hdrHits, func(have map[string]bool, l string) bool {
		return (l == "HDR" && (have["HDR10"] || have["HDR10+"])) || (l == "HDR10" && have["HDR10+"])
	})
	r.Audio = dedupe(p.audioHits, nil)
	r.Languages = dedupe(p.langHits, nil)
	if eds := dedupe(p.edHits, nil); len(eds) > 0 {
		r.Edition = strings.Join(eds, " ")
	}

	// A season or series with no single episode is a pack.
	if len(r.Episodes) == 0 && r.AirDate == "" && r.AbsoluteEpisode == 0 && (p.pack || r.Complete) {
		r.FullSeason = true
	}

	p.group()
	p.sample()
}

// dedupe orders hits by position and drops repeats and anything skip rejects.
func dedupe(hits []labelHit, skip func(have map[string]bool, label string) bool) []string {
	if len(hits) == 0 {
		return nil
	}
	sort.SliceStable(hits, func(i, j int) bool { return hits[i].pos < hits[j].pos })
	have := map[string]bool{}
	for _, h := range hits {
		have[h.label] = true
	}
	var out []string
	seen := map[string]bool{}
	for _, h := range hits {
		if seen[h.label] || (skip != nil && skip(have, h.label)) {
			continue
		}
		seen[h.label] = true
		out = append(out, h.label)
	}
	return out
}

// group reads the scene-style "-GRP" tail.
func (p *parser) group() {
	if p.r.Group != "" {
		return
	}
	body := strings.TrimSpace(p.s)
	if loc := reSampleTail.FindStringIndex(body); loc != nil && loc[0] >= p.titleEnd {
		body = body[:loc[0]] // "-GRP-sample"
	}
	// Drop trailing [tags] and (tags): "-GRP[site]".
	for len(body) > 0 && (body[len(body)-1] == ']' || body[len(body)-1] == ')') {
		open := byte('[')
		if body[len(body)-1] == ')' {
			open = '('
		}
		j := strings.LastIndexByte(body, open)
		if j <= 0 {
			break
		}
		body = strings.TrimSpace(body[:j])
	}
	d := strings.LastIndexByte(body, '-')
	if d < 0 || d < p.titleEnd {
		return // no marker before the dash: "Spider-Man"
	}
	g := body[d+1:]
	if !reGroup.MatchString(g) || strings.Trim(g, "0123456789") == "" {
		return
	}
	if !p.free(span{d + 1, len(body)}) {
		return // the tail is part of a token ("WEB-DL", "DTS-HD")
	}
	p.r.Group = g
}

func (p *parser) sample() {
	if p.r.IsSample {
		return
	}
	l := strings.ToLower(p.s)
	switch {
	case l == "sample" || strings.HasPrefix(l, "sample-"):
		p.r.IsSample = true
	default:
		if loc := reSampleTail.FindStringIndex(p.s); loc != nil && loc[0] >= p.titleEnd && p.titleEnd < len(p.s) {
			p.r.IsSample = true
		}
	}
}
