//go:build !js

package guide

import (
	nextgo "github.com/Suhaibinator/Next.go"
	"github.com/Suhaibinator/Next.go/ui"
)

func Page(*nextgo.Context) (nextgo.Page, error) {
	return nextgo.Page{
		Title:       "Reading guide — Next.go",
		Description: "How to read and update the explicitly saved public edition.",
		Body: ui.El("article", ui.Attrs{"data-offline-page": "guide"},
			ui.El("h1", nil, ui.Text("Read the saved edition deliberately.")),
			ui.El("p", ui.Attrs{"class": "lead"}, ui.Text("A saved edition is a public snapshot, not evidence of your current account, permissions, or the newest server state.")),
			ui.El("ol", nil,
				ui.El("li", nil, ui.Text("While online, choose Enable offline and wait for the anonymous public edition to finish installing.")),
				ui.El("li", nil, ui.Text("Reload once if the browser has not yet placed this tab under worker control. Then disconnect and use the reading links.")),
				ui.El("li", nil, ui.Text("Reconnect and check for an update. A waiting edition activates after all tabs in this application scope close; open a new tab afterward."))),
			ui.El("p", nil, ui.Text("The live health endpoint remains network-only. An unavailable document can use the approved inert fallback; API traffic and requests with queries are outside the saved shelf.")),
		),
	}, nil
}
