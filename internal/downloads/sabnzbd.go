package downloads

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"sync"

	"github.com/BaileyPillon/marquee/internal/core/outbound"
)

// sabnzbd talks to SABnzbd's HTTP API (/api?mode=...&output=json).
//
// The API key travels in the query string, which is how SABnzbd wants it.
// outbound strips query strings from its log lines and from errors, and nothing
// here puts a URL (or SABnzbd's own error text, which can echo the NZB link)
// into an error.
type sabnzbd struct {
	base   string
	apiKey string
	hc     *outbound.Client

	knownCats sync.Map
}

func newSABnzbd(cfg AdapterConfig) (*sabnzbd, error) {
	base, err := normalizeBase(cfg.URL)
	if err != nil {
		return nil, err
	}
	return &sabnzbd{base: strings.TrimSuffix(base, "/api"), apiKey: cfg.APIKey, hc: cfg.HTTP}, nil
}

var _ Adapter = (*sabnzbd)(nil)

// api calls one mode and returns the decoded JSON object.
func (s *sabnzbd) api(ctx context.Context, mode string, extra url.Values) (map[string]json.RawMessage, error) {
	q := url.Values{"mode": {mode}, "output": {"json"}}
	if s.apiKey != "" {
		q.Set("apikey", s.apiKey)
	}
	for k, v := range extra {
		q[k] = v
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, s.base+"/api?"+q.Encode(), nil)
	if err != nil {
		return nil, errors.New("SABnzbd: the address cannot be used")
	}
	status, _, body, err := send(s.hc, req)
	if err != nil {
		return nil, err
	}
	if status != http.StatusOK {
		return nil, statusError("SABnzbd", status)
	}
	var obj map[string]json.RawMessage
	if err := json.Unmarshal(body, &obj); err != nil {
		return nil, errors.New("SABnzbd sent an answer Marquee cannot read (is the address SABnzbd's, such as http://host:8080?)")
	}
	// {"status": false, "error": "API Key Incorrect"}
	var ok bool
	if raw, has := obj["status"]; has && json.Unmarshal(raw, &ok) == nil && !ok {
		var msg string
		_ = json.Unmarshal(obj["error"], &msg)
		if strings.Contains(strings.ToLower(msg), "api key") {
			return nil, fmt.Errorf("%w: SABnzbd does not accept that API key", ErrAuth)
		}
		return nil, fmt.Errorf("%w: SABnzbd said no", ErrRejected)
	}
	return obj, nil
}

func (s *sabnzbd) Test(ctx context.Context) (string, error) {
	obj, err := s.api(ctx, "version", nil)
	if err != nil {
		return "", err
	}
	var version string
	_ = json.Unmarshal(obj["version"], &version)
	// mode=version needs no key, so check the key with a call that does.
	if _, err := s.api(ctx, "queue", url.Values{"limit": {"1"}}); err != nil {
		return "", err
	}
	return version, nil
}

func (s *sabnzbd) Add(ctx context.Context, in AddInput) (string, error) {
	if in.NZBURL == "" {
		return "", fmt.Errorf("%w: nothing to add", ErrRejected)
	}
	if in.Category != "" {
		s.ensureCategory(ctx, in.Category)
	}
	q := url.Values{"name": {in.NZBURL}}
	if in.Category != "" {
		q.Set("cat", in.Category)
	}
	if in.Name != "" {
		q.Set("nzbname", in.Name)
	}
	if in.Paused {
		q.Set("priority", "-2") // SABnzbd's "paused"
	}
	obj, err := s.api(ctx, "addurl", q)
	if err != nil {
		return "", err
	}
	var ids []string
	_ = json.Unmarshal(obj["nzo_ids"], &ids)
	if len(ids) == 0 || ids[0] == "" {
		return "", fmt.Errorf("%w: SABnzbd took the link but gave no id for it", ErrRejected)
	}
	return ids[0], nil
}

// ensureCategory makes sure SABnzbd has the category, because a download added
// under an unknown category lands in the default one and would never show up in
// Marquee's list. Best effort: if SABnzbd will not make it, adding goes on.
func (s *sabnzbd) ensureCategory(ctx context.Context, name string) {
	if _, known := s.knownCats.Load(name); known {
		return
	}
	if obj, err := s.api(ctx, "get_cats", nil); err == nil {
		var cats []string
		_ = json.Unmarshal(obj["categories"], &cats)
		for _, c := range cats {
			if strings.EqualFold(c, name) {
				s.knownCats.Store(name, true)
				return
			}
		}
	} else {
		return
	}
	if _, err := s.api(ctx, "set_config", url.Values{
		"section": {"categories"}, "name": {name}, "dir": {name},
	}); err == nil {
		s.knownCats.Store(name, true)
	}
}

// ---- reading the queue and the history ---------------------------------------

// flexFloat reads a number that SABnzbd sends as a number or as a string.
type flexFloat float64

func (f *flexFloat) UnmarshalJSON(b []byte) error {
	s := strings.Trim(strings.TrimSpace(string(b)), `"`)
	if s == "" || s == "null" {
		*f = 0
		return nil
	}
	n, err := strconv.ParseFloat(s, 64)
	if err != nil {
		*f = 0
		return nil // an odd value is not worth failing a whole poll
	}
	*f = flexFloat(n)
	return nil
}

type sabSlot struct {
	ID         string    `json:"nzo_id"`
	Filename   string    `json:"filename"`
	Status     string    `json:"status"`
	Percentage flexFloat `json:"percentage"`
	MB         flexFloat `json:"mb"`
	MBLeft     flexFloat `json:"mbleft"`
	Cat        string    `json:"cat"`
	TimeLeft   string    `json:"timeleft"`
}

