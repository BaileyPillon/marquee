package alerts

import (
	"errors"
	"reflect"
	"strings"
	"testing"

	"github.com/BaileyPillon/marquee/internal/settings"
)

const (
	botToken = "123456789:AAH-secret_token_value"
	hookURL  = "https://discord.com/api/webhooks/1234567890/the-very-secret-webhook-token"
)

func isInvalid(err error) bool {
	var ie *InvalidError
	return errors.As(err, &ie)
}

func TestCreateListGetAndSecretsAreMasked(t *testing.T) {
	e := newEnv(t)
	ch := e.channel("Phone", KindTelegram, map[string]any{"botToken": botToken, "chatId": "-100123"})
	if ch.ID == 0 || ch.Name != "Phone" || ch.Kind != KindTelegram || !ch.Enabled || ch.LastOKAt != nil || ch.LastError != "" {
		t.Fatalf("channel = %+v", ch)
	}
	// The secret is masked, the rest is shown, and every event is on by default.
	if ch.Config["botToken"] != settings.Mask(botToken) || ch.Config["chatId"] != "-100123" {
		t.Errorf("config = %v", ch.Config)
	}
	if strings.Contains(ch.Config["botToken"].(string), "secret_token") {
		t.Error("the secret shows in the answer")
	}
	if !reflect.DeepEqual(ch.Events, Events) {
		t.Errorf("events = %v, want all", ch.Events)
	}

	// The database holds the secret sealed, not in the clear.
	var cfg string
	if err := e.db.QueryRow(`SELECT config_sealed FROM alert_channels WHERE id = ?`, ch.ID).Scan(&cfg); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(cfg, "secret_token") || !strings.Contains(cfg, `"chatId":"-100123"`) {
		t.Errorf("config_sealed = %s", cfg)
	}

	got, err := e.svc.Get(bg(), ch.ID)
	if err != nil || !reflect.DeepEqual(got, ch) {
		t.Errorf("Get = %+v, %v", got, err)
	}
	e.channel("Mail", KindWebhook, map[string]any{"url": "https://example.test/hook"})
	list, err := e.svc.List(bg())
	if err != nil || len(list) != 2 || list[0].Name != "Phone" || list[1].Name != "Mail" {
		t.Errorf("List = %+v, %v", list, err)
	}
	if _, err := e.svc.Get(bg(), 99); !errors.Is(err, ErrNotFound) {
		t.Errorf("Get(99) = %v", err)
	}
}

func TestEveryKindCanBeCreatedWithItsSettings(t *testing.T) {
	e := newEnv(t)
	for kind, cfg := range map[string]map[string]any{
		KindDiscord:  {"url": hookURL},
		KindNtfy:     {"topic": "marquee-alerts"},
		KindGotify:   {"server": "https://gotify.example.test", "token": "AbCdEf123456"},
		KindTelegram: {"botToken": botToken, "chatId": "42"},
		KindPushover: {"appToken": "app-token-123", "userKey": "user-key-456"},
		KindEmail:    {"host": "smtp.example.test", "from": "Marquee <marquee@example.test>", "to": "bailey@example.test, other@example.test", "username": "u", "password": "p"},
		KindWebhook:  {"url": "https://example.test/hook", "secret": "s3cret-value"},
	} {
		ch, err := e.svc.Create(bg(), Input{Name: ptr(kind), Kind: ptr(kind), Config: cfg})
		if err != nil {
			t.Errorf("%s: %v", kind, err)
			continue
		}
		// Defaults filled in.
		switch kind {
		case KindNtfy:
			if ch.Config["server"] != "https://ntfy.sh" || ch.Config["token"] != "" {
				t.Errorf("ntfy config = %v", ch.Config)
			}
		case KindEmail:
			if ch.Config["port"] != 587 || ch.Config["security"] != "starttls" || ch.Config["password"] != settings.MaskPrefix {
				t.Errorf("email config = %v", ch.Config)
			}
		case KindDiscord:
			if ch.Config["url"] != settings.Mask(hookURL) {
				t.Errorf("discord url = %v", ch.Config["url"])
			}
		}
	}
}

