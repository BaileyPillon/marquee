package subtitles

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/BaileyPillon/marquee/internal/core/outbound"
)

// DefaultOpenSubtitlesURL is the OpenSubtitles.com REST API, version 1.
const DefaultOpenSubtitlesURL = "https://api.opensubtitles.com/api/v1"

const (
	// loginKeep is how long a login token is used again before logging in anew
	// (the service says a token lasts 24 hours).
	loginKeep = 12 * time.Hour
	// maxReply bounds what is read from an API answer.
	maxReply = 4 << 20
)

// OpenSubtitles is the OpenSubtitles.com provider. It needs the owner's API key;
// a username and password, when given, are used to log in so that downloads have
// the account's quota. It is safe for concurrent use. Configure it with the
// owner's settings before each use: it keeps the login token between calls and
// forgets it when the account changes.
type OpenSubtitles struct {
	hc      *outbound.Client
	base    string
	version string
	now     func() time.Time
	// loginBase says the login's base_url may replace the base for downloads (it
	// does not when a test points the provider at its own server).
	loginBase bool

	mu       sync.Mutex
	cfg      OpenSubtitlesConfig
	token    string
	tokenFor string
	tokenAt  time.Time
	apiBase  string
}

// NewOpenSubtitles builds the provider. baseURL is DefaultOpenSubtitlesURL
// unless a test says otherwise; version goes into the User-Agent the API asks
// for ("Marquee v1.2.3").
func NewOpenSubtitles(hc *outbound.Client, baseURL, version string) *OpenSubtitles {
	if baseURL == "" {
		baseURL = DefaultOpenSubtitlesURL
	}
	return &OpenSubtitles{
		hc: hc, base: strings.TrimRight(baseURL, "/"), version: version, now: time.Now,
		loginBase: strings.TrimRight(baseURL, "/") == DefaultOpenSubtitlesURL,
	}
}

var _ Provider = (*OpenSubtitles)(nil)

// Name implements Provider.
func (o *OpenSubtitles) Name() string { return ProviderOpenSubtitles }

// Configure sets the owner's account. A changed username or password drops the
// saved login.
func (o *OpenSubtitles) Configure(cfg OpenSubtitlesConfig) {
	o.mu.Lock()
	defer o.mu.Unlock()
	o.cfg = cfg
	if o.tokenFor != credentialSig(cfg) {
		o.token, o.tokenFor, o.apiBase = "", "", ""
	}
}

func credentialSig(cfg OpenSubtitlesConfig) string { return cfg.Username + "\x00" + cfg.Password }

func (o *OpenSubtitles) config() OpenSubtitlesConfig {
	o.mu.Lock()
	defer o.mu.Unlock()
	return o.cfg
}

func (o *OpenSubtitles) userAgent() string { return "Marquee v" + o.version }

var errNoKey = &AuthError{Provider: ProviderOpenSubtitles, Message: "OpenSubtitles needs your API key. Add it in Settings, then Subtitles."}

// reply is one answer from the API.
type reply struct {
	status int
	body   []byte
	header http.Header
}

// call sends one request. Only network failures are errors here; the caller
// decides what a status means.
func (o *OpenSubtitles) call(ctx context.Context, method, base, path string, query url.Values, body any, token string) (reply, error) {
	u := base + path
	if len(query) > 0 {
		u += "?" + query.Encode() // sorted by key, as the API wants
	}
	var rd io.Reader
	if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			return reply{}, err
		}
		rd = bytes.NewReader(b)
	}
	req, err := http.NewRequestWithContext(ctx, method, u, rd)
	if err != nil {
		return reply{}, fmt.Errorf("the OpenSubtitles address is not usable")
	}
	req.Header.Set("Api-Key", o.config().APIKey)
	req.Header.Set("User-Agent", o.userAgent())
	req.Header.Set("Accept", "application/json")
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	resp, err := o.hc.Do(req)
	if err != nil {
		return reply{}, &UnavailableError{Provider: ProviderOpenSubtitles, Message: "OpenSubtitles could not be reached."}
	}
	defer resp.Body.Close()
	b, err := io.ReadAll(io.LimitReader(resp.Body, maxReply))
	if err != nil {
		return reply{}, &UnavailableError{Provider: ProviderOpenSubtitles, Message: "OpenSubtitles stopped answering."}
	}
	return reply{status: resp.StatusCode, body: b, header: resp.Header}, nil
}

