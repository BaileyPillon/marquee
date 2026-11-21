package mcp

import (
	"context"
	"fmt"
	"math"
	"strings"
	"time"

	"github.com/BaileyPillon/marquee/internal/downloads"
)

// The read-only status tools: calendar, downloads and health.

// ---- calendar ---------------------------------------------------------------------

const (
	defaultCalendarDays = 7
	maxCalendarDays     = 30
)

type airingOut struct {
	TitleID int64  `json:"titleId"`
	Show    string `json:"show"`
	Season  int    `json:"season"`
	Episode int    `json:"episode"`
	Name    string `json:"name"`
	AirDate string `json:"airDate"`
}

type calendarOut struct {
	From     string      `json:"from"`
	To       string      `json:"to"`
	Days     int         `json:"days"`
	Episodes []airingOut `json:"episodes"`
}

func (s *Server) calendar(ctx context.Context, c *toolCall) (*toolOutput, error) {
	var a struct {
		Days flexInt `json:"days"`
	}
	if err := decodeArgs(c.Args, &a); err != nil {
		return nil, err
	}
	days := defaultCalendarDays
	if a.Days != 0 {
		if a.Days < 1 || a.Days > maxCalendarDays {
			return nil, userErrorf(`"days" must be a whole number from 1 to %d.`, maxCalendarDays)
		}
		days = int(a.Days)
	}
	now := s.now()
	out := calendarOut{
		From: now.Format("2006-01-02"), To: now.AddDate(0, 0, days-1).Format("2006-01-02"),
		Days: days, Episodes: []airingOut{},
	}
	rows, err := s.store.Upcoming(ctx, out.From, out.To)
	if err != nil {
		return nil, err
	}
	for _, r := range rows {
		out.Episodes = append(out.Episodes, airingOut{
			TitleID: r.TitleID, Show: r.Show, Season: r.Season, Episode: r.Number, Name: r.Name, AirDate: r.AirDate,
		})
	}

	var b strings.Builder
	if len(out.Episodes) == 0 {
		fmt.Fprintf(&b, "No episodes of monitored shows air from %s to %s.", out.From, out.To)
	} else {
		fmt.Fprintf(&b, "%s of monitored shows air from %s to %s:", plural(len(out.Episodes), "episode", "episodes"), out.From, out.To)
		for _, e := range out.Episodes {
			name := ""
			if e.Name != "" {
				name = " " + e.Name
			}
			fmt.Fprintf(&b, "\n- %s: %s S%02dE%02d%s", e.AirDate, e.Show, e.Season, e.Episode, name)
		}
	}
	return &toolOutput{Text: b.String(), Data: out}, nil
}

// ---- downloads --------------------------------------------------------------------

const downloadsShown = 50

type downloadOut struct {
	ID               int64   `json:"id"`
	Title            string  `json:"title"`
	Kind             string  `json:"kind,omitempty"`
	Quality          string  `json:"quality,omitempty"`
	State            string  `json:"state"`
	ProgressPercent  float64 `json:"progressPercent"`
	SpeedBytesPerSec int64   `json:"speedBytesPerSec"`
	SizeBytes        int64   `json:"sizeBytes"`
	ETASeconds       *int64  `json:"etaSeconds,omitempty"`
	ReleaseTitle     string  `json:"releaseTitle,omitempty"`
}

type downloadsOut struct {
	Count     int           `json:"count"`
	Downloads []downloadOut `json:"downloads"`
	Summary   struct {
		Downloading int `json:"downloading"`
		Queued      int `json:"queued"`
		Importing   int `json:"importing"`
	} `json:"summary"`
}

