//go:build !js

package lifecycle

import (
	"context"
	"log"

	nextgo "github.com/Suhaibinator/Next.go"
	"github.com/Suhaibinator/Next.go/ui"
)

func Page(c *nextgo.Context) (nextgo.Page, error) {
	// Snapshot inputs before returning; completion work never reads a live request.
	path := c.Request.URL.Path
	if err := nextgo.After(c.Context, func(work context.Context) error {
		if err := work.Err(); err != nil {
			return err
		}
		log.Printf("demo completion callback: rendered %s", path)
		return nil
	}); err != nil {
		return nextgo.Page{}, err
	}
	return nextgo.Page{Title: "Request lifecycle — Next.go", Body: ui.El("section", ui.Attrs{"class": "counter"},
		ui.El("h1", nil, ui.Text("Request completion work")),
		ui.El("p", nil, ui.Text("This page schedules a bounded Go callback that logs its path after request handling. Check the server terminal for the completion message.")),
		ui.El("p", nil, ui.Text("Native servers start work after their handler finishes. Lambda waits until the Runtime API accepts the response. Work has a timeout and cooperative cancellation, and shutdown drains it.")),
		ui.El("p", nil, ui.Text("The hook is process-local and best effort. Required business operations belong in a durable queue.")),
		ui.El("a", ui.Attrs{"href": nextgo.MustURL(c, "/features")}, ui.Text("Back to framework features")),
	)}, nil
}
