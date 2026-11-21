package mediaservers

import (
	"context"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"strings"

	"github.com/BaileyPillon/marquee/internal/core/outbound"
	"github.com/BaileyPillon/marquee/internal/core/pathmap"
	"github.com/BaileyPillon/marquee/internal/parse"
	"github.com/BaileyPillon/marquee/internal/titles"
)

// jellyfin talks to Jellyfin and Emby, whose APIs are close cousins.
type jellyfin struct {
	c    *caller
	kind string
}

var _ adapter = (*jellyfin)(nil)

func newJellyfin(hc *outbound.Client, kind, base, token string) *jellyfin {
	return &jellyfin{kind: kind, c: &caller{hc: hc, base: base, headers: map[string]string{
		// The two spellings of the same token: old and new servers each read one.
		"X-Emby-Token":  token,
		"Authorization": `MediaBrowser Token="` + token + `"`,
		"Accept":        "application/json",
	}}}
}

func (j *jellyfin) who() string { return displayKind(j.kind) }

type jfInfo struct {
	Version    string `json:"Version"`
	ServerName string `json:"ServerName"`
	ID         string `json:"Id"`
}

func (j *jellyfin) info(ctx context.Context) (jfInfo, error) {
	rep, err := j.c.do(ctx, http.MethodGet, "/System/Info", nil, nil, outbound.WithTimeout(callTimeout))
	if err != nil {
		return jfInfo{}, err
	}
	if !rep.ok() {
		return jfInfo{}, rep.err(j.who())
	}
	var in jfInfo
	if err := rep.json(j.who(), &in); err != nil {
		return jfInfo{}, err
	}
	return in, nil
}

func (j *jellyfin) test(ctx context.Context) (string, []Library, error) {
	in, err := j.info(ctx)
	if err != nil {
		return "", nil, err
	}
	libs, err := j.libraries(ctx)
	if err != nil {
		return in.Version, nil, err
	}
	return in.Version, libs, nil
}

func (j *jellyfin) libraries(ctx context.Context) ([]Library, error) {
	rep, err := j.c.do(ctx, http.MethodGet, "/Library/VirtualFolders", nil, nil, outbound.WithTimeout(callTimeout))
	if err != nil {
		return nil, err
	}
	if !rep.ok() {
		return nil, rep.err(j.who())
	}
	var folders []struct {
		Name      string   `json:"Name"`
		ItemID    string   `json:"ItemId"`
		Locations []string `json:"Locations"`
	}
	if err := rep.json(j.who(), &folders); err != nil {
		return nil, err
	}
	out := make([]Library, 0, len(folders))
	for _, f := range folders {
		paths := f.Locations
		if paths == nil {
			paths = []string{}
		}
		out = append(out, Library{ID: f.ItemID, Name: f.Name, Paths: paths})
	}
	return out, nil
}

// notify tells the server which files changed, in two steps.
//
// First the precise update: POST /Library/Media/Updated with each path. If the
// server will not take it, everything is rescanned instead (POST
// /Library/Refresh) and that is all.
//
// Then the libraries that hold the paths are asked to rescan themselves. This
// second step is not a luxury: a server answers "204" to a Media/Updated for a
// file in a folder it has never seen, and then does nothing, because it looks
// for the nearest item it already knows and a brand new film's folder has none.
// Found live on Jellyfin 12.4: the film showed up only after the library was
// refreshed. A library refresh is the same call the web client's "Scan library"
// button makes, and only touches the libraries that matter.
func (j *jellyfin) notify(ctx context.Context, ev Event) error {
	typ := "Created"
	if ev.Kind == EventDeleted {
		typ = "Deleted"
	}
	updates := make([]map[string]string, 0, len(ev.Paths))
	for _, p := range ev.Paths {
		updates = append(updates, map[string]string{"Path": p, "UpdateType": typ})
	}
	if len(updates) > 0 {
		rep, err := j.c.do(ctx, http.MethodPost, "/Library/Media/Updated", nil,
			map[string]any{"Updates": updates}, outbound.WithTimeout(callTimeout))
		if err != nil {
			return err
		}
		if rep.ok() {
			return j.refreshLibrariesHolding(ctx, ev.Paths)
		}
		if rep.status == http.StatusUnauthorized || rep.status == http.StatusForbidden {
			return rep.err(j.who())
		}
	}
	return j.fullRefresh(ctx)
}

// fullRefresh asks the server to rescan every library.
func (j *jellyfin) fullRefresh(ctx context.Context) error {
	rep, err := j.c.do(ctx, http.MethodPost, "/Library/Refresh", nil, nil, outbound.WithTimeout(callTimeout))
	if err != nil {
		return err
	}
	if !rep.ok() {
		return rep.err(j.who())
	}
	return nil
}

