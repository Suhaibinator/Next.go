//go:build !js

package app

import (
	nextgo "github.com/Suhaibinator/Next.go"
	"github.com/Suhaibinator/Next.go/ui"
)

// This ordinary exported page is deliberately outside the shell's ownership.
func Page(c *nextgo.Context) (nextgo.Page, error) {
	return nextgo.Page{
		Title:       "Public static shell example",
		Description: "An ordinary public page linking into an explicit Go client shell.",
		Body: ui.El("main", ui.Attrs{"data-shell-home": ""},
			ui.El("h1", nil, ui.Text("Public Go workspace")),
			ui.El("p", nil, ui.Text("This ordinary page has its own exported HTML and navigation JSON. The workspace reuses one public build-time shell at its declared deep URLs.")),
			ui.El("a", ui.Attrs{"href": nextgo.MustURL(c, "/workspace/notes/caf%C3%A9?tag=one&tag=two#details"), "data-shell-enter": "", "data-nextgo-prefetch": "false"}, ui.Text("Open an unexported workspace URL")),
		),
	}, nil
}
