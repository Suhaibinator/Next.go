//go:build !js

package app

import (
	nextgo "github.com/Suhaibinator/Next.go"
	"github.com/Suhaibinator/Next.go/examples/zones/identity"
	"github.com/Suhaibinator/Next.go/ui"
)

func Page(c *nextgo.Context) (nextgo.Page, error) {
	if err := c.RequireServer(); err != nil {
		return nextgo.Page{}, err
	}
	info := nextgo.CurrentZone(c)
	subject, _ := identity.Subject(c.Request)
	return nextgo.Page{Title: info.Tenant + " tenant — zones", Body: ui.El("article", ui.Attrs{"data-tenant-home": info.Tenant}, ui.El("h1", nil, ui.Text(info.Tenant+" tenant fixture")), ui.El("p", nil, ui.Text("Zone context selects a synthetic repository. It does not authenticate a visitor. The protected page, API and action each verify a signed demo session and membership again.")), ui.El("p", ui.Attrs{"data-tenant-subject": ""}, ui.Text("Current synthetic subject: "+subject)), ui.El("p", nil, ui.Text("Anyone can select either demo identity below. This public issuer is not real account authentication and these records are not private.")), ui.El("form", ui.Attrs{"method": "post", "action": nextgo.MustURL(c, "/_nextgo/actions/assume"), "data-tenant-assume": ""}, ui.El("button", ui.Attrs{"type": "submit", "name": "subject", "value": "alice"}, ui.Text("Assume Alice (alpha member)")), ui.El("button", ui.Attrs{"type": "submit", "name": "subject", "value": "bob"}, ui.Text("Assume Bob (beta member)"))), ui.El("a", ui.Attrs{"href": nextgo.MustURL(c, "/dashboard")}, ui.Text("Open member dashboard")))}, nil
}