// refreshLibrariesHolding asks each library whose folders hold one of the paths
// to rescan. When the libraries cannot be listed, everything is rescanned; when
// none holds any path, everything is rescanned too, and the error says the
// server's path rules are probably wrong.
func (j *jellyfin) refreshLibrariesHolding(ctx context.Context, paths []string) error {
	if len(paths) == 0 {
		return nil
	}
	libs, err := j.libraries(ctx)
	if err != nil {
		return j.fullRefresh(ctx)
	}
	var ids []string
	seen := map[string]bool{}
	for _, l := range libs {
		for _, loc := range l.Paths {
			for _, p := range paths {
				if l.ID != "" && !seen[l.ID] && pathmap.Within(loc, p) {
					seen[l.ID] = true
					ids = append(ids, l.ID)
				}
			}
		}
	}
	if len(ids) == 0 {
		if err := j.fullRefresh(ctx); err != nil {
			return err
		}
		return fmt.Errorf("none of the server's libraries holds %s: check this server's path rules", paths[0])
	}
	q := url.Values{}
	q.Set("Recursive", "true")
	q.Set("ImageRefreshMode", "Default")
	q.Set("MetadataRefreshMode", "Default")
	q.Set("ReplaceAllImages", "false")
	q.Set("ReplaceAllMetadata", "false")
	var first error
	for _, id := range ids {
		rep, err := j.c.do(ctx, http.MethodPost, "/Items/"+url.PathEscape(id)+"/Refresh", q, nil, outbound.WithTimeout(callTimeout))
		if err == nil && !rep.ok() {
			err = rep.err(j.who())
		}
		if err != nil && first == nil {
			first = err
		}
	}
	return first
}

type jfItem struct {
	ID             string            `json:"Id"`
	Name           string            `json:"Name"`
	Type           string            `json:"Type"`
	ProductionYear int               `json:"ProductionYear"`
	ProviderIDs    map[string]string `json:"ProviderIds"`
}

func jfTypeFor(kind string) string {
	switch kind {
	case titles.KindShow:
		return "Series"
	case titles.KindArtist:
		return "MusicArtist"
	}
	return "Movie"
}

// providerMatch reports whether an item carries one of the title's ids. Keys
// compare without case ("Tmdb", "TvMaze", "MusicBrainzArtist").
func (it jfItem) providerMatch(q titleQuery) bool {
	for k, v := range it.ProviderIDs {
		v = strings.TrimSpace(strings.ToLower(v))
		if v == "" {
			continue
		}
		switch strings.ToLower(k) {
		case "tmdb":
			if q.TMDB > 0 && v == strconv.FormatInt(q.TMDB, 10) {
				return true
			}
		case "tvmaze":
			if q.TVmaze > 0 && v == strconv.FormatInt(q.TVmaze, 10) {
				return true
			}
		case "imdb":
			if q.IMDb != "" && v == q.IMDb {
				return true
			}
		case "musicbrainzartist":
			if q.MBID != "" && v == q.MBID {
				return true
			}
		}
	}
	return false
}

func (j *jellyfin) open(ctx context.Context, q titleQuery) (string, bool, error) {
	query := url.Values{}
	query.Set("Recursive", "true")
	query.Set("IncludeItemTypes", "Movie,Series,MusicArtist")
	query.Set("searchTerm", q.Name)
	query.Set("Fields", "ProviderIds,ProductionYear,Path")
	rep, err := j.c.do(ctx, http.MethodGet, "/Items", query, nil, outbound.WithTimeout(openTimeout))
	if err != nil {
		return "", false, err
	}
	if !rep.ok() {
		return "", false, rep.err(j.who())
	}
	var res struct {
		Items []jfItem `json:"Items"`
	}
	if err := rep.json(j.who(), &res); err != nil {
		return "", false, err
	}
	want := jfTypeFor(q.Kind)
	var (
		byName []jfItem
		hit    *jfItem
	)
	for i := range res.Items {
		it := res.Items[i]
		if it.Type != "" && it.Type != want {
			continue
		}
		if it.providerMatch(q) {
			hit = &it
			break
		}
		if nameMatches(q, it.Name, it.ProductionYear) {
			byName = append(byName, it)
		}
	}
	if hit == nil && len(byName) > 0 {
		// Several items with the same name and year are the same film twice;
		// the first one will do.
		hit = &byName[0]
	}
	if hit == nil || hit.ID == "" {
		return "", false, nil
	}
	if j.kind == KindEmby {
		in, err := j.info(ctx)
		if err != nil {
			return "", false, err
		}
		return j.c.base + "/web/index.html#!/item?id=" + url.QueryEscape(hit.ID) + "&serverId=" + url.QueryEscape(in.ID), true, nil
	}
	return j.c.base + "/web/#/details?id=" + url.QueryEscape(hit.ID), true, nil
}

// nameMatches is the name-and-year match: the normalized name equals the title's
// name or an alternative one, and the year is within one (a year either side
// does not know is not held against it).
func nameMatches(q titleQuery, name string, year int) bool {
	n := parse.Normalize(name)
	if n == "" {
		return false
	}
	found := false
	for _, c := range q.Names {
		if parse.Normalize(c) == n {
			found = true
			break
		}
	}
	if !found {
		return false
	}
	if q.Year > 0 && year > 0 {
		d := q.Year - year
		if d < -1 || d > 1 {
			return false
		}
	}
	return true
}
