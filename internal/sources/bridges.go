package sources

import (
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
	"github.com/BaileyPillon/marquee/internal/grab"
)

// Bridges the owner can import from.
const (
	FromProwlarr = "prowlarr"
	FromJackett  = "jackett"
)

// ImportResult is what an import did.
type ImportResult struct {
	Created []Indexer `json:"created"`
	Skipped int       `json:"skipped"` // already added (same address)
}

// maxBridgeBytes caps an indexer list answer.
const maxBridgeBytes = 16 << 20

type bridgeIndexer struct {
	id       string
	name     string
	kind     string
	priority int
}

// Import adds one source per usable indexer of a Prowlarr or a Jackett. It is
// safe to run again: indexers whose address is already a source are skipped.
func (s *Service) Import(ctx context.Context, from, baseURL, apiKey string) (ImportResult, error) {
	from = strings.ToLower(strings.TrimSpace(from))
	if from != FromProwlarr && from != FromJackett {
		return ImportResult{}, invalid("from must be prowlarr or jackett.")
	}
	base, keyInURL, err := cleanURL(baseURL)
	if err != nil {
		return ImportResult{}, err
	}
	base = strings.TrimRight(base, "/")
	key, err := cleanKey(apiKey)
	if err != nil {
		return ImportResult{}, err
	}
	if key == "" {
		key = keyInURL
	}
	if key == "" {
		return ImportResult{}, invalid("Paste the API key of your " + titleCase(from) + ".")
	}

	var list []bridgeIndexer
	switch from {
	case FromProwlarr:
		list, err = s.listProwlarr(ctx, base, key)
	default:
		list, err = s.listJackett(ctx, base, key)
	}
	if err != nil {
		return ImportResult{}, err
	}

	existing, err := s.store.List(ctx)
	if err != nil {
		return ImportResult{}, err
	}
	have := map[string]bool{}
	for _, r := range existing {
		have[r.URL] = true
	}
	sealed, err := s.box.Seal(key)
	if err != nil {
		return ImportResult{}, err
	}
	res := ImportResult{Created: []Indexer{}}
	for _, b := range list {
		var feed string
		if from == FromProwlarr {
			feed = base + "/" + b.id + "/api"
		} else {
			feed = base + "/api/v2.0/indexers/" + url.PathEscape(b.id) + "/results/torznab/"
		}
		if have[feed] {
			res.Skipped++
			continue
		}
		prio := b.priority
		if prio < MinPriority || prio > MaxPriority {
			prio = DefaultPriority
		}
		name := strings.TrimSpace(b.name)
		if name == "" {
			name = titleCase(from) + " " + b.id
		}
		if r := []rune(name); len(r) > maxNameRunes {
			name = string(r[:maxNameRunes])
		}
		id, err := s.store.Insert(ctx, record{
			Name: name, Kind: b.kind, URL: feed, KeySealed: sealed, Categories: []int{},
			Enabled: true, Priority: prio, CreatedAt: s.now(),
		})
		if errors.Is(err, ErrDuplicate) {
			res.Skipped++
			continue
		}
		if err != nil {
			return res, err
		}
		have[feed] = true
		ix, err := s.Get(ctx, id)
		if err != nil {
			return res, err
		}
		res.Created = append(res.Created, ix)
	}
	return res, nil
}

func titleCase(s string) string {
	if s == "" {
		return s
	}
	return strings.ToUpper(s[:1]) + s[1:]
}

// getJSON fetches a bridge's list. The key is sent in a header (Prowlarr) or
// in the query (Jackett); either way no error text carries it.
func (s *Service) getJSON(ctx context.Context, rawURL, header, key, name string, dst any) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, rawURL, nil)
	if err != nil {
		return invalid("That address does not look right.")
	}
	req.Header.Set("Accept", "application/json")
	if header != "" {
		req.Header.Set(header, key)
	}
	resp, err := s.hc.Do(req, outbound.WithTimeout(SearchTimeout))
	if err != nil {
		return s.transportError(ctx, err, key)
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(io.LimitReader(resp.Body, maxBridgeBytes))
	if err != nil {
		return failure(CodeUnreachable, "The answer from "+name+" was cut short.")
	}
	switch code := resp.StatusCode; {
	case code == http.StatusOK:
	case code == http.StatusUnauthorized || code == http.StatusForbidden:
		return failure(CodeKeyRefused, name+" refused the API key.")
	case code == http.StatusNotFound:
		return failure(CodeNotAFeed, "That address does not look like "+name+" (404). Check the address and port.")
	default:
		return failure(CodeSourceError, fmt.Sprintf("%s answered with HTTP %d.", name, code))
	}
	if err := json.Unmarshal(body, dst); err != nil {
		return failure(CodeNotAFeed, "That address answered, but not like "+name+" does.")
	}
	return nil
}

func (s *Service) listProwlarr(ctx context.Context, base, key string) ([]bridgeIndexer, error) {
	var raw []struct {
		ID             int64  `json:"id"`
		Name           string `json:"name"`
		Enable         bool   `json:"enable"`
		Protocol       string `json:"protocol"`
		Priority       int    `json:"priority"`
		Implementation string `json:"implementation"`
	}
	if err := s.getJSON(ctx, base+"/api/v1/indexer", "X-Api-Key", key, "Prowlarr", &raw); err != nil {
		return nil, err
	}
	var out []bridgeIndexer
	for _, r := range raw {
		if !r.Enable || r.ID <= 0 {
			continue
		}
		kind := KindTorznab
		if strings.EqualFold(r.Protocol, grab.ProtocolUsenet) {
			kind = KindNewznab
		}
		out = append(out, bridgeIndexer{id: strconv.FormatInt(r.ID, 10), name: r.Name, kind: kind, priority: r.Priority})
	}
	return out, nil
}

func (s *Service) listJackett(ctx context.Context, base, key string) ([]bridgeIndexer, error) {
	var raw []struct {
		ID         string `json:"id"`
		Name       string `json:"name"`
		Configured *bool  `json:"configured"`
	}
	q := url.Values{"configured": {"true"}, "apikey": {key}}
	if err := s.getJSON(ctx, base+"/api/v2.0/indexers?"+q.Encode(), "", key, "Jackett", &raw); err != nil {
		return nil, err
	}
	var out []bridgeIndexer
	for _, r := range raw {
		if r.ID == "" || (r.Configured != nil && !*r.Configured) {
			continue
		}
		out = append(out, bridgeIndexer{id: r.ID, name: r.Name, kind: KindTorznab, priority: DefaultPriority})
	}
	return out, nil
}
