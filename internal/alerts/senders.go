package alerts

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"crypto/tls"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"mime"
	"mime/quotedprintable"
	"net"
	"net/http"
	"net/smtp"
	"net/url"
	"strings"
	"time"

	"github.com/BaileyPillon/marquee/internal/core/outbound"
)

// Where the hosted services live. Options can replace them (tests do).
const (
	DefaultTelegramURL = "https://api.telegram.org"
	DefaultPushoverURL = "https://api.pushover.net/1/messages.json"
)

const (
	// maxReply caps what is read from a service's answer.
	maxReply = 64 << 10
	// smtpTimeout bounds one whole mail.
	smtpTimeout = 30 * time.Second
)

// senders send one message to one kind of channel. Every network call goes
// through the outbound client; none of them puts a URL, a token or a password
// into an error.
type senders struct {
	hc          *outbound.Client
	telegramURL string
	pushoverURL string
	smtpTLS     *tls.Config // replaces the default TLS settings for mail (tests)
	now         func() time.Time
}

// send sends m to a channel of the given kind with its settings (secrets in the
// clear).
func (s *senders) send(ctx context.Context, kind string, cfg map[string]string, m Message) error {
	var err error
	switch kind {
	case KindDiscord:
		err = s.discord(ctx, cfg, m)
	case KindNtfy:
		err = s.ntfy(ctx, cfg, m)
	case KindGotify:
		err = s.gotify(ctx, cfg, m)
	case KindTelegram:
		err = s.telegram(ctx, cfg, m)
	case KindPushover:
		err = s.pushover(ctx, cfg, m)
	case KindEmail:
		err = s.email(ctx, cfg, m)
	case KindWebhook:
		err = s.webhook(ctx, cfg, m)
	default:
		err = fmt.Errorf("unknown kind %q", kind)
	}
	if err != nil {
		return errors.New(redact(cleanErr(err).Error(), kind, cfg))
	}
	return nil
}

// cleanErr drops the URL that net/http puts in its errors: for Discord and
// Telegram the URL holds the secret.
func cleanErr(err error) error {
	var ue *url.Error
	if errors.As(err, &ue) {
		return ue.Err
	}
	return err
}

// redact takes every secret of a channel out of an error text.
func redact(msg, kind string, cfg map[string]string) string {
	for _, f := range specs[kind] {
		if v := cfg[f.name]; f.secret && len(v) >= 4 {
			msg = strings.ReplaceAll(msg, v, "…")
		}
	}
	return msg
}

// post sends a request and reads the start of the answer.
func (s *senders) post(ctx context.Context, rawURL, contentType string, body []byte, header http.Header) (int, []byte, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, rawURL, bytes.NewReader(body))
	if err != nil {
		return 0, nil, errors.New("the address is not valid")
	}
	req.Header.Set("Content-Type", contentType)
	for k, v := range header {
		req.Header[k] = v
	}
	resp, err := s.hc.Do(req)
	if err != nil {
		return 0, nil, err
	}
	defer resp.Body.Close()
	b, _ := io.ReadAll(io.LimitReader(resp.Body, maxReply))
	return resp.StatusCode, b, nil
}

func ok2xx(status int) bool { return status >= 200 && status <= 299 }

// answer describes a refusal in a few words: the status, and the service's own
// short explanation when it gave one.
func answer(who string, status int, body []byte) error {
	msg := fmt.Sprintf("%s answered HTTP %d", who, status)
	if note := explanation(body); note != "" {
		msg += ": " + note
	}
	return errors.New(msg)
}

// explanation finds a short human reason in a JSON error body.
func explanation(body []byte) string {
	var v struct {
		Description string   `json:"description"`
		Message     string   `json:"message"`
		Error       string   `json:"error"`
		Errors      []string `json:"errors"`
	}
	if json.Unmarshal(body, &v) != nil {
		return ""
	}
	for _, s := range append([]string{v.Description, v.Message, v.Error}, v.Errors...) {
		if s = strings.TrimSpace(s); s != "" {
			if len(s) > 200 {
				s = s[:200]
			}
			return s
		}
	}
	return ""
}

func (m Message) heading() string {
	if m.Title != "" {
		return m.Title
	}
	return "Marquee"
}

// ---- Discord ------------------------------------------------------------------

var discordColors = map[string]int{
	EventRequestCreated: 0xE0A458, EventRequestApproved: 0x3BA55D, EventRequestDeclined: 0xED4245,
	EventTitleAvailable: 0x3BA55D, EventDownloadFailed: 0xED4245, EventImportFailed: 0xED4245,
	EventHealthProblem: 0xFAA61A,
}

