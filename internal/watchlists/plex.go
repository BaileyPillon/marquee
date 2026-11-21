package watchlists

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"

	"github.com/BaileyPillon/marquee/internal/core/outbound"
)

// The Plex endpoints this package uses. NONE of them has been run against the
// real service by Marquee's tests, which use fakes (see plex_test.go); they
// follow Plex's public documentation and what the community has written down,
// and the client reads every answer leniently.
//
//	POST https://plex.tv/api/v2/pins?strong=true      start a link (PIN flow)
//	GET  https://plex.tv/api/v2/pins/{id}?code=...    poll it; authToken appears once the owner agrees
//	GET  https://plex.tv/api/v2/user                  who the token belongs to
//	GET  https://discover.provider.plex.tv/library/sections/watchlist/all?includeGuids=1
//	                                                  the owner's watchlist, paged with X-Plex-Container-Start/Size
//	POST https://community.plex.tv/api                GraphQL: the owner's friends, and a friend's watchlist
//	GET  https://discover.provider.plex.tv/library/metadata/{ratingKey}?includeGuids=1
//	                                                  the external ids (tmdb, imdb, tvdb) of one item
//
// Every call carries X-Plex-Product: Marquee and X-Plex-Client-Identifier, and
// once there is a token, X-Plex-Token (in the query too, where the discover
// service documents it; the outbound client never logs a query string).
const (
	DefaultPlexURL      = "https://plex.tv"
	DefaultDiscoverURL  = "https://discover.provider.plex.tv"
	DefaultCommunityURL = "https://community.plex.tv/api"

	product  = "Marquee"
	pageSize = 50
	maxPages = 100
	// maxReply caps one Plex answer. A watchlist page is tens of kilobytes.
	maxReply = 8 << 20
)

// Auth is who Marquee is to Plex: a stable client identifier and, once linked,
// the owner's token.
type Auth struct {
	ClientID string
	Token    string
}

// Pin is a link request at Plex.
type Pin struct {
	ID   int64
	Code string
}

// PlexAPI is Plex as the package uses it. PlexClient is the real one; tests use
// a client pointed at a fake server, or a fake of their own.
type PlexAPI interface {
	// CreatePIN starts a link.
	CreatePIN(ctx context.Context, a Auth) (Pin, error)
	// CheckPIN polls a link. The token is empty until the owner has agreed.
	// ErrPinExpired means Plex no longer knows the pin.
	CheckPIN(ctx context.Context, a Auth, id int64, code string) (token string, err error)
	// Account says whose token this is.
	Account(ctx context.Context, a Auth) (PlexUser, error)
	// OwnerWatchlist reads the whole watchlist, with the external ids.
	OwnerWatchlist(ctx context.Context, a Auth) ([]Item, error)
	// Friends lists the owner's Plex friends.
	Friends(ctx context.Context, a Auth) ([]PlexUser, error)
	// FriendWatchlist reads a friend's watchlist. The items have no ids yet:
	// ItemGuids finds them.
	FriendWatchlist(ctx context.Context, a Auth, friendID string) ([]Item, error)
	// ItemGuids reads the external ids of one item.
	ItemGuids(ctx context.Context, a Auth, key string) ([]string, error)
}

// PlexClient talks to Plex through the outbound client.
type PlexClient struct {
	hc           *outbound.Client
	plexURL      string
	discoverURL  string
	communityURL string
}

var _ PlexAPI = (*PlexClient)(nil)

// PlexURLs are the three Plex services. Empty fields take the real addresses;
// tests point them at a fake.
type PlexURLs struct {
	Plex, Discover, Community string
}

// NewPlexClient builds the client for the real Plex, or for the addresses given.
func NewPlexClient(hc *outbound.Client, u PlexURLs) *PlexClient {
	pick := func(v, def string) string {
		if v == "" {
			v = def
		}
		return strings.TrimRight(v, "/")
	}
	return &PlexClient{
		hc: hc, plexURL: pick(u.Plex, DefaultPlexURL),
		discoverURL: pick(u.Discover, DefaultDiscoverURL), communityURL: pick(u.Community, DefaultCommunityURL),
	}
}

// plexError is a Plex answer that was not a success. It never holds a URL.
type plexError struct {
	Op     string
	Status int
}

func (e *plexError) Error() string { return fmt.Sprintf("plex: %s answered HTTP %d", e.Op, e.Status) }

