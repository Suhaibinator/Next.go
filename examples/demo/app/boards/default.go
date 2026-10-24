//go:build !js

package boards

import (
	nextgo "github.com/Suhaibinator/Next.go"
	"github.com/Suhaibinator/Next.go/examples/demo/components/counter"
	"github.com/Suhaibinator/Next.go/ui"
)

// Default supplies the implicit primary branch for slot-only direct URLs.
func Default(*nextgo.Context) (nextgo.Page, error) {
	return nextgo.Page{Title: "Default board — Next.go", Body: ui.Fragment(ui.El("p", nil, ui.Text("Primary default page")), counter.View(counter.Props{Initial: 1, Label: "Primary default"}))}, nil
}
