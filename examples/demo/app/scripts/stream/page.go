//go:build !js

package stream

import (
	"context"
	nextgo "github.com/Suhaibinator/Next.go"
	"github.com/Suhaibinator/Next.go/examples/demo/components/scriptwidget"
	"github.com/Suhaibinator/Next.go/scripts"
	"github.com/Suhaibinator/Next.go/ui"
	"time"
)

func Page(*nextgo.Context) (nextgo.Page, error) {
	return nextgo.Page{Title: "Streamed scripts — Next.go", Stream: true, Body: ui.El("article", nil, ui.El("h1", nil, ui.Text("Stream a Go widget")), ui.Defer("script-widget", ui.Text("Waiting for widget data"), func(ctx context.Context) (ui.Node, error) {
		timer := time.NewTimer(300 * time.Millisecond)
		defer timer.Stop()
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-timer.C:
			return scriptwidget.View(scriptwidget.Props{Label: "Streamed script widget", Dependency: scripts.Script{ID: "demo-vendor", Src: "/vendor-demo.js", Asset: true}, Global: "NextgoDemoVendor"}), nil
		}
	}))}, nil
}
