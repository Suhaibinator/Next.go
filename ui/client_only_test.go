package ui

import (
	"context"
	"errors"
	"io"
	"strings"
	"testing"
)

func TestClientIslandEscapesFallbackAndPublicProps(t *testing.T) {
	html, err := HTML(ClientIsland("widget", map[string]string{"label": `"<&`}, El("p", Attrs{"title": `"<&`}, Text("Useful <fallback>"))))
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{`data-nextgo-client="1"`, `data-nextgo-island="widget"`, `data-nextgo-props="{&#34;label&#34;:&#34;\&#34;\u003c\u0026&#34;}"`, `title="&#34;&lt;&amp;"`, `Useful &lt;fallback&gt;`} {
		if !strings.Contains(html, want) {
			t.Fatalf("missing %q in %s", want, html)
		}
	}
	ordinary, err := HTML(Island("widget", nil, Text("SSR")))
	if err != nil || strings.Contains(ordinary, "data-nextgo-client") || !strings.Contains(ordinary, ">SSR</div>") {
		t.Fatal("ordinary island changed", ordinary, err)
	}
}

func TestClientIslandRejectsNestedFrameworkAndDeferredWork(t *testing.T) {
	calls := 0
	deferred := Defer("late", Text("wait"), func(context.Context) (Node, error) { calls++; return Text("unexpected"), nil })
	for _, fallback := range []Node{
		Island("nested", nil, Text("no")),
		ClientIsland("nested", nil, Text("no")),
		deferred,
		El("section", nil, Fragment(Text("before"), deferred)),
		El("p", Attrs{"data-nextgo-island": "nested"}),
		El("div", Attrs{"DATA-NEXTGO-key": "nested"}),
		El("template", nil, Text("no")),
		El("meta", Attrs{"name": "description"}),
		El("a", Attrs{"href": "javascript:alert(1)"}),
	} {
		if html, err := HTML(ClientIsland("outer", nil, fallback)); err == nil || html != "" {
			t.Fatalf("invalid fallback emitted markup: %q %v", html, err)
		}
	}
	if calls != 0 {
		t.Fatal("fallback executed deferred loader", calls)
	}
}

func TestClientIslandBoundsFallbackBeforePublishingBoundary(t *testing.T) {
	for _, fallback := range []Node{
		Text(strings.Repeat("x", 64<<10+1)),
		nodeFunc(func(w io.Writer) error { _, err := io.WriteString(w, strings.Repeat("x", 64<<10+1)); return err }),
	} {
		if html, err := HTML(ClientIsland("large", nil, fallback)); err == nil || html != "" {
			t.Fatal("oversized fallback was published", len(html), err)
		}
	}
	if _, err := HTML(ClientIsland("limit", nil, Text(strings.Repeat("x", 64<<10)))); err != nil {
		t.Fatal("exact limit rejected", err)
	}
	for _, n := range []Node{ClientIsland("", nil, Text("fallback")), ClientIsland("x", nil, nil), ClientIsland("x", make(chan int), Text("fallback"))} {
		if _, err := HTML(n); err == nil {
			t.Fatal("invalid declaration accepted")
		}
	}
}

func TestClientIslandPreservesRenderingContextAndFailure(t *testing.T) {
	type key struct{}
	ctx := context.WithValue(context.Background(), key{}, "request")
	failure := errors.New("fallback failed")
	fallback := nodeFunc(func(w io.Writer) error {
		if RenderContext(w).Value(key{}) != "request" {
			t.Fatal("fallback lost request context")
		}
		return failure
	})
	if html, err := HTMLContext(ctx, ClientIsland("x", nil, fallback)); !errors.Is(err, failure) || html != "" {
		t.Fatal(html, err)
	}
}

func TestClientIslandBoundsTypedFallbackElementsBeforePublishing(t *testing.T) {
	children := make([]Node, 2001)
	for i := range children {
		children[i] = El("i", nil)
	}
	if _, err := HTML(ClientIsland("limit", nil, Fragment(children[:2000]...))); err != nil {
		t.Fatal("element limit rejected", err)
	}
	if html, err := HTML(ClientIsland("large", nil, Fragment(children...))); err == nil || html != "" {
		t.Fatal("oversized typed tree published a boundary", err)
	}
}
