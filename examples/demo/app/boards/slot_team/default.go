//go:build !js

package team

import (
	nextgo "github.com/Suhaibinator/Next.go"
	"github.com/Suhaibinator/Next.go/examples/demo/components/counter"
	"github.com/Suhaibinator/Next.go/ui"
)

func Default(*nextgo.Context) (nextgo.Page, error) {
	return nextgo.Page{Body: ui.Fragment(ui.El("h2", nil, ui.Text("Team default")), counter.View(counter.Props{Initial: 3, Label: "Default team"}))}, nil
}
