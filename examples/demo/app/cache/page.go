//go:build !js

package cachepage

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
	// Simulate a cancellable backend read, including during background refresh.
	timer := time.NewTimer(150 * time.Millisecond)
	defer timer.Stop()
	select {
	case <-timer.C:
	case <-c.Context.Done():
		return nextgo.Page{}, c.Context.Err()
	}
	return nextgo.Page{Metadata: seo.Metadata{Title: fmt.Sprintf("Cached snapshot %d", count)}, Body: ui.Fragment(
		ui.El("section", ui.Attrs{"class": "prose"}, ui.El("h1", nil, ui.Text("Server cache lifecycle")),
			ui.El("p", nil, ui.El("a", ui.Attrs{"href": nextgo.MustURL(c, "/shared-cache")}, ui.Text("Try shared data across two app processes"))),
			ui.El("p", nil, ui.Text("This public snapshot is fresh for one second. For the next four seconds a read can return the last good snapshot while bounded background work refreshes it. After five seconds a read waits for fresh data.")),
			ui.El("p", nil, ui.Text("Change the illustrative source below, wait a second, then use Refresh server data twice to observe stale and refreshed snapshots. Increment on server publishes and strictly expires this cache so its own write is visible immediately.")),
			ui.El("form", ui.Attrs{"method": "post", "action": nextgo.MustURL(c, "/_nextgo/actions/cache-source-increment")}, ui.El("button", ui.Attrs{"type": "submit"}, ui.Text("Change source without invalidating snapshot"))),
			ui.El("p", nil, ui.Text("The source and cache are public, process-local examples. They reset on restart and are not shared between instances. Browser/CDN reuse is disabled for this lifetime policy."))),
		toolbox.View(toolbox.Props{Count: count}),
	)}, nil
}
