package renderowner

import (
	"context"
	"testing"
	"time"
)

type preservedContextKey struct{}

func TestWithoutRecorderShadowsOnlyTheChildAndPreservesContext(t *testing.T) {
	parent, cancel := context.WithTimeout(context.WithValue(context.Background(), preservedContextKey{}, "request-value"), time.Minute)
	defer cancel()
	recorder := New()
	ctx := With(recorder.Context(parent), Semantic("page", "/sibling"))
	hidden := WithoutRecorder(ctx)
	if RecorderFrom(hidden) != nil || RecorderFrom(ctx) != recorder || recorder.disabled {
		t.Fatal("child shadow disabled or exposed the shared recorder")
	}
	if hidden.Value(preservedContextKey{}) != "request-value" {
		t.Fatal("request value lost")
	}
	deadline, ok := parent.Deadline()
	childDeadline, childOK := hidden.Deadline()
	if !ok || !childOK || !deadline.Equal(childDeadline) {
		t.Fatal("deadline lost")
	}
	for i := 0; i < 2; i++ {
		inner := With(hidden, Frame{Kind: "keyed", ID: "row", Tag: "p"})
		ticket, err := Begin(inner, "nested", "panel", "v1", "null")
		if err != nil || ticket != nil {
			t.Fatal("fallback ancestry was recorded", ticket, err)
		}
	}
	if len(recorder.owners) != 1 {
		t.Fatal("fallback key paths entered owner graph", recorder.owners)
	}
	for index, key := range []string{"A", "B"} {
		ticket, err := Begin(ctx, key, "panel", "v1", "null")
		if err != nil || ticket == nil || ticket.Node() != index+1 {
			t.Fatal("outer sibling ownership lost", key, ticket, err)
		}
	}
	cancel()
	if hidden.Err() != context.Canceled || ctx.Err() != context.Canceled {
		t.Fatal("cancellation was hidden")
	}
}
