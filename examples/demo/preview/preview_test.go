//go:build !js

package preview_test

import (
	"net/http/httptest"
	"strings"
	"testing"

	nextgo "github.com/Suhaibinator/Next.go"
	"github.com/Suhaibinator/Next.go/draft"
	"github.com/Suhaibinator/Next.go/examples/demo/preview"
	"github.com/Suhaibinator/Next.go/ui"
)

func TestPreviewEntryRequiresEditorAuthorizationAndSameOriginPOST(t *testing.T) {
	admin, reader := "editor-"+strings.Repeat("a", 32), "reader-"+strings.Repeat("r", 32)
	t.Setenv("NEXTGO_DEMO_DRAFT_KEY", strings.Repeat("d7", 32))
	t.Setenv("NEXTGO_DEMO_DRAFT_HTTP", "true")
	t.Setenv("NEXTGO_DEMO_ADMIN_TOKEN", admin)
	t.Setenv("NEXTGO_DEMO_READER_TOKEN", reader)
	c := nextgo.Config{BasePath: "/demo", Routes: []nextgo.Route{{Pattern: "/preview", Page: func(c *nextgo.Context) (nextgo.Page, error) {
		return nextgo.Page{Body: ui.Text("draft=" + map[bool]string{true: "yes", false: "no"}[draft.Enabled(c)])}, nil
	}}}}
	preview.Configure(&c)
	app, err := nextgo.New(c)
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		method, origin, token, body string
		status                      int
	}{
		{"GET", "http://example.test", admin, "", 405},
		{"POST", "https://foreign.test", admin, "", 403},
		{"POST", "", admin, "", 403},
		{"POST", "http://example.test", "bad", "", 401},
		{"POST", "http://example.test", reader, "", 403},
		{"POST", "http://example.test", admin, strings.Repeat("x", 5000), 413},
		{"POST", "http://example.test", admin, "", 303},
		{"POST", "http://example.test", "", "token=" + admin, 303},
	} {
		r := httptest.NewRequest(tc.method, "http://example.test/demo/api/preview/enable", strings.NewReader(tc.body))
		r.Header.Set("Origin", tc.origin)
		r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		if tc.token != "" {
			r.Header.Set("Authorization", "Bearer "+tc.token)
		}
		w := httptest.NewRecorder()
		app.ServeHTTP(w, r)
		if w.Code != tc.status {
			t.Fatal(tc.method, tc.status, w.Code, w.Body.String())
		}
		cookies := w.Result().Cookies()
		if tc.status != 303 {
			if len(cookies) != 0 {
				t.Fatal("denied entry issued cookie")
			}
			continue
		}
		if len(cookies) != 1 || cookies[0].Path != "/demo" || !cookies[0].HttpOnly || w.Header().Get("Location") != "/demo/preview" || w.Header().Get("Cache-Control") != "private, no-store" {
			t.Fatal(w.Header())
		}
		page := httptest.NewRequest("GET", "http://example.test/demo/preview", nil)
		page.AddCookie(cookies[0])
		result := httptest.NewRecorder()
		app.ServeHTTP(result, page)
		if result.Code != 200 || !strings.Contains(result.Body.String(), "draft=yes") {
			t.Fatal(result.Code, result.Body.String())
		}
	}
}
