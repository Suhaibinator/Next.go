//go:build !js

package stream

import (
	"context"
	nextgo "github.com/Suhaibinator/Next.go"
	"github.com/Suhaibinator/Next.go/assets"
	"github.com/Suhaibinator/Next.go/ui"
	"time"
)

func Page(*nextgo.Context) (nextgo.Page, error) {
	return nextgo.Page{Resources: []assets.Resource{assets.Stylesheet("/assets/link-status.css")}, Title: "Deferred link page — Next.go", Stream: true, Body: ui.Fragment(
		ui.El("h1", nil, ui.Text("Deferred link page")),
		ui.Defer("link-feedback-content", ui.El("p", ui.Attrs{"data-link-content-pending": ""}, ui.Text("The page is open. Its content is still loading…")), func(ctx context.Context) (ui.Node, error) {
			timer := time.NewTimer(500 * time.Millisecond)
			defer timer.Stop()
			select {
			case <-ctx.Done():
				return nil, ctx.Err()
			case <-timer.C:
			}
			return ui.El("p", ui.Attrs{"data-link-content-ready": ""}, ui.Text("Deferred content is ready.")), nil
		}))}, nil
}
