//go:build !js

package profiles

import (
	"fmt"
	"time"

	nextgo "github.com/Suhaibinator/Next.go"
	"github.com/Suhaibinator/Next.go/examples/demo/components/toolbox"
	"github.com/Suhaibinator/Next.go/examples/demo/state"
	"github.com/Suhaibinator/Next.go/seo"
	"github.com/Suhaibinator/Next.go/ui"
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
	return nextgo.Page{Metadata: seo.Metadata{Title: fmt.Sprintf("Profiled snapshot %d", count)}, Body: ui.Fragment(
		ui.El("section", ui.Attrs{"class": "prose"}, ui.El("h1", nil, ui.Text("Named cache profiles")),
			ui.El("p", nil, ui.Text("This explicit public profile allows up to two seconds of Go browser reuse, refreshes server snapshots after one second, and has an eight-second hard expiry. HTTP/CDN responses must revalidate.")),
			ui.El("p", nil, ui.Text("Go action requests clear the current browser cache on dispatch and settlement. Increment without refreshing page leaves the displayed number unchanged; navigate away and back to read the published value.")),
			ui.El("p", nil, ui.Text("Refresh server data clears browser reuse but can still receive a valid server stale snapshot while its background refresh runs. The counter and cache are public, process-local demonstrations."))),
		ui.El("form", ui.Attrs{"method": "post", "action": nextgo.MustURL(c.Context, "/_nextgo/actions/cache-publish-stale")},
			ui.El("p", nil, ui.Text("Publish with stale refresh commits a counter update and marks this snapshot stale. The next read may show the last-good value while Go refreshes it; the existing hard expiry is never extended.")),
			ui.El("button", ui.Attrs{"type": "submit", "data-publish-stale": ""}, ui.Text("Publish with stale refresh"))),
		toolbox.View(toolbox.Props{Count: count}),
	)}, nil
}