// failure turns an unexpected answer into the error to report. It never carries
// the address or a key.
func (o *OpenSubtitles) failure(r reply) error {
	switch {
	case r.status == http.StatusUnauthorized || r.status == http.StatusForbidden:
		return &AuthError{Provider: ProviderOpenSubtitles, Message: "OpenSubtitles did not accept the API key or login (HTTP " + strconv.Itoa(r.status) + ")."}
	case r.status == http.StatusTooManyRequests:
		return &RateLimitError{Provider: ProviderOpenSubtitles, RetryAfter: retryAfter(r.header)}
	case r.status >= 500:
		return &UnavailableError{Provider: ProviderOpenSubtitles, Message: "OpenSubtitles did not answer (HTTP " + strconv.Itoa(r.status) + "). Try again later."}
	}
	return &UnavailableError{Provider: ProviderOpenSubtitles, Message: "OpenSubtitles answered an unexpected HTTP " + strconv.Itoa(r.status) + "."}
}

func retryAfter(h http.Header) time.Duration {
	if n, err := strconv.Atoi(strings.TrimSpace(h.Get("Retry-After"))); err == nil && n > 0 {
		return time.Duration(n) * time.Second
	}
	return 0
}

// ---- search -----------------------------------------------------------------------

// imdbNumber turns "tt0063350" into 63350.
func imdbNumber(id string) int64 {
	n, err := strconv.ParseInt(strings.TrimPrefix(strings.ToLower(strings.TrimSpace(id)), "tt"), 10, 64)
	if err != nil || n <= 0 {
		return 0
	}
	return n
}

type osFile struct {
	FileID   int64  `json:"file_id"`
	CDNumber int    `json:"cd_number"`
	FileName string `json:"file_name"`
}

type osSubtitle struct {
	Attributes struct {
		Language          string   `json:"language"`
		DownloadCount     int      `json:"download_count"`
		HearingImpaired   bool     `json:"hearing_impaired"`
		Release           string   `json:"release"`
		MovieHashMatch    bool     `json:"moviehash_match"`
		AITranslated      bool     `json:"ai_translated"`
		MachineTranslated bool     `json:"machine_translated"`
		Files             []osFile `json:"files"`
		FeatureDetails    struct {
			Year          int `json:"year"`
			SeasonNumber  int `json:"season_number"`
			EpisodeNumber int `json:"episode_number"`
		} `json:"feature_details"`
	} `json:"attributes"`
}

// Search implements Provider: GET /subtitles with the video's ids, season and
// episode, the languages and its hash.
func (o *OpenSubtitles) Search(ctx context.Context, q Query) ([]Candidate, error) {
	if o.config().APIKey == "" {
		return nil, errNoKey
	}
	params, byID := o.searchParams(q)
	r, err := o.call(ctx, http.MethodGet, o.base, "/subtitles", params, nil, "")
	if err != nil {
		return nil, err
	}
	if r.status != http.StatusOK {
		return nil, o.failure(r)
	}
	var resp struct {
		Data []osSubtitle `json:"data"`
	}
	if err := json.Unmarshal(r.body, &resp); err != nil {
		return nil, &UnavailableError{Provider: ProviderOpenSubtitles, Message: "OpenSubtitles sent an answer Marquee could not read."}
	}
	out := make([]Candidate, 0, len(resp.Data))
	for _, d := range resp.Data {
		a := d.Attributes
		var file *osFile
		for i := range a.Files {
			if a.Files[i].FileID > 0 && (a.Files[i].CDNumber <= 1) {
				file = &a.Files[i]
				break
			}
		}
		if file == nil {
			continue // a multi-disc set, or no file: nothing to download
		}
		c := Candidate{
			Provider: ProviderOpenSubtitles, ProviderID: strconv.FormatInt(file.FileID, 10),
			Language: strings.ToLower(strings.ReplaceAll(a.Language, "_", "-")),
			HI:       a.HearingImpaired, Downloads: a.DownloadCount, HashMatch: a.MovieHashMatch, IDMatch: byID,
			Machine: a.AITranslated || a.MachineTranslated,
			Year:    a.FeatureDetails.Year, Season: a.FeatureDetails.SeasonNumber, Episode: a.FeatureDetails.EpisodeNumber,
		}
		for _, name := range []string{a.Release, file.FileName} {
			if name = strings.TrimSpace(name); name != "" && !containsString(c.Releases, name) {
				c.Releases = append(c.Releases, name)
			}
		}
		out = append(out, c)
	}
	return out, nil
}

