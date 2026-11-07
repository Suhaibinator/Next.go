//go:build js && wasm && !nextgo_module

package client

import (
	"errors"
	"github.com/Suhaibinator/Next.go/internal/navigation"
	"syscall/js"
	"testing"
)

// The shared ownership DOM double does not parse HTML. These tests exercise
// traversal and native-association policy, separately from real browser cases.
func TestMergeNodePreflightBoundsQueuedWidthAndDepthBeforeSelectors(t *testing.T) {
	fixture := clientOnlyDOM(t)
	root := fixture.Call("make", "div")
	for i := 0; i < 8; i++ {
		root.Call("appendChild", fixture.Call("make", "span"))
	}
	if count, err := mergeBoundedNodes(root, 8); !errors.Is(err, errRetainedCapacity) || count <= 8 {
		t.Fatal("queued width escaped admission", count, err)
	}
	deep := fixture.Call("make", "div")
	at := deep
	for i := 0; i < 129; i++ {
		next := fixture.Call("make", "div")
		at.Call("appendChild", next)
		at = next
	}
	if _, err := mergeBoundedNodes(deep, retainedViewNodes); !errors.Is(err, errRetainedCapacity) {
		t.Fatal("deep traversal escaped admission", err)
	}
	if count, err := mergeBoundedNodes(fixture.Call("make", "div"), 1); err != nil || count != 1 {
		t.Fatal("bounded ordinary root refused", count, err)
	}
}
func TestMergeClientOwnershipRejectsNativeCrossBoundaryAssociations(t *testing.T) {
	fixture := clientOnlyDOM(t)
	cases := []struct{ tag, attr, value string }{
		{"button", "is", "x-demo"}, {"input", "id", "draft"}, {"input", "form", "outside"}, {"input", "type", "radio"},
		{"label", "for", "outside"}, {"input", "list", "outside"}, {"div", "aria-labelledby", "outside"},
		{"button", "popovertarget", "outside"}, {"details", "name", "exclusive"}, {"a", "name", "fragment"},
		{"input", "autofocus", ""}, {"form", "data-safe", ""}, {"x-editor", "data-safe", ""},
	}
	for _, item := range cases {
		root := fixture.Call("make", "div")
		child := fixture.Call("make", item.tag)
		child.Call("setAttribute", item.attr, item.value)
		root.Call("appendChild", child)
		if mergeClientTreeSafe(root) {
			t.Fatalf("unsupported child admitted: %s[%s]", item.tag, item.attr)
		}
		if mergeClientTreeSafe(child) {
			t.Fatalf("unsupported owner admitted: %s[%s]", item.tag, item.attr)
		}
	}
	root := fixture.Call("make", "div")
	input := fixture.Call("make", "input")
	input.Call("setAttribute", "type", "file")
	root.Call("appendChild", input)
	root.Call("appendChild", fixture.Call("make", "details"))
	root.Call("appendChild", fixture.Call("make", "a"))
	if !mergeClientTreeSafe(root) {
		t.Fatal("safe stationary native controls refused")
	}
	root.Set("shadowRoot", fixture.Call("make", "div"))
	if mergeClientTreeSafe(root) {
		t.Fatal("live shadow root admitted")
	}
}
func TestMergeClientHelperRefusesQueuedExcessBeforeAllocatingSelectorResult(t *testing.T) {
	root := js.Global().Get("Object").New()
	children := js.Global().Get("Object").New()
	children.Set("length", retainedViewNodes+1)
	root.Set("childNodes", children)
	queried := false
	query := js.FuncOf(func(js.Value, []js.Value) any { queried = true; return js.Global().Get("Array").New() })
	defer query.Release()
	root.Set("querySelectorAll", query)
	if mergeClientTreeSafe(root) || queried {
		t.Fatal("oversized late Mount tree reached selector allocation")
	}
}
func TestMergeColdAndServerSkeletonRefuseCustomizedBuiltinsAndLiveShadow(t *testing.T) {
	fixture := clientOnlyDOM(t)
	oldDoc := doc
	defer func() { doc = oldDoc }()
	app := fixture.Call("make", "div")
	app.Call("setAttribute", "id", "nextgo-app")
	app.Set("_connected", true)
	body := fixture.Call("make", "body")
	body.Call("appendChild", app)
	document := js.Global().Get("Object").New()
	document.Set("body", body)
	get := js.FuncOf(func(js.Value, []js.Value) any { return app })
	defer get.Release()
	document.Set("getElementById", get)
	doc = document
	child := fixture.Call("make", "button")
	app.Call("appendChild", child)
	child.Call("setAttribute", "is", "x-demo")
	if mergeColdBoundarySafe(child) == nil || validateMergeSkeleton(app, nil) == nil {
		t.Fatal("customized built-in bypassed cold/server guard")
	}
	child.Call("removeAttribute", "is")
	child.Set("shadowRoot", fixture.Call("make", "div"))
	if mergeColdBoundarySafe(child) == nil || validateMergeSkeleton(app, nil) == nil {
		t.Fatal("live shadow bypassed cold/server guard")
	}
}
func TestMergePreparedCarrierRejectsReaderChangedAnchorIdentity(t *testing.T) {
	fixture := clientOnlyDOM(t)
	old, fresh := fixture.Call("make", "main"), fixture.Call("make", "main")
	a, b := fixture.Call("make", "div"), fixture.Call("make", "div")
	old.Call("appendChild", a)
	fresh.Call("appendChild", b)
	rootsA, rootsB := map[string]js.Value{"a": a}, map[string]js.Value{"a": b}
	carrier, err := buildMergeCarrier(old, fresh, rootsA, rootsB, map[string]*mergeBoundary{"a": {}})
	if err != nil {
		t.Fatal(err)
	}
	if !mergeCarrierStillExact(carrier, rootsA, rootsB) {
		t.Fatal("unchanged prepared carrier refused")
	}
	detached := fixture.Call("make", "aside")
	detached.Call("appendChild", a)
	if mergeCarrierStillExact(carrier, rootsA, rootsB) {
		t.Fatal("reader changed anchor ownership but old plan survived")
	}
}

