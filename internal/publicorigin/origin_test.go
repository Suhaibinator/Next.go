package publicorigin

import (
	"context"
	"crypto/tls"
	"net/http/httptest"
	"testing"
)

func TestDeclaredOriginIsSeparateFromTransportAndHeaders(t *testing.T) {
	declared, err := Parse("https://PUBLIC.example:8443")
	if err != nil {
		t.Fatal(err)
	}
	r := httptest.NewRequest("POST", "http://backend.example/path", nil)
	r.RemoteAddr = "192.0.2.1:1234"
	r.Header.Set("X-Forwarded-Host", "attacker.example")
	r.Header.Set("X-Forwarded-Proto", "https")
	r = r.WithContext(WithContext(r.Context(), declared))
	got, err := Request(r)
	if err != nil || got.String() != "https://public.example:8443" || got.Host() != "public.example:8443" || got.Scheme() != "https" {
		t.Fatal(got, err)
	}
	if r.TLS != nil || r.Host != "backend.example" || r.RemoteAddr != "192.0.2.1:1234" {
		t.Fatal("origin declaration changed transport evidence")
	}
	cleared := WithContext(r.Context(), Origin{})
	if !Present(cleared) || FromContext(cleared).String() != "" {
		t.Fatal("clearing marker did not shadow the inherited origin")
	}
	got, err = Request(r.WithContext(cleared))
	if err != nil || got.String() != "http://backend.example" {
		t.Fatal("ordinary origin inferred forwarded headers", got, err)
	}
	r.TLS = &tls.ConnectionState{}
	got, err = Request(r.WithContext(cleared))
	if err != nil || got.String() != "https://backend.example" {
		t.Fatal(got, err)
	}
}

func TestOriginRequiresLiteralHTTPAuthority(t *testing.T) {
	for _, raw := range []string{"", "ftp://example.test", "https://user:password@example.test", "https://example.test/", "https://example.test/path", "https://example.test?x", "https://example.test#f", "https://example.test\\evil", "https://example.test\r\nX:x", "https://example.test:0"} {
		if _, err := Parse(raw); err == nil {
			t.Errorf("accepted origin %q", raw)
		}
	}
	if Present(nil) || Present(context.Background()) || FromContext(nil).String() != "" {
		t.Fatal("ordinary context acquired a declaration")
	}
	if _, err := Request(nil); err == nil {
		t.Fatal("nil request accepted")
	}
}
