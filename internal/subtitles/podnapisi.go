package subtitles

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"

	"github.com/BaileyPillon/marquee/internal/core/outbound"
)

// DefaultPodnapisiURL is Podnapisi's address.
const DefaultPodnapisiURL = "https://www.podnapisi.net"

// Podnapisi is the Podnapisi provider. It needs no key.
type Podnapisi struct {
	hc      *outbound.Client
	base    string
	version string
}

// NewPodnapisi builds the provider; baseURL is DefaultPodnapisiURL unless a
// test says otherwise.
func NewPodnapisi(hc *outbound.Client, baseURL, version string) *Podnapisi {
	if baseURL == "" {
		baseURL = DefaultPodnapisiURL
	}
	return &Podnapisi{hc: hc, base: strings.TrimRight(baseURL, "/"), version: version}
}

var _ Provider = (*Podnapisi)(nil)

// Name implements Provider.
func (p *Podnapisi) Name() string { return ProviderPodnapisi }

func podnapisiDown(msg string) error {
	return &UnavailableError{Provider: ProviderPodnapisi, Message: msg}
}

// podLanguage is the code Podnapisi uses: its own "pb" for Brazilian
// Portuguese, the first part of the code for the rest.
func podLanguage(code string) string {
	code = strings.ToLower(code)
	switch code {
	case "pt-br":
		return "pb"
	}
	left, _, _ := strings.Cut(code, "-")
	return left
}

// podFromLanguage turns a Podnapisi code back into the settings' spelling.
func podFromLanguage(code string) string {
	switch strings.ToLower(code) {
	case "pb":
		return "pt-br"
	}
	return strings.ToLower(code)
}

// number reads a JSON value that is a number or a numeric string.
type number int

func (n *number) UnmarshalJSON(b []byte) error {
	s := strings.Trim(strings.TrimSpace(string(b)), `"`)
	if v, err := strconv.ParseFloat(s, 64); err == nil {
		*n = number(v)
	}
	return nil
}

// podEntry is one result of the advanced search. The service's JSON has changed
// shape over the years, so every field is read leniently.
type podEntry struct {
	ID              string   `json:"id"`
	Language        string   `json:"language"`
	Year            number   `json:"year"`
	Season          number   `json:"season"`
	Episode         number   `json:"episode"`
	Releases        []string `json:"releases"`
	Release         string   `json:"release"`
	CustomReleases  []string `json:"custom_releases"`
	Flags           []string `json:"flags"`
	HearingImpaired bool     `json:"hearing_impaired"`
	Downloads       number   `json:"downloads"`
	Stats           struct {
		Downloads number `json:"downloads"`
	} `json:"stats"`
}

// Search implements Provider: one advanced search per language, as JSON.
func (p *Podnapisi) Search(ctx context.Context, q Query) ([]Candidate, error) {
	v := q.Video
	var out []Candidate
	var firstErr error
	for _, lang := range q.Languages {
		params := url.Values{}
		params.Set("keywords", v.Title)
		params.Set("language", podLanguage(lang))
		if v.IsEpisode() {
			if v.Season > 0 {
				params.Set("seasons", strconv.Itoa(v.Season))
			}
			if v.Episode > 0 {
				params.Set("episodes", strconv.Itoa(v.Episode))
			}
		} else if v.Year > 0 {
			params.Set("year", strconv.Itoa(v.Year))
		}
		list, err := p.search(ctx, params, lang)
		if err != nil {
			if firstErr == nil {
				firstErr = err
			}
			continue
		}
		out = append(out, list...)
	}
	if len(out) == 0 && firstErr != nil {
		return nil, firstErr
	}
	return out, nil
}

