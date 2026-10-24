//go:build !js

package sharedprerender

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

func Page(c *nextgo.Context) (nextgo.Page, error) {
	slug := c.Params["slug"]
	if !validSlug(slug) {
		return nextgo.Page{}, nextgo.ErrNotFound
	}
	if os.Getenv("NEXTGO_DEMO_SHARED_PRERENDER_FAIL") == "true" {
		return nextgo.Page{}, errors.New("demo shared prerender source unavailable")
	}
	var random [16]byte
	if _, err := rand.Read(random[:]); err != nil {
		return nextgo.Page{}, err
	}
	generation := hex.EncodeToString(random[:])
	title := "Shared prerender " + slug + " " + generation
	description := "Public build snapshot " + generation + " with authority-coordinated regeneration."
	structured, err := seo.JSONLD(map[string]any{"@context": "https://schema.org", "@type": "WebPage", "name": title, "identifier": generation})
	if err != nil {
		return nextgo.Page{}, err
	}
	enabled := os.Getenv("NEXTGO_DEMO_SHARED_PRERENDER") == "true"
	form := func(action, label string) ui.Node {
		button := ui.Attrs{"type": "submit"}
		if !enabled {
			button["disabled"] = ""
		}
		return ui.El("form", ui.Attrs{"method": "post", "action": nextgo.MustURL(c, "/_nextgo/actions/"+action)},
			ui.El("input", ui.Attrs{"type": "hidden", "name": "slug", "value": slug}), ui.El("button", button, ui.Text(label)))
	}
	mode := "Local prerendering is active. Enable NEXTGO_DEMO_SHARED_PRERENDER with the shared authority configured at build and runtime to share generations."
	if enabled {
		mode = "Shared prerendering is active. The build reserves an authority ticket; runtime imports the seed only while that ticket remains valid."
	}
	return nextgo.Page{
		Title: title, Description: description,
		Meta: map[string]string{"shared-prerender-generation": generation}, JSONLD: []seo.StructuredData{structured},
		Metadata: seo.Metadata{Language: "en", Direction: "ltr", OpenGraph: seo.OpenGraph{Title: title, Description: description, Type: "website"}},
		Body: ui.Fragment(ui.El("section", ui.Attrs{"class": "prose", "data-shared-prerender": ""},
			ui.El("h1", nil, ui.Text("Shared prerender: "+slug)), ui.El("p", ui.Attrs{"data-shared-prerender-mode": ""}, ui.Text(mode)),
			ui.El("p", nil, ui.Text("Generation: "), ui.El("code", ui.Attrs{"data-shared-prerender-generation": ""}, ui.Text(generation))),
			ui.El("p", ui.Attrs{"data-shared-prerender-process": ""}, ui.Text(fmt.Sprintf("Rendered by process %d at %s.", os.Getpid(), time.Now().UTC().Format(time.RFC3339Nano)))),
			ui.El("p", nil, ui.Text("Alpha and beta are rendered during the build. Other valid slugs generate on demand. Each public generation keeps its HTML, metadata and two Go counter islands together; counters maintain independent state in this browser.")),
			form("shared-prerender-tag-expire", "Expire prerender tag"), form("shared-prerender-path-expire", "Expire prerender path"),
			ui.El("p", nil, ui.El("a", ui.Attrs{"href": nextgo.MustURL(c, "/shared-prerender")}, ui.Text("Shared prerender overview")), ui.Text(" · "),
				ui.El("a", ui.Attrs{"href": nextgo.MustURL(c, "/shared-prerender/beta")}, ui.Text("Open beta")), ui.Text(" · "),
				ui.El("a", ui.Attrs{"href": nextgo.MustURL(c, "/shared-prerender/new")}, ui.Text("Generate an unlisted path")))),
			counter.View(counter.Props{Label: "Prerender first counter", Initial: 3}), counter.View(counter.Props{Label: "Prerender second counter", Initial: 10})),
	}, nil
}

func validSlug(slug string) bool {
	if len(slug) == 0 || len(slug) > 64 {
		return false
	}
	for _, r := range slug {
		if !(r >= 'a' && r <= 'z' || r >= '0' && r <= '9' || r == '-') {
			return false
		}
	}
	return true
}
