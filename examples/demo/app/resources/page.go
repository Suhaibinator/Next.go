//go:build !js

package resources

import (
	"time"

	nextgo "github.com/Suhaibinator/Next.go"
	"github.com/Suhaibinator/Next.go/client"
	"github.com/Suhaibinator/Next.go/examples/demo/components/resourcepanel"
	"github.com/Suhaibinator/Next.go/examples/demo/resourcedata"
	"github.com/Suhaibinator/Next.go/ui"
)

func Page(c *nextgo.Context) (nextgo.Page, error) {
	if err := c.RequireServer(); err != nil {
		return nextgo.Page{}, err
	}
	data, body, err := resourcedata.Load(c, 0)
	if err != nil {
		return nextgo.Page{}, err
	}
	endpoint := nextgo.MustURL(c, "/api/resources")
	props := resourcepanel.Props{Endpoint: endpoint, Initial: data, Seed: client.ResourceSeed{Key: "demo:public-resource:v1", Body: body, ExpiresUnixMilli: time.Now().Add(15 * time.Second).UnixMilli()}}
	return nextgo.Page{Title: "Scoped client resources — Next.go", Body: ui.El("article", ui.Attrs{"class": "prose"},
		ui.El("h1", nil, ui.Text("Scoped Go async resources")),
		ui.El("p", nil, ui.Text("Useful HTML arrives from a native server loader. Go/WASM explicitly adopts its bounded public seed. Buttons request reuse, HTTP loading, recoverable failure and retry; disposal cancels consumers.")),
		resourcepanel.View(props),
		ui.El("p", nil, ui.Text("This public demo is not a private session cache. Typed framework actions invalidate scoped resources at dispatch and settlement; custom API mutations must explicitly invalidate or refresh.")))}, nil
}
