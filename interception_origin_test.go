package nextgo

import (
	"github.com/Suhaibinator/Next.go/ui"
	"strings"
	"testing"
)

func TestInterceptedFromIsScopedToInterceptionAndPreservesSelectedQuery(t *testing.T) {
	fixture, _, _ := interceptionFixture(t)
	routes := fixture.routes
	routes[0].Slots[0].Intercepts[0].Page = func(c *Context) (Page, error) {
		from, ok := c.InterceptedFrom()
		if !ok || from != "/gallery/one?q=source" {
			t.Fatal(from, ok)
		}
		return Page{Body: ui.Text("return to " + from)}, nil
	}
	routes[1].Page = func(c *Context) (Page, error) {
		if from, ok := c.InterceptedFrom(); ok || from != "" {
			t.Fatal("standalone page inherited an origin", from, ok)
		}
		return Page{Body: ui.Text("standalone")}, nil
	}
	app := testApp(t, Config{Routes: routes})
	carrier := nestedCarrier(app, request(app, "/gallery/one?q=source", false).Body.String())
	response := slotNavigate(t, app, "/photos/one/42?q=photo", carrier, false)
	if response.Code != 200 || !strings.Contains(response.Body.String(), "return to /gallery/one?q=source") {
		t.Fatal(response.Code, response.Body.String())
	}
	if direct := request(app, "/photos/one/42", false); direct.Code != 200 {
		t.Fatal(direct.Code)
	}
}
