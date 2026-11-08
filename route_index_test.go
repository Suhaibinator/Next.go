package nextgo

import (
	"fmt"
	"net/http"
	"strings"
	"testing"

	"github.com/Suhaibinator/Next.go/ui"
)

func TestCompiledDispatcherLargeTableAndSpecificity(t *testing.T) {
	routes := make([]Route, 0, 3005)
	for i := 0; i < 3000; i++ {
		name := fmt.Sprintf("r%05d", i)
		routes = append(routes, Route{Pattern: "/records/" + name + "/:id", Page: func(c *Context) (Page, error) {
			return Page{Body: ui.Text("selected=" + name + " value=" + c.Params["id"])}, nil
		}})
	}
	routes = append(routes,
		Route{Pattern: "/records/:group/static", Page: static("less specific parameter prefix")},
		Route{Pattern: "/records/r00001/edit", Page: static("exact static page")},
		Route{Pattern: "/files/*tail?", Page: func(c *Context) (Page, error) { return Page{Body: ui.Text("tail=" + c.Params["tail"])}, nil }},
		Route{Pattern: "/records/new", API: func(w http.ResponseWriter, r *http.Request) { _, _ = w.Write([]byte("exact endpoint")) }},
	)
	app := testApp(t, Config{Routes: routes})
	for _, i := range []int{0, 1, 1500, 2999} {
		name := fmt.Sprintf("r%05d", i)
		for _, value := range []string{"a%20b", "%252F", "static"} {
			response := request(app, "/records/"+name+"/"+value, false)
			want := strings.ReplaceAll(strings.ReplaceAll(value, "%20", " "), "%252F", "%2F")
			if response.Code != 200 || !strings.Contains(response.Body.String(), "selected="+name+" value="+want) {
				t.Fatal(response.Code, response.Body.String())
			}
		}
	}
	for _, tc := range []struct{ path, want string }{{"/records/new", "exact endpoint"}, {"/records/r00001/edit", "exact static page"}, {"/records/other/static", "less specific parameter prefix"}, {"/files", "tail="}, {"/files/a%20b/c", "tail=a b/c"}} {
		response := request(app, tc.path, false)
		if response.Code != 200 || !strings.Contains(response.Body.String(), tc.want) {
			t.Fatal(tc, response.Code, response.Body.String())
		}
	}
	for _, path := range []string{"/records/r00000/%2F", "/files/%00", "/missing"} {
		response := request(app, path, false)
		if response.Code != 404 {
			t.Fatal(path, response.Code)
		}
	}
}
