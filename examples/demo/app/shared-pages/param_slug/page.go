//go:build !js

package sharedpage

import (
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"time"

	nextgo "github.com/Suhaibinator/Next.go"
	"github.com/Suhaibinator/Next.go/examples/demo/components/counter"
	"github.com/Suhaibinator/Next.go/seo"
	"github.com/Suhaibinator/Next.go/ui"
)

func validSlug(slug string) bool {
	if len(slug) == 0 || len(slug) > 64 {
		return false
	}
	for _, c := range slug {
		if !(c >= 'a' && c <= 'z' || c >= '0' && c <= '9' || c == '-') {
			return false
		}
	}
	return true
}
func Page(c *nextgo.Context) (nextgo.Page, error) {
	slug := c.Params["slug"]
	if !validSlug(slug) {
		return nextgo.Page{}, nextgo.ErrNotFound
	}
	if os.Getenv("NEXTGO_DEMO_SHARED_PAGE_FAIL") == "true" {
		return nextgo.Page{}, errors.New("demo shared page source unavailable")
	}
	var bytes [16]byte
	if _, err := rand.Read(bytes[:]); err != nil {
		return nextgo.Page{}, err
	}
	generation := hex.EncodeToString(bytes[:])
	title := "Shared page " + slug + " " + generation
	description := "Public rendered generation " + generation + " shared by Go application instances."
	structured, err := seo.JSONLD(map[string]any{"@context": "https://schema.org", "@type": "WebPage", "name": title, "identifier": generation})
	if err != nil {
		return nextgo.Page{}, err
	}
	enabled := os.Getenv("NEXTGO_DEMO_SHARED_ENDPOINT") != ""
	form := func(action, label string) ui.Node {
		button := ui.Attrs{"type": "submit"}
		if !enabled {
			button["disabled"] = ""
		}
		return ui.El("form", ui.Attrs{"method": "post", "action": nextgo.MustURL(c, "/_nextgo/actions/"+action)}, ui.El("input", ui.Attrs{"type": "hidden", "name": "slug", "value": slug}), ui.El("button", button, ui.Text(label)))
	}
	var notice ui.Node = ui.Fragment()
	if !enabled {
		notice = ui.El("p", ui.Attrs{"data-shared-page-disabled": ""}, ui.Text("This page is rendering normally. Set NEXTGO_DEMO_SHARED_ENDPOINT and NEXTGO_CACHE_TOKEN to enable the shared public-page policy."))
	}
	return nextgo.Page{Title: title, Description: description, Meta: map[string]string{"shared-page-generation": generation}, JSONLD: []seo.StructuredData{structured}, Metadata: seo.Metadata{
		Language: "en", Direction: "ltr", Viewport: &seo.Viewport{Fit: "cover", ThemeColors: []seo.ThemeColor{{Color: "#115e59"}}}, OpenGraph: seo.OpenGraph{Title: title, Description: description, Type: "website"},
	}, Body: ui.Fragment(ui.El("section", ui.Attrs{"class": "prose", "data-shared-page": ""},
		ui.El("h1", nil, ui.Text("Shared rendered page: "+slug)), notice,
		ui.El("p", nil, ui.Text("Generation: "), ui.El("code", ui.Attrs{"data-shared-page-generation": ""}, ui.Text(generation))),
		ui.El("p", ui.Attrs{"data-shared-page-process": ""}, ui.Text(fmt.Sprintf("Rendered by process %d at %s.", os.Getpid(), time.Now().UTC().Format(time.RFC3339Nano)))),
		ui.El("p", nil, ui.Text("The public body, layout, head and document controls are frozen together. Go counters hydrate those stored nodes in each browser independently. After fifteen seconds an unexpired snapshot can refresh through bounded completion work.")),
		form("shared-page-tag-expire", "Expire page tag"), form("shared-page-path-expire", "Expire page path"), form("shared-page-stale", "Mark page stale"),
		ui.El("p", nil, ui.El("a", ui.Attrs{"href": nextgo.MustURL(c, "/shared-pages")}, ui.Text("Leave shared page")), ui.Text(" · "), ui.El("a", ui.Attrs{"href": nextgo.MustURL(c, "/shared-pages/beta")}, ui.Text("Open another shared page")))),
		counter.View(counter.Props{Label: "Shared first counter", Initial: 3}), counter.View(counter.Props{Label: "Shared second counter", Initial: 10}),
	)}, nil
}
