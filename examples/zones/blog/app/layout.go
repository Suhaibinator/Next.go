//go:build !js

package app

import (
	nextgo "github.com/Suhaibinator/Next.go"
	"github.com/Suhaibinator/Next.go/examples/zones/blog/components/counter"
	"github.com/Suhaibinator/Next.go/examples/zones/site"
	"github.com/Suhaibinator/Next.go/ui"
)

func Layout(c *nextgo.Context, body ui.Node) (ui.Node, error) {
	return site.Layout(c, body, "blog", counter.View(counter.Props{Label: "blog", Initial: 1}))
}