type sabHistorySlot struct {
	ID       string    `json:"nzo_id"`
	Name     string    `json:"name"`
	Status   string    `json:"status"`
	Fail     string    `json:"fail_message"`
	Storage  string    `json:"storage"`
	Category string    `json:"category"`
	Bytes    flexFloat `json:"bytes"`
}

const mib = 1 << 20

func (s *sabnzbd) List(ctx context.Context, category string) ([]Item, error) {
	// The queue first, then the history: a download moves from the first to the
	// second, so reading them in this order can never miss one that is moving.
	qObj, err := s.api(ctx, "queue", nil)
	if err != nil {
		return nil, err
	}
	hObj, err := s.api(ctx, "history", url.Values{"limit": {"200"}})
	if err != nil {
		return nil, err
	}

	var queue struct {
		KBPerSec flexFloat `json:"kbpersec"`
		Slots    []sabSlot `json:"slots"`
	}
	if raw, ok := qObj["queue"]; ok {
		if err := json.Unmarshal(raw, &queue); err != nil {
			return nil, errors.New("SABnzbd sent a queue Marquee cannot read")
		}
	}
	var history struct {
		Slots []sabHistorySlot `json:"slots"`
	}
	if raw, ok := hObj["history"]; ok {
		if err := json.Unmarshal(raw, &history); err != nil {
			return nil, errors.New("SABnzbd sent a history Marquee cannot read")
		}
	}

	var items []Item
	gotSpeed := false
	for _, sl := range queue.Slots {
		if category != "" && !strings.EqualFold(sl.Cat, category) {
			continue
		}
		it := sl.item()
		if it.State == StateDownloading && !gotSpeed { // SABnzbd reports one speed for the whole queue
			it.SpeedBps = int64(float64(queue.KBPerSec) * 1024)
			gotSpeed = true
		}
		items = append(items, it)
	}
	for _, h := range history.Slots {
		if category != "" && !strings.EqualFold(h.Category, category) {
			continue
		}
		items = append(items, h.item())
	}
	return items, nil
}

func (sl sabSlot) item() Item {
	it := Item{
		Ref:      sl.ID,
		Name:     sl.Filename,
		Progress: clamp01(float64(sl.Percentage) / 100),
		Size:     int64(float64(sl.MB) * mib),
		ETA:      -1,
	}
	it.Done = max(it.Size-int64(float64(sl.MBLeft)*mib), 0)
	switch strings.ToLower(sl.Status) {
	case "paused":
		it.State = StatePaused
	case "queued", "propagating", "idle":
		it.State = StateQueued
	default: // downloading, fetching, grabbing, ...
		it.State = StateDownloading
	}
	if it.State == StateDownloading {
		if eta := parseSabTime(sl.TimeLeft); eta > 0 {
			it.ETA = eta
		}
	}
	return it
}

var linkRe = regexp.MustCompile(`https?://\S+`)

func (h sabHistorySlot) item() Item {
	it := Item{
		Ref:      h.ID,
		Name:     h.Name,
		Size:     int64(h.Bytes),
		ETA:      -1,
		SavePath: parentDir(h.Storage),
	}
	switch strings.ToLower(h.Status) {
	case "completed":
		it.State, it.Finished, it.Progress, it.ETA = StateCompleted, true, 1, 0
		it.Done = it.Size
		it.ContentPath = h.Storage
	case "failed":
		it.State = StateFailed
		it.Error = "SABnzbd could not finish this download."
		// The failure text can echo the NZB link, which can carry a key.
		if m := strings.TrimSpace(linkRe.ReplaceAllString(h.Fail, "(link)")); m != "" {
			it.Error = "SABnzbd says: " + shorten(m, 240)
		}
	default: // Verifying, Repairing, Extracting, Moving, Running, ...: still being worked on
		it.State = StateDownloading
		it.Progress = 0.99
		it.Done = it.Size
	}
	return it
}

func parentDir(p string) string {
	if i := strings.LastIndexAny(p, `/\`); i > 0 {
		return p[:i]
	}
	return ""
}

// parseSabTime reads SABnzbd's "H:MM:SS" (or "D:HH:MM:SS") into seconds.
func parseSabTime(s string) int {
	parts := strings.Split(strings.TrimSpace(s), ":")
	if len(parts) < 2 || len(parts) > 4 {
		return 0
	}
	mult := []int{1, 60, 3600, 86400}
	total := 0
	for i := range parts {
		n, err := strconv.Atoi(parts[len(parts)-1-i])
		if err != nil || n < 0 {
			return 0
		}
		total += n * mult[i]
	}
	return total
}

func (s *sabnzbd) Remove(ctx context.Context, ref string, deleteData bool) error {
	extra := url.Values{"name": {"delete"}, "value": {ref}}
	if deleteData {
		extra.Set("del_files", "1")
	}
	// It is in the queue or in the history; asking both is harmless.
	if _, err := s.api(ctx, "queue", extra); err != nil {
		return err
	}
	_, err := s.api(ctx, "history", extra)
	return err
}

func (s *sabnzbd) Pause(ctx context.Context, ref string) error {
	_, err := s.api(ctx, "queue", url.Values{"name": {"pause"}, "value": {ref}})
	return err
}

func (s *sabnzbd) Resume(ctx context.Context, ref string) error {
	_, err := s.api(ctx, "queue", url.Values{"name": {"resume"}, "value": {ref}})
	return err
}
