package metadata

import (
	"context"
	"encoding/json"
	"fmt"
	"net/url"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/BaileyPillon/marquee/internal/core/outbound"
)

// DefaultMusicBrainzURL is the MusicBrainz web service. It needs no key but
// allows one request per second, and wants a User-Agent with contact details
// (the outbound client's "Marquee/<version> (+<project url>)" has both).
const DefaultMusicBrainzURL = "https://musicbrainz.org"

const (
	mbPageSize  = 100
	mbMaxPages  = 5
	mbMaxAlias  = 10
	mbSearchMax = 10
	// mbTrackAlbumsPerCall bounds one Artist call to about 30 seconds of
	// track lookups; the scheduled refresh reads the next batch.
	mbTrackAlbumsPerCall = 15
	// An album's track list almost never changes, so it is kept longer and
	// is not refetched by a refresh.
	trackTTL = 14 * 24 * time.Hour
)

var mbidRe = regexp.MustCompile(`^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$`)

// IsMBID reports whether s looks like a MusicBrainz id.
func IsMBID(s string) bool { return mbidRe.MatchString(strings.ToLower(s)) }

// Secondary types that make a release group something other than an album
// someone would want to collect. Soundtracks stay: for a composer they are the
// work.
var mbExcludedSecondary = map[string]bool{
	"live": true, "compilation": true, "remix": true, "dj-mix": true, "demo": true,
	"interview": true, "spokenword": true, "audiobook": true, "audio drama": true,
	"field recording": true, "mixtape/street": true,
}

// MusicBrainz looks up artists, their albums and track lists.
type MusicBrainz struct {
	g     getter
	cache *Cache
	base  string
	// trackBudget is how many albums not yet in the cache one Artist call reads
	// track lists for. At one request a second and two requests an album, an
	// artist like Chopin (over 500 release groups) would otherwise keep the
	// first Add busy for a quarter of an hour.
	trackBudget int
}

func newMusicBrainz(hc *outbound.Client, cache *Cache, rawBase string) (*MusicBrainz, error) {
	u, err := baseURL(rawBase)
	if err != nil {
		return nil, err
	}
	hc.SetHostLimit(u.Host, 1, 1) // the published limit: one request a second
	return &MusicBrainz{g: getter{hc: hc, provider: ProviderMusicBrainz}, cache: cache, base: u.String(), trackBudget: mbTrackAlbumsPerCall}, nil
}

// ---- MusicBrainz JSON ------------------------------------------------------

type mbLifeSpan struct {
	Begin string `json:"begin"`
	Ended bool   `json:"ended"`
}

type mbArtist struct {
	ID             string     `json:"id"`
	Name           string     `json:"name"`
	SortName       string     `json:"sort-name"`
	Type           string     `json:"type"`
	Country        string     `json:"country"`
	Disambiguation string     `json:"disambiguation"`
	LifeSpan       mbLifeSpan `json:"life-span"`
	Genres         []struct {
		Name  string `json:"name"`
		Count int    `json:"count"`
	} `json:"genres"`
	Aliases []struct {
		Name string `json:"name"`
	} `json:"aliases"`
}

type mbReleaseGroup struct {
	ID               string   `json:"id"`
	Title            string   `json:"title"`
	PrimaryType      string   `json:"primary-type"`
	SecondaryTypes   []string `json:"secondary-types"`
	FirstReleaseDate string   `json:"first-release-date"`
}

type mbRelease struct {
	ID     string `json:"id"`
	Title  string `json:"title"`
	Status string `json:"status"`
	Date   string `json:"date"`
	Media  []struct {
		Position int `json:"position"`
		Tracks   []struct {
			Position  int    `json:"position"`
			Title     string `json:"title"`
			Length    *int   `json:"length"`
			Recording struct {
				ID     string `json:"id"`
				Length *int   `json:"length"`
			} `json:"recording"`
		} `json:"tracks"`
	} `json:"media"`
}

// ---- ArtistProvider --------------------------------------------------------

// SearchArtists searches by name.
func (m *MusicBrainz) SearchArtists(ctx context.Context, query string) ([]SearchHit, error) {
	q := strings.TrimSpace(query)
	if q == "" {
		return nil, nil
	}
	body, err := m.cache.fetch(ctx, ProviderMusicBrainz, "artist-search:"+strings.ToLower(q), SearchTTL, false,
		func(ctx context.Context) ([]byte, error) {
			return m.g.get(ctx, m.base+"/ws/2/artist?query="+url.QueryEscape(q)+"&fmt=json&limit="+strconv.Itoa(mbSearchMax), nil)
		})
	if err != nil {
		return nil, err
	}
	var raw struct {
		Artists []mbArtist `json:"artists"`
	}
	if err := json.Unmarshal(body, &raw); err != nil {
		return nil, upstream(ProviderMusicBrainz, fmt.Errorf("unreadable search answer: %w", err))
	}
	hits := make([]SearchHit, 0, len(raw.Artists))
	for _, a := range raw.Artists {
		var detail []string
		if a.Type != "" {
			detail = append(detail, a.Type)
		}
		if a.Country != "" {
			detail = append(detail, a.Country)
		}
		hits = append(hits, SearchHit{
			Kind: KindArtist, Provider: ProviderMusicBrainz, ProviderID: a.ID,
			Name: cleanText(a.Name), Year: year(a.LifeSpan.Begin),
			Overview: cleanText(a.Disambiguation),
			Detail:   strings.Join(detail, " · "),
		})
	}
	return hits, nil
}

