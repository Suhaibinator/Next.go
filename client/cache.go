package client

import (
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/Suhaibinator/Next.go/internal/navigation"
)

const navigationCacheLimit = 32
const navigationCacheBytes int64 = 8 << 20
const navigationCacheTTL = 30 * time.Second
const navigationCacheMaxTTL = time.Duration(navigation.MaxStaleMillis) * time.Millisecond

type cacheEntry[T any] struct {
	value   T
	expires time.Time
	used    uint64
	cost    int64
}

// navigationCache is deliberately small and local to one browser runtime. Only
// responses explicitly marked safe by the server are inserted.
type navigationCache[T any] struct {
	mu       sync.Mutex
	entries  map[string]cacheEntry[T]
	sequence uint64
	bytes    int64
	weigh    func(T) int64
	now      func() time.Time
}

func (c *navigationCache[T]) get(key string) (T, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	var zero T
	entry, ok := c.entries[key]
	if !ok {
		return zero, false
	}
	if !c.clock().Before(entry.expires) {
		c.remove(key)
		return zero, false
	}
	c.sequence++
	entry.used = c.sequence
	c.entries[key] = entry
	return entry.value, true
}
func (c *navigationCache[T]) clock() time.Time {
	if c.now != nil {
		return c.now()
	}
	return time.Now()
}
func (c *navigationCache[T]) put(key string, value T, ttl time.Duration) {
	c.putUntil(key, value, c.clock().Add(min(ttl, navigationCacheMaxTTL)))
}

// putUntil preserves a fetch's original deadline even if admission is delayed.
func (c *navigationCache[T]) putUntil(key string, value T, until time.Time) {
	c.mu.Lock()
	defer c.mu.Unlock()
	now := c.clock()
	if !now.Before(until) {
		c.remove(key)
		return
	}
	until = minTime(until, now.Add(navigationCacheMaxTTL))
	if c.entries == nil {
		c.entries = map[string]cacheEntry[T]{}
	}
	c.remove(key)
	cost := int64(len(key)) + 128
	if c.weigh != nil {
		payload := c.weigh(value)
		if payload < 0 || payload > navigationCacheBytes-cost {
			return
		}
		cost += payload
	}
	if cost > navigationCacheBytes {
		return
	}
	for candidate, entry := range c.entries {
		if !now.Before(entry.expires) {
			c.remove(candidate)
		}
	}
	for len(c.entries) >= navigationCacheLimit || c.bytes > navigationCacheBytes-cost {
		var oldest string
		var used uint64
		for candidate, entry := range c.entries {
			if oldest == "" || entry.used < used {
				oldest, used = candidate, entry.used
			}
		}
		c.remove(oldest)
	}
	c.sequence++
	c.entries[key] = cacheEntry[T]{value: value, expires: until, used: c.sequence, cost: cost}
	c.bytes += cost
}

func minTime(a, b time.Time) time.Time {
	if a.Before(b) {
		return a
	}
	return b
}

// Caller holds the cache mutex.
func (c *navigationCache[T]) remove(key string) {
	if entry, ok := c.entries[key]; ok {
		c.bytes -= entry.cost
		delete(c.entries, key)
	}
}

func (c *navigationCache[T]) clear() {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.entries = nil
	c.bytes = 0
}

// The opt-in header explicitly promises request-independent public output. It
// never overrides private/no-store, Set-Cookie, or Vary: *. The server may vary
// public anonymous output on Cookie/Authorization to bypass intermediary caches
// for credentialed requests; authentication changes must call Refresh.
// Legacy HTTP freshness can shorten the default 30s cap. An explicit bounded
// millisecond hint opts into a separate Go window, without overriding privacy.
func cacheLifetime(header interface {
	Get(string) string
	Values(string) []string
}) time.Duration {
	if len(header.Values("X-Nextgo-Prefetch")) != 1 || header.Get("X-Nextgo-Prefetch") != "public" || len(header.Values("Set-Cookie")) > 0 {
		return 0
	}
	for _, vary := range header.Values("Vary") {
		for _, field := range strings.Split(vary, ",") {
			switch strings.ToLower(strings.TrimSpace(field)) {
			case "*":
				return 0
			}
		}
	}
	ttl := navigationCacheTTL
	explicit := len(header.Values(navigation.StaleHeader)) > 0
	if explicit {
		values := header.Values(navigation.StaleHeader)
		if len(values) != 1 {
			return 0
		}
		value := strings.TrimSpace(values[0])
		if value == "" || len(value) > 8 {
			return 0
		}
		for _, r := range value {
			if r < '0' || r > '9' {
				return 0
			}
		}
		millis, err := strconv.ParseUint(value, 10, 64)
		if err != nil || millis == 0 || millis > navigation.MaxStaleMillis {
			return 0
		}
		ttl = time.Duration(millis) * time.Millisecond
	}
	for _, policy := range header.Values("Cache-Control") {
		for _, directive := range strings.Split(policy, ",") {
			parts := strings.SplitN(strings.TrimSpace(strings.ToLower(directive)), "=", 2)
			switch parts[0] {
			case "no-store", "private", "no-cache":
				return 0
			case "max-age":
				if len(parts) != 2 {
					return 0
				}
				seconds, err := strconv.Atoi(strings.Trim(parts[1], `"`))
				if err != nil || seconds < 0 || seconds == 0 && !explicit {
					return 0
				}
				if !explicit && seconds < int(ttl/time.Second) {
					ttl = time.Duration(seconds) * time.Second
				}
			}
		}
	}
	if ages := header.Values("Age"); len(ages) > 0 {
		if len(ages) != 1 {
			return 0
		}
		age, err := strconv.ParseUint(strings.TrimSpace(ages[0]), 10, 64)
		if err != nil || age > uint64(navigationCacheMaxTTL/time.Second) || time.Duration(age)*time.Second >= ttl {
			return 0
		}
		ttl -= time.Duration(age) * time.Second
	}
	return ttl
}
