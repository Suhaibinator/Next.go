package nextgo

import (
	"bytes"
	"context"
	"encoding/json"
	"html"
	"io"
	"net/http"
	"net/http/httptest"
	"regexp"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Suhaibinator/Next.go/internal/navigation"
	"github.com/Suhaibinator/Next.go/internal/renderowner"
	"github.com/Suhaibinator/Next.go/ui"
)

func mergeTestPage(c *Context) (Page, error) {
	return Page{Title: "Fresh " + c.Query.Get("q"), Retention: &RouteRetention{Partition: "public"}, Body: ui.Fragment(ui.Text("significant € "),
		ui.Keyed("section", "A", nil, ui.ClientIslandWithOptions("panel", map[string]string{"query": c.Query.Get("q")}, ui.El("p", nil, ui.Text("fresh "+c.Query.Get("q"))), ui.IslandOptions{Key: "editor", Schema: "v1"})),
		ui.El("aside", nil, ui.Text("Server "+c.Query.Get("q"))),
		ui.Keyed("section", "B", nil, ui.ClientIslandWithOptions("panel", map[string]string{"query": c.Query.Get("q")}, ui.Text("Second"), ui.IslandOptions{Key: "editor", Schema: "v1"})))}, nil
}
func TestMergeActualServerFreshQueryAndBootstrapBinding(t *testing.T) {
	a := testApp(t, Config{RetainRoutes: true, Routes: []Route{{Pattern: "/", Page: mergeTestPage}}})
	_, first := runtimeEnvelope(t, a, "/?q=a", nil)
	_, next := runtimeEnvelope(t, a, "/?q=b", nil)
	if first.Merge == nil || next.Merge == nil || first.Merge.Validate(first.HTML) != nil || next.Merge.Validate(next.HTML) != nil || len(next.Merge.Islands) != 2 || !strings.Contains(next.HTML, "Server b") || next.Title != "Fresh b" {
		t.Fatal(first, next)
	}
	for i := range first.Merge.Islands {
		before, _ := first.Merge.Address(first.Merge.Islands[i])
		after, _ := next.Merge.Address(next.Merge.Islands[i])
		if before != after {
			t.Fatal("query entered owner identity")
		}
	}
	w := httptest.NewRecorder()
	a.ServeHTTP(w, httptest.NewRequest("GET", "/?q=b", nil))
	match := regexp.MustCompile(`data-nextgo-merge="([^"]+)"`).FindStringSubmatch(w.Body.String())
	if w.Code != 200 || len(match) != 2 || w.Header().Get("Cache-Control") != "private, no-store" {
		t.Fatal(w.Code, w.Header(), w.Body.String())
	}
	d, err := navigation.ParseMergeDescriptor(html.UnescapeString(match[1]))
	if err != nil || d == nil || d.BodySHA256 != next.Merge.BodySHA256 {
		t.Fatal(d, err)
	}
	if strings.Contains(w.Body.String(), "merge-html") {
		t.Fatal("duplicate raw body bootstrap snapshot")
	}
}
func TestMergeSnapshotCannotFollowMutableRequestAndParentPrefixStopsAtLayout(t *testing.T) {
	makeSnapshot := func(path string) *routeMergeOrigin {
		request := httptest.NewRequest("GET", path, nil)
		c := &Context{Request: request, Params: map[string]string{"id": "forged"}}
		r := Route{Pattern: "/shop/products/:id", Layouts: []LayoutFunc{nil}, LayoutIDs: []string{"app/shop"}, LayoutBases: []string{"/shop"}}
		origin := snapshotRouteMerge(c, r)
		request.URL.Path = "/evil"
		c.Params["id"] = "later"
		return origin
	}
	a, b := makeSnapshot("/shop/products/1?q=a"), makeSnapshot("/shop/products/2?q=b")
	if a.page.ID == b.page.ID || a.layouts[0].ID != b.layouts[0].ID || a.page.ID == "" {
		t.Fatal("leaf values reset parent or author map stole identity", a, b)
	}
}

type mergeWriterProbe struct {
	want io.Writer
	seen bool
}

