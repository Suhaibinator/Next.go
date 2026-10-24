//go:build !js

package fixedarticle

import (
	nextgo "github.com/Suhaibinator/Next.go"
	"github.com/Suhaibinator/Next.go/ui"
)

func Page(c *nextgo.Context) (nextgo.Page, error) {
	return nextgo.Page{Title: "Listed article — Next.go", Body: ui.El("h1", nil, ui.Text("Listed: "+c.Params["slug"]))}, nil
}
