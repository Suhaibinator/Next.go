//go:build !js

package stream

import (
	"context"
	nextgo "github.com/Suhaibinator/Next.go"
	"github.com/Suhaibinator/Next.go/ui"
	"independent.example/alpha/internal/state"
	"time"
)

func Page(*nextgo.Context) (nextgo.Page, error) {
	return nextgo.Page{Title: "alpha streamed note", Stream: true, Body: ui.El("article", ui.Attrs{"data-independent-stream": "alpha"}, ui.El("h1", nil, ui.Text("A streamed alpha field note")), ui.Defer("release-note", ui.El("p", ui.Attrs{"data-independent-loading": "alpha", "role": "status"}, ui.Text("Loading a public native note…")), func(ctx context.Context) (ui.Node, error) {
		timer := time.NewTimer(150 * time.Millisecond)
		defer timer.Stop()
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-timer.C:
		}
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		return ui.El("p", ui.Attrs{"data-independent-loaded": "alpha"}, ui.Text("Public native note: "+state.NativeVersion)), nil
	}))}, nil
}
