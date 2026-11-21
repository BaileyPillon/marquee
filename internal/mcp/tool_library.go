package mcp

import (
	"context"
	"errors"
	"fmt"
	"path"
	"slices"
	"sort"
	"strconv"
	"strings"

	"github.com/BaileyPillon/marquee/internal/titles"
)

type fileOut struct {
	FileName  string `json:"fileName"`
	Quality   string `json:"quality,omitempty"`
	Copy      string `json:"copy"`
	SizeBytes int64  `json:"sizeBytes"`
}

type seasonOut struct {
	Season          int   `json:"season"`
	Monitored       bool  `json:"monitored"`
	Episodes        int   `json:"episodes"`
	Have            int   `json:"have"`
	Missing         int   `json:"missing"`
	Upcoming        int   `json:"upcoming"`
	HaveEpisodes    []int `json:"haveEpisodes"`
	MissingEpisodes []int `json:"missingEpisodes"`
}

type nextEpisodeOut struct {
	Season  int    `json:"season"`
	Episode int    `json:"episode"`
	Name    string `json:"name,omitempty"`
	AirDate string `json:"airDate"`
}

type albumOut struct {
	AlbumID   int64  `json:"albumId"`
	Name      string `json:"name"`
	Year      int    `json:"year,omitempty"`
	Type      string `json:"type,omitempty"`
	Monitored bool   `json:"monitored"`
	Tracks    int    `json:"tracks"`
}

type libraryOut struct {
	TitleID    int64    `json:"titleId"`
	Kind       string   `json:"kind"`
	Name       string   `json:"name"`
	Year       int      `json:"year"`
	Status     string   `json:"status,omitempty"`
	Monitored  bool     `json:"monitored"`
	Overview   string   `json:"overview,omitempty"`
	Genres     []string `json:"genres,omitempty"`
	RuntimeMin int      `json:"runtimeMin,omitempty"`
	OnDisk     bool     `json:"onDisk"`
	Files      struct {
		Count int   `json:"count"`
		Bytes int64 `json:"bytes"`
	} `json:"files"`
	QualitiesOnDisk []string `json:"qualitiesOnDisk"`
	Copies          struct {
		HD  bool `json:"hd"`
		UHD bool `json:"uhd"`
	} `json:"copies"`
	Movie *struct {
		Files []fileOut `json:"files"`
	} `json:"movie,omitempty"`
	Show *struct {
		Seasons     []seasonOut     `json:"seasons"`
		NextEpisode *nextEpisodeOut `json:"nextEpisode,omitempty"`
	} `json:"show,omitempty"`
	Artist *struct {
		Albums []albumOut `json:"albums"`
	} `json:"artist,omitempty"`
}

// copyOf says which copy a file belongs to, the way the rest of Marquee reads a
// quality label: 2160p is the 4K copy, everything else the HD one.
func copyOf(quality string) string {
	if strings.Contains(quality, "2160p") {
		return "uhd"
	}
	return "hd"
}

func (s *Server) libraryLookup(ctx context.Context, c *toolCall) (*toolOutput, error) {
	var a struct {
		TitleID flexInt `json:"titleId"`
	}
	if err := decodeArgs(c.Args, &a); err != nil {
		return nil, err
	}
	if a.TitleID <= 0 {
		return nil, userErrorf(`Give the title's id in "titleId" (find it with search_titles).`)
	}
	d, err := s.d.Titles.Get(ctx, int64(a.TitleID))
	if errors.Is(err, titles.ErrNotFound) {
		return nil, userErrorf("Marquee does not track a title with id %d. Find the right one with search_titles.", a.TitleID)
	}
	if err != nil {
		return nil, err
	}
	files, err := s.store.TitleFiles(ctx, d.ID)
	if err != nil {
		return nil, err
	}

	out := libraryOut{
		TitleID: d.ID, Kind: d.Kind, Name: d.Name, Year: d.Year, Status: d.Status, Monitored: d.Monitored,
		Overview: shorten(d.Overview, 400), Genres: d.Genres, RuntimeMin: d.RuntimeMin,
		OnDisk: len(files) > 0, QualitiesOnDisk: []string{},
	}
	seenQuality := map[string]bool{}
	for _, f := range files {
		out.Files.Count++
		out.Files.Bytes += f.Size
		if f.Quality != "" && !seenQuality[f.Quality] {
			seenQuality[f.Quality] = true
			out.QualitiesOnDisk = append(out.QualitiesOnDisk, f.Quality)
		}
		if copyOf(f.Quality) == "uhd" {
			out.Copies.UHD = true
		} else {
			out.Copies.HD = true
		}
	}
	sort.Strings(out.QualitiesOnDisk)

	var text string
	switch d.Kind {
	case titles.KindMovie:
		text = s.movieDetail(&out, files)
	case titles.KindShow:
		text = s.showDetail(&out, d, files)
	default:
		text = artistDetail(&out, d, files)
	}
	return &toolOutput{Text: text, Data: out}, nil
}

func headline(o *libraryOut) string {
	mon := "not monitored"
	if o.Monitored {
		mon = "monitored"
	}
	extra := ""
	if o.Status != "" {
		extra = ", " + o.Status
	}
	return fmt.Sprintf("%s%s, %s%s, %s (titleId %d).", o.Name, yearSuffix(o.Year), o.Kind, extra, mon, o.TitleID)
}

