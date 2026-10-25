//go:build !js

package validate

import (
	"context"
	"encoding/json"
	"net/http/httptest"
	"testing"

	"github.com/Suhaibinator/Next.go/examples/form-validation/model"
)

func TestPublicFieldChecksAndCancellation(t *testing.T) {
	for _, tc := range []struct {
		query   string
		status  int
		message string
	}{{"field=name&value=Ada", 200, ""}, {"field=name&value=reserved", 200, "This public name is reserved."}, {"field=code&value=reserved", 200, "This public code is reserved."}, {"field=name&value=offline", 503, ""}, {"field=secret&value=x", 400, ""}, {"field=name&field=code&value=x", 400, ""}, {"field=name&value=x&unknown=y", 400, ""}} {
		w := httptest.NewRecorder()
		Handle(w, httptest.NewRequest("GET", "http://example.test/api/validate?"+tc.query, nil))
		if w.Code != tc.status || w.Header().Get("Cache-Control") != "no-store" {
			t.Fatal(tc, w.Code, w.Body.String())
		}
		if tc.status == 200 {
			var result model.ValidationResult
			if err := json.Unmarshal(w.Body.Bytes(), &result); err != nil || result.Message != tc.message {
				t.Fatal(result, err)
			}
		}
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	w := httptest.NewRecorder()
	Handle(w, httptest.NewRequest("GET", "http://example.test/api/validate?field=name&value=slow-reserved", nil).WithContext(ctx))
	if w.Body.Len() != 0 {
		t.Fatal("canceled check published a response", w.Body.String())
	}
	w = httptest.NewRecorder()
	Handle(w, httptest.NewRequest("HEAD", "http://example.test/api/validate?field=name&value=Ada", nil))
	if w.Code != 200 || w.Body.Len() != 0 {
		t.Fatal("HEAD", w.Code, w.Body.String())
	}
}
