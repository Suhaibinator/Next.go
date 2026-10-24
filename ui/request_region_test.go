package ui

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strings"
	"sync/atomic"
	"testing"
)

func TestRequestRegionCaptureReusesStaticWorkAndBindsCurrentRequest(t *testing.T) {
	type requestKey struct{}
	props := map[string]string{"label": "public <props>"}
	var staticCalls, resolverCalls atomic.Int32
	static := nodeFunc(func(w io.Writer) error {
		staticCalls.Add(1)
		_, err := io.WriteString(w, "<h1>Built once</h1>")
		return err
	})
	shell, err := CaptureShell(context.Background(), El("main", nil, static, RequestRegion("account", "profile", props, El("p", nil, Text("Loading <profile>"))), Text("footer")))
	if err != nil || staticCalls.Load() != 1 || len(shell.Regions) != 1 {
		t.Fatal(shell, err, staticCalls.Load())
	}
	if shell.HTML != `<main><h1>Built once</h1><div aria-busy="true" data-nextgo-boundary="account"><p>Loading &lt;profile&gt;</p></div>footer</main>` {
		t.Fatal(shell.HTML)
	}
	spec := shell.Regions[0]
	if spec.ID != "account" || spec.Region != "profile" || shell.HTML[spec.Start:spec.End] != `<div aria-busy="true" data-nextgo-boundary="account"><p>Loading &lt;profile&gt;</p></div>` {
		t.Fatal(spec)
	}
	props["label"] = "mutated after build"
	restored, err := RestoreShell(shell)
	if err != nil {
		t.Fatal(err)
	}
	for _, user := range []string{"Alice", "Bob", "Carol"} {
		ctx := context.WithValue(context.Background(), requestKey{}, user)
		ctx = WithRegionResolver(ctx, func(work context.Context, name string, raw json.RawMessage) (Node, error) {
			resolverCalls.Add(1)
			var p map[string]string
			if err := json.Unmarshal(raw, &p); err != nil {
				return nil, err
			}
			if name != "profile" || p["label"] != "public <props>" {
				t.Error(name, p)
			}
			raw[0] = '!' // Each callback owns its props; later requests remain valid.
			return El("strong", nil, Text(work.Value(requestKey{}).(string))), nil
		})
		html, err := HTMLContext(ctx, restored)
		if err != nil || html != "<main><h1>Built once</h1><strong>"+user+"</strong>footer</main>" {
			t.Fatal(html, err)
		}
	}
	if staticCalls.Load() != 1 || resolverCalls.Load() != 3 {
		t.Fatal("static application code reran", staticCalls.Load(), resolverCalls.Load())
	}
}

