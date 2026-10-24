//go:build !js

package evergreen

import (
	"fmt"
	nextgo "github.com/Suhaibinator/Next.go"
	"github.com/Suhaibinator/Next.go/examples/demo/components/toolbox"
	"github.com/Suhaibinator/Next.go/examples/demo/state"
	"github.com/Suhaibinator/Next.go/seo"
	"github.com/Suhaibinator/Next.go/ui"
	"time"
)

func Page(c *nextgo.Context) (nextgo.Page, error) {
	count := state.Count()
	timer := time.NewTimer(150 * time.Millisecond)
	defer timer.Stop()
	select {
	case <-timer.C:
	case <-c.Context.Done():
		return nextgo.Page{}, c.Context.Err()
	}
	return nextgo.Page{Metadata: seo.Metadata{Title: fmt.Sprintf("Evergreen snapshot %d", count)}, Body: ui.Fragment(
		ui.El("section", ui.Attrs{"class": "prose"}, ui.El("h1", nil, ui.Text("A cache without a hard expiry")),
			ui.El("p", nil, ui.Text("This named partial profile inherits the default: five minutes of Go browser reuse and refreshes server snapshots after fifteen minutes. It has no hard expiry; a transient refresh failure may retain last-good HTML. Entry and byte limits, eviction, strict invalidation and permanent-error policy still apply.")),
			ui.El("p", nil, ui.Text("These public snapshots and counter values live in one process. They do not survive restart or synchronize across replicas. HTTP/CDN responses must revalidate."))),
		ui.El("form", ui.Attrs{"method": "post", "action": nextgo.MustURL(c.Context, "/_nextgo/actions/evergreen-publish")},
			ui.El("button", ui.Attrs{"type": "submit", "data-evergreen-publish": ""}, ui.Text("Publish with default-profile refresh"))),
		toolbox.View(toolbox.Props{Count: count}),
	)}, nil
}