func (s *senders) discord(ctx context.Context, cfg map[string]string, m Message) error {
	color, ok := discordColors[m.Event]
	if !ok {
		color = 0x99AAB5
	}
	body, _ := json.Marshal(map[string]any{
		"username": "Marquee",
		"embeds": []map[string]any{{
			"title": m.heading(), "description": m.Body, "color": color,
			"timestamp": m.At.UTC().Format(time.RFC3339),
		}},
	})
	status, b, err := s.post(ctx, cfg["url"], "application/json", body, nil)
	if err != nil {
		return err
	}
	if !ok2xx(status) {
		return answer("Discord", status, b)
	}
	return nil
}

// ---- ntfy ---------------------------------------------------------------------

var ntfyTags = map[string]string{
	EventRequestCreated: "inbox_tray", EventRequestApproved: "white_check_mark", EventRequestDeclined: "x",
	EventTitleAvailable: "clapper", EventDownloadFailed: "warning", EventImportFailed: "warning",
	EventHealthProblem: "rotating_light",
}

func (s *senders) ntfy(ctx context.Context, cfg map[string]string, m Message) error {
	h := http.Header{}
	h.Set("Title", m.heading())
	tag := ntfyTags[m.Event]
	if tag == "" {
		tag = "bell"
	}
	h.Set("Tags", tag)
	switch m.Event {
	case EventDownloadFailed, EventImportFailed, EventHealthProblem:
		h.Set("Priority", "high")
	}
	if cfg["token"] != "" {
		h.Set("Authorization", "Bearer "+cfg["token"])
	}
	target := strings.TrimRight(cfg["server"], "/") + "/" + url.PathEscape(cfg["topic"])
	status, b, err := s.post(ctx, target, "text/plain; charset=utf-8", []byte(m.Body), h)
	if err != nil {
		return err
	}
	if !ok2xx(status) {
		return answer("ntfy", status, b)
	}
	return nil
}

// ---- Gotify -------------------------------------------------------------------

func (s *senders) gotify(ctx context.Context, cfg map[string]string, m Message) error {
	priority := 5
	switch m.Event {
	case EventDownloadFailed, EventImportFailed, EventHealthProblem:
		priority = 8
	}
	body, _ := json.Marshal(map[string]any{"title": m.heading(), "message": m.Body, "priority": priority})
	target := strings.TrimRight(cfg["server"], "/") + "/message?token=" + url.QueryEscape(cfg["token"])
	status, b, err := s.post(ctx, target, "application/json", body, nil)
	if err != nil {
		return err
	}
	if !ok2xx(status) {
		return answer("Gotify", status, b)
	}
	return nil
}

// ---- Telegram -----------------------------------------------------------------

func (s *senders) telegram(ctx context.Context, cfg map[string]string, m Message) error {
	body, _ := json.Marshal(map[string]any{"chat_id": cfg["chatId"], "text": m.heading() + "\n" + m.Body})
	target := strings.TrimRight(s.telegramURL, "/") + "/bot" + cfg["botToken"] + "/sendMessage"
	status, b, err := s.post(ctx, target, "application/json", body, nil)
	if err != nil {
		return err
	}
	var reply struct {
		OK bool `json:"ok"`
	}
	if !ok2xx(status) || (json.Unmarshal(b, &reply) == nil && !reply.OK) {
		return answer("Telegram", status, b)
	}
	return nil
}

// ---- Pushover -----------------------------------------------------------------

func (s *senders) pushover(ctx context.Context, cfg map[string]string, m Message) error {
	form := url.Values{
		"token": {cfg["appToken"]}, "user": {cfg["userKey"]}, "title": {m.heading()}, "message": {m.Body},
	}
	status, b, err := s.post(ctx, s.pushoverURL, "application/x-www-form-urlencoded", []byte(form.Encode()), nil)
	if err != nil {
		return err
	}
	var reply struct {
		Status int `json:"status"`
	}
	if !ok2xx(status) || (json.Unmarshal(b, &reply) == nil && reply.Status != 1) {
		return answer("Pushover", status, b)
	}
	return nil
}

// ---- webhook ------------------------------------------------------------------

// SignatureHeader carries the HMAC-SHA256 of a webhook's body, as
// "sha256=<hex>", when the channel has a secret.
const SignatureHeader = "X-Marquee-Signature"

func (s *senders) webhook(ctx context.Context, cfg map[string]string, m Message) error {
	payload := map[string]any{
		"event": m.Event, "title": m.Title, "message": m.Body, "sentAt": m.At.UTC().Format(time.RFC3339), "test": m.Test,
	}
	if len(m.Data) > 0 {
		payload["data"] = m.Data
	}
	body, err := json.Marshal(payload)
	if err != nil {
		return err
	}
	h := http.Header{}
	h.Set("X-Marquee-Event", m.Event)
	if secret := cfg["secret"]; secret != "" {
		mac := hmac.New(sha256.New, []byte(secret))
		mac.Write(body)
		h.Set(SignatureHeader, "sha256="+hex.EncodeToString(mac.Sum(nil)))
	}
	status, b, err := s.post(ctx, cfg["url"], "application/json", body, h)
	if err != nil {
		return err
	}
	if !ok2xx(status) {
		return answer("The webhook", status, b)
	}
	return nil
}

