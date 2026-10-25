//go:build !js

package linkhint

import (
	"github.com/Suhaibinator/Next.go/ui"
	"strconv"
)

func View(p Props) ui.Node { return ui.Island("linkhint", p, Body(p)) }

// Body is shared by the eager and lazy examples; both render ordinary anchors.
func Body(p Props) ui.Node {
	links := make([]ui.Node, 0, len(p.Links))
	for i, link := range p.Links {
		links = append(links, ui.El("li", nil, ui.Link(link.Href, ui.LinkOptions{Prefetch: ui.PrefetchNone, Attrs: ui.Attrs{"data-link-hint-link": strconv.Itoa(i)}}, ui.Text(link.Label),
			ui.El("span", ui.Attrs{"data-link-hint": strconv.Itoa(i), "aria-hidden": "true", "class": "link-hint"}, ui.Text("")))))
	}
	return ui.El("nav", ui.Attrs{"aria-label": p.Label}, ui.El("ul", nil, links...))
}
