//go:build !js

package widgetpage

import (
	nextgo "github.com/Suhaibinator/Next.go"
	"github.com/Suhaibinator/Next.go/assets"
	"github.com/Suhaibinator/Next.go/examples/widgets/components/chart"
	"github.com/Suhaibinator/Next.go/examples/widgets/components/chartlazy"
	"github.com/Suhaibinator/Next.go/scripts"
	"github.com/Suhaibinator/Next.go/ui"
)

func View(c *nextgo.Context, lazy bool, mode string) (nextgo.Page, error) {
	data, err := scripts.NewDataAttributes(map[string]string{"data-chart-library": "uplot", "data-chart-label": `Go "owned" <chart> & café`})
	if err != nil {
		return nextgo.Page{}, err
	}
	p := chart.Props{Mode: mode, Dependency: scripts.Script{ID: "uplot-1.6.32", Src: "/vendor/uPlot-1.6.32.iife.min.js", Asset: true,
		Integrity: "sha384-Gx3t0zdBAuQOuvvmaLZj7HKEiSgWTAs+VdtNY7wt19QDPTDQjFIwAuXDj0zeN00c", Data: data}}
	if mode == "esm" || mode == "esm-late" {
		p.Module = &scripts.Module{ID: "chart-module-v1", Src: "/vendor/chart-module-v1.js", Asset: true}
		p.Mode = ""
		if mode == "esm-late" {
			p.Mode = "late"
		}
	}
	view, title := chart.View(p), "Scoped chart"
	if lazy {
		view, title = chartlazy.View(p), "Lazy scoped chart"
	}
	links := []ui.Node{}
	for _, link := range [][2]string{{"/", "Chart home"}, {"/lazy", "Lazy chart"}, {"/esm", "ESM chart"}, {"/lazy-esm", "Lazy ESM chart"}, {"/late", "Late chart"}, {"/lazy-late", "Lazy late chart"}, {"/failure", "Failing constructor"}, {"/about", "Chart details"}} {
		links = append(links, ui.Link(nextgo.MustURL(c, link[0]), ui.LinkOptions{Prefetch: ui.PrefetchNone}, ui.Text(link[1])))
	}
	return nextgo.Page{Title: title, Resources: []assets.Resource{assets.Stylesheet("/assets/vendor/uPlot-1.6.32.min.css",
		"sha384-IfV0B7MIOYuO95kO9G5ySKPz/85zqFNOAs8iy4tkK5zd9izhJAB8b7lHrwYqqmYE")},
		Body: ui.El("main", nil, ui.El("h1", nil, ui.Text("Scoped browser chart")), view, ui.El("nav", ui.Attrs{"aria-label": "Chart examples"}, links...))}, nil
}
