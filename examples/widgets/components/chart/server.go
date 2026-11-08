//go:build !js

package chart

import "github.com/Suhaibinator/Next.go/ui"

func View(p Props) ui.Node { return ui.Island("chart", p, Body()) }

func Body() ui.Node {
	return ui.El("section", ui.Attrs{"aria-label": "Sample chart"},
		ui.El("output", ui.Attrs{"data-widget-status": "", "aria-live": "polite"}, ui.Text("Chart preview")),
		ui.El("div", ui.Attrs{"data-widget-host": "", "role": "img", "aria-label": "Line chart; sample values are listed in the table"},
			ui.El("p", ui.Attrs{"data-widget-fallback": ""}, ui.Text("The interactive chart will appear here."))),
		ui.El("table", nil, ui.El("caption", nil, ui.Text("Sample data")),
			ui.El("thead", nil, ui.El("tr", nil, ui.El("th", nil, ui.Text("Sample")), ui.El("th", nil, ui.Text("Value")))),
			ui.El("tbody", nil, row("1", "2"), row("2", "4"), row("3", "3"), row("4", "5"))),
		ui.El("button", ui.Attrs{"type": "button", "data-widget-stop": "", "disabled": ""}, ui.Text("Stop chart")),
		ui.El("button", ui.Attrs{"type": "button", "data-widget-start": "", "disabled": ""}, ui.Text("Start chart")))
}

func row(sample, value string) ui.Node {
	return ui.El("tr", nil, ui.El("td", nil, ui.Text(sample)), ui.El("td", nil, ui.Text(value)))
}