func containsString(list []string, s string) bool {
	for _, x := range list {
		if x == s {
			return true
		}
	}
	return false
}

// searchParams builds the query: ids when the video has them, else its name.
// byID says the search was by id.
func (o *OpenSubtitles) searchParams(q Query) (params url.Values, byID bool) {
	v := q.Video
	params = url.Values{}
	if len(q.Languages) > 0 {
		// Lower case, like every parameter the API takes: "en,pt-br".
		params.Set("languages", strings.ToLower(strings.Join(q.Languages, ",")))
	}
	if v.Hash != "" {
		params.Set("moviehash", v.Hash)
	}
	imdb := imdbNumber(v.IMDb)
	if v.IsEpisode() {
		params.Set("type", "episode")
		if imdb > 0 {
			params.Set("parent_imdb_id", strconv.FormatInt(imdb, 10))
			byID = true
		}
		if v.TMDb > 0 {
			params.Set("parent_tmdb_id", strconv.FormatInt(v.TMDb, 10))
			byID = true
		}
		if v.Season > 0 || v.Episode > 0 {
			params.Set("season_number", strconv.Itoa(v.Season))
			params.Set("episode_number", strconv.Itoa(v.Episode))
		}
	} else {
		params.Set("type", "movie")
		if imdb > 0 {
			params.Set("imdb_id", strconv.FormatInt(imdb, 10))
			byID = true
		}
		if v.TMDb > 0 {
			params.Set("tmdb_id", strconv.FormatInt(v.TMDb, 10))
			byID = true
		}
	}
	if !byID && v.Title != "" {
		params.Set("query", strings.ToLower(v.Title))
		if v.Year > 0 && !v.IsEpisode() {
			params.Set("year", strconv.Itoa(v.Year))
		}
	}
	return params, byID
}

// ---- login and download ---------------------------------------------------------------

// login returns a token for the owner's account, or "" when no username and
// password are set (downloads then use the anonymous quota).
func (o *OpenSubtitles) login(ctx context.Context) (token, base string, err error) {
	cfg := o.config()
	if cfg.Username == "" || cfg.Password == "" {
		return "", o.base, nil
	}
	o.mu.Lock()
	if o.token != "" && o.tokenFor == credentialSig(cfg) && o.now().Sub(o.tokenAt) < loginKeep {
		token, base = o.token, o.apiBaseOr()
		o.mu.Unlock()
		return token, base, nil
	}
	o.mu.Unlock()

	r, err := o.call(ctx, http.MethodPost, o.base, "/login", nil, map[string]string{"username": cfg.Username, "password": cfg.Password}, "")
	if err != nil {
		return "", "", err
	}
	switch {
	case r.status == http.StatusOK:
	case r.status == http.StatusUnauthorized || r.status == http.StatusBadRequest || r.status == http.StatusForbidden:
		return "", "", &AuthError{Provider: ProviderOpenSubtitles, Message: "OpenSubtitles did not accept the username and password."}
	default:
		return "", "", o.failure(r)
	}
	var resp struct {
		Token   string `json:"token"`
		BaseURL string `json:"base_url"`
	}
	if err := json.Unmarshal(r.body, &resp); err != nil || resp.Token == "" {
		return "", "", &UnavailableError{Provider: ProviderOpenSubtitles, Message: "OpenSubtitles sent a login answer Marquee could not read."}
	}
	o.mu.Lock()
	defer o.mu.Unlock()
	o.token, o.tokenFor, o.tokenAt = resp.Token, credentialSig(cfg), o.now()
	o.apiBase = ""
	// The account's own API host (VIP accounts have one), but only ever one of
	// OpenSubtitles' own: the key and token are sent there.
	if host := strings.TrimSpace(resp.BaseURL); o.loginBase && host != "" && !strings.ContainsAny(host, "/?#@ ") &&
		(host == "opensubtitles.com" || strings.HasSuffix(host, ".opensubtitles.com")) {
		o.apiBase = "https://" + host + "/api/v1"
	}
	return o.token, o.apiBaseOr(), nil
}

