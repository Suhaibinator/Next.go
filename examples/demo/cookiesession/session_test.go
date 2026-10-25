//go:build !js

package cookiesession

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	nextgo "github.com/Suhaibinator/Next.go"
	"github.com/Suhaibinator/Next.go/examples/demo/access"
	"github.com/Suhaibinator/Next.go/internal/paths"
)

const readerCredential = "reader-cookie-test-credential-000000000000"
const adminCredential = "admin-cookie-test-credential-0000000000000"

func configure(t *testing.T) {
	t.Helper()
	t.Setenv("NEXTGO_DEMO_COOKIE_KEY", strings.Repeat("17", 32))
	t.Setenv("NEXTGO_DEMO_COOKIE_ALLOW_INSECURE", "false")
	t.Setenv("NEXTGO_DEMO_READER_TOKEN", readerCredential)
	t.Setenv("NEXTGO_DEMO_ADMIN_TOKEN", adminCredential)
}

func request(cookie *http.Cookie) *http.Request {
	r := httptest.NewRequest("GET", "https://example.test/cookie-session", nil)
	if cookie != nil {
		r.AddCookie(cookie)
	}
	return r
}

func TestSignedCookieAttributesRoleAndStatelessRead(t *testing.T) {
	configure(t)
	ctx := context.Background()
	for _, test := range []struct {
		credential string
		role       access.Role
	}{{readerCredential, access.Reader}, {adminCredential, access.Administrator}} {
		r := request(nil)
		r.Header.Set("X-Role", "untrusted")
		r.Header.Set("Authorization", "Bearer original-must-not-establish-identity")
		role, cookie, err := Login(ctx, r, test.credential)
		if err != nil || role != test.role || cookie == nil {
			t.Fatal("valid configured credential rejected", role, err)
		}
		if r.Header.Get("Authorization") != "Bearer original-must-not-establish-identity" || cookie.Path != "/" || !cookie.HttpOnly || !cookie.Secure || cookie.SameSite != http.SameSiteLaxMode || cookie.Domain != "" || cookie.MaxAge != 900 || cookie.Expires.Before(time.Now().Add(14*time.Minute)) {
			t.Fatal("cookie placement/security or original request changed")
		}
		// Verification requires no issuer-owned map or process-local identity.
		fresh := request(cookie)
		actual, err := Read(ctx, fresh)
		if err != nil || actual != test.role {
			t.Fatal("fresh signed-cookie read failed", actual, err)
		}
		payload, _, _ := strings.Cut(cookie.Value, ".")
		decoded, _ := base64.RawURLEncoding.DecodeString(payload)
		if strings.Contains(string(decoded), test.credential) || strings.Contains(cookie.Value, test.credential) || len(cookie.Value) > maxTokenBytes {
			t.Fatal("credential serialized or token bound exceeded")
		}
		if _, err := ReadReport(ctx, fresh); test.role == access.Administrator && err != nil || test.role == access.Reader && !errors.Is(err, nextgo.ErrForbidden) {
			t.Fatal("report authorization did not reverify role", err)
		}
		if _, err := readAt(ctx, fresh, cookie.Expires); !errors.Is(err, nextgo.ErrUnauthorized) {
			t.Fatal("expired cookie accepted", err)
		}
	}
}

func TestConfigurationAndTransportFailClosed(t *testing.T) {
	configure(t)
	ctx := context.Background()
	for _, key := range []string{"", "short", strings.Repeat("g", 64)} {
		t.Setenv("NEXTGO_DEMO_COOKIE_KEY", key)
		if Available(ctx, request(nil)) {
			t.Fatal("missing or malformed signing key enabled demo")
		}
		if _, cookie, err := Login(ctx, request(nil), adminCredential); err == nil || cookie != nil {
			t.Fatal("disabled login produced a cookie")
		}
	}
	t.Setenv("NEXTGO_DEMO_COOKIE_KEY", strings.Repeat("17", 32))
	insecure := httptest.NewRequest("GET", "http://127.0.0.1:3011/cookie-session", nil)
	if Available(ctx, insecure) {
		t.Fatal("HTTP enabled without explicit local opt-in")
	}
	insecure.Header.Set("X-Forwarded-Proto", "https")
	if Available(ctx, insecure) {
		t.Fatal("forwarded header granted TLS")
	}
	t.Setenv("NEXTGO_DEMO_COOKIE_ALLOW_INSECURE", "true")
	_, cookie, err := Login(ctx, insecure, readerCredential)
	if err != nil || cookie == nil || cookie.Secure {
		t.Fatal("explicit loopback mode failed", err)
	}
	foreign := httptest.NewRequest("GET", "http://example.test/cookie-session", nil)
	if Available(ctx, foreign) {
		t.Fatal("local opt-in enabled non-loopback HTTP")
	}
}

