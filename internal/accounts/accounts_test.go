package accounts

import (
	"context"
	"database/sql"
	"errors"
	"io"
	"log/slog"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/BaileyPillon/marquee/internal/core/db"
)

// lightParams keeps argon2 fast enough to run it dozens of times.
var lightParams = &PasswordParams{Time: 1, Memory: 64, Threads: 1, KeyLen: 32, SaltLen: 16}

type clock struct{ t atomic.Int64 }

func newClock() *clock {
	c := &clock{}
	c.t.Store(time.Date(2026, 11, 20, 12, 0, 0, 0, time.UTC).UnixMilli())
	return c
}
func (c *clock) Now() time.Time          { return time.UnixMilli(c.t.Load()).UTC() }
func (c *clock) Advance(d time.Duration) { c.t.Add(d.Milliseconds()) }

func newService(t *testing.T, clk *clock) (*Service, *sql.DB) {
	t.Helper()
	d, err := db.Open(filepath.Join(t.TempDir(), "marquee.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { d.Close() })
	if _, err := db.Migrate(context.Background(), d, db.SetMain); err != nil {
		t.Fatal(err)
	}
	opts := Options{
		Logger:         slog.New(slog.NewTextHandler(io.Discard, nil)),
		PasswordParams: lightParams,
	}
	if clk != nil {
		opts.Now = clk.Now
	}
	return NewService(d, opts), d
}

var (
	ctx = context.Background()
	ci  = ClientInfo{IP: "10.0.0.1", UserAgent: "test"}
)

func mustOwner(t *testing.T, s *Service) Login {
	t.Helper()
	l, err := s.SetupOwner(ctx, "Bailey", "correct horse", ci)
	if err != nil {
		t.Fatal(err)
	}
	return l
}

func TestConcurrentOwnerSetupOnlyOneWins(t *testing.T) {
	s, d := newService(t, nil)

	const n = 10
	var wg sync.WaitGroup
	var wins, taken atomic.Int32
	start := make(chan struct{})
	errs := make(chan error, n)
	for i := range n {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			_, err := s.SetupOwner(ctx, "owner"+string(rune('a'+i)), "password-"+string(rune('a'+i)), ci)
			switch {
			case err == nil:
				wins.Add(1)
			case errors.Is(err, ErrOwnerExists):
				taken.Add(1)
			default:
				errs <- err
			}
		}()
	}
	close(start)
	wg.Wait()
	close(errs)
	for err := range errs {
		t.Errorf("unexpected error: %v", err)
	}
	if wins.Load() != 1 || taken.Load() != n-1 {
		t.Fatalf("%d goroutines succeeded and %d were refused; want exactly 1 and %d", wins.Load(), taken.Load(), n-1)
	}
	var users int
	if err := d.QueryRow(`SELECT COUNT(*) FROM users`).Scan(&users); err != nil || users != 1 {
		t.Errorf("users table has %d rows, %v; want 1", users, err)
	}
	if need, _ := s.NeedsOwner(ctx); need {
		t.Error("NeedsOwner is still true")
	}
}

func TestOwnerSetupValidation(t *testing.T) {
	s, _ := newService(t, nil)
	cases := []struct {
		name, pw string
		want     error
	}{
		{"", "long enough", ErrInvalidName},
		{"   ", "long enough", ErrInvalidName},
		{"bad\x00name", "long enough", ErrInvalidName},
		{strings.Repeat("x", 41), "long enough", ErrInvalidName},
		{"ok", "short", ErrWeakPassword},
		{"ok", "1234567", ErrWeakPassword},
		{"ok", strings.Repeat("x", 600), ErrPasswordTooLong},
	}
	for _, c := range cases {
		if _, err := s.SetupOwner(ctx, c.name, c.pw, ci); !errors.Is(err, c.want) {
			t.Errorf("SetupOwner(%q, %q) = %v, want %v", c.name, c.pw, err, c.want)
		}
	}
	if need, _ := s.NeedsOwner(ctx); !need {
		t.Fatal("a rejected setup must not create the owner")
	}
	if _, err := s.SetupOwner(ctx, "  Bailey  ", "exactly8", ci); err != nil {
		t.Errorf("an 8-character password should be accepted: %v", err)
	}
}

func TestSignInSessionAndSignOut(t *testing.T) {
	s, _ := newService(t, nil)
	mustOwner(t, s)

	if _, err := s.SignIn(ctx, "bailey", "wrong password", ci); !errors.Is(err, ErrInvalidCredentials) {
		t.Fatalf("wrong password: %v", err)
	}
	if _, err := s.SignIn(ctx, "nobody", "correct horse", ci); !errors.Is(err, ErrInvalidCredentials) {
		t.Fatalf("unknown user: %v", err)
	}
	login, err := s.SignIn(ctx, "BAILEY", "correct horse", ci) // names are case-insensitive
	if err != nil {
		t.Fatal(err)
	}
	if len(login.Token) < 43 { // 32 bytes of base64url
		t.Errorf("token too short: %d chars", len(login.Token))
	}
	p, err := s.AuthenticateSession(ctx, login.Token)
	if err != nil || p.User.Name != "Bailey" || p.User.Role != RoleOwner || p.Via != ViaSession {
		t.Fatalf("AuthenticateSession = %+v, %v", p, err)
	}
	if !p.HasScope(ScopeSettings) || !p.HasScope(ScopeAdmin) {
		t.Errorf("owner session scopes = %v", p.Scopes)
	}
	if err := s.SignOut(ctx, login.Token); err != nil {
		t.Fatal(err)
	}
	if _, err := s.AuthenticateSession(ctx, login.Token); !errors.Is(err, ErrUnauthenticated) {
		t.Errorf("session still valid after sign-out: %v", err)
	}
	if _, err := s.AuthenticateSession(ctx, ""); !errors.Is(err, ErrUnauthenticated) {
		t.Errorf("empty token: %v", err)
	}
}

func TestSessionTokenIsStoredHashed(t *testing.T) {
	s, d := newService(t, nil)
	login := mustOwner(t, s)
	var id string
	if err := d.QueryRow(`SELECT id FROM sessions`).Scan(&id); err != nil {
		t.Fatal(err)
	}
	if id == login.Token || id != hashToken(login.Token) || len(id) != 64 {
		t.Errorf("session id %q is not the sha256 hex of the token", id)
	}
}

func TestSessionsSlideAndExpire(t *testing.T) {
	clk := newClock()
	s, d := newService(t, clk)
	login := mustOwner(t, s)
	first := login.ExpiresAt
	if want := clk.Now().Add(SessionLifetime); !first.Equal(want) {
		t.Fatalf("expiry = %v, want %v", first, want)
	}

	// A quick second use does not write.
	clk.Advance(time.Minute)
	p, err := s.AuthenticateSession(ctx, login.Token)
	if err != nil || p.Refreshed {
		t.Fatalf("early use: refreshed=%v err=%v", p != nil && p.Refreshed, err)
	}

	// Later use slides the expiry 30 days from now and asks for a new cookie.
	clk.Advance(10 * 24 * time.Hour)
	p, err = s.AuthenticateSession(ctx, login.Token)
	if err != nil || !p.Refreshed {
		t.Fatalf("later use: %+v %v", p, err)
	}
	if want := clk.Now().Add(SessionLifetime); !p.ExpiresAt.Equal(want) {
		t.Errorf("slid expiry = %v, want %v", p.ExpiresAt, want)
	}
	var stored string
	_ = d.QueryRow(`SELECT expires_at FROM sessions`).Scan(&stored)
	if stored != db.TS(p.ExpiresAt) {
		t.Errorf("stored expiry %s != %s", stored, db.TS(p.ExpiresAt))
	}

	// Without use it dies 30 days after the last slide.
	clk.Advance(SessionLifetime + time.Minute)
	if _, err := s.AuthenticateSession(ctx, login.Token); !errors.Is(err, ErrUnauthenticated) {
		t.Fatalf("expired session accepted: %v", err)
	}
	var n int
	_ = d.QueryRow(`SELECT COUNT(*) FROM sessions`).Scan(&n)
	if n != 0 {
		t.Error("expired session was not deleted")
	}
}

func TestLoginRateLimit(t *testing.T) {
	clk := newClock()
	s, _ := newService(t, clk)
	mustOwner(t, s)

	for i := range LoginMaxFailures {
		if _, err := s.SignIn(ctx, "bailey", "nope nope", ci); !errors.Is(err, ErrInvalidCredentials) {
			t.Fatalf("attempt %d: %v", i+1, err)
		}
	}
	// Eleventh try is blocked, even with the right password.
	_, err := s.SignIn(ctx, "bailey", "correct horse", ci)
	var rl *RateLimitedError
	if !errors.As(err, &rl) || rl.RetryAfter <= 0 || rl.RetryAfter > LoginWindow {
		t.Fatalf("want *RateLimitedError, got %v", err)
	}
	// Other IPs and other names are not affected.
	if _, err := s.SignIn(ctx, "bailey", "correct horse", ClientInfo{IP: "10.0.0.2"}); err != nil {
		t.Errorf("a different IP should be fine: %v", err)
	}
	if _, err := s.SignIn(ctx, "someone-else", "x", ci); !errors.Is(err, ErrInvalidCredentials) {
		t.Errorf("a different name should not be rate limited yet: %v", err)
	}
	// After the window the block lifts.
	clk.Advance(LoginWindow + time.Second)
	if _, err := s.SignIn(ctx, "bailey", "correct horse", ci); err != nil {
		t.Errorf("after the window: %v", err)
	}
}

func TestSuccessfulLoginClearsFailures(t *testing.T) {
	s, _ := newService(t, nil)
	mustOwner(t, s)
	for range LoginMaxFailures - 1 {
		_, _ = s.SignIn(ctx, "bailey", "bad bad bad", ci)
	}
	if _, err := s.SignIn(ctx, "bailey", "correct horse", ci); err != nil {
		t.Fatal(err)
	}
	for range LoginMaxFailures - 1 {
		if _, err := s.SignIn(ctx, "bailey", "bad bad bad", ci); !errors.Is(err, ErrInvalidCredentials) {
			t.Fatalf("failures were not cleared by a good login: %v", err)
		}
	}
}

func TestPasswordHashFormat(t *testing.T) {
	// Default parameters: time=2, memory=64 MiB, threads=2.
	h := newHasher(DefaultPasswordParams)
	enc, err := h.hash("correct horse")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(enc, "$argon2id$v=19$m=65536,t=2,p=2$") {
		t.Errorf("hash = %s", enc)
	}
	if parts := strings.Split(enc, "$"); len(parts) != 6 {
		t.Errorf("not PHC-shaped: %s", enc)
	}
	if ok, err := h.verify(enc, "correct horse"); !ok || err != nil {
		t.Errorf("verify right = %v, %v", ok, err)
	}
	if ok, _ := h.verify(enc, "wrong horse"); ok {
		t.Error("verify wrong = true")
	}
	other, _ := h.hash("correct horse")
	if other == enc {
		t.Error("two hashes of one password are identical; salt is not random")
	}
	// A hash made with other parameters still verifies (the params are in the string).
	if ok, err := newHasher(*lightParams).verify(enc, "correct horse"); !ok || err != nil {
		t.Errorf("cross-parameter verify = %v, %v", ok, err)
	}
	for _, bad := range []string{"", "plain", "$argon2id$v=19$m=1,t=1,p=1$x", "$bcrypt$v=19$m=65536,t=2,p=2$AAAA$AAAA",
		"$argon2id$v=19$m=999999999,t=2,p=2$AAAA$AAAA"} {
		if ok, err := h.verify(bad, "x"); ok || err == nil {
			t.Errorf("verify(%q) = %v, %v; want an error", bad, ok, err)
		}
	}
}

func TestInviteFlow(t *testing.T) {
	clk := newClock()
	s, _ := newService(t, clk)
	owner := mustOwner(t, s).User

	token, expires, err := s.CreateInvite(ctx, owner, RoleMember)
	if err != nil {
		t.Fatal(err)
	}
	if want := clk.Now().Add(InviteLifetime); !expires.Equal(want) {
		t.Errorf("invite expiry = %v, want %v (7 days)", expires, want)
	}
	login, err := s.AcceptInvite(ctx, token, "Friend", "friend password", ci)
	if err != nil {
		t.Fatal(err)
	}
	if login.User.Role != RoleMember || login.User.Name != "Friend" {
		t.Errorf("new user = %+v", login.User)
	}
	// One-time: a second use fails.
	if _, err := s.AcceptInvite(ctx, token, "Another", "another password", ci); !errors.Is(err, ErrInvalidInvite) {
		t.Errorf("reused invite: %v", err)
	}
	// The new account can sign in.
	if _, err := s.SignIn(ctx, "friend", "friend password", ci); err != nil {
		t.Errorf("friend cannot sign in: %v", err)
	}

	// Taken names do not burn the invite.
	t2, _, _ := s.CreateInvite(ctx, owner, RoleMember)
	if _, err := s.AcceptInvite(ctx, t2, "bailey", "some password", ci); !errors.Is(err, ErrNameTaken) {
		t.Errorf("name taken: %v", err)
	}
	if _, err := s.AcceptInvite(ctx, t2, "Pal", "some password", ci); err != nil {
		t.Errorf("invite should still be usable after a name clash: %v", err)
	}

	// Expiry.
	t3, _, _ := s.CreateInvite(ctx, owner, RoleMember)
	clk.Advance(InviteLifetime + time.Second)
	if _, err := s.AcceptInvite(ctx, t3, "Late", "some password", ci); !errors.Is(err, ErrInvalidInvite) {
		t.Errorf("expired invite: %v", err)
	}
	if _, err := s.AcceptInvite(ctx, "not-a-token", "X", "some password", ci); !errors.Is(err, ErrInvalidInvite) {
		t.Errorf("bogus invite: %v", err)
	}
}

func TestInviteRoleRules(t *testing.T) {
	s, _ := newService(t, nil)
	owner := mustOwner(t, s).User
	adminTok, _, err := s.CreateInvite(ctx, owner, RoleAdmin)
	if err != nil {
		t.Fatal(err)
	}
	adminLogin, err := s.AcceptInvite(ctx, adminTok, "Admin", "admin password", ci)
	if err != nil {
		t.Fatal(err)
	}
	memberTok, _, _ := s.CreateInvite(ctx, owner, RoleMember)
	member, _ := s.AcceptInvite(ctx, memberTok, "Member", "member password", ci)

	if _, _, err := s.CreateInvite(ctx, adminLogin.User, RoleMember); err != nil {
		t.Errorf("admin should invite members: %v", err)
	}
	if _, _, err := s.CreateInvite(ctx, adminLogin.User, RoleAdmin); !errors.Is(err, ErrForbidden) {
		t.Errorf("admin inviting an admin: %v, want ErrForbidden", err)
	}
	if _, _, err := s.CreateInvite(ctx, member.User, RoleMember); !errors.Is(err, ErrForbidden) {
		t.Errorf("member inviting: %v, want ErrForbidden", err)
	}
	if _, _, err := s.CreateInvite(ctx, owner, RoleOwner); !errors.Is(err, ErrInvalidRole) {
		t.Errorf("inviting an owner: %v, want ErrInvalidRole", err)
	}
	if _, _, err := s.CreateInvite(ctx, owner, "root"); !errors.Is(err, ErrInvalidRole) {
		t.Errorf("bogus role: %v", err)
	}
}

func TestAPIKeyLifecycle(t *testing.T) {
	s, d := newService(t, nil)
	owner := mustOwner(t, s).User

	key, secret, err := s.CreateAPIKey(ctx, owner, "Claude", []string{"ask", "see", "see"})
	if err != nil {
		t.Fatal(err)
	}
	parts := strings.Split(secret, "_")
	if len(parts) != 3 || parts[0] != "mq" || parts[1] != key.Prefix || len(parts[1]) != 8 || len(parts[2]) != 52 {
		t.Fatalf("secret = %q, want mq_<8 chars>_<52 chars>", secret)
	}
	if got := strings.Join(key.Scopes, ","); got != "see,ask" {
		t.Errorf("scopes = %s, want de-duplicated in canonical order", got)
	}
	var stored string
	_ = d.QueryRow(`SELECT hash FROM api_keys`).Scan(&stored)
	if stored == parts[2] || strings.Contains(stored, parts[2]) || len(stored) != 64 {
		t.Errorf("secret stored as %q; want a sha256 hex of it", stored)
	}

	p, err := s.AuthenticateAPIKey(ctx, secret)
	if err != nil || p.Via != ViaAPIKey || p.User.ID != owner.ID || p.KeyID != key.ID {
		t.Fatalf("AuthenticateAPIKey = %+v, %v", p, err)
	}
	if !p.HasScope("see") || !p.HasScope("ask") || p.HasScope("settings") || p.HasScope("admin") {
		t.Errorf("key scopes = %v; a key holds only what it was given", p.Scopes)
	}

	// Wrong secret, malformed keys.
	for _, bad := range []string{
		"mq_" + key.Prefix + "_" + strings.Repeat("a", 52),
		"mq_" + key.Prefix, "mq_", "mq__x", "xx_" + key.Prefix + "_" + parts[2], "", "mq_zzzzzzzz_" + parts[2],
	} {
		if _, err := s.AuthenticateAPIKey(ctx, bad); !errors.Is(err, ErrUnauthenticated) {
			t.Errorf("AuthenticateAPIKey(%q) = %v", bad, err)
		}
	}

	keys, err := s.ListAPIKeys(ctx, owner)
	if err != nil || len(keys) != 1 || keys[0].LastUsedAt == nil {
		t.Fatalf("ListAPIKeys = %+v, %v (last_used_at should be recorded)", keys, err)
	}

	// Revoke: rejected afterwards; revoking again is fine; others' keys are 404.
	if err := s.RevokeAPIKey(ctx, owner, key.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := s.AuthenticateAPIKey(ctx, secret); !errors.Is(err, ErrUnauthenticated) {
		t.Errorf("revoked key still works: %v", err)
	}
	if err := s.RevokeAPIKey(ctx, owner, key.ID); err != nil {
		t.Errorf("second revoke: %v", err)
	}
	if err := s.RevokeAPIKey(ctx, owner, 9999); !errors.Is(err, ErrNotFound) {
		t.Errorf("revoke unknown: %v", err)
	}
	keys, _ = s.ListAPIKeys(ctx, owner)
	if len(keys) != 1 || keys[0].RevokedAt == nil {
		t.Errorf("revoked key should stay in the list, marked: %+v", keys)
	}
}

func TestAPIKeyScopeRules(t *testing.T) {
	s, _ := newService(t, nil)
	owner := mustOwner(t, s).User
	tok, _, _ := s.CreateInvite(ctx, owner, RoleMember)
	member := func() User { l, _ := s.AcceptInvite(ctx, tok, "Member", "member password", ci); return l.User }()

	if _, _, err := s.CreateAPIKey(ctx, member, "k", []string{"see", "ask"}); err != nil {
		t.Errorf("member see+ask: %v", err)
	}
	for _, sc := range []string{"approve", "settings", "admin", "delete", "downloads"} {
		_, _, err := s.CreateAPIKey(ctx, member, "k", []string{"see", sc})
		if !errors.Is(err, ErrScopeNotAllowed) {
			t.Errorf("member granting %q: %v, want ErrScopeNotAllowed", sc, err)
		}
	}
	if _, _, err := s.CreateAPIKey(ctx, owner, "k", AllScopes); err != nil {
		t.Errorf("owner with every scope: %v", err)
	}
	if _, _, err := s.CreateAPIKey(ctx, owner, "k", []string{"see", "fly"}); !errors.Is(err, ErrInvalidScope) {
		t.Errorf("unknown scope: %v", err)
	}
	if _, _, err := s.CreateAPIKey(ctx, owner, "k", nil); !errors.Is(err, ErrInvalidScope) {
		t.Errorf("no scopes: %v", err)
	}
	if _, _, err := s.CreateAPIKey(ctx, owner, " ", []string{"see"}); !errors.Is(err, ErrInvalidKeyName) {
		t.Errorf("blank name: %v", err)
	}
}

func TestAPIKeyCannotExceedItsPersonsRole(t *testing.T) {
	s, d := newService(t, nil)
	owner := mustOwner(t, s).User
	_, secret, err := s.CreateAPIKey(ctx, owner, "k", []string{"see", "settings"})
	if err != nil {
		t.Fatal(err)
	}
	// Demote behind the service's back (there is no endpoint for it yet).
	if _, err := d.Exec(`DROP INDEX users_one_owner`); err != nil {
		t.Fatal(err)
	}
	if _, err := d.Exec(`UPDATE users SET role = 'member'`); err != nil {
		t.Fatal(err)
	}
	p, err := s.AuthenticateAPIKey(ctx, secret)
	if err != nil {
		t.Fatal(err)
	}
	if p.HasScope("settings") || !p.HasScope("see") {
		t.Errorf("after demotion scopes = %v, want only see", p.Scopes)
	}
}

func TestOnlyOneOwnerRowCanExist(t *testing.T) {
	s, d := newService(t, nil)
	mustOwner(t, s)
	_, err := d.Exec(`INSERT INTO users (name, role, password_hash, created_at) VALUES ('second', 'owner', 'x', 'x')`)
	if !db.IsUniqueViolation(err) {
		t.Errorf("a second owner row was accepted: %v", err)
	}
}

func TestDisabledUserCannotSignIn(t *testing.T) {
	s, d := newService(t, nil)
	login := mustOwner(t, s)
	_, secret, _ := s.CreateAPIKey(ctx, login.User, "k", []string{"see"})
	if _, err := d.Exec(`UPDATE users SET disabled = 1`); err != nil {
		t.Fatal(err)
	}
	if _, err := s.SignIn(ctx, "bailey", "correct horse", ci); !errors.Is(err, ErrInvalidCredentials) {
		t.Errorf("disabled user signed in: %v", err)
	}
	if _, err := s.AuthenticateSession(ctx, login.Token); !errors.Is(err, ErrUnauthenticated) {
		t.Errorf("disabled user's session works: %v", err)
	}
	if _, err := s.AuthenticateAPIKey(ctx, secret); !errors.Is(err, ErrUnauthenticated) {
		t.Errorf("disabled user's key works: %v", err)
	}
}

func TestCleanupRemovesExpired(t *testing.T) {
	clk := newClock()
	s, d := newService(t, clk)
	owner := mustOwner(t, s).User
	_, _, _ = s.CreateInvite(ctx, owner, RoleMember)
	clk.Advance(SessionLifetime + InviteLifetime + time.Hour)
	if err := s.Cleanup(ctx); err != nil {
		t.Fatal(err)
	}
	for _, table := range []string{"sessions", "invites"} {
		var n int
		_ = d.QueryRow(`SELECT COUNT(*) FROM ` + table).Scan(&n)
		if n != 0 {
			t.Errorf("%s still has %d rows after cleanup", table, n)
		}
	}
}

func TestCreateMemberHasNoWayToSignIn(t *testing.T) {
	s, d := newService(t, nil)
	mustOwner(t, s)
	u, err := s.CreateMember(ctx, "  Priya ")
	if err != nil {
		t.Fatal(err)
	}
	if u.Name != "Priya" || u.Role != RoleMember || u.Disabled || u.ID == 0 {
		t.Errorf("member = %+v", u)
	}
	// Not with an empty password, nor the name, nor a guess.
	for _, pw := range []string{"", "Priya", "password", "correct horse"} {
		if _, err := s.SignIn(ctx, "Priya", pw, ci); !errors.Is(err, ErrInvalidCredentials) {
			t.Errorf("sign-in with %q = %v, want invalid credentials", pw, err)
		}
	}
	var n int
	if err := d.QueryRow(`SELECT COUNT(*) FROM users WHERE role = 'member' AND password_hash LIKE '$argon2id$%'`).Scan(&n); err != nil || n != 1 {
		t.Errorf("the stored hash is not an argon2id hash (%d, %v)", n, err)
	}

	if _, err := s.CreateMember(ctx, "priya"); !errors.Is(err, ErrNameTaken) {
		t.Errorf("same name in another case = %v, want ErrNameTaken", err)
	}
	if _, err := s.CreateMember(ctx, "Bailey"); !errors.Is(err, ErrNameTaken) {
		t.Errorf("the owner's name = %v, want ErrNameTaken", err)
	}
	for _, bad := range []string{"", "   ", strings.Repeat("x", 41), "bad\x00name"} {
		if _, err := s.CreateMember(ctx, bad); !errors.Is(err, ErrInvalidName) {
			t.Errorf("CreateMember(%q) = %v, want ErrInvalidName", bad, err)
		}
	}
	// An owner is still the only owner.
	if need, _ := s.NeedsOwner(ctx); need {
		t.Error("NeedsOwner after members exist")
	}
}