// Artist returns an artist with their albums and EPs, newest first, and the
// track list of each. MusicBrainz allows one request a second, so reading an
// album takes two seconds; one call reads at most mbTrackAlbumsPerCall albums
// that are not already cached, and returns the rest without tracks. Every
// request is cached as it finishes, so a retry after a failure, or the next
// refresh, carries on where the last call stopped.
func (m *MusicBrainz) Artist(ctx context.Context, id string) (*ArtistInfo, error) {
	id = strings.ToLower(strings.TrimSpace(id))
	if !IsMBID(id) {
		return nil, notFound(ProviderMusicBrainz)
	}
	body, err := m.cache.fetch(ctx, ProviderMusicBrainz, "artist:"+id, DetailsTTL, false,
		func(ctx context.Context) ([]byte, error) {
			return m.g.get(ctx, m.base+"/ws/2/artist/"+id+"?inc=genres+aliases&fmt=json", nil)
		})
	if err != nil {
		return nil, err
	}
	var a mbArtist
	if err := json.Unmarshal(body, &a); err != nil {
		return nil, upstream(ProviderMusicBrainz, fmt.Errorf("unreadable artist answer: %w", err))
	}
	info := &ArtistInfo{
		MBID: a.ID, Name: cleanText(a.Name), SortName: cleanText(a.SortName),
		Type: a.Type, Country: a.Country, Disambiguation: cleanText(a.Disambiguation),
		Year: year(a.LifeSpan.Begin), Ended: a.LifeSpan.Ended,
		Genres: []string{}, AltNames: []string{}, Albums: []AlbumInfo{},
	}
	if info.MBID == "" {
		info.MBID = id
	}
	for _, g := range a.Genres {
		if g.Name != "" {
			info.Genres = append(info.Genres, g.Name)
		}
	}
	seen := map[string]bool{strings.ToLower(info.Name): true}
	for _, al := range a.Aliases {
		n := cleanText(al.Name)
		if n == "" || seen[strings.ToLower(n)] || len(info.AltNames) >= mbMaxAlias {
			continue
		}
		seen[strings.ToLower(n)] = true
		info.AltNames = append(info.AltNames, n)
	}

	groups, err := m.releaseGroups(ctx, info.MBID)
	if err != nil {
		return nil, err
	}
	// Newest first: when there are too many albums to read in one go, these
	// are the ones that matter.
	sort.SliceStable(groups, func(i, j int) bool { return groups[i].FirstReleaseDate > groups[j].FirstReleaseDate })
	budget := m.trackBudget
	spend := func() bool {
		if budget <= 0 {
			return false
		}
		budget--
		return true
	}
	for _, rg := range groups {
		album := AlbumInfo{
			MBID: rg.ID, Title: cleanText(rg.Title),
			Year: year(rg.FirstReleaseDate), ReleaseDate: rg.FirstReleaseDate,
			Type:   strings.ToLower(rg.PrimaryType),
			Cover:  Image{Ref: "caa/release-group/" + rg.ID + "/front-500", Kind: "cover"},
			Tracks: []TrackInfo{},
		}
		if err := m.fillTracks(ctx, &album, spend); err != nil {
			return nil, err
		}
		info.Albums = append(info.Albums, album)
	}
	return info, nil
}

// releaseGroups lists an artist's albums and EPs, leaving out live albums,
// compilations and the like.
func (m *MusicBrainz) releaseGroups(ctx context.Context, artist string) ([]mbReleaseGroup, error) {
	var out []mbReleaseGroup
	for page := 0; page < mbMaxPages; page++ {
		offset := page * mbPageSize
		body, err := m.cache.fetch(ctx, ProviderMusicBrainz, "release-groups:"+artist+":"+strconv.Itoa(offset), DetailsTTL, false,
			func(ctx context.Context) ([]byte, error) {
				return m.g.get(ctx, fmt.Sprintf("%s/ws/2/release-group?artist=%s&type=album%%7Cep&fmt=json&limit=%d&offset=%d",
					m.base, artist, mbPageSize, offset), nil)
			})
		if err != nil {
			return nil, err
		}
		var raw struct {
			Count  int              `json:"release-group-count"`
			Groups []mbReleaseGroup `json:"release-groups"`
		}
		if err := json.Unmarshal(body, &raw); err != nil {
			return nil, upstream(ProviderMusicBrainz, fmt.Errorf("unreadable release-group answer: %w", err))
		}
		for _, g := range raw.Groups {
			if keepReleaseGroup(g) {
				out = append(out, g)
			}
		}
		if offset+len(raw.Groups) >= raw.Count || len(raw.Groups) == 0 {
			break
		}
	}
	return out, nil
}