func TestCookieRejectsHostScopeTamperingDuplicatesAndBearerSpoofs(t *testing.T) {
	configure(t)
	ctx := context.Background()
	_, cookie, err := Login(ctx, request(nil), adminCredential)
	if err != nil {
		t.Fatal(err)
	}
	for _, header := range []string{cookie.Name + "=" + cookie.Value + "; " + cookie.Name + "=", cookie.Name + "=" + cookie.Value + "; " + cookie.Name, cookie.Name + "=\"" + cookie.Value + "\"", cookie.Name + "=" + cookie.Value + "x", "padding=" + strings.Repeat("a", maxCookieHeaderBytes) + "; " + cookie.Name + "=" + cookie.Value} {
		r := request(nil)
		r.Header.Set("Cookie", header)
		if _, err := Read(ctx, r); !errors.Is(err, nextgo.ErrUnauthorized) {
			t.Fatal("ambiguous or invalid cookie accepted", err)
		}
	}
	spoof := request(nil)
	spoof.Header.Set("Authorization", "Bearer "+adminCredential)
	spoof.Header.Set("X-Role", "administrator")
	spoof.URL.RawQuery = "role=administrator&credential=" + adminCredential
	if _, err := Read(ctx, spoof); !errors.Is(err, nextgo.ErrUnauthorized) {
		t.Fatal("bearer/query/header granted cookie identity", err)
	}
	if _, _, err := Login(ctx, spoof, readerCredential+"wrong"); !errors.Is(err, nextgo.ErrUnauthorized) {
		t.Fatal("original bearer substituted for explicit credential", err)
	}
	for _, variation := range []struct {
		host string
		base string
	}{{"different.test", ""}, {"example.test:8443", ""}, {"example.test", "/base/fr"}, {"example.test", "/base/en"}} {
		otherCtx := paths.WithContext(ctx, paths.Config{BasePath: variation.base})
		other := request(nil)
		other.Host = variation.host
		name, _, err := Scope(otherCtx, other)
		if err != nil {
			t.Fatal(err)
		}
		copyCookie := *cookie
		copyCookie.Name = name // Renaming cannot defeat the full MAC scope binding.
		other.AddCookie(&copyCookie)
		if _, err := Read(otherCtx, other); !errors.Is(err, nextgo.ErrUnauthorized) {
			t.Fatal("cookie replay crossed host/port/application scope", variation, err)
		}
	}
	base := paths.WithContext(ctx, paths.Config{BasePath: "/base/fr"})
	_, scoped, err := Login(base, request(nil), readerCredential)
	if err != nil || scoped.Path != "/base/fr" || scoped.Name == cookie.Name {
		t.Fatal("scoped cookie placement incorrect", err)
	}
}

func TestClaimsValidationAndLogoutDoesNotPretendToRevoke(t *testing.T) {
	configure(t)
	ctx := context.Background()
	r := request(nil)
	_, cookie, err := Login(ctx, r, adminCredential)
	if err != nil {
		t.Fatal(err)
	}
	payload, _, _ := strings.Cut(cookie.Value, ".")
	decoded, _ := base64.RawURLEncoding.DecodeString(payload)
	valid, err := decodeClaims(decoded)
	if err != nil {
		t.Fatal(err)
	}
	s, _ := requestScope(ctx, r)
	key, _ := signingKey()
	for _, mutate := range []func(*claims){func(c *claims) { c.Version = 2 }, func(c *claims) { c.Role = "owner" }, func(c *claims) { c.Issued = time.Now().Add(time.Minute).Unix() }, func(c *claims) { c.Expires = c.Issued + 901 }, func(c *claims) { c.Expires = c.Issued }, func(c *claims) { c.Nonce = "short" }} {
		changed := valid
		mutate(&changed)
		data, _ := json.Marshal(changed)
		encoded := base64.RawURLEncoding.EncodeToString(data)
		copyCookie := *cookie
		copyCookie.Value = encoded + "." + base64.RawURLEncoding.EncodeToString(signature(key, s.binding, encoded))
		if _, err := Read(ctx, request(&copyCookie)); !errors.Is(err, nextgo.ErrUnauthorized) {
			t.Fatal("invalid signed claims accepted", err)
		}
	}
	for _, data := range []string{`{"Version":1,"Version":1,"Role":"reader","Issued":1,"Expires":2,"Nonce":"a"}`, `{"Version":1,"Role":"reader","Issued":1,"Expires":2,"Nonce":"a","extra":true}`, string(decoded) + `{}`} {
		if _, err := decodeClaims([]byte(data)); err == nil {
			t.Fatal("ambiguous claim JSON accepted")
		}
	}
	clear, err := Clear(ctx, r)
	if err != nil || clear.Name != cookie.Name || clear.Path != cookie.Path || clear.Secure != cookie.Secure || clear.HttpOnly != cookie.HttpOnly || clear.SameSite != cookie.SameSite || clear.MaxAge != -1 || clear.Value != "" {
		t.Fatal("logout did not preserve deletion identity", err)
	}
	if role, err := Read(ctx, request(cookie)); err != nil || role != access.Administrator {
		t.Fatal("stateless demo falsely pretended browser deletion revokes a copied token", role, err)
	}
	cancelled, cancel := context.WithCancel(ctx)
	cancel()
	if _, err := Read(cancelled, request(cookie)); !errors.Is(err, context.Canceled) {
		t.Fatal("data read ignored cancellation", err)
	}
}
