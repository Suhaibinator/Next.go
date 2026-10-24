//go:build !js

package app_test

import (
	"encoding/json"
	nextgo "github.com/Suhaibinator/Next.go"
	"github.com/Suhaibinator/Next.go/actions"
	"github.com/Suhaibinator/Next.go/examples/demo/app"
	"github.com/Suhaibinator/Next.go/examples/demo/state"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func actionRequest(body, kind string) *http.Request {
	r := httptest.NewRequest("POST", "http://example.test/increment", strings.NewReader(body))
	r.Header.Set("Origin", "http://example.test")
	r.Header.Set("Content-Type", kind)
	return r
}

func TestConfigureAndIncrementAction(t *testing.T) {
	config := nextgo.Config{Routes: []nextgo.Route{{Pattern: "/posts"}}}
	app.Configure(&config)
	if !config.PreserveLayouts || config.Cache != state.Cache || config.Actions == nil || config.Images == nil || config.Handlers["/robots.txt"] == nil || config.Handlers["/sitemap.xml"] == nil {
		t.Fatal("sample feature configuration incomplete")
	}
	policy := config.Routes[0].Cache
	if policy == nil || policy.TTL != 10*time.Second || len(policy.Tags) != 1 || policy.Tags[0] != "posts" {
		t.Fatalf("policy %#v", policy)
	}
	before := state.Count()
	w := httptest.NewRecorder()
	config.Actions.ServeHTTP(w, actionRequest(`{"amount":2}`, "application/json"))
	if w.Code != 200 {
		t.Fatal(w.Code, w.Body.String())
	}
	var response actions.Response
	if err := json.Unmarshal(w.Body.Bytes(), &response); err != nil {
		t.Fatal(err)
	}
	if int64(response.Data.(map[string]any)["count"].(float64)) != before+2 || response.Redirect != "/features" {
		t.Fatal(response)
	}
	w = httptest.NewRecorder()
	config.Actions.ServeHTTP(w, actionRequest("amount=1", "application/x-www-form-urlencoded"))
	if w.Code != 303 || w.Header().Get("Location") != "/features" || state.Count() != before+3 {
		t.Fatal(w.Code, w.Header(), state.Count())
	}
}

func TestInvalidAmountDoesNotMutate(t *testing.T) {
	config := nextgo.Config{}
	app.Configure(&config)
	for _, body := range []string{`{"amount":0}`, `{"amount":-1}`, `{"amount":11}`, `{"amount":"invalid"}`} {
		before := state.Count()
		w := httptest.NewRecorder()
		config.Actions.ServeHTTP(w, actionRequest(body, "application/json"))
		if w.Code != 422 && w.Code != 400 {
			t.Fatal(w.Code, w.Body.String())
		}
		if state.Count() != before {
			t.Fatal("invalid submission changed state")
		}
	}
}

func TestProtectedReviewActionChecksRoleBeforeMutation(t *testing.T) {
	reader, admin := strings.Repeat("r", 40), strings.Repeat("a", 40)
	t.Setenv("NEXTGO_DEMO_READER_TOKEN", reader)
	t.Setenv("NEXTGO_DEMO_ADMIN_TOKEN", admin)
	config := nextgo.Config{}
	app.Configure(&config)
	for _, tc := range []struct {
		token  string
		status int
	}{{"", 401}, {reader, 403}, {admin, 200}} {
		before := state.Reviews()
		r := actionRequest(`{}`, "application/json")
		r.URL.Path = "/review-report"
		r.RequestURI = "/review-report"
		if tc.token != "" {
			r.Header.Set("Authorization", "Bearer "+tc.token)
		}
		w := httptest.NewRecorder()
		config.Actions.ServeHTTP(w, r)
		if w.Code != tc.status {
			t.Fatalf("review status %d: %s", w.Code, w.Body.String())
		}
		want := before
		if tc.status == 200 {
			want++
		}
		if state.Reviews() != want {
			t.Fatal("denied mutation changed state", before, state.Reviews())
		}
		if tc.token != "" && strings.Contains(w.Body.String(), tc.token) {
			t.Fatal("credential reflected")
		}
	}
}
