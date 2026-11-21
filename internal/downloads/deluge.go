package downloads

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/BaileyPillon/marquee/internal/core/outbound"
)

// deluge talks to the Deluge web UI's JSON-RPC endpoint (/json). The web UI
// proxies the daemon's core.* calls, so Marquee signs in to the web UI, makes
// sure it is connected to a daemon, and then calls the daemon through it.
//
// The category is a label from Deluge's Label plugin when that is enabled. When
// it is not, downloads are tracked by their own refs only, and List returns
// everything the daemon has.
type deluge struct {
	endpoint string // .../json
	password string
	hc       *outbound.Client

	mu      sync.Mutex // guards the session below
	cookies []*http.Cookie

	nextID atomic.Int64

	labelMu    sync.Mutex
	labelKnown bool
	labelOK    bool
	labelAt    time.Time
}

// labelRecheck is how long a "the Label plugin is (not) there" answer is kept.
const labelRecheck = 5 * time.Minute

func newDeluge(cfg AdapterConfig) (*deluge, error) {
	base, err := normalizeBase(cfg.URL)
	if err != nil {
		return nil, err
	}
	return &deluge{endpoint: strings.TrimSuffix(base, "/json") + "/json", password: cfg.Password, hc: cfg.HTTP}, nil
}

var _ Adapter = (*deluge)(nil)

// rpcError is an error Deluge reported in its reply.
type rpcError struct {
	Message string `json:"message"`
	Code    int    `json:"code"`
}

func (e *rpcError) Error() string { return "Deluge: " + e.Message }

