package actions

import (
	"fmt"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/Suhaibinator/Next.go/internal/paths"
)

func TestActionAccessErrorsHaveSafeJSONAndProgressiveHTML(t *testing.T) {
	for _, tc := range []struct {
		denial error
		status int
		code   string
	}{{ErrUnauthorized, 401, "unauthorized"}, {ErrForbidden, 403, "forbidden"}} {
		for _, jsonMode := range []bool{false, true} {
			invalidated := false
			router := New(Options{UnauthorizedChallenge: `Bearer realm="test"`, OnRevalidate: func([]string, []string) { invalidated = true }})
			router.Handle("review", func(*Context) (Result, error) {
				return Result{Data: "secret result", RevalidateTags: []string{"private"}}, fmt.Errorf("private credential detail: %w", tc.denial)
			})
			r := httptest.NewRequest("POST", "http://example.test/review", strings.NewReader(""))
			r.Header.Set("Origin", "http://example.test")
			r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
			if jsonMode {
				r.Header.Set("Accept", "application/json")
			}
			r = r.WithContext(paths.WithContext(r.Context(), paths.Config{BasePath: "/mounted", TrailingSlash: true}))
			w := httptest.NewRecorder()
			router.ServeHTTP(w, r)
			if w.Code != tc.status || invalidated || strings.Contains(w.Body.String(), "private credential") || strings.Contains(w.Body.String(), "secret result") {
				t.Fatal(w.Code, w.Body.String(), invalidated)
			}
			if tc.status == 401 && w.Header().Get("WWW-Authenticate") != `Bearer realm="test"` {
				t.Fatal("missing authentication challenge")
			}
			if w.Header().Get("Cache-Control") != "private, no-store" {
				t.Fatal("denial cacheable", w.Header())
			}
			if jsonMode && !strings.Contains(w.Body.String(), `"code":"`+tc.code+`"`) {
				t.Fatal("wrong denial code", w.Body.String())
			}
			if !jsonMode && !strings.Contains(w.Body.String(), `href="/mounted/"`) {
				t.Fatal("progressive return URL ignores mount", w.Body.String())
			}
		}
	}
}

func TestRejectInvalidAuthenticationChallengeConfiguration(t *testing.T) {
	for _, challenge := range []string{"Bearer\r\nSet-Cookie: injected", strings.Repeat("a", 8193)} {
		func() {
			defer func() {
				if recover() == nil {
					t.Fatal("invalid challenge accepted")
				}
			}()
			New(Options{UnauthorizedChallenge: challenge})
		}()
	}
}
