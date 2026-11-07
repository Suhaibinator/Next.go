//go:build !js

package boot

import (
	nextgo "github.com/Suhaibinator/Next.go"
	"github.com/Suhaibinator/Next.go/examples/merge/components/ordinary"
)

// Page intentionally has no route retention, props source or client-only view.
// The eager ordinary component binds a useful counter before its start Mount
// initiates a real navigation. The target never repeats that initial action.
func Page(c *nextgo.Context) (nextgo.Page, error) {
	step := c.Query.Get("step")
	if step == "" {
		step = "arrive"
	}
	return nextgo.Page{Title: "Boot " + step, Description: "Ordinary initial boot " + step, Body: ordinary.View(ordinary.Props{Caption: "Ordinary initial boot " + step, Boot: step == "start"})}, nil
}