// call sends one request and returns the body of a 2xx answer. 401 and 403 are
// ErrPlexAuth, 404 is errNotFound; anything else is a plexError.
func (c *PlexClient) call(ctx context.Context, op, method, rawURL string, a Auth, body any) ([]byte, error) {
	var rd io.Reader
	if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			return nil, err
		}
		rd = bytes.NewReader(b)
	}
	req, err := http.NewRequestWithContext(ctx, method, rawURL, rd)
	if err != nil {
		return nil, errors.New("plex: could not build the " + op + " request") // the error could quote the URL, which can carry the token
	}
	req.Header.Set("Accept", "application/json")
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	req.Header.Set("X-Plex-Product", product)
	req.Header.Set("X-Plex-Client-Identifier", a.ClientID)
	if a.Token != "" {
		req.Header.Set("X-Plex-Token", a.Token)
	}
	resp, err := c.hc.Do(req)
	if err != nil {
		return nil, fmt.Errorf("plex: %s: %w", op, err) // outbound has already taken the query string out
	}
	defer resp.Body.Close()
	switch {
	case resp.StatusCode == http.StatusUnauthorized || resp.StatusCode == http.StatusForbidden:
		_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 4096))
		return nil, ErrPlexAuth
	case resp.StatusCode == http.StatusNotFound:
		_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 4096))
		return nil, errNotFound
	case resp.StatusCode < 200 || resp.StatusCode > 299:
		_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 4096))
		return nil, &plexError{Op: op, Status: resp.StatusCode}
	}
	b, err := io.ReadAll(io.LimitReader(resp.Body, maxReply+1))
	if err != nil {
		return nil, fmt.Errorf("plex: %s: reading the answer: %w", op, err)
	}
	if len(b) > maxReply {
		return nil, fmt.Errorf("plex: %s: the answer is larger than %d MB", op, maxReply>>20)
	}
	return b, nil
}

var errNotFound = errors.New("plex: not found")

// withToken adds the token to a discover-service query, where Plex documents it.
func withToken(rawURL string, a Auth, extra url.Values) string {
	u, err := url.Parse(rawURL)
	if err != nil {
		return rawURL
	}
	q := u.Query()
	for k, v := range extra {
		q[k] = v
	}
	if a.Token != "" {
		q.Set("X-Plex-Token", a.Token)
	}
	u.RawQuery = q.Encode()
	return u.String()
}

// idText reads an id that may be a JSON number or a string.
type idText string

func (i *idText) UnmarshalJSON(b []byte) error {
	var s string
	if err := json.Unmarshal(b, &s); err == nil {
		*i = idText(s)
		return nil
	}
	var n json.Number
	dec := json.NewDecoder(bytes.NewReader(b))
	dec.UseNumber()
	if err := dec.Decode(&n); err != nil {
		*i = ""
		return nil // null, or something we do not use
	}
	*i = idText(n.String())
	return nil
}

// ---- link (PIN flow) --------------------------------------------------------

type pinAnswer struct {
	ID        idText  `json:"id"`
	Code      string  `json:"code"`
	AuthToken *string `json:"authToken"`
}

func (c *PlexClient) CreatePIN(ctx context.Context, a Auth) (Pin, error) {
	b, err := c.call(ctx, "create pin", http.MethodPost, c.plexURL+"/api/v2/pins?strong=true", a, nil)
	if err != nil {
		return Pin{}, err
	}
	var p pinAnswer
	if err := json.Unmarshal(b, &p); err != nil {
		return Pin{}, fmt.Errorf("plex: unreadable pin answer: %w", err)
	}
	id, err := strconv.ParseInt(string(p.ID), 10, 64)
	if err != nil || id <= 0 || p.Code == "" {
		return Pin{}, errors.New("plex: the pin answer has no id or code")
	}
	return Pin{ID: id, Code: p.Code}, nil
}

func (c *PlexClient) CheckPIN(ctx context.Context, a Auth, id int64, code string) (string, error) {
	u := c.plexURL + "/api/v2/pins/" + strconv.FormatInt(id, 10)
	if code != "" {
		u += "?code=" + url.QueryEscape(code)
	}
	b, err := c.call(ctx, "check pin", http.MethodGet, u, a, nil)
	switch {
	case errors.Is(err, errNotFound):
		return "", ErrPinExpired
	case err != nil:
		return "", err
	}
	var p pinAnswer
	if err := json.Unmarshal(b, &p); err != nil {
		return "", fmt.Errorf("plex: unreadable pin answer: %w", err)
	}
	if p.AuthToken == nil {
		return "", nil
	}
	return strings.TrimSpace(*p.AuthToken), nil
}