// roundTrip makes one JSON-RPC call with the current session cookie.
func (d *deluge) roundTrip(ctx context.Context, method string, params []any) (json.RawMessage, error) {
	if params == nil {
		params = []any{}
	}
	payload, err := json.Marshal(map[string]any{"method": method, "params": params, "id": d.nextID.Add(1)})
	if err != nil {
		return nil, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, d.endpoint, bytes.NewReader(payload))
	if err != nil {
		return nil, errors.New("Deluge: the address cannot be used")
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")
	d.mu.Lock()
	for _, c := range d.cookies {
		req.AddCookie(c)
	}
	d.mu.Unlock()
	resp, err := d.hc.Do(req)
	if err != nil {
		return nil, unreachable(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, statusError("Deluge", resp.StatusCode)
	}
	var reply struct {
		Result json.RawMessage `json:"result"`
		Error  *rpcError       `json:"error"`
	}
	if err := json.NewDecoder(limitBody(resp)).Decode(&reply); err != nil {
		return nil, errors.New("Deluge sent an answer Marquee cannot read (is the address the web UI's, such as http://host:8112?)")
	}
	if reply.Error != nil {
		return nil, reply.Error
	}
	// A successful login sets the session cookie; keep whatever Deluge hands out.
	if cs := resp.Cookies(); len(cs) > 0 {
		d.mu.Lock()
		d.cookies = cs
		d.mu.Unlock()
	}
	return reply.Result, nil
}

func isNotAuthenticated(err error) bool {
	var re *rpcError
	return errors.As(err, &re) && (re.Code == 1 || strings.Contains(strings.ToLower(re.Message), "not authenticated"))
}

func isNotConnected(err error) bool {
	var re *rpcError
	if !errors.As(err, &re) {
		return false
	}
	m := strings.ToLower(re.Message)
	return strings.Contains(m, "not connected") || strings.Contains(m, "no daemon") || strings.Contains(m, "not connect")
}

// call makes a call, signing in (and reconnecting to the daemon) once if Deluge
// says the session is gone.
func (d *deluge) call(ctx context.Context, method string, params ...any) (json.RawMessage, error) {
	d.mu.Lock()
	haveSession := len(d.cookies) > 0
	d.mu.Unlock()
	if !haveSession {
		if err := d.login(ctx); err != nil {
			return nil, err
		}
	}
	res, err := d.roundTrip(ctx, method, params)
	switch {
	case err == nil:
		return res, nil
	case isNotAuthenticated(err):
		if err := d.login(ctx); err != nil {
			return nil, err
		}
	case isNotConnected(err):
		if err := d.connect(ctx); err != nil {
			return nil, err
		}
	default:
		return nil, err
	}
	return d.roundTrip(ctx, method, params)
}

func (d *deluge) login(ctx context.Context) error {
	d.mu.Lock()
	d.cookies = nil
	d.mu.Unlock()
	res, err := d.roundTrip(ctx, "auth.login", []any{d.password})
	if err != nil {
		return err
	}
	var ok bool
	if json.Unmarshal(res, &ok) != nil || !ok {
		d.mu.Lock()
		d.cookies = nil // Deluge hands out a session cookie even when the password is wrong
		d.mu.Unlock()
		return fmt.Errorf("%w: wrong Deluge web UI password", ErrAuth)
	}
	return d.connect(ctx)
}

// connect makes sure the web UI is connected to a daemon, using the first host
// it has when it is not.
func (d *deluge) connect(ctx context.Context) error {
	res, err := d.roundTrip(ctx, "web.connected", nil)
	if err != nil {
		return err
	}
	var connected bool
	_ = json.Unmarshal(res, &connected)
	if connected {
		return nil
	}
	res, err = d.roundTrip(ctx, "web.get_hosts", nil)
	if err != nil {
		return err
	}
	// Each host is [id, address, port, status].
	var hosts [][]any
	if json.Unmarshal(res, &hosts) != nil || len(hosts) == 0 || len(hosts[0]) == 0 {
		return errors.New("Deluge's web UI has no daemon to connect to; add one in the web UI's Connection Manager")
	}
	id, _ := hosts[0][0].(string)
	if id == "" {
		return errors.New("Deluge's web UI listed a daemon Marquee cannot read")
	}
	if _, err := d.roundTrip(ctx, "web.connect", []any{id}); err != nil {
		return fmt.Errorf("could not connect Deluge's web UI to its daemon: %w", err)
	}
	return nil
}

// ---- label plugin ------------------------------------------------------------

// delugeLabel makes a category name acceptable to the Label plugin (lower-case
// letters, digits, dashes and underscores).
func delugeLabel(category string) string {
	var b strings.Builder
	for _, r := range strings.ToLower(category) {
		switch {
		case r >= 'a' && r <= 'z', r >= '0' && r <= '9', r == '-', r == '_':
			b.WriteRune(r)
		default:
			b.WriteByte('-')
		}
	}
	return b.String()
}

// labels reports the labels Deluge has, or ok=false when the Label plugin is
// not enabled. The answer is kept for a few minutes.
func (d *deluge) labels(ctx context.Context) (labels []string, ok bool, err error) {
	res, err := d.call(ctx, "label.get_labels")
	if err != nil {
		var re *rpcError
		if errors.As(err, &re) { // Deluge answered: the plugin is not there
			d.labelMu.Lock()
			d.labelKnown, d.labelOK, d.labelAt = true, false, time.Now()
			d.labelMu.Unlock()
			return nil, false, nil
		}
		return nil, false, err
	}
	_ = json.Unmarshal(res, &labels)
	d.labelMu.Lock()
	d.labelKnown, d.labelOK, d.labelAt = true, true, time.Now()
	d.labelMu.Unlock()
	return labels, true, nil
}

func (d *deluge) labelEnabled(ctx context.Context) (bool, error) {
	d.labelMu.Lock()
	known, okv, at := d.labelKnown, d.labelOK, d.labelAt
	d.labelMu.Unlock()
	if known && time.Since(at) < labelRecheck {
		return okv, nil
	}
	_, ok, err := d.labels(ctx)
	return ok, err
}

// ---- Adapter -----------------------------------------------------------------

func (d *deluge) Test(ctx context.Context) (string, error) {
	res, err := d.call(ctx, "daemon.get_version")
	if err != nil {
		var re *rpcError
		if errors.As(err, &re) { // Deluge 1.x called it daemon.info
			res, err = d.call(ctx, "daemon.info")
		}
		if err != nil {
			return "", err
		}
	}
	var version string
	_ = json.Unmarshal(res, &version)
	return version, nil
}

func (d *deluge) Add(ctx context.Context, in AddInput) (string, error) {
	var want string // the id we expect, computed from the torrent itself
	if len(in.TorrentFile) > 0 {
		m, err := ParseTorrent(in.TorrentFile)
		if err != nil {
			return "", fmt.Errorf("%w: %w", ErrRejected, err)
		}
		want = m.InfoHash
	} else if in.MagnetURL != "" {
		h, err := MagnetInfoHash(in.MagnetURL)
		if err != nil {
			return "", fmt.Errorf("%w: %w", ErrRejected, err)
		}
		want = h
	} else {
		return "", fmt.Errorf("%w: nothing to add", ErrRejected)
	}

	opts := map[string]any{"add_paused": in.Paused}
	if in.SavePath != "" {
		opts["download_location"] = in.SavePath
	}
	var (
		res json.RawMessage
		err error
	)
	if len(in.TorrentFile) > 0 {
		res, err = d.call(ctx, "core.add_torrent_file", "marquee.torrent", base64.StdEncoding.EncodeToString(in.TorrentFile), opts)
	} else {
		res, err = d.call(ctx, "core.add_torrent_magnet", in.MagnetURL, opts)
	}
	if err != nil {
		var re *rpcError
		switch {
		case errors.As(err, &re) && strings.Contains(strings.ToLower(re.Message), "already"):
			// Deluge has it. Keep going so the label still gets put on it.
		case errors.As(err, &re):
			return "", fmt.Errorf("%w: %w", ErrRejected, err)
		default:
			return "", err
		}
	}
	ref := want
	var id string
	if json.Unmarshal(res, &id) == nil && id != "" {
		ref = strings.ToLower(id)
	}

	if in.Category != "" {
		d.setLabel(ctx, ref, delugeLabel(in.Category))
	}
	return ref, nil
}

// setLabel puts the label on a torrent when the Label plugin is there. It is
// best effort: a download without its label is still tracked by its ref.
func (d *deluge) setLabel(ctx context.Context, ref, label string) {
	on, err := d.labelEnabled(ctx)
	if err != nil || !on {
		return
	}
	labels, _, err := d.labels(ctx)
	if err != nil {
		return
	}
	have := false
	for _, l := range labels {
		if l == label {
			have = true
			break
		}
	}
	if !have {
		if _, err := d.call(ctx, "label.add", label); err != nil {
			return
		}
	}
	_, _ = d.call(ctx, "label.set_torrent", ref, label)
}

// delugeKeys are the status fields Marquee reads.
var delugeKeys = []string{
	"name", "state", "progress", "total_size", "total_done", "download_payload_rate",
	"eta", "save_path", "is_finished", "message", "label",
}

type delugeTorrent struct {
	Name       string  `json:"name"`
	State      string  `json:"state"`
	Progress   float64 `json:"progress"` // 0 to 100
	TotalSize  int64   `json:"total_size"`
	TotalDone  int64   `json:"total_done"`
	Rate       float64 `json:"download_payload_rate"`
	ETA        float64 `json:"eta"`
	SavePath   string  `json:"save_path"`
	IsFinished bool    `json:"is_finished"`
	Message    string  `json:"message"`
}

func (d *deluge) List(ctx context.Context, category string) ([]Item, error) {
	filter := map[string]any{}
	if category != "" {
		on, err := d.labelEnabled(ctx)
		if err != nil {
			return nil, err
		}
		if on {
			filter["label"] = delugeLabel(category)
		}
	}
	res, err := d.call(ctx, "core.get_torrents_status", filter, delugeKeys)
	if err != nil {
		return nil, err
	}
	return parseDelugeList(res)
}

func parseDelugeList(res json.RawMessage) ([]Item, error) {
	var all map[string]delugeTorrent
	if err := json.Unmarshal(res, &all); err != nil {
		return nil, errors.New("Deluge sent a list Marquee cannot read")
	}
	items := make([]Item, 0, len(all))
	for id, t := range all {
		items = append(items, t.item(id))
	}
	return items, nil
}

func (t delugeTorrent) item(id string) Item {
	state, finished := mapDelugeState(t.State, t.IsFinished, t.Progress)
	it := Item{
		Ref:         strings.ToLower(id),
		Name:        t.Name,
		State:       state,
		Progress:    clamp01(t.Progress / 100),
		Size:        t.TotalSize,
		Done:        t.TotalDone,
		SpeedBps:    int64(t.Rate),
		ETA:         -1,
		SavePath:    t.SavePath,
		ContentPath: joinClientPath(t.SavePath, t.Name),
		Finished:    finished,
	}
	if t.ETA > 0 {
		it.ETA = int(t.ETA)
	}
	if finished {
		it.Progress, it.SpeedBps, it.ETA = 1, 0, 0
	}
	if state == StateFailed {
		it.Error = "Deluge reported an error with this download."
		if m := strings.TrimSpace(t.Message); m != "" && !strings.EqualFold(m, "ok") {
			it.Error = "Deluge says: " + m
		}
	}
	return it
}

// mapDelugeState turns Deluge's state into Marquee's. Seeding, and a finished
// torrent that was then paused or queued, count as complete.
func mapDelugeState(state string, isFinished bool, progress float64) (string, bool) {
	complete := isFinished || progress >= 100
	switch state {
	case "Seeding":
		return StateCompleted, true
	case "Error":
		return StateFailed, false
	case "Moving":
		return StateDownloading, false // finished downloading, still being moved
	case "Paused":
		if complete {
			return StateCompleted, true
		}
		return StatePaused, false
	case "Queued", "Checking", "Allocating":
		if complete {
			return StateCompleted, true
		}
		return StateQueued, false
	default: // Downloading
		if complete {
			return StateCompleted, true
		}
		return StateDownloading, false
	}
}

func (d *deluge) Remove(ctx context.Context, ref string, deleteData bool) error {
	_, err := d.call(ctx, "core.remove_torrent", ref, deleteData)
	var re *rpcError
	if errors.As(err, &re) {
		m := strings.ToLower(re.Message)
		if strings.Contains(m, "not in session") || strings.Contains(m, "does not exist") ||
			strings.Contains(m, "invalid torrent") || strings.Contains(m, "not found") {
			return nil // already gone
		}
	}
	return err
}

func (d *deluge) Pause(ctx context.Context, ref string) error {
	_, err := d.call(ctx, "core.pause_torrent", ref)
	var re *rpcError
	if errors.As(err, &re) && strings.Contains(strings.ToLower(re.Message), "method") {
		_, err = d.call(ctx, "core.pause_torrents", []string{ref})
	}
	return err
}

func (d *deluge) Resume(ctx context.Context, ref string) error {
	_, err := d.call(ctx, "core.resume_torrent", ref)
	var re *rpcError
	if errors.As(err, &re) && strings.Contains(strings.ToLower(re.Message), "method") {
		_, err = d.call(ctx, "core.resume_torrents", []string{ref})
	}
	return err
}
