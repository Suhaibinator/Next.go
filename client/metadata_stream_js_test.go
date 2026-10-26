//go:build js && wasm

package client

import (
	"context"
	"errors"
	"strings"
	"syscall/js"
	"testing"

	"github.com/Suhaibinator/Next.go/internal/navigation"
)

const metadataFixtureOwner = "0123456789abcdef0123456789abcdef"

type metadataTestNode struct {
	value js.Value
	tag   string
	attrs map[string]string
}
type metadataFixture struct {
	t                                                       *testing.T
	functions                                               []js.Func
	head                                                    js.Value
	children                                                []*metadataTestNode
	title, description, owned, resource, structured, legacy *metadataTestNode
	active                                                  bool
	onCreate                                                func()
}

func (f *metadataFixture) fn(body func(js.Value, []js.Value) any) js.Func {
	fn := js.FuncOf(body)
	f.functions = append(f.functions, fn)
	return fn
}
func metadataNodeValues(nodes []*metadataTestNode) []any {
	out := make([]any, len(nodes))
	for i, node := range nodes {
		out[i] = node.value
	}
	return out
}
func (f *metadataFixture) syncHead() {
	f.head.Set("childNodes", metadataNodeValues(f.children))
	for i, node := range f.children {
		next := js.Null()
		if i+1 < len(f.children) {
			next = f.children[i+1].value
		}
		node.value.Set("nextSibling", next)
	}
}
func (f *metadataFixture) node(tag string, attrs map[string]string) *metadataTestNode {
	n := &metadataTestNode{value: js.Global().Get("Object").New(), tag: tag, attrs: attrs}
	n.value.Set("tagName", strings.ToUpper(tag))
	n.value.Set("parentNode", js.Null())
	n.value.Set("textContent", "")
	syncAttrs := func() {
		var values []any
		for name, value := range n.attrs {
			values = append(values, map[string]any{"name": name, "value": value})
		}
		n.value.Set("attributes", values)
	}
	syncAttrs()
	n.value.Set("setAttribute", f.fn(func(_ js.Value, args []js.Value) any {
		n.attrs[args[0].String()] = args[1].String()
		syncAttrs()
		return nil
	}))
	n.value.Set("getAttribute", f.fn(func(_ js.Value, args []js.Value) any {
		value, ok := n.attrs[args[0].String()]
		if !ok {
			return js.Null()
		}
		return value
	}))
	n.value.Set("remove", f.fn(func(js.Value, []js.Value) any {
		for i, node := range f.children {
			if node == n {
				f.children = append(f.children[:i], f.children[i+1:]...)
				break
			}
		}
		n.value.Set("parentNode", js.Null())
		f.syncHead()
		return nil
	}))
	return n
}
func newMetadataFixture(t *testing.T) *metadataFixture {
	t.Helper()
	f := &metadataFixture{t: t, active: true, head: js.Global().Get("Object").New()}
	oldDoc, oldWin := doc, win
	t.Cleanup(func() {
		doc, win = oldDoc, oldWin
		for _, fn := range f.functions {
			fn.Release()
		}
	})
	f.title = f.node("title", map[string]string{})
	f.title.value.Set("textContent", "Fallback")
	f.description = f.node("meta", map[string]string{"name": "description", "content": "Fallback description"})
	f.owned = f.node("link", map[string]string{"rel": "canonical", "href": "https://site.test/fallback", "data-nextgo-head": "", "data-nextgo-metadata-owned": metadataFixtureOwner})
	// Synthetic lookalikes stress exact ownership; native resources currently emit CSS/font links.
	f.resource = f.node("link", map[string]string{"rel": "canonical", "href": "https://resource.test/preserve", "data-nextgo-head": ""})
	f.structured = f.node("script", map[string]string{"type": "application/ld+json", "data-nextgo-head": ""})
	f.legacy = f.node("meta", map[string]string{"name": "robots", "content": "noindex", "data-nextgo-meta": ""})
	f.children = []*metadataTestNode{f.title, f.description, f.owned, f.resource, f.structured, f.legacy}
	for _, node := range f.children {
		node.value.Set("parentNode", f.head)
	}
	f.syncHead()
	f.head.Set("querySelectorAll", f.fn(func(_ js.Value, args []js.Value) any {
		var selected []*metadataTestNode
		for _, node := range f.children {
			switch args[0].String() {
			case "title":
				if node.tag == "title" {
					selected = append(selected, node)
				}
			case `meta[name="description"]`:
				if node.tag == "meta" && node.attrs["name"] == "description" {
					selected = append(selected, node)
				}
			case "[data-nextgo-metadata-owned]":
				if _, ok := node.attrs["data-nextgo-metadata-owned"]; ok {
					selected = append(selected, node)
				}
			}
		}
		return metadataNodeValues(selected)
	}))
	f.head.Set("appendChild", f.fn(func(_ js.Value, args []js.Value) any {
		fragment := args[0]
		list := fragment.Get("childNodes")
		for i := 0; i < list.Length(); i++ {
			value := list.Index(i)
			value.Set("parentNode", f.head)
			f.children = append(f.children, &metadataTestNode{value: value, tag: strings.ToLower(value.Get("tagName").String()), attrs: map[string]string{}})
		}
		f.syncHead()
		fragment.Set("childNodes", []any{})
		return fragment
	}))
	insertBefore := f.fn(func(_ js.Value, args []js.Value) any {
		fragment, anchor := args[0], args[1]
		position := len(f.children)
		if !anchor.IsNull() && !anchor.IsUndefined() {
			for i, node := range f.children {
				if node.value.Equal(anchor) {
					position = i
					break
				}
			}
		}
		list := fragment.Get("childNodes")
		var added []*metadataTestNode
		for i := 0; i < list.Length(); i++ {
			value := list.Index(i)
			value.Set("parentNode", f.head)
			added = append(added, &metadataTestNode{value: value, tag: strings.ToLower(value.Get("tagName").String()), attrs: map[string]string{}})
		}
		result := append([]*metadataTestNode{}, f.children[:position]...)
		result = append(result, added...)
		result = append(result, f.children[position:]...)
		f.children = result
		f.syncHead()
		fragment.Set("childNodes", []any{})
		return fragment
	})
	// The fake must reject detached anchors exactly as the DOM does. A native JS
	// wrapper throws before entering the Go callback, so failures propagate through
	// syscall/js rather than silently turning into append operations.
	f.head.Set("insertBefore", js.Global().Get("Function").New("insert", `return function(fragment, anchor) {
  if (anchor != null && anchor.parentNode !== this) throw new Error("NotFoundError");
  return insert(fragment, anchor);
 }`).Invoke(insertBefore))
	fragment := f.fn(func(js.Value, []js.Value) any {
		v := js.Global().Get("Object").New()
		var nodes []any
		v.Set("childNodes", []any{})
		v.Set("appendChild", f.fn(func(_ js.Value, args []js.Value) any {
			nodes = append(nodes, args[0])
			v.Set("childNodes", nodes)
			return args[0]
		}))
		return v
	})
	create := f.fn(func(_ js.Value, args []js.Value) any {
		if f.onCreate != nil {
			f.onCreate()
		}
		return f.node(args[0].String(), map[string]string{}).value
	})
	doc = js.ValueOf(map[string]any{"head": f.head, "baseURI": "https://site.test/", "createDocumentFragment": fragment, "createElement": create})
	win = js.ValueOf(map[string]any{"URL": js.Global().Get("URL")})
	return f
}
func (f *metadataFixture) plan() *metadataHead {
	return &metadataHead{owner: metadataFixtureOwner, nodes: []js.Value{f.owned.value}, title: f.title.value, description: f.description.value, active: func() bool { return f.active }}
}
func metadataFixtureFrame(t *testing.T, failed bool, elements []navigation.MetadataElement) navigation.StreamFrame {
	t.Helper()
	raw, err := navigation.EncodeMetadataPayload(navigation.MetadataPayload{Version: 1, Owner: metadataFixtureOwner, Title: "Final", Description: "Final description", Elements: elements}, failed)
	if err != nil {
		t.Fatal(err)
	}
	return navigation.StreamFrame{Version: 1, Type: "metadata", Metadata: raw, Failed: failed}
}
func TestMetadataTransactionPreservesExactResourceAndStructuredNodes(t *testing.T) {
	f := newMetadataFixture(t)
	p := f.plan()
	frame := metadataFixtureFrame(t, false, []navigation.MetadataElement{{Tag: "meta", Attrs: map[string]string{"name": "robots", "content": "index"}}, {Tag: "link", Attrs: map[string]string{"rel": "canonical", "href": "https://site.test/final"}}})
	if err := p.apply(frame); err != nil {
		t.Fatal(err)
	}
	if !p.completed || f.title.value.Get("textContent").String() != "Final" || f.description.attrs["content"] != "Final description" {
		t.Fatal("metadata did not commit")
	}
	if !f.owned.value.Get("parentNode").IsNull() {
		t.Fatal("old owned node retained")
	}
	for _, node := range []*metadataTestNode{f.resource, f.structured, f.legacy} {
		if !node.value.Get("parentNode").Equal(f.head) {
			t.Fatal("unowned node removed")
		}
	}
	if f.resource.attrs["href"] != "https://resource.test/preserve" || f.legacy.attrs["content"] != "noindex" {
		t.Fatal("resource/legacy metadata modified")
	}
	if err := p.apply(frame); err == nil {
		t.Fatal("duplicate terminal accepted")
	}
}
func TestMetadataTransactionFailureAndInvalidOutputKeepFallback(t *testing.T) {
	for _, name := range []string{"failed", "unsafe", "owner", "ownership", "stale", "reentrant"} {
		t.Run(name, func(t *testing.T) {
			f := newMetadataFixture(t)
			p := f.plan()
			frame := metadataFixtureFrame(t, name == "failed", []navigation.MetadataElement{{Tag: "meta", Attrs: map[string]string{"name": "robots", "content": "index"}}})
			switch name {
			case "unsafe":
				frame.Metadata = []byte(`{"version":1,"owner":"` + metadataFixtureOwner + `","title":"Final","description":"Final","elements":[{"tag":"script","attrs":{"src":"https://evil.test"}}]}`)
			case "owner":
				frame.Metadata = []byte(strings.Replace(string(frame.Metadata), metadataFixtureOwner, strings.Repeat("f", 32), 1))
			case "ownership":
				f.owned.value.Set("parentNode", js.Null())
			case "stale":
				f.active = false
			case "reentrant":
				f.onCreate = func() { f.active = false }
			}
			err := p.apply(frame)
			if name == "failed" {
				if err != nil || !p.completed {
					t.Fatal(err)
				}
			} else if err == nil {
				t.Fatal("invalid completion accepted")
			}
			if f.title.value.Get("textContent").String() != "Fallback" || f.description.attrs["content"] != "Fallback description" {
				t.Fatal("partial head mutation")
			}
			if name != "ownership" && !f.owned.value.Get("parentNode").Equal(f.head) {
				t.Fatal("fallback node removed")
			}
		})
	}
}
func TestMetadataNavigationProvenanceRejectsResourcesAndInvalidIndices(t *testing.T) {
	f := newMetadataFixture(t)
	data := envelope{Head: []headElement{{Tag: "link", Attrs: map[string]string{"rel": "stylesheet", "href": "/styles.css"}}}}
	for _, indices := range [][]int{{0}, {1}, {0, 0}} {
		if _, err := newNavigationMetadata(context.Background(), navigationID, metadataFixtureOwner, indices, data); err == nil {
			t.Fatal("accepted invalid SEO selection", indices)
		}
	}
	data.Head = []headElement{{Tag: "link", Attrs: map[string]string{"rel": "canonical", "href": "https://resource.test/preserve"}}, {Tag: "link", Attrs: map[string]string{"rel": "canonical", "href": "https://site.test/fallback"}}}
	plan, err := newNavigationMetadata(context.Background(), navigationID, metadataFixtureOwner, []int{1}, data)
	if err != nil {
		t.Fatal(err)
	}
	fragment := js.ValueOf(map[string]any{"childNodes": []any{f.resource.value, f.owned.value}})
	if err := plan.capture(fragment); err != nil {
		t.Fatal(err)
	}
	if len(plan.nodes) != 1 || !plan.nodes[0].Equal(f.owned.value) {
		t.Fatal("capture ignored exact provenance")
	}
	if _, marked := f.resource.attrs["data-nextgo-metadata-owned"]; marked {
		t.Fatal("resource inferred owned from canonical relation")
	}

}
func TestMetadataInitialGenerationAndNavigationInvalidateLateHead(t *testing.T) {
	f := newMetadataFixture(t)
	oldInitial, oldGeneration, oldStreaming, oldID, oldDraft := initialMetadata, initialMetadataGeneration, streamingActive, navigationID, draftFrozen
	t.Cleanup(func() {
		initialMetadata, initialMetadataGeneration, streamingActive, navigationID, draftFrozen = oldInitial, oldGeneration, oldStreaming, oldID, oldDraft
	})
	draftFrozen = false
	streamingActive = true
	attrs := map[string]string{"data-nextgo-metadata-version": "1", "data-nextgo-metadata-owner": metadataFixtureOwner, "data-nextgo-build": strings.Repeat("a", 20), "data-nextgo-configuration": strings.Repeat("b", 32)}
	body := js.ValueOf(map[string]any{"getAttribute": f.fn(func(_ js.Value, args []js.Value) any {
		v, ok := attrs[args[0].String()]
		if !ok {
			return js.Null()
		}
		return v
	})})
	doc.Set("body", body)
	if err := startInitialMetadata(); err != nil {
		t.Fatal(err)
	}
	p := initialMetadata
	if p == nil || !p.ownsHead() {
		t.Fatal("initial head not owned")
	}
	navigationID++
	if err := p.apply(metadataFixtureFrame(t, false, nil)); !errors.Is(err, context.Canceled) {
		t.Fatal("late initial metadata survived navigation", err)
	}
	stopInitialMetadata()
	if initialMetadata != nil || p.active() {
		t.Fatal("stopped generation remains active")
	}
}
func TestMetadataTemplateRequiresPhysicalCompleteMarker(t *testing.T) {
	f := newMetadataFixture(t)
	for _, tc := range []struct {
		name, token, ready, tag string
		kind                    int
		hidden, ok              bool
	}{{"complete", metadataFixtureOwner, metadataFixtureOwner, "SPAN", 1, true, true}, {"partial", metadataFixtureOwner, metadataFixtureOwner, "", 0, false, false}, {"text-between", metadataFixtureOwner, metadataFixtureOwner, "", 3, false, false}, {"wrong-owner", metadataFixtureOwner, strings.Repeat("f", 32), "SPAN", 1, true, false}, {"visible", metadataFixtureOwner, metadataFixtureOwner, "SPAN", 1, false, false}, {"wrong-tag", metadataFixtureOwner, metadataFixtureOwner, "DIV", 1, true, false}} {
		t.Run(tc.name, func(t *testing.T) {
			marker := js.Null()
			if tc.kind != 0 {
				marker = js.ValueOf(map[string]any{"nodeType": tc.kind, "tagName": tc.tag, "hasAttribute": f.fn(func(js.Value, []js.Value) any { return tc.hidden }), "getAttribute": f.fn(func(js.Value, []js.Value) any { return tc.ready })})
			}
			template := js.ValueOf(map[string]any{"nextSibling": marker, "getAttribute": f.fn(func(js.Value, []js.Value) any { return tc.token })})
			_, ok := completedMetadataTemplate(template)
			if ok != tc.ok {
				t.Fatal(ok, tc.ok)
			}
		})
	}
}

