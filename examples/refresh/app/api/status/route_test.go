//go:build !js

package status

import (
	"encoding/json"
	"net/http/httptest"
	"testing"
)

func TestPublicNativeInstanceAndMonotonicRequests(t *testing.T) {
	var previous struct {
		Instance string `json:"instance"`
		Requests uint64 `json:"requests"`
		Source   string `json:"source"`
	}
	for i := 0; i < 2; i++ {
		w := httptest.NewRecorder()
		Handle(w, httptest.NewRequest("GET", "http://example.test/api/status", nil))
		var current struct {
			Instance string `json:"instance"`
			Requests uint64 `json:"requests"`
			Source   string `json:"source"`
		}
		if w.Code != 200 || w.Header().Get("Cache-Control") != "no-store" || json.Unmarshal(w.Body.Bytes(), &current) != nil || len(current.Instance) != 32 || current.Source != "native-v1" {
			t.Fatal(w.Code, w.Body.String())
		}
		if i > 0 && (current.Instance != previous.Instance || current.Requests != previous.Requests+1) {
			t.Fatal(previous, current)
		}
		previous = current
		w = httptest.NewRecorder()
		Handle(w, httptest.NewRequest("HEAD", "http://example.test/api/status", nil))
		if w.Code != 200 || w.Body.Len() != 0 {
			t.Fatal(w.Code, w.Body.String())
		}
	}
}
