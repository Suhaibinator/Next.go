//go:build js && wasm

package client

import (
	"context"
	"fmt"
	"runtime"
	"strings"
	"syscall/js"
	"testing"
	"time"

	"github.com/Suhaibinator/Next.go/internal/navigation"
)

type viewportTestEnvironment struct {
	c                           *viewportController
	navigator, connection, root js.Value
	functions                   []js.Func
	workers                     []<-chan struct{}
	barriers                    []viewportTestBarrier
}

type viewportTestBarrier struct {
	stop chan struct{}
	done <-chan struct{}
}

func viewportTestFixture(t *testing.T) *viewportTestEnvironment {
	t.Helper()
	oldDoc, oldWin, oldURL, oldManifest, oldDraft, oldPending, oldEpoch := doc, win, currentURL, routeManifest, draftFrozen, prefetches, cacheEpoch
	f := &viewportTestEnvironment{}
	f.connection = js.ValueOf(map[string]any{"saveData": false, "effectiveType": "4g"})
	f.navigator = js.ValueOf(map[string]any{"onLine": true, "connection": f.connection})
	win = js.ValueOf(map[string]any{"URL": js.Global().Get("URL"), "WeakMap": js.Global().Get("WeakMap"), "location": map[string]any{"origin": "https://example.test"}, "navigator": f.navigator, "innerWidth": 1000, "innerHeight": 800})
	f.root = js.ValueOf(map[string]any{"isConnected": true})
	f.root.Set("contains", f.function(func(_ js.Value, args []js.Value) any { return args[0].Get("inRoot").Bool() }))
	doc = js.ValueOf(map[string]any{"visibilityState": "visible", "baseURI": "https://example.test/", "getElementById": f.function(func(js.Value, []js.Value) any { return f.root })})
	noop := f.function(func(js.Value, []js.Value) any { return nil })
	observer := js.ValueOf(map[string]any{"takeRecords": noop, "disconnect": noop, "unobserve": noop, "observe": noop})
	f.c = &viewportController{io: observer, mutations: observer, attempts: js.Global().Get("WeakMap").New(), wake: make(chan struct{}, 1), active: true}
	currentURL = "https://example.test/home"
	routeManifest = navigation.Manifest{}
	draftFrozen = false
	prefetches = map[string]*prefetchRequest{}
	cacheEpoch = 10
	t.Cleanup(func() {
		f.c.active = false
		f.c.disposed = true
		f.c.queue = nil
		deadline := time.NewTimer(time.Second)
		defer deadline.Stop()
		// Disposal releases callback barriers even if their entry wait failed.
		// Join their helpers before workers and before restoring fixture globals.
		for _, barrier := range f.barriers {
			close(barrier.stop)
		}
		for _, barrier := range f.barriers {
			select {
			case <-barrier.done:
			case <-deadline.C:
				t.Fatal("viewport fixture barrier helper did not physically exit")
			}
		}
		// Keep wake open while an already admitted iteration can still signal.
		// Every fixture-owned worker must exit before globals/functions change.
		for _, stopped := range f.workers {
			select {
			case <-stopped:
			case f.c.wake <- struct{}{}:
			case <-deadline.C:
				t.Fatal("viewport fixture worker did not accept shutdown")
			}
		}
		for _, stopped := range f.workers {
			select {
			case <-stopped:
			case <-deadline.C:
				t.Fatal("viewport fixture worker did not physically exit")
			}
		}
		close(f.c.wake)
		doc, win, currentURL, routeManifest, draftFrozen, prefetches, cacheEpoch = oldDoc, oldWin, oldURL, oldManifest, oldDraft, oldPending, oldEpoch
		for _, fn := range f.functions {
			fn.Release()
		}
	})
	return f
}
func (f *viewportTestEnvironment) rectangleBarrier(entered <-chan struct{}) <-chan struct{} {
	stop, done, release := make(chan struct{}), make(chan struct{}), make(chan struct{})
	f.barriers = append(f.barriers, viewportTestBarrier{stop: stop, done: done})
	go func() {
		defer close(done)
		defer close(release)
		select {
		case <-entered:
		case <-stop:
			return
		}
		for !f.c.disposed {
			select {
			case <-stop:
				return
			default:
				runtime.Gosched()
			}
		}
	}()
	return release
}
func (f *viewportTestEnvironment) worker(index int) {
	stopped := make(chan struct{})
	f.workers = append(f.workers, stopped)
	go func() {
		defer close(stopped)
		f.c.worker(index)
	}()
}
func (f *viewportTestEnvironment) function(fn func(js.Value, []js.Value) any) js.Func {
	v := js.FuncOf(fn)
	f.functions = append(f.functions, v)
	return v
}
func (f *viewportTestEnvironment) link(attrs map[string]string) js.Value {
	node := js.ValueOf(map[string]any{"nodeType": 1, "tagName": "A", "isConnected": true, "inRoot": true})
	node.Set("getAttribute", f.function(func(_ js.Value, args []js.Value) any {
		value, ok := attrs[args[0].String()]
		if !ok {
			return nil
		}
		return value
	}))
	node.Set("hasAttribute", f.function(func(_ js.Value, args []js.Value) any { _, ok := attrs[args[0].String()]; return ok }))
	node.Set("closest", f.function(func(js.Value, []js.Value) any { return node }))
	node.Set("getBoundingClientRect", f.function(func(js.Value, []js.Value) any { return map[string]any{"top": 1, "left": 1, "bottom": 30, "right": 30} }))
	return node
}
func viewportWait(t *testing.T, predicate func() bool) {
	t.Helper()
	deadline := time.After(time.Second)
	for !predicate() {
		select {
		case <-deadline:
			t.Fatal("viewport scheduler did not reach expected state")
		case <-time.After(time.Millisecond):
		}
	}
}
func TestViewportNetworkAndDocumentGates(t *testing.T) {
	f := viewportTestFixture(t)
	if !viewportAllowed() {
		t.Fatal("ordinary visible online document disabled")
	}
	for _, name := range []string{"hidden", "offline", "saveData", "2g", "slow-2g", "development", "draft", "frozen"} {
		t.Run(name, func(t *testing.T) {
			switch name {
			case "hidden":
				doc.Set("visibilityState", "hidden")
			case "offline":
				f.navigator.Set("onLine", false)
			case "saveData":
				f.connection.Set("saveData", true)
			case "2g", "slow-2g":
				f.connection.Set("effectiveType", name)
			case "development":
				routeManifest.Development = true
			case "draft":
				routeManifest.Draft = true
			case "frozen":
				draftFrozen = true
			}
			if viewportAllowed() {
				t.Fatal("gate ignored", name)
			}
			doc.Set("visibilityState", "visible")
			f.navigator.Set("onLine", true)
			f.connection.Set("saveData", false)
			f.connection.Set("effectiveType", "4g")
			routeManifest.Development = false
			routeManifest.Draft = false
			draftFrozen = false
		})
	}
}
func TestViewportLinkEligibilityAndBoundedAttemptLedger(t *testing.T) {
	f := viewportTestFixture(t)
	for _, test := range []struct {
		name  string
		attrs map[string]string
	}{
		{"plain", map[string]string{"href": "/page"}}, {"false", map[string]string{"href": "/page", "data-nextgo-prefetch": "false"}}, {"api", map[string]string{"href": "/api/health", "data-nextgo-prefetch": "viewport"}}, {"external", map[string]string{"href": "https://other.test/page", "data-nextgo-prefetch": "viewport"}}, {"download", map[string]string{"href": "/page", "data-nextgo-prefetch": "viewport", "download": ""}}, {"target", map[string]string{"href": "/page", "data-nextgo-prefetch": "viewport", "target": "_blank"}}, {"reload", map[string]string{"href": "/page", "data-nextgo-prefetch": "viewport", "data-nextgo-reload": ""}}, {"fragment", map[string]string{"href": "/home#section", "data-nextgo-prefetch": "viewport"}}, {"oversize", map[string]string{"href": "/page?q=" + strings.Repeat("x", viewportURLLimit), "data-nextgo-prefetch": "viewport"}},
	} {
		if _, ok := f.c.candidate(f.link(test.attrs)); ok {
			t.Fatal("ineligible candidate", test.name)
		}
	}
	for i := 0; i < viewportQueueLimit+8; i++ {
		f.c.enqueue(f.link(map[string]string{"href": fmt.Sprint("/page/", i), "data-nextgo-prefetch": "viewport"}))
	}
	if len(f.c.queue) != viewportQueueLimit {
		t.Fatal("unbounded queue", len(f.c.queue))
	}
	for _, queued := range f.c.queue {
		if f.c.attempted(queued) {
			t.Fatal("attempt spent before dispatch")
		}
	}
	f.c.queue = nil
	for i := 0; i < viewportObservedLimit+5; i++ {
		candidate, _ := f.c.candidate(f.link(map[string]string{"href": fmt.Sprint("/page/", i), "data-nextgo-prefetch": "viewport"}))
		f.c.mark(candidate)
	}
	if len(f.c.recent) != viewportObservedLimit || len(f.c.recentOrder) != viewportObservedLimit {
		t.Fatal("unbounded URL ledger")
	}
	duplicate, _ := f.c.candidate(f.link(map[string]string{"href": "/page/128", "data-nextgo-prefetch": "viewport"}))
	if !f.c.attempted(duplicate) {
		t.Fatal("duplicate node repeats uncached URL")
	}
	cacheEpoch++
	duplicate.epoch = cacheEpoch
	if f.c.attempted(duplicate) {
		t.Fatal("invalidation cannot retry")
	}
}
func TestViewportWorkerSlotsRemainHeldUntilPhysicalCompletion(t *testing.T) {
	f := viewportTestFixture(t)
	f.c.scanning = true // isolate worker state from independent discovery
	var cleanupEntered chan struct{}
	canceled := 0
	var completions []chan struct{}
	for i := 0; i < 4; i++ {
		node := f.link(map[string]string{"href": fmt.Sprint("/page/", i), "data-nextgo-prefetch": "viewport"})
		candidate, _ := f.c.candidate(node)
		done := make(chan struct{})
		completions = append(completions, done)
		prefetches[candidate.url] = &prefetchRequest{done: done, cancel: func() { canceled++ }, viewport: i < 2}
		f.c.enqueue(node)
	}
	defer func() {
		for _, done := range completions {
			close(done)
		}
		if cleanupEntered != nil {
			select {
			case <-cleanupEntered:
			case <-time.After(time.Second):
				t.Error("controlled cleanup worker did not enter rectangle callback")
			}
		}
	}()
	for i := 0; i < viewportWorkers; i++ {
		f.worker(i)
	}
	f.c.signal()
	viewportWait(t, func() bool { return f.c.flights[0].pending != nil && f.c.flights[1].pending != nil })
	if len(f.c.queue) != 2 {
		t.Fatal("workers exceeded two physical jobs")
	}
	f.c.pause()
	if canceled != 2 || len(f.c.queue) != 0 {
		t.Fatal("pause did not cancel only owned flights", canceled, len(f.c.queue))
	}
	for _, flight := range f.c.flights {
		if f.c.attempted(flight.candidate) {
			t.Fatal("canceled attempt cannot resume")
		}
	}
	f.c.active = true
	candidate, _ := f.c.candidate(f.link(map[string]string{"href": "/page/2", "data-nextgo-prefetch": "viewport"}))
	f.c.queue = append(f.c.queue, candidate)
	f.c.signal()
	time.Sleep(5 * time.Millisecond)
	if len(f.c.queue) != 1 {
		t.Fatal("cancellation released physical slot")
	}
	// The ordinary test's assertions are complete. Hold the actual worker
	// inside its JS fixture callback while testing.T runs actual cleanup.
	cleanupEntered = make(chan struct{})
	releaseRectangle := f.rectangleBarrier(cleanupEntered)
	candidate.node.Set("getBoundingClientRect", f.function(func(js.Value, []js.Value) any {
		close(cleanupEntered)
		<-releaseRectangle
		return map[string]any{"top": 1, "left": 1, "bottom": 30, "right": 30}
	}))
}
func TestViewportForegroundNavigationNeverWaitsForSpeculation(t *testing.T) {
	f := viewportTestFixture(t)
	_ = f
	// Minimal document attributes suffice: network failure happens before DOM parsing.
	get := f.function(func(js.Value, []js.Value) any { return nil })
	doc.Set("body", map[string]any{"getAttribute": get})
	win = js.Global()
	key := "https://example.test/page"
	canceled := false
	pending := &prefetchRequest{done: make(chan struct{}), cancel: func() { canceled = true }, viewport: true}
	prefetches[key] = pending
	mockBrowserFetch(t, func([]js.Value) any {
		return js.Global().Get("Promise").Call("reject", js.Global().Get("Error").New("foreground reached network"))
	})
	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()
	err := navigate(ctx, key, false, js.Undefined(), navigationID, NavigationOptions{})
	if !canceled || err == nil || !strings.Contains(err.Error(), "foreground reached network") {
		t.Fatal("foreground waited for speculation", canceled, err)
	}
	if prefetches[key] != nil {
		t.Fatal("canceled viewport still occupies URL admission")
	}
}

