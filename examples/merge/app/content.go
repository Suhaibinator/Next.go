//go:build !js

package app

import (
	"fmt"
	nextgo "github.com/Suhaibinator/Next.go"
	"github.com/Suhaibinator/Next.go/assets"
	"github.com/Suhaibinator/Next.go/examples/merge/components/ordinary"
	"github.com/Suhaibinator/Next.go/examples/merge/components/panel"
	"github.com/Suhaibinator/Next.go/seo"
	"github.com/Suhaibinator/Next.go/ui"
	"strconv"
)

func ProductPage(c *nextgo.Context, product string) (nextgo.Page, error) {
	generation := loads.Add(1)
	if cookie(c, "deny", "") == "1" {
		return nextgo.Page{}, nextgo.ErrForbidden
	}
	version := cookie(c, "version", "1")
	number, _ := strconv.Atoi(version)
	q := c.Query.Get("q")
	makePanel := func(name string) ui.Node {
		extra := 0
		if name == "B" {
			extra = 5
		}
		p := panel.Props{Name: name, Product: product, Price: fmt.Sprintf("%d.00", number*10+len(q)+extra), Query: q, RawQuery: c.Request.URL.RawQuery, Version: version, Initial: 2, NavigateOnUpdate: name == "A" && cookie(c, "navigate", "") == "C", Reject: cookie(c, "reject", "") == name, EffectFail: cookie(c, "effectFail", "") == name}
		key := "editor"
		if name == "A" && c.Query.Get("keyed") == "1" {
			key += "-" + q
		}
		schema := "v1"
		if name == "B" && cookie(c, "schema", "") == "2" {
			schema = "v2"
		}
		return ui.Keyed("section", name, ui.Attrs{"class": "merge-carrier"}, panel.View(p, key, schema))
	}
	var ordinaryRoot ui.Node = ui.Fragment()
	if c.Query.Get("ordinary") == "1" {
		ordinaryRoot = ordinary.View(ordinary.Props{Caption: "Ordinary fresh version " + version + " query " + q})
	}
	title := fmt.Sprintf("Merge %s · %s · server %d", product, q, generation)
	return nextgo.Page{Title: title, Description: fmt.Sprintf("Fresh public server description %d", generation), Metadata: seo.Metadata{OpenGraph: seo.OpenGraph{Title: title, Description: "Public query and Refresh example", Type: "website"}}, Resources: []assets.Resource{assets.Stylesheet("/assets/merge.css")}, Retention: &nextgo.RouteRetention{Partition: "public-demo-" + cookie(c, "partition", "1")}, Body: ui.Fragment(
		ui.El("header", nil, ui.El("h1", nil, ui.Text("Fresh Go server output and local drafts")), ui.El("nav", ui.Attrs{"aria-label": "Merge example"},
			ui.El("a", ui.Attrs{"href": "?q=a", "data-nextgo-prefetch": "false"}, ui.Text("Query A")), ui.Text(" "), ui.El("a", ui.Attrs{"href": "?q=b", "data-nextgo-prefetch": "false"}, ui.Text("Query B")), ui.Text(" "), ui.El("a", ui.Attrs{"href": "?q=a&q=&q=two", "data-nextgo-prefetch": "false"}, ui.Text("Repeated query")))),
		ui.Text("Significant server text before both independent roots. "),
		makePanel("A"),
		ui.El("aside", ui.Attrs{"data-merge-server": "", "data-generation": strconv.FormatUint(generation, 10)}, ui.Text(fmt.Sprintf("Fresh server generation %d; version %s; query %s", generation, version, q))),
		makePanel("B"),
		ordinaryRoot,
		ui.El("footer", nil, ui.Text("Typed props, server skeleton and head commit together. Local drafts stay local.")),
	)}, nil
}
