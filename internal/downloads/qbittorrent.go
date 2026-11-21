package downloads

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"net/textproto"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"

	"github.com/BaileyPillon/marquee/internal/core/outbound"
)

// qbittorrent talks to qBittorrent's WebAPI v2 (4.x and 5.x).
//
// It signs in on demand: the session cookie is kept, and a 403 (the session
// expired, or qBittorrent restarted) signs in again and repeats the request
// once. A qBittorrent that does not ask for a login (localhost bypass) never
// sees a login at all.
type qbittorrent struct {
	base string
	user string
	pass string
	hc   *outbound.Client

	mu      sync.Mutex
	cookies []*http.Cookie

	// Which pause/resume endpoints this qBittorrent has: 0 not known yet,
	// 1 stop/start (v5), 2 pause/resume (v4).
	pauseAPI atomic.Int32
	// categories already known to exist, so Add does not ask every time.
	knownCats sync.Map
	// authFails counts sign-ins qBittorrent has refused in a row. qBittorrent
	// bans an address for an hour after five, so after two Marquee stops trying
	// on its own (the poll would otherwise knock every five seconds) until the
	// owner presses Test or edits the client.
	authFails atomic.Int32
}

const maxAuthFails = 2

var errAuthPaused = fmt.Errorf("%w: qBittorrent refused the sign-in twice, so Marquee stopped trying (qBittorrent bans an address after five); fix the username and password in Settings, then press Test", ErrAuth)

func newQBittorrent(cfg AdapterConfig) (*qbittorrent, error) {
	base, err := normalizeBase(cfg.URL)
	if err != nil {
		return nil, err
	}
	return &qbittorrent{base: base, user: cfg.Username, pass: cfg.Password, hc: cfg.HTTP}, nil
}

var _ Adapter = (*qbittorrent)(nil)

// ---- transport ---------------------------------------------------------------

type qbtRequest struct {
	method      string
	path        string // under /api/v2
	query       url.Values
	body        []byte
	contentType string
}

func (q *qbittorrent) once(ctx context.Context, r qbtRequest) (int, []byte, error) {
	u := q.base + "/api/v2" + r.path
	if len(r.query) > 0 {
		u += "?" + r.query.Encode()
	}
	var rd io.Reader
	if r.body != nil {
		rd = bytes.NewReader(r.body)
	}
	req, err := http.NewRequestWithContext(ctx, r.method, u, rd)
	if err != nil {
		return 0, nil, errors.New("qBittorrent: the address cannot be used")
	}
	if r.contentType != "" {
		req.Header.Set("Content-Type", r.contentType)
	}
	q.mu.Lock()
	for _, c := range q.cookies {
		req.AddCookie(c)
	}
	q.mu.Unlock()
	status, _, reply, err := send(q.hc, req)
	return status, reply, err
}

// do sends a request, signing in first when there is a username and no session
// yet, and again (once) when qBittorrent answers 403.
func (q *qbittorrent) do(ctx context.Context, r qbtRequest) (int, []byte, error) {
	q.mu.Lock()
	needLogin := len(q.cookies) == 0 && q.user != ""
	q.mu.Unlock()
	if needLogin {
		if err := q.signIn(ctx); err != nil {
			return 0, nil, err
		}
	}
	status, body, err := q.once(ctx, r)
	if err != nil || status != http.StatusForbidden {
		return status, body, err
	}
	if err := q.signIn(ctx); err != nil {
		return 0, nil, err
	}
	status, body, err = q.once(ctx, r)
	if err == nil && status == http.StatusForbidden {
		return status, body, statusError("qBittorrent", status)
	}
	return status, body, err
}

// signIn signs in, unless qBittorrent has already refused the login too often.
func (q *qbittorrent) signIn(ctx context.Context) error {
	if q.authFails.Load() >= maxAuthFails {
		return errAuthPaused
	}
	err := q.login(ctx)
	switch {
	case err == nil:
		q.authFails.Store(0)
	case errors.Is(err, ErrAuth):
		q.authFails.Add(1)
	}
	return err
}

