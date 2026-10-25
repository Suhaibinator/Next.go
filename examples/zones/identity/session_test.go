//go:build !js

package identity

import (
	"net/http/httptest"
	"testing"

	nextgo "github.com/Suhaibinator/Next.go"
)

func TestSyntheticSessionIntegrityAndIndependentMembership(t *testing.T) {
	cookie, err := Issue("alice")
	if err != nil {
		t.Fatal(err)
	}
	r := httptest.NewRequest("GET", "http://alpha.example.test/", nil)
	r.AddCookie(cookie)
	subject, err := Subject(r)
	if err != nil || subject != "alice" || !Member(subject, "alpha") || Member(subject, "beta") {
		t.Fatal(subject, err)
	}
	// Copying the same valid demo identity to another authority does not grant
	// membership there. Tenant selection and subject membership stay separate.
	r = httptest.NewRequest("GET", "http://beta.example.test/", nil)
	r.AddCookie(cookie)
	subject, err = Subject(r)
	if err != nil || Member(subject, "beta") {
		t.Fatal(subject, err)
	}
	r = httptest.NewRequest("GET", "http://alpha.example.test/?subject=alice", nil)
	r.Header.Set("X-User", "alice")
	r.Header.Set("X-Forwarded-Host", "alpha.example.test")
	if _, err := Subject(r); err != nextgo.ErrUnauthorized {
		t.Fatal("request routing claims authenticated a subject", err)
	}
	cookie.Value = "x" + cookie.Value
	r.AddCookie(cookie)
	if _, err := Subject(r); err != nextgo.ErrUnauthorized {
		t.Fatal("tampered session accepted", err)
	}
	if _, err := Issue("administrator"); err != nextgo.ErrUnauthorized {
		t.Fatal("unknown demo subject", err)
	}
}
