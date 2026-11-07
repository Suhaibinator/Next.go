//go:build !js

package plainprops

import (
	"context"
	"encoding/json"
	nextgo "github.com/Suhaibinator/Next.go"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestPlainPropsHasIdenticalOrdinarySSRAndNoClientBoundary(t *testing.T) {
	app, err := nextgo.New(nextgo.Config{BuildID: "plain-test", RetainRoutes: true, Routes: []nextgo.Route{{Pattern: "/plain-props", Page: Page}}})
	if err != nil {
		t.Fatal(err)
	}
	defer app.Close(context.Background())
	previous := ""
	for _, target := range []string{"/plain-props?q=a", "/plain-props?q=b", "/plain-props?q=b"} {
		r := httptest.NewRequest("GET", target, nil)
		r.Header.Set("X-Nextgo-Navigate", "1")
		w := httptest.NewRecorder()
		app.ServeHTTP(w, r)
		var data nextgo.Envelope
		if w.Code != 200 || json.Unmarshal(w.Body.Bytes(), &data) != nil || data.Merge != nil || data.Retention == nil || strings.Contains(data.HTML, "data-nextgo-client") || !strings.Contains(data.HTML, `data-ordinary-props-revision="">0`) {
			t.Fatal("not an ordinary initial-source fixture", target, w.Code, w.Body.String())
		}
		if previous != "" && data.HTML != previous {
			t.Fatal("fixture requires exact same server body across queries and Refresh")
		}
		previous = data.HTML
	}
}
