package alerts

import (
	"crypto/hmac"
	"crypto/sha256"
	"crypto/tls"
	"encoding/hex"
	"encoding/json"
	"mime"
	"net/url"
	"strings"
	"testing"
	"time"
)

// testSend sends the test message to a channel and returns the one request it made.
func (e *env) testSend(ch Channel) hit {
	e.t.Helper()
	e.rec.reset()
	if err := e.svc.Test(bg(), ch.ID); err != nil {
		e.t.Fatalf("Test(%s): %v", ch.Name, err)
	}
	return e.rec.only(e.t)
}

func jsonBody(t *testing.T, h hit) map[string]any {
	t.Helper()
	var m map[string]any
	if err := json.Unmarshal(h.Body, &m); err != nil {
		t.Fatalf("body is not JSON: %q", h.Body)
	}
	return m
}

func TestDiscordSendsAnEmbedToTheWebhook(t *testing.T) {
	e := newEnv(t)
	e.rec.answer("/discord/", 204, "")
	ch := e.channel("Discord", KindDiscord, map[string]any{"url": e.rec.srv.URL + "/discord/webhooks/12345/token-abc-def"})
	h := e.testSend(ch)
	if h.Method != "POST" || h.Path != "/discord/webhooks/12345/token-abc-def" || h.Header.Get("Content-Type") != "application/json" {
		t.Errorf("request = %s %s %s", h.Method, h.Path, h.Header.Get("Content-Type"))
	}
	body := jsonBody(t, h)
	embeds, _ := body["embeds"].([]any)
	if len(embeds) != 1 {
		t.Fatalf("body = %s", h.Body)
	}
	emb := embeds[0].(map[string]any)
	if emb["title"] != "Marquee test" || emb["description"] != TestMessage || emb["color"] == nil || emb["timestamp"] != "2026-11-21T12:00:00Z" {
		t.Errorf("embed = %v", emb)
	}
	if body["username"] != "Marquee" {
		t.Errorf("username = %v", body["username"])
	}
	got, _ := e.svc.Get(bg(), ch.ID)
	if got.LastOKAt == nil || got.LastError != "" {
		t.Errorf("channel after a good send = %+v", got)
	}
}

func TestNtfyPostsTheTextWithTitleAndTags(t *testing.T) {
	e := newEnv(t)
	ch := e.channel("ntfy", KindNtfy, map[string]any{"server": e.rec.srv.URL + "/ntfy/", "topic": "marquee-alerts", "token": "tk_secret_token"})
	h := e.testSend(ch)
	if h.Method != "POST" || h.Path != "/ntfy/marquee-alerts" {
		t.Errorf("request = %s %s", h.Method, h.Path)
	}
	if string(h.Body) != TestMessage || h.Header.Get("Title") != "Marquee test" || h.Header.Get("Tags") != "bell" ||
		h.Header.Get("Authorization") != "Bearer tk_secret_token" || !strings.HasPrefix(h.Header.Get("Content-Type"), "text/plain") {
		t.Errorf("body %q headers %v", h.Body, h.Header)
	}
	// Without a token there is no Authorization header, and a failure is "high" priority with its own tag.
	open := e.channel("ntfy open", KindNtfy, map[string]any{"server": e.rec.srv.URL + "/ntfy", "topic": "t2"})
	e.rec.reset()
	e.svc.Notify(bg(), Event{Type: EventDownloadFailed, Title: "Download failed", Body: "Oops", Key: "1"})
	e.svc.Wait()
	var seen bool
	for _, h := range e.rec.all() {
		if h.Path == "/ntfy/t2" {
			seen = true
			if h.Header.Get("Authorization") != "" || h.Header.Get("Priority") != "high" || h.Header.Get("Tags") != "warning" || h.Header.Get("Title") != "Download failed" {
				t.Errorf("failure headers = %v", h.Header)
			}
		}
	}
	if !seen {
		t.Error("the open channel got nothing")
	}
	_ = open
}

func TestGotifyPostsJSONWithTheTokenInTheQuery(t *testing.T) {
	e := newEnv(t)
	ch := e.channel("Gotify", KindGotify, map[string]any{"server": e.rec.srv.URL + "/gotify", "token": "AbC+dEf/123"})
	h := e.testSend(ch)
	if h.Method != "POST" || h.Path != "/gotify/message" {
		t.Errorf("request = %s %s", h.Method, h.Path)
	}
	if q, _ := url.ParseQuery(h.Query); q.Get("token") != "AbC+dEf/123" {
		t.Errorf("query = %q", h.Query)
	}
	body := jsonBody(t, h)
	if body["title"] != "Marquee test" || body["message"] != TestMessage || body["priority"] != float64(5) {
		t.Errorf("body = %v", body)
	}
}

