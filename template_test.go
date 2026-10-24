package nextgo

import (
	"github.com/Suhaibinator/Next.go/ui"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestTemplateKeysRespectSegmentDepthAndIgnoreQueries(t *testing.T) {
	for _, tc := range []struct{ pattern, path, want string }{
		{"/", "/", "/"}, {"/", "/posts/a", "/posts"}, {"/posts", "/posts", "/posts"}, {"/posts", "/posts/a/details", "/posts/a"}, {"/posts/:slug", "/posts/a/details/more", "/posts/a/details"}, {"/docs/*path", "/docs/a/b/c", "/docs/a/b/c"}, {"/docs/*path?", "/docs", "/docs"},
	} {
		if got := templateKey(tc.pattern, tc.path); got != tc.want {
			t.Errorf("%s %s =%s want%s", tc.pattern, tc.path, got, tc.want)
		}
	}
	wrapper := Template("posts", "/posts", func(_ *Context, body ui.Node) (ui.Node, error) {
		return ui.El("section", ui.Attrs{"data-template": ""}, body), nil
	})
	var previous string
	for _, path := range []string{"/posts/a/deep?q=1", "/posts/a/deeper?q=2"} {
		c := &Context{Request: httptest.NewRequest("GET", path, nil)}
		node, err := wrapper(c, ui.Text("body"))
		if err != nil {
			t.Fatal(err)
		}
		html, err := ui.HTML(node)
		if err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(html, `data-nextgo-layout="template:posts:/posts/a"`) || !strings.Contains(html, `data-nextgo-outlet="template:posts:/posts/a"`) {
			t.Fatal(html)
		}
		if previous != "" && previous != html {
			t.Fatal("deep/query change altered key")
		}
		previous = html
	}
}

func TestTemplateLayoutNestingAndInvalidConfiguration(t *testing.T) {
	layout := func(_ *Context, body ui.Node) (ui.Node, error) { return ui.El("main", nil, body), nil }
	template := Template("app", "/", func(_ *Context, body ui.Node) (ui.Node, error) { return ui.El("section", nil, body), nil })
	a := testApp(t, Config{Routes: []Route{{Pattern: "/about", Page: static("body"), Layouts: []LayoutFunc{layout, template}}}})
	html := request(a, "/about", false).Body.String()
	if !strings.Contains(html, `<main><nextgo-layout data-nextgo-layout="template:app:/about"><section><nextgo-outlet`) {
		t.Fatal(html)
	}
	for _, wrapper := range []LayoutFunc{Template("", "/", layout), Template("app", "bad", layout), Template("app", "/", nil)} {
		if _, err := wrapper(&Context{Request: httptest.NewRequest("GET", "/", nil)}, ui.Text("x")); err == nil {
			t.Fatal("invalid template accepted")
		}
	}
}