func (c *PlexClient) Account(ctx context.Context, a Auth) (PlexUser, error) {
	b, err := c.call(ctx, "account", http.MethodGet, c.plexURL+"/api/v2/user", a, nil)
	if err != nil {
		return PlexUser{}, err
	}
	var u struct {
		ID       idText `json:"id"`
		UUID     string `json:"uuid"`
		Username string `json:"username"`
		Title    string `json:"title"`
		Email    string `json:"email"`
	}
	if err := json.Unmarshal(b, &u); err != nil {
		return PlexUser{}, fmt.Errorf("plex: unreadable account answer: %w", err)
	}
	id := string(u.ID)
	if id == "" {
		id = u.UUID
	}
	if id == "" {
		return PlexUser{}, errors.New("plex: the account answer has no id")
	}
	name := firstNonEmpty(u.Username, u.Title)
	return PlexUser{ID: id, Username: name, DisplayName: firstNonEmpty(u.Title, u.Username)}, nil
}

func firstNonEmpty(v ...string) string {
	for _, s := range v {
		if s = strings.TrimSpace(s); s != "" {
			return s
		}
	}
	return ""
}

// ---- the owner's watchlist ---------------------------------------------------

// metadataEntry is one item as the discover service writes it.
type metadataEntry struct {
	RatingKey string `json:"ratingKey"`
	Key       string `json:"key"`
	GUID      string `json:"guid"`
	Type      string `json:"type"`
	Title     string `json:"title"`
	Year      int    `json:"year"`
	Guid      []struct {
		ID string `json:"id"`
	} `json:"Guid"`
}

func (m metadataEntry) item() Item {
	key := m.RatingKey
	if key == "" {
		key = lastSegment(m.Key)
	}
	if key == "" {
		key = lastSegment(m.GUID)
	}
	it := Item{Key: key, Title: strings.TrimSpace(m.Title), Year: m.Year, Type: normalizeType(m.Type)}
	for _, g := range m.Guid {
		if g.ID != "" {
			it.Guids = append(it.Guids, g.ID)
		}
	}
	return it
}

func lastSegment(s string) string {
	s = strings.TrimRight(s, "/")
	if i := strings.LastIndex(s, "/"); i >= 0 {
		return s[i+1:]
	}
	return s
}

// normalizeType maps Plex's and the GraphQL API's spellings to movie or show.
func normalizeType(t string) string {
	switch strings.ToLower(strings.TrimSpace(t)) {
	case "movie", "film":
		return "movie"
	case "show", "tv", "tvshow", "tv_show", "series":
		return "show"
	}
	return strings.ToLower(strings.TrimSpace(t))
}

type mediaContainer struct {
	MediaContainer struct {
		Size      int             `json:"size"`
		TotalSize int             `json:"totalSize"`
		Offset    int             `json:"offset"`
		Metadata  []metadataEntry `json:"Metadata"`
	} `json:"MediaContainer"`
}

func (c *PlexClient) OwnerWatchlist(ctx context.Context, a Auth) ([]Item, error) {
	var out []Item
	seen := map[string]bool{}
	start := 0
	for page := 0; page < maxPages; page++ {
		u := withToken(c.discoverURL+"/library/sections/watchlist/all", a, url.Values{
			"includeGuids":           {"1"},
			"X-Plex-Container-Start": {strconv.Itoa(start)},
			"X-Plex-Container-Size":  {strconv.Itoa(pageSize)},
		})
		b, err := c.call(ctx, "watchlist", http.MethodGet, u, a, nil)
		if err != nil {
			return nil, err
		}
		var mc mediaContainer
		if err := json.Unmarshal(b, &mc); err != nil {
			return nil, fmt.Errorf("plex: unreadable watchlist answer: %w", err)
		}
		entries := mc.MediaContainer.Metadata
		for _, m := range entries {
			it := m.item()
			if it.Key == "" || seen[it.Key] {
				continue
			}
			seen[it.Key] = true
			out = append(out, it)
		}
		start += len(entries)
		total := mc.MediaContainer.TotalSize
		if len(entries) == 0 || (total > 0 && start >= total) || (total == 0 && len(entries) < pageSize) {
			break
		}
	}
	return out, nil
}

func (c *PlexClient) ItemGuids(ctx context.Context, a Auth, key string) ([]string, error) {
	u := withToken(c.discoverURL+"/library/metadata/"+url.PathEscape(key), a, url.Values{"includeGuids": {"1"}})
	b, err := c.call(ctx, "item", http.MethodGet, u, a, nil)
	if err != nil {
		return nil, err
	}
	var mc mediaContainer
	if err := json.Unmarshal(b, &mc); err != nil {
		return nil, fmt.Errorf("plex: unreadable item answer: %w", err)
	}
	var out []string
	for _, m := range mc.MediaContainer.Metadata {
		out = append(out, m.item().Guids...)
	}
	return out, nil
}

// ---- friends (the community GraphQL API) ----------------------------------

type graphQLReply struct {
	Data   json.RawMessage `json:"data"`
	Errors []struct {
		Message string `json:"message"`
	} `json:"errors"`
}

