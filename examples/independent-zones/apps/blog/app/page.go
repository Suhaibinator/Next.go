//go:build !js

package app

import (
	nextgo "github.com/Suhaibinator/Next.go"
	"github.com/Suhaibinator/Next.go/assets"
	"github.com/Suhaibinator/Next.go/ui"
	"independent.example/blog/components/counter"
	"independent.example/blog/internal/state"
	"independent.example/blog/mutations"
)

func Page(c *nextgo.Context) (nextgo.Page, error) {
	return nextgo.Page{Title: "blog independent release", Resources: []assets.Resource{assets.Stylesheet("/assets/card.css")}, Body: ui.El("article", ui.Attrs{"data-independent-zone": "blog", "class": "independent-card"},
		ui.El("h1", nil, ui.Text("The blog application")),
		ui.El("p", ui.Attrs{"class": "lead"}, ui.Text("This Go module owns its server, browser modules and frozen public assets. Its peer is built and deployed independently.")),
		ui.El("p", nil, ui.Text("Native implementation: "), ui.El("output", ui.Attrs{"data-independent-native": ""}, ui.Text(state.NativeVersion))),
		ui.El("div", ui.Attrs{"class": "counter-grid"}, counter.View(counter.Props{Instance: "primary", Initial: 3}), counter.View(counter.Props{Instance: "secondary", Initial: 10})),
		ui.El("nav", ui.Attrs{"aria-label": "Public blog notes"}, ui.El("a", ui.Attrs{"href": nextgo.MustURL(c, "/posts/welcome?source=home")}, ui.Text("Read the welcome note")), ui.El("a", ui.Attrs{"href": nextgo.MustURL(c, "/posts/ownership?source=home")}, ui.Text("Read the ownership note"))),
		ui.El("form", ui.Attrs{"method": "post", "action": nextgo.MustURL(c, mutations.Preview.Path()), "data-independent-form": ""}, ui.El("label", nil, ui.Text("Public preview amount "), ui.El("input", ui.Attrs{"name": "amount", "type": "number", "value": "1", "min": "1", "max": "5", "step": "1"})), ui.El("button", ui.Attrs{"type": "submit"}, ui.Text("Submit native blog preview"))),
		ui.El("p", ui.Attrs{"data-independent-receipt": ""}, ui.Text("Preview flag: "+c.Query.Get("preview"))),
		ui.El("p", nil, ui.El("a", ui.Attrs{"href": nextgo.MustURL(c, "/api/state"), "data-nextgo-reload": "true"}, ui.Text("Inspect public blog process state"))),
		ui.El("a", ui.Attrs{"href": nextgo.MustAssetURL(c, "/release.txt"), "data-independent-asset": "", "data-nextgo-reload": "true"}, ui.Text("Inspect the frozen release marker")),
	)}, nil
}