func TestValidation(t *testing.T) {
	e := newEnv(t)
	good := map[string]map[string]any{
		KindDiscord:  {"url": hookURL},
		KindNtfy:     {"topic": "alerts"},
		KindGotify:   {"server": "https://g.test", "token": "t"},
		KindTelegram: {"botToken": botToken, "chatId": "42"},
		KindPushover: {"appToken": "a", "userKey": "u"},
		KindEmail:    {"host": "smtp.test", "from": "a@b.test", "to": "c@d.test"},
		KindWebhook:  {"url": "https://x.test/h"},
	}
	with := func(kind string, over map[string]any) map[string]any {
		out := map[string]any{}
		for k, v := range good[kind] {
			out[k] = v
		}
		for k, v := range over {
			out[k] = v
		}
		return out
	}
	for name, tc := range map[string]struct {
		in Input
	}{
		"no name":            {Input{Kind: ptr(KindWebhook), Config: good[KindWebhook]}},
		"no kind":            {Input{Name: ptr("x"), Config: good[KindWebhook]}},
		"blank name":         {Input{Name: ptr("  "), Kind: ptr(KindWebhook), Config: good[KindWebhook]}},
		"long name":          {Input{Name: ptr(strings.Repeat("x", 61)), Kind: ptr(KindWebhook), Config: good[KindWebhook]}},
		"unknown kind":       {Input{Name: ptr("x"), Kind: ptr("sms"), Config: nil}},
		"unknown field":      {Input{Name: ptr("x"), Kind: ptr(KindWebhook), Config: with(KindWebhook, map[string]any{"colour": "red"})}},
		"unknown event":      {Input{Name: ptr("x"), Kind: ptr(KindWebhook), Config: good[KindWebhook], Events: &[]string{"request.created", "the.moon"}}},
		"discord no url":     {Input{Name: ptr("x"), Kind: ptr(KindDiscord), Config: map[string]any{}}},
		"discord bad url":    {Input{Name: ptr("x"), Kind: ptr(KindDiscord), Config: map[string]any{"url": "ftp://x"}}},
		"webhook no scheme":  {Input{Name: ptr("x"), Kind: ptr(KindWebhook), Config: map[string]any{"url": "example.test/hook"}}},
		"ntfy no topic":      {Input{Name: ptr("x"), Kind: ptr(KindNtfy), Config: map[string]any{}}},
		"ntfy odd topic":     {Input{Name: ptr("x"), Kind: ptr(KindNtfy), Config: map[string]any{"topic": "a b/c"}}},
		"ntfy bad server":    {Input{Name: ptr("x"), Kind: ptr(KindNtfy), Config: with(KindNtfy, map[string]any{"server": "nope"})}},
		"gotify no token":    {Input{Name: ptr("x"), Kind: ptr(KindGotify), Config: map[string]any{"server": "https://g.test"}}},
		"telegram bad token": {Input{Name: ptr("x"), Kind: ptr(KindTelegram), Config: with(KindTelegram, map[string]any{"botToken": "nope"})}},
		"telegram bad chat":  {Input{Name: ptr("x"), Kind: ptr(KindTelegram), Config: with(KindTelegram, map[string]any{"chatId": "my chat"})}},
		"pushover no user":   {Input{Name: ptr("x"), Kind: ptr(KindPushover), Config: map[string]any{"appToken": "a"}}},
		"email no host":      {Input{Name: ptr("x"), Kind: ptr(KindEmail), Config: map[string]any{"from": "a@b.test", "to": "c@d.test"}}},
		"email bad port":     {Input{Name: ptr("x"), Kind: ptr(KindEmail), Config: with(KindEmail, map[string]any{"port": 70000})}},
		"email odd security": {Input{Name: ptr("x"), Kind: ptr(KindEmail), Config: with(KindEmail, map[string]any{"security": "maybe"})}},
		"email bad from":     {Input{Name: ptr("x"), Kind: ptr(KindEmail), Config: with(KindEmail, map[string]any{"from": "not an address"})}},
		"email bad to":       {Input{Name: ptr("x"), Kind: ptr(KindEmail), Config: with(KindEmail, map[string]any{"to": "a@b.test, nonsense"})}},
		"email user no pass": {Input{Name: ptr("x"), Kind: ptr(KindEmail), Config: with(KindEmail, map[string]any{"username": "u"})}},
		"setting not text":   {Input{Name: ptr("x"), Kind: ptr(KindWebhook), Config: with(KindWebhook, map[string]any{"secret": []string{"a"}})}},
	} {
		if _, err := e.svc.Create(bg(), tc.in); !isInvalid(err) {
			t.Errorf("%s: err = %v, want an InvalidError", name, err)
		}
	}
	// A port may be a number or text.
	if ch, err := e.svc.Create(bg(), Input{Name: ptr("p1"), Kind: ptr(KindEmail), Config: with(KindEmail, map[string]any{"port": 465.0})}); err != nil || ch.Config["port"] != 465 {
		t.Errorf("numeric port: %v %v", ch.Config, err)
	}
	if ch, err := e.svc.Create(bg(), Input{Name: ptr("p2"), Kind: ptr(KindEmail), Config: with(KindEmail, map[string]any{"port": "2525"})}); err != nil || ch.Config["port"] != 2525 {
		t.Errorf("text port: %v %v", ch.Config, err)
	}
	if n := e.count(`SELECT COUNT(*) FROM alert_channels`); n != 2 {
		t.Errorf("%d channels after the failed ones, want 2", n)
	}
}

func (e *env) count(q string, args ...any) int {
	e.t.Helper()
	var n int
	if err := e.db.QueryRow(q, args...).Scan(&n); err != nil {
		e.t.Fatal(err)
	}
	return n
}

