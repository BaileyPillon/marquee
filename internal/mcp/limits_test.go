package mcp

import (
	"errors"
	"fmt"
	"testing"
	"time"
)

func TestLimiterSlidesAndRefunds(t *testing.T) {
	l := newLimiter(3, time.Minute)
	now := t0
	for i := range 3 {
		if ok, _ := l.take(1, now.Add(time.Duration(i)*10*time.Second)); !ok {
			t.Fatalf("take %d refused", i+1)
		}
	}
	ok, wait := l.take(1, now.Add(30*time.Second))
	if ok || wait != 30*time.Second {
		t.Errorf("a fourth take = %v, wait %v; want refused, 30s (until the first leaves)", ok, wait)
	}
	if ok, _ := l.take(2, now); !ok {
		t.Error("another key shares the allowance")
	}
	// One event leaves the window at a time.
	if ok, _ := l.take(1, now.Add(61*time.Second)); !ok {
		t.Error("the window did not slide")
	}
	if left, until := l.left(1, now.Add(61*time.Second)); left != 0 || !until.Equal(now.Add(70*time.Second)) {
		t.Errorf("left = %d until %v", left, until)
	}
	// A refund frees a place.
	at := now.Add(62 * time.Second)
	if ok, _ := l.take(3, at); !ok {
		t.Fatal(ok)
	}
	l.give(3, at)
	if left, _ := l.left(3, at); left != 3 {
		t.Errorf("left after a refund = %d", left)
	}
	l.give(3, at) // refunding what is not there is harmless
	if left, _ := l.left(99, at); left != 3 {
		t.Errorf("an unknown key has %d left", left)
	}
	// A retry hint is never zero, so Retry-After is a real number.
	l2 := newLimiter(1, time.Minute)
	l2.take(1, now)
	if _, wait := l2.take(1, now.Add(time.Minute-time.Millisecond)); wait < time.Second {
		t.Errorf("wait = %v, want at least a second", wait)
	}
}

func TestConfirmTokensRules(t *testing.T) {
	c := newConfirmTokens(5 * time.Minute)
	token, exp, err := c.issue(7, 70, "h1", t0)
	if err != nil || !exp.Equal(t0.Add(5*time.Minute)) {
		t.Fatalf("issue = %q %v %v", token, exp, err)
	}
	other, _, _ := c.issue(7, 70, "h1", t0)
	if other == token || len(token) < 20 {
		t.Errorf("tokens must be long and distinct: %q %q", token, other)
	}

	for name, tc := range map[string]struct {
		token string
		key   int64
		hash  string
		at    time.Time
		want  error
	}{
		"another key":    {token, 8, "h1", t0, errTokenUnknown},
		"another inputs": {token, 7, "h2", t0, errTokenMismatch},
		"never issued":   {"cf_nope", 7, "h1", t0, errTokenUnknown},
		"expired":        {token, 7, "h1", t0.Add(5 * time.Minute), errTokenUnknown},
		"just in time":   {token, 7, "h1", t0.Add(5*time.Minute - time.Nanosecond), nil},
	} {
		if got := c.check(tc.token, tc.key, tc.hash, tc.at); !errors.Is(got, tc.want) && got != tc.want {
			t.Errorf("%s: check = %v, want %v", name, got, tc.want)
		}
	}
	// check does not spend; redeem does, once.
	if err := c.redeem(token, 7, "h1", t0); err != nil {
		t.Fatal(err)
	}
	if err := c.redeem(token, 7, "h1", t0); !errors.Is(err, errTokenUsed) {
		t.Errorf("second redeem = %v", err)
	}
	if err := c.check(token, 7, "h1", t0); !errors.Is(err, errTokenUsed) {
		t.Errorf("check after redeem = %v", err)
	}
	// An old token is forgotten, and one key cannot fill the memory.
	for i := range maxTokensPerKey + 10 {
		if _, _, err := c.issue(9, 90, "h", t0.Add(time.Duration(i)*time.Second)); err != nil {
			t.Fatal(err)
		}
	}
	count := func() (n int) {
		c.mu.Lock()
		defer c.mu.Unlock()
		for _, e := range c.m {
			if e.keyID == 9 {
				n++
			}
		}
		return n
	}
	if n := count(); n > maxTokensPerKey {
		t.Errorf("a key holds %d tokens, cap is %d", n, maxTokensPerKey)
	}
	if _, _, err := c.issue(9, 90, "h", t0.Add(10*time.Minute)); err != nil {
		t.Fatal(err)
	}
	c.mu.Lock()
	_, still := c.m[token]
	c.mu.Unlock()
	if n := count(); n != 1 {
		t.Errorf("after everything expired, %d tokens remain", n)
	}
	if still {
		t.Error("an expired token was not forgotten")
	}
}

