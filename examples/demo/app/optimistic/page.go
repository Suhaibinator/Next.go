//go:build !js

package optimistic

import (
	nextgo "github.com/Suhaibinator/Next.go"
	view "github.com/Suhaibinator/Next.go/examples/demo/components/optimistic"
	"github.com/Suhaibinator/Next.go/examples/demo/mutations"
	"github.com/Suhaibinator/Next.go/examples/demo/state"
	"github.com/Suhaibinator/Next.go/ui"
)

func Page(c *nextgo.Context) (nextgo.Page, error) {
	if err := c.Err(); err != nil {
		return nextgo.Page{}, err
	}
	return nextgo.Page{Title: "Optimistic updates — Next.go", Body: ui.Fragment(
		ui.El("section", ui.Attrs{"class": "prose"}, ui.El("h1", nil, ui.Text("Optimistic Go updates")), ui.El("p", nil, ui.Text("The HTML contains the confirmed server count. Go adds tentative changes after hydration and reconciles each server response."))),
		view.View(view.Props{Initial: state.OptimisticCount()}),
		ui.El("section", ui.Attrs{"class": "counter"}, ui.El("h2", nil, ui.Text("Progressive HTML form")),
			ui.El("p", nil, ui.Text("This form submits a change and returns fresh HTML, including when WebAssembly is unavailable.")),
			ui.El("form", ui.Attrs{"method": "post", "action": nextgo.MustURL(c, mutations.Increment.Path())},
				ui.El("input", ui.Attrs{"type": "hidden", "name": "amount", "value": "1"}),
				ui.El("button", ui.Attrs{"type": "submit"}, ui.Text("Submit one on the server")))),
		ui.El("p", nil, ui.El("a", ui.Attrs{"href": nextgo.MustURL(c, "/features")}, ui.Text("Back to features"))))}, nil
}
