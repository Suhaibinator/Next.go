//go:build !js

package app

import (
	_ "embed"

	nextgo "github.com/Suhaibinator/Next.go"
	"github.com/Suhaibinator/Next.go/content"
	"github.com/Suhaibinator/Next.go/ui"
)

//go:embed guide.md
var guide string

func Page(*nextgo.Context) (nextgo.Page, error) {
	body, err := content.Parse(guide, content.Limits{})
	if err != nil {
		return nextgo.Page{}, err
	}
	return nextgo.Page{Title: "Bounded Markdown in Go", Body: ui.El("main", nil,
		ui.El("article", ui.Attrs{"id": "guide"}, body),
		notice("Ordinary Go composition", "This trusted component is composed outside the parsed document. Markdown cannot call it."),
	)}, nil
}

func notice(title, message string) ui.Node {
	return ui.El("aside", ui.Attrs{"aria-label": title}, ui.El("h2", nil, ui.Text(title)), ui.El("p", nil, ui.Text(message)))
}
