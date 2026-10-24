//go:build !js

package data_test

import (
	"context"
	"errors"
	"net/http/httptest"
	"testing"
	"time"

	nextgo "github.com/Suhaibinator/Next.go"
	"github.com/Suhaibinator/Next.go/examples/demo/data"
)

func TestPreviewObservesRequestDeadline(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 25*time.Millisecond)
	defer cancel()
	request := httptest.NewRequest("GET", "/posts?delay=2s", nil).WithContext(ctx)
	c := &nextgo.Context{Context: request.Context(), Request: request, Query: request.URL.Query()}

	started := time.Now()
	err := data.Preview(c)
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("Preview error = %v, want request deadline exceeded", err)
	}
	// Leave ample scheduling slack while distinguishing cancellation from the
	// requested two-second load completing normally.
	if elapsed := time.Since(started); elapsed >= time.Second {
		t.Fatalf("Preview took %s despite a 25ms request deadline", elapsed)
	}
}

func TestFindPreservesCancellationAndNotFound(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	for _, slug := range []string{"html-first", "missing-post"} {
		if _, err := data.Find(ctx, slug); !errors.Is(err, context.Canceled) {
			t.Errorf("Find canceled request for %q: error = %v, want context.Canceled", slug, err)
		}
	}
	if _, err := data.Find(context.Background(), "missing-post"); !errors.Is(err, nextgo.ErrNotFound) {
		t.Errorf("Find missing post: error = %v, want nextgo.ErrNotFound", err)
	}
}
