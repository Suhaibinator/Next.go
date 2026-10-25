//go:build !js

package app

import (
	nextgo "github.com/Suhaibinator/Next.go"
	"github.com/Suhaibinator/Next.go/examples/form-validation/components/validationform"
	"github.com/Suhaibinator/Next.go/examples/form-validation/model"
	"github.com/Suhaibinator/Next.go/ui"
)

func Page(c *nextgo.Context) (nextgo.Page, error) {
	if err := c.RequireServer(); err != nil {
		return nextgo.Page{}, err
	}
	return nextgo.Page{Title: "Public form validation — Next.go", Body: ui.El("article", nil,
		ui.El("h1", nil, ui.Text("Choices with a clear state.")),
		ui.El("p", ui.Attrs{"class": "lead"}, ui.Text("Two independent Go forms adopt edits made before WebAssembly loads. Public field checks run after a short pause; newer edits, reset and leaving the page invalidate older results.")),
		ui.El("p", nil, ui.Text("Try reserved for a field rejection, offline for an unavailable check, or slow-reserved to see a delayed rejection. Retry checks explicitly. These rules are synthetic and no record is saved.")),
		ui.El("div", ui.Attrs{"class": "form-grid"}, validationform.View(validationform.Props{Instance: "primary", Values: model.Initial(true), Action: nextgo.MustURL(c, "/api/preview"), ValidateURL: nextgo.MustURL(c, "/api/validate")}), validationform.View(validationform.Props{Instance: "secondary", Values: model.Initial(false), Action: nextgo.MustURL(c, "/api/preview"), ValidateURL: nextgo.MustURL(c, "/api/validate")})),
	)}, nil
}