func (s *Server) movieDetail(o *libraryOut, files []FileRow) string {
	o.Movie = &struct {
		Files []fileOut `json:"files"`
	}{Files: []fileOut{}}
	for _, f := range files {
		o.Movie.Files = append(o.Movie.Files, fileOut{FileName: path.Base(f.RelPath), Quality: f.Quality, Copy: copyOf(f.Quality), SizeBytes: f.Size})
	}
	var b strings.Builder
	b.WriteString(headline(o))
	if len(files) == 0 {
		b.WriteString(" Not on disk.")
		return b.String()
	}
	b.WriteString(" On disk:")
	for _, f := range o.Movie.Files {
		q := f.Quality
		if q == "" {
			q = "quality unknown"
		}
		fmt.Fprintf(&b, "\n- %s, %s (%s copy)", q, humanBytes(f.SizeBytes), strings.ToUpper(f.Copy))
	}
	return b.String()
}

func (s *Server) showDetail(o *libraryOut, d *titles.Detail, files []FileRow) string {
	have := map[int64]bool{}
	for _, f := range files {
		for _, id := range f.EpisodeIDs {
			have[id] = true
		}
	}
	today := s.now().Format("2006-01-02")

	bySeason := map[int]*seasonOut{}
	get := func(n int) *seasonOut {
		if bySeason[n] == nil {
			bySeason[n] = &seasonOut{Season: n, HaveEpisodes: []int{}, MissingEpisodes: []int{}}
		}
		return bySeason[n]
	}
	for _, se := range d.Seasons {
		get(se.Number).Monitored = se.Monitored
	}
	var next *nextEpisodeOut
	for _, e := range d.Episodes {
		se := get(e.Season)
		se.Episodes++
		switch {
		case have[e.ID]:
			se.Have++
			se.HaveEpisodes = append(se.HaveEpisodes, e.Number)
		case e.AirDate != "" && e.AirDate <= today:
			se.Missing++
			se.MissingEpisodes = append(se.MissingEpisodes, e.Number)
		default:
			se.Upcoming++
		}
		if e.AirDate >= today && !have[e.ID] && (next == nil || e.AirDate < next.AirDate) {
			next = &nextEpisodeOut{Season: e.Season, Episode: e.Number, Name: e.Name, AirDate: e.AirDate}
		}
	}
	seasons := make([]seasonOut, 0, len(bySeason))
	for _, se := range bySeason {
		slices.Sort(se.HaveEpisodes)
		slices.Sort(se.MissingEpisodes)
		seasons = append(seasons, *se)
	}
	sort.Slice(seasons, func(i, j int) bool { return seasons[i].Season < seasons[j].Season })
	o.Show = &struct {
		Seasons     []seasonOut     `json:"seasons"`
		NextEpisode *nextEpisodeOut `json:"nextEpisode,omitempty"`
	}{Seasons: seasons, NextEpisode: next}

	var b strings.Builder
	b.WriteString(headline(o))
	if len(files) > 0 {
		fmt.Fprintf(&b, " %s on disk (%s).", plural(o.Files.Count, "file", "files"), humanBytes(o.Files.Bytes))
	} else {
		b.WriteString(" Nothing on disk.")
	}
	for _, se := range seasons {
		name := "Season " + strconv.Itoa(se.Season)
		if se.Season == 0 {
			name = "Specials"
		}
		fmt.Fprintf(&b, "\n- %s: %d of %d episodes on disk", name, se.Have, se.Episodes)
		if se.Have > 0 {
			b.WriteString(" (" + episodeRanges(se.HaveEpisodes) + ")")
		}
		if se.Missing > 0 {
			fmt.Fprintf(&b, "; missing (aired, not on disk): %s", episodeRanges(se.MissingEpisodes))
		}
		if se.Upcoming > 0 {
			fmt.Fprintf(&b, "; %d still to air", se.Upcoming)
		}
		if !se.Monitored {
			b.WriteString("; not monitored")
		}
	}
	if next != nil {
		fmt.Fprintf(&b, "\nNext to air: S%02dE%02d %s on %s.", next.Season, next.Episode, next.Name, next.AirDate)
	}
	return b.String()
}

func artistDetail(o *libraryOut, d *titles.Detail, files []FileRow) string {
	o.Artist = &struct {
		Albums []albumOut `json:"albums"`
	}{Albums: []albumOut{}}
	for _, al := range d.Albums {
		o.Artist.Albums = append(o.Artist.Albums, albumOut{
			AlbumID: al.ID, Name: al.Name, Year: al.Year, Type: al.Type, Monitored: al.Monitored, Tracks: len(al.Tracks),
		})
	}
	var b strings.Builder
	b.WriteString(headline(o))
	fmt.Fprintf(&b, " %s on disk", plural(len(files), "file", "files"))
	if len(files) > 0 {
		fmt.Fprintf(&b, " (%s)", humanBytes(o.Files.Bytes))
	}
	b.WriteString(".")
	for _, al := range o.Artist.Albums {
		fmt.Fprintf(&b, "\n- %s%s (%s)", al.Name, yearSuffix(al.Year), plural(al.Tracks, "track", "tracks"))
	}
	return b.String()
}

// episodeRanges writes sorted episode numbers as "1-3, 5".
func episodeRanges(nums []int) string {
	var parts []string
	for i := 0; i < len(nums); {
		j := i
		for j+1 < len(nums) && nums[j+1] == nums[j]+1 {
			j++
		}
		if j > i {
			parts = append(parts, fmt.Sprintf("%d-%d", nums[i], nums[j]))
		} else {
			parts = append(parts, strconv.Itoa(nums[i]))
		}
		i = j + 1
	}
	return "episodes " + strings.Join(parts, ", ")
}
