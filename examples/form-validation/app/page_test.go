//go:build !js

package app

import (
	"context"
	"net/http/httptest"
	"strings"
	"testing"

	nextgo "github.com/Suhaibinator/Next.go"
)

func TestMountedPublicFormsAreUsefulSSRAndDeclareRuntime(t *testing.T) {
	a, err := nextgo.New(nextgo.Config{BuildID: "form-validation-test", BasePath: "/forms-demo", Routes: []nextgo.Route{{Pattern: "/", Page: Page}}})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := a.Close(context.Background()); err != nil {
			t.Error(err)
		}
	})
	w := httptest.NewRecorder()
	a.ServeHTTP(w, httptest.NewRequest("GET", "http://example.test/forms-demo", nil))
	if w.Code != 200 || w.Header().Get(nextgo.RequiresServerHeader) != "1" {
		t.Fatal(w.Code, w.Header(), w.Body.String())
	}
	for _, want := range []string{`data-fv-instance="primary"`, `data-fv-instance="secondary"`, `action="/forms-demo/api/preview"`, `name="channel" type="radio" value=""`, `data-fv-option="0"`, `data-fv-option="1"`} {
		if !strings.Contains(w.Body.String(), want) {
			t.Fatalf("missing %s in %s", want, w.Body.String())
		}
	}
	if strings.Count(w.Body.String(), `name="topics"`) != 2 {
		t.Fatal("forms do not have independent native owners")
	}
}
