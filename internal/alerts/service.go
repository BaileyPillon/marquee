package alerts

import (
	"context"
	"crypto/tls"
	"database/sql"
	"errors"
	"io"
	"log/slog"
	"slices"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"github.com/BaileyPillon/marquee/internal/core/events"
	"github.com/BaileyPillon/marquee/internal/core/outbound"
	"github.com/BaileyPillon/marquee/internal/core/secrets"
)

// Options configure the service.
type Options struct {
	Logger *slog.Logger
	// Now replaces the clock (tests).
	Now func() time.Time
	// Hub is where events come from. Without it only Notify and ReportProblems
	// send anything.
	Hub *events.Hub
	// Requests and Downloads fill in the names a message needs.
	Requests  RequestReader
	Downloads DownloadReader
	// TelegramURL and PushoverURL replace the hosted services' addresses (tests).
	TelegramURL string
	PushoverURL string
	// SMTPTLS replaces the TLS settings used for mail (tests with a self-signed server).
	SMTPTLS *tls.Config
	// DedupeWindow and RateLimit replace the defaults (tests).
	DedupeWindow time.Duration
	RateLimit    int
}

// Service is the alerts module.
type Service struct {
	store *Store
	box   *secrets.Box
	snd   *senders
	log   *slog.Logger
	now   func() time.Time
	o     Options

	mu       sync.Mutex
	sent     map[int64]map[string]time.Time // channel -> dedupe key -> when it was sent
	recent   map[int64][]time.Time          // channel -> when the last messages went out
	problems map[string]bool                // health problems being reported
	closed   bool
	wg       sync.WaitGroup
	sem      chan struct{} // bounds sends in flight
}

// NewService builds the module on the main database. hc is the outbound client
// every send goes through.
func NewService(d *sql.DB, box *secrets.Box, hc *outbound.Client, o Options) *Service {
	if o.Logger == nil {
		o.Logger = slog.New(slog.NewTextHandler(io.Discard, nil))
	}
	if o.Now == nil {
		o.Now = time.Now
	}
	if o.TelegramURL == "" {
		o.TelegramURL = DefaultTelegramURL
	}
	if o.PushoverURL == "" {
		o.PushoverURL = DefaultPushoverURL
	}
	if o.DedupeWindow <= 0 {
		o.DedupeWindow = DedupeWindow
	}
	if o.RateLimit <= 0 {
		o.RateLimit = RateLimit
	}
	return &Service{
		store: NewStore(d), box: box, log: o.Logger, now: o.Now, o: o,
		snd:  &senders{hc: hc, telegramURL: o.TelegramURL, pushoverURL: o.PushoverURL, smtpTLS: o.SMTPTLS, now: o.Now},
		sent: map[int64]map[string]time.Time{}, recent: map[int64][]time.Time{}, problems: map[string]bool{},
		sem: make(chan struct{}, 8),
	}
}

// ---- managing channels --------------------------------------------------------

const maxNameRunes = 60

func (s *Service) view(r channelRow) Channel {
	return Channel{
		ID: r.ID, Name: r.Name, Kind: r.Kind, Config: mask(s.box, r.Kind, r.Config), Events: r.Events,
		Enabled: r.Enabled, LastOKAt: r.LastOKAt, LastError: r.LastError, CreatedAt: r.CreatedAt,
	}
}

// List returns every channel, oldest first, secrets masked.
func (s *Service) List(ctx context.Context) ([]Channel, error) {
	rows, err := s.store.list(ctx)
	if err != nil {
		return nil, err
	}
	out := make([]Channel, len(rows))
	for i, r := range rows {
		out[i] = s.view(r)
	}
	return out, nil
}

// Get returns one channel, secrets masked.
func (s *Service) Get(ctx context.Context, id int64) (Channel, error) {
	r, err := s.store.get(ctx, id)
	if err != nil {
		return Channel{}, err
	}
	return s.view(r), nil
}

func cleanName(name string) (string, error) {
	name = strings.TrimSpace(name)
	if name == "" || utf8.RuneCountInString(name) > maxNameRunes {
		return "", invalid("A channel needs a name of up to 60 characters.")
	}
	for _, r := range name {
		if r < ' ' || r == 0x7f {
			return "", invalid("A channel name cannot have control characters in it.")
		}
	}
	return name, nil
}

