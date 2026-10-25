//go:build !js

package app

import (
	nextgo "github.com/Suhaibinator/Next.go"
	"github.com/Suhaibinator/Next.go/ui"
)

func Forbidden(c *nextgo.Context) (nextgo.Page, error) {
	return nextgo.Page{Title: "Tenant membership required", Body: ui.El("article", ui.Attrs{"data-tenant-denial": "403"}, ui.El("h1", nil, ui.Text("This subject is not a tenant member")), ui.El("p", nil, ui.Text("Routing selected a tenant; the separate membership check refused this subject.")), ui.El("a", ui.Attrs{"href": nextgo.MustURL(c, "/")}, ui.Text("Return to public demo issuer")))}, nil
}
