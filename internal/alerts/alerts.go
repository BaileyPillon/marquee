// Package alerts tells the owner what is going on, on the channels they set up:
// Discord, ntfy, Gotify, Telegram, Pushover, email or a webhook of their own.
//
// A channel lists the events it wants:
//
//	request.created    somebody asked for a title and it waits for the owner's yes
//	request.approved   an ask was approved (by the owner, or by the rules)
//	request.declined   an ask was declined
//	title.available    an asked-for title is in the library
//	download.failed    a download failed in its client
//	import.failed      a finished download could not be imported
//	health.problem     something Marquee depends on is not working
//
// The service listens to the in-process event hub ("requests", "downloads",
// "library" and "health" topics), turns an event into a plain, short message
// ("Priya asked for Metropolis (1927) in 4K — waiting for your yes") and sends
// it to every enabled channel that wants it. The same event is never sent to
// the same channel twice within ten minutes, and a channel gets at most 30
// messages a minute.
//
// Secrets in a channel's settings (a webhook URL, a token, an SMTP password) are
// sealed with the master key and shown masked.
package alerts

import (
	"context"
	"errors"
	"time"
)

// Kinds of channel.
const (
	KindDiscord  = "discord"
	KindNtfy     = "ntfy"
	KindGotify   = "gotify"
	KindTelegram = "telegram"
	KindPushover = "pushover"
	KindEmail    = "email"
	KindWebhook  = "webhook"
)

// Kinds lists every kind.
var Kinds = []string{KindDiscord, KindNtfy, KindGotify, KindTelegram, KindPushover, KindEmail, KindWebhook}

// Events a channel can ask for.
const (
	EventRequestCreated  = "request.created"
	EventRequestApproved = "request.approved"
	EventRequestDeclined = "request.declined"
	EventTitleAvailable  = "title.available"
	EventDownloadFailed  = "download.failed"
	EventImportFailed    = "import.failed"
	EventHealthProblem   = "health.problem"
)

// Events lists every event.
var Events = []string{
	EventRequestCreated, EventRequestApproved, EventRequestDeclined, EventTitleAvailable,
	EventDownloadFailed, EventImportFailed, EventHealthProblem,
}

// Hub topics the service listens to.
const (
	// HealthTopic carries {"event": "problem", "key": "...", "message": "..."}.
	HealthTopic = "health"
)

// Windows.
const (
	// DedupeWindow is how long the same event is not sent again to a channel.
	DedupeWindow = 10 * time.Minute
	// RateWindow and RateLimit are the most messages one channel gets in a minute.
	RateWindow = time.Minute
	RateLimit  = 30
)

// Errors the web layer maps to HTTP statuses.
var (
	ErrNotFound  = errors.New("alerts: no such alert channel")
	ErrNameTaken = errors.New("alerts: another channel already has that name")
)

// InvalidError is input the caller can fix. Code is stable; Message is plain words.
type InvalidError struct{ Code, Message string }

func (e *InvalidError) Error() string { return e.Message }

func invalid(msg string) error { return &InvalidError{Code: "invalid_request", Message: msg} }

func validKind(k string) bool {
	for _, v := range Kinds {
		if v == k {
			return true
		}
	}
	return false
}

func validEvent(e string) bool {
	for _, v := range Events {
		if v == e {
			return true
		}
	}
	return false
}

// Channel is an alert channel as the owner sees it: secrets are masked.
type Channel struct {
	ID        int64          `json:"id"`
	Name      string         `json:"name"`
	Kind      string         `json:"kind"`
	Config    map[string]any `json:"config"`
	Events    []string       `json:"events"`
	Enabled   bool           `json:"enabled"`
	LastOKAt  *time.Time     `json:"lastOkAt"`
	LastError string         `json:"lastError"`
	CreatedAt time.Time      `json:"createdAt"`
}

// Input is a channel to create or a change to one. On a change, a field left
// nil keeps what it was, and a secret that arrives masked (still starting with
// settings.MaskPrefix) keeps the stored value.
type Input struct {
	Name    *string
	Kind    *string
	Config  map[string]any
	Events  *[]string
	Enabled *bool
}

// Event is something worth telling the owner.
type Event struct {
	Type  string // one of Events
	Title string // a short heading
	Body  string // one plain sentence
	// Key says which thing this is about; the same Type and Key are not sent to
	// a channel twice within DedupeWindow.
	Key string
	// Data goes to a webhook as it is, next to the message.
	Data map[string]any
}

// Message is what a sender sends.
type Message struct {
	Event string
	Title string
	Body  string
	Data  map[string]any
	At    time.Time
	Test  bool
}

// RequestInfo is an ask as a message needs it.
type RequestInfo struct {
	ID            int64
	UserName      string
	TitleName     string
	Year          int
	Kind          string // movie, show or artist
	Quality       string // hd or uhd
	State         string
	DecidedByName string // empty when the rules decided
	DeclineReason string
}

// RequestReader reads an ask. The program wires the requests module in.
type RequestReader interface {
	Request(ctx context.Context, id int64) (RequestInfo, error)
}

// DownloadInfo is a download as a message needs it.
type DownloadInfo struct {
	ID           int64
	TitleID      int64
	ReleaseTitle string
	State        string
	Error        string
}

// DownloadReader reads a download. The program wires the downloads module in.
type DownloadReader interface {
	Download(ctx context.Context, id int64) (DownloadInfo, error)
}
