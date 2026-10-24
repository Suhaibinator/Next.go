//go:build !js

package param_id

import (
	nextgo "github.com/Suhaibinator/Next.go"
	"github.com/Suhaibinator/Next.go/examples/demo/components/counter"
	"github.com/Suhaibinator/Next.go/examples/demo/internal/photos"
	"github.com/Suhaibinator/Next.go/ui"
)

func Page(c *nextgo.Context) (nextgo.Page, error) {
	p, err := photos.Load(c)
	if err != nil {
		return nextgo.Page{}, err
	}
	return nextgo.Page{Title: p.Title, Body: ui.Fragment(ui.El("h1", nil, ui.Text(p.Title)), ui.El("p", nil, ui.Text("Standalone photo page")), photos.View(p), counter.View(counter.Props{Label: "Standalone photo counter", Initial: 30}), ui.El("a", ui.Attrs{"href": nextgo.MustURL(c, "/gallery")}, ui.Text("Return to gallery")))}, nil
}
