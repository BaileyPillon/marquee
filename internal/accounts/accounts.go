// Package accounts is who can use Marquee: the owner (the first visitor),
// admins and members, their sessions, one-time invites, and API keys with
// scopes. SQL lives in store.go only; HTTP lives in the web package.
package accounts

import (
	"errors"
	"fmt"
	"slices"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"
)

// Role is what a person may do.
type Role string

// The three roles.
const (
	RoleOwner  Role = "owner"
	RoleAdmin  Role = "admin"
	RoleMember Role = "member"
)

// Valid reports whether r is a known role.
func (r Role) Valid() bool { return r == RoleOwner || r == RoleAdmin || r == RoleMember }

// User is a person with an account. The password hash is not part of it.
type User struct {
	ID        int64     `json:"id"`
	Name      string    `json:"name"`
	Role      Role      `json:"role"`
	CreatedAt time.Time `json:"createdAt"`
	Disabled  bool      `json:"disabled"`
}

// API key scopes (the permission ticks).
const (
	ScopeSee       = "see"
	ScopeAsk       = "ask"
	ScopeDownloads = "downloads"
	ScopeSettings  = "settings"
	ScopeDelete    = "delete"
	ScopeAdmin     = "admin"
	ScopeApprove   = "approve"
)

// AllScopes lists the known scopes in display order.
var AllScopes = []string{ScopeSee, ScopeAsk, ScopeDownloads, ScopeSettings, ScopeDelete, ScopeAdmin, ScopeApprove}

// ScopesForRole is the most a person of that role may put on a key (or do
// with a session). A key can never exceed the person it belongs to.
func ScopesForRole(r Role) []string {
	switch r {
	case RoleOwner:
		return slices.Clone(AllScopes)
	case RoleAdmin:
		return []string{ScopeSee, ScopeAsk, ScopeDownloads, ScopeDelete, ScopeAdmin, ScopeApprove}
	case RoleMember:
		return []string{ScopeSee, ScopeAsk}
	}
	return nil
}

// APIKey is the public side of a key. The secret is shown once, at creation.
type APIKey struct {
	ID         int64      `json:"id"`
	Name       string     `json:"name"`
	Prefix     string     `json:"prefix"`
	Scopes     []string   `json:"scopes"`
	CreatedAt  time.Time  `json:"createdAt"`
	LastUsedAt *time.Time `json:"lastUsedAt"`
	RevokedAt  *time.Time `json:"revokedAt"`
}

// Via says how a request proved who it is.
type Via string

// Ways of signing in.
const (
	ViaSession Via = "session"
	ViaAPIKey  Via = "apikey"
)

// Principal is the authenticated caller of one request.
type Principal struct {
	User   User     `json:"user"`
	Via    Via      `json:"via"`
	Scopes []string `json:"scopes"`
	KeyID  int64    `json:"-"`

	// Session only: when it expires now, and whether this request slid the
	// expiry (so the cookie should be re-issued).
	ExpiresAt time.Time `json:"-"`
	Refreshed bool      `json:"-"`
}

// HasScope reports whether the caller holds scope.
func (p *Principal) HasScope(scope string) bool { return slices.Contains(p.Scopes, scope) }

// Errors the web layer maps to HTTP statuses.
var (
	ErrNotFound           = errors.New("accounts: not found")
	ErrOwnerExists        = errors.New("accounts: an owner already exists")
	ErrNameTaken          = errors.New("accounts: that name is taken")
	ErrInvalidName        = errors.New("accounts: invalid name")
	ErrWeakPassword       = errors.New("accounts: password too short")
	ErrPasswordTooLong    = errors.New("accounts: password too long")
	ErrInvalidCredentials = errors.New("accounts: wrong name or password")
	ErrUnauthenticated    = errors.New("accounts: not signed in")
	ErrForbidden          = errors.New("accounts: not allowed")
	ErrInvalidInvite      = errors.New("accounts: invite is invalid, used or expired")
	ErrInvalidRole        = errors.New("accounts: invalid role")
	ErrInvalidScope       = errors.New("accounts: unknown scope")
	ErrScopeNotAllowed    = errors.New("accounts: scope not allowed for this role")
	ErrInvalidKeyName     = errors.New("accounts: invalid key name")
)

// RateLimitedError is returned when a name+IP has failed too often.
type RateLimitedError struct{ RetryAfter time.Duration }

func (e *RateLimitedError) Error() string {
	return fmt.Sprintf("accounts: too many failed sign-ins, try again in %s", e.RetryAfter.Round(time.Second))
}

// ScopeError names the scope that is not allowed or unknown.
type ScopeError struct {
	Scope string
	Err   error // ErrInvalidScope or ErrScopeNotAllowed
}

func (e *ScopeError) Error() string { return fmt.Sprintf("%v: %q", e.Err, e.Scope) }
func (e *ScopeError) Unwrap() error { return e.Err }

const (
	maxNameRunes    = 40
	maxKeyNameRunes = 60
)

// cleanName trims and validates a display/sign-in name.
func cleanName(name string) (string, error) {
	n := strings.TrimSpace(name)
	if n == "" || utf8.RuneCountInString(n) > maxNameRunes {
		return "", ErrInvalidName
	}
	for _, r := range n {
		if unicode.IsControl(r) || !unicode.IsPrint(r) {
			return "", ErrInvalidName
		}
	}
	return n, nil
}

func checkPassword(pw string) error {
	if utf8.RuneCountInString(pw) < MinPasswordLength {
		return ErrWeakPassword
	}
	if len(pw) > maxPasswordBytes {
		return ErrPasswordTooLong
	}
	return nil
}
