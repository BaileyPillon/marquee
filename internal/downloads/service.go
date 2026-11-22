package downloads

import (
	"context"
	"database/sql"
	"errors"
	"log/slog"
	"net/url"
	"strings"
	"sync"
	"sync/atomic"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/BaileyPillon/marquee/internal/core/events"
	"github.com/BaileyPillon/marquee/internal/core/outbound"
	"github.com/BaileyPillon/marquee/internal/core/pathmap"
	"github.com/BaileyPillon/marquee/internal/core/secrets"
	"github.com/BaileyPillon/marquee/internal/grab"
	"github.com/BaileyPillon/marquee/internal/settings"
)

// Options are what a Service needs besides the database.
type Options struct {
	Logger *slog.Logger
	// Hub receives the "downloads" events. Optional.
	Hub *events.Hub
	// HTTP is the one outbound client every call to a client or an indexer uses.
	HTTP *outbound.Client
	// Box seals and opens the clients' passwords and API keys.
	Box *secrets.Box
	// Adapters builds the adapter for a client. Tests replace it; the default
	// is NewAdapter.
	Adapters AdapterFactory
	// Now is a test hook.
	Now func() time.Time
}

// Service is the downloads module.
type Service struct {
	store   *Store
	log     *slog.Logger
	hub     *events.Hub
	hc      *outbound.Client
	box     *secrets.Box
	factory AdapterFactory
	now     func() time.Time

	adMu     sync.Mutex
	adapters map[int64]adapterEntry

	handlerMu sync.RWMutex
	handler   grab.CompletedHandler

	// Polling state. pollMu keeps polls from overlapping.
	pollMu   sync.Mutex
	lastPoll time.Time
	misses   map[int64]int
	active   atomic.Bool // something was in progress at the last look
}

type adapterEntry struct {
	sig string
	a   Adapter
}

var _ grab.Grabber = (*Service)(nil)

// NewService builds the service on the main database.
func NewService(d *sql.DB, opts Options) *Service {
	if opts.Logger == nil {
		opts.Logger = slog.Default()
	}
	if opts.Adapters == nil {
		opts.Adapters = NewAdapter
	}
	if opts.Now == nil {
		opts.Now = time.Now
	}
	s := &Service{
		store: NewStore(d), log: opts.Logger, hub: opts.Hub, hc: opts.HTTP, box: opts.Box,
		factory: opts.Adapters, now: opts.Now,
		adapters: map[int64]adapterEntry{}, misses: map[int64]int{},
	}
	s.active.Store(true) // the first tick after a start looks at once
	return s
}

// SetCompletedHandler sets who is told when a download finishes in its client
// (the importer). Nil is allowed: nobody is told.
func (s *Service) SetCompletedHandler(h grab.CompletedHandler) {
	s.handlerMu.Lock()
	s.handler = h
	s.handlerMu.Unlock()
}

func (s *Service) completedHandler() grab.CompletedHandler {
	s.handlerMu.RLock()
	defer s.handlerMu.RUnlock()
	return s.handler
}

func (s *Service) publish(typ string, d Download) {
	if s.hub == nil {
		return
	}
	// The stream reaches every signed-in person, so it never carries a path:
	// importStep is only the word for where the import is up to.
	s.hub.Publish(Topic, map[string]any{
		"type": typ, "id": d.ID, "titleId": d.TitleID, "state": d.State, "progress": d.Progress,
		"importStep": d.ImportStep,
	})
}

// ---- clients: what the owner sees --------------------------------------------