func TestMergeFinalPlanRejectsReplacedApplicationEvenWithoutRetainedCandidates(t *testing.T) {
	fixture := clientOnlyDOM(t)
	oldDoc := doc
	defer func() { doc = oldDoc }()
	old, fresh, replacement := fixture.Call("make", "div"), fixture.Call("make", "body"), fixture.Call("make", "div")
	parent := fixture.Call("make", "body")
	parent.Set("_connected", true)
	parent.Call("appendChild", old)
	document := js.Global().Get("Object").New()
	get := js.FuncOf(func(js.Value, []js.Value) any { return replacement })
	defer get.Release()
	document.Set("getElementById", get)
	doc = document
	plan := &mergeDOMPlan{tree: &mergeCarrier{old: old, fresh: fresh}, parent: parent}
	if err := validatePreparedMergePlan(plan, envelope{}); !errors.Is(err, errMergeOwnership) {
		t.Fatal("old app plan survived actual root replacement", err)
	}
}

func TestMergeUnusedLazyRegistrationKeepsIndependentRootsAndLateLazyOwnershipForcesCold(t *testing.T) {
	fixture := clientOnlyDOM(t)
	oldModules, oldDoc, oldManifest, oldOffline := lazyModules, doc, routeManifest, documentOffline
	t.Cleanup(func() { lazyModules, doc, routeManifest, documentOffline = oldModules, oldDoc, oldManifest, oldOffline })
	lazyModules = map[string]*lazyState{"lazyordinary": {}}
	app, fresh := fixture.Get("outer"), fixture.Call("make", "body")
	app.Call("setAttribute", "id", "nextgo-app")
	body := fixture.Call("make", "body")
	body.Set("_connected", true)
	body.Call("appendChild", app)
	doc = fixture.Get("owner")
	doc.Set("body", body)
	currentApp := app
	get := js.FuncOf(func(js.Value, []js.Value) any { return currentApp })
	t.Cleanup(get.Release)
	doc.Set("getElementById", get)
	routeManifest = navigation.Manifest{RetainRoutes: true, MergeVersion: navigation.MergeVersion}
	documentOffline = nil
	if mergeContainsLazyBoundary(app) || mergeContainsLazyBoundary(fresh) {
		t.Fatal("unused generated lazy registration disabled independent ordinary roots")
	}
	if !devRefreshBuild && !mergeRuntimeEnabled() {
		t.Fatal("unused generated lazy registration disabled production merge runtime")
	}
	lazy := fixture.Call("make", "div")
	lazy.Call("setAttribute", "data-nextgo-island", "lazyordinary")
	fresh.Call("appendChild", lazy)
	if !mergeContainsLazyBoundary(fresh) || !mergeContainsLazyBoundary(lazy) {
		t.Fatal("actual incoming lazy boundary escaped cold policy")
	}
	plan := &mergeDOMPlan{tree: &mergeCarrier{old: app, fresh: fresh}, parent: body}
	// A late lazy root is a cold policy refusal only while the exact owning
	// application is still live. Ownership loss must abort before cold fallback.
	currentApp = fixture.Call("make", "aside")
	if err := validatePreparedMergePlan(plan, envelope{}); !errors.Is(err, errMergeOwnership) {
		t.Fatal("lazy refusal masked replaced application ownership", err)
	}
	currentApp = app
	if err := validatePreparedMergePlan(plan, envelope{}); !errors.Is(err, errMergeUnsupported) {
		t.Fatal("reader-added incoming lazy root escaped final policy", err)
	}
	app.Call("appendChild", lazy)
	if err := validatePreparedMergePlan(plan, envelope{}); !errors.Is(err, errMergeUnsupported) {
		t.Fatal("reader-added current lazy root escaped final policy", err)
	}
	// Oversized trees must fail cold before selector allocation, including when
	// the added lazy root is not itself among the first bounded descendants.
	tooWide := js.Global().Get("Object").New()
	children := js.Global().Get("Object").New()
	children.Set("length", retainedViewNodes+1)
	tooWide.Set("childNodes", children)
	queried := false
	query := js.FuncOf(func(js.Value, []js.Value) any { queried = true; return js.Global().Get("Array").New() })
	t.Cleanup(query.Release)
	tooWide.Set("querySelectorAll", query)
	if !mergeContainsLazyBoundary(tooWide) || queried {
		t.Fatal("lazy eligibility selector allocated beyond bounded tree")
	}
	if !mergeContainsLazyBoundary(js.Null()) {
		t.Fatal("invalid lazy eligibility root admitted")
	}
}