// cleanEvents checks the list of events and puts it in order, once each.
func cleanEvents(list []string) ([]string, error) {
	out := []string{}
	for _, e := range Events {
		if slices.Contains(list, e) {
			out = append(out, e)
		}
	}
	for _, e := range list {
		if !validEvent(e) {
			return nil, invalid("Unknown event " + `"` + e + `". The events are ` + strings.Join(Events, ", ") + ".")
		}
	}
	return out, nil
}

// Create makes a channel. The name and kind are required. Events default to all
// of them, and the channel starts enabled.
func (s *Service) Create(ctx context.Context, in Input) (Channel, error) {
	if in.Name == nil || in.Kind == nil {
		return Channel{}, invalid("A channel needs a name and a kind.")
	}
	name, err := cleanName(*in.Name)
	if err != nil {
		return Channel{}, err
	}
	if !validKind(*in.Kind) {
		return Channel{}, invalid("The kind must be one of " + strings.Join(Kinds, ", ") + ".")
	}
	kind := *in.Kind
	evs := slices.Clone(Events)
	if in.Events != nil {
		if evs, err = cleanEvents(*in.Events); err != nil {
			return Channel{}, err
		}
	}
	plain, err := merge(s.box, kind, nil, in.Config)
	if err != nil {
		return Channel{}, err
	}
	if err := validate(kind, plain); err != nil {
		return Channel{}, err
	}
	sealed, err := seal(s.box, kind, plain)
	if err != nil {
		return Channel{}, err
	}
	enabled := in.Enabled == nil || *in.Enabled
	id, err := s.store.insert(ctx, channelRow{Name: name, Kind: kind, Config: sealed, Events: evs, Enabled: enabled}, s.now())
	if err != nil {
		return Channel{}, err
	}
	return s.Get(ctx, id)
}

// Update changes a channel. The kind cannot change.
func (s *Service) Update(ctx context.Context, id int64, in Input) (Channel, error) {
	r, err := s.store.get(ctx, id)
	if err != nil {
		return Channel{}, err
	}
	if in.Kind != nil && *in.Kind != r.Kind {
		return Channel{}, invalid("The kind of a channel cannot be changed. Make a new channel instead.")
	}
	if in.Name != nil {
		if r.Name, err = cleanName(*in.Name); err != nil {
			return Channel{}, err
		}
	}
	if in.Events != nil {
		if r.Events, err = cleanEvents(*in.Events); err != nil {
			return Channel{}, err
		}
	}
	if in.Enabled != nil {
		r.Enabled = *in.Enabled
	}
	if in.Config != nil {
		plain, err := merge(s.box, r.Kind, r.Config, in.Config)
		if err != nil {
			return Channel{}, err
		}
		if err := validate(r.Kind, plain); err != nil {
			return Channel{}, err
		}
		if r.Config, err = seal(s.box, r.Kind, plain); err != nil {
			return Channel{}, err
		}
	}
	if err := s.store.update(ctx, r); err != nil {
		return Channel{}, err
	}
	s.forget(id) // a changed channel starts with a clean slate
	return s.Get(ctx, id)
}

// Delete removes a channel.
func (s *Service) Delete(ctx context.Context, id int64) error {
	if err := s.store.delete(ctx, id); err != nil {
		return err
	}
	s.forget(id)
	return nil
}

func (s *Service) forget(id int64) {
	s.mu.Lock()
	delete(s.sent, id)
	delete(s.recent, id)
	s.mu.Unlock()
}

// open reads a channel's settings with the secrets in the clear.
func (s *Service) open(r channelRow) (map[string]string, error) {
	return merge(s.box, r.Kind, r.Config, nil)
}

// TestMessage is what Test sends.
const TestMessage = "This is a test from Marquee. If you can read it, alerts work."

// Test sends a test message to one channel now, whether it is enabled or not and
// whatever events it wants, and records how it went. The error is in plain words
// and holds no secret.
func (s *Service) Test(ctx context.Context, id int64) error {
	r, err := s.store.get(ctx, id)
	if err != nil {
		return err
	}
	return s.deliver(ctx, r, Message{Title: "Marquee test", Body: TestMessage, At: s.now(), Test: true, Event: "test"})
}

