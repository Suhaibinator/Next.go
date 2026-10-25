//go:build !js

package two

import (
	nextgo "github.com/Suhaibinator/Next.go"
	"github.com/Suhaibinator/Next.go/assets"
	"github.com/Suhaibinator/Next.go/ui"
)

func Page(*nextgo.Context) (nextgo.Page, error) {
	return nextgo.Page{Resources: []assets.Resource{assets.Stylesheet("/assets/link-status.css")}, Title: "Second link page — Next.go", Body: ui.El("h1", nil, ui.Text("Second link page"))}, nil
}
