package mcp

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"slices"
	"sync"
	"time"
)

// ---- sliding-window limiter --------------------------------------------------

// limiter allows n events per window for each key (an API key id). It keeps the
// time of every recent event, so the window slides and there is no burst at a
// boundary. State lives in memory: a restart forgets it.
type limiter struct {
	n      int
	window time.Duration

	mu      sync.Mutex
	buckets map[int64][]time.Time
}

func newLimiter(n int, window time.Duration) *limiter {
	return &limiter{n: n, window: window, buckets: map[int64][]time.Time{}}
}

// prune drops events that have left the window. Callers hold l.mu.
func (l *limiter) prune(key int64, now time.Time) []time.Time {
	b := l.buckets[key]
	cut := now.Add(-l.window)
	i := 0
	for i < len(b) && !b[i].After(cut) {
		i++
	}
	if i > 0 {
		b = slices.Clone(b[i:])
		if len(b) == 0 {
			delete(l.buckets, key)
		} else {
			l.buckets[key] = b
		}
	}
	return b
}

// take records one event if the key is under its limit. When it is not, it says
// how long until a place frees up.
func (l *limiter) take(key int64, now time.Time) (ok bool, retryAfter time.Duration) {
	l.mu.Lock()
	defer l.mu.Unlock()
	b := l.prune(key, now)
	if len(b) >= l.n {
		return false, max(b[0].Add(l.window).Sub(now), time.Second)
	}
	l.buckets[key] = append(b, now)
	// Keep the map small when many keys came and went.
	if len(l.buckets) > 4096 {
		for k := range l.buckets {
			l.prune(k, now)
		}
	}
	return true, 0
}

// give takes back an event recorded at "at" (an ask that was refused after its
// place was reserved).
func (l *limiter) give(key int64, at time.Time) {
	l.mu.Lock()
	defer l.mu.Unlock()
	b := l.buckets[key]
	if i := slices.IndexFunc(b, func(t time.Time) bool { return t.Equal(at) }); i >= 0 {
		l.buckets[key] = slices.Delete(slices.Clone(b), i, i+1)
	}
}

// left is how many events the key may still make now, and when the oldest one
// leaves the window (zero when there is none).
func (l *limiter) left(key int64, now time.Time) (int, time.Time) {
	l.mu.Lock()
	defer l.mu.Unlock()
	b := l.prune(key, now)
	if len(b) == 0 {
		return l.n, time.Time{}
	}
	return max(l.n-len(b), 0), b[0].Add(l.window)
}

// ---- confirm tokens ----------------------------------------------------------

// Why a confirm token was refused.
var (
	errTokenUnknown  = errors.New("mcp: unknown or expired confirm token")
	errTokenUsed     = errors.New("mcp: confirm token already used")
	errTokenMismatch = errors.New("mcp: confirm token was issued for a different ask")
)

type confirmEntry struct {
	keyID   int64
	userID  int64
	hash    string
	expires time.Time
	used    bool
}

// confirmTokens are the short-lived, single-use tokens that tie the second call
// of ask_for_title to the first: the same key, the same inputs, within the TTL.
// They are random, kept in memory, and never reused.
type confirmTokens struct {
	ttl time.Duration

	mu sync.Mutex
	m  map[string]*confirmEntry
}

const maxTokensPerKey = 20

func newConfirmTokens(ttl time.Duration) *confirmTokens {
	return &confirmTokens{ttl: ttl, m: map[string]*confirmEntry{}}
}

// issue makes a token for a key and the hash of the inputs it covers.
func (c *confirmTokens) issue(keyID, userID int64, hash string, now time.Time) (string, time.Time, error) {
	var b [18]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "", time.Time{}, err
	}
	token := "cf_" + base64.RawURLEncoding.EncodeToString(b[:])
	exp := now.Add(c.ttl)

	c.mu.Lock()
	defer c.mu.Unlock()
	// Forget old tokens, and cap what one key can hold.
	var mine []string
	for t, e := range c.m {
		switch {
		case !e.expires.After(now):
			delete(c.m, t)
		case e.keyID == keyID:
			mine = append(mine, t)
		}
	}
	if len(mine) >= maxTokensPerKey {
		slices.SortFunc(mine, func(a, b string) int { return c.m[a].expires.Compare(c.m[b].expires) })
		for _, t := range mine[:len(mine)-maxTokensPerKey+1] {
			delete(c.m, t)
		}
	}
	c.m[token] = &confirmEntry{keyID: keyID, userID: userID, hash: hash, expires: exp}
	return token, exp, nil
}

// check says whether the token would be accepted, without using it.
func (c *confirmTokens) check(token string, keyID int64, hash string, now time.Time) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	_, err := c.lookup(token, keyID, hash, now)
	return err
}

