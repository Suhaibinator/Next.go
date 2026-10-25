//go:build js && wasm

package mutation

import (
	"context"
	"errors"
	"strings"
	"syscall/js"
	"testing"

	"github.com/Suhaibinator/Next.go/client"
)

func TestCombinedCallRejectsInvalidInputBeforeAnyMutationDispatch(t *testing.T) {
	requests := 0
	callEnvironment(t, func([]js.Value) any { requests++; return callResponse(`{"data":{"count":1}}`, nil) })
	c := Must[inputJS, outputJS]("add", "v1")
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := c.CallAndRefresh(ctx, inputJS{1}); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	if _, err := c.CallAndRefresh(nil, inputJS{1}); err == nil {
		t.Fatal("nil context accepted")
	}
	if _, err := c.CallAndRefresh(context.Background(), inputJS{1}, CallOptions{MaxInputBytes: 1}); err == nil || !strings.Contains(err.Error(), "input") {
		t.Fatal("input limit not enforced", err)
	}
	if _, err := c.CallAndRefresh(context.Background(), inputJS{1}, CallOptions{MaxResponseBytes: -1}); err == nil || !strings.Contains(err.Error(), "limits") {
		t.Fatal("invalid result bound accepted", err)
	}
	var form *client.Multipart
	if _, err := c.CallMultipartAndRefresh(ctx, form); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	if _, err := c.CallMultipartAndRefresh(context.Background(), form); err == nil || !strings.Contains(err.Error(), "multipart") {
		t.Fatal("missing multipart body accepted", err)
	}
	if requests != 0 {
		t.Fatal("invalid input dispatched a mutation", requests)
	}
}
