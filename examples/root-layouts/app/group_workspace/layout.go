//go:build !js

package workspace

import (
	nextgo "github.com/Suhaibinator/Next.go"
	"github.com/Suhaibinator/Next.go/examples/root-layouts/components/counter"
	"github.com/Suhaibinator/Next.go/ui"
)

func Layout(c *nextgo.Context, body ui.Node) (ui.Node, error) {
	link := func(path, label string) ui.Node {
		return ui.El("a", ui.Attrs{"href": nextgo.MustURL(c, path), "data-nextgo-prefetch": "false"}, ui.Text(label))
	}
	return ui.El("main", ui.Attrs{"data-root-layout": "workspace"}, ui.El("nav", nil, link("/", "Marketing home"), link("/dashboard", "Dashboard home"), link("/dashboard/settings", "Dashboard settings")), counter.View(counter.Props{Initial: 40}), body), nil
}