func TestNamesAreUniqueIgnoringCase(t *testing.T) {
	e := newEnv(t)
	a := e.webhookChannel("Phone")
	_, err := e.svc.Create(bg(), Input{Name: ptr("phone"), Kind: ptr(KindWebhook), Config: map[string]any{"url": "https://x.test/h"}})
	if !errors.Is(err, ErrNameTaken) {
		t.Errorf("same name = %v, want ErrNameTaken", err)
	}
	b := e.webhookChannel("Desk")
	if _, err := e.svc.Update(bg(), b.ID, Input{Name: ptr("PHONE")}); !errors.Is(err, ErrNameTaken) {
		t.Errorf("rename onto another's name = %v", err)
	}
	if _, err := e.svc.Update(bg(), a.ID, Input{Name: ptr("Phone")}); err != nil {
		t.Errorf("keeping its own name: %v", err)
	}
}

func TestUpdateKeepsMaskedSecretsAndChangesTheRest(t *testing.T) {
	e := newEnv(t)
	ch := e.channel("Phone", KindTelegram, map[string]any{"botToken": botToken, "chatId": "42"}, EventRequestCreated)

	// What the UI does: send back what it was shown, with one change.
	masked := ch.Config["botToken"].(string)
	up, err := e.svc.Update(bg(), ch.ID, Input{Config: map[string]any{"botToken": masked, "chatId": "43"}, Events: &[]string{EventDownloadFailed, EventHealthProblem}, Enabled: ptr(false)})
	if err != nil {
		t.Fatal(err)
	}
	if up.Config["chatId"] != "43" || up.Config["botToken"] != masked || up.Enabled || !reflect.DeepEqual(up.Events, []string{EventDownloadFailed, EventHealthProblem}) {
		t.Errorf("updated = %+v", up)
	}
	// The stored secret is still the real one: sending works with it.
	r, _ := e.svc.store.get(bg(), ch.ID)
	cfg, err := e.svc.open(r)
	if err != nil || cfg["botToken"] != botToken || cfg["chatId"] != "43" {
		t.Errorf("stored config = %v, %v", cfg, err)
	}

	// A new secret replaces it; an empty one clears it, which a telegram channel cannot do without.
	if _, err := e.svc.Update(bg(), ch.ID, Input{Config: map[string]any{"botToken": "987654321:ZZZ-another_token_value"}}); err != nil {
		t.Fatal(err)
	}
	r, _ = e.svc.store.get(bg(), ch.ID)
	if cfg, _ = e.svc.open(r); cfg["botToken"] != "987654321:ZZZ-another_token_value" {
		t.Errorf("new secret = %q", cfg["botToken"])
	}
	if _, err := e.svc.Update(bg(), ch.ID, Input{Config: map[string]any{"botToken": ""}}); !isInvalid(err) {
		t.Errorf("clearing a required secret = %v", err)
	}
	// Fields left out change nothing.
	if up, err = e.svc.Update(bg(), ch.ID, Input{Name: ptr("Renamed")}); err != nil || up.Name != "Renamed" || up.Config["chatId"] != "43" || up.Enabled {
		t.Errorf("rename only = %+v, %v", up, err)
	}
	// An optional secret can be cleared.
	n := e.channel("Hook", KindWebhook, map[string]any{"url": "https://x.test/h", "secret": "abcdef"})
	if up, err = e.svc.Update(bg(), n.ID, Input{Config: map[string]any{"secret": ""}}); err != nil || up.Config["secret"] != "" {
		t.Errorf("clearing the webhook secret = %+v, %v", up, err)
	}

	if _, err := e.svc.Update(bg(), ch.ID, Input{Kind: ptr(KindDiscord)}); !isInvalid(err) {
		t.Errorf("changing the kind = %v", err)
	}
	if _, err := e.svc.Update(bg(), ch.ID, Input{Kind: ptr(KindTelegram)}); err != nil {
		t.Errorf("the same kind is fine: %v", err)
	}
	if _, err := e.svc.Update(bg(), 999, Input{Name: ptr("x")}); !errors.Is(err, ErrNotFound) {
		t.Errorf("Update(999) = %v", err)
	}
	if _, err := e.svc.Update(bg(), ch.ID, Input{Events: &[]string{"nope"}}); !isInvalid(err) {
		t.Errorf("unknown event = %v", err)
	}
}

func TestDeleteAndEmptyEvents(t *testing.T) {
	e := newEnv(t)
	ch := e.webhookChannel("Hook", EventRequestCreated)
	if len(ch.Events) != 1 {
		t.Errorf("events = %v", ch.Events)
	}
	quiet := e.channel("Quiet", KindWebhook, map[string]any{"url": "https://x.test/h"}, []string{}...)
	if quiet.Events == nil || len(quiet.Events) != 0 {
		t.Errorf("an empty list of events = %v", quiet.Events)
	}
	if err := e.svc.Delete(bg(), ch.ID); err != nil {
		t.Fatal(err)
	}
	if err := e.svc.Delete(bg(), ch.ID); !errors.Is(err, ErrNotFound) {
		t.Errorf("second delete = %v", err)
	}
	if list, _ := e.svc.List(bg()); len(list) != 1 {
		t.Errorf("%d channels left", len(list))
	}
}
