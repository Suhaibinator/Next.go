//go:build !js

package keyedtable

import (
	nextgo "github.com/Suhaibinator/Next.go"
	"github.com/Suhaibinator/Next.go/examples/demo/components/keyedlist"
)

func Page(c *nextgo.Context) (nextgo.Page, error) {
	return nextgo.Page{Title: "Keyed Go table — Next.go", Body: keyedlist.View(keyedlist.Props{Name: "Keyed table", Table: true, Items: []keyedlist.Item{{ID: "a", Label: "Alpha"}, {ID: "b", Label: "Bravo"}}, Target: nextgo.MustURL(c, "/keyed")})}, nil
}
