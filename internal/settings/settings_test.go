package settings

import (
	"context"
	"crypto/rand"
	"database/sql"
	"errors"
	"path/filepath"
	"strings"
	"testing"

	"github.com/BaileyPillon/marquee/internal/core/db"
	"github.com/BaileyPillon/marquee/internal/core/secrets"
)

var ctx = context.Background()

func newSvc(t *testing.T) (*Service, *sql.DB) {
	t.Helper()
	d, err := db.Open(filepath.Join(t.TempDir(), "marquee.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { d.Close() })
	if _, err := db.Migrate(ctx, d, db.SetMain); err != nil {
		t.Fatal(err)
	}
	key := make([]byte, secrets.KeySize)
	_, _ = rand.Read(key)
	box, err := secrets.New(key)
	if err != nil {
		t.Fatal(err)
	}
	return New(d, box), d
}

func rawStored(t *testing.T, d *sql.DB, section string) string {
	t.Helper()
	var v string
	if err := d.QueryRow(`SELECT value FROM settings WHERE key = ?`, section).Scan(&v); err != nil {
		t.Fatal(err)
	}
	return v
}

func TestSecretFieldsAreSealedAndMasked(t *testing.T) {
	s, d := newSvc(t)
	const apiKey = "abcd1234efgh5678"

	sec, err := s.Put(ctx, "tmdb", map[string]any{
		"apiKey":   apiKey,
		"language": "en",
		"nested":   map[string]any{"authToken": "tokentokentoken9999", "name": "x"},
		"clients":  []any{map[string]any{"smtpPassword": "hunter2hunter2", "host": "h"}},
	})
	if err != nil {
		t.Fatal(err)
	}

	// Returned masked, plain fields untouched.
	if got := sec.Value["apiKey"]; got != "••••5678" {
		t.Errorf("apiKey = %v, want ••••5678", got)
	}
	if sec.Value["language"] != "en" {
		t.Errorf("language = %v", sec.Value["language"])
	}
	if got := sec.Value["nested"].(map[string]any)["authToken"]; got != "••••9999" {
		t.Errorf("nested token = %v", got)
	}
	if got := sec.Value["clients"].([]any)[0].(map[string]any)["smtpPassword"]; got != "••••ter2" {
		t.Errorf("array password = %v", got)
	}

	// Stored sealed: neither the secrets nor their tails appear in the database.
	raw := rawStored(t, d, "tmdb")
	for _, leak := range []string{apiKey, "efgh5678", "tokentokentoken9999", "hunter2hunter2"} {
		if strings.Contains(raw, leak) {
			t.Errorf("stored JSON contains %q: %s", leak, raw)
		}
	}
	if !strings.Contains(raw, `"language":"en"`) {
		t.Errorf("plain field should be stored plain: %s", raw)
	}

	// The typed getter decrypts for other modules.
	var got struct {
		APIKey   string `json:"apiKey"`
		Language string `json:"language"`
	}
	ok, err := s.Get(ctx, "tmdb", &got)
	if err != nil || !ok || got.APIKey != apiKey || got.Language != "en" {
		t.Errorf("Get = %+v, %v, %v", got, ok, err)
	}

	// And GetMasked masks again.
	m, err := s.GetMasked(ctx, "tmdb")
	if err != nil || m.Value["apiKey"] != "••••5678" || m.UpdatedAt == nil {
		t.Errorf("GetMasked = %+v, %v", m, err)
	}
}

func TestShortSecretsRevealNothing(t *testing.T) {
	if got := Mask("abcdefg"); got != "••••" {
		t.Errorf("Mask(short) = %q", got)
	}
	if got := Mask("abcdefghijkl"); got != "••••ijkl" {
		t.Errorf("Mask(12 chars) = %q", got)
	}
	if got := Mask(""); got != "" {
		t.Errorf("Mask(empty) = %q", got)
	}
}

func TestMaskedValueSentBackKeepsStoredSecret(t *testing.T) {
	s, _ := newSvc(t)
	if _, err := s.Put(ctx, "tmdb", map[string]any{"apiKey": "original-secret-0001", "region": "US"}); err != nil {
		t.Fatal(err)
	}
	// The UI edits another field and sends the mask back untouched.
	sec, err := s.Put(ctx, "tmdb", map[string]any{"apiKey": "••••0001", "region": "CA"})
	if err != nil {
		t.Fatal(err)
	}
	if sec.Value["apiKey"] != "••••0001" || sec.Value["region"] != "CA" {
		t.Errorf("after save = %+v", sec.Value)
	}
	var got map[string]any
	if _, err := s.Get(ctx, "tmdb", &got); err != nil || got["apiKey"] != "original-secret-0001" {
		t.Errorf("stored secret changed: %v, %v", got, err)
	}

	// A new value replaces it; an empty value clears it.
	if _, err := s.Put(ctx, "tmdb", map[string]any{"apiKey": "replacement-secret-0002"}); err != nil {
		t.Fatal(err)
	}
	_, _ = s.Get(ctx, "tmdb", &got)
	if got["apiKey"] != "replacement-secret-0002" {
		t.Errorf("replace failed: %v", got)
	}
	sec, _ = s.Put(ctx, "tmdb", map[string]any{"apiKey": ""})
	if sec.Value["apiKey"] != "" {
		t.Errorf("cleared secret shows %v", sec.Value["apiKey"])
	}
	// A mask with nothing stored behind it stores nothing.
	sec, _ = s.Put(ctx, "other", map[string]any{"apiKey": "••••zzzz"})
	if sec.Value["apiKey"] != "" {
		t.Errorf("orphan mask became %v", sec.Value["apiKey"])
	}
}

func TestSecretMustBeText(t *testing.T) {
	s, _ := newSvc(t)
	if _, err := s.Put(ctx, "x", map[string]any{"apiKey": 12345}); !errors.Is(err, ErrBadSecret) {
		t.Errorf("numeric secret: %v", err)
	}
}

func TestTypedSetAndGet(t *testing.T) {
	s, d := newSvc(t)
	type smtp struct {
		Host     string `json:"host"`
		Port     int    `json:"port"`
		Password string `json:"password"`
	}
	if err := SetAs(ctx, s, "alerts", smtp{Host: "mail.example", Port: 587, Password: "p@ssw0rd-long"}); err != nil {
		t.Fatal(err)
	}
	if raw := rawStored(t, d, "alerts"); strings.Contains(raw, "p@ssw0rd-long") {
		t.Errorf("password stored in the clear: %s", raw)
	}
	got, ok, err := GetAs[smtp](ctx, s, "alerts")
	if err != nil || !ok || got != (smtp{"mail.example", 587, "p@ssw0rd-long"}) {
		t.Errorf("GetAs = %+v, %v, %v", got, ok, err)
	}
	if _, ok, err := GetAs[smtp](ctx, s, "never-set"); ok || err != nil {
		t.Errorf("unset section: ok=%v err=%v", ok, err)
	}
	if err := s.Set(ctx, "alerts", []int{1}); !errors.Is(err, ErrNotObject) {
		t.Errorf("Set with a list: %v", err)
	}
}

func TestUnsetSectionIsEmptyObject(t *testing.T) {
	s, _ := newSvc(t)
	sec, err := s.GetMasked(ctx, "general")
	if err != nil || len(sec.Value) != 0 || sec.UpdatedAt != nil || sec.Name != "general" {
		t.Errorf("GetMasked(unset) = %+v, %v", sec, err)
	}
}

func TestBadSectionNames(t *testing.T) {
	s, _ := newSvc(t)
	for _, name := range []string{"", "Upper", "../x", "has space", "1abc", strings.Repeat("a", 33), "a_b"} {
		if _, err := s.GetMasked(ctx, name); !errors.Is(err, ErrBadSection) {
			t.Errorf("section %q: %v", name, err)
		}
	}
	if _, err := s.GetMasked(ctx, "download-clients"); err != nil {
		t.Errorf("dashes should be fine: %v", err)
	}
}

func TestUndecryptableSecretIsMaskedNotLeaked(t *testing.T) {
	s, d := newSvc(t)
	if _, err := s.Put(ctx, "tmdb", map[string]any{"apiKey": "some-long-secret-value"}); err != nil {
		t.Fatal(err)
	}
	// Another master key opens the same database.
	other := make([]byte, secrets.KeySize)
	_, _ = rand.Read(other)
	box, _ := secrets.New(other)
	s2 := New(d, box)

	sec, err := s2.GetMasked(ctx, "tmdb")
	if err != nil || sec.Value["apiKey"] != "••••" {
		t.Errorf("GetMasked with the wrong key = %v, %v", sec.Value, err)
	}
	var v map[string]any
	if _, err := s2.Get(ctx, "tmdb", &v); err == nil {
		t.Error("typed Get with the wrong master key should fail loudly")
	}
}

func TestIsSecretKey(t *testing.T) {
	for k, want := range map[string]bool{
		"apiKey": true, "authToken": true, "smtpPassword": true, "key": true, "token": true, "password": true,
		"language": false, "keyboard": false, "tokens": false, "monkeyCount": false, "": false,
	} {
		if IsSecretKey(k) != want {
			t.Errorf("IsSecretKey(%q) = %v, want %v", k, !want, want)
		}
	}
}
