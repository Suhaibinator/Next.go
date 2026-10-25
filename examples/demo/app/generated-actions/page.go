//go:build !js

package generatedactions

import (
	"fmt"
	"strconv"

	nextgo "github.com/Suhaibinator/Next.go"
	"github.com/Suhaibinator/Next.go/examples/demo/actionrefs"
	"github.com/Suhaibinator/Next.go/examples/demo/actionstate"
	"github.com/Suhaibinator/Next.go/examples/demo/components/generatedaction"
	"github.com/Suhaibinator/Next.go/ui"
)

func Page(c *nextgo.Context) (nextgo.Page, error) {
	value, err := actionstate.Load(c.Context)
	if err != nil {
		return nextgo.Page{}, err
	}
	return nextgo.Page{Title: fmt.Sprintf("Generated actions revision %d — Next.go", value.Revision), Description: "A generated Go action reference shares a versioned contract with a server-only handler.", Meta: map[string]string{"generated-action-revision": strconv.FormatInt(value.Revision, 10)}, Body: ui.Fragment(
		ui.El("section", ui.Attrs{"class": "prose"}, ui.El("h1", nil, ui.Text("Generated Go actions")),
			ui.El("p", nil, ui.Text("The compiler discovers a shared contract and native handler, generates a typed browser reference, and registers its versioned endpoint. This public process-local counter resets on restart; production handlers must authorize callers and store data durably.")),
			ui.El("p", nil, ui.Text("Cached server snapshot revision: "), ui.El("output", ui.Attrs{"data-generated-ssr-revision": ""}, ui.Text(strconv.FormatInt(value.Revision, 10))), ui.Text("; loader execution: "), ui.El("output", ui.Attrs{"data-generated-ssr-load": ""}, ui.Text(strconv.FormatInt(value.Load, 10)))),
			ui.El("p", nil, ui.Text("Successful mutations declare tag and path revalidation. Refresh the server snapshot to see fresh HTML and metadata; validation and private failures leave confirmed data intact."))),
		generatedaction.View(generatedaction.Props{Count: value.Count, Revision: value.Revision, Action: nextgo.MustURL(c, actionrefs.GeneratedAddPath())}),
		ui.El("p", nil, ui.El("a", ui.Attrs{"href": nextgo.MustURL(c, "/features")}, ui.Text("Try existing manual actions"))))}, nil
}
