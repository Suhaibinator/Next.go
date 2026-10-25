//go:build !js

package app

import (
	nextgo "github.com/Suhaibinator/Next.go"
	"github.com/Suhaibinator/Next.go/ui"
)

func Page(*nextgo.Context) (nextgo.Page, error) {
	return nextgo.Page{
		Title:       "Saved public pages — Next.go",
		Description: "An explicit, bounded saved-anonymous-public reading example in Go.",
		Body: ui.El("article", ui.Attrs{"data-offline-page": "overview"},
			ui.El("h1", nil, ui.Text("A small public reading shelf.")),
			ui.El("p", ui.Attrs{"class": "lead"}, ui.Text("Read useful server HTML first, then explicitly save this anonymous public edition for offline reading.")),
			ui.El("p", nil, ui.Text("This example saves the overview, reading guide, approved offline fallback, and their verified local browser assets. It does not save a signed-in view, API response, navigation JSON, or mutation queue.")),
			ui.El("p", nil, ui.Text("The counter below is a separate lazy Go component. It works after its WASM loads, including from a complete saved edition. Its local state resets on each document navigation.")),
		),
	}, nil
}
