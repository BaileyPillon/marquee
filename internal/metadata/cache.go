package metadata

import (
	"context"
	"errors"
	"log/slog"
	"sync"
	"time"
)

// Cache lifetimes.
const (
	SearchTTL  = 6 * time.Hour
	DetailsTTL = 24 * time.Hour
	// maxStale is how long past its expiry an entry is still kept, to serve
	// when the provider is down.
	maxStale = 30 * 24 * time.Hour
)

type bypassKey struct{}

// BypassCache makes the calls under ctx skip fresh cache hits and ask the
// provider again (the result is still stored). A manual or scheduled refresh
// uses it; ordinary lookups do not.
func BypassCache(ctx context.Context) context.Context {
	return context.WithValue(ctx, bypassKey{}, true)
}

func bypassed(ctx context.Context) bool {
	v, _ := ctx.Value(bypassKey{}).(bool)
	return v
}

// Cache keeps provider answers in the database.
type Cache struct {
	store  *store
	status *Status
	log    *slog.Logger
	now    func() time.Time
}

// fetch returns the cached body for key while it is fresh. Otherwise it calls
// load and stores the answer. If load fails because the provider is down and
// an older copy exists, the older copy is returned (stale-while-error). A
// provider saying "not found", or a bad key, is an answer, not an outage, and
// is never papered over with old data.
//
// sticky calls (an album's track list, which almost never changes) ignore
// BypassCache.
func (c *Cache) fetch(ctx context.Context, provider, key string, ttl time.Duration, sticky bool,
	load func(context.Context) ([]byte, error)) ([]byte, error) {

	now := c.now()
	body, expires, found, err := c.store.get(ctx, provider, key)
	if err != nil {
		// A broken cache must not stop lookups.
		c.log.Warn("metadata cache read failed", "provider", provider, "err", err)
		found = false
	}
	if found && now.Before(expires) && (sticky || !bypassed(ctx)) {
		return body, nil
	}

	fresh, err := load(ctx)
	switch {
	case err == nil:
		c.status.ok(provider)
		if perr := c.store.put(ctx, provider, key, fresh, now, now.Add(ttl)); perr != nil {
			c.log.Warn("metadata cache write failed", "provider", provider, "err", perr)
		}
		return fresh, nil
	case ctx.Err() != nil:
		return nil, err // the caller went away; not the provider's fault
	case errors.Is(err, ErrNotFound):
		c.status.ok(provider)
		return nil, err
	case errors.Is(err, ErrTMDBKeyInvalid):
		c.status.fail(provider, err)
		return nil, err
	case !errors.Is(err, ErrUpstream):
		return nil, err
	}
	c.status.fail(provider, err)
	if found {
		c.log.Warn("metadata provider failed, serving the stale copy", "provider", provider, "err", err)
		return body, nil
	}
	return nil, err
}

// fresh reports whether key has a copy that has not expired.
func (c *Cache) fresh(ctx context.Context, provider, key string) bool {
	_, expires, found, err := c.store.get(ctx, provider, key)
	return err == nil && found && c.now().Before(expires)
}

// Prune deletes entries that expired long ago.
func (c *Cache) Prune(ctx context.Context) (int64, error) {
	return c.store.prune(ctx, c.now().Add(-maxStale))
}

// ---- provider health -------------------------------------------------------

// ProviderStatus is what is known about one provider since the program started.
type ProviderStatus struct {
	LastOK      *time.Time `json:"lastOk"`
	LastError   string     `json:"lastError"`
	LastErrorAt *time.Time `json:"lastErrorAt"`
}

// Status tracks each provider's last success and last failure.
type Status struct {
	mu  sync.Mutex
	now func() time.Time
	m   map[string]*ProviderStatus
}

func newStatus(now func() time.Time) *Status {
	s := &Status{now: now, m: map[string]*ProviderStatus{}}
	for _, p := range []string{ProviderTMDB, ProviderTVmaze, ProviderMusicBrainz, ProviderArtwork} {
		s.m[p] = &ProviderStatus{}
	}
	return s
}

func (s *Status) ok(provider string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	t := s.now()
	s.entry(provider).LastOK = &t
	// A success clears the error: the status shows what is wrong now.
	e := s.entry(provider)
	e.LastError, e.LastErrorAt = "", nil
}

func (s *Status) fail(provider string, err error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	t := s.now()
	e := s.entry(provider)
	e.LastError, e.LastErrorAt = err.Error(), &t
}

func (s *Status) entry(provider string) *ProviderStatus {
	e := s.m[provider]
	if e == nil {
		e = &ProviderStatus{}
		s.m[provider] = e
	}
	return e
}

// Snapshot copies the current state of every provider.
func (s *Status) Snapshot() map[string]ProviderStatus {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make(map[string]ProviderStatus, len(s.m))
	for k, v := range s.m {
		out[k] = *v
	}
	return out
}
