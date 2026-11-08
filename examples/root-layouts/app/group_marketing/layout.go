//go:build !js

package marketing

import (
	nextgo "github.com/Suhaibinator/Next.go"
	"github.com/Suhaibinator/Next.go/examples/root-layouts/components/counter"
	"github.com/Suhaibinator/Next.go/examples/root-layouts/components/rootnav"
	"github.com/Suhaibinator/Next.go/ui"
)

func Layout(c *nextgo.Context, body ui.Node) (ui.Node, error) {
	link := func(path, label string) ui.Node {
		return ui.El("a", ui.Attrs{"href": nextgo.MustURL(c, path), "data-nextgo-prefetch": "false"}, ui.Text(label))
	}
	var conditional ui.Node = ui.Fragment()
	if value, _ := nextgo.PublicValue(c, "rootRewrite"); value == "enabled" {
		conditional = link("/masked?other=1", "Conditional dashboard rewrite")
	}
	return ui.El("main", ui.Attrs{"data-root-layout": "marketing"}, ui.El("nav", nil, link("/", "Marketing home"), link("/guide", "Marketing guide"), link("/dashboard", "Open dashboard"), conditional, link("/plain", "Open implicit root")), counter.View(counter.Props{Initial: 2}), rootnav.View(), body), nil
}
