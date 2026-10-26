//go:build !js

package clientonlypage

import (
	nextgo "github.com/Suhaibinator/Next.go"
	"github.com/Suhaibinator/Next.go/examples/demo/components/clientpanel"
	"github.com/Suhaibinator/Next.go/examples/demo/components/clientpanellazy"
	"github.com/Suhaibinator/Next.go/examples/demo/components/counter"
	"github.com/Suhaibinator/Next.go/ui"
)

func Page(c *nextgo.Context) (nextgo.Page, error) {
	failure := c.Query.Get("fail")
	if failure != "error" && failure != "panic" {
		failure = ""
	}
	return nextgo.Page{Title: "Explicit client-only views — Next.go", Description: "Meaningful server fallbacks and explicitly browser-rendered Go components.", Body: ui.El("article", ui.Attrs{"data-client-only-page": ""},
		ui.El("h1", nil, ui.Text("Choose browser rendering explicitly.")),
		ui.El("p", nil, ui.Text("Ordinary islands keep their server DOM. These client-only boundaries instead render useful fallbacks, then create their views in Go after WebAssembly starts. Their outer roots stay in place. The lazy pair also waits for its component module.")),
		counter.View(counter.Props{Label: "Hydrated companion", Initial: 2}),
		clientpanel.View(clientpanel.Props{Label: "Eager browser panel", Initial: 3, Failure: failure}),
		clientpanel.View(clientpanel.Props{Label: "Eager independent panel", Initial: 7}),
		clientpanellazy.View(clientpanellazy.Props{Label: "Lazy browser panel", Initial: 5, Failure: failure}),
		clientpanellazy.View(clientpanellazy.Props{Label: "Lazy independent panel", Initial: 9}),
		ui.El("p", nil, ui.El("a", ui.Attrs{"href": nextgo.MustURL(c, "/about"), "data-client-only-exit": "", "data-nextgo-prefetch": "false"}, ui.Text("Leave these browser views"))),
		ui.El("p", nil, ui.Text("The optional fail=error and fail=panic query modes deliberately report a mounting failure and restore each affected fallback. These are recovery demonstrations; the default page mounts normally.")))}, nil
}
