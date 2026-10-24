//go:build !js

package details

import (
	nextgo "github.com/Suhaibinator/Next.go"
	"github.com/Suhaibinator/Next.go/examples/demo/components/scriptwidget"
	"github.com/Suhaibinator/Next.go/scripts"
	"github.com/Suhaibinator/Next.go/ui"
)

func Page(c *nextgo.Context) (nextgo.Page, error) {
	return nextgo.Page{Title: "Script details — Next.go", Body: ui.El("article", nil, ui.El("h1", nil, ui.Text("Reuse a document dependency")), scriptwidget.View(scriptwidget.Props{Label: "Detail script widget", Dependency: scripts.Script{ID: "demo-vendor-alias", Src: "/vendor-demo.js", Asset: true}, Global: "NextgoDemoVendor"}), ui.El("a", ui.Attrs{"href": nextgo.MustURL(c, "/scripts")}, ui.Text("Back to script widgets")))}, nil
}
