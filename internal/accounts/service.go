package accounts

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"database/sql"
	"encoding/base32"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"log/slog"
	"slices"
	"strings"
	"time"
)

// Lifetimes and throttles.
const (
	SessionLifetime = 30 * 24 * time.Hour
	InviteLifetime  = 7 * 24 * time.Hour
	touchEvery      = 5 * time.Minute // slide the session at most this often
	keyTouchEvery   = time.Minute     // record API key use at most this often
)

// Options tune a Service. The zero value is production behaviour.
type Options struct {
	Logger *slog.Logger
	// Now replaces the clock (tests).
	Now func() time.Time
	// PasswordParams replaces DefaultPasswordParams (tests use light ones).
	PasswordParams *PasswordParams
}

// Service holds the accounts logic.
type Service struct {
	store   *Store
	hasher  *hasher
	limiter *loginLimiter
	log     *slog.Logger
	now     func() time.Time
}

// NewService builds the service on the main database.
func NewService(d *sql.DB, opts Options) *Service {
	if opts.Logger == nil {
		opts.Logger = slog.Default()
	}
	if opts.Now == nil {
		opts.Now = time.Now
	}
	params := DefaultPasswordParams
	if opts.PasswordParams != nil {
		params = *opts.PasswordParams
	}
	return &Service{
		store:   NewStore(d),
		hasher:  newHasher(params),
		limiter: newLoginLimiter(LoginMaxFailures, LoginWindow, opts.Now),
		log:     opts.Logger,
		now:     opts.Now,
	}
}

// Login is the result of signing in: the person and a new session token.
type Login struct {
	User      User
	Token     string // shown once; goes into the cookie
	ExpiresAt time.Time
}

// ClientInfo is recorded with a session.
type ClientInfo struct {
	IP        string
	UserAgent string
}

// ---- first run -------------------------------------------------------------

// NeedsOwner reports whether nobody has signed up yet.
func (s *Service) NeedsOwner(ctx context.Context) (bool, error) {
	n, err := s.store.CountUsers(ctx)
	return n == 0, err
}

// SetupOwner creates the owner, but only if there are no users at all, and
// signs them in. It is safe against concurrent callers.
func (s *Service) SetupOwner(ctx context.Context, name, password string, ci ClientInfo) (Login, error) {
	name, err := cleanName(name)
	if err != nil {
		return Login{}, err
	}
	if err := checkPassword(password); err != nil {
		return Login{}, err
	}
	// Cheap early exit so a late caller does not pay for a 64 MiB hash.
	if need, err := s.NeedsOwner(ctx); err != nil {
		return Login{}, err
	} else if !need {
		return Login{}, ErrOwnerExists
	}
	hash, err := s.hasher.hash(password)
	if err != nil {
		return Login{}, err
	}
	u, err := s.store.CreateOwnerIfNone(ctx, name, hash, s.now())
	if err != nil {
		return Login{}, err
	}
	s.log.Info("owner account created", "user", u.Name)
	return s.newSession(ctx, u, ci)
}

// ---- sign in / out ---------------------------------------------------------

func limiterKey(ip, name string) string { return ip + "|" + strings.ToLower(strings.TrimSpace(name)) }

// SignIn checks a name and password. After LoginMaxFailures failures within
// LoginWindow for the same IP and name it returns *RateLimitedError.
func (s *Service) SignIn(ctx context.Context, name, password string, ci ClientInfo) (Login, error) {
	key := limiterKey(ci.IP, name)
	if wait := s.limiter.retryAfter(key); wait > 0 {
		return Login{}, &RateLimitedError{RetryAfter: wait}
	}
	row, err := s.store.UserByName(ctx, strings.TrimSpace(name))
	switch {
	case errors.Is(err, ErrNotFound):
		s.hasher.burn(password)
		s.limiter.fail(key)
		return Login{}, ErrInvalidCredentials
	case err != nil:
		return Login{}, err
	}
	ok, err := s.hasher.verify(row.PasswordHash, password)
	if err != nil {
		s.log.Error("stored password hash is unreadable", "user", row.Name, "err", err)
		return Login{}, ErrInvalidCredentials
	}
	if !ok || row.Disabled {
		s.limiter.fail(key)
		return Login{}, ErrInvalidCredentials
	}
	s.limiter.reset(key)
	return s.newSession(ctx, row.User, ci)
}

func newToken(n int) (string, error) {
	b := make([]byte, n)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(b), nil
}

func hashToken(token string) string {
	sum := sha256.Sum256([]byte(token))
	return hex.EncodeToString(sum[:])
}