// ClientView is a download client as the API shows it. Secrets are masked: a
// value starting with settings.MaskPrefix means "set"; sending it back
// unchanged keeps the stored one.
type ClientView struct {
	ID          int64      `json:"id"`
	Name        string     `json:"name"`
	Kind        string     `json:"kind"`
	URL         string     `json:"url"`
	Username    string     `json:"username"`
	Password    string     `json:"password"`
	APIKey      string     `json:"apiKey"`
	Category    string     `json:"category"`
	SavePath    string     `json:"savePath"`
	Enabled     bool       `json:"enabled"`
	Priority    int        `json:"priority"`
	LastOKAt    *time.Time `json:"lastOkAt"`
	LastError   string     `json:"lastError"`
	LastErrorAt *time.Time `json:"lastErrorAt"`
	CreatedAt   time.Time  `json:"createdAt"`
	// PathMap says how the client's folders look from Marquee's side, for a
	// client in a container or on another machine: [{"from": "/downloads",
	// "to": "D:\\dl"}]. The importer uses it to find a finished download.
	PathMap []pathmap.Rule `json:"pathMap"`
}

func (s *Service) view(c clientRow) ClientView {
	pm := c.PathMap
	if pm == nil {
		pm = []pathmap.Rule{}
	}
	return ClientView{
		ID: c.ID, Name: c.Name, Kind: c.Kind, URL: c.URL, Username: c.Username,
		Password: s.mask(c.PasswordSealed), APIKey: s.mask(c.APIKeySealed),
		Category: c.Category, SavePath: c.SavePath, Enabled: c.Enabled, Priority: c.Priority,
		LastOKAt: c.LastOKAt, LastError: c.LastError, LastErrorAt: c.LastErrorAt, CreatedAt: c.CreatedAt,
		PathMap: pm,
	}
}

// mask shows a stored secret the way settings does: dots, plus the last four
// characters of a long one. A secret that cannot be opened shows plain dots.
func (s *Service) mask(sealed string) string {
	if sealed == "" {
		return ""
	}
	plain, err := s.box.Open(sealed)
	if err != nil {
		return settings.MaskPrefix
	}
	return settings.Mask(plain)
}

// ClientInput is what the owner sends to add or change a client. When changing,
// a field left out keeps its value. A password or API key that is left out or
// still masked keeps the stored secret; an empty one clears it.
type ClientInput struct {
	Name     *string `json:"name"`
	Kind     *string `json:"kind"`
	URL      *string `json:"url"`
	Username *string `json:"username"`
	Password *string `json:"password"`
	APIKey   *string `json:"apiKey"`
	Category *string `json:"category"`
	SavePath *string `json:"savePath"`
	Enabled  *bool   `json:"enabled"`
	Priority *int    `json:"priority"`
	// PathMap replaces the client's path rules when sent (an empty list clears
	// them); left out, the rules stay.
	PathMap *[]pathmap.Rule `json:"pathMap"`
}

const (
	maxNameRunes     = 100
	maxCategoryRunes = 64
	maxPathRunes     = 500
	maxCredRunes     = 500
	maxPriority      = 999
)

// Clients lists every client, enabled or not.
func (s *Service) Clients(ctx context.Context) ([]ClientView, error) {
	rows, err := s.store.listClients(ctx)
	if err != nil {
		return nil, err
	}
	out := make([]ClientView, len(rows))
	for i, c := range rows {
		out[i] = s.view(c)
	}
	return out, nil
}

// AddClient creates a client.
func (s *Service) AddClient(ctx context.Context, in ClientInput) (ClientView, error) {
	c := clientRow{Category: DefaultCategory, Enabled: true, Priority: 1}
	if in.Kind == nil || strings.TrimSpace(*in.Kind) == "" {
		return ClientView{}, &InvalidError{Message: "Say which download app this is."}
	}
	c.Kind = strings.TrimSpace(*in.Kind)
	if !SupportedKind(c.Kind) {
		if c.Kind == KindTransmission || c.Kind == KindNZBGet {
			return ClientView{}, &InvalidError{Message: "Marquee cannot talk to " + displayKind(c.Kind) + " yet."}
		}
		return ClientView{}, &InvalidError{Message: "The kind must be qbittorrent, deluge or sabnzbd."}
	}
	if err := s.applyInput(&c, in, nil); err != nil {
		return ClientView{}, err
	}
	if c.Name == "" {
		return ClientView{}, &InvalidError{Message: "Give the client a name."}
	}
	if c.URL == "" {
		return ClientView{}, &InvalidError{Message: "Give the address of the client."}
	}
	if c.Kind == KindSABnzbd && c.APIKeySealed == "" {
		return ClientView{}, &InvalidError{Message: "SABnzbd needs its API key (Config > General in SABnzbd)."}
	}
	row, err := s.store.insertClient(ctx, c, s.now())
	if err != nil {
		return ClientView{}, err
	}
	return s.view(row), nil
}

