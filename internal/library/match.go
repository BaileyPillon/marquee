package library

import (
	"context"
	"encoding/json"
	"log/slog"
	"path"
	"regexp"
	"slices"
	"strconv"
	"strings"

	"github.com/BaileyPillon/marquee/internal/parse"
)

// candIdx is a candidate with its names already normalized.
type candIdx struct {
	Candidate
	names []string
}

// matcher reads one file's path and, when a TitleSource is set, finds the
// title (and episodes) it belongs to. It lives for one scan: the candidate
// lists are fetched once and kept.
type matcher struct {
	ctx   context.Context
	ts    TitleSource
	log   *slog.Logger
	cands map[string][]candIdx
}

func newMatcher(ctx context.Context, ts TitleSource, log *slog.Logger) *matcher {
	return &matcher{ctx: ctx, ts: ts, log: log, cands: map[string][]candIdx{}}
}

// enabled reports whether matching can find anything at all.
func (m *matcher) enabled() bool { return m.ts != nil }

func (m *matcher) candidates(kind string) []candIdx {
	if m.ts == nil {
		return nil
	}
	if c, ok := m.cands[kind]; ok {
		return c
	}
	list, err := m.ts.Candidates(m.ctx, kind)
	if err != nil {
		// Matching waits for the next scan; the files stay unmatched till then.
		m.log.Warn("library: could not list titles to match against", "kind", kind, "err", err)
		m.cands[kind] = nil
		return nil
	}
	idx := make([]candIdx, 0, len(list))
	for _, c := range list {
		ci := candIdx{Candidate: c}
		for _, n := range append([]string{c.Name}, c.AltNames...) {
			if nn := parse.Normalize(n); nn != "" && !slices.Contains(ci.names, nn) {
				ci.names = append(ci.names, nn)
			}
		}
		idx = append(idx, ci)
	}
	m.cands[kind] = idx
	return idx
}

func mustJSON(v any) string {
	b, err := json.Marshal(v)
	if err != nil {
		return "{}"
	}
	return string(b)
}

// classify parses one file's relative path and tries to match it. The result
// is unmatched unless a title was found.
func (m *matcher) classify(kind, rel string) fileWrite {
	w := fileWrite{Rel: rel, Kind: kind, State: StateUnmatched}
	switch kind {
	case KindMusic:
		mr := parse.ParseMusic(rel)
		w.Parsed, w.Quality = mustJSON(mr), mr.QualityName()
		m.matchMusic(&w, mr)
	case KindShow:
		fr := parse.ParseRelease(path.Base(rel))
		w.Parsed, w.Quality = mustJSON(fr), parse.QualityName(fr)
		m.matchShow(&w, rel, fr)
	default:
		fr := parse.ParseRelease(path.Base(rel))
		w.Parsed, w.Quality = mustJSON(fr), parse.QualityName(fr)
		m.matchMovie(&w, rel, fr)
	}
	return w
}

// hint is one guess at a title name found in a path.
type hint struct {
	Title string
	Year  int
}

func abs(n int) int {
	if n < 0 {
		return -n
	}
	return n
}

// pick finds the one candidate whose normalized name (or an alternate name)
// equals the normalized title and whose year is within one of year. With no
// year, exactly one candidate may carry that name. A candidate with no known
// year fits any year, but a closer year always wins.
func pick(cands []candIdx, title string, year int) (Candidate, bool) {
	n := parse.Normalize(title)
	if n == "" {
		return Candidate{}, false
	}
	var best []Candidate
	bestDiff := -1
	for _, c := range cands {
		if !slices.Contains(c.names, n) {
			continue
		}
		diff := 0
		if year > 0 {
			switch {
			case c.Year == 0:
				diff = 1
			case abs(c.Year-year) > 1:
				continue
			default:
				diff = abs(c.Year - year)
			}
		}
		if bestDiff == -1 || diff < bestDiff {
			best, bestDiff = best[:0], diff
		}
		if diff == bestDiff {
			best = append(best, c.Candidate)
		}
	}
	if len(best) == 1 {
		return best[0], true
	}
	return Candidate{}, false
}

func setMatch(w *fileWrite, id int64) {
	w.State = StateMatched
	w.TitleID = &id
}

// ---- movies ------------------------------------------------------------------