// deliver sends to one channel and records the result.
func (s *Service) deliver(ctx context.Context, r channelRow, m Message) error {
	cfg, err := s.open(r)
	if err == nil {
		err = s.snd.send(ctx, r.Kind, cfg, m)
	}
	text := ""
	if err != nil {
		text = err.Error()
	}
	if merr := s.store.markResult(context.WithoutCancel(ctx), r.ID, text, s.now()); merr != nil && !errors.Is(merr, context.Canceled) {
		s.log.Warn("could not record the result of an alert", "channel", r.ID, "err", merr)
	}
	return err
}

// ---- sending events -----------------------------------------------------------

// Notify sends an event to every enabled channel that wants it, unless that
// channel already got the same event within the dedupe window or is over its
// rate limit. It returns at once; the sends go on in the background (Wait
// waits for them).
func (s *Service) Notify(ctx context.Context, ev Event) {
	if !validEvent(ev.Type) {
		s.log.Warn("alerts: ignoring an event of an unknown type", "type", ev.Type)
		return
	}
	rows, err := s.store.list(ctx)
	if err != nil {
		s.log.Warn("alerts: could not read the channels", "err", err)
		return
	}
	key := ev.Type + ":" + ev.Key
	msg := Message{Event: ev.Type, Title: ev.Title, Body: ev.Body, Data: ev.Data, At: s.now()}
	for _, r := range rows {
		if !r.Enabled || !slices.Contains(r.Events, ev.Type) {
			continue
		}
		if !s.admit(r.ID, key) {
			continue
		}
		s.mu.Lock()
		if s.closed {
			s.mu.Unlock()
			return
		}
		s.wg.Add(1)
		s.mu.Unlock()
		go func() {
			defer s.wg.Done()
			s.sem <- struct{}{}
			defer func() { <-s.sem }()
			sctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 45*time.Second)
			defer cancel()
			if err := s.deliver(sctx, r, msg); err != nil {
				s.log.Warn("an alert could not be sent", "channel", r.ID, "kind", r.Kind, "event", ev.Type, "err", err)
			}
		}()
	}
}

// admit says whether a message may go to a channel now, and counts it if so:
// not the same key twice within the dedupe window, and not more than the rate
// limit within a minute.
func (s *Service) admit(channel int64, key string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	now := s.now()

	seen := s.sent[channel]
	if seen == nil {
		seen = map[string]time.Time{}
		s.sent[channel] = seen
	}
	for k, at := range seen { // forget what is old
		if now.Sub(at) >= s.o.DedupeWindow {
			delete(seen, k)
		}
	}
	if _, dup := seen[key]; dup {
		return false
	}
	recent := s.recent[channel][:0:0]
	for _, at := range s.recent[channel] {
		if now.Sub(at) < RateWindow {
			recent = append(recent, at)
		}
	}
	if len(recent) >= s.o.RateLimit {
		s.recent[channel] = recent
		s.log.Warn("alerts: a channel is over its rate limit; dropping a message", "channel", channel)
		return false
	}
	seen[key] = now
	s.recent[channel] = append(recent, now)
	return true
}

// Wait waits for the sends in flight to end.
func (s *Service) Wait() { s.wg.Wait() }

// Close stops taking events and waits for the sends in flight.
func (s *Service) Close() {
	s.mu.Lock()
	s.closed = true
	s.mu.Unlock()
	s.wg.Wait()
}

// ReportProblems says which health problems exist now (key to message). A
// problem that was not there at the last report is sent as a health.problem
// event; one that is still there is not sent again (it stays quiet until it
// has gone and come back); one that has gone is forgotten.
func (s *Service) ReportProblems(ctx context.Context, now map[string]string) {
	s.mu.Lock()
	var fresh []string
	for k := range now {
		if !s.problems[k] {
			fresh = append(fresh, k)
		}
	}
	for k := range s.problems {
		if _, still := now[k]; !still {
			delete(s.problems, k)
		}
	}
	for _, k := range fresh {
		s.problems[k] = true
	}
	s.mu.Unlock()
	slices.Sort(fresh)
	for _, k := range fresh {
		s.Notify(ctx, Event{Type: EventHealthProblem, Title: "Something needs a look", Body: now[k], Key: k})
	}
}
