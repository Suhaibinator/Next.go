//go:build !js

package app

import (
	"context"
	"encoding/json"
	"net/http/httptest"
	"strings"
	"testing"

	nextgo "github.com/Suhaibinator/Next.go"
)

func TestEmbeddedContentHasUsefulEscapedHTMLAndNavigation(t *testing.T) {
	app, err := nextgo.New(nextgo.Config{BuildID: "content-example", Routes: []nextgo.Route{{Pattern: "/", Page: Page}}})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = app.Close(context.Background()) })
	for _, navigation := range []bool{false, true} {
		request := httptest.NewRequest("GET", "http://example.test/", nil)
		if navigation {
			request.Header.Set("X-Nextgo-Navigate", "1")
		}
		response := httptest.NewRecorder()
		app.ServeHTTP(response, request)
		body := response.Body.String()
		if response.Code != 200 || response.Header().Get(nextgo.RequiresServerHeader) != "" {
			t.Fatal(response.Code, response.Header(), body)
		}
		if navigation {
			var envelope nextgo.Envelope
			if err := json.Unmarshal(response.Body.Bytes(), &envelope); err != nil || envelope.Version != nextgo.NavigationProtocolVersion {
				t.Fatal(envelope, err)
			}
			body = envelope.HTML
		}
		for _, expected := range []string{"<h1>A small, explicit content format</h1>", "<strong>real HTML</strong>", `<code class="language-go">`, "&lt;script&gt;", "Ordinary Go composition", `href="#guide"`} {
			if !strings.Contains(body, expected) {
				t.Errorf("navigation=%v missing %q", navigation, expected)
			}
		}
		if strings.Contains(body, "<script>This raw") {
			t.Fatal("raw source became executable HTML")
		}
	}
}