// UpdateClient changes a client. Its kind cannot change.
func (s *Service) UpdateClient(ctx context.Context, id int64, in ClientInput) (ClientView, error) {
	old, err := s.store.getClient(ctx, id)
	if err != nil {
		return ClientView{}, err
	}
	if in.Kind != nil && strings.TrimSpace(*in.Kind) != old.Kind {
		return ClientView{}, &InvalidError{Message: "The kind of a client cannot be changed. Add a new client instead."}
	}
	c := old
	if err := s.applyInput(&c, in, &old); err != nil {
		return ClientView{}, err
	}
	if c.Name == "" || c.URL == "" {
		return ClientView{}, &InvalidError{Message: "A client needs a name and an address."}
	}
	if c.Kind == KindSABnzbd && c.APIKeySealed == "" {
		return ClientView{}, &InvalidError{Message: "SABnzbd needs its API key."}
	}
	if err := s.store.updateClient(ctx, c); err != nil {
		return ClientView{}, err
	}
	s.dropAdapter(id)
	row, err := s.store.getClient(ctx, id)
	if err != nil {
		return ClientView{}, err
	}
	return s.view(row), nil
}

// applyInput copies the fields the owner sent onto c, checking each. old is the
// stored client when changing one (nil when adding).
func (s *Service) applyInput(c *clientRow, in ClientInput, old *clientRow) error {
	if in.Name != nil {
		name := strings.TrimSpace(*in.Name)
		if name == "" || utf8.RuneCountInString(name) > maxNameRunes || hasControl(name) {
			return &InvalidError{Message: "The name must be 1 to 100 characters."}
		}
		c.Name = name
	}
	if in.URL != nil {
		base, err := normalizeBase(*in.URL)
		if err != nil {
			return err
		}
		c.URL = base
	}
	if in.Username != nil {
		u := strings.TrimSpace(*in.Username)
		if utf8.RuneCountInString(u) > maxCredRunes || hasControl(u) {
			return &InvalidError{Message: "That username is not valid."}
		}
		c.Username = u
	}
	if in.Category != nil {
		cat := strings.TrimSpace(*in.Category)
		if cat == "" {
			cat = DefaultCategory
		}
		if utf8.RuneCountInString(cat) > maxCategoryRunes || hasControl(cat) {
			return &InvalidError{Message: "The category must be 1 to 64 characters."}
		}
		c.Category = cat
	}
	if in.SavePath != nil {
		p := strings.TrimSpace(*in.SavePath)
		if utf8.RuneCountInString(p) > maxPathRunes || hasControl(p) {
			return &InvalidError{Message: "That save path is not valid."}
		}
		c.SavePath = p
	}
	if in.Enabled != nil {
		c.Enabled = *in.Enabled
	}
	if in.Priority != nil {
		if *in.Priority < 0 || *in.Priority > maxPriority {
			return &InvalidError{Message: "The priority must be a number from 0 to 999 (lowest is used first)."}
		}
		c.Priority = *in.Priority
	}
	if in.PathMap != nil {
		rules, err := pathmap.Clean(*in.PathMap)
		if err != nil {
			return &InvalidError{Message: "The path rules are not valid: " + err.Error() + "."}
		}
		c.PathMap = rules
	}

	var err error
	if c.PasswordSealed, err = s.sealSecret(in.Password, c.PasswordSealed); err != nil {
		return err
	}
	if c.APIKeySealed, err = s.sealSecret(in.APIKey, c.APIKeySealed); err != nil {
		return err
	}

	// A stored secret is never sent to a different host: someone who can edit
	// the address could otherwise collect it. Type it again for the new address.
	if old != nil && hostOf(c.URL) != hostOf(old.URL) {
		if (old.PasswordSealed != "" && c.PasswordSealed == old.PasswordSealed) ||
			(old.APIKeySealed != "" && c.APIKeySealed == old.APIKeySealed) {
			return &InvalidError{Message: "You changed the address, so type the password or API key again. Marquee does not send a saved one to a new address."}
		}
	}
	return nil
}

