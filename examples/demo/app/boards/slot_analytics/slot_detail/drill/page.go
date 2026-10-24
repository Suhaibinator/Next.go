//go:build !js

package drill

import (
	nextgo "github.com/Suhaibinator/Next.go"
	"github.com/Suhaibinator/Next.go/examples/demo/components/counter"
)

func Page(c *nextgo.Context) (nextgo.Page, error) {
	initial := 15
	if c.Request.Header.Get("X-Demo-Detail-Version") == "updated" {
		initial = 25
	}
	return nextgo.Page{Body: counter.View(counter.Props{Initial: initial, Label: "Detail drill"})}, nil
}
