//go:build !js

package disabled

import (
	nextgo "github.com/Suhaibinator/Next.go"
	"github.com/Suhaibinator/Next.go/seo"
	"github.com/Suhaibinator/Next.go/ui"
)

func Page(*nextgo.Context) (nextgo.Page, error) {
	manifest := ""
	return nextgo.Page{Title: "Disabled metadata", Metadata: seo.Metadata{Icons: []seo.Icon{}, Manifest: &manifest}, Body: ui.El("article", nil, ui.El("h1", nil, ui.Text("An empty choice clears defaults.")), ui.El("p", nil, ui.Text("A non-nil empty icon list and an explicitly empty manifest remove the managed head links. Back and forward navigation restores the correct choices for each page. The public resources remain available at their URLs.")))}, nil
}