func TestViewportIncrementalScanRotatesPastOffscreenObserverCohort(t *testing.T) {
	f := viewportTestFixture(t)
	nodes := make([]js.Value, 0, viewportObservedLimit+viewportScanSlice+1)
	for i := 0; i < viewportScanSlice+7; i++ {
		nodes = append(nodes, js.ValueOf(map[string]any{"tagName": "DIV"}))
	}
	for i := 0; i < viewportObservedLimit+1; i++ {
		nodes = append(nodes, f.link(map[string]string{"href": fmt.Sprint("/later/", i), "data-nextgo-prefetch": "viewport"}))
	}
	cursor := 0
	walker := js.ValueOf(map[string]any{"nextNode": f.function(func(js.Value, []js.Value) any {
		if cursor == len(nodes) {
			return nil
		}
		node := nodes[cursor]
		cursor++
		return node
	})})
	doc.Set("createTreeWalker", f.function(func(js.Value, []js.Value) any { return walker }))
	observed, maxObserved := 0, 0
	f.c.io.Set("observe", f.function(func(js.Value, []js.Value) any {
		observed++
		if len(f.c.observed) > maxObserved {
			maxObserved = len(f.c.observed)
		}
		return nil
	}))
	f.c.requestScan()
	viewportWait(t, func() bool { return !f.c.scanning })
	if observed != viewportObservedLimit+1 || maxObserved > viewportObservedLimit || cursor != len(nodes) {
		t.Fatal("scan permanently starved later links or exceeded observer quota", observed, maxObserved, cursor)
	}
}

