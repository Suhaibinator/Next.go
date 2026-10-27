//go:build !js

package boot

import (
	"context"
	"encoding/json"
	nextgo "github.com/Suhaibinator/Next.go"
	"html"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestBootHasOnlyAnOrdinarySSRIslandAndNoRetentionDescriptor(t *testing.T) {
	app, err := nextgo.New(nextgo.Config{BuildID: "boot-test", RetainRoutes: true, Routes: []nextgo.Route{{Pattern: "/boot", Page: Page}}})
	if err != nil {
		t.Fatal(err)
	}
	defer app.Close(context.Background())
	for _, step := range []string{"start", "arrive"} {
		r := httptest.NewRequest("GET", "/boot?step="+step, nil)
		r.Header.Set("X-Nextgo-Navigate", "1")
		w := httptest.NewRecorder()
		app.ServeHTTP(w, r)
		var data nextgo.Envelope
		if w.Code != 200 || json.Unmarshal(w.Body.Bytes(), &data) != nil {
			t.Fatal(step, w.Code, w.Body.String())
		}
		output := html.UnescapeString(data.HTML)
		if data.Title != "Boot "+step || data.Description != "Ordinary initial boot "+step || data.Retention != nil || data.Merge != nil || strings.Contains(output, "data-nextgo-client") || !strings.Contains(output, `data-nextgo-island="ordinary"`) || !strings.Contains(output, "Ordinary initial boot "+step) || !strings.Contains(output, `data-ordinary-count="">2`) {
			t.Fatal("boot fixture must be real ordinary SSR without merge or retention", step, data)
		}
		if strings.Contains(output, `"boot":true`) != (step == "start") {
			t.Fatal("only initial start may navigate during Mount", step, output)
		}
	}
}
