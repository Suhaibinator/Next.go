//go:build !js

package lazyprops

import (
	"context"
	"encoding/json"
	nextgo "github.com/Suhaibinator/Next.go"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestLazyPropsPageProvidesUsefulOrdinarySSRAndPublicCurrentQuery(t *testing.T) {
	app, err := nextgo.New(nextgo.Config{BuildID: "lazy-props-test", RetainRoutes: true, Routes: []nextgo.Route{{Pattern: "/lazy-props", Page: Page}}})
	if err != nil {
		t.Fatal(err)
	}
	defer app.Close(context.Background())
	for _, query := range []string{"tea", "fresh"} {
		request := httptest.NewRequest("GET", "/lazy-props?q="+query, nil)
		request.Header.Set("X-Nextgo-Navigate", "1")
		response := httptest.NewRecorder()
		app.ServeHTTP(response, request)
		var page nextgo.Envelope
		if response.Code != 200 || json.Unmarshal(response.Body.Bytes(), &page) != nil || page.Title != "Lazy ordinary props · "+query || page.Merge != nil || page.Retention == nil {
			t.Fatal("invalid ordinary lazy page", query, response.Code, response.Body.String())
		}
		for _, expected := range []string{`data-nextgo-island="lazyordinary"`, `data-lazy-count="">2`, `data-lazy-revision="">0`, `data-lazy-increment="" disabled=""`, `Fresh lazy ordinary source query ` + query} {
			if !strings.Contains(page.HTML, expected) {
				t.Fatalf("useful server fallback missing %q: %s", expected, page.HTML)
			}
		}
		if strings.Contains(page.HTML, "data-nextgo-client") {
			t.Fatal("ordinary lazy fixture became a ClientView boundary")
		}
	}
}
