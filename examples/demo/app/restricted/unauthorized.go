//go:build !js

package restricted

import (
	nextgo "github.com/Suhaibinator/Next.go"
	"github.com/Suhaibinator/Next.go/ui"
)

func Unauthorized(c *nextgo.Context) (nextgo.Page, error) {
	if err := c.SetHeader("WWW-Authenticate", `Bearer realm="nextgo-demo"`); err != nil {
		return nextgo.Page{}, err
	}
	return nextgo.Page{Title: "Sign in required — Next.go", Body: ui.El("section", ui.Attrs{"class": "prose", "data-access-unauthorized": ""}, ui.El("p", ui.Attrs{"class": "eyebrow"}, ui.Text("401 / AUTHENTICATION REQUIRED")), ui.El("h1", nil, ui.Text("Sign in required")), ui.El("p", nil, ui.Text("This server-rendered report requires a configured Bearer credential. The demo has no public sign-in or account creation endpoint.")), ui.El("a", ui.Attrs{"href": nextgo.MustURL(c, "/features")}, ui.Text("Return to framework features")))}, nil
}
