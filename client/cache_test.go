package client

import (
	"fmt"
	"net/http"
	"testing"
	"time"

	"github.com/Suhaibinator/Next.go/internal/navigation"
)

func TestNavigationCacheBoundTTLAndInvalidation(t *testing.T) {
	now := time.Unix(100, 0)
	cache := navigationCache[string]{now: func() time.Time { return now }}
	for i := range navigationCacheLimit {
		cache.put(fmt.Sprint(i), fmt.Sprint(i), navigationCacheTTL)
	}
	if _, ok := cache.get("0"); !ok {
		t.Fatal("missing fresh entry")
	}
	cache.put("new", "new", time.Minute)
	if len(cache.entries) != navigationCacheLimit {
		t.Fatal("cache exceeded bound")
	}
	if _, ok := cache.get("1"); ok {
		t.Fatal("least recently used entry was retained")
	}
	now = now.Add(navigationCacheTTL)
	if _, ok := cache.get("0"); ok {
		t.Fatal("expired entry reused")
	}
	cache.put("fresh", "fresh", time.Second)
	cache.clear()
	if _, ok := cache.get("fresh"); ok {
		t.Fatal("invalidated entry reused")
	}
}

func TestExplicitBrowserWindowsRemainPrivateAndBounded(t *testing.T) {
	for _, tc := range []struct {
		name   string
		values []string
		policy string
		age    string
		want   time.Duration
	}{
		{"long", []string{"300000"}, "public, max-age=0, must-revalidate", "", 5 * time.Minute},
		{"subsecond", []string{"500"}, "public, max-age=0", "", 500 * time.Millisecond},
		{"age", []string{"2500"}, "public, max-age=0", "1", 1500 * time.Millisecond},
		{"expired-age", []string{"500"}, "public, max-age=0", "1", 0},
		{"no-store", []string{"300000"}, "no-store", "", 0},
		{"private", []string{"300000"}, "private", "", 0},
		{"no-cache", []string{"300000"}, "public,no-cache", "", 0},
		{"too-long", []string{"86400001"}, "public", "", 0},
		{"overflow", []string{"999999999999999999999"}, "public", "", 0},
		{"signed", []string{"+100"}, "public", "", 0},
		{"combined", []string{"100,200"}, "public", "", 0},
		{"repeated", []string{"100", "200"}, "public", "", 0},
		{"malformed", []string{"forever"}, "public,max-age=30", "", 0},
	} {
		t.Run(tc.name, func(t *testing.T) {
			header := http.Header{"X-Nextgo-Prefetch": {"public"}, navigation.StaleHeader: tc.values, "Cache-Control": {tc.policy}}
			if tc.age != "" {
				header.Set("Age", tc.age)
			}
			if got := cacheLifetime(header); got != tc.want {
				t.Fatal(got, tc.want)
			}
		})
	}
}

func TestNavigationCacheAbsoluteAdmissionDoesNotExtendDeadlines(t *testing.T) {
	now := time.Unix(100, 0)
	c := navigationCache[string]{now: func() time.Time { return now }}
	until := now.Add(time.Second)
	now = now.Add(2 * time.Second)
	c.putUntil("expired", "value", until)
	if _, ok := c.get("expired"); ok {
		t.Fatal("late result got a new cache lifetime")
	}
	c.putUntil("long", "value", now.Add(48*time.Hour))
	now = now.Add(navigationCacheMaxTTL)
	if _, ok := c.get("long"); ok {
		t.Fatal("maximum lifetime exceeded")
	}
	c.put("profile", "value", 5*time.Minute)
	now = now.Add(time.Minute)
	if _, ok := c.get("profile"); !ok {
		t.Fatal("explicit long profile still capped at legacy 30 seconds")
	}
}

func TestCacheLifetimeRequiresExplicitNonPersonalizedPolicy(t *testing.T) {
	for _, tc := range []struct {
		name   string
		header http.Header
		want   time.Duration
	}{
		{"no opt in", http.Header{}, 0},
		{"public", http.Header{"X-Nextgo-Prefetch": {"public"}}, 30 * time.Second},
		{"short", http.Header{"X-Nextgo-Prefetch": {"public"}, "Cache-Control": {"public, max-age=3"}, "Age": {"1"}}, 2 * time.Second},
		{"no store", http.Header{"X-Nextgo-Prefetch": {"public"}, "Cache-Control": {"no-store"}}, 0},
		{"private", http.Header{"X-Nextgo-Prefetch": {"public"}, "Cache-Control": {"private"}}, 0},
		{"cookie", http.Header{"X-Nextgo-Prefetch": {"public"}, "Set-Cookie": {"session=secret"}}, 0},
		{"public anonymous cookie variation", http.Header{"X-Nextgo-Prefetch": {"public"}, "Vary": {"Cookie"}}, 30 * time.Second},
		{"public anonymous auth variation", http.Header{"X-Nextgo-Prefetch": {"public"}, "Vary": {"Authorization"}}, 30 * time.Second},
		{"all variation", http.Header{"X-Nextgo-Prefetch": {"public"}, "Vary": {"*"}}, 0},
		{"expired", http.Header{"X-Nextgo-Prefetch": {"public"}, "Cache-Control": {"max-age=1"}, "Age": {"2"}}, 0},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := cacheLifetime(tc.header); got != tc.want {
				t.Fatalf("got %s want %s", got, tc.want)
			}
		})
	}
}

func TestNavigationCacheByteBudgetEvictionReplacementAndExpiration(t *testing.T) {
	now := time.Now()
	cache := navigationCache[int64]{now: func() time.Time { return now }, weigh: func(value int64) int64 { return value }}
	for _, key := range []string{"a", "b"} {
		cache.put(key, 3<<20, time.Second)
	}
	_, _ = cache.get("a")
	cache.put("c", 3<<20, time.Second)
	if _, ok := cache.get("b"); ok {
		t.Fatal("byte budget failed to evict least recently used response")
	}
	if cache.bytes > navigationCacheBytes || len(cache.entries) != 2 {
		t.Fatalf("bytes=%d entries=%d", cache.bytes, len(cache.entries))
	}
	cache.put("a", 1<<62, time.Second)
	if _, ok := cache.get("a"); ok {
		t.Fatal("oversized replacement retained stale response")
	}
	now = now.Add(time.Second)
	if _, ok := cache.get("c"); ok || cache.bytes != 0 {
		t.Fatal("expired cost retained")
	}
	cache.put("new", 123, time.Second)
	cache.clear()
	if cache.bytes != 0 {
		t.Fatal("clear retained byte accounting")
	}
	cache.put("negative", -1, time.Second)
	if len(cache.entries) != 0 {
		t.Fatal("negative cost accepted")
	}
}

func TestCacheAgeCannotOverflowOrExtendFreshness(t *testing.T) {
	for _, age := range []string{"18446744073709551615", "9223372036854775807", "18446744073709551616", "invalid", "-1", "30", "31"} {
		header := http.Header{"X-Nextgo-Prefetch": {"public"}, "Age": {age}}
		if ttl := cacheLifetime(header); ttl != 0 {
			t.Fatalf("Age %q produced TTL %v", age, ttl)
		}
	}
	if ttl := cacheLifetime(http.Header{"X-Nextgo-Prefetch": {"public"}, "Age": {"0", "1"}}); ttl != 0 {
		t.Fatal("ambiguous Age admitted")
	}
}