func TestViewportMutationInspectionSkipsTextAndBoundsRecords(t *testing.T) {
	text := js.ValueOf(map[string]any{"nodeType": 3})
	comment := js.ValueOf(map[string]any{"nodeType": 8})
	element := js.ValueOf(map[string]any{"nodeType": 1})
	record := func(added []any) any {
		return map[string]any{"type": "childList", "addedNodes": added, "removedNodes": []any{text, comment}}
	}
	if viewportMutationRelevant(js.ValueOf([]any{record([]any{text, comment})})) {
		t.Fatal("ordinary text state update triggers full scan")
	}
	if !viewportMutationRelevant(js.ValueOf([]any{record([]any{element})})) {
		t.Fatal("element insertion ignored")
	}
	if !viewportMutationRelevant(js.ValueOf([]any{map[string]any{"type": "attributes"}})) {
		t.Fatal("eligibility/layout attribute ignored")
	}
	many := make([]any, viewportScanSlice+1)
	for i := range many {
		many[i] = record(nil)
	}
	if !viewportMutationRelevant(js.ValueOf(many)) {
		t.Fatal("overflow failed conservative rescan")
	}
}

func TestViewportInvalidationSchedulesCoalescedRescanWithoutDOMMutation(t *testing.T) {
	f := viewportTestFixture(t)
	previous := viewportPrefetch
	viewportPrefetch = f.c
	t.Cleanup(func() { viewportPrefetch = previous })
	// A running scan owns its cursor. Repeated invalidation should request one
	// follow-up pass rather than create observers or concurrent traversals.
	f.c.scanning = true
	candidate, _ := f.c.candidate(f.link(map[string]string{"href": "/retry", "data-nextgo-prefetch": "viewport"}))
	f.c.mark(candidate)
	invalidateCache()
	if !f.c.dirty || !f.c.scanning {
		t.Fatal("invalidation did not schedule coalesced scan")
	}
	candidate.epoch = cacheEpoch
	if f.c.attempted(candidate) {
		t.Fatal("new epoch still suppressed by old attempt")
	}
	f.c.dirty = false
	invalidateCache()
	if !f.c.dirty {
		t.Fatal("second invalidation failed to request follow-up scan")
	}
}
