package events

import (
	"bufio"
	"context"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func quiet() *slog.Logger { return slog.New(slog.NewTextHandler(io.Discard, nil)) }

func recv(t *testing.T, c *Client) Message {
	t.Helper()
	select {
	case m, ok := <-c.C():
		if !ok {
			t.Fatal("channel closed")
		}
		return m
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for an event")
	}
	return Message{}
}

func TestPublishReachesSubscribers(t *testing.T) {
	h := NewHub(quiet())
	all, un1 := h.Subscribe()
	only, un2 := h.Subscribe("downloads")
	defer un1()
	defer un2()

	h.Publish("downloads", map[string]int{"percent": 42})
	h.Publish("health", map[string]string{"status": "ok"})

	m := recv(t, all)
	if m.Topic != "downloads" || string(m.Data) != `{"percent":42}` {
		t.Errorf("first = %+v", m)
	}
	if m := recv(t, all); m.Topic != "health" {
		t.Errorf("second = %+v", m)
	}
	if m := recv(t, only); m.Topic != "downloads" {
		t.Errorf("filtered client got %+v", m)
	}
	select {
	case m := <-only.C():
		t.Errorf("filtered client also received %+v", m)
	case <-time.After(50 * time.Millisecond):
	}
}

func TestSlowClientIsDropped(t *testing.T) {
	h := NewHub(quiet(), WithBuffer(2))
	slow, _ := h.Subscribe()
	fast, un := h.Subscribe()
	defer un()

	for i := range 5 {
		h.Publish("t", i)
		recv(t, fast) // the fast client keeps up
	}
	if !slow.Dropped() {
		t.Fatal("slow client was not dropped")
	}
	// The slow client's buffer drains, then its channel is closed.
	n := 0
	for range slow.C() {
		n++
	}
	if n != 2 {
		t.Errorf("slow client received %d buffered events, want 2", n)
	}
	if h.Len() != 1 {
		t.Errorf("hub has %d clients, want 1", h.Len())
	}
	// Publishing never blocks on the dropped client.
	h.Publish("t", 99)
}

func TestUnsubscribeAndClose(t *testing.T) {
	h := NewHub(quiet())
	c, un := h.Subscribe()
	un()
	un() // twice is fine
	if _, ok := <-c.C(); ok {
		t.Error("channel still open after unsubscribe")
	}

	c2, _ := h.Subscribe()
	h.Close()
	if _, ok := <-c2.C(); ok {
		t.Error("channel still open after Close")
	}
	h.Publish("t", 1) // no panic
	c3, _ := h.Subscribe()
	if _, ok := <-c3.C(); ok {
		t.Error("subscribe after Close should return a closed channel")
	}
}

func TestPublishIgnoresBadInput(t *testing.T) {
	h := NewHub(quiet())
	c, un := h.Subscribe()
	defer un()
	h.Publish("", 1)
	h.Publish("a\nb", 1)
	h.Publish("ok", make(chan int)) // not JSON-encodable
	select {
	case m := <-c.C():
		t.Errorf("unexpected delivery %+v", m)
	case <-time.After(50 * time.Millisecond):
	}
}

func readLines(r *bufio.Reader, until string) (string, error) {
	var sb strings.Builder
	for {
		line, err := r.ReadString('\n')
		sb.WriteString(line)
		if err != nil || strings.Contains(sb.String(), until) {
			return sb.String(), err
		}
	}
}

func TestHTTPStreamAndHeartbeat(t *testing.T) {
	h := NewHub(quiet(), WithHeartbeat(30*time.Millisecond))
	srv := httptest.NewServer(h)
	defer srv.Close()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	req, _ := http.NewRequestWithContext(ctx, http.MethodGet, srv.URL+"?topic=downloads", nil)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if ct := resp.Header.Get("Content-Type"); ct != "text/event-stream" {
		t.Fatalf("Content-Type = %q", ct)
	}
	br := bufio.NewReader(resp.Body)

	// A heartbeat comment arrives with no events published.
	got, err := readLines(br, ": ping")
	if err != nil || !strings.Contains(got, ": ping") {
		t.Fatalf("no heartbeat: %q, %v", got, err)
	}

	// Wait for the handler to subscribe, then publish one wanted and one filtered event.
	deadline := time.Now().Add(2 * time.Second)
	for h.Len() == 0 && time.Now().Before(deadline) {
		time.Sleep(5 * time.Millisecond)
	}
	h.Publish("health", "ignored")
	h.Publish("downloads", map[string]int{"percent": 7})

	got, err = readLines(br, `"percent":7`)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(got, "event: downloads\n") || !strings.Contains(got, `data: {"percent":7}`) {
		t.Errorf("stream = %q", got)
	}
	if strings.Contains(got, "ignored") {
		t.Errorf("received an event for a topic it did not ask for: %q", got)
	}

	cancel()
	deadline = time.Now().Add(2 * time.Second)
	for h.Len() != 0 && time.Now().Before(deadline) {
		time.Sleep(5 * time.Millisecond)
	}
	if h.Len() != 0 {
		t.Error("client still registered after disconnect")
	}
}

func TestCloseEndsOpenStreams(t *testing.T) {
	h := NewHub(quiet())
	srv := httptest.NewServer(h)
	defer srv.Close()
	resp, err := http.Get(srv.URL)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()

	done := make(chan struct{})
	go func() { _, _ = io.Copy(io.Discard, resp.Body); close(done) }()
	for h.Len() == 0 {
		time.Sleep(5 * time.Millisecond)
	}
	h.Close()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("stream did not end after Close")
	}
}