func (q *qbittorrent) login(ctx context.Context) error {
	form := url.Values{"username": {q.user}, "password": {q.pass}}
	u := q.base + "/api/v2/auth/login"
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, u, strings.NewReader(form.Encode()))
	if err != nil {
		return errors.New("qBittorrent: the address cannot be used")
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	resp, err := q.hc.Do(req)
	if err != nil {
		return unreachable(err)
	}
	defer resp.Body.Close()
	reply, _ := io.ReadAll(io.LimitReader(resp.Body, 256))
	text := strings.TrimSpace(string(reply))

	// Four versions' worth of answers: 4.x says 200 "Ok." or 200 "Fails."; 5.x
	// says 204 (no body) for success and 401 for a wrong password.
	switch {
	case resp.StatusCode == http.StatusForbidden:
		return fmt.Errorf("%w: qBittorrent has banned this address for a while after too many wrong logins", ErrAuth)
	case resp.StatusCode == http.StatusUnauthorized:
		return fmt.Errorf("%w: wrong username or password", ErrAuth)
	case resp.StatusCode == http.StatusNoContent, resp.StatusCode == http.StatusOK && text == "Ok.":
		// signed in
	case resp.StatusCode == http.StatusOK:
		return fmt.Errorf("%w: wrong username or password", ErrAuth)
	default:
		return statusError("qBittorrent", resp.StatusCode)
	}
	q.mu.Lock()
	q.cookies = resp.Cookies()
	q.mu.Unlock()
	return nil
}

func (q *qbittorrent) form(ctx context.Context, path string, v url.Values) (int, []byte, error) {
	return q.do(ctx, qbtRequest{
		method: http.MethodPost, path: path,
		body: []byte(v.Encode()), contentType: "application/x-www-form-urlencoded",
	})
}

// ---- Adapter -----------------------------------------------------------------

func (q *qbittorrent) Test(ctx context.Context) (string, error) {
	q.authFails.Store(0) // pressing Test is the owner asking for another try
	status, body, err := q.do(ctx, qbtRequest{method: http.MethodGet, path: "/app/version"})
	if err != nil {
		return "", err
	}
	if status != http.StatusOK {
		return "", statusError("qBittorrent", status)
	}
	return strings.TrimSpace(string(body)), nil
}

func (q *qbittorrent) Add(ctx context.Context, in AddInput) (string, error) {
	ref, err := q.refFor(in)
	if err != nil {
		return "", err
	}
	if in.Category != "" {
		if err := q.ensureCategory(ctx, in.Category); err != nil {
			return "", err
		}
	}

	var buf bytes.Buffer
	mw := multipart.NewWriter(&buf)
	field := func(k, v string) { _ = mw.WriteField(k, v) }
	if len(in.TorrentFile) > 0 {
		h := textproto.MIMEHeader{}
		h.Set("Content-Disposition", `form-data; name="torrents"; filename="marquee.torrent"`)
		h.Set("Content-Type", "application/x-bittorrent")
		part, err := mw.CreatePart(h)
		if err != nil {
			return "", err
		}
		_, _ = part.Write(in.TorrentFile)
	} else {
		field("urls", in.MagnetURL)
	}
	if in.Category != "" {
		field("category", in.Category)
	}
	if in.SavePath != "" {
		field("savepath", in.SavePath)
		// With automatic torrent management on, qBittorrent ignores savepath.
		field("autoTMM", "false")
	}
	// qBittorrent 5 renamed "paused" to "stopped". Each version ignores the
	// name it does not know.
	field("paused", strconv.FormatBool(in.Paused))
	field("stopped", strconv.FormatBool(in.Paused))
	if err := mw.Close(); err != nil {
		return "", err
	}

	status, body, err := q.do(ctx, qbtRequest{
		method: http.MethodPost, path: "/torrents/add",
		body: buf.Bytes(), contentType: mw.FormDataContentType(),
	})
	if err != nil {
		return "", err
	}
	ok, ids := parseQbtAdd(status, body)
	if ok {
		if ref == "" && len(ids) > 0 {
			ref = ids[0]
		}
		if ref == "" {
			return "", fmt.Errorf("%w: qBittorrent took it but did not say what it is called", ErrRejected)
		}
		return ref, nil
	}
	// A torrent that is already there is reported as a failure; that is fine.
	if ref != "" {
		if _, found, err := q.one(ctx, ref); err == nil && found {
			return ref, nil
		}
	}
	if status == http.StatusUnsupportedMediaType {
		return "", fmt.Errorf("%w: qBittorrent says that is not a valid torrent file", ErrRejected)
	}
	if status != http.StatusOK && status != http.StatusConflict && status != http.StatusBadRequest {
		return "", statusError("qBittorrent", status)
	}
	return "", fmt.Errorf("%w: qBittorrent would not add it (is the torrent file damaged?)", ErrRejected)
}

