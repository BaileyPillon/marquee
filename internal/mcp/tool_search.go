package mcp

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"github.com/BaileyPillon/marquee/internal/metadata"
	"github.com/BaileyPillon/marquee/internal/titles"
)

const (
	defaultSearchLimit = 8
	maxSearchLimit     = 20
	maxQueryRunes      = 200
	lookupTimeout      = 25 * time.Second
	overviewRunes      = 240
)

type searchHit struct {
	Kind       string `json:"kind"`
	Name       string `json:"name"`
	Year       int    `json:"year"`
	InLibrary  bool   `json:"inLibrary"`
	TitleID    int64  `json:"titleId,omitempty"`
	OnDisk     bool   `json:"onDisk"`
	Provider   string `json:"provider,omitempty"`
	ProviderID string `json:"providerId,omitempty"`
	Overview   string `json:"overview,omitempty"`
	Detail     string `json:"detail,omitempty"`
	Source     string `json:"source"`
}

type searchOut struct {
	Query    string      `json:"query"`
	Results  []searchHit `json:"results"`
	Warnings []string    `json:"warnings"`
}

// shorten cuts text to n runes.
func shorten(s string, n int) string {
	s = strings.Join(strings.Fields(s), " ")
	if r := []rune(s); len(r) > n {
		return string(r[:n-1]) + "…"
	}
	return s
}

// providerIDOf is a catalog title's id at its provider.
func providerIDOf(t titles.Title) (provider, id string) {
	switch t.Kind {
	case titles.KindMovie:
		if t.TMDBID != 0 {
			return metadata.ProviderTMDB, fmt.Sprint(t.TMDBID)
		}
	case titles.KindShow:
		if t.TVmazeID != 0 {
			return metadata.ProviderTVmaze, fmt.Sprint(t.TVmazeID)
		}
	case titles.KindArtist:
		if t.MBID != "" {
			return metadata.ProviderMusicBrainz, t.MBID
		}
	}
	return "", ""
}

