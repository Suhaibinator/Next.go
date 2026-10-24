//go:build !js

package app

import (
	nextgo "github.com/Suhaibinator/Next.go"
	"github.com/Suhaibinator/Next.go/examples/demo/components/counter"
	"github.com/Suhaibinator/Next.go/ui"
)

func Page(c *nextgo.Context) (nextgo.Page, error) {
	return nextgo.Page{Title: "Next.go — Go, all the way through", Description: "A small server-rendered Go framework with WebAssembly islands.", Meta: map[string]string{"theme-color": "#f7f8fa"}, Body: ui.Fragment(
		ui.El("section", ui.Attrs{"class": "hero"},
			ui.El("div", nil, ui.El("p", ui.Attrs{"class": "eyebrow accent"}, ui.Text("A SMALL FULL-STACK FRAMEWORK")),
				ui.El("h1", nil, ui.Text("Go, all the way"), ui.El("br", nil), ui.El("span", ui.Attrs{"class": "muted"}, ui.Text("through."))),
				ui.El("p", ui.Attrs{"class": "lead"}, ui.Text("Real HTML from the server. Interactive components in your browser. One language, from the first request to the next click.")),
				ui.El("div", ui.Attrs{"class": "hero-links"}, ui.El("a", ui.Attrs{"class": "button primary", "href": nextgo.MustURL(c, "/posts")}, ui.Text("Explore the example →")), ui.El("a", ui.Attrs{"href": nextgo.MustURL(c, "/about")}, ui.Text("How it works ↗")))),
			ui.El("aside", ui.Attrs{"class": "code-card"}, ui.El("div", ui.Attrs{"class": "code-title"}, ui.Text("components/counter/client_js.go")), ui.El("pre", nil, ui.El("code", nil, ui.Text("state := client.NewState(3)\n\ns.On(button, \"click\", func(client.Event) {\n    state.Set(state.Get() + 1)\n})\n\ns.Cleanup(state.Subscribe(func(n int) {\n    output.Text(strconv.Itoa(n))\n}))"))), ui.El("div", ui.Attrs{"class": "code-foot"}, ui.Text("Compiled to WebAssembly. Executed by Go.")))),
		ui.El("section", ui.Attrs{"class": "demo-section", "id": "interactive"}, ui.El("div", ui.Attrs{"class": "section-heading"}, ui.El("div", nil, ui.El("p", ui.Attrs{"class": "eyebrow"}, ui.Text("01 / TRY IT")), ui.El("h2", nil, ui.Text("HTML first. Interaction follows."))), ui.El("span", ui.Attrs{"class": "badge"}, ui.Text("Two independent Go islands"))),
			ui.El("div", ui.Attrs{"class": "counter-grid"}, counter.View(counter.Props{Label: "First counter", Initial: 3}), counter.View(counter.Props{Label: "Second counter", Initial: 10}))),
		ui.El("section", ui.Attrs{"class": "feature-grid"}, feature("02", "Routes are files", "Static pages, dynamic parameters, and nested layouts. Ordinary Go packages."), feature("03", "Keep the first render", "Hydration binds events to the HTML already on screen. No initial DOM replacement."), feature("04", "Stay in the flow", "Follow a page link. Go fetches the next view and updates browser history.")))}, nil
}
func feature(n, title, body string) ui.Node {
	return ui.El("article", nil, ui.El("p", ui.Attrs{"class": "eyebrow"}, ui.Text(n+" / BUILT IN")), ui.El("h3", nil, ui.Text(title)), ui.El("p", ui.Attrs{"class": "muted"}, ui.Text(body)))
}
