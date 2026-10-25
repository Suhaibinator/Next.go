//go:build !js

package cookiesessionpage

import (
	"fmt"
	"strconv"

	nextgo "github.com/Suhaibinator/Next.go"
	"github.com/Suhaibinator/Next.go/examples/demo/access"
	"github.com/Suhaibinator/Next.go/examples/demo/actions/session/login"
	"github.com/Suhaibinator/Next.go/examples/demo/actions/session/logout"
	"github.com/Suhaibinator/Next.go/examples/demo/components/sessionpanel"
	"github.com/Suhaibinator/Next.go/examples/demo/cookiesession"
	"github.com/Suhaibinator/Next.go/ui"
)

func Page(c *nextgo.Context) (nextgo.Page, error) {
	if err := c.SetHeader("Cache-Control", "private, no-store"); err != nil {
		return nextgo.Page{}, err
	}
	if err := c.SetHeader("X-Robots-Tag", "noindex"); err != nil {
		return nextgo.Page{}, err
	}
	enabled := cookiesession.Available(c.Context, c.Request)
	role := "anonymous"
	if identity, err := cookiesession.Read(c.Context, c.Request); err == nil {
		role = string(identity)
	}
	var report ui.Node = ui.Fragment()
	if role == string(access.Administrator) {
		// Authorization is repeated at the data boundary. The earlier display
		// read never substitutes for a protected repository's own check.
		value, err := cookiesession.ReadReport(c.Context, c.Request)
		if err != nil {
			return nextgo.Page{}, err
		}
		report = ui.El("section", ui.Attrs{"class": "prose", "data-session-admin-report": ""}, ui.El("h2", nil, ui.Text(value.Title)), ui.El("p", nil, ui.Text(value.Summary)))
	}
	availability := "This configured demo uses a signed, HttpOnly, same-site cookie. Reader and administrator credentials are checked only on the server."
	if !enabled {
		availability = "The cookie demo is disabled. Configure NEXTGO_DEMO_COOKIE_KEY as 64 hexadecimal characters and use HTTPS. Loopback HTTP requires the explicit NEXTGO_DEMO_COOKIE_ALLOW_INSECURE=true development setting."
	}
	return nextgo.Page{Title: fmt.Sprintf("Cookie session: %s — Next.go", role), Description: "Signed cookie output and fresh server authorization in one Go action response.", Meta: map[string]string{"cookie-session-role": role}, Body: ui.Fragment(
		ui.El("section", ui.Attrs{"class": "prose", "data-cookie-session-page": "", "data-session-enabled": strconv.FormatBool(enabled)},
			ui.El("h1", nil, ui.Text("Signed cookie session")),
			ui.El("p", nil, ui.Text(availability)),
			ui.El("p", nil, ui.Text("Server-authorized role: "), ui.El("output", ui.Attrs{"data-session-role": ""}, ui.Text(role))),
			ui.El("p", nil, ui.Text("The sample is stateless across processes sharing its key and host/application scope. Its signed tokens expire within fifteen minutes. Signing keys and credentials never enter page props or typed action output.")),
			ui.El("p", nil, ui.Text("Signing out clears this browser cookie only; it does not revoke a copied token. This demo has no durable revocation, key rotation, identity provider or account store and makes no production authentication claim."))),
		sessionpanel.View(sessionpanel.Props{Role: role, Enabled: enabled, LoginAction: nextgo.MustURL(c, login.Contract.Path()), LogoutAction: nextgo.MustURL(c, logout.Contract.Path())}, report),
		ui.El("p", nil, ui.El("a", ui.Attrs{"href": nextgo.MustURL(c, "/features"), "data-nextgo-prefetch": "false"}, ui.Text("Leave cookie session"))))}, nil
}
