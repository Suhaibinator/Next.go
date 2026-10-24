//go:build !js

package sharedcachepage

import (
	"errors"
	"fmt"

	nextgo "github.com/Suhaibinator/Next.go"
	"github.com/Suhaibinator/Next.go/cache/shared"
	"github.com/Suhaibinator/Next.go/examples/demo/shareddata"
	"github.com/Suhaibinator/Next.go/ui"
)

func Page(c *nextgo.Context) (nextgo.Page, error) {
	if err := c.SetHeader("Cache-Control", "private, no-store"); err != nil {
		return nextgo.Page{}, err
	}
	options := shared.LoadOptions{}
	background := c.Query.Get("refresh") == "background"
	if background {
		options.ScheduleRefresh = nextgo.CacheRefresh(c.Context)
	}
	data, info, err := shareddata.Load(c.Context, c.Query.Get("fail") == "1", options)
	var content ui.Node
	if errors.Is(err, shareddata.ErrDisabled) {
		content = ui.El("p", ui.Attrs{"data-shared-disabled": ""}, ui.Text("Set NEXTGO_DEMO_SHARED_ENDPOINT and NEXTGO_CACHE_TOKEN on the app, then run the nextgo-cache service. See docs/SHARED_CACHE.md for the two-instance example."))
	} else if err != nil {
		return nextgo.Page{}, err
	} else {
		if err := c.SetHeader("X-Demo-Shared-Cache", string(info.State)); err != nil {
			return nextgo.Page{}, err
		}
		content = ui.Fragment(
			ui.El("p", nil, ui.Text("Generation: "), ui.El("code", ui.Attrs{"data-shared-generation": ""}, ui.Text(data.Generation))),
			ui.El("p", nil, ui.Text(fmt.Sprintf("Generated at %s by process %d; this read: ", data.GeneratedAt, data.Process)), ui.El("span", ui.Attrs{"data-shared-cache-state": ""}, ui.Text(string(info.State))), ui.Text(".")),
			ui.El("form", ui.Attrs{"method": "post", "action": nextgo.MustURL(c, "/_nextgo/actions/shared-cache-stale")}, ui.El("button", ui.Attrs{"type": "submit"}, ui.Text("Mark shared generation stale"))),
			ui.El("form", ui.Attrs{"method": "post", "action": nextgo.MustURL(c, "/_nextgo/actions/shared-cache-expire")}, ui.El("button", ui.Attrs{"type": "submit"}, ui.Text("Expire shared generation"))))
	}
	mode := ui.El("a", ui.Attrs{"href": nextgo.MustURL(c, "/shared-cache?refresh=background")}, ui.Text("Use background refresh"))
	if background {
		mode = ui.El("p", nil, ui.Text("Background refresh uses bounded completion work. Native HTTP runs it after handling the response; Lambda drains it after publication, before accepting another event. Hard expiry always waits for fresh data."))
	}
	return nextgo.Page{Title: "Shared Go cache — Next.go", Body: ui.El("section", ui.Attrs{"class": "prose"},
		ui.El("h1", nil, ui.Text("Shared data across Go instances")),
		ui.El("p", nil, ui.Text("Two app processes contact one authenticated cache service and read the same serialized generation, fresh for 15 seconds. Default reads then wait for a replacement; background mode can show an unexpired last-good generation while refreshing. Tag expiration also fences pending loaders on every connected instance.")),
		content,
		mode,
		ui.El("p", nil, ui.El("a", ui.Attrs{"href": nextgo.MustURL(c, "/shared-pages")}, ui.Text("Try shared rendered-page snapshots"))),
		ui.El("p", nil, ui.Text("This public demonstration stores generated IDs in a bounded memory service. Restarting that service loses its contents. Page snapshots, application writes and browser/CDN caches are not made shared by this data API.")),
		ui.El("a", ui.Attrs{"href": nextgo.MustURL(c, "/cache")}, ui.Text("Compare the process-local page cache")))}, nil
}
