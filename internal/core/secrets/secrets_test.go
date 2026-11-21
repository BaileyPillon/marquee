package secrets

import (
	"bytes"
	"crypto/rand"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func testBox(t *testing.T) *Box {
	t.Helper()
	key := make([]byte, KeySize)
	if _, err := rand.Read(key); err != nil {
		t.Fatal(err)
	}
	b, err := New(key)
	if err != nil {
		t.Fatal(err)
	}
	return b
}

func TestSealOpenRoundTrip(t *testing.T) {
	b := testBox(t)
	for _, plain := range []string{"", "x", "tmdb-api-key-1234567890", strings.Repeat("long ", 1000), "héllo • 世界"} {
		sealed, err := b.Seal(plain)
		if err != nil {
			t.Fatal(err)
		}
		if len(plain) >= 8 && strings.Contains(sealed, plain) {
			t.Errorf("sealed text contains the plaintext")
		}
		got, err := b.Open(sealed)
		if err != nil {
			t.Fatalf("Open: %v", err)
		}
		if got != plain {
			t.Errorf("round trip = %q, want %q", got, plain)
		}
	}
}

func TestSealUsesFreshNonce(t *testing.T) {
	b := testBox(t)
	a, _ := b.Seal("same")
	c, _ := b.Seal("same")
	if a == c {
		t.Fatal("two seals of the same value are identical; nonce is not random")
	}
}

func TestOpenRejectsWrongKeyAndTampering(t *testing.T) {
	a, c := testBox(t), testBox(t)
	sealed, _ := a.Seal("secret")
	if _, err := c.Open(sealed); err != ErrInvalid {
		t.Errorf("wrong key: err = %v, want ErrInvalid", err)
	}
	raw := []byte(sealed)
	raw[len(raw)-3] ^= 1
	if _, err := a.Open(string(raw)); err == nil {
		t.Error("tampered value opened")
	}
	for _, bad := range []string{"", "not base64!!", "AAAA"} {
		if _, err := a.Open(bad); err != ErrInvalid {
			t.Errorf("Open(%q) = %v, want ErrInvalid", bad, err)
		}
	}
}

func TestNewRejectsWrongKeySize(t *testing.T) {
	if _, err := New(make([]byte, 16)); err == nil {
		t.Fatal("16-byte key accepted")
	}
}

func TestLoadOrCreateMakesKeyOnceAndWarns(t *testing.T) {
	path := filepath.Join(t.TempDir(), "sub", KeyFileName)

	var logs bytes.Buffer
	log := slog.New(slog.NewTextHandler(&logs, nil))
	first, err := LoadOrCreate(path, log)
	if err != nil {
		t.Fatal(err)
	}
	key, err := os.ReadFile(path)
	if err != nil || len(key) != KeySize {
		t.Fatalf("key file: %d bytes, %v", len(key), err)
	}
	if !strings.Contains(logs.String(), "level=WARN") || !strings.Contains(logs.String(), "back this file up") {
		t.Errorf("no backup warning on creation; log was %q", logs.String())
	}

	// Second start: same key, no new warning.
	logs.Reset()
	second, err := LoadOrCreate(path, log)
	if err != nil {
		t.Fatal(err)
	}
	if logs.Len() != 0 {
		t.Errorf("warned again on a normal start: %q", logs.String())
	}
	sealed, _ := first.Seal("hello")
	if got, err := second.Open(sealed); err != nil || got != "hello" {
		t.Errorf("second Box cannot open first Box's value: %q, %v", got, err)
	}
}

func TestLoadRejectsBadKeyFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), KeyFileName)
	if err := os.WriteFile(path, []byte("too short"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := LoadOrCreate(path, nil); err == nil {
		t.Fatal("a 9-byte key file was accepted")
	}
}

func TestKeyPathHonoursEnv(t *testing.T) {
	t.Setenv(EnvKeyFile, "")
	if got, want := KeyPath("cfg"), filepath.Join("cfg", KeyFileName); got != want {
		t.Errorf("KeyPath = %q, want %q", got, want)
	}
	override := filepath.Join(t.TempDir(), "elsewhere.key")
	t.Setenv(EnvKeyFile, override)
	if got := KeyPath("cfg"); got != override {
		t.Errorf("KeyPath with env = %q, want %q", got, override)
	}
}
