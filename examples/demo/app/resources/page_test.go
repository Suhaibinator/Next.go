//go:build !js

package resources

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	nextgo "github.com/Suhaibinator/Next.go"
)

func TestResourcePageDeclaresServerDependencyForHTMLHeadAndNavigation(t *testing.T) {
	app, err := nextgo.New(nextgo.Config{BuildID: "resource-page-test", BasePath: "/site", Routes: []nextgo.Route{{Pattern: "/resources", Page: Page}}})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := app.Close(context.Background()); err != nil {
			t.Error(err)
		}
	})
	for _, kind := range []string{"html", "head", "navigation"} {
		method := http.MethodGet
		if kind == "head" {
			method = http.MethodHead
		}
		request := httptest.NewRequest(method, "http://example.test/site/resources", nil)
		if kind == "navigation" {
			request.Header.Set("X-Nextgo-Navigate", "1")
		}
		response := httptest.NewRecorder()
		app.ServeHTTP(response, request)
		if response.Code != http.StatusOK || response.Header().Get(nextgo.RequiresServerHeader) != "1" || len(response.Header().Values(nextgo.RequiresServerHeader)) != 1 {
			t.Fatal(kind, response.Code, response.Header(), response.Body.String())
		}
		if kind == "head" {
			if response.Body.Len() != 0 {
				t.Fatal("HEAD emitted resource body")
			}
			continue
		}
		if !strings.Contains(response.Body.String(), "Public Go resource") || !strings.Contains(response.Body.String(), "/site/api/resources") {
			t.Fatal(kind, "resource SSR or mounted HTTP endpoint missing", response.Body.String())
		}
	}
}
