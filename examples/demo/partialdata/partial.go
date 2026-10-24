//go:build !js

// Package partialdata separates reusable public shells from current-request data.
package partialdata

import (
	"bytes"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"time"

	nextgo "github.com/Suhaibinator/Next.go"
	"github.com/Suhaibinator/Next.go/examples/demo/components/counter"
	"github.com/Suhaibinator/Next.go/seo"
	"github.com/Suhaibinator/Next.go/ui"
)

type VisitorProps struct {
	Slug string `json:"slug"`
}

var processNonce = randomID()

func randomID() string {
	var value [16]byte
	if _, err := rand.Read(value[:]); err != nil {
		panic(err)
	}
	return hex.EncodeToString(value[:])
}

func Prerender() *nextgo.PrerenderPolicy {
	return &nextgo.PrerenderPolicy{Partial: true, HostIndependent: true, Revalidate: time.Hour, Expire: 24 * time.Hour, Tags: []string{"partial-demo"}, Fallback: nextgo.FallbackNotFound}
}

func Page(c *nextgo.Context, slug string) (nextgo.Page, error) {
	if os.Getenv("NEXTGO_DEMO_PARTIAL_SHELL_FAIL") == "true" {
		return nextgo.Page{}, errors.New("private partial shell provider unavailable")
	}
	generation := randomID()
	title := "Partial " + slug + " " + generation
	description := "A public Go shell " + generation + " with request-time server content."
	structured, err := seo.JSONLD(map[string]any{"@context": "https://schema.org", "@type": "WebPage", "identifier": generation, "name": title})
	if err != nil {
		return nextgo.Page{}, err
	}
	return nextgo.Page{Title: title, Description: description, Meta: map[string]string{"partial-shell-generation": generation}, JSONLD: []seo.StructuredData{structured}, Metadata: seo.Metadata{Language: "en", Direction: "ltr", OpenGraph: seo.OpenGraph{Title: title, Description: description, Type: "website"}},
		Body: ui.El("section", ui.Attrs{"data-partial-shell": slug},
			ui.El("h1", nil, ui.Text("A reusable public shell: "+slug)),
			ui.El("p", nil, ui.Text("This HTML and metadata were prepared during the build. Only the named region reads your current request.")),
			ui.El("code", ui.Attrs{"data-partial-shell-generation": ""}, ui.Text(generation)),
			ui.El("p", ui.Attrs{"data-partial-shell-process": ""}, ui.Text(fmt.Sprintf("Shell created by process %d", os.Getpid()))),
			counter.View(counter.Props{Label: "Partial shell counter", Initial: 3}),
			ui.RequestRegion("request-content", "visitor", VisitorProps{Slug: slug}, ui.El("p", ui.Attrs{"data-partial-loading": "", "role": "status"}, ui.Text("Personalizing this region on the server…"))),
			ui.El("p", nil, ui.El("a", ui.Attrs{"href": nextgo.MustURL(c, "/partial")}, ui.Text("Partial overview")))),
	}, nil
}

func Regions() map[string]nextgo.RegionFunc { return map[string]nextgo.RegionFunc{"visitor": visitor} }

func visitor(c *nextgo.Context, raw json.RawMessage) (ui.Node, error) {
	if os.Getenv("NEXTGO_DEMO_PARTIAL_REGION_FAIL") == "true" {
		return nil, errors.New("private partial region provider unavailable")
	}
	var props VisitorProps
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&props); err != nil {
		return nil, err
	}
	if err := decoder.Decode(new(any)); err != io.EOF {
		return nil, errors.New("invalid partial region props")
	}
	delay := 350 * time.Millisecond
	if text := c.Query.Get("delay"); text != "" {
		parsed, err := time.ParseDuration(text)
		if err != nil || parsed < 0 || parsed > 2*time.Second {
			return nil, errors.New("private partial delay is outside the sample range")
		}
		delay = parsed
	}
	timer := time.NewTimer(delay)
	defer timer.Stop()
	select {
	case <-c.Done():
		return nil, c.Err()
	case <-timer.C:
	}
	message := c.Query.Get("message")
	if message == "" {
		message = "Hello from the current request"
	}
	name := "anonymous"
	if cookie, err := c.Request.Cookie("nextgo_partial_name"); err == nil {
		name = cookie.Value
	}
	slug := c.Params["slug"]
	if slug == "" {
		slug = "root"
	}
	return ui.El("section", ui.Attrs{"data-partial-region": "", "aria-label": "Current request region"},
		ui.El("h2", nil, ui.Text("Current request, fresh server data")),
		ui.El("p", ui.Attrs{"data-partial-message": ""}, ui.Text(message)),
		ui.El("p", ui.Attrs{"data-partial-visitor": ""}, ui.Text(name)),
		ui.El("code", ui.Attrs{"data-partial-request-id": ""}, ui.Text(randomID())),
		ui.El("code", ui.Attrs{"data-partial-region-process": ""}, ui.Text(processNonce)),
		ui.El("p", ui.Attrs{"data-partial-region-slug": ""}, ui.Text(slug)),
		ui.El("p", ui.Attrs{"data-partial-region-prop": ""}, ui.Text(props.Slug)),
		ui.El("p", ui.Attrs{"data-partial-region-path": ""}, ui.Text(c.Request.URL.Path)),
		counter.View(counter.Props{Label: "Partial request counter", Initial: 10})), nil
}