// ---- email --------------------------------------------------------------------

func (s *senders) tlsConfig(host string) *tls.Config {
	if s.smtpTLS != nil {
		c := s.smtpTLS.Clone()
		if c.ServerName == "" {
			c.ServerName = host
		}
		return c
	}
	return &tls.Config{ServerName: host, MinVersion: tls.VersionTLS12}
}

func (s *senders) email(ctx context.Context, cfg map[string]string, m Message) error {
	host := cfg["host"]
	addr := net.JoinHostPort(host, cfg["port"])
	from, _ := mailAddress(cfg["from"])
	to := recipients(cfg["to"])
	if from == "" || len(to) == 0 {
		return errors.New("the From or To address is not valid")
	}

	dctx, cancel := context.WithTimeout(ctx, smtpTimeout)
	defer cancel()
	var d net.Dialer
	var (
		conn net.Conn
		err  error
	)
	if cfg["security"] == "tls" {
		td := tls.Dialer{NetDialer: &d, Config: s.tlsConfig(host)}
		conn, err = td.DialContext(dctx, "tcp", addr)
	} else {
		conn, err = d.DialContext(dctx, "tcp", addr)
	}
	if err != nil {
		return fmt.Errorf("could not connect to the mail server: %w", cleanErr(err))
	}
	if dl, ok := dctx.Deadline(); ok {
		_ = conn.SetDeadline(dl)
	}
	c, err := smtp.NewClient(conn, host)
	if err != nil {
		_ = conn.Close()
		return fmt.Errorf("the mail server did not greet us: %w", err)
	}
	defer c.Close()
	if err := c.Hello("marquee.local"); err != nil {
		return fmt.Errorf("the mail server refused the greeting: %w", err)
	}
	if cfg["security"] == "starttls" {
		if ok, _ := c.Extension("STARTTLS"); !ok {
			return errors.New("the mail server does not offer STARTTLS, so nothing was sent; choose another security setting if you trust the connection")
		}
		if err := c.StartTLS(s.tlsConfig(host)); err != nil {
			return fmt.Errorf("could not start an encrypted mail connection: %w", err)
		}
	}
	if cfg["username"] != "" {
		if err := c.Auth(smtp.PlainAuth("", cfg["username"], cfg["password"], host)); err != nil {
			return fmt.Errorf("the mail server did not accept the sign-in: %w", err)
		}
	}
	if err := c.Mail(from); err != nil {
		return fmt.Errorf("the mail server did not accept the From address: %w", err)
	}
	for _, r := range to {
		if err := c.Rcpt(r); err != nil {
			return fmt.Errorf("the mail server did not accept the address %s: %w", r, err)
		}
	}
	w, err := c.Data()
	if err != nil {
		return fmt.Errorf("the mail server refused the message: %w", err)
	}
	if _, err := w.Write(s.buildMail(cfg["from"], to, m)); err != nil {
		return fmt.Errorf("could not send the message: %w", err)
	}
	if err := w.Close(); err != nil {
		return fmt.Errorf("the mail server did not take the message: %w", err)
	}
	_ = c.Quit()
	return nil
}

func mailAddress(s string) (string, bool) {
	list := recipients(s)
	if len(list) != 1 {
		return "", false
	}
	return list[0], true
}

// buildMail writes the message: plain text, UTF-8, with the subject encoded.
func (s *senders) buildMail(from string, to []string, m Message) []byte {
	var id [8]byte
	_, _ = rand.Read(id[:])
	var b bytes.Buffer
	line := func(k, v string) { b.WriteString(k + ": " + v + "\r\n") }
	line("From", from)
	line("To", strings.Join(to, ", "))
	line("Subject", mime.QEncoding.Encode("utf-8", "[Marquee] "+m.heading()))
	line("Date", m.At.Format(time.RFC1123Z))
	line("Message-ID", "<"+hex.EncodeToString(id[:])+"@marquee.local>")
	line("MIME-Version", "1.0")
	line("Content-Type", "text/plain; charset=UTF-8")
	line("Content-Transfer-Encoding", "quoted-printable")
	b.WriteString("\r\n")
	qp := quotedprintable.NewWriter(&b)
	_, _ = qp.Write([]byte(strings.ReplaceAll(m.Body, "\n", "\r\n") + "\r\n"))
	_ = qp.Close()
	return b.Bytes()
}
