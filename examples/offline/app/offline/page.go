//go:build !js

package fallback

import (
	nextgo "github.com/Suhaibinator/Next.go"
	"github.com/Suhaibinator/Next.go/ui"
)

func Page(*nextgo.Context) (nextgo.Page, error) {
	return nextgo.Page{
		Title:       "Offline reading fallback — Next.go",
		Description: "This is a saved anonymous public fallback. Reconnect for current information; no account or action outcome is confirmed.",
		Body: ui.El("article", ui.Attrs{"data-offline-page": "fallback"},
			ui.El("h1", nil, ui.Text("This page is outside the saved shelf.")),
			ui.El("p", ui.Attrs{"class": "lead"}, ui.Text("You can still open the saved overview and reading guide from the links above.")),
			ui.El("p", nil, ui.Text("The saved fallback contains only this page's approved title and description as escaped text. Its application body, layout, styles, and Go runtime are intentionally omitted.")),
		),
	}, nil
}
