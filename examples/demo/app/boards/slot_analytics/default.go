//go:build !js

package analytics

import (
	"fmt"
	nextgo "github.com/Suhaibinator/Next.go"
	"github.com/Suhaibinator/Next.go/examples/demo/components/counter"
	"github.com/Suhaibinator/Next.go/ui"
)

func Default(c *nextgo.Context) (nextgo.Page, error) {
	switch c.Request.Header.Get("X-Demo-Default-Failure") {
	case "error":
		return nextgo.Page{}, fmt.Errorf("demonstration default loader failure")
	case "missing":
		return nextgo.Page{}, nextgo.ErrNotFound
	}
	return nextgo.Page{Body: ui.Fragment(ui.El("h2", nil, ui.Text("Analytics default")), counter.View(counter.Props{Initial: 2, Label: "Default analytics"}))}, nil
}
