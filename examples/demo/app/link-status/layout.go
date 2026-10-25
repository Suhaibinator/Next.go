//go:build !js

package linkstatus

import (
	nextgo "github.com/Suhaibinator/Next.go"
	"github.com/Suhaibinator/Next.go/examples/demo/components/lazylinkhint"
	"github.com/Suhaibinator/Next.go/examples/demo/components/linkhint"
	"github.com/Suhaibinator/Next.go/ui"
)

func Layout(c *nextgo.Context, body ui.Node) (ui.Node, error) {
	links := []linkhint.Link{
		{Label: "First page", Href: nextgo.MustURL(c, "/link-status/one")},
		{Label: "Same first page", Href: nextgo.MustURL(c, "/link-status/one")},
		{Label: "Second page", Href: nextgo.MustURL(c, "/link-status/two")},
		{Label: "Deferred page", Href: nextgo.MustURL(c, "/link-status/stream")},
	}
	return ui.El("section", nil,
		ui.El("h2", nil, ui.Text("Link loading feedback")),
		ui.El("p", nil, ui.Text("These ordinary links work before WebAssembly. Go adds a local loading hint while a clicked link waits for its page; the shared menus remain mounted.")),
		linkhint.View(linkhint.Props{Label: "Eager link feedback", Links: links}),
		lazylinkhint.View(lazylinkhint.Props{Label: "Lazy link feedback", Links: links}), body), nil
}
