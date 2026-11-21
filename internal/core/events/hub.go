// Package events is the Server-Sent Events hub that pushes live updates
// (download progress, new asks, health) to open browser tabs.
//
// The hub does not know about accounts: the web layer puts sign-in checks in
// front of Handler.
package events

import (
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"strings"
	"sync"
	"time"
)

// Defaults.
const (
	DefaultHeartbeat = 25 * time.Second
	DefaultBuffer    = 64
)

// Message is one event as delivered to a subscriber.
type Message struct {
	ID    uint64
	Topic string
	Data  []byte // JSON
}

// Option configures a Hub.
type Option func(*Hub)

// WithHeartbeat sets the keep-alive comment interval.
func WithHeartbeat(d time.Duration) Option { return func(h *Hub) { h.heartbeat = d } }

// WithBuffer sets how many events a client may have waiting before it counts
// as slow and is dropped.
func WithBuffer(n int) Option { return func(h *Hub) { h.buffer = n } }

// Hub fans events out to subscribers.
type Hub struct {
	log       *slog.Logger
	heartbeat time.Duration
	buffer    int

	mu      sync.Mutex
	clients map[*Client]struct{}
	nextID  uint64
	closed  bool
	done    chan struct{}
}

// Client is one subscriber. Read from C until it closes; it closes when the
// client unsubscribes, is dropped for being slow, or the hub shuts down.
type Client struct {
	ch      chan Message
	topics  map[string]struct{} // empty = everything
	dropped bool
}

// C is the stream of events.
func (c *Client) C() <-chan Message { return c.ch }

// Dropped reports whether the hub cut this client off for being too slow.
func (c *Client) Dropped() bool { return c.dropped }

// NewHub creates a hub.
func NewHub(log *slog.Logger, opts ...Option) *Hub {
	if log == nil {
		log = slog.Default()
	}
	h := &Hub{
		log:       log,
		heartbeat: DefaultHeartbeat,
		buffer:    DefaultBuffer,
		clients:   map[*Client]struct{}{},
		done:      make(chan struct{}),
	}
	for _, o := range opts {
		o(h)
	}
	if h.buffer < 1 {
		h.buffer = 1
	}
	if h.heartbeat <= 0 {
		h.heartbeat = DefaultHeartbeat
	}
	return h
}

// Publish sends payload (encoded as JSON) to every subscriber of topic.
// It never blocks: a subscriber whose buffer is full is dropped.
func (h *Hub) Publish(topic string, payload any) {
	if topic == "" || strings.ContainsAny(topic, "\r\n") {
		h.log.Warn("events: ignoring publish with a bad topic")
		return
	}
	data, err := json.Marshal(payload)
	if err != nil {
		h.log.Warn("events: payload does not encode as JSON", "topic", topic, "err", err)
		return
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.closed {
		return
	}
	h.nextID++
	msg := Message{ID: h.nextID, Topic: topic, Data: data}
	for c := range h.clients {
		if len(c.topics) > 0 {
			if _, want := c.topics[topic]; !want {
				continue
			}
		}
		select {
		case c.ch <- msg:
		default:
			c.dropped = true
			delete(h.clients, c)
			close(c.ch)
			h.log.Info("events: dropped a slow client", "topic", topic)
		}
	}
}

// Subscribe registers a client for the given topics (none = all topics) and
// returns it with a function that unsubscribes.
func (h *Hub) Subscribe(topics ...string) (*Client, func()) {
	c := &Client{ch: make(chan Message, h.buffer), topics: map[string]struct{}{}}
	for _, t := range topics {
		if t = strings.TrimSpace(t); t != "" {
			c.topics[t] = struct{}{}
		}
	}
	h.mu.Lock()
	if h.closed {
		close(c.ch)
		h.mu.Unlock()
		return c, func() {}
	}
	h.clients[c] = struct{}{}
	h.mu.Unlock()
	return c, func() {
		h.mu.Lock()
		defer h.mu.Unlock()
		if _, ok := h.clients[c]; ok {
			delete(h.clients, c)
			close(c.ch)
		}
	}
}

// Len is the number of connected clients.
func (h *Hub) Len() int {
	h.mu.Lock()
	defer h.mu.Unlock()
	return len(h.clients)
}

// Close disconnects every client and stops accepting new ones.
func (h *Hub) Close() {
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.closed {
		return
	}
	h.closed = true
	close(h.done)
	for c := range h.clients {
		delete(h.clients, c)
		close(c.ch)
	}
}

// ServeHTTP streams events as text/event-stream until the client goes away.
// The optional query parameter topic (repeatable, or comma separated) limits
// what is sent.
func (h *Hub) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	var topics []string
	for _, v := range r.URL.Query()["topic"] {
		topics = append(topics, strings.Split(v, ",")...)
	}

	rc := http.NewResponseController(w)
	hd := w.Header()
	hd.Set("Content-Type", "text/event-stream")
	hd.Set("Cache-Control", "no-cache")
	hd.Set("X-Accel-Buffering", "no") // reverse proxies: do not buffer
	w.WriteHeader(http.StatusOK)
	if _, err := fmt.Fprint(w, "retry: 3000\n: connected\n\n"); err != nil {
		return
	}
	if err := rc.Flush(); err != nil {
		return
	}

	c, unsubscribe := h.Subscribe(topics...)
	defer unsubscribe()

	tick := time.NewTicker(h.heartbeat)
	defer tick.Stop()
	for {
		select {
		case <-r.Context().Done():
			return
		case <-h.done:
			return
		case <-tick.C:
			if _, err := fmt.Fprint(w, ": ping\n\n"); err != nil {
				return
			}
		case msg, ok := <-c.ch:
			if !ok {
				return // dropped as too slow, or hub closed
			}
			if _, err := fmt.Fprintf(w, "id: %d\nevent: %s\ndata: %s\n\n", msg.ID, msg.Topic, msg.Data); err != nil {
				return
			}
		}
		if err := rc.Flush(); err != nil {
			return
		}
	}
}
