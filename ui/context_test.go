package ui

import (
	"context"
	"io"
	"strings"
	"testing"
)

type renderValueKey struct{}

func TestRenderToPropagatesContextToCustomNodesAndDeferredWork(t *testing.T) {
	ctx := context.WithValue(context.Background(), renderValueKey{}, "selected request")
	var output strings.Builder
	calls := 0
	custom := nodeFunc(func(w io.Writer) error {
		if RenderContext(w).Value(renderValueKey{}) != "selected request" {
			t.Fatal("lost custom-node context")
		}
		return Defer("deferred", nil, func(ctx context.Context) (Node, error) {
			calls++
			return Text(ctx.Value(renderValueKey{}).(string)), nil
		}).Render(w)
	})
	if err := RenderTo(ctx, &output, custom); err != nil || output.String() != "selected request" || calls != 1 {
		t.Fatal(output.String(), calls, err)
	}
	if RenderContext(&output) == nil {
		t.Fatal("ordinary writer has no fallback context")
	}
	canceled, cancel := context.WithCancel(ctx)
	cancel()
	if err := RenderTo(canceled, &output, Text("must not write")); err != context.Canceled {
		t.Fatal(err)
	}
	if err := RenderTo(nil, &output, Text("invalid")); err == nil {
		t.Fatal("nil context accepted")
	}
	if err := RenderTo(ctx, nil, Text("invalid")); err == nil {
		t.Fatal("nil writer accepted")
	}
}
