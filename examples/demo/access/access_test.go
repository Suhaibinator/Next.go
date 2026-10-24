//go:build !js

package access

import (
	"context"
	"errors"
	"net/http/httptest"
	"strings"
	"testing"

	nextgo "github.com/Suhaibinator/Next.go"
)

func TestAuthenticationAndDataAuthorization(t *testing.T) {
	reader, admin := strings.Repeat("r", 40), strings.Repeat("a", 40)
	t.Setenv("NEXTGO_DEMO_READER_TOKEN", reader)
	t.Setenv("NEXTGO_DEMO_ADMIN_TOKEN", admin)
	for _, tc := range []struct {
		header string
		role   Role
		denial error
	}{{"", "", nextgo.ErrUnauthorized}, {"Bearer wrong", "", nextgo.ErrUnauthorized}, {"Basic " + admin, "", nextgo.ErrUnauthorized}, {"Bearer " + reader, Reader, nextgo.ErrForbidden}, {"Bearer " + admin, Administrator, nil}, {"bearer " + admin, Administrator, nil}} {
		r := httptest.NewRequest("GET", "/restricted?role=administrator&token="+admin, nil)
		r.Header.Set("X-Role", "administrator")
		if tc.header != "" {
			r.Header.Set("Authorization", tc.header)
		}
		role, _ := Authenticate(r)
		if role != tc.role {
			t.Fatal("role", role, tc.role)
		}
		value, err := ReadReport(context.Background(), r)
		if !errors.Is(err, tc.denial) || (value.Title != "") != (tc.denial == nil) {
			t.Fatalf("data boundary: role=%q value=%+v err=%v", role, value, err)
		}
	}
	r := httptest.NewRequest("GET", "/restricted", nil)
	r.Header.Add("Authorization", "Bearer "+reader)
	r.Header.Add("Authorization", "Bearer "+admin)
	if _, err := ReadReport(context.Background(), r); !errors.Is(err, nextgo.ErrUnauthorized) {
		t.Fatal("duplicate credentials accepted")
	}
	t.Setenv("NEXTGO_DEMO_ADMIN_TOKEN", "")
	r.Header.Set("Authorization", "Bearer "+admin)
	if _, err := ReadReport(context.Background(), r); !errors.Is(err, nextgo.ErrUnauthorized) {
		t.Fatal("disabled principal accepted")
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := ReadReport(ctx, r); !errors.Is(err, context.Canceled) {
		t.Fatal("canceled loader ran", err)
	}
}
