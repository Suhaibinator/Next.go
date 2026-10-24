//go:build !js

package chart

import (
	nextgo "github.com/Suhaibinator/Next.go"
	"github.com/Suhaibinator/Next.go/examples/demo/components/counter"
	"github.com/Suhaibinator/Next.go/ui"
)

func Page(c *nextgo.Context) (nextgo.Page, error) {
	if c.Request.Header.Get("X-Demo-Primary-Deny") == "yes" {
		return nextgo.Page{}, nextgo.ErrForbidden
	}
	initial := 12
	if c.Request.Header.Get("X-Demo-Primary-Version") == "updated" {
		initial = 20
	}
	return nextgo.Page{Title: "Chart board — Next.go", Body: ui.Fragment(ui.El("p", nil, ui.Text("Primary chart page")), counter.View(counter.Props{Initial: initial, Label: "Primary chart"}))}, nil
}
