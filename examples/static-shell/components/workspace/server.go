//go:build !js

package workspace

import (
	"context"

	nextgo "github.com/Suhaibinator/Next.go"
	"github.com/Suhaibinator/Next.go/ui"
)

func View(ctx context.Context) ui.Node {
	return ui.ClientIsland("workspace", Props{Label: "Public workspace", Initial: 0}, Fallback(ctx))
}

// Fallback remains useful without WebAssembly. Only deployment URL placement
// comes from ctx; all visible text and props are fixed public build content.
func Fallback(ctx context.Context) ui.Node {
	return ui.El("main", ui.Attrs{"data-shell-fallback": ""},
		ui.El("h1", nil, ui.Text("Public workspace")),
		ui.El("p", nil, ui.Text("This public shell is ready to read. Go WebAssembly will show the actual browser route and a local counter when it loads.")),
		ui.El("label", nil, ui.Text("Local fallback draft "),
			ui.El("input", ui.Attrs{"type": "text", "value": "A public draft", "maxlength": "80", "autocomplete": "off", "data-shell-fallback-input": ""})),
		ui.El("nav", ui.Attrs{"aria-label": "Public workspace links"},
			ui.El("a", ui.Attrs{"href": nextgo.MustURL(ctx, "/workspace/notes/caf%C3%A9?tag=one&tag=two#details")}, ui.Text("Café note")),
			ui.Text(" · "),
			ui.El("a", ui.Attrs{"href": nextgo.MustURL(ctx, "/workspace/boards/team?sort=recent")}, ui.Text("Team board")),
			ui.Text(" · "),
			ui.El("a", ui.Attrs{"href": nextgo.MustURL(ctx, "/")}, ui.Text("Home"))),
		ui.El("p", nil, ui.Text("Owned deep document URLs return this generic HTTP 200 shell. A browser missing-screen cannot change that HTTP status.")),
	)
}
