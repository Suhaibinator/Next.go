package draft

import (
	"bytes"
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/Suhaibinator/Next.go/cache"
)

func manager(t *testing.T, change func(*Options)) *Manager {
	t.Helper()
	o := Options{Keys: map[string][]byte{"a": bytes.Repeat([]byte{7}, 32)}, ActiveKey: "a", Scope: "test-app", Path: "/demo", TTL: time.Hour}
	if change != nil {
		change(&o)
	}
	m, err := New(o)
	if err != nil {
		t.Fatal(err)
	}
	m.now = func() time.Time { return time.Unix(1800000000, 0) }
	return m
}
func request(cookie *http.Cookie, target string) *http.Request {
	r := httptest.NewRequest("GET", target, nil)
	if cookie != nil {
		r.AddCookie(cookie)
	}
	return r
}
func TestSignedSessionsAreScopedExpiringAndDoNotExposeSecrets(t *testing.T) {
	m := manager(t, nil)
	r := request(nil, "https://EXAMPLE.test/demo/preview")
	c, err := m.Cookie(r)
	if err != nil {
		t.Fatal(err)
	}
	if !c.Secure || !c.HttpOnly || c.SameSite != http.SameSiteLaxMode || c.Domain != "" || c.Path != "/demo" || c.MaxAge != 3600 {
		t.Fatal(c)
	}
	if strings.Contains(c.Value, "test-app") || strings.Contains(c.Value, strings.Repeat("7", 32)) {
		t.Fatal("cookie discloses configuration")
	}
	r = request(c, "https://example.test/demo/preview")
	bound := m.BindRequest(r)
	if !Enabled(bound.Context()) || !cache.Bypassed(bound.Context()) || Enabled(r.Context()) {
		t.Fatal("request state not isolated")
	}
	for _, target := range []string{"https://other.test/demo/preview", "https://example.test/democracy", "https://example.test/other"} {
		if Enabled(m.BindRequest(request(c, target)).Context()) {
			t.Fatal("accepted outside scope", target)
		}
	}
	tampered := *c
	tampered.Value = c.Value[:len(c.Value)-5] + "AAAAA"
	if m.valid(request(&tampered, "https://example.test/demo/preview")) {
		t.Fatal("tamper accepted")
	}
	r.AddCookie(c)
	if m.valid(r) {
		t.Fatal("ambiguous cookies accepted")
	}
	other := manager(t, func(o *Options) { o.Scope = "other-app" })
	if other.valid(request(c, "https://example.test/demo")) {
		t.Fatal("scope replay accepted")
	}
	localHTTP := manager(t, func(o *Options) { o.AllowInsecure = true })
	if localHTTP.valid(request(c, "https://example.test/demo")) {
		t.Fatal("secure policy replay accepted")
	}
	m.now = func() time.Time { return time.Unix(1800003600, 0) }
	if m.valid(request(c, "https://example.test/demo")) {
		t.Fatal("expired cookie accepted")
	}
	m.now = func() time.Time { return time.Unix(1799999969, 0) }
	if m.valid(request(c, "https://example.test/demo")) {
		t.Fatal("future cookie accepted")
	}
}
func TestKeysAreOwnedAndRotationAndClearingAreExplicit(t *testing.T) {
	key := bytes.Repeat([]byte{7}, 32)
	m := manager(t, func(o *Options) { o.Keys["a"] = key })
	c, err := m.Cookie(request(nil, "https://example.test/demo"))
	if err != nil {
		t.Fatal(err)
	}
	key[0] = 99
	if !m.valid(request(c, "https://example.test/demo")) {
		t.Fatal("caller mutated signing key")
	}
	rotated := manager(t, func(o *Options) { o.Keys["b"] = bytes.Repeat([]byte{8}, 32); o.ActiveKey = "b" })
	if !rotated.valid(request(c, "https://example.test/demo")) {
		t.Fatal("rotation rejected retained key")
	}
	newCookie, err := rotated.Cookie(request(nil, "https://example.test/demo"))
	if err != nil || !strings.HasPrefix(newCookie.Value, "b.") {
		t.Fatal(newCookie, err)
	}
	retired := manager(t, func(o *Options) { delete(o.Keys, "a"); o.Keys["b"] = bytes.Repeat([]byte{8}, 32); o.ActiveKey = "b" })
	if retired.valid(request(c, "https://example.test/demo")) {
		t.Fatal("retired key still accepted")
	}
	clear := m.ClearCookie()
	if clear.Name != c.Name || clear.Path != c.Path || clear.MaxAge != -1 || !clear.Secure || !clear.HttpOnly {
		t.Fatal(clear)
	}
	if !m.valid(request(c, "https://example.test/demo")) {
		t.Fatal("clear unexpectedly revoked a stateless copy")
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := m.Cookie(request(nil, "https://example.test/demo").WithContext(ctx)); err != context.Canceled {
		t.Fatal(err)
	}
}
func TestInvalidConfigurationAndMalformedCookiesFailClosed(t *testing.T) {
	for _, change := range []func(*Options){func(o *Options) { o.Keys = nil }, func(o *Options) { o.Keys["a"] = []byte("short") }, func(o *Options) { o.ActiveKey = "missing" }, func(o *Options) { o.Scope = "" }, func(o *Options) { o.Scope = "a\x00b" }, func(o *Options) { o.Path = "/a/../b" }, func(o *Options) { o.Path = "/a;bad" }, func(o *Options) { o.CookieName = "bad name" }, func(o *Options) { o.CookieName = "__Host-preview" }, func(o *Options) { o.TTL = 25 * time.Hour }, func(o *Options) { o.TTL = time.Minute + time.Nanosecond }} {
		o := Options{Keys: map[string][]byte{"a": bytes.Repeat([]byte{7}, 32)}, ActiveKey: "a", Scope: "app", Path: "/demo"}
		change(&o)
		if _, err := New(o); err == nil {
			t.Fatal("invalid configuration accepted", o.ActiveKey, o.Scope, o.Path)
		}
	}
	m := manager(t, nil)
	for _, token := range []string{"", "x", "a..", "a.!!!!.!!!!", strings.Repeat("x", 257)} {
		if m.valid(request(&http.Cookie{Name: "nextgo_draft", Value: token}, "https://example.test/demo")) {
			t.Fatal("invalid token accepted")
		}
	}
	var zero Manager
	if zero.Validate() == nil || zero.valid(request(nil, "https://example.test/demo")) {
		t.Fatal("zero manager accepted")
	}
	if _, err := zero.Cookie(request(nil, "https://example.test/demo")); err == nil {
		t.Fatal("zero manager issued cookie")
	}
}
