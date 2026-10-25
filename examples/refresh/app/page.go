//go:build !js

package app

import (
	nextgo "github.com/Suhaibinator/Next.go"
	"github.com/Suhaibinator/Next.go/examples/refresh/components/refreshcounter"
	"github.com/Suhaibinator/Next.go/examples/refresh/serverstate"
	"github.com/Suhaibinator/Next.go/examples/refresh/shared"
	"github.com/Suhaibinator/Next.go/ui"
)

func Page(c *nextgo.Context) (nextgo.Page, error) {
	serverFlag, _ := nextgo.PublicValue(c, "refresh.server")
	note, _ := nextgo.PublicValue(c, "refresh.note")
	workURL := ""
	var diagnostics ui.Node = ui.Fragment()
	if serverFlag != "false" {
		workURL = nextgo.MustURL(c, "/api/work?delay=8000")
		diagnostics = ui.El("details", nil, ui.El("summary", nil, ui.Text("Public development diagnostics")), ui.El("p", nil, ui.Text("Native instance token: "), ui.El("output", ui.Attrs{"data-refresh-server-instance": ""}, ui.Text(serverstate.Instance()))), ui.El("a", ui.Attrs{"href": nextgo.MustURL(c, "/api/status"), "data-nextgo-reload": "true"}, ui.Text("Inspect the native instance and request count")), ui.El("p", nil, ui.Text("The opaque token is public fixture data. It lets a development test distinguish a retained native child from a full restart without exposing an operating PID.")))
	}
	return nextgo.Page{Title: "Explicit Go state refresh", Body: ui.El("article", nil,
		ui.El("h1", nil, ui.Text("Keep a public draft while Go changes.")), ui.El("p", ui.Attrs{"class": "lead", "data-refresh-intro": ""}, ui.Text(shared.Message)),
		ui.El("p", ui.Attrs{"data-refresh-public-note": ""}, ui.Text(note)),
		ui.El("p", nil, ui.Text("These independent lazy Go counters identify their instance and three public scalar cells. In development, an eligible component-only edit can bind a replacement module to the existing DOM. Other edits and busy work use a full reload.")),
		ui.El("div", ui.Attrs{"class": "counter-grid"}, refreshcounter.View(refreshcounter.Props{Instance: "primary", Label: "First counter", Initial: 3, Enabled: true, WorkURL: workURL}), refreshcounter.View(refreshcounter.Props{Instance: "secondary", Label: "Second counter", Initial: 10, Enabled: true, WorkURL: workURL})),
		diagnostics,
	)}, nil
}
