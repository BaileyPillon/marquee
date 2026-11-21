package accounts

import (
	"sync"
	"time"
)

// Login rate limit: this many failures per window per IP+name.
const (
	LoginMaxFailures = 10
	LoginWindow      = 5 * time.Minute
)

// loginLimiter counts failed sign-ins in a sliding window.
type loginLimiter struct {
	mu     sync.Mutex
	max    int
	window time.Duration
	now    func() time.Time
	fails  map[string][]time.Time
}

func newLoginLimiter(max int, window time.Duration, now func() time.Time) *loginLimiter {
	return &loginLimiter{max: max, window: window, now: now, fails: map[string][]time.Time{}}
}

// retryAfter returns 0 if the key may try, otherwise how long until it may.
func (l *loginLimiter) retryAfter(key string) time.Duration {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.prune(key)
	f := l.fails[key]
	if len(f) < l.max {
		return 0
	}
	return max(f[0].Add(l.window).Sub(l.now()), time.Second)
}

func (l *loginLimiter) fail(key string) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.prune(key)
	l.fails[key] = append(l.fails[key], l.now())
}

func (l *loginLimiter) reset(key string) {
	l.mu.Lock()
	defer l.mu.Unlock()
	delete(l.fails, key)
}

// sweep forgets keys whose failures have all aged out.
func (l *loginLimiter) sweep() {
	l.mu.Lock()
	defer l.mu.Unlock()
	for k := range l.fails {
		l.prune(k)
	}
}

// prune drops expired entries for key. Caller holds the lock.
func (l *loginLimiter) prune(key string) {
	f := l.fails[key]
	if len(f) == 0 {
		delete(l.fails, key)
		return
	}
	cut := l.now().Add(-l.window)
	i := 0
	for i < len(f) && !f[i].After(cut) {
		i++
	}
	if i == len(f) {
		delete(l.fails, key)
	} else if i > 0 {
		l.fails[key] = f[i:]
	}
}