func (s *Service) newSession(ctx context.Context, u User, ci ClientInfo) (Login, error) {
	token, err := newToken(32)
	if err != nil {
		return Login{}, fmt.Errorf("accounts: session token: %w", err)
	}
	now := s.now()
	expires := now.Add(SessionLifetime)
	ua := ci.UserAgent
	if len(ua) > 300 {
		ua = ua[:300]
	}
	if err := s.store.InsertSession(ctx, hashToken(token), u.ID, now, expires, ua, ci.IP); err != nil {
		return Login{}, err
	}
	return Login{User: u, Token: token, ExpiresAt: expires.UTC().Truncate(time.Millisecond)}, nil
}

// SignOut ends the session behind a cookie token.
func (s *Service) SignOut(ctx context.Context, token string) error {
	return s.store.DeleteSession(ctx, hashToken(token))
}

// AuthenticateSession resolves a cookie token. Sessions slide: using one
// pushes its expiry 30 days out (written at most every few minutes).
func (s *Service) AuthenticateSession(ctx context.Context, token string) (*Principal, error) {
	if token == "" {
		return nil, ErrUnauthenticated
	}
	id := hashToken(token)
	rec, err := s.store.SessionByID(ctx, id)
	if errors.Is(err, ErrNotFound) {
		return nil, ErrUnauthenticated
	}
	if err != nil {
		return nil, err
	}
	now := s.now()
	if !rec.ExpiresAt.After(now) {
		_ = s.store.DeleteSession(ctx, id)
		return nil, ErrUnauthenticated
	}
	if rec.User.Disabled {
		return nil, ErrUnauthenticated
	}
	p := &Principal{
		User:      rec.User,
		Via:       ViaSession,
		Scopes:    ScopesForRole(rec.User.Role),
		ExpiresAt: rec.ExpiresAt,
	}
	if now.Sub(rec.LastSeenAt) >= touchEvery {
		expires := now.Add(SessionLifetime)
		if err := s.store.TouchSession(ctx, id, now, expires); err != nil {
			s.log.Warn("could not slide session", "err", err)
		} else {
			p.ExpiresAt = expires.UTC().Truncate(time.Millisecond)
			p.Refreshed = true
		}
	}
	return p, nil
}

// ---- invites ---------------------------------------------------------------

// CreateInvite makes a one-time invite token. The owner may invite admins and
// members; an admin may invite members only.
func (s *Service) CreateInvite(ctx context.Context, actor User, role Role) (token string, expires time.Time, err error) {
	if actor.Role != RoleOwner && actor.Role != RoleAdmin {
		return "", time.Time{}, ErrForbidden
	}
	if role != RoleAdmin && role != RoleMember {
		return "", time.Time{}, ErrInvalidRole
	}
	if role == RoleAdmin && actor.Role != RoleOwner {
		return "", time.Time{}, ErrForbidden
	}
	token, err = newToken(32)
	if err != nil {
		return "", time.Time{}, err
	}
	now := s.now()
	expires = now.Add(InviteLifetime)
	if err := s.store.InsertInvite(ctx, hashToken(token), actor.ID, role, now, expires); err != nil {
		return "", time.Time{}, err
	}
	return token, expires.UTC().Truncate(time.Millisecond), nil
}

// AcceptInvite spends an invite to create an account, and signs it in.
func (s *Service) AcceptInvite(ctx context.Context, token, name, password string, ci ClientInfo) (Login, error) {
	name, err := cleanName(name)
	if err != nil {
		return Login{}, err
	}
	if err := checkPassword(password); err != nil {
		return Login{}, err
	}
	if token == "" {
		return Login{}, ErrInvalidInvite
	}
	hash, err := s.hasher.hash(password)
	if err != nil {
		return Login{}, err
	}
	u, err := s.store.AcceptInvite(ctx, hashToken(token), name, hash, s.now())
	if err != nil {
		return Login{}, err
	}
	return s.newSession(ctx, u, ci)
}

// ---- API keys --------------------------------------------------------------

var keyEncoding = base32.StdEncoding.WithPadding(base32.NoPadding)

const keyPrefixTag = "mq"

// CleanScopes validates, de-duplicates and orders scopes, and checks the person
// may hold each one.
func CleanScopes(role Role, scopes []string) ([]string, error) {
	allowed := ScopesForRole(role)
	var out []string
	for _, sc := range AllScopes {
		if slices.Contains(scopes, sc) {
			if !slices.Contains(allowed, sc) {
				return nil, &ScopeError{Scope: sc, Err: ErrScopeNotAllowed}
			}
			out = append(out, sc)
		}
	}
	for _, sc := range scopes {
		if !slices.Contains(AllScopes, sc) {
			return nil, &ScopeError{Scope: sc, Err: ErrInvalidScope}
		}
	}
	if len(out) == 0 {
		return nil, &ScopeError{Scope: "", Err: ErrInvalidScope}
	}
	return out, nil
}

