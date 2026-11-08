//go:build !js

package privatepage

import (
	nextgo "github.com/Suhaibinator/Next.go"
	native "github.com/Suhaibinator/Next.go/examples/demo/app/_native"
	private "github.com/Suhaibinator/Next.go/examples/demo/app/private_helpers"
	"github.com/Suhaibinator/Next.go/ui"
)

func Page(c *nextgo.Context) (nextgo.Page, error) {
	return nextgo.Page{Title: "Routing-private Go helpers", Body: ui.El("article", ui.Attrs{"class": "prose", "data-private-routing-page": ""},
		ui.El("h1", nil, ui.Text("Private routing. Ordinary Go imports.")),
		ui.El("p", ui.Attrs{"data-private-helper-message": ""}, ui.Text(private.PublicMessage)),
		ui.El("p", ui.Attrs{"data-underscore-helper-message": ""}, ui.Text(native.PublicMessage)),
		ui.El("p", nil, ui.Text("Directories named private_name or beginning with an underscore are excluded from route discovery, including nested slots, layouts and handlers. Their Go code can still be imported and remains part of build/watch snapshots.")),
		ui.El("p", nil, ui.Text("This is route organization, not authorization or a client/server boundary. Only deliberately public demonstration strings are shown here; Go build tags keep these helpers native-only.")),
		ui.El("a", ui.Attrs{"href": nextgo.MustURL(c, "/_public"), "data-private-literal-link": "", "data-nextgo-prefetch": "false"}, ui.Text("Visit an intentional underscore URL")),
		ui.El("a", ui.Attrs{"href": nextgo.MustURL(c, "/about"), "data-private-routing-exit": "", "data-nextgo-prefetch": "false"}, ui.Text("Leave this routing example")))}, nil
}
