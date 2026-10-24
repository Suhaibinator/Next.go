//go:build !js

package param_id

import (
	nextgo "github.com/Suhaibinator/Next.go"
	"github.com/Suhaibinator/Next.go/examples/demo/components/counter"
	"github.com/Suhaibinator/Next.go/examples/demo/components/modal"
	"github.com/Suhaibinator/Next.go/examples/demo/internal/photos"
	"github.com/Suhaibinator/Next.go/ui"
)

func Page(c *nextgo.Context) (nextgo.Page, error) {
	p, err := photos.Load(c)
	if err != nil {
		return nextgo.Page{}, err
	}
	next := "two"
	if p.ID == "two" {
		next = "one"
	}
	from, ok := c.InterceptedFrom()
	if !ok {
		return nextgo.Page{}, nextgo.ErrNotFound
	}
	return nextgo.Page{Body: modal.View(modal.Props{Title: p.Title, ReturnTo: nextgo.MustURL(c, from)}, ui.Fragment(photos.View(p), counter.View(counter.Props{Label: "Modal photo counter", Initial: 20}), ui.El("a", ui.Attrs{"href": nextgo.MustURL(c, "/photos/"+next)}, ui.Text("Next photo"))))}, nil
}