// redeem checks the token and uses it up, as one step.
func (c *confirmTokens) redeem(token string, keyID int64, hash string, now time.Time) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	e, err := c.lookup(token, keyID, hash, now)
	if err != nil {
		return err
	}
	e.used = true
	return nil
}

func (c *confirmTokens) lookup(token string, keyID int64, hash string, now time.Time) (*confirmEntry, error) {
	e := c.m[token]
	// A token of another key is "unknown": it must not tell one key about the
	// tokens of another.
	if e == nil || e.keyID != keyID || !e.expires.After(now) {
		return nil, errTokenUnknown
	}
	if e.used {
		return nil, errTokenUsed
	}
	if e.hash != hash {
		return nil, errTokenMismatch
	}
	return e, nil
}

// inputHash is the fingerprint of an ask's inputs.
func inputHash(v any) string {
	b, _ := json.Marshal(v)
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:])
}

// ---- sessions ----------------------------------------------------------------

// A session exists only so that clients written for the 2025 revisions find the
// Mcp-Session-Id header they expect. It carries no authority: every request is
// authenticated by its own API key, and a session id presented by a different
// key is refused.
type session struct {
	id       string
	keyID    int64
	version  string
	client   string
	created  time.Time
	lastSeen time.Time
}

type sessionState int

const (
	sessionUnknown sessionState = iota // never issued here, or forgotten (a restart)
	sessionOK                          // issued to this key
	sessionRefused                     // belongs to another key, or was ended
)

type sessions struct {
	idle time.Duration
	max  int

	mu    sync.Mutex
	m     map[string]*session
	ended map[string]time.Time
}

func newSessions(idle time.Duration, max int) *sessions {
	return &sessions{idle: idle, max: max, m: map[string]*session{}, ended: map[string]time.Time{}}
}

func (s *sessions) create(keyID int64, version, client string, now time.Time) (*session, error) {
	var b [18]byte
	if _, err := rand.Read(b[:]); err != nil {
		return nil, err
	}
	sess := &session{id: "mqs_" + base64.RawURLEncoding.EncodeToString(b[:]), keyID: keyID, version: version,
		client: client, created: now, lastSeen: now}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.sweep(now)
	for len(s.m) >= s.max { // full: drop the least recently used
		var oldest string
		for id, x := range s.m {
			if oldest == "" || x.lastSeen.Before(s.m[oldest].lastSeen) {
				oldest = id
			}
		}
		delete(s.m, oldest)
	}
	s.m[sess.id] = sess
	return sess, nil
}

// check looks at a session id presented with a request.
func (s *sessions) check(id string, keyID int64, now time.Time) (*session, sessionState) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, gone := s.ended[id]; gone {
		return nil, sessionRefused
	}
	x := s.m[id]
	if x == nil {
		return nil, sessionUnknown
	}
	if x.keyID != keyID {
		return nil, sessionRefused
	}
	if now.Sub(x.lastSeen) > s.idle {
		delete(s.m, id)
		return nil, sessionUnknown
	}
	x.lastSeen = now
	return x, sessionOK
}

// end terminates a session (HTTP DELETE). A later request with its id is
// refused, as the specification asks.
func (s *sessions) end(id string, keyID int64, now time.Time) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	x := s.m[id]
	if x == nil || x.keyID != keyID {
		return false
	}
	delete(s.m, id)
	s.ended[id] = now
	return true
}

func (s *sessions) sweep(now time.Time) {
	for id, x := range s.m {
		if now.Sub(x.lastSeen) > s.idle {
			delete(s.m, id)
		}
	}
	for id, t := range s.ended {
		if now.Sub(t) > s.idle {
			delete(s.ended, id)
		}
	}
}

// ---- recent calls ------------------------------------------------------------

// Call is one tool call, as the settings screen lists it. It holds no
// arguments, and no results.
type Call struct {
	At       time.Time `json:"at"`
	Tool     string    `json:"tool"`
	UserID   int64     `json:"-"`
	UserName string    `json:"userName"`
	OK       bool      `json:"ok"`
}

// callRing keeps the most recent calls in memory.
type callRing struct {
	mu   sync.Mutex
	buf  []Call
	next int
	full bool
}

func newCallRing(n int) *callRing { return &callRing{buf: make([]Call, n)} }

func (r *callRing) add(c Call) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.buf[r.next] = c
	r.next = (r.next + 1) % len(r.buf)
	if r.next == 0 {
		r.full = true
	}
}

// list returns the calls newest first; a user id other than 0 keeps only theirs.
func (r *callRing) list(userID int64) []Call {
	r.mu.Lock()
	defer r.mu.Unlock()
	n := r.next
	if r.full {
		n = len(r.buf)
	}
	out := make([]Call, 0, n)
	for i := 1; i <= n; i++ {
		c := r.buf[(r.next-i+len(r.buf))%len(r.buf)]
		if userID == 0 || c.UserID == userID {
			out = append(out, c)
		}
	}
	return out
}