func (c *PlexClient) graphQL(ctx context.Context, a Auth, op, query string, vars map[string]any) (json.RawMessage, error) {
	b, err := c.call(ctx, op, http.MethodPost, c.communityURL, a, map[string]any{"query": query, "variables": vars})
	if err != nil {
		return nil, err
	}
	var r graphQLReply
	if err := json.Unmarshal(b, &r); err != nil {
		return nil, fmt.Errorf("plex: unreadable %s answer: %w", op, err)
	}
	if len(r.Errors) > 0 && (len(r.Data) == 0 || string(r.Data) == "null") {
		return nil, fmt.Errorf("plex: %s: %s", op, r.Errors[0].Message)
	}
	return r.Data, nil
}

const friendsQuery = `query Friends { allFriendsV2 { user { id username displayName } } }`

func (c *PlexClient) Friends(ctx context.Context, a Auth) ([]PlexUser, error) {
	data, err := c.graphQL(ctx, a, "friends", friendsQuery, map[string]any{})
	if err != nil {
		return nil, err
	}
	var d struct {
		AllFriendsV2 []struct {
			User struct {
				ID          idText `json:"id"`
				Username    string `json:"username"`
				DisplayName string `json:"displayName"`
			} `json:"user"`
		} `json:"allFriendsV2"`
	}
	if err := json.Unmarshal(data, &d); err != nil {
		return nil, fmt.Errorf("plex: unreadable friends answer: %w", err)
	}
	var out []PlexUser
	seen := map[string]bool{}
	for _, f := range d.AllFriendsV2 {
		id := string(f.User.ID)
		if id == "" || seen[id] {
			continue
		}
		seen[id] = true
		out = append(out, PlexUser{ID: id, Username: strings.TrimSpace(f.User.Username), DisplayName: strings.TrimSpace(f.User.DisplayName)})
	}
	return out, nil
}

const watchlistQuery = `query FriendWatchlist($user: ID!, $first: PaginationInt!, $after: String) {
  user(id: $user) {
    watchlist(first: $first, after: $after) {
      nodes { id title type publicationYear }
      pageInfo { hasNextPage endCursor }
    }
  }
}`

func (c *PlexClient) FriendWatchlist(ctx context.Context, a Auth, friendID string) ([]Item, error) {
	var out []Item
	seen := map[string]bool{}
	after := ""
	for page := 0; page < maxPages; page++ {
		vars := map[string]any{"user": friendID, "first": pageSize}
		if after != "" {
			vars["after"] = after
		}
		data, err := c.graphQL(ctx, a, "friend watchlist", watchlistQuery, vars)
		if err != nil {
			return nil, err
		}
		var d struct {
			User struct {
				Watchlist struct {
					Nodes []struct {
						ID              idText `json:"id"`
						Title           string `json:"title"`
						Type            string `json:"type"`
						PublicationYear int    `json:"publicationYear"`
					} `json:"nodes"`
					PageInfo struct {
						HasNextPage bool   `json:"hasNextPage"`
						EndCursor   string `json:"endCursor"`
					} `json:"pageInfo"`
				} `json:"watchlist"`
			} `json:"user"`
		}
		if err := json.Unmarshal(data, &d); err != nil {
			return nil, fmt.Errorf("plex: unreadable friend watchlist answer: %w", err)
		}
		wl := d.User.Watchlist
		for _, n := range wl.Nodes {
			key := string(n.ID)
			if key == "" || seen[key] {
				continue
			}
			seen[key] = true
			out = append(out, Item{Key: key, Title: strings.TrimSpace(n.Title), Year: n.PublicationYear, Type: normalizeType(n.Type)})
		}
		if !wl.PageInfo.HasNextPage || wl.PageInfo.EndCursor == "" || wl.PageInfo.EndCursor == after || len(wl.Nodes) == 0 {
			break
		}
		after = wl.PageInfo.EndCursor
	}
	return out, nil
}

// ---- guids -------------------------------------------------------------------

// guidValue finds one scheme in a list of ids: guidValue(g, "tmdb") reads
// tmdb://603 as "603".
func guidValue(guids []string, scheme string) string {
	prefix := scheme + "://"
	for _, g := range guids {
		if strings.HasPrefix(strings.ToLower(g), prefix) {
			return strings.TrimSpace(g[len(prefix):])
		}
	}
	return ""
}

// authURL is the page the owner is sent to for agreeing to a link.
func authURL(clientID, code string) string {
	return "https://app.plex.tv/auth#?clientID=" + url.QueryEscape(clientID) +
		"&code=" + url.QueryEscape(code) + "&context[device][product]=" + product
}