// sealSecret decides what to store for a secret the owner sent: nothing sent or
// a masked value keeps the stored one, empty clears it, anything else is sealed.
func (s *Service) sealSecret(in *string, stored string) (string, error) {
	switch {
	case in == nil, in != nil && strings.HasPrefix(*in, settings.MaskPrefix):
		return stored, nil
	case *in == "":
		return "", nil
	case utf8.RuneCountInString(*in) > maxCredRunes:
		return "", &InvalidError{Message: "That password or key is too long."}
	}
	return s.box.Seal(*in)
}

func hostOf(raw string) string {
	u, err := url.Parse(raw)
	if err != nil {
		return ""
	}
	return strings.ToLower(u.Host)
}

func hasControl(s string) bool {
	for _, r := range s {
		if unicode.IsControl(r) {
			return true
		}
	}
	return false
}

// DeleteClient removes a client. A client that still has downloads in progress
// is refused (ErrClientInUse); finished downloads go with it.
func (s *Service) DeleteClient(ctx context.Context, id int64) error {
	if _, err := s.store.getClient(ctx, id); err != nil {
		return err
	}
	n, err := s.store.countLive(ctx, id)
	if err != nil {
		return err
	}
	if n > 0 {
		return ErrClientInUse
	}
	if err := s.store.deleteClient(ctx, id); err != nil {
		return err
	}
	s.dropAdapter(id)
	return nil
}

// TestResult is the outcome of testing a client.
type TestResult struct {
	OK      bool   `json:"ok"`
	Version string `json:"version"`
	Error   string `json:"error"`
}

// TestClient signs in to a client and asks its version. A client that does not
// answer is not an error here: the result says so.
func (s *Service) TestClient(ctx context.Context, id int64) (TestResult, error) {
	c, err := s.store.getClient(ctx, id)
	if err != nil {
		return TestResult{}, err
	}
	ctx, cancel := context.WithTimeout(ctx, clientTimeout)
	defer cancel()
	a, err := s.adapter(c)
	var version string
	if err == nil {
		version, err = a.Test(ctx)
	}
	if err != nil {
		msg := PlainError(err)
		_ = s.store.clientFailed(context.WithoutCancel(ctx), id, msg, s.now())
		return TestResult{OK: false, Error: msg}, nil
	}
	_ = s.store.clientOK(context.WithoutCancel(ctx), id, s.now())
	return TestResult{OK: true, Version: version}, nil
}

// ---- adapters ------------------------------------------------------------------

var errSecretUnreadable = errors.New("the saved password or API key cannot be read (was the master key file replaced?); type it again in Settings")

func (s *Service) open(sealed string) (string, error) {
	if sealed == "" {
		return "", nil
	}
	plain, err := s.box.Open(sealed)
	if err != nil {
		return "", errSecretUnreadable
	}
	return plain, nil
}

// adapter returns the adapter for a client, building it when the client is new
// or was edited. Adapters are kept so a signed-in session is reused.
func (s *Service) adapter(c clientRow) (Adapter, error) {
	sig := strings.Join([]string{c.Kind, c.URL, c.Username, c.PasswordSealed, c.APIKeySealed}, "\x00")
	s.adMu.Lock()
	defer s.adMu.Unlock()
	if e, ok := s.adapters[c.ID]; ok && e.sig == sig {
		return e.a, nil
	}
	pw, err := s.open(c.PasswordSealed)
	if err != nil {
		return nil, err
	}
	key, err := s.open(c.APIKeySealed)
	if err != nil {
		return nil, err
	}
	a, err := s.factory(AdapterConfig{Kind: c.Kind, URL: c.URL, Username: c.Username, Password: pw, APIKey: key, HTTP: s.hc})
	if err != nil {
		return nil, err
	}
	s.adapters[c.ID] = adapterEntry{sig: sig, a: a}
	return a, nil
}

