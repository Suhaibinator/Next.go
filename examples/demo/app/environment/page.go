//go:build !js

package environmentpage

import (
	nextgo "github.com/Suhaibinator/Next.go"
	"github.com/Suhaibinator/Next.go/examples/demo/components/settings"
	"github.com/Suhaibinator/Next.go/ui"
)

func Page(c *nextgo.Context) (nextgo.Page, error) {
	message, _ := nextgo.PublicValue(c.Context, "demo.message")
	return nextgo.Page{Title: "Runtime configuration — Next.go", Body: ui.El("article", ui.Attrs{"class": "prose"}, ui.El("h1", nil, ui.Text("Runtime configuration")), ui.El("p", nil, ui.Text("The server explicitly publishes this message. Go in the browser reads the same document configuration without rebuilding WASM.")), settings.View(settings.Props{Message: message}), ui.El("a", ui.Attrs{"href": nextgo.MustURL(c, "/about")}, ui.Text("Architecture")))}, nil
}
