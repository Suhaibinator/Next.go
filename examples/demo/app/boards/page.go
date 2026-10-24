//go:build !js

package boards

import (
	nextgo "github.com/Suhaibinator/Next.go"
	"github.com/Suhaibinator/Next.go/ui"
)

func Page(*nextgo.Context) (nextgo.Page, error) {
	return nextgo.Page{Title: "Parallel boards — Next.go", Body: ui.El("p", nil, ui.Text("Two named file-route branches share this layout. Soft navigation retains unmatched authorized panels; a direct load uses defaults."))}, nil
}
