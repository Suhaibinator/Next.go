//go:build !js

package boards

import (
	nextgo "github.com/Suhaibinator/Next.go"
	"github.com/Suhaibinator/Next.go/examples/demo/components/selectionnav"
	"github.com/Suhaibinator/Next.go/ui"
)

func Layout(c *nextgo.Context, body ui.Node) (ui.Node, error) {
	analytics, err := c.Slot("analytics")
	if err != nil {
		return nil, err
	}
	team, err := c.Slot("team")
	if err != nil {
		return nil, err
	}
	items := []selectionnav.Item{}
	for _, name := range []string{"children", "analytics", "team"} {
		value, err := c.Selection(name)
		if err != nil {
			return nil, err
		}
		items = append(items, selectionnav.Item{Label: name, Selection: value})
	}
	link := func(path, label string) ui.Node {
		return ui.El("a", ui.Attrs{"href": nextgo.MustURL(c, path)}, ui.Text(label))
	}
	return ui.El("section", ui.Attrs{"class": "notes-layout", "data-parallel-board": ""}, ui.El("h1", nil, ui.Text("Parallel boards")), ui.El("nav", ui.Attrs{"aria-label": "Board tabs"}, link("/boards", "Overview"), ui.Text(" · "), link("/boards/chart", "Chart"), ui.Text(" · "), link("/boards/settings", "Settings"), ui.Text(" · "), link("/boards/activity", "Activity"), ui.Text(" · "), link("/boards/drill", "Drill"), ui.Text(" · "), link("/boards/reports", "Reports"), ui.Text(" · "), link("/boards/reports/summary", "Summary")),
		selectionnav.View(selectionnav.Props{Label: "Board selections", Items: items}),
		ui.El("section", ui.Attrs{"data-board-primary": ""}, body), ui.El("div", ui.Attrs{"class": "post-grid"}, ui.El("aside", ui.Attrs{"aria-label": "Analytics panel"}, analytics), ui.El("aside", ui.Attrs{"aria-label": "Team panel"}, team))), nil
}
