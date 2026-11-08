//go:build !js

package clear

import (
	nextgo "github.com/Suhaibinator/Next.go"
	"github.com/Suhaibinator/Next.go/seo"
	"github.com/Suhaibinator/Next.go/ui"
)

func Page(*nextgo.Context) (nextgo.Page, error) {
	return nextgo.Page{Title: "Cleared image defaults", Metadata: seo.Metadata{OpenGraph: seo.OpenGraph{Images: []seo.Image{}}, Twitter: seo.Twitter{Images: []string{}}}, Body: ui.El("h1", nil, ui.Text("The public files remain available."))}, nil
}
