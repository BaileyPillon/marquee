package accounts

import (
	"crypto/rand"
	"crypto/subtle"
	"encoding/base64"
	"errors"
	"fmt"
	"io"
	"strings"
	"sync"

	"golang.org/x/crypto/argon2"
)

// PasswordParams are the argon2id settings. Memory is in KiB.
type PasswordParams struct {
	Time    uint32
	Memory  uint32
	Threads uint8
	KeyLen  uint32
	SaltLen uint32
}

// DefaultPasswordParams: time=2, memory=64 MiB, threads=2, 32-byte key,
// 16-byte salt.
var DefaultPasswordParams = PasswordParams{Time: 2, Memory: 64 * 1024, Threads: 2, KeyLen: 32, SaltLen: 16}

// MinPasswordLength is counted in characters.
const MinPasswordLength = 8

// maxPasswordBytes keeps one request from making argon2 chew on megabytes.
const maxPasswordBytes = 512

// hasher hashes and verifies passwords. A small semaphore bounds how many
// 64 MiB hashes run at once, so a burst of logins cannot exhaust memory.
type hasher struct {
	params PasswordParams
	sem    chan struct{}

	dummyOnce sync.Once
	dummy     string
}

func newHasher(p PasswordParams) *hasher {
	return &hasher{params: p, sem: make(chan struct{}, 4)}
}

// hash returns a PHC-style string: $argon2id$v=19$m=65536,t=2,p=2$<salt>$<key>
func (h *hasher) hash(password string) (string, error) {
	h.sem <- struct{}{}
	defer func() { <-h.sem }()
	salt := make([]byte, h.params.SaltLen)
	if _, err := io.ReadFull(rand.Reader, salt); err != nil {
		return "", fmt.Errorf("accounts: salt: %w", err)
	}
	key := argon2.IDKey([]byte(password), salt, h.params.Time, h.params.Memory, h.params.Threads, h.params.KeyLen)
	enc := base64.RawStdEncoding
	return fmt.Sprintf("$argon2id$v=%d$m=%d,t=%d,p=%d$%s$%s",
		argon2.Version, h.params.Memory, h.params.Time, h.params.Threads,
		enc.EncodeToString(salt), enc.EncodeToString(key)), nil
}

var errBadHash = errors.New("accounts: stored password hash is not in the expected format")

// verify compares password with an encoded hash in constant time, using the
// parameters stored in the hash (so old hashes keep working if defaults change).
func (h *hasher) verify(encoded, password string) (bool, error) {
	parts := strings.Split(encoded, "$")
	if len(parts) != 6 || parts[0] != "" || parts[1] != "argon2id" {
		return false, errBadHash
	}
	var version int
	if _, err := fmt.Sscanf(parts[2], "v=%d", &version); err != nil || version != argon2.Version {
		return false, errBadHash
	}
	var m, t uint32
	var p uint8
	if _, err := fmt.Sscanf(parts[3], "m=%d,t=%d,p=%d", &m, &t, &p); err != nil {
		return false, errBadHash
	}
	if m == 0 || m > 1<<20 || t == 0 || t > 20 || p == 0 {
		return false, errBadHash
	}
	enc := base64.RawStdEncoding
	salt, err := enc.DecodeString(parts[4])
	if err != nil {
		return false, errBadHash
	}
	want, err := enc.DecodeString(parts[5])
	if err != nil || len(want) == 0 {
		return false, errBadHash
	}
	h.sem <- struct{}{}
	defer func() { <-h.sem }()
	got := argon2.IDKey([]byte(password), salt, t, m, p, uint32(len(want)))
	return subtle.ConstantTimeCompare(got, want) == 1, nil
}

// burn spends the same time as a real verification, so a login for a name
// that does not exist is not measurably faster than one with a wrong password.
func (h *hasher) burn(password string) {
	h.dummyOnce.Do(func() { h.dummy, _ = h.hash("marquee-dummy-password") })
	_, _ = h.verify(h.dummy, password)
}