func (p *Podnapisi) search(ctx context.Context, params url.Values, lang string) ([]Candidate, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, p.base+"/subtitles/search/advanced?"+params.Encode(), nil)
	if err != nil {
		return nil, podnapisiDown("The Podnapisi address is not usable.")
	}
	req.Header.Set("Accept", "application/json")
	req.Header.Set("User-Agent", "Marquee v"+p.version)
	resp, err := p.hc.Do(req)
	if err != nil {
		return nil, podnapisiDown("Podnapisi could not be reached.")
	}
	defer resp.Body.Close()
	switch {
	case resp.StatusCode == http.StatusTooManyRequests:
		return nil, &RateLimitError{Provider: ProviderPodnapisi, RetryAfter: retryAfter(resp.Header)}
	case resp.StatusCode != http.StatusOK:
		return nil, podnapisiDown("Podnapisi did not answer (HTTP " + strconv.Itoa(resp.StatusCode) + ").")
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, maxReply))
	if err != nil {
		return nil, podnapisiDown("Podnapisi stopped answering.")
	}
	var parsed struct {
		Data []podEntry `json:"data"`
	}
	if err := json.Unmarshal(body, &parsed); err != nil {
		return nil, podnapisiDown("Podnapisi sent an answer Marquee could not read.")
	}
	out := make([]Candidate, 0, len(parsed.Data))
	for _, e := range parsed.Data {
		if e.ID == "" {
			continue
		}
		c := Candidate{
			Provider: ProviderPodnapisi, ProviderID: e.ID, Language: podFromLanguage(firstNonEmpty(e.Language, podLanguage(lang))),
			HI: e.HearingImpaired, Downloads: int(e.Downloads),
			Year: int(e.Year), Season: int(e.Season), Episode: int(e.Episode),
		}
		if c.Downloads == 0 {
			c.Downloads = int(e.Stats.Downloads)
		}
		for _, f := range e.Flags {
			if strings.Contains(strings.ToLower(f), "hearing") {
				c.HI = true
			}
		}
		for _, list := range [][]string{e.Releases, e.CustomReleases, {e.Release}} {
			for _, name := range list {
				if name = strings.TrimSpace(name); name != "" && !containsString(c.Releases, name) {
					c.Releases = append(c.Releases, name)
				}
			}
		}
		out = append(out, c)
	}
	return out, nil
}

func firstNonEmpty(a, b string) string {
	if strings.TrimSpace(a) != "" {
		return a
	}
	return b
}

// Download implements Provider: the subtitle comes as a zip; the .srt in it is
// returned.
func (p *Podnapisi) Download(ctx context.Context, c Candidate) (DownloadResult, error) {
	id := strings.TrimSpace(c.ProviderID)
	if id == "" || strings.ContainsAny(id, "/?#\\ ") {
		return DownloadResult{}, invalid("That is not a Podnapisi subtitle id.")
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, p.base+"/subtitles/"+url.PathEscape(id)+"/download", nil)
	if err != nil {
		return DownloadResult{}, podnapisiDown("The Podnapisi address is not usable.")
	}
	req.Header.Set("User-Agent", "Marquee v"+p.version)
	resp, err := p.hc.Do(req)
	if err != nil {
		return DownloadResult{}, podnapisiDown("Podnapisi could not be reached.")
	}
	defer resp.Body.Close()
	switch {
	case resp.StatusCode == http.StatusTooManyRequests:
		return DownloadResult{}, &RateLimitError{Provider: ProviderPodnapisi, RetryAfter: retryAfter(resp.Header)}
	case resp.StatusCode != http.StatusOK:
		return DownloadResult{}, podnapisiDown("Podnapisi did not hand over the file (HTTP " + strconv.Itoa(resp.StatusCode) + ").")
	}
	// A zip of a subtitle is small; the cap keeps a wrong answer from filling memory.
	data, err := io.ReadAll(io.LimitReader(resp.Body, maxSubtitleBytes+1))
	if err != nil || len(data) > maxSubtitleBytes {
		return DownloadResult{}, podnapisiDown("The file from Podnapisi was unreadable or too big.")
	}
	srt, err := ExtractSRT(data)
	if err != nil {
		return DownloadResult{}, podnapisiDown("Podnapisi sent a file Marquee could not unpack: " + err.Error() + ".")
	}
	return DownloadResult{Data: srt}, nil
}

// Test implements Provider: a small search.
func (p *Podnapisi) Test(ctx context.Context) error {
	_, err := p.search(ctx, url.Values{"keywords": {"Metropolis"}, "year": {"1927"}, "language": {"en"}}, "en")
	return err
}
