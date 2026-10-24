//go:build !js

package posts

import (
	"fmt"
	nextgo "github.com/Suhaibinator/Next.go"
	"github.com/Suhaibinator/Next.go/examples/demo/data"
	"github.com/Suhaibinator/Next.go/ui"
)

func Layout(c *nextgo.Context, body ui.Node) (ui.Node, error) {
	posts, err := data.AllForRequest(c)
	if err != nil {
		return nil, err
	}
	return ui.El("section", ui.Attrs{"class": "notes-layout", "data-posts-layout": ""}, ui.El("div", ui.Attrs{"class": "notes-bar"}, ui.El("a", ui.Attrs{"href": nextgo.MustURL(c, "/posts")}, ui.Text("FIELD NOTES")), ui.El("span", ui.Attrs{"class": "muted small"}, ui.Text(fmt.Sprintf("%d notes · Request-memoized · Nested layout", len(posts))))), body), nil
}
