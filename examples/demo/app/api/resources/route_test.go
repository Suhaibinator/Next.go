//go:build !js

package resources

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/Suhaibinator/Next.go/examples/demo/components/resourcepanel"
)

func TestResourceEndpointPublicGETHeadFailureAndMethod(t *testing.T) {
	for _, method := range []string{http.MethodGet, http.MethodHead} {
		request := httptest.NewRequest(method, "http://demo.test/api/resources", nil)
		request.Header.Set("Authorization", "Bearer never-echo-this")
		request.AddCookie(&http.Cookie{Name: "session", Value: "never-echo-this"})
		response := httptest.NewRecorder()
		Handle(response, request)
		if response.Code != http.StatusOK || response.Header().Get("Cache-Control") != "public, max-age=30" || len(response.Header().Values("Set-Cookie")) != 0 {
			t.Fatal(response.Code, response.Header())
		}
		if method == http.MethodHead {
			if response.Body.Len() != 0 {
				t.Fatal("HEAD body emitted")
			}
			continue
		}
		var data resourcepanel.Data
		if err := json.Unmarshal(response.Body.Bytes(), &data); err != nil {
			t.Fatal(err)
		}
		if data.Message != "Public Go resource" || strings.Contains(response.Body.String(), "never-echo") {
			t.Fatal(response.Body.String())
		}
		if _, err := time.Parse(time.RFC3339Nano, data.Generated); err != nil {
			t.Fatal(err)
		}
	}
	response := httptest.NewRecorder()
	Handle(response, httptest.NewRequest("GET", "http://demo.test/api/resources?fail=1", nil))
	if response.Code != http.StatusServiceUnavailable || response.Header().Get("Cache-Control") != "no-store" || !json.Valid(response.Body.Bytes()) {
		t.Fatal(response.Code, response.Body.String())
	}
	response = httptest.NewRecorder()
	Handle(response, httptest.NewRequest("POST", "http://demo.test/api/resources", nil))
	if response.Code != http.StatusMethodNotAllowed || response.Header().Get("Allow") != "GET, HEAD" {
		t.Fatal(response.Code, response.Header())
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	response = httptest.NewRecorder()
	Handle(response, httptest.NewRequest("GET", "http://demo.test/api/resources", nil).WithContext(ctx))
	if response.Code != http.StatusServiceUnavailable || response.Header().Get("Cache-Control") != "no-store" {
		t.Fatal(response.Code, response.Header())
	}
}
