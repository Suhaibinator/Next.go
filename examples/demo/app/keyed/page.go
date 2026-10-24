//go:build !js

package keyedpage

import (
	nextgo "github.com/Suhaibinator/Next.go"
	"github.com/Suhaibinator/Next.go/examples/demo/components/keyedlist"
	"github.com/Suhaibinator/Next.go/ui"
)

func Page(c *nextgo.Context) (nextgo.Page, error) {
	items := []keyedlist.Item{{ID: "a", Label: "Alpha"}, {ID: "b", Label: "Bravo"}, {ID: "c", Label: "Charlie"}}
	return nextgo.Page{Title: "Keyed Go collections — Next.go", Body: ui.El("article", nil, ui.El("h1", nil, ui.Text("Stable rows, explicit Go updates")), keyedlist.View(keyedlist.Props{Name: "First keyed list", Items: items, Target: nextgo.MustURL(c, "/keyed/details"), Mismatch: c.Query.Get("mode") == "mismatch", BindPanic: c.Query.Get("mode") == "bind-panic", UpdatePanic: c.Query.Get("mode") == "update-panic"}), keyedlist.View(keyedlist.Props{Name: "Second keyed list", Items: items, Target: nextgo.MustURL(c, "/keyed/details")}))}, nil
}
