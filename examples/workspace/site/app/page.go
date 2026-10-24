//go:build !js

package app

import (
	nextgo "github.com/Suhaibinator/Next.go"
	"github.com/Suhaibinator/Next.go/ui"
	"workspace.example/shared"
	"workspace.example/site/components/counter"
)

func Page(c *nextgo.Context) (nextgo.Page, error) {
	return nextgo.Page{Title: "Go workspace — Next.go", Body: ui.El("article", nil, ui.El("h1", nil, ui.Text("Go modules, one application")), ui.El("p", ui.Attrs{"data-workspace-note": ""}, ui.Text(shared.Note)), counter.View(), ui.El("a", ui.Attrs{"href": nextgo.MustURL(c, "/about")}, ui.Text("About this workspace")))}, nil
}
