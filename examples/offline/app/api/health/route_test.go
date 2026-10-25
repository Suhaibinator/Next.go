//go:build !js

package health

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestHealthIsAnExplicitNetworkOnlyEndpoint(t *testing.T) {
	for _, method := range []string{http.MethodGet, http.MethodHead, http.MethodPost} {
		w := httptest.NewRecorder()
		Handle(w, httptest.NewRequest(method, "https://reader.example/api/health", nil))
		if w.Header().Get("Cache-Control") != "no-store" {
			t.Fatal(method, w.Header())
		}
		if method == http.MethodPost {
			if w.Code != 405 || w.Header().Get("Allow") != "GET, HEAD" {
				t.Fatal(w.Code, w.Header())
			}
			continue
		}
		if w.Code != 200 || w.Header().Get("Content-Type") != "application/json; charset=utf-8" {
			t.Fatal(method, w.Code, w.Header())
		}
		if method == http.MethodHead {
			if w.Body.Len() != 0 {
				t.Fatal("HEAD sent a body", w.Body.String())
			}
			continue
		}
		var result struct {
			OK     bool   `json:"ok"`
			Source string `json:"source"`
		}
		if err := json.Unmarshal(w.Body.Bytes(), &result); err != nil || !result.OK || result.Source != "live-network-only" {
			t.Fatal(result, err)
		}
	}
}
