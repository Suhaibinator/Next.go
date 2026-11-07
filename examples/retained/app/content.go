//go:build !js

package app

import (
	"strings"

	nextgo "github.com/Suhaibinator/Next.go"
	"github.com/Suhaibinator/Next.go/assets"
	"github.com/Suhaibinator/Next.go/examples/retained/components/retentionpanel"
	"github.com/Suhaibinator/Next.go/ui"
)

func Render(c *nextgo.Context, name string) (nextgo.Page, error) {
	if name != "a" && name != "b" && name != "c" && name != "d" && name != "ids" && name != "unsafe" && name != "owner" && name != "broken" {
		return nextgo.Page{}, nextgo.ErrNotFound
	}
	pageLoads.Add(1)
	if demoDenied(c.Request) {
		return nextgo.Page{}, nextgo.ErrForbidden
	}
	name = strings.ToUpper(name)
	navigation := []ui.Node{ui.El("p", nil, ui.Text("An experimental buffered-history slice. Changed server output still renders fresh."))}
	for _, path := range []string{"a", "b", "c", "d", "ids", "owner", "broken"} {
		navigation = append(navigation, ui.El("a", ui.Attrs{"href": "/" + path, "data-nextgo-prefetch": "false"}, ui.Text(strings.ToUpper(path))), ui.Text(" "))
	}
	for _, kind := range []string{"details", "anchor", "radio", "form", "form-association"} {
		navigation = append(navigation, ui.El("a", ui.Attrs{"href": "/unsafe?kind=" + kind, "data-nextgo-prefetch": "false"}, ui.Text("UNSAFE "+kind)), ui.Text(" "))
	}
	content := []ui.Node{
		ui.El("h1", nil, ui.Text("Page "+name+" · version "+demoCookie(c.Request, "retained-version"))),
		ui.El("nav", ui.Attrs{"aria-label": "Retained example"}, navigation...),
	}
	if name != "OWNER" && name != "BROKEN" {
		content = append(content, retentionpanel.View(retentionpanel.Props{Name: name, Initial: 2, RetireNavigate: name == "A" && c.Query.Get("retire") == "1", ResumeFail: name == "A" && c.Query.Get("resumeFail") == "1", CleanupPanic: name == "A" && c.Query.Get("cleanupPanic") == "1"}))
	}
	if name == "IDS" {
		content = append(content, ui.El("label", ui.Attrs{"for": "example-author-id"}, ui.Text("Cold path with an author ID")), ui.El("input", ui.Attrs{"id": "example-author-id"}))
	}
	if name == "UNSAFE" {
		switch c.Query.Get("kind") {
		case "details":
			content = append(content, ui.El("details", ui.Attrs{"name": "retained-group"}, ui.El("summary", nil, ui.Text("Named details")), ui.Text("Native document-wide group")))
		case "anchor":
			content = append(content, ui.El("a", ui.Attrs{"name": "retained-anchor"}, ui.Text("Named anchor")))
		case "radio":
			content = append(content, ui.El("label", nil, ui.Text("Native radio"), ui.El("input", ui.Attrs{"type": "radio", "name": "retained-radio"})))
		case "form":
			content = append(content, ui.El("form", ui.Attrs{"action": "/unsafe", "method": "get"}, ui.El("input", ui.Attrs{"name": "draft"})))
		case "form-association":
			content = append(content, ui.El("input", ui.Attrs{"form": "outside-form", "aria-label": "External form owner"}))
		default:
			return nextgo.Page{}, nextgo.ErrNotFound
		}
	}
	for i := 0; i < 40; i++ {
		content = append(content, ui.El("p", nil, ui.Text("This ordinary server-rendered paragraph provides real window scroll space for history restoration.")))
	}
	body := ui.El("main", nil, content...)
	if name == "OWNER" || name == "BROKEN" {
		body = retentionpanel.WithContent(retentionpanel.Props{Name: name, Initial: 2, Fail: name == "BROKEN"}, content...)
	}
	return nextgo.Page{
		Title:     "Retained page " + name,
		Resources: []assets.Resource{assets.Stylesheet("/assets/retained.css")},
		Body:      body,
		Retention: &nextgo.RouteRetention{Partition: demoPartition(c.Request)},
	}, nil
}