func keepReleaseGroup(g mbReleaseGroup) bool {
	if g.ID == "" || g.Title == "" {
		return false
	}
	switch strings.ToLower(g.PrimaryType) {
	case "album", "ep":
	default:
		return false
	}
	for _, s := range g.SecondaryTypes {
		if mbExcludedSecondary[strings.ToLower(s)] {
			return false
		}
	}
	return true
}

// fillTracks finds the earliest official release of the album and copies its
// track list. Reading an album that is not in the cache costs one unit of
// spend (which says whether there is any left); with none left the album is
// returned without tracks, and a later refresh picks it up.
func (m *MusicBrainz) fillTracks(ctx context.Context, album *AlbumInfo, spend func() bool) error {
	paid := false
	allowed := func(key string) bool {
		if m.cache.fresh(ctx, ProviderMusicBrainz, key) || paid {
			return true
		}
		paid = spend()
		return paid
	}
	if !allowed("releases:" + album.MBID) {
		return nil
	}
	body, err := m.cache.fetch(ctx, ProviderMusicBrainz, "releases:"+album.MBID, trackTTL, true,
		func(ctx context.Context) ([]byte, error) {
			return m.g.get(ctx, m.base+"/ws/2/release?release-group="+album.MBID+"&fmt=json&limit="+strconv.Itoa(mbPageSize), nil)
		})
	if err != nil {
		return skipMissing(err)
	}
	var list struct {
		Releases []mbRelease `json:"releases"`
	}
	if err := json.Unmarshal(body, &list); err != nil {
		return upstream(ProviderMusicBrainz, fmt.Errorf("unreadable release list: %w", err))
	}
	pick := earliestOfficial(list.Releases)
	if pick == nil {
		return nil
	}
	album.ReleaseID = pick.ID
	if album.Year == 0 {
		album.Year, album.ReleaseDate = year(pick.Date), pick.Date
	}

	if !allowed("release:" + pick.ID) {
		return nil
	}
	body, err = m.cache.fetch(ctx, ProviderMusicBrainz, "release:"+pick.ID, trackTTL, true,
		func(ctx context.Context) ([]byte, error) {
			return m.g.get(ctx, m.base+"/ws/2/release/"+pick.ID+"?inc=recordings&fmt=json", nil)
		})
	if err != nil {
		return skipMissing(err)
	}
	var rel mbRelease
	if err := json.Unmarshal(body, &rel); err != nil {
		return upstream(ProviderMusicBrainz, fmt.Errorf("unreadable release answer: %w", err))
	}
	for mi, medium := range rel.Media {
		disc := medium.Position
		if disc <= 0 {
			disc = mi + 1
		}
		for ti, t := range medium.Tracks {
			pos := t.Position
			if pos <= 0 {
				pos = ti + 1
			}
			length := 0
			switch {
			case t.Length != nil:
				length = *t.Length
			case t.Recording.Length != nil:
				length = *t.Recording.Length
			}
			album.Tracks = append(album.Tracks, TrackInfo{
				Disc: disc, Position: pos, Title: cleanText(t.Title), LengthMS: length, MBID: t.Recording.ID,
			})
		}
	}
	return nil
}

// skipMissing turns "MusicBrainz has no such release" into "no tracks" and
// keeps every other failure.
func skipMissing(err error) error {
	if CodeOf(err) == ErrNotFound.Code {
		return nil
	}
	return err
}

// earliestOfficial picks the release with the earliest date among the
// official ones; with none official, among all; undated releases come last.
func earliestOfficial(rs []mbRelease) *mbRelease {
	var best *mbRelease
	better := func(a, b *mbRelease) bool { // is a better than b?
		ao, bo := strings.EqualFold(a.Status, "official"), strings.EqualFold(b.Status, "official")
		if ao != bo {
			return ao
		}
		if (a.Date == "") != (b.Date == "") {
			return a.Date != ""
		}
		return a.Date < b.Date // ISO dates, partial ones included, sort as text
	}
	for i := range rs {
		if rs[i].ID == "" {
			continue
		}
		if best == nil || better(&rs[i], best) {
			best = &rs[i]
		}
	}
	return best
}
