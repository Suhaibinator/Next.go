//go:build !js

package dashboard

import (
	nextgo "github.com/Suhaibinator/Next.go"
	"github.com/Suhaibinator/Next.go/examples/zones/tenantdata"
	"github.com/Suhaibinator/Next.go/ui"
)

func Page(c *nextgo.Context) (nextgo.Page, error) {
	if err := c.RequireServer(); err != nil {
		return nextgo.Page{}, err
	}
	record, err := tenantdata.Read(c, c.Request)
	if err != nil {
		return nextgo.Page{}, err
	}
	return nextgo.Page{Title: record.Tenant + " member dashboard", Body: ui.El("article", ui.Attrs{"data-tenant-dashboard": record.Tenant}, ui.El("h1", nil, ui.Text(record.Title)), ui.El("p", ui.Attrs{"data-tenant-authorized-subject": ""}, ui.Text(record.Subject)), ui.El("a", ui.Attrs{"href": nextgo.MustURL(c, "/api/record"), "data-nextgo-reload": "true"}, ui.Text("Read authorized record API")), ui.El("form", ui.Attrs{"method": "post", "action": nextgo.MustURL(c, "/_nextgo/actions/preview"), "data-tenant-action": ""}, ui.El("input", ui.Attrs{"name": "note", "value": "Public tenant preview", "maxlength": "80", "aria-label": "Public tenant note"}), ui.El("button", ui.Attrs{"type": "submit"}, ui.Text("Preview authorized tenant action"))))}, nil
}
