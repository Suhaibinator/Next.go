//go:build !js

package rules

import (
	nextgo "github.com/Suhaibinator/Next.go"
	"github.com/Suhaibinator/Next.go/ui"
	"os"
)

func Page(c *nextgo.Context) (nextgo.Page, error) {
	link := func(path, label string) ui.Node {
		return ui.El("li", nil, ui.El("a", ui.Attrs{"href": nextgo.MustURL(c, path)}, ui.Text(label)))
	}
	enabled := "Request-rule examples are disabled. Set NEXTGO_DEMO_RULES=true before starting the server."
	if os.Getenv("NEXTGO_DEMO_RULES") == "true" {
		enabled = "Request-rule examples are enabled. Rules select content; data and mutation handlers still enforce authorization."
	}
	return nextgo.Page{Title: "Request rules — Next.go", Body: ui.El("article", ui.Attrs{"class": "prose"},
		ui.El("h1", nil, ui.Text("Runtime request rules")), ui.El("p", nil, ui.Text(enabled)),
		ui.El("ul", nil, link("/rules/choose?post=html-first", "Choose a post by query"), link("/rules/jump/go-islands?go=yes", "Conditional redirect to Go islands"), link("/rules/header", "Choose a post by header"), link("/rules/cookie", "Choose a post by cookie"), link("/rules/host", "Choose architecture by host")),
		ui.El("p", nil, ui.Text("The header example selects Go islands for X-Demo-Route: go. The cookie example selects it for nextgo_variant=go. Both otherwise show HTML first. The host example matches localhost and 127.0.0.1.")),
		ui.El("p", nil, ui.Text("Set NEXTGO_DEMO_PROXY_ORIGIN to a trusted HTTP(S) origin to enable the remote links. Proxied content runs at this application's origin; only proxy backends you trust as application content.")),
		ui.El("ul", nil, link("/rules/external?go=yes", "Open external redirect"), link("/rules/remote/document", "Open proxied document"), link("/rules/remote/echo", "Inspect proxied JSON")))}, nil
}
