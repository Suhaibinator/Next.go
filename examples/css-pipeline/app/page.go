//go:build !js

package app

import (
	nextgo "github.com/Suhaibinator/Next.go"
	"github.com/Suhaibinator/Next.go/assets"
	"github.com/Suhaibinator/Next.go/ui"
)

func Page(c *nextgo.Context) (nextgo.Page, error) {
	return nextgo.Page{Title: "Optional CSS tools — Next.go", Resources: []assets.Resource{assets.Stylesheet("/assets/style.css")}, Body: ui.El("main", nil,
		ui.El("p", ui.Attrs{"class": "eyebrow"}, ui.Text("Go design tokens")),
		ui.El("h1", nil, ui.Text("Your stylesheet, built with Go.")),
		ui.El("p", nil, ui.Text("Edit styles/tokens.json or styles/main.css. The optional asset pipeline rebuilds the stylesheet alongside the application.")),
		ui.El("p", nil, ui.Text("Generated CSS lives in the private build generation; the source public directory stays untouched.")),
	)}, nil
}