// refFor works out the info-hash qBittorrent will know the torrent by.
func (q *qbittorrent) refFor(in AddInput) (string, error) {
	if len(in.TorrentFile) > 0 {
		m, err := ParseTorrent(in.TorrentFile)
		if err != nil {
			return "", fmt.Errorf("%w: %w", ErrRejected, err)
		}
		return m.InfoHash, nil
	}
	if in.MagnetURL == "" {
		return "", fmt.Errorf("%w: nothing to add", ErrRejected)
	}
	h, err := MagnetInfoHash(in.MagnetURL)
	if err != nil {
		return "", fmt.Errorf("%w: %w", ErrRejected, err)
	}
	return h, nil
}

// parseQbtAdd reads the answer of torrents/add: older versions say "Ok." or
// "Fails."; newer ones answer JSON with counts (and the ids that were added).
func parseQbtAdd(status int, body []byte) (ok bool, ids []string) {
	text := strings.TrimSpace(string(body))
	if status == http.StatusOK && text == "Ok." {
		return true, nil
	}
	if strings.HasPrefix(text, "{") {
		var r struct {
			Success int      `json:"success_count"`
			Failure int      `json:"failure_count"`
			Pending int      `json:"pending_count"`
			Added   []string `json:"added_torrent_ids"`
		}
		if json.Unmarshal(body, &r) == nil && status == http.StatusOK && (r.Success+r.Pending > 0 || r.Failure == 0) {
			for _, id := range r.Added {
				ids = append(ids, strings.ToLower(id))
			}
			return true, ids
		}
	}
	return false, nil
}

func (q *qbittorrent) ensureCategory(ctx context.Context, name string) error {
	if _, known := q.knownCats.Load(name); known {
		return nil
	}
	status, body, err := q.do(ctx, qbtRequest{method: http.MethodGet, path: "/torrents/categories"})
	if err != nil {
		return err
	}
	if status == http.StatusOK {
		var cats map[string]json.RawMessage
		if json.Unmarshal(body, &cats) == nil {
			if _, ok := cats[name]; ok {
				q.knownCats.Store(name, true)
				return nil
			}
		}
	}
	status, _, err = q.form(ctx, "/torrents/createCategory", url.Values{"category": {name}})
	if err != nil {
		return err
	}
	switch status {
	case http.StatusOK, http.StatusConflict: // 409: it is there already
		q.knownCats.Store(name, true)
		return nil
	case http.StatusBadRequest:
		return fmt.Errorf("%w: qBittorrent does not accept %q as a category name", ErrRejected, name)
	}
	return statusError("qBittorrent", status)
}

// qbtTorrent is the part of a torrents/info entry Marquee reads.
type qbtTorrent struct {
	Hash        string  `json:"hash"`
	Name        string  `json:"name"`
	State       string  `json:"state"`
	Progress    float64 `json:"progress"`
	Size        int64   `json:"size"`
	AmountLeft  int64   `json:"amount_left"`
	DLSpeed     int64   `json:"dlspeed"`
	ETA         int64   `json:"eta"`
	SavePath    string  `json:"save_path"`
	ContentPath string  `json:"content_path"`
}

func (q *qbittorrent) List(ctx context.Context, category string) ([]Item, error) {
	status, body, err := q.do(ctx, qbtRequest{
		method: http.MethodGet, path: "/torrents/info",
		query: url.Values{"category": {category}},
	})
	if err != nil {
		return nil, err
	}
	if status != http.StatusOK {
		return nil, statusError("qBittorrent", status)
	}
	return parseQbtList(body)
}

func parseQbtList(body []byte) ([]Item, error) {
	var list []qbtTorrent
	if err := json.Unmarshal(body, &list); err != nil {
		return nil, errors.New("qBittorrent sent a list Marquee cannot read")
	}
	items := make([]Item, 0, len(list))
	for _, t := range list {
		items = append(items, t.item())
	}
	return items, nil
}