func TestInputHashIsStable(t *testing.T) {
	a := askKey{Kind: "movie", Provider: "tmdb", ProviderID: "20", Quality: "hd", Seasons: []int{1, 2}}
	b := a
	if inputHash(a) != inputHash(b) {
		t.Error("the same inputs hash differently")
	}
	for name, c := range map[string]askKey{
		"quality": {Kind: "movie", Provider: "tmdb", ProviderID: "20", Quality: "uhd", Seasons: []int{1, 2}},
		"id":      {Kind: "movie", Provider: "tmdb", ProviderID: "21", Quality: "hd", Seasons: []int{1, 2}},
		"seasons": {Kind: "movie", Provider: "tmdb", ProviderID: "20", Quality: "hd", Seasons: []int{1}},
		"note":    {Kind: "movie", Provider: "tmdb", ProviderID: "20", Quality: "hd", Seasons: []int{1, 2}, Note: "x"},
		"title":   {TitleID: 5, Quality: "hd"},
	} {
		if inputHash(a) == inputHash(c) {
			t.Errorf("a different %s hashes the same", name)
		}
	}
}

func TestSessionsLifecycle(t *testing.T) {
	s := newSessions(time.Hour, 3)
	a, err := s.create(1, "2025-11-25", "c", t0)
	if err != nil {
		t.Fatal(err)
	}
	if _, st := s.check(a.id, 1, t0); st != sessionOK {
		t.Errorf("own session = %v", st)
	}
	if _, st := s.check(a.id, 2, t0); st != sessionRefused {
		t.Errorf("another key's session = %v", st)
	}
	if _, st := s.check("mqs_x", 1, t0); st != sessionUnknown {
		t.Errorf("unknown = %v", st)
	}
	// Idle sessions are forgotten (and then unknown, not refused).
	if _, st := s.check(a.id, 1, t0.Add(2*time.Hour)); st != sessionUnknown {
		t.Errorf("an idle session = %v", st)
	}
	// Using it keeps it alive.
	b, _ := s.create(1, "v", "c", t0)
	for i := 1; i <= 5; i++ {
		if _, st := s.check(b.id, 1, t0.Add(time.Duration(i)*50*time.Minute)); st != sessionOK {
			t.Fatalf("a session in use ended at step %d", i)
		}
	}
	// The table is bounded: the least recently used goes first.
	var ids []string
	for i := range 4 {
		x, _ := s.create(1, "v", fmt.Sprint(i), t0.Add(5*time.Hour+time.Duration(i)*time.Second))
		ids = append(ids, x.id)
	}
	if _, st := s.check(ids[0], 1, t0.Add(5*time.Hour+5*time.Second)); st != sessionUnknown {
		t.Errorf("the oldest of four in a table of three = %v", st)
	}
	if _, st := s.check(ids[3], 1, t0.Add(5*time.Hour+5*time.Second)); st != sessionOK {
		t.Errorf("the newest = %v", st)
	}
}

func TestCallRing(t *testing.T) {
	r := newCallRing(4)
	if got := r.list(0); len(got) != 0 {
		t.Errorf("a new ring = %v", got)
	}
	for i := range 6 {
		r.add(Call{Tool: fmt.Sprint("t", i), UserID: int64(i % 2), UserName: "u", OK: i%3 != 0, At: t0.Add(time.Duration(i) * time.Second)})
	}
	got := r.list(0)
	if len(got) != 4 || got[0].Tool != "t5" || got[3].Tool != "t2" {
		t.Errorf("ring = %+v, want t5..t2 newest first", got)
	}
	if mine := r.list(1); len(mine) != 2 || mine[0].Tool != "t5" || mine[1].Tool != "t3" {
		t.Errorf("user 1 = %+v", mine)
	}

	// The real one keeps 200.
	big := newCallRing(RecentCallsKept)
	for i := range RecentCallsKept + 50 {
		big.add(Call{Tool: fmt.Sprint(i)})
	}
	if l := big.list(0); len(l) != 200 || l[0].Tool != "249" || l[199].Tool != "50" {
		t.Errorf("200-slot ring: %d entries, first %q last %q", len(l), l[0].Tool, l[len(l)-1].Tool)
	}
}
