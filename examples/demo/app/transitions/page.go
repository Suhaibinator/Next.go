//go:build !js

package transitions

import (
	nextgo "github.com/Suhaibinator/Next.go"
	"github.com/Suhaibinator/Next.go/examples/demo/components/transitioncard"
	"github.com/Suhaibinator/Next.go/ui"
)

func Page(c *nextgo.Context) (nextgo.Page, error) {
	return nextgo.Page{Title: "View transitions — Next.go", Body: ui.El("article", ui.Attrs{"class": "prose"},
		ui.El("h1", nil, ui.Text("Go view transitions")),
		ui.El("p", nil, ui.Text("Animation enhances a complete HTML page. Reduced motion and unsupported browsers use ordinary updates. Set NEXTGO_DEMO_VIEW_TRANSITIONS=true to animate navigation in this example.")),
		transitioncard.View(),
		ui.El("a", ui.Attrs{"href": nextgo.MustURL(c, "/about"), "data-nextgo-transition-type": "details"}, ui.Text("Animated architecture link")),
		ui.El("a", ui.Attrs{"href": nextgo.MustURL(c, "/posts"), "data-nextgo-transition": "none"}, ui.Text("Field notes without animation")))}, nil
}