// dirOf returns the directory names above a relative path, shallowest first.
func dirOf(rel string) []string {
	d := path.Dir(rel)
	if d == "." || d == "/" {
		return nil
	}
	return strings.Split(d, "/")
}

// matchMovie tries the file's own name, then the folder it sits in (Plex and
// Jellyfin put "Title (Year)/Title (Year).mkv"). A year the file name lacks is
// borrowed from the folder and the other way round.
func (m *matcher) matchMovie(w *fileWrite, rel string, fr parse.Release) {
	cands := m.candidates(CandidateMovie)
	if len(cands) == 0 {
		return
	}
	var hints []hint
	var parent parse.Release
	if dirs := dirOf(rel); len(dirs) > 0 {
		parent = parse.ParseRelease(dirs[len(dirs)-1])
	}
	if fr.Title != "" {
		hints = append(hints, hint{fr.Title, firstNonZero(fr.Year, parent.Year)})
	}
	if parent.Title != "" {
		hints = append(hints, hint{parent.Title, firstNonZero(parent.Year, fr.Year)})
	}
	for _, h := range hints {
		if c, ok := pick(cands, h.Title, h.Year); ok {
			setMatch(w, c.ID)
			return
		}
	}
	// "Blade.Runner.2049.1080p" reads as a film from 2049, but a title can end
	// in a year-like number. Try the number as part of the file's own title.
	if fr.Title != "" && fr.Year >= 1900 {
		if c, ok := pick(cands, fr.Title+" "+strconv.Itoa(fr.Year), parent.Year); ok {
			setMatch(w, c.ID)
		}
	}
}

func firstNonZero(a, b int) int {
	if a != 0 {
		return a
	}
	return b
}

// ---- shows -------------------------------------------------------------------

var reSeasonDir = regexp.MustCompile(`(?i)^(?:(?:season|series|saison|staffel|temporada)[ ._-]*\d{1,4}(?:[^0-9].*)?|s\d{1,4}|specials?)$`)

func isSeasonDir(name string) bool { return reSeasonDir.MatchString(strings.TrimSpace(name)) }

// matchShow finds the series by its folder (the nearest folder that is not a
// "Season 1" folder) or by the title in the file name, then maps the season
// and episode numbers to episode ids. Every episode in the name must exist, or
// the file stays unmatched for the next scan to try again. A file with no
// episode numbers (daily or absolute numbering) matches the series only.
func (m *matcher) matchShow(w *fileWrite, rel string, fr parse.Release) {
	cands := m.candidates(CandidateShow)
	if len(cands) == 0 {
		return
	}
	var hints []hint
	dirs := dirOf(rel)
	for i := len(dirs) - 1; i >= 0; i-- {
		if isSeasonDir(dirs[i]) {
			continue
		}
		pr := parse.ParseRelease(dirs[i])
		if pr.Title != "" {
			hints = append(hints, hint{pr.Title, firstNonZero(pr.Year, fr.Year)})
		}
	}
	if fr.Title != "" {
		hints = append(hints, hint{fr.Title, fr.Year})
	}
	var series Candidate
	found := false
	for _, h := range hints {
		series, found = pick(cands, h.Title, h.Year)
		if !found && h.Year > 0 {
			// A year in a show's name is not always its first-air year; if
			// only one show carries the name, take it.
			series, found = pick(cands, h.Title, 0)
		}
		if found {
			break
		}
	}
	if !found {
		return
	}
	if len(fr.Episodes) > 0 {
		ids := make([]int64, 0, len(fr.Episodes))
		for _, ep := range fr.Episodes {
			id, ok, err := m.ts.EpisodeID(m.ctx, series.ID, fr.Season, ep)
			if err != nil {
				m.log.Warn("library: episode lookup failed", "title", series.ID, "season", fr.Season, "episode", ep, "err", err)
				return
			}
			if !ok {
				return
			}
			ids = append(ids, id)
		}
		w.EpisodeIDs = ids
	}
	setMatch(w, series.ID)
}

// ---- music -------------------------------------------------------------------

// matchMusic matches the artist only; albums and tracks come with the music
// milestone.
func (m *matcher) matchMusic(w *fileWrite, mr parse.MusicRelease) {
	if mr.Artist == "" {
		return
	}
	cands := m.candidates(CandidateArtist)
	if c, ok := pick(cands, mr.Artist, 0); ok {
		setMatch(w, c.ID)
	}
}
