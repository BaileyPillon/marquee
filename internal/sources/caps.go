package sources

import (
	"encoding/xml"
	"io"
	"slices"
	"strconv"
	"strings"
	"time"
)

// Search modes a source can offer. They are the Torznab "t=" values, with the
// hyphens dropped from their names here.
const (
	ModeSearch = "search"
	ModeTV     = "tvsearch"
	ModeMovie  = "movie"
	ModeMusic  = "music"
)

// Mode is one search mode of a source and the parameters it understands.
type Mode struct {
	Available bool     `json:"available"`
	Params    []string `json:"params"`
}

// Category is one category a source lists.
type Category struct {
	ID   int    `json:"id"`
	Name string `json:"name"`
}

// Caps is what a source says it can do (the answer to t=caps), kept so a search
// only uses the id parameters the source understands.
type Caps struct {
	Title        string          `json:"title,omitempty"`
	LimitMax     int             `json:"limitMax"`
	LimitDefault int             `json:"limitDefault"`
	Modes        map[string]Mode `json:"modes"`
	Categories   []Category      `json:"categories"`
	FetchedAt    time.Time       `json:"fetchedAt"`
}

// ModeAvailable reports whether the source offers a mode.
func (c *Caps) ModeAvailable(mode string) bool {
	if c == nil {
		return mode == ModeSearch
	}
	return c.Modes[mode].Available
}

// Supports reports whether a mode understands a parameter.
func (c *Caps) Supports(mode, param string) bool {
	if c == nil {
		return false
	}
	m := c.Modes[mode]
	return m.Available && slices.Contains(m.Params, param)
}

// Limit is how many items one search may ask for, given the wish.
func (c *Caps) Limit(want int) int {
	if want <= 0 {
		want = DefaultLimit
	}
	if c != nil && c.LimitMax > 0 && want > c.LimitMax {
		want = c.LimitMax
	}
	return want
}

type xmlCaps struct {
	XMLName xml.Name `xml:"caps"`
	Server  struct {
		Title string `xml:"title,attr"`
	} `xml:"server"`
	Limits struct {
		Max     string `xml:"max,attr"`
		Default string `xml:"default,attr"`
	} `xml:"limits"`
	Searching struct {
		Search      xmlMode `xml:"search"`
		TVSearch    xmlMode `xml:"tv-search"`
		MovieSearch xmlMode `xml:"movie-search"`
		MusicSearch xmlMode `xml:"music-search"`
		AudioSearch xmlMode `xml:"audio-search"`
	} `xml:"searching"`
	Categories struct {
		Cats []xmlCategory `xml:"category"`
	} `xml:"categories"`
}

type xmlMode struct {
	Available string `xml:"available,attr"`
	Params    string `xml:"supportedParams,attr"`
}

type xmlCategory struct {
	ID     string        `xml:"id,attr"`
	Name   string        `xml:"name,attr"`
	Subcat []xmlCategory `xml:"subcat"`
}

func (m xmlMode) mode() Mode {
	out := Mode{Available: strings.EqualFold(strings.TrimSpace(m.Available), "yes"), Params: []string{}}
	for _, p := range strings.Split(m.Params, ",") {
		if p = strings.ToLower(strings.TrimSpace(p)); p != "" && !slices.Contains(out.Params, p) {
			out.Params = append(out.Params, p)
		}
	}
	return out
}

// ParseCaps reads the answer to t=caps. now stamps FetchedAt.
func ParseCaps(r io.Reader, now time.Time) (*Caps, error) {
	var x xmlCaps
	if err := xml.NewDecoder(io.LimitReader(r, maxFeedBytes)).Decode(&x); err != nil {
		return nil, err
	}
	c := &Caps{Title: strings.TrimSpace(x.Server.Title), Modes: map[string]Mode{}, FetchedAt: now.UTC()}
	c.LimitMax, _ = strconv.Atoi(strings.TrimSpace(x.Limits.Max))
	c.LimitDefault, _ = strconv.Atoi(strings.TrimSpace(x.Limits.Default))
	s := x.Searching
	c.Modes[ModeSearch] = s.Search.mode()
	c.Modes[ModeTV] = s.TVSearch.mode()
	c.Modes[ModeMovie] = s.MovieSearch.mode()
	music := s.MusicSearch
	if music.Available == "" && music.Params == "" {
		music = s.AudioSearch
	}
	c.Modes[ModeMusic] = music.mode()

	var walk func(cats []xmlCategory)
	walk = func(cats []xmlCategory) {
		for _, cat := range cats {
			if id, err := strconv.Atoi(strings.TrimSpace(cat.ID)); err == nil {
				c.Categories = append(c.Categories, Category{ID: id, Name: strings.TrimSpace(cat.Name)})
			}
			walk(cat.Subcat)
		}
	}
	walk(x.Categories.Cats)
	if c.Categories == nil {
		c.Categories = []Category{}
	}
	return c, nil
}
