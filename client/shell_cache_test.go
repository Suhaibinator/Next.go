package client

import (
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/Suhaibinator/Next.go/internal/navigation"
)

func TestPublicShellCacheLifetimeRequiresNegotiatedBoundedOptIn(t *testing.T) {
	valid := func() http.Header {
		return http.Header{"X-Nextgo-Shell-Protocol": {"1"}, "X-Nextgo-Prefetch": {"shell"}, "X-Nextgo-Shell-Stale": {"30000"}, "Cache-Control": {"private, no-store"}}
	}
	shell := strings.Repeat("a", 64)
	for _, tc := range []struct {
		name string
		edit func(http.Header)
		want time.Duration
	}{
		{"valid", func(http.Header) {}, 30 * time.Second},
		{"no-protocol", func(h http.Header) { h.Del(navigation.ShellProtocolHeader) }, 0},
		{"duplicate-protocol", func(h http.Header) { h.Add(navigation.ShellProtocolHeader, "1") }, 0},
		{"wrong-protocol", func(h http.Header) { h.Set(navigation.ShellProtocolHeader, "2") }, 0},
		{"ordinary-page-marker", func(h http.Header) { h.Set("X-Nextgo-Prefetch", "public") }, 0},
		{"cookie", func(h http.Header) { h.Set("Set-Cookie", "session=private") }, 0},
		{"vary-star", func(h http.Header) { h.Set("Vary", "Cookie, *") }, 0},
		{"no-private", func(h http.Header) { h.Set("Cache-Control", "no-store") }, 0},
		{"no-no-store", func(h http.Header) { h.Set("Cache-Control", "private") }, 0},
		{"conflicting-public", func(h http.Header) { h.Add("Cache-Control", "public") }, 0},
		{"age", func(h http.Header) { h.Set("Age", "1") }, 0},
		{"zero", func(h http.Header) { h.Set(navigation.ShellStaleHeader, "0") }, 0},
		{"too-long", func(h http.Header) { h.Set(navigation.ShellStaleHeader, "30001") }, 0},
		{"malformed", func(h http.Header) { h.Set(navigation.ShellStaleHeader, "1e3") }, 0},
		{"duplicate-ttl", func(h http.Header) { h.Add(navigation.ShellStaleHeader, "1") }, 0},
		{"short", func(h http.Header) { h.Set(navigation.ShellStaleHeader, "125") }, 125 * time.Millisecond},
	} {
		t.Run(tc.name, func(t *testing.T) {
			h := valid()
			tc.edit(h)
			if got := shellCacheLifetime(h, shell); got != tc.want {
				t.Fatal(got, tc.want)
			}
			if cacheLifetime(h) != 0 {
				t.Fatal("special shell policy loosened generic page privacy")
			}
		})
	}
	if shellCacheLifetime(valid(), "invalid") != 0 {
		t.Fatal("unbound shell cached")
	}
}
