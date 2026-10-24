//go:build !js

package activity

import (
	nextgo "github.com/Suhaibinator/Next.go"
	"github.com/Suhaibinator/Next.go/examples/demo/components/counter"
	"github.com/Suhaibinator/Next.go/ui"
)

func Page(*nextgo.Context) (nextgo.Page, error) {
	return nextgo.Page{Body: ui.Fragment(ui.El("h2", nil, ui.Text("Team activity")), counter.View(counter.Props{Initial: 9, Label: "Team activity"}))}, nil
}
