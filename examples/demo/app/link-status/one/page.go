//go:build !js

package one

import (
	nextgo "github.com/Suhaibinator/Next.go"
	"github.com/Suhaibinator/Next.go/assets"
	"github.com/Suhaibinator/Next.go/ui"
)

func Page(*nextgo.Context) (nextgo.Page, error) {
	return nextgo.Page{Resources: []assets.Resource{assets.Stylesheet("/assets/link-status.css")}, Title: "First link page — Next.go", Body: ui.El("h1", nil, ui.Text("First link page"))}, nil
}
