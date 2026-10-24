//go:build !js

package scriptpage

import (
	"encoding/base64"
	"fmt"
	nextgo "github.com/Suhaibinator/Next.go"
	"github.com/Suhaibinator/Next.go/examples/demo/components/counter"
	"github.com/Suhaibinator/Next.go/examples/demo/components/lazycounter"
	"github.com/Suhaibinator/Next.go/examples/demo/components/scriptwidget"
	"github.com/Suhaibinator/Next.go/scripts"
	"github.com/Suhaibinator/Next.go/seo"
	"github.com/Suhaibinator/Next.go/ui"
)

func Page(c *nextgo.Context) (nextgo.Page, error) {
	dependency := scripts.Script{ID: "demo-vendor", Src: "/vendor-demo.js", Asset: true}
	global := "NextgoDemoVendor"
	switch c.Query.Get("mode") {
	case "lazy":
		dependency.Strategy = scripts.LazyOnload
	case "integrity":
		dependency.Integrity = "sha256-" + base64.StdEncoding.EncodeToString(make([]byte, 32))
	case "module":
		dependency.ID = "demo-module"
		dependency.Src = "/vendor-module.js"
		dependency.Type = "module"
		global = "NextgoDemoModule"
	case "missing":
		dependency.Src = "/missing-vendor.js"
	case "conflict":
		dependency.Src = "/vendor-module.js"
	}
	structured, err := seo.JSONLD(map[string]any{"@context": "https://schema.org", "@type": "WebPage", "name": "Managed Go dependencies"})
	if err != nil {
		return nextgo.Page{}, err
	}
	body := ui.El("article", nil, ui.El("h1", nil, ui.Text("Trusted scripts, managed from Go")), ui.El("p", nil, ui.Text("The HTML and Go counter work independently of this optional external library.")), counter.View(counter.Props{Label: "Independent Go counter", Initial: 2}), scriptwidget.View(scriptwidget.Props{Label: "First script widget", Dependency: dependency, Global: global}), scriptwidget.View(scriptwidget.Props{Label: "Second script widget", Dependency: dependency, Global: global}), lazycounter.View(lazycounter.Props{Label: "Lazy Go script consumer", Initial: 1, Target: nextgo.MustURL(c, "/scripts/details"), Dependency: &dependency}), ui.El("a", ui.Attrs{"href": nextgo.MustURL(c, "/scripts/details")}, ui.Text("Script details")), ui.El("a", ui.Attrs{"href": nextgo.MustURL(c, "/scripts/stream")}, ui.Text("Stream a script widget")))
	if c.Query.Get("mode") == "concurrent" {
		extra := []ui.Node{body}
		for i := 0; i < 6; i++ {
			extra = append(extra, scriptwidget.View(scriptwidget.Props{Label: fmt.Sprintf("Concurrent script widget %d", i), Dependency: scripts.Script{ID: fmt.Sprintf("concurrent-%d", i), Src: fmt.Sprintf("/vendor-demo.js?copy=%d", i), Asset: true}, Global: "NextgoDemoVendor"}))
		}
		body = ui.Fragment(extra...)
	}
	return nextgo.Page{Title: "Managed scripts — Next.go", Body: body, JSONLD: []seo.StructuredData{structured}}, nil
}