func TestTelegramPostsToTheBotsSendMessage(t *testing.T) {
	e := newEnv(t)
	e.rec.answer("/telegram/", 200, `{"ok": true, "result": {}}`)
	ch := e.channel("Telegram", KindTelegram, map[string]any{"botToken": botToken, "chatId": "-100123"})
	h := e.testSend(ch)
	if h.Method != "POST" || h.Path != "/telegram/bot"+botToken+"/sendMessage" {
		t.Errorf("request = %s %s", h.Method, h.Path)
	}
	body := jsonBody(t, h)
	if body["chat_id"] != "-100123" || body["text"] != "Marquee test\n"+TestMessage {
		t.Errorf("body = %v", body)
	}
}

func TestPushoverPostsAForm(t *testing.T) {
	e := newEnv(t)
	e.rec.answer("/pushover/", 200, `{"status": 1, "request": "abc"}`)
	ch := e.channel("Pushover", KindPushover, map[string]any{"appToken": "app-token-123", "userKey": "user-key-456"})
	h := e.testSend(ch)
	if h.Method != "POST" || h.Path != "/pushover/1/messages.json" || h.Header.Get("Content-Type") != "application/x-www-form-urlencoded" {
		t.Errorf("request = %s %s %s", h.Method, h.Path, h.Header.Get("Content-Type"))
	}
	form, _ := url.ParseQuery(string(h.Body))
	if form.Get("token") != "app-token-123" || form.Get("user") != "user-key-456" || form.Get("title") != "Marquee test" || form.Get("message") != TestMessage {
		t.Errorf("form = %v", form)
	}
}

func TestWebhookPostsJSONAndSignsItWhenThereIsASecret(t *testing.T) {
	e := newEnv(t)
	signed := e.channel("signed", KindWebhook, map[string]any{"url": e.rec.srv.URL + "/hook", "secret": "s3cret-value"})
	h := e.testSend(signed)
	if h.Method != "POST" || h.Header.Get("Content-Type") != "application/json" || h.Header.Get("X-Marquee-Event") != "test" {
		t.Errorf("request = %s %v", h.Method, h.Header)
	}
	body := jsonBody(t, h)
	if body["event"] != "test" || body["title"] != "Marquee test" || body["message"] != TestMessage || body["test"] != true || body["sentAt"] != "2026-11-21T12:00:00Z" {
		t.Errorf("body = %v", body)
	}
	mac := hmac.New(sha256.New, []byte("s3cret-value"))
	mac.Write(h.Body)
	if want := "sha256=" + hex.EncodeToString(mac.Sum(nil)); h.Header.Get(SignatureHeader) != want {
		t.Errorf("signature = %q, want %q", h.Header.Get(SignatureHeader), want)
	}

	plain := e.channel("plain", KindWebhook, map[string]any{"url": e.rec.srv.URL + "/plain"})
	if h := e.testSend(plain); h.Header.Get(SignatureHeader) != "" {
		t.Errorf("an unsigned webhook carries a signature: %q", h.Header.Get(SignatureHeader))
	}
}

// ---- email -----------------------------------------------------------------------

func mailConfig(s *smtpServer, security string, extra map[string]any) map[string]any {
	cfg := map[string]any{
		"host": s.host(), "port": s.port(), "security": security,
		"from": "Marquee <marquee@example.test>", "to": "bailey@example.test, partner@example.test",
	}
	for k, v := range extra {
		cfg[k] = v
	}
	return cfg
}

func TestEmailUsesStartTLSAndSignsIn(t *testing.T) {
	srv := newSMTP(t, true, false)
	e := newEnv(t, rigOpts{tls: &tls.Config{InsecureSkipVerify: true}})
	ch := e.channel("Mail", KindEmail, mailConfig(srv, "starttls", map[string]any{"username": "marquee-user", "password": "mail-pass-1"}))
	if err := e.svc.Test(bg(), ch.ID); err != nil {
		t.Fatal(err)
	}
	srv.mu.Lock()
	defer srv.mu.Unlock()
	if !srv.sawTLS {
		t.Error("the connection was never upgraded with STARTTLS")
	}
	if len(srv.auths) != 1 || srv.auths[0] != "|marquee-user|mail-pass-1" {
		t.Errorf("auth = %v", srv.auths)
	}
	if len(srv.froms) != 1 || srv.froms[0] != "marquee@example.test" || len(srv.rcpts) != 2 || srv.rcpts[0] != "bailey@example.test" || srv.rcpts[1] != "partner@example.test" {
		t.Errorf("envelope: from %v to %v", srv.froms, srv.rcpts)
	}
	if len(srv.mails) != 1 {
		t.Fatalf("%d mails", len(srv.mails))
	}
	mail := srv.mails[0]
	head, body, _ := strings.Cut(mail, "\n\n")
	for _, want := range []string{"From: Marquee <marquee@example.test>", "To: bailey@example.test, partner@example.test", "MIME-Version: 1.0",
		"Content-Type: text/plain; charset=UTF-8", "Subject: [Marquee] Marquee test"} {
		if !strings.Contains(head, want) {
			t.Errorf("headers lack %q:\n%s", want, head)
		}
	}
	if !strings.Contains(body, "This is a test from Marquee.") {
		t.Errorf("body = %q", body)
	}
	got, _ := e.svc.Get(bg(), ch.ID)
	if got.LastOKAt == nil || got.LastError != "" {
		t.Errorf("channel = %+v", got)
	}
}

