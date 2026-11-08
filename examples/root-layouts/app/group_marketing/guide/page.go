//go:build !js

package guide

import (
	nextgo "github.com/Suhaibinator/Next.go"
	"github.com/Suhaibinator/Next.go/ui"
)

func Page(*nextgo.Context) (nextgo.Page, error) {
	return nextgo.Page{Title: "Marketing guide", Body: ui.El("h1", nil, ui.Text("Same root, retained state"))}, nil
}