func (n *mergeWriterProbe) Render(w io.Writer) error {
	n.seen = w == n.want
	_, err := io.WriteString(w, "useful")
	return err
}
func TestMergeInertOriginForwardsExactWriter(t *testing.T) {
	var writer bytes.Buffer
	probe := &mergeWriterProbe{want: &writer}
	node := routeOwnerNode{frame: renderowner.Semantic("page", "/a"), child: probe}
	if err := node.Render(&writer); err != nil || !probe.seen || writer.String() != "useful" {
		t.Fatal(err, probe.seen, writer.String())
	}
	if err := (routeOwnerNode{}).Render(&writer); err != nil {
		t.Fatal(err)
	}
}
func TestMergeInertWrappersPreserveRealDeferAndShellCapture(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	release := make(chan struct{})
	var once sync.Once
	defer once.Do(func() { close(release) })
	node := routeOwnerNode{frame: renderowner.Semantic("page", "/a"), child: ui.Keyed("section", "row", nil, ui.Defer("later", ui.Text("loading first"), func(ctx context.Context) (ui.Node, error) {
		select {
		case <-release:
			return ui.Text("loaded later"), nil
		case <-ctx.Done():
			return nil, ctx.Err()
		}
	}))}
	var events []ui.StreamEvent
	err := ui.RenderEvents(ctx, node, func(event ui.StreamEvent) error {
		events = append(events, event)
		if event.ID == "" {
			if !strings.Contains(string(event.HTML), "loading first") {
				t.Error("lost progressive fallback")
			}
			once.Do(func() { close(release) })
		}
		return nil
	})
	if err != nil || len(events) != 2 || events[1].ID != "later" || string(events[1].HTML) != "loaded later" {
		t.Fatal(err, events)
	}
	shell, err := ui.CaptureShell(context.Background(), routeOwnerNode{frame: renderowner.Semantic("page", "/a"), child: ui.Keyed("section", "row", nil, ui.RequestRegion("private", "account", nil, ui.Text("public fallback")))})
	if err != nil || len(shell.Regions) != 1 || !strings.Contains(shell.HTML, "public fallback") {
		t.Fatal(shell, err)
	}
}
func TestMergeEligibilityNeverInstallsRecorderForCaptureOrUnsupportedResponses(t *testing.T) {
	a := testApp(t, Config{RetainRoutes: true, Routes: []Route{{Pattern: "/", Page: mergeTestPage}}})
	r := httptest.NewRequest("GET", "/", nil)
	p := Page{Retention: &RouteRetention{Partition: "public"}}
	if !a.mergeResponseEligible(r, p, 200) {
		t.Fatal("ordinary buffered render ineligible")
	}
	for _, request := range []*http.Request{r.WithContext(context.WithValue(r.Context(), offlineCaptureKey{}, &offlineCapture{})), r.WithContext(context.WithValue(r.Context(), prerenderCaptureKey{}, &prerenderCapture{}))} {
		if a.mergeResponseEligible(request, p, 200) {
			t.Fatal("capture recorder leaked")
		}
	}
	for _, status := range []int{401, 403, 404, 500} {
		if a.mergeResponseEligible(r, p, status) {
			t.Fatal(status)
		}
	}
	p.Stream = true
	if a.mergeResponseEligible(r, p, 200) {
		t.Fatal("stream recorder leaked")
	}
}
func TestMergeDefaultResponsesRemainWithoutWireExtension(t *testing.T) {
	a := testApp(t, Config{Routes: []Route{{Pattern: "/", Page: mergeTestPage}}})
	_, response := runtimeEnvelope(t, a, "/", nil)
	encoded, _ := json.Marshal(response)
	if response.Merge != nil || strings.Contains(string(encoded), `"merge"`) || strings.Contains(response.HTML, navigation.MergeNodeAttribute) {
		t.Fatal("default acquired merge authority", response)
	}
}

type mergeCountedProps struct{ calls *int }

func (p mergeCountedProps) MarshalJSON() ([]byte, error) {
	*p.calls++
	return []byte(`{"value":"public"}`), nil
}

type mergeFallbackContextKey struct{}
type mergeCountedFallback struct {
	calls             *int
	hidden, preserved *bool
}