func TestEmailWithTLSFromTheFirstByte(t *testing.T) {
	srv := newSMTP(t, false, true)
	e := newEnv(t, rigOpts{tls: &tls.Config{InsecureSkipVerify: true}})
	ch := e.channel("Mail", KindEmail, mailConfig(srv, "tls", map[string]any{"username": "u", "password": "p"}))
	if err := e.svc.Test(bg(), ch.ID); err != nil {
		t.Fatal(err)
	}
	srv.mu.Lock()
	defer srv.mu.Unlock()
	if len(srv.mails) != 1 || len(srv.auths) != 1 {
		t.Errorf("mails %d auths %v", len(srv.mails), srv.auths)
	}
}

func TestEmailRefusesToSendInTheClearWhenStartTLSIsMissing(t *testing.T) {
	srv := newSMTP(t, false, false) // offers no STARTTLS
	e := newEnv(t, rigOpts{tls: &tls.Config{InsecureSkipVerify: true}})
	ch := e.channel("Mail", KindEmail, mailConfig(srv, "starttls", map[string]any{"username": "u", "password": "mail-pass-2"}))
	err := e.svc.Test(bg(), ch.ID)
	if err == nil || !strings.Contains(err.Error(), "STARTTLS") || strings.Contains(err.Error(), "mail-pass-2") {
		t.Fatalf("err = %v", err)
	}
	srv.mu.Lock()
	defer srv.mu.Unlock()
	if len(srv.mails) != 0 || len(srv.auths) != 0 {
		t.Errorf("something was sent: mails %d auths %v", len(srv.mails), srv.auths)
	}
	got, _ := e.svc.Get(bg(), ch.ID)
	if got.LastError == "" || got.LastOKAt != nil {
		t.Errorf("channel = %+v", got)
	}
}

func TestEmailWithoutEncryptionSendsNoPasswordToAMailServerElsewhere(t *testing.T) {
	// "none" security is for a trusted local relay; with a user and password,
	// net/smtp itself refuses to send them unencrypted to a host that is not
	// localhost. (Here the host is 127.0.0.1, so that is allowed: the check is
	// that the mail goes through without STARTTLS.)
	srv := newSMTP(t, false, false)
	e := newEnv(t)
	ch := e.channel("Relay", KindEmail, mailConfig(srv, "none", nil))
	if err := e.svc.Test(bg(), ch.ID); err != nil {
		t.Fatal(err)
	}
	srv.mu.Lock()
	defer srv.mu.Unlock()
	if len(srv.mails) != 1 || len(srv.auths) != 0 || srv.sawTLS {
		t.Errorf("mails %d auths %v tls %v", len(srv.mails), srv.auths, srv.sawTLS)
	}
}

func TestMailSubjectsAreEncodedForNonASCIIText(t *testing.T) {
	s := &senders{}
	raw := s.buildMail("a@b.test", []string{"c@d.test"}, Message{Title: "Ask for Amélie — 4K", Body: "Priya asked for Amélie (2001)\nline two", At: testNow()})
	text := string(raw)
	head, body, _ := strings.Cut(text, "\r\n\r\n")
	var subject string
	for _, l := range strings.Split(head, "\r\n") {
		if v, ok := strings.CutPrefix(l, "Subject: "); ok {
			subject = v
		}
	}
	dec, err := new(mime.WordDecoder).DecodeHeader(subject)
	if err != nil || dec != "[Marquee] Ask for Amélie — 4K" {
		t.Errorf("subject %q decodes to %q, %v", subject, dec, err)
	}
	if strings.Contains(head, "é") {
		t.Error("a raw non-ASCII character is in the headers")
	}
	if !strings.Contains(body, "Am=C3=A9lie") || !strings.Contains(body, "\r\n") {
		t.Errorf("body = %q", body)
	}
}

// ---- when sending goes wrong ---------------------------------------------------------