// CreateAPIKey makes a key for user. The returned secret has the form
// mq_<prefix>_<secret> and is never stored or shown again.
func (s *Service) CreateAPIKey(ctx context.Context, user User, name string, scopes []string) (APIKey, string, error) {
	name = strings.TrimSpace(name)
	if name == "" || len([]rune(name)) > maxKeyNameRunes {
		return APIKey{}, "", ErrInvalidKeyName
	}
	scopes, err := CleanScopes(user.Role, scopes)
	if err != nil {
		return APIKey{}, "", err
	}
	prefixRaw, secretRaw := make([]byte, 5), make([]byte, 32)
	if _, err := rand.Read(prefixRaw); err != nil {
		return APIKey{}, "", err
	}
	if _, err := rand.Read(secretRaw); err != nil {
		return APIKey{}, "", err
	}
	prefix := strings.ToLower(keyEncoding.EncodeToString(prefixRaw)) // 8 chars
	secret := strings.ToLower(keyEncoding.EncodeToString(secretRaw)) // 52 chars
	k, err := s.store.InsertAPIKey(ctx, user.ID, name, prefix, hashToken(secret), scopes, s.now())
	if err != nil {
		return APIKey{}, "", err
	}
	return k, keyPrefixTag + "_" + prefix + "_" + secret, nil
}

// ListAPIKeys returns the user's keys.
func (s *Service) ListAPIKeys(ctx context.Context, user User) ([]APIKey, error) {
	return s.store.ListAPIKeys(ctx, user.ID)
}

// RevokeAPIKey revokes one of the user's keys.
func (s *Service) RevokeAPIKey(ctx context.Context, user User, id int64) error {
	return s.store.RevokeAPIKey(ctx, user.ID, id, s.now())
}

// IsAPIKey reports whether a bearer token looks like one of ours.
func IsAPIKey(token string) bool { return strings.HasPrefix(token, keyPrefixTag+"_") }

// AuthenticateAPIKey resolves mq_<prefix>_<secret>. A key never carries more
// than its person's role allows, even if the role changed after it was made.
func (s *Service) AuthenticateAPIKey(ctx context.Context, token string) (*Principal, error) {
	parts := strings.SplitN(token, "_", 3)
	if len(parts) != 3 || parts[0] != keyPrefixTag || parts[1] == "" || parts[2] == "" {
		return nil, ErrUnauthenticated
	}
	rec, err := s.store.ActiveAPIKeyByPrefix(ctx, parts[1])
	if errors.Is(err, ErrNotFound) {
		return nil, ErrUnauthenticated
	}
	if err != nil {
		return nil, err
	}
	want, got := []byte(rec.Hash), []byte(hashToken(parts[2]))
	if subtle.ConstantTimeCompare(want, got) != 1 || rec.User.Disabled {
		return nil, ErrUnauthenticated
	}
	allowed := ScopesForRole(rec.User.Role)
	var scopes []string
	for _, sc := range rec.Scopes {
		if slices.Contains(allowed, sc) {
			scopes = append(scopes, sc)
		}
	}
	if err := s.store.TouchAPIKey(ctx, rec.ID, s.now(), keyTouchEvery); err != nil {
		s.log.Warn("could not record API key use", "err", err)
	}
	return &Principal{User: rec.User, Via: ViaAPIKey, Scopes: scopes, KeyID: rec.ID}, nil
}

// ---- housekeeping ----------------------------------------------------------

// Cleanup removes expired sessions and stale invites and forgets old failed
// sign-ins. It is meant to run from the job scheduler.
func (s *Service) Cleanup(ctx context.Context) error {
	s.limiter.sweep()
	sessions, invites, err := s.store.PurgeExpired(ctx, s.now())
	if err != nil {
		return err
	}
	if sessions+invites > 0 {
		s.log.Debug("accounts cleanup", "sessions", sessions, "invites", invites)
	}
	return nil
}

// ---- members made by another module ------------------------------------------

// CreateMember makes a member who has no password anyone knows, so the person
// cannot sign in until the owner gives them a way in (an invite, later a Plex
// sign-in). It is for modules that act for a person who has no account yet: a
// Plex friend whose watchlist becomes asks. A name that is taken gives
// ErrNameTaken.
func (s *Service) CreateMember(ctx context.Context, name string) (User, error) {
	name, err := cleanName(name)
	if err != nil {
		return User{}, err
	}
	nobody, err := newToken(32)
	if err != nil {
		return User{}, fmt.Errorf("accounts: member password: %w", err)
	}
	hash, err := s.hasher.hash(nobody) // nobody is thrown away: no one can sign in with it
	if err != nil {
		return User{}, err
	}
	u, err := s.store.InsertMember(ctx, name, hash, s.now())
	if err != nil {
		return User{}, err
	}
	s.log.Info("member created for another service", "user", u.Name)
	return u, nil
}