// apiBaseOr is the base to use for the account's calls; the caller holds o.mu.
func (o *OpenSubtitles) apiBaseOr() string {
	if o.apiBase != "" {
		return o.apiBase
	}
	return o.base
}

// Download implements Provider: POST /download for a link, then fetch the link.
// The answer's "remaining" is the quota left today.
func (o *OpenSubtitles) Download(ctx context.Context, c Candidate) (DownloadResult, error) {
	if o.config().APIKey == "" {
		return DownloadResult{}, errNoKey
	}
	fileID, err := strconv.ParseInt(c.ProviderID, 10, 64)
	if err != nil || fileID <= 0 {
		return DownloadResult{}, invalid("That is not an OpenSubtitles subtitle id.")
	}
	token, base, err := o.login(ctx)
	if err != nil {
		return DownloadResult{}, err
	}
	r, err := o.call(ctx, http.MethodPost, base, "/download", nil, map[string]any{"file_id": fileID, "sub_format": "srt"}, token)
	if err != nil {
		return DownloadResult{}, err
	}
	var info struct {
		Link         string `json:"link"`
		Remaining    *int   `json:"remaining"`
		ResetTimeUTC string `json:"reset_time_utc"`
	}
	_ = json.Unmarshal(r.body, &info)
	res := DownloadResult{Remaining: info.Remaining}
	if t, err := time.Parse(time.RFC3339, info.ResetTimeUTC); err == nil {
		res.ResetAt = t
	}
	switch {
	case r.status == http.StatusOK:
	case r.status == http.StatusNotAcceptable || (info.Remaining != nil && *info.Remaining <= 0 && r.status != http.StatusOK):
		return DownloadResult{Remaining: info.Remaining}, &QuotaError{Provider: ProviderOpenSubtitles, ResetAt: res.ResetAt}
	case r.status == http.StatusUnauthorized:
		o.mu.Lock()
		o.token = "" // a stale login: try again from scratch next time
		o.mu.Unlock()
		return DownloadResult{}, o.failure(r)
	default:
		return DownloadResult{}, o.failure(r)
	}

	link, err := url.Parse(info.Link)
	if err != nil || (link.Scheme != "http" && link.Scheme != "https") || link.Host == "" {
		return DownloadResult{}, &UnavailableError{Provider: ProviderOpenSubtitles, Message: "OpenSubtitles gave no usable download link."}
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, link.String(), nil)
	if err != nil {
		return DownloadResult{}, &UnavailableError{Provider: ProviderOpenSubtitles, Message: "OpenSubtitles gave no usable download link."}
	}
	req.Header.Set("User-Agent", o.userAgent())
	resp, err := o.hc.Do(req)
	if err != nil {
		return DownloadResult{}, &UnavailableError{Provider: ProviderOpenSubtitles, Message: "The subtitle file could not be fetched from OpenSubtitles."}
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return DownloadResult{}, &UnavailableError{Provider: ProviderOpenSubtitles, Message: "OpenSubtitles did not hand over the file (HTTP " + strconv.Itoa(resp.StatusCode) + ")."}
	}
	data, err := io.ReadAll(io.LimitReader(resp.Body, maxSubtitleBytes+1))
	if err != nil || len(data) > maxSubtitleBytes {
		return DownloadResult{}, &UnavailableError{Provider: ProviderOpenSubtitles, Message: "The subtitle file from OpenSubtitles was unreadable or too big."}
	}
	res.Data = data
	return res, nil
}

// Test implements Provider: a small search checks the API key, and a login
// checks the username and password when they are set.
func (o *OpenSubtitles) Test(ctx context.Context) error {
	if o.config().APIKey == "" {
		return errNoKey
	}
	r, err := o.call(ctx, http.MethodGet, o.base, "/subtitles", url.Values{"imdb_id": {"133093"}, "languages": {"en"}, "type": {"movie"}}, nil, "")
	if err != nil {
		return err
	}
	if r.status != http.StatusOK {
		return o.failure(r)
	}
	if _, _, err := o.login(ctx); err != nil {
		return err
	}
	return nil
}
