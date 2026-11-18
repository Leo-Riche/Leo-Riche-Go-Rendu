package open5e

import (
	"sync"
	"time"
)

// DefaultTTL is how long a cached API response stays fresh.
const DefaultTTL = 5 * time.Minute

// DefaultReapInterval is how often the reaper goroutine wakes up to purge
// expired entries.
const DefaultReapInterval = time.Minute

// entry is one cached response body plus its expiry.
type entry struct {
	body      []byte
	expiresAt time.Time
}

// Stats is a snapshot of cache activity, printed by the cache command.
type Stats struct {
	Entries int
	Hits    int
	Misses  int
	Purged  int
	TTL     time.Duration
}

// Cache is an in-memory, TTL-bounded store of raw API responses keyed by URL.
// It is safe for concurrent use: every field below is guarded by mu, and the
// concurrent prefetcher writes to it from several goroutines at once.
//
// A reaper goroutine started by NewCache periodically drops expired entries so
// that a long session does not keep every page it ever read. Stop ends it.
type Cache struct {
	mu      sync.Mutex
	entries map[string]entry
	ttl     time.Duration
	now     func() time.Time
	hits    int
	misses  int
	purged  int

	stop     chan struct{}
	stopOnce sync.Once
	done     chan struct{}
}

// NewCache returns a running cache. A ttl or interval below or equal to zero
// falls back to the package defaults.
func NewCache(ttl, interval time.Duration) *Cache {
	return newCache(ttl, interval, time.Now)
}

// newCache is NewCache with an injectable clock, which the tests use to expire
// entries without sleeping.
func newCache(ttl, interval time.Duration, now func() time.Time) *Cache {
	if ttl <= 0 {
		ttl = DefaultTTL
	}
	if interval <= 0 {
		interval = DefaultReapInterval
	}
	if now == nil {
		now = time.Now
	}
	c := &Cache{
		entries: make(map[string]entry),
		ttl:     ttl,
		now:     now,
		stop:    make(chan struct{}),
		done:    make(chan struct{}),
	}
	go c.reaper(interval)
	return c
}

// reaper purges expired entries until the cache is stopped.
func (c *Cache) reaper(interval time.Duration) {
	defer close(c.done)
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		select {
		case <-ticker.C:
			c.Reap()
		case <-c.stop:
			return
		}
	}
}

// Get returns a fresh entry. An entry past its TTL is dropped and reported as
// a miss, so a stale body is never served even between two reaper ticks.
func (c *Cache) Get(key string) ([]byte, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()

	e, ok := c.entries[key]
	if !ok {
		c.misses++
		return nil, false
	}
	if !c.now().Before(e.expiresAt) {
		delete(c.entries, key)
		c.purged++
		c.misses++
		return nil, false
	}
	c.hits++
	return e.body, true
}

// Put stores a response body under key with a fresh TTL.
func (c *Cache) Put(key string, body []byte) {
	if key == "" || body == nil {
		return
	}
	stored := make([]byte, len(body))
	copy(stored, body)

	c.mu.Lock()
	defer c.mu.Unlock()
	c.entries[key] = entry{body: stored, expiresAt: c.now().Add(c.ttl)}
}

// Reap drops every expired entry and returns how many it removed. The reaper
// goroutine calls it on a ticker; tests call it directly.
func (c *Cache) Reap() int {
	c.mu.Lock()
	defer c.mu.Unlock()

	now := c.now()
	removed := 0
	for key, e := range c.entries {
		if !now.Before(e.expiresAt) {
			delete(c.entries, key)
			removed++
		}
	}
	c.purged += removed
	return removed
}

// Len is the number of entries currently held, expired or not.
func (c *Cache) Len() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return len(c.entries)
}

// Stats returns a snapshot of cache activity.
func (c *Cache) Stats() Stats {
	c.mu.Lock()
	defer c.mu.Unlock()
	return Stats{
		Entries: len(c.entries),
		Hits:    c.hits,
		Misses:  c.misses,
		Purged:  c.purged,
		TTL:     c.ttl,
	}
}

// Stop ends the reaper goroutine and waits for it to exit. It is safe to call
// more than once.
func (c *Cache) Stop() {
	c.stopOnce.Do(func() {
		close(c.stop)
		<-c.done
	})
}