func TestMetadataCarrierTextChecksPrimitiveAndBothLengthBudgets(t *testing.T) {
	for _, tc := range []struct {
		value any
		valid bool
	}{{"safe 日本語", true}, {strings.Repeat("x", navigation.MaxMetadataBytes), true}, {strings.Repeat("x", navigation.MaxMetadataBytes+1), false}, {strings.Repeat("日", navigation.MaxMetadataBytes/3+1), false}, {nil, false}, {map[string]any{"length": 1}, false}} {
		got, err := metadataCarrierText(js.ValueOf(tc.value))
		if (err == nil) != tc.valid || tc.valid && got != tc.value.(string) {
			t.Fatal("invalid carrier length/type result", err)
		}
	}
}
func TestMetadataTransactionWithNoFallbackSEOUsesDescriptionAnchor(t *testing.T) {
	f := newMetadataFixture(t)
	f.owned.value.Call("remove")
	p := f.plan()
	p.nodes = nil
	frame := metadataFixtureFrame(t, false, []navigation.MetadataElement{{Tag: "meta", Attrs: map[string]string{"name": "robots", "content": "index"}}})
	if err := p.apply(frame); err != nil {
		t.Fatal(err)
	}
	if len(f.children) != 6 || !f.children[1].value.Equal(f.description.value) || !f.children[3].value.Equal(f.resource.value) || !f.children[4].value.Equal(f.structured.value) {
		t.Fatal("replacement moved resources or ignored framework description anchor")
	}
}