func (s *Server) searchTitles(ctx context.Context, c *toolCall) (*toolOutput, error) {
	var a struct {
		Query string  `json:"query"`
		Kind  string  `json:"kind"`
		Limit flexInt `json:"limit"`
	}
	if err := decodeArgs(c.Args, &a); err != nil {
		return nil, err
	}
	query := strings.TrimSpace(a.Query)
	switch {
	case query == "":
		return nil, userErrorf(`Give a name to search for in "query".`)
	case utf8.RuneCountInString(query) > maxQueryRunes:
		return nil, userErrorf("That search is too long (at most %d characters).", maxQueryRunes)
	}
	var kinds []string
	switch k := strings.ToLower(strings.TrimSpace(a.Kind)); k {
	case "", "any":
		kinds = []string{metadata.KindMovie, metadata.KindShow, metadata.KindArtist}
	case metadata.KindMovie, metadata.KindShow, metadata.KindArtist:
		kinds = []string{k}
	default:
		return nil, userErrorf(`"kind" must be movie, show, artist or any.`)
	}
	limit := defaultSearchLimit
	if a.Limit != 0 {
		if a.Limit < 1 || a.Limit > maxSearchLimit {
			return nil, userErrorf(`"limit" must be a whole number from 1 to %d.`, maxSearchLimit)
		}
		limit = int(a.Limit)
	}

	// What Marquee already tracks (a plain name search of the catalog).
	filter := titles.ListFilter{Q: query, Limit: limit, Sort: titles.SortName}
	if len(kinds) == 1 {
		filter.Kind = kinds[0]
	}
	cat, err := s.d.Titles.List(ctx, filter)
	if err != nil {
		return nil, err
	}

	// Lookups at the providers, one kind each, at the same time. One that fails
	// (no TMDB key yet, a provider down) is reported and does not hide the rest.
	lctx, cancel := context.WithTimeout(ctx, lookupTimeout)
	defer cancel()
	var (
		mu       sync.Mutex
		wg       sync.WaitGroup
		lookups  = make(map[string][]titles.LookupResult, len(kinds))
		failures = make(map[string]string, len(kinds))
	)
	for _, k := range kinds {
		wg.Add(1)
		go func() {
			defer wg.Done()
			res, err := s.d.Titles.Lookup(lctx, k, query)
			mu.Lock()
			defer mu.Unlock()
			if err != nil {
				failures[k] = s.lookupFailure(k, err)
				return
			}
			lookups[k] = res
		}()
	}
	wg.Wait()

	out := searchOut{Query: query, Results: []searchHit{}, Warnings: []string{}}
	seen := map[int64]bool{}
	for _, t := range cat.Items {
		if len(out.Results) >= limit {
			break
		}
		provider, pid := providerIDOf(t)
		out.Results = append(out.Results, searchHit{
			Kind: t.Kind, Name: t.Name, Year: t.Year, InLibrary: true, TitleID: t.ID,
			Provider: provider, ProviderID: pid, Overview: shorten(t.Overview, overviewRunes), Source: "catalog",
		})
		seen[t.ID] = true
	}
	// Provider hits take turns by kind, so one kind cannot crowd out the others.
	for i := 0; len(out.Results) < limit; i++ {
		more := false
		for _, k := range kinds {
			list := lookups[k]
			if i >= len(list) {
				continue
			}
			more = true
			h := list[i]
			if h.InLibrary && seen[h.TitleID] {
				continue
			}
			if len(out.Results) >= limit {
				break
			}
			out.Results = append(out.Results, searchHit{
				Kind: h.Kind, Name: h.Name, Year: h.Year, InLibrary: h.InLibrary, TitleID: h.TitleID,
				Provider: h.Provider, ProviderID: h.ProviderID, Overview: shorten(h.Overview, overviewRunes),
				Detail: shorten(h.Detail, 80), Source: "provider",
			})
			if h.InLibrary {
				seen[h.TitleID] = true
			}
		}
		if !more {
			break
		}
	}
	for _, k := range kinds {
		if msg := failures[k]; msg != "" {
			out.Warnings = append(out.Warnings, msg)
		}
	}

	var ids []int64
	for _, h := range out.Results {
		if h.InLibrary {
			ids = append(ids, h.TitleID)
		}
	}
	onDisk, err := s.store.OnDisk(ctx, ids)
	if err != nil {
		return nil, err
	}
	for i := range out.Results {
		out.Results[i].OnDisk = onDisk[out.Results[i].TitleID]
	}

	return &toolOutput{Text: searchText(out), Data: out}, nil
}

// lookupFailure says in plain words why one kind could not be searched.
func (s *Server) lookupFailure(kind string, err error) string {
	label := map[string]string{"movie": "Movies", "show": "Shows", "artist": "Artists"}[kind]
	var te *titles.InvalidError
	switch {
	case metadata.CodeOf(err) != "":
		return label + ": " + metadata.MessageOf(err)
	case errors.As(err, &te):
		return label + ": " + te.Message
	case errors.Is(err, context.DeadlineExceeded):
		return label + ": the provider took too long to answer."
	}
	s.log.Warn("mcp: provider lookup failed", "kind", kind, "err", err)
	return label + ": the search failed."
}

func searchText(o searchOut) string {
	var b strings.Builder
	if len(o.Results) == 0 {
		fmt.Fprintf(&b, "No matches for %q.", o.Query)
	} else {
		fmt.Fprintf(&b, "%s for %q:", plural(len(o.Results), "match", "matches"), o.Query)
		for _, h := range o.Results {
			fmt.Fprintf(&b, "\n- %s%s, %s: ", h.Name, yearSuffix(h.Year), h.Kind)
			switch {
			case h.InLibrary && h.OnDisk:
				fmt.Fprintf(&b, "in the library (titleId %d), files on disk", h.TitleID)
			case h.InLibrary:
				fmt.Fprintf(&b, "tracked by Marquee (titleId %d), nothing on disk yet", h.TitleID)
			default:
				fmt.Fprintf(&b, "not in the library (%s id %s)", h.Provider, h.ProviderID)
			}
		}
	}
	for _, w := range o.Warnings {
		b.WriteString("\n! " + w)
	}
	return b.String()
}