func (s *Service) dropAdapter(id int64) {
	s.adMu.Lock()
	delete(s.adapters, id)
	s.adMu.Unlock()
}

// ---- downloads: what the owner sees --------------------------------------------

// List filters.
const (
	FilterActive  = "active"
	FilterHistory = "history"
	FilterAll     = "all"

	DefaultLimit = 50
	MaxLimit     = 200
)

// ListFilter selects downloads. State is "active" (not over yet), "history"
// (imported, failed or removed) or "all"; empty means active.
type ListFilter struct {
	State  string
	Limit  int
	Offset int
}

// List returns downloads and the total that match.
func (s *Service) List(ctx context.Context, f ListFilter) ([]View, int, error) {
	var (
		states []string
		order  string
	)
	switch f.State {
	case "", FilterActive:
		states, order = openStates, "d.id DESC"
	case FilterHistory:
		states, order = terminalStates, "d.updated_at DESC, d.id DESC"
	case FilterAll:
		order = "d.id DESC"
	default:
		return nil, 0, &InvalidError{Message: "state must be active, history or all."}
	}
	if f.Limit <= 0 {
		f.Limit = DefaultLimit
	}
	f.Limit = min(f.Limit, MaxLimit)
	f.Offset = max(f.Offset, 0)
	return s.store.listViews(ctx, states, order, f.Limit, f.Offset)
}

// Get returns one download as the API shows it.
func (s *Service) Get(ctx context.Context, id int64) (View, error) {
	return s.store.getView(ctx, id)
}

// Download returns the stored row, for the importer.
func (s *Service) Download(ctx context.Context, id int64) (Download, error) {
	return s.store.getDownload(ctx, id)
}

// SetState moves a download to a state, for the importer: importing, imported
// or failed (errText says why). The poll never moves a download out of those.
func (s *Service) SetState(ctx context.Context, id int64, state, errText string) error {
	switch state {
	case StateImporting, StateImported, StateFailed, StateCompleted, StateRemoved:
	default:
		return &InvalidError{Message: "That is not a state a download can be moved to."}
	}
	if err := s.store.setState(ctx, id, state, errText, s.now()); err != nil {
		return err
	}
	if d, err := s.store.getDownload(ctx, id); err == nil {
		s.publish(eventFor(state), d)
	}
	return nil
}

// SetImportStep is how the importer says where it is: step is one of the Step
// constants ("" clears it) and target the path the files are going to (empty
// keeps the one already stored). It only counts while the download is importing.
// Each change is published on the stream as a "state" event so the Downloads
// screen moves along.
func (s *Service) SetImportStep(ctx context.Context, id int64, step, target string) error {
	if !validStep(step) {
		return &InvalidError{Message: "That is not an import step."}
	}
	ok, err := s.store.setImportStep(ctx, id, step, target, s.now())
	if err != nil {
		return err
	}
	if !ok {
		return nil // not importing (any more): nothing to say
	}
	if d, err := s.store.getDownload(ctx, id); err == nil {
		s.publish("state", d)
	}
	return nil
}

func eventFor(state string) string {
	switch state {
	case StateCompleted:
		return "completed"
	case StateFailed:
		return "failed"
	case StateRemoved:
		return "removed"
	}
	return "state"
}

// Summary counts what is going on and says which clients answer.
type Summary struct {
	Downloading int            `json:"downloading"`
	Importing   int            `json:"importing"`
	Queued      int            `json:"queued"`
	Clients     []ClientHealth `json:"clients"`
}

// ClientHealth is one enabled client's last known health.
type ClientHealth struct {
	ID        int64  `json:"id"`
	Name      string `json:"name"`
	Kind      string `json:"kind"`
	OK        bool   `json:"ok"`
	LastError string `json:"lastError"`
}

