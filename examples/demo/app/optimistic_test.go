//go:build !js

package app_test

import (
	"context"
	"encoding/json"
	"net/http/httptest"
	"testing"

	nextgo "github.com/Suhaibinator/Next.go"
	"github.com/Suhaibinator/Next.go/examples/demo/app"
	"github.com/Suhaibinator/Next.go/examples/demo/state"
)

func TestOptimisticActionReturnsCanonicalDeltaAndRejectsBeforeMutation(t *testing.T) {
	config := nextgo.Config{}
	app.Configure(&config)
	for _, body := range []string{`{"amount":0}`, `{"amount":6}`, `{"amount":"invalid"}`, `{"amount":1,"extra":true}`} {
		before := state.OptimisticCount()
		r := actionRequest(body, "application/json")
		r.URL.Path = "/optimistic-increment/v1"
		w := httptest.NewRecorder()
		config.Actions.ServeHTTP(w, r)
		if w.Code < 400 || state.OptimisticCount() != before {
			t.Fatal(w.Code, w.Body.String(), state.OptimisticCount(), before)
		}
	}
	before := state.OptimisticCount()
	r := actionRequest(`{"amount":5}`, "application/json")
	r.URL.Path = "/optimistic-increment/v1"
	w := httptest.NewRecorder()
	config.Actions.ServeHTTP(w, r)
	var result struct {
		Data     struct{ Applied, Count int64 }
		Redirect string
	}
	if err := json.Unmarshal(w.Body.Bytes(), &result); err != nil || w.Code != 200 || result.Data.Applied != 2 || result.Data.Count != before+2 || result.Redirect != "/optimistic" {
		t.Fatal(err, w.Code, w.Body.String())
	}
	r = actionRequest("amount=1", "application/x-www-form-urlencoded")
	r.URL.Path = "/optimistic-increment/v1"
	w = httptest.NewRecorder()
	config.Actions.ServeHTTP(w, r)
	if w.Code != 303 || w.Header().Get("Location") != "/optimistic" || state.OptimisticCount() != before+3 {
		t.Fatal("progressive mutation failed", w.Code, w.Header(), state.OptimisticCount())
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	r = actionRequest(`{"amount":1}`, "application/json").WithContext(ctx)
	r.URL.Path = "/optimistic-increment/v1"
	w = httptest.NewRecorder()
	config.Actions.ServeHTTP(w, r)
	if w.Code < 400 || state.OptimisticCount() != before+3 {
		t.Fatal("canceled request mutated", w.Code, state.OptimisticCount())
	}
	r = actionRequest(`{"amount":1}`, "application/json")
	r.URL.Path = "/optimistic-increment/v1"
	r.Header.Set("Origin", "https://foreign.test")
	w = httptest.NewRecorder()
	config.Actions.ServeHTTP(w, r)
	if w.Code != 403 || state.OptimisticCount() != before+3 {
		t.Fatal("foreign mutation accepted", w.Code, state.OptimisticCount())
	}
}