func (n mergeCountedFallback) Render(w io.Writer) error {
	*n.calls++
	*n.hidden = *n.hidden && renderowner.RecorderFrom(ui.RenderContext(w)) == nil
	*n.preserved = *n.preserved && ui.RenderContext(w).Value(mergeFallbackContextKey{}) == "request-value"
	return ui.El("p", nil, ui.Text("Reusable allowed fallback")).Render(w)
}
func TestMergeActualServerSiblingFallbackContextIsolationAndSingleCounts(t *testing.T) {
	propsCalls, renderCalls := 0, 0
	hidden, preserved := true, true
	fallback := mergeCountedFallback{&renderCalls, &hidden, &preserved}
	a := testApp(t, Config{RetainRoutes: true, Routes: []Route{{Pattern: "/", Page: func(*Context) (Page, error) {
		return Page{Retention: &RouteRetention{Partition: "public"}, Body: ui.Fragment(
			ui.ClientIslandWithOptions("panel", mergeCountedProps{&propsCalls}, fallback, ui.IslandOptions{Key: "A", Schema: "v1"}),
			ui.ClientIslandWithOptions("panel", mergeCountedProps{&propsCalls}, fallback, ui.IslandOptions{Key: "B", Schema: "v1"}),
		)}, nil
	}}}})
	for _, navigate := range []bool{false, true} {
		beforeProps, beforeRender := propsCalls, renderCalls
		r := httptest.NewRequest("GET", "/", nil)
		r = r.WithContext(context.WithValue(r.Context(), mergeFallbackContextKey{}, "request-value"))
		if navigate {
			r.Header.Set("X-Nextgo-Navigate", "1")
		}
		w := httptest.NewRecorder()
		a.ServeHTTP(w, r)
		if w.Code != 200 || propsCalls-beforeProps != 2 || renderCalls-beforeRender != 2 || !hidden || !preserved {
			t.Fatal("server rerendered or leaked fallback owner context", navigate, w.Code, propsCalls, renderCalls, hidden, preserved, w.Body.String())
		}
		if navigate {
			var data Envelope
			if json.Unmarshal(w.Body.Bytes(), &data) != nil || data.Merge == nil || len(data.Merge.Islands) != 2 || len(data.Merge.Owners) != 1 || data.Merge.Validate(data.HTML) != nil {
				t.Fatal("valid sibling descriptors missing", w.Body.String())
			}
		}
	}
}

func TestMergeActualNestedLayoutAuthorityIsIndependentFromLegacyRetention(t *testing.T) {
	outer := func(c *Context, child ui.Node) (ui.Node, error) {
		return ui.El("section", ui.Attrs{"data-layout-proof": "outer"}, ui.Text("Outer "+c.Query.Get("q")), child), nil
	}
	inner := func(c *Context, child ui.Node) (ui.Node, error) {
		return ui.El("article", ui.Attrs{"data-layout-proof": "inner"}, ui.Text("Inner "+c.Query.Get("q")), child), nil
	}
	route := Route{Pattern: "/layouts/products/:id", Page: mergeTestPage, Layouts: []LayoutFunc{outer, inner}, LayoutIDs: []string{"app/layouts", "app/layouts/products"}, LayoutBases: []string{"/layouts", "/layouts/products"}}
	a := testApp(t, Config{RetainRoutes: true, Routes: []Route{route}})
	_, first := runtimeEnvelope(t, a, "/layouts/products/tea?q=a", nil)
	_, fresh := runtimeEnvelope(t, a, "/layouts/products/tea?q=b", nil)
	if first.Retention != nil || fresh.Retention != nil || first.Merge == nil || fresh.Merge == nil || fresh.Merge.Validate(fresh.HTML) != nil || !strings.Contains(fresh.HTML, "Outer b") || !strings.Contains(fresh.HTML, "Inner b") {
		t.Fatal("nested layout must emit new merge authority only", first, fresh)
	}
	for i := range first.Merge.Islands {
		before, _ := first.Merge.Address(first.Merge.Islands[i])
		after, _ := fresh.Merge.Address(fresh.Merge.Islands[i])
		if before != after {
			t.Fatal("layout query reset identity")
		}
	}
	w := httptest.NewRecorder()
	a.ServeHTTP(w, httptest.NewRequest("GET", "/layouts/products/tea?q=b", nil))
	if w.Code != 200 || w.Header().Get("Cache-Control") != "private, no-store" || strings.Contains(w.Body.String(), navigation.RetentionAttribute+"=") || !strings.Contains(w.Body.String(), navigation.MergeAttribute+"=") {
		t.Fatal("bootstrap layout authority/cache policy", w.Code, w.Header(), w.Body.String())
	}
	for _, bad := range []Route{
		{Pattern: route.Pattern, Page: mergeTestPage, Layouts: route.Layouts, LayoutBases: route.LayoutBases},
		{Pattern: route.Pattern, Page: mergeTestPage, Layouts: route.Layouts, LayoutIDs: route.LayoutIDs},
		{Pattern: route.Pattern, Page: mergeTestPage, Layouts: route.Layouts, LayoutIDs: []string{"same", "same"}, LayoutBases: route.LayoutBases},
		{Pattern: route.Pattern, Page: mergeTestPage, Layouts: route.Layouts, LayoutIDs: route.LayoutIDs, LayoutBases: []string{"/layouts/products", "/layouts"}},
	} {
		app := testApp(t, Config{RetainRoutes: true, Routes: []Route{bad}})
		_, cold := runtimeEnvelope(t, app, "/layouts/products/tea?q=b", nil)
		if cold.Merge != nil || cold.Retention != nil || strings.Contains(cold.HTML, navigation.MergeNodeAttribute) {
			t.Fatal("missing/ambiguous layout origin acquired authority", bad, cold)
		}
	}
}
