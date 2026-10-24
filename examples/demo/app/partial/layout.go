//go:build !js

package partial

import (
	"errors"
	"os"

	nextgo "github.com/Suhaibinator/Next.go"
	"github.com/Suhaibinator/Next.go/ui"
)

func Layout(c *nextgo.Context, body ui.Node) (ui.Node, error) {
	if os.Getenv("NEXTGO_DEMO_PARTIAL_SHELL_FAIL") == "true" {
		return nil, errors.New("private partial layout provider unavailable")
	}
	link := func(path, label string) ui.Node {
		return ui.El("a", ui.Attrs{"href": nextgo.MustURL(c, path)}, ui.Text(label))
	}
	return ui.El("section", ui.Attrs{"data-partial-layout": ""},
		ui.El("nav", ui.Attrs{"aria-label": "Partial pages"}, link("/partial", "Partial root"), ui.Text(" · "), link("/partial/alpha", "Partial alpha"), ui.Text(" · "), link("/partial/beta", "Partial beta")), body), nil
}
