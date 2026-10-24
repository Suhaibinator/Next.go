//go:build !js

package previewpage

import (
	"fmt"
	nextgo "github.com/Suhaibinator/Next.go"
	"github.com/Suhaibinator/Next.go/draft"
	"github.com/Suhaibinator/Next.go/examples/demo/components/counter"
	"github.com/Suhaibinator/Next.go/examples/demo/preview"
	"github.com/Suhaibinator/Next.go/ui"
)

func Page(c *nextgo.Context) (nextgo.Page, error) {
	article, err := preview.Load(c)
	if err != nil {
		return nextgo.Page{}, err
	}
	controls := ui.El("p", nil, ui.Text("Preview access is not configured."))
	if draft.Configured(c) && !draft.Enabled(c) {
		controls = ui.El("form", ui.Attrs{"method": "post", "action": nextgo.MustURL(c, "/api/preview/enable")}, ui.El("label", nil, ui.Text("Editor token "), ui.El("input", ui.Attrs{"type": "password", "name": "token", "required": "", "autocomplete": "off"})), ui.El("button", ui.Attrs{"type": "submit"}, ui.Text("Enable preview")))
	}
	if draft.Enabled(c) {
		controls = ui.El("p", ui.Attrs{"role": "status"}, ui.Text("You are viewing unpublished content."))
	}
	return nextgo.Page{Title: article.Title + " — Next.go", Body: ui.El("article", ui.Attrs{"class": "prose", "data-preview-mode": fmt.Sprint(draft.Enabled(c)), "data-preview-generation": fmt.Sprint(article.Generation)}, ui.El("h1", nil, ui.Text(article.Title)), ui.El("p", nil, ui.Text(article.Body)), controls, counter.View(counter.Props{Label: "Preview votes", Initial: 0}), ui.El("a", ui.Attrs{"href": nextgo.MustURL(c, "/about")}, ui.Text("Read the architecture")))}, nil
}