// one reads a single torrent by hash.
func (q *qbittorrent) one(ctx context.Context, hash string) (Item, bool, error) {
	status, body, err := q.do(ctx, qbtRequest{
		method: http.MethodGet, path: "/torrents/info",
		query: url.Values{"hashes": {hash}},
	})
	if err != nil {
		return Item{}, false, err
	}
	if status != http.StatusOK {
		return Item{}, false, statusError("qBittorrent", status)
	}
	items, err := parseQbtList(body)
	if err != nil || len(items) == 0 {
		return Item{}, false, err
	}
	return items[0], true, nil
}

func (t qbtTorrent) item() Item {
	state, finished := mapQbtState(t.State)
	it := Item{
		Ref:         strings.ToLower(t.Hash),
		Name:        t.Name,
		State:       state,
		Progress:    clamp01(t.Progress),
		Size:        t.Size,
		Done:        max(t.Size-t.AmountLeft, 0),
		SpeedBps:    t.DLSpeed,
		ETA:         -1,
		SavePath:    t.SavePath,
		ContentPath: t.ContentPath,
		Finished:    finished,
	}
	// 8640000 seconds is qBittorrent's way of saying "never".
	if t.ETA >= 0 && t.ETA < 8640000 {
		it.ETA = int(t.ETA)
	}
	if it.ContentPath == "" { // before 4.3.2 there was no content_path
		it.ContentPath = joinClientPath(t.SavePath, t.Name)
	}
	if finished {
		it.Progress, it.SpeedBps, it.ETA = 1, 0, 0
	}
	if state == StateFailed {
		it.Error = qbtErrorText(t.State)
	}
	return it
}

// mapQbtState turns qBittorrent's state names into Marquee's. The second result
// says the download is complete (seeding, or finished and then paused).
func mapQbtState(s string) (state string, finished bool) {
	switch s {
	case "uploading", "stalledUP", "queuedUP", "forcedUP", "checkingUP", "pausedUP", "stoppedUP":
		return StateCompleted, true
	case "error", "missingFiles":
		return StateFailed, false
	case "stalledDL":
		return StateStalled, false
	case "queuedDL", "checkingDL", "checkingResumeData", "allocating":
		return StateQueued, false
	case "pausedDL", "stoppedDL":
		return StatePaused, false
	default: // downloading, forcedDL, metaDL, forcedMetaDL, moving, unknown
		return StateDownloading, false
	}
}

func qbtErrorText(state string) string {
	if state == "missingFiles" {
		return "qBittorrent cannot find the downloaded files."
	}
	return "qBittorrent reported an error with this download."
}

func clamp01(f float64) float64 {
	switch {
	case f < 0:
		return 0
	case f > 1:
		return 1
	}
	return f
}

func (q *qbittorrent) Remove(ctx context.Context, ref string, deleteData bool) error {
	status, _, err := q.form(ctx, "/torrents/delete", url.Values{
		"hashes": {ref}, "deleteFiles": {strconv.FormatBool(deleteData)},
	})
	if err != nil {
		return err
	}
	if status != http.StatusOK {
		return statusError("qBittorrent", status)
	}
	return nil
}

func (q *qbittorrent) Pause(ctx context.Context, ref string) error {
	return q.pauseResume(ctx, ref, "stop", "pause")
}

func (q *qbittorrent) Resume(ctx context.Context, ref string) error {
	return q.pauseResume(ctx, ref, "start", "resume")
}

// pauseResume tries the version 5 endpoint (stop, start) first and falls back
// to the version 4 one (pause, resume) when qBittorrent does not have it. It
// remembers which one worked.
func (q *qbittorrent) pauseResume(ctx context.Context, ref, v5, v4 string) error {
	v := url.Values{"hashes": {ref}}
	order := []struct {
		api  int32
		name string
	}{{1, v5}, {2, v4}}
	if q.pauseAPI.Load() == 2 {
		order[0], order[1] = order[1], order[0]
	}
	for _, o := range order {
		status, _, err := q.form(ctx, "/torrents/"+o.name, v)
		if err != nil {
			return err
		}
		switch status {
		case http.StatusOK:
			q.pauseAPI.Store(o.api)
			return nil
		case http.StatusNotFound: // this qBittorrent does not have that endpoint
			continue
		default:
			return statusError("qBittorrent", status)
		}
	}
	return errors.New("qBittorrent has neither the stop/start nor the pause/resume endpoints")
}
