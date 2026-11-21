// Package secrets encrypts small values (API keys, tokens, passwords for other
// services) with a master key that lives in a file next to the database.
//
// Values are sealed with AES-256-GCM using a fresh random nonce and stored as
// base64 text of nonce||ciphertext.
package secrets

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"encoding/base64"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"log/slog"
	"os"
	"path/filepath"
)

// KeySize is the master key length in bytes (AES-256).
const KeySize = 32

// KeyFileName is the default master key file name inside the config dir.
const KeyFileName = "marquee.key"

// EnvKeyFile overrides the master key path.
const EnvKeyFile = "MARQUEE_MASTER_KEY_FILE"

// ErrInvalid is returned by Open when the value is not something Seal produced
// with this key (wrong key, damaged, or not sealed at all).
var ErrInvalid = errors.New("secrets: value cannot be decrypted with this master key")

// Box seals and opens values with one master key.
type Box struct {
	aead cipher.AEAD
}

// New builds a Box from a 32-byte key.
func New(key []byte) (*Box, error) {
	if len(key) != KeySize {
		return nil, fmt.Errorf("secrets: master key must be %d bytes, got %d", KeySize, len(key))
	}
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, err
	}
	aead, err := cipher.NewGCM(block)
	if err != nil {
		return nil, err
	}
	return &Box{aead: aead}, nil
}

// KeyPath returns where the master key lives: MARQUEE_MASTER_KEY_FILE if set,
// otherwise <configDir>/marquee.key.
func KeyPath(configDir string) string {
	if p := os.Getenv(EnvKeyFile); p != "" {
		return p
	}
	return filepath.Join(configDir, KeyFileName)
}

// LoadOrCreate reads the master key at path, or creates a new random one if the
// file does not exist. Creation logs a one-time warning telling the owner to
// back the file up.
func LoadOrCreate(path string, log *slog.Logger) (*Box, error) {
	if log == nil {
		log = slog.Default()
	}
	key, err := readKey(path)
	if errors.Is(err, fs.ErrNotExist) {
		key, err = createKey(path, log)
	}
	if err != nil {
		return nil, err
	}
	return New(key)
}

func readKey(path string) ([]byte, error) {
	key, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	if len(key) != KeySize {
		return nil, fmt.Errorf("secrets: %s must hold exactly %d random bytes, found %d (is it the right file?)",
			path, KeySize, len(key))
	}
	return key, nil
}

func createKey(path string, log *slog.Logger) ([]byte, error) {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return nil, fmt.Errorf("secrets: create key dir: %w", err)
	}
	key := make([]byte, KeySize)
	if _, err := io.ReadFull(rand.Reader, key); err != nil {
		return nil, fmt.Errorf("secrets: generate key: %w", err)
	}
	// O_EXCL: if another process created it a moment ago, use theirs.
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if errors.Is(err, fs.ErrExist) {
		return readKey(path)
	}
	if err != nil {
		return nil, fmt.Errorf("secrets: create %s: %w", path, err)
	}
	if _, err := f.Write(key); err != nil {
		_ = f.Close()
		_ = os.Remove(path)
		return nil, fmt.Errorf("secrets: write %s: %w", path, err)
	}
	if err := f.Sync(); err != nil {
		_ = f.Close()
		_ = os.Remove(path)
		return nil, fmt.Errorf("secrets: sync %s: %w", path, err)
	}
	if err := f.Close(); err != nil {
		return nil, fmt.Errorf("secrets: close %s: %w", path, err)
	}
	log.Warn("created a new master key; back this file up, because without it every saved API key and password becomes unreadable",
		"path", path)
	return key, nil
}

// Seal encrypts plaintext and returns base64(nonce || ciphertext).
func (b *Box) Seal(plaintext string) (string, error) {
	nonce := make([]byte, b.aead.NonceSize())
	if _, err := io.ReadFull(rand.Reader, nonce); err != nil {
		return "", fmt.Errorf("secrets: nonce: %w", err)
	}
	out := b.aead.Seal(nonce, nonce, []byte(plaintext), nil)
	return base64.StdEncoding.EncodeToString(out), nil
}

// Open reverses Seal.
func (b *Box) Open(sealed string) (string, error) {
	raw, err := base64.StdEncoding.DecodeString(sealed)
	if err != nil {
		return "", ErrInvalid
	}
	ns := b.aead.NonceSize()
	if len(raw) < ns+b.aead.Overhead() {
		return "", ErrInvalid
	}
	plain, err := b.aead.Open(nil, raw[:ns], raw[ns:], nil)
	if err != nil {
		return "", ErrInvalid
	}
	return string(plain), nil
}
