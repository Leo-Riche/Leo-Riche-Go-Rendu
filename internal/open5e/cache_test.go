package open5e

import (
	"fmt"
	"sync"
	"testing"
	"time"
)

// fakeClock is an injectable clock, so cache expiry can be tested without
// sleeping for five minutes.
type fakeClock struct {
	mu  sync.Mutex
	now time.Time
}

func newFakeClock() *fakeClock {
	return &fakeClock{now: time.Date(2026, 11, 17, 12, 0, 0, 0, time.UTC)}
}

func (c *fakeClock) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.now
}

func (c *fakeClock) Advance(d time.Duration) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.now = c.now.Add(d)
}

func TestCachePutGet(t *testing.T) {
	clock := newFakeClock()
	c := newCache(time.Minute, time.Hour, clock.Now)
	defer c.Stop()

	if _, ok := c.Get("missing"); ok {
		t.Error("an empty cache must not report a hit")
	}

	c.Put("key", []byte("body"))
	got, ok := c.Get("key")
	if !ok {
		t.Fatal("the entry just stored is not a hit")
	}
	if string(got) != "body" {
		t.Errorf("cached body = %q, want %q", got, "body")
	}

	stats := c.Stats()
	if stats.Entries != 1 || stats.Hits != 1 || stats.Misses != 1 {
		t.Errorf("stats = %+v, want 1 entry, 1 hit, 1 miss", stats)
	}
}

func TestCachePutCopiesTheBody(t *testing.T) {
	c := newCache(time.Minute, time.Hour, newFakeClock().Now)
	defer c.Stop()

	body := []byte("original")
	c.Put("key", body)
	body[0] = 'X'

	got, _ := c.Get("key")
	if string(got) != "original" {
		t.Errorf("cached body = %q; the cache must not alias the caller's slice", got)
	}
}

func TestCacheExpiresOnGet(t *testing.T) {
	clock := newFakeClock()
	c := newCache(5*time.Minute, time.Hour, clock.Now)
	defer c.Stop()

	c.Put("key", []byte("body"))

	clock.Advance(4*time.Minute + 59*time.Second)
	if _, ok := c.Get("key"); !ok {
		t.Error("the entry expired one second early")
	}

	clock.Advance(2 * time.Second)
	if _, ok := c.Get("key"); ok {
		t.Error("the entry is past its TTL and must not be served")
	}
	if c.Len() != 0 {
		t.Errorf("an expired entry should be dropped on read, %d left", c.Len())
	}
}

func TestCacheReapPurgesExpiredEntriesOnly(t *testing.T) {
	clock := newFakeClock()
	c := newCache(time.Minute, time.Hour, clock.Now)
	defer c.Stop()

	c.Put("old", []byte("a"))
	clock.Advance(90 * time.Second)
	c.Put("fresh", []byte("b"))

	if removed := c.Reap(); removed != 1 {
		t.Errorf("Reap() removed %d entries, want 1", removed)
	}
	if _, ok := c.Get("fresh"); !ok {
		t.Error("the fresh entry was reaped")
	}
	if _, ok := c.Get("old"); ok {
		t.Error("the expired entry survived the reaper")
	}
	if got := c.Stats().Purged; got != 1 {
		t.Errorf("purged count = %d, want 1", got)
	}
}

// TestCacheReaperGoroutineRuns proves the background goroutine started by
// NewCache actually purges, by giving it a very short interval.
func TestCacheReaperGoroutineRuns(t *testing.T) {
	clock := newFakeClock()
	c := newCache(time.Millisecond, time.Millisecond, clock.Now)
	defer c.Stop()

	c.Put("key", []byte("body"))
	clock.Advance(time.Second)

	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if c.Len() == 0 {
			return
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatal("the reaper goroutine never purged the expired entry")
}

func TestCacheStopIsIdempotent(t *testing.T) {
	c := newCache(time.Minute, time.Minute, nil)
	c.Stop()
	c.Stop() // must not panic or block
}

// TestCacheConcurrentAccess is the mutex test: run it with -race.
func TestCacheConcurrentAccess(t *testing.T) {
	c := newCache(time.Minute, 10*time.Millisecond, nil)
	defer c.Stop()

	const workers = 16
	var wg sync.WaitGroup
	for w := 0; w < workers; w++ {
		wg.Add(1)
		go func(w int) {
			defer wg.Done()
			for i := 0; i < 200; i++ {
				key := fmt.Sprintf("key-%d", (w+i)%32)
				c.Put(key, []byte(key))
				c.Get(key)
				if i%50 == 0 {
					c.Reap()
					c.Stats()
					c.Len()
				}
			}
		}(w)
	}
	wg.Wait()
}

func TestNewCacheFallsBackToDefaults(t *testing.T) {
	c := newCache(0, 0, nil)
	defer c.Stop()
	if got := c.Stats().TTL; got != DefaultTTL {
		t.Errorf("TTL = %s, want the default %s", got, DefaultTTL)
	}
}