func TestRequestRegionRestoresStreamingShellAndRuntimeNestedWork(t *testing.T) {
	shell, err := CaptureShell(context.Background(), Fragment(Text("before"), RequestRegion("parent", "outer", nil, Text("public fallback")), Text("after")))
	if err != nil {
		t.Fatal(err)
	}
	restored, err := RestoreShell(shell)
	if err != nil {
		t.Fatal(err)
	}
	limiter, _ := NewStreamLimiter(1, 1)
	ctx, release, err := limiter.Begin(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	defer release()
	var calls atomic.Int32
	ctx = WithRegionResolver(ctx, func(_ context.Context, name string, props json.RawMessage) (Node, error) {
		calls.Add(1)
		if name == "outer" {
			return Fragment(Text("outer result"), RequestRegion("child", "inner", map[string]int{"number": 2}, Text("inner pending")), Defer("ordinary", Text("ordinary pending"), func(context.Context) (Node, error) { return Text("ordinary ready"), nil })), nil
		}
		if name != "inner" || string(props) != `{"number":2}` {
			t.Error(name, string(props))
		}
		return Text("inner ready"), nil
	})
	events := []StreamEvent{}
	if err := RenderEvents(ctx, restored, func(e StreamEvent) error { events = append(events, e); return nil }); err != nil {
		t.Fatal(err)
	}
	if len(events) != 4 || string(events[0].HTML) != shell.HTML || events[1].ID != "parent" {
		t.Fatal(events)
	}
	loaded := map[string]string{}
	for _, event := range events[1:] {
		if event.Err != nil {
			t.Fatal(event.Err)
		}
		loaded[event.ID] = string(event.HTML)
	}
	if !strings.Contains(loaded["parent"], `data-nextgo-boundary="child"`) || loaded["child"] != "inner ready" || loaded["ordinary"] != "ordinary ready" || calls.Load() != 2 {
		t.Fatal(loaded, calls.Load())
	}
}

func TestRequestRegionCaptureNeverExecutesDeferredClosures(t *testing.T) {
	var calls atomic.Int32
	loader := func(context.Context) (Node, error) { calls.Add(1); return Text("private data"), nil }
	cases := map[string]Node{
		"ordinary":        Defer("ordinary", nil, loader),
		"fallback-defer":  RequestRegion("account", "profile", nil, Defer("ordinary", nil, loader)),
		"fallback-region": RequestRegion("account", "profile", nil, RequestRegion("nested", "profile", nil, Text("nested"))),
		"duplicate-id":    Fragment(RequestRegion("same", "one", nil, nil), RequestRegion("same", "two", nil, nil)),
		"invalid-id":      RequestRegion("bad[id]", "profile", nil, nil),
		"invalid-region":  RequestRegion("good", "bad/name", nil, nil),
	}
	for name, node := range cases {
		t.Run(name, func(t *testing.T) {
			shell, err := CaptureShell(context.Background(), node)
			if err == nil || shell.HTML != "" || len(shell.Regions) != 0 {
				t.Fatal("invalid shell captured", shell, err)
			}
		})
	}
	if calls.Load() != 0 {
		t.Fatal("build executed a deferred application closure", calls.Load())
	}
	if _, err := HTML(RequestRegion("account", "profile", nil, nil)); err == nil {
		t.Fatal("request region rendered without resolver/capture")
	}
}

func TestRequestRegionCapturePropagatesCancellationAndRecoversPanic(t *testing.T) {
	canceled, cancel := context.WithCancel(context.Background())
	cancel()
	node := RequestRegion("account", "profile", nil, Text("pending"))
	if _, err := CaptureShell(canceled, node); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	if _, err := CaptureShell(nil, node); err == nil {
		t.Fatal("nil context accepted")
	}
	panicNode := nodeFunc(func(io.Writer) error { panic("controlled shell panic") })
	if shell, err := CaptureShell(context.Background(), panicNode); err == nil || shell.HTML != "" {
		t.Fatal(shell, err)
	}
	shell, err := CaptureShell(context.Background(), node)
	if err != nil {
		t.Fatal(err)
	}
	restored, err := RestoreShell(shell)
	if err != nil {
		t.Fatal(err)
	}
	ctx := WithRegionResolver(canceled, func(context.Context, string, json.RawMessage) (Node, error) {
		t.Error("canceled region called resolver")
		return nil, nil
	})
	if _, err := HTMLContext(ctx, restored); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
}

func TestRequestRegionRestoreRejectsMalformedDescriptorsBeforeRendering(t *testing.T) {
	shell, err := CaptureShell(context.Background(), Fragment(Text("before"), RequestRegion("one", "registered", nil, Text("fallback")), Text("after")))
	if err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"negative-start", "past-end", "reversed", "wrong-id", "wrong-wrapper", "empty-props", "bad-json", "duplicate-props", "invalid-utf8-props", "deep-props", "duplicate-id"} {
		t.Run(name, func(t *testing.T) {
			bad := CapturedShell{HTML: shell.HTML, Regions: append([]RegionSpec(nil), shell.Regions...)}
			switch name {
			case "negative-start":
				bad.Regions[0].Start = -1
			case "past-end":
				bad.Regions[0].End = len(shell.HTML) + 1
			case "reversed":
				bad.Regions[0].Start = bad.Regions[0].End + 1
			case "wrong-id":
				bad.Regions[0].ID = "different"
			case "wrong-wrapper":
				bad.HTML = strings.Replace(bad.HTML, "aria-busy=\"true\"", "aria-busy=\"nope\"", 1)
			case "empty-props":
				bad.Regions[0].Props = nil
			case "bad-json":
				bad.Regions[0].Props = json.RawMessage(`{"missing":`)
			case "duplicate-props":
				bad.Regions[0].Props = json.RawMessage(`{"x":1,"x":2}`)
			case "invalid-utf8-props":
				bad.Regions[0].Props = []byte{'"', 0xff, '"'}
			case "deep-props":
				bad.Regions[0].Props = json.RawMessage(strings.Repeat("[", 66) + "0" + strings.Repeat("]", 66))
			case "duplicate-id":
				bad.Regions = append(bad.Regions, bad.Regions[0])
			}
			if node, err := RestoreShell(bad); err == nil || node != nil {
				t.Fatal("malformed compiled shell adopted", name, err)
			}
		})
	}
}

func TestRequestRegionCaptureAndRestoreApplyResourceBounds(t *testing.T) {
	tests := map[string]Node{
		"shell-bytes": Text(strings.Repeat("x", MaxShellBytes+1)),
		"props-bytes": RequestRegion("one", "registered", strings.Repeat("x", MaxRegionPropsBytes), nil),
	}
	many := make([]Node, MaxShellRegions+1)
	for i := range many {
		many[i] = RequestRegion(fmt.Sprintf("hole%d", i), "registered", nil, nil)
	}
	tests["holes"] = Fragment(many...)
	props := json.RawMessage(`"` + strings.Repeat("x", MaxRegionPropsBytes-2) + `"`)
	aggregate := make([]Node, 17)
	for i := range aggregate {
		aggregate[i] = RequestRegion(fmt.Sprintf("hole%d", i), "registered", props, nil)
	}
	tests["aggregate-props"] = Fragment(aggregate...)
	for name, node := range tests {
		t.Run(name, func(t *testing.T) {
			if _, err := CaptureShell(context.Background(), node); err == nil {
				t.Fatal("unbounded shell accepted")
			}
		})
	}
	if _, err := RestoreShell(CapturedShell{HTML: strings.Repeat("x", MaxShellBytes+1)}); err == nil {
		t.Fatal("unbounded restore accepted")
	}
}

func TestRequestRegionRestoreOwnsMutableDescriptorData(t *testing.T) {
	shell, err := CaptureShell(context.Background(), RequestRegion("one", "registered", map[string]int{"n": 1}, Text("pending")))
	if err != nil {
		t.Fatal(err)
	}
	restored, err := RestoreShell(shell)
	if err != nil {
		t.Fatal(err)
	}
	shell.Regions[0].Region = "mutated"
	shell.Regions[0].Start = -1
	shell.Regions[0].Props[0] = '!'
	ctx := WithRegionResolver(context.Background(), func(_ context.Context, name string, props json.RawMessage) (Node, error) {
		if name != "registered" || string(props) != `{"n":1}` {
			t.Error("caller mutated restored shell", name, string(props))
		}
		return Text("owned"), nil
	})
	if html, err := HTMLContext(ctx, restored); err != nil || html != "owned" {
		t.Fatal(html, err)
	}
}
