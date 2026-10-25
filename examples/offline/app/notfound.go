//go:build !js

package app

import (
	nextgo "github.com/Suhaibinator/Next.go"
	"github.com/Suhaibinator/Next.go/ui"
)

func NotFound(*nextgo.Context) (nextgo.Page, error) {
	return nextgo.Page{Title: "Page not found — Next.go", Body: ui.El("article", nil,
		ui.El("h1", nil, ui.Text("This page does not exist on the server.")),
		ui.El("p", nil, ui.Text("Use the reading links to return to the public shelf.")))}, nil
}