func TestErrorsAreInPlainWordsAndHoldNoSecret(t *testing.T) {
	e := newEnv(t)
	e.rec.answer("/telegram/", 400, `{"ok": false, "description": "Bad Request: chat not found"}`)
	e.rec.answer("/pushover/", 200, `{"status": 0, "errors": ["user identifier is invalid"]}`)
	e.rec.answer("/hook/", 500, `{"message": "boom"}`)
	e.rec.answer("/gotify/", 401, `{"error": "Unauthorized"}`)

	for _, tc := range []struct {
		ch   Channel
		want string
		leak string
	}{
		{e.channel("tg", KindTelegram, map[string]any{"botToken": botToken, "chatId": "1"}), "Telegram answered HTTP 400: Bad Request: chat not found", "secret_token"},
		{e.channel("po", KindPushover, map[string]any{"appToken": "app-token-123", "userKey": "user-key-456"}), "Pushover answered HTTP 200: user identifier is invalid", "app-token-123"},
		{e.channel("wh", KindWebhook, map[string]any{"url": e.rec.srv.URL + "/hook/tokenpart-1234", "secret": "s3cret-value"}), "The webhook answered HTTP 500: boom", "tokenpart-1234"},
		{e.channel("gf", KindGotify, map[string]any{"server": e.rec.srv.URL + "/gotify", "token": "gotify-token-xyz"}), "Gotify answered HTTP 401: Unauthorized", "gotify-token-xyz"},
	} {
		err := e.svc.Test(bg(), tc.ch.ID)
		if err == nil || err.Error() != tc.want || strings.Contains(err.Error(), tc.leak) {
			t.Errorf("%s: err = %v, want %q", tc.ch.Name, err, tc.want)
		}
		got, _ := e.svc.Get(bg(), tc.ch.ID)
		if got.LastError != tc.want || got.LastOKAt != nil {
			t.Errorf("%s: channel = %+v", tc.ch.Name, got)
		}
	}
}

func TestAnUnreachableServerGivesAnErrorWithoutTheURL(t *testing.T) {
	e := newEnv(t)
	ch := e.channel("Discord", KindDiscord, map[string]any{"url": "http://127.0.0.1:1/api/webhooks/1/secret-webhook-token-abc"})
	err := e.svc.Test(bg(), ch.ID)
	if err == nil {
		t.Fatal("no error")
	}
	for _, leak := range []string{"secret-webhook-token-abc", "127.0.0.1:1/api"} {
		if strings.Contains(err.Error(), leak) {
			t.Errorf("the error %q holds %q", err, leak)
		}
	}
	// A telegram bot token travels in the path, so it must not show up either.
	e.svc.o.TelegramURL = "http://127.0.0.1:1"
	e.svc.snd.telegramURL = "http://127.0.0.1:1"
	tg := e.channel("tg", KindTelegram, map[string]any{"botToken": botToken, "chatId": "1"})
	if err := e.svc.Test(bg(), tg.ID); err == nil || strings.Contains(err.Error(), "secret_token") || strings.Contains(err.Error(), "bot123456789") {
		t.Errorf("telegram err = %v", err)
	}
}

func TestAFailureKeepsTheTimeOfTheLastSuccessAndASuccessClearsTheError(t *testing.T) {
	e := newEnv(t)
	ch := e.channel("Hook", KindWebhook, map[string]any{"url": e.rec.srv.URL + "/hook"})
	if err := e.svc.Test(bg(), ch.ID); err != nil {
		t.Fatal(err)
	}
	okAt := e.clock.Now()
	e.clock.Advance(time.Hour)
	e.rec.answer("/hook", 503, "")
	if err := e.svc.Test(bg(), ch.ID); err == nil {
		t.Fatal("no error for a 503")
	}
	got, _ := e.svc.Get(bg(), ch.ID)
	if got.LastOKAt == nil || !got.LastOKAt.Equal(okAt) || got.LastError == "" {
		t.Errorf("after the failure: %+v", got)
	}
	e.rec.answer("/hook", 200, "")
	if err := e.svc.Test(bg(), ch.ID); err != nil {
		t.Fatal(err)
	}
	got, _ = e.svc.Get(bg(), ch.ID)
	if got.LastError != "" || !got.LastOKAt.Equal(e.clock.Now()) {
		t.Errorf("after the success: %+v", got)
	}
	if err := e.svc.Test(bg(), 12345); err == nil {
		t.Error("Test on a channel that is not there")
	}
}

func TestTestSendsToADisabledChannelWithNoEvents(t *testing.T) {
	e := newEnv(t)
	ch := e.channel("Muted", KindWebhook, map[string]any{"url": e.rec.srv.URL + "/hook"}, []string{}...)
	if _, err := e.svc.Update(bg(), ch.ID, Input{Enabled: ptr(false)}); err != nil {
		t.Fatal(err)
	}
	if err := e.svc.Test(bg(), ch.ID); err != nil {
		t.Fatal(err)
	}
	e.rec.only(t)
}