func (s *Server) activeDownloads(ctx context.Context, _ *toolCall) (*toolOutput, error) {
	if s.d.Downloads == nil {
		return nil, userErrorf("No download client is set up on this Marquee.")
	}
	list, total, err := s.d.Downloads.List(ctx, downloads.ListFilter{State: downloads.FilterActive, Limit: downloadsShown})
	if err != nil {
		return nil, err
	}
	sum, err := s.d.Downloads.Summary(ctx)
	if err != nil {
		return nil, err
	}
	out := downloadsOut{Count: total, Downloads: []downloadOut{}}
	out.Summary.Downloading, out.Summary.Queued, out.Summary.Importing = sum.Downloading, sum.Queued, sum.Importing
	for _, v := range list {
		name := "(a title that was removed)"
		if v.Title != nil {
			name = v.Title.Name + yearSuffix(v.Title.Year)
		}
		d := downloadOut{
			ID: v.ID, Title: name, Kind: v.Kind, Quality: v.Quality, State: v.State,
			ProgressPercent: math.Round(v.Progress*1000) / 10, SpeedBytesPerSec: v.SpeedBps, SizeBytes: v.Size,
			ETASeconds: v.ETASeconds, ReleaseTitle: shorten(v.ReleaseTitle, 160),
		}
		out.Downloads = append(out.Downloads, d)
	}

	var b strings.Builder
	if total == 0 {
		b.WriteString("Nothing is downloading right now.")
	} else {
		fmt.Fprintf(&b, "%s in progress", plural(total, "download", "downloads"))
		if total > len(out.Downloads) {
			fmt.Fprintf(&b, " (showing %d)", len(out.Downloads))
		}
		b.WriteString(":")
		for _, d := range out.Downloads {
			fmt.Fprintf(&b, "\n- %s", d.Title)
			if d.Quality != "" {
				b.WriteString(" " + qualityWord(d.Quality))
			}
			fmt.Fprintf(&b, ": %s, %.1f%%", d.State, d.ProgressPercent)
			if d.SpeedBytesPerSec > 0 {
				fmt.Fprintf(&b, " at %s/s", humanBytes(d.SpeedBytesPerSec))
			}
			if d.ETASeconds != nil && *d.ETASeconds >= 0 {
				fmt.Fprintf(&b, ", about %s left", humanETA(*d.ETASeconds))
			}
		}
	}
	return &toolOutput{Text: b.String(), Data: out}, nil
}

// ---- health -----------------------------------------------------------------------

type healthOut struct {
	Status        string `json:"status"`
	Version       string `json:"version"`
	UptimeSeconds int64  `json:"uptimeSeconds"`
	Database      struct {
		OK bool `json:"ok"`
	} `json:"database"`
	// Present for the owner and admins only.
	Clients      any `json:"clients,omitempty"`
	Sources      any `json:"sources,omitempty"`
	MediaServers any `json:"mediaServers,omitempty"`
}

func (s *Server) health(ctx context.Context, c *toolCall) (*toolOutput, error) {
	pctx, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()
	out := healthOut{
		Status: "ok", Version: s.d.Version,
		UptimeSeconds: int64(max(s.now().Sub(s.d.Started), 0) / time.Second),
	}
	out.Database.OK = s.store.Ping(pctx) == nil
	if !out.Database.OK {
		out.Status = "degraded"
	}

	var b strings.Builder
	fmt.Fprintf(&b, "Marquee %s has been up for %s; database %s.", s.d.Version, humanETA(out.UptimeSeconds), okWord(out.Database.OK))

	if isStaff(c.P) {
		clients, sources, media := []Component{}, []Component{}, []Component{}
		if s.d.Downloads != nil {
			sum, err := s.d.Downloads.Summary(ctx)
			if err != nil {
				return nil, err
			}
			for _, cl := range sum.Clients {
				clients = append(clients, Component{Name: cl.Name, Kind: cl.Kind, OK: cl.OK, Detail: shorten(cl.LastError, 200)})
			}
		}
		if s.d.Sources != nil {
			list, err := s.d.Sources.List(ctx)
			if err != nil {
				return nil, err
			}
			for _, src := range list {
				if !src.Enabled {
					continue
				}
				comp := Component{Name: src.Name, Kind: src.Kind, OK: src.LastError == "" && src.LastOKAt != nil}
				switch {
				case src.LastError != "":
					comp.Detail = shorten(src.LastError, 200)
				case src.LastOKAt == nil:
					comp.Detail = "not checked yet"
				}
				sources = append(sources, comp)
			}
		}
		if s.d.MediaServers != nil {
			for _, m := range s.d.MediaServers(ctx) {
				m.Detail = shorten(m.Detail, 200)
				media = append(media, m)
			}
		}
		out.Clients, out.Sources, out.MediaServers = clients, sources, media
		componentLine(&b, "Download clients", clients)
		componentLine(&b, "Sources", sources)
		componentLine(&b, "Media servers", media)
	}
	return &toolOutput{Text: b.String(), Data: out}, nil
}

func okWord(ok bool) string {
	if ok {
		return "ok"
	}
	return "not answering"
}

func componentLine(b *strings.Builder, label string, list []Component) {
	if len(list) == 0 {
		fmt.Fprintf(b, "\n%s: none connected.", label)
		return
	}
	parts := make([]string, len(list))
	for i, c := range list {
		parts[i] = c.Name + " (" + okWord(c.OK)
		if !c.OK && c.Detail != "" {
			parts[i] += ": " + c.Detail
		}
		parts[i] += ")"
	}
	fmt.Fprintf(b, "\n%s: %s.", label, strings.Join(parts, ", "))
}