// Summary returns the counts for the Downloads header and the health badges:
// downloading counts downloading and stalled, importing counts finished ones
// waiting for or in the importer, queued counts queued ones.
func (s *Service) Summary(ctx context.Context) (Summary, error) {
	counts, err := s.store.stateCounts(ctx)
	if err != nil {
		return Summary{}, err
	}
	clients, err := s.store.enabledClients(ctx)
	if err != nil {
		return Summary{}, err
	}
	sum := Summary{
		Downloading: counts[StateDownloading] + counts[StateStalled],
		Importing:   counts[StateCompleted] + counts[StateImporting],
		Queued:      counts[StateQueued],
		Clients:     make([]ClientHealth, 0, len(clients)),
	}
	for _, c := range clients {
		sum.Clients = append(sum.Clients, ClientHealth{
			ID: c.ID, Name: c.Name, Kind: c.Kind, OK: c.LastError == "" && c.LastOKAt != nil, LastError: c.LastError,
		})
	}
	return sum, nil
}

// ---- pause, resume, remove -----------------------------------------------------

// clientError turns what an adapter returned into an *Error the API can map.
func clientError(err error) error {
	switch {
	case errors.Is(err, ErrRejected):
		return wrap(ErrClientRejected, err)
	default:
		return wrap(ErrClientUnreachable, err)
	}
}

// adapterFor finds the adapter of a download's client.
func (s *Service) adapterFor(ctx context.Context, d Download) (Adapter, error) {
	c, err := s.store.getClient(ctx, d.ClientID)
	if err != nil {
		return nil, err
	}
	a, err := s.adapter(c)
	if err != nil {
		return nil, clientError(err)
	}
	return a, nil
}

// Pause pauses a download that is still being fetched.
func (s *Service) Pause(ctx context.Context, id int64) (View, error) {
	return s.pauseResume(ctx, id, true)
}

// Resume resumes a paused download.
func (s *Service) Resume(ctx context.Context, id int64) (View, error) {
	return s.pauseResume(ctx, id, false)
}

func (s *Service) pauseResume(ctx context.Context, id int64, pause bool) (View, error) {
	d, err := s.store.getDownload(ctx, id)
	if err != nil {
		return View{}, err
	}
	switch {
	case pause && !inList([]string{StateQueued, StateDownloading, StateStalled}, d.State):
		return View{}, &Error{Code: ErrInvalidState.Code, Message: "That download is " + d.State + ", so it cannot be paused."}
	case !pause && d.State != StatePaused:
		return View{}, &Error{Code: ErrInvalidState.Code, Message: "That download is " + d.State + ", so it cannot be resumed."}
	}
	a, err := s.adapterFor(ctx, d)
	if err != nil {
		return View{}, err
	}
	cctx, cancel := context.WithTimeout(ctx, clientTimeout)
	defer cancel()
	next := StateQueued // the next poll says what it really is
	if pause {
		err, next = a.Pause(cctx, d.ClientRef), StatePaused
	} else {
		err = a.Resume(cctx, d.ClientRef)
	}
	if err != nil {
		return View{}, clientError(err)
	}
	if err := s.store.setState(ctx, id, next, "", s.now()); err != nil {
		return View{}, err
	}
	s.active.Store(true)
	d.State = next
	s.publish("state", d)
	return s.store.getView(ctx, id)
}

// Remove takes a download out of its client (and deletes what it downloaded, if
// deleteData) and keeps its row in the history as "removed". Removing one that
// is already removed is not an error.
func (s *Service) Remove(ctx context.Context, id int64, deleteData bool) error {
	d, err := s.store.getDownload(ctx, id)
	if err != nil {
		return err
	}
	if d.State == StateRemoved {
		return nil
	}
	a, err := s.adapterFor(ctx, d)
	if err != nil {
		return err
	}
	cctx, cancel := context.WithTimeout(ctx, clientTimeout)
	defer cancel()
	if err := a.Remove(cctx, d.ClientRef, deleteData); err != nil {
		return clientError(err)
	}
	if err := s.store.setState(ctx, id, StateRemoved, "", s.now()); err != nil {
		return err
	}
	d.State = StateRemoved
	s.publish("removed", d)
	return nil
}
