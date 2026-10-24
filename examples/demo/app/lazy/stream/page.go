//go:build !js

package stream

import (
	"context"
	nextgo "github.com/Suhaibinator/Next.go"
	"github.com/Suhaibinator/Next.go/examples/demo/components/lazycounter"
	"github.com/Suhaibinator/Next.go/ui"
	"time"
)

func Page(c *nextgo.Context) (nextgo.Page, error) {
	return nextgo.Page{Title: "Streamed lazy module — Next.go", Stream: true, Body: ui.El("article", nil, ui.El("h1", nil, ui.Text("Stream a lazy Go component")), ui.Defer("lazy-island", ui.Text("Waiting for a lazy island…"), func(ctx context.Context) (ui.Node, error) {
		timer := time.NewTimer(500 * time.Millisecond)
		defer timer.Stop()
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-timer.C:
			return lazycounter.View(lazycounter.Props{Label: "Streamed lazy counter", Initial: 7, Target: nextgo.MustURL(c, "/about")}), nil
		}
	}))}, nil
}
