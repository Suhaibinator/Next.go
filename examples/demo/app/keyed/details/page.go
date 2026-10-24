//go:build !js

package keyeddetails

import (
	nextgo "github.com/Suhaibinator/Next.go"
	"github.com/Suhaibinator/Next.go/examples/demo/components/keyedlist"
)

func Page(c *nextgo.Context) (nextgo.Page, error) {
	return nextgo.Page{Title: "Keyed collection details — Next.go", Body: keyedlist.View(keyedlist.Props{Name: "Detail keyed list", Items: []keyedlist.Item{{ID: "a", Label: "Detail Alpha"}}, Target: nextgo.MustURL(c, "/keyed")})}, nil
}
