//go:build !js

package actionviewpage

import (
	"context"
	"fmt"
	"strconv"
	"time"

	nextgo "github.com/Suhaibinator/Next.go"
	"github.com/Suhaibinator/Next.go/examples/demo/actions/view/save"
	"github.com/Suhaibinator/Next.go/examples/demo/actionviewstate"
	"github.com/Suhaibinator/Next.go/examples/demo/components/actionview"
	"github.com/Suhaibinator/Next.go/examples/demo/components/actionviewlazy"
	"github.com/Suhaibinator/Next.go/examples/demo/components/counter"
	"github.com/Suhaibinator/Next.go/ui"
)

func Page(c *nextgo.Context) (nextgo.Page, error) {
	value, err := actionviewstate.Load(c.Context)
	if err != nil {
		return nextgo.Page{}, err
	}
	delay := 250 * time.Millisecond
	if raw := c.Query.Get("delay"); raw != "" {
		parsed, err := time.ParseDuration(raw)
		if err != nil || parsed < 0 || parsed > 2*time.Second {
			return nextgo.Page{}, fmt.Errorf("invalid demo view delay")
		}
		delay = parsed
	}
	return nextgo.Page{Stream: true, Title: fmt.Sprintf("Action view revision %d — Next.go", value.Revision), Description: "One Go mutation response carries confirmed data and a fresh streamed page.", Meta: map[string]string{"action-view-revision": strconv.FormatInt(value.Revision, 10)}, Body: ui.Fragment(
		ui.El("section", ui.Attrs{"class": "prose", "data-action-view-page": ""},
			ui.El("h1", nil, ui.Text("One action, one streamed view")),
			ui.El("p", nil, ui.Text("This public process-local counter resets on restart. The generated Go caller receives confirmed data first, then the server's fresh HTML and deferred content through the same POST. A later view failure does not undo the action and must not trigger another mutation.")),
			ui.El("p", nil, ui.Text("Server snapshot count: "), ui.El("output", ui.Attrs{"data-action-view-ssr-count": ""}, ui.Text(strconv.FormatInt(value.Count, 10))), ui.Text("; revision: "), ui.El("output", ui.Attrs{"data-action-view-ssr-revision": ""}, ui.Text(strconv.FormatInt(value.Revision, 10))), ui.Text("; load: "), ui.El("output", ui.Attrs{"data-action-view-ssr-load": ""}, ui.Text(strconv.FormatInt(value.Load, 10))))),
		actionview.View(actionview.Props{Count: value.Count, Revision: value.Revision, Action: nextgo.MustURL(c, save.Contract.Path())}),
		actionviewlazy.View(actionviewlazy.Props{Count: value.Count, Revision: value.Revision}),
		ui.Defer("action-view-detail", ui.El("p", ui.Attrs{"data-action-view-fallback": ""}, ui.Text("Loading the streamed action snapshot…")), func(ctx context.Context) (ui.Node, error) {
			timer := time.NewTimer(delay)
			defer timer.Stop()
			select {
			case <-ctx.Done():
				return nil, ctx.Err()
			case <-timer.C:
			}
			if value.ViewFail {
				return nil, fmt.Errorf("private action view deferred diagnostic")
			}
			return ui.El("section", ui.Attrs{"class": "prose"},
				ui.El("p", nil, ui.Text("Deferred server count: "), ui.El("output", ui.Attrs{"data-action-view-deferred": ""}, ui.Text(strconv.FormatInt(value.Count, 10))), ui.Text("; revision: "), ui.El("output", ui.Attrs{"data-action-view-deferred-revision": ""}, ui.Text(strconv.FormatInt(value.Revision, 10)))),
				counter.View(counter.Props{Initial: int(value.Count), Label: "Action view streamed counter"})), nil
		}),
		ui.El("p", nil, ui.El("a", ui.Attrs{"href": nextgo.MustURL(c, "/features"), "data-nextgo-prefetch": "false"}, ui.Text("Leave action view"))))}, nil
}
