//go:build !js

package work

import (
	"context"
	"net/http/httptest"
	"testing"
)

func TestBoundedWorkAndCancellation(t *testing.T) {
	w := httptest.NewRecorder()
	Handle(w, httptest.NewRequest("GET", "http://example.test/api/work?delay=0", nil))
	if w.Code != 200 || w.Body.String() != "{\"complete\":true}\n" {
		t.Fatal(w.Code, w.Body.String())
	}
	for _, query := range []string{"delay=-1", "delay=15001", "delay=0&delay=1", "delay=0&other=1", ""} {
		w = httptest.NewRecorder()
		Handle(w, httptest.NewRequest("GET", "http://example.test/api/work?"+query, nil))
		if w.Code != 400 {
			t.Fatal(query, w.Code)
		}
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	w = httptest.NewRecorder()
	Handle(w, httptest.NewRequest("GET", "http://example.test/api/work?delay=8000", nil).WithContext(ctx))
	if w.Body.Len() != 0 {
		t.Fatal("canceled work published", w.Body.String())
	}
}
