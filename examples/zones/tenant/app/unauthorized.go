//go:build !js

package app

import (
	nextgo "github.com/Suhaibinator/Next.go"
	"github.com/Suhaibinator/Next.go/ui"
)

func Unauthorized(c *nextgo.Context) (nextgo.Page, error) {
	return nextgo.Page{Title: "Synthetic identity required", Body: ui.El("article", ui.Attrs{"data-tenant-denial": "401"}, ui.El("h1", nil, ui.Text("Select a synthetic identity first")), ui.El("a", ui.Attrs{"href": nextgo.MustURL(c, "/")}, ui.Text("Return to public demo issuer")))}, nil
}
