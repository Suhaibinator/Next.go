//go:build !js

package stream

import (
	"context"
	"fmt"
	nextgo "github.com/Suhaibinator/Next.go"
	"github.com/Suhaibinator/Next.go/examples/demo/components/counter"
	"github.com/Suhaibinator/Next.go/ui"
	"os"
	"time"
)

func Page(c *nextgo.Context) (nextgo.Page, error) {
	if mode := c.Query.Get("mode"); mode == "nested" || mode == "error" {
		return nestedPage(mode), nil
	}

	return nextgo.Page{Title: "Streaming — Next.go", Stream: true, Body: ui.El("section", ui.Attrs{"class": "counter"},
		ui.El("h1", nil, ui.Text("Streaming server rendering")), ui.El("p", nil, ui.Text("The initial HTML arrives immediately. A deferred server loader completes after 500 milliseconds.")),
		ui.Defer("demo-content", ui.El("p", ui.Attrs{"data-stream-loading": "", "role": "status"}, ui.Text("Loading deferred server content…")), func(ctx context.Context) (ui.Node, error) {
			// Explicitly enabled only by the admission qualification fixture.
			if os.Getenv("NEXTGO_DEMO_STREAM_HOLD") == "true" && c.Query.Get("hold") == "1" {
				<-ctx.Done()
				return nil, ctx.Err()
			}
			timer := time.NewTimer(500 * time.Millisecond)
			defer timer.Stop()
			select {
			case <-ctx.Done():
				return nil, ctx.Err()
			case <-c.Done():
				return nil, c.Err()
			case <-timer.C:
			}
			return ui.El("div", ui.Attrs{"data-stream-loaded": ""}, ui.El("h2", nil, ui.Text("Deferred content is ready")), ui.El("p", nil, ui.Text("This content was rendered by a Go loader after the initial document was flushed. Its interactive island hydrates when the streamed chunk arrives.")), counter.View(counter.Props{Label: "Streamed counter", Initial: 7})), nil
		}))}, nil
}

func delayed(ctx context.Context, delay time.Duration, load func() (ui.Node, error)) (ui.Node, error) {
	timer := time.NewTimer(delay)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return nil, ctx.Err()
	case <-timer.C:
		return load()
	}
}
func nestedPage(mode string) nextgo.Page {
	fast := ui.Defer("fast-boundary", ui.El("p", ui.Attrs{"data-fast-pending": ""}, ui.Text("Waiting for the first result…")), func(ctx context.Context) (ui.Node, error) {
		return delayed(ctx, 150*time.Millisecond, func() (ui.Node, error) {
			inner := ui.Defer("inner-boundary", ui.El("p", ui.Attrs{"data-inner-pending": ""}, ui.Text("Waiting for nested data…")), func(ctx context.Context) (ui.Node, error) {
				return delayed(ctx, 600*time.Millisecond, func() (ui.Node, error) {
					return ui.El("div", ui.Attrs{"data-inner-loaded": ""}, counter.View(counter.Props{Label: "Nested streamed counter", Initial: 3})), nil
				})
			})
			return ui.El("div", ui.Attrs{"data-fast-loaded": ""}, counter.View(counter.Props{Label: "Fast streamed counter", Initial: 2}), inner), nil
		})
	})
	slow := ui.Defer("slow-boundary", ui.El("p", ui.Attrs{"data-slow-pending": ""}, ui.Text("Waiting for an independent result…")), func(ctx context.Context) (ui.Node, error) {
		return delayed(ctx, 1500*time.Millisecond, func() (ui.Node, error) {
			if mode == "error" {
				return nil, fmt.Errorf("private deferred loader diagnostic")
			}
			return ui.El("div", ui.Attrs{"data-slow-loaded": ""}, counter.View(counter.Props{Label: "Slow streamed counter", Initial: 4})), nil
		})
	})
	return nextgo.Page{Title: "Nested streaming — Next.go", Stream: true, Body: ui.El("section", nil, ui.El("h1", nil, ui.Text("Independent streamed boundaries")), fast, slow)}
}
