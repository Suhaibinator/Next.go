//go:build !js

package stream

import (
	"context"
	nextgo "github.com/Suhaibinator/Next.go"
	"github.com/Suhaibinator/Next.go/ui"
	"independent.example/blog/internal/state"
	"time"
)

func Page(*nextgo.Context) (nextgo.Page, error) {
	return nextgo.Page{Title: "blog streamed note", Stream: true, Body: ui.El("article", ui.Attrs{"data-independent-stream": "blog"}, ui.El("h1", nil, ui.Text("A streamed blog field note")), ui.Defer("release-note", ui.El("p", ui.Attrs{"data-independent-loading": "blog", "role": "status"}, ui.Text("Loading a public native note…")), func(ctx context.Context) (ui.Node, error) {
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
		return ui.El("p", ui.Attrs{"data-independent-loaded": "blog"}, ui.Text("Public native note: "+state.NativeVersion)), nil
	}))}, nil
}
