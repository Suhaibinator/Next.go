//go:build !js

package restricted

import (
	nextgo "github.com/Suhaibinator/Next.go"
	"github.com/Suhaibinator/Next.go/examples/demo/access"
	"github.com/Suhaibinator/Next.go/examples/demo/state"
	"github.com/Suhaibinator/Next.go/seo"
	"github.com/Suhaibinator/Next.go/ui"
	"strconv"
)

func Page(c *nextgo.Context) (nextgo.Page, error) {
	if err := c.SetHeader("Cache-Control", "private, no-store"); err != nil {
		return nextgo.Page{}, err
	}
	report, err := access.ReadReport(c.Context, c.Request)
	if err != nil {
		return nextgo.Page{}, err
	}
	return nextgo.Page{Title: report.Title + " — Next.go", Metadata: seo.Metadata{Robots: "noindex"}, Body: ui.El("section", ui.Attrs{"class": "prose", "data-private-report": ""}, ui.El("p", ui.Attrs{"class": "eyebrow"}, ui.Text("SERVER ACCESS CHECK")), ui.El("h1", nil, ui.Text(report.Title)), ui.El("p", nil, ui.Text(report.Summary)), ui.El("p", ui.Attrs{"data-report-reviews": ""}, ui.Text(strconv.FormatInt(state.Reviews(), 10))), ui.El("form", ui.Attrs{"method": "post", "action": nextgo.MustURL(c, "/_nextgo/actions/review-report")}, ui.El("button", ui.Attrs{"type": "submit"}, ui.Text("Record report review"))))}, nil
}
