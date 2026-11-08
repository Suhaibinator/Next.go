//go:build js && wasm

package client

import (
	"context"
	"errors"
	"runtime"
	"syscall/js"
	"testing"
	"time"

	"github.com/Suhaibinator/Next.go/scripts"
)

func widgetFixture(t *testing.T) (*Scope, Element, js.Value, js.Value) {
	t.Helper()
	f := clientOnlyDOM(t)
	host := f.Call("make", "div")
	fallback := f.Call("make", "input", map[string]any{"data-original": ""})
	fallback.Set("value", "typed before startup")
	host.Call("appendChild", fallback)
	f.Get("root").Call("appendChild", host)
	scope := &Scope{Root: Element{f.Get("root")}}
	t.Cleanup(scope.dispose)
	return scope, Element{host}, fallback, f
}

func awaitWidget(t *testing.T, done <-chan struct{}) {
	t.Helper()
	select {
	case <-done:
	case <-time.After(3 * time.Second):
		t.Fatal("widget did not settle")
	}
}

func TestWidgetOwnsSubtreeAndRestoresExactFallbackAfterClose(t *testing.T) {
	parent, host, fallback, f := widgetFixture(t)
	beforeLive, beforeWorkers, beforeOwners := liveWidgets, widgetWorkers, len(widgetOwners)
	ready := make(chan error, 1)
	cleanups, clicks := 0, 0
	var node js.Value
	var mountContext context.Context
	w, err := parent.Widget(host, WidgetOptions{Mount: func(ctx context.Context, scope *Scope) error {
		mountContext = ctx
		if scope.Root.Value.Get("childNodes").Length() != 0 {
			return errors.New("constructor did not receive an empty host")
		}
		node = f.Call("make", "button", map[string]any{"data-widget-content": ""})
		scope.Root.Value.Call("appendChild", node)
		scope.Cleanup(func() { cleanups++ })
		scope.On(Element{node}, "click", func(Event) { clicks++ })
		return nil
	}, Ready: func(err error) { ready <- err }})
	if err != nil {
		t.Fatal(err)
	}
	if err := <-ready; err != nil {
		t.Fatal(err)
	}
	awaitWidget(t, w.Settled())
	if !w.Ready() || widgetWorkers != beforeWorkers || fallback.Get("parentNode").Equal(host.Value) {
		t.Fatal("startup state or fallback ownership incorrect")
	}
	if _, err := parent.Require("[data-widget-content]"); err == nil {
		t.Fatal("parent crossed opaque widget ownership")
	}
	node.Call("dispatchEvent", js.Global().Get("Event").New("click"))
	w.Close()
	w.Close()
	node.Call("dispatchEvent", js.Global().Get("Event").New("click"))
	if w.Ready() || cleanups != 1 || clicks != 1 || node.Get("listeners").Int() != 0 || mountContext.Err() == nil {
		t.Fatal("resource/listener/context cleanup failed", cleanups, clicks)
	}
	if !host.Value.Get("firstChild").Equal(fallback) || fallback.Get("value").String() != "typed before startup" {
		t.Fatal("fallback identity/value changed")
	}
	if liveWidgets != beforeLive || widgetWorkers != beforeWorkers || len(widgetOwners) != beforeOwners || len(parent.children) != 0 {
		t.Fatal("widget owner retained")
	}
}

func TestWidgetFailurePanicAndGoexitCleanBeforeNotification(t *testing.T) {
	for _, mode := range []string{"error", "panic", "goexit"} {
		t.Run(mode, func(t *testing.T) {
			parent, host, fallback, f := widgetFixture(t)
			ready := make(chan error, 1)
			cleanups := 0
			w, err := parent.Widget(host, WidgetOptions{Mount: func(_ context.Context, scope *Scope) error {
				scope.Root.Value.Call("appendChild", f.Call("make", "div"))
				scope.Cleanup(func() { cleanups++ })
				switch mode {
				case "panic":
					panic("constructor failed")
				case "goexit":
					runtime.Goexit()
				}
				return errors.New("constructor failed")
			}, Ready: func(err error) {
				if cleanups != 1 || !host.Value.Get("firstChild").Equal(fallback) {
					t.Error("failed constructor notified before resource/fallback cleanup")
				}
				ready <- err
			}})
			if err != nil {
				t.Fatal(err)
			}
			awaitWidget(t, w.Settled())
			select {
			case err := <-ready:
				if err == nil {
					t.Fatal("failure reported success")
				}
			default:
				t.Fatal("failure not reported")
			}
			if w.Ready() || cleanups != 1 || len(parent.children) != 0 {
				t.Fatal("failed constructor retained ownership")
			}
		})
	}
}

func TestWidgetCanceledLateConstructorCannotReuseHostOrLoseFallback(t *testing.T) {
	parent, host, fallback, f := widgetFixture(t)
	started, release := make(chan struct{}), make(chan struct{})
	notified, cleanups := 0, 0
	beforeWorkers := widgetWorkers
	w, err := parent.Widget(host, WidgetOptions{Mount: func(_ context.Context, scope *Scope) error {
		close(started)
		<-release // Deliberately noncooperative, to exercise physical ownership.
		for scope.Root.Value.Get("firstChild").Truthy() {
			scope.Root.Value.Call("removeChild", scope.Root.Value.Get("firstChild"))
		}
		scope.Root.Value.Call("appendChild", f.Call("make", "div"))
		scope.Cleanup(func() { cleanups++ })
		return nil
	}, Ready: func(error) { notified++ }})
	if err != nil {
		t.Fatal(err)
	}
	<-started
	w.Close()
	if widgetWorkers != beforeWorkers+1 || len(parent.children) != 1 {
		t.Fatal("cancellation released a still-running constructor")
	}
	if _, _, err := parent.Subscope(host); err == nil {
		t.Fatal("pending host was borrowed by another child")
	}
	other := &Scope{Root: parent.Root}
	t.Cleanup(other.dispose)
	if _, err := other.Widget(host, WidgetOptions{Mount: func(context.Context, *Scope) error { return nil }, Ready: func(error) {}}); err == nil {
		t.Fatal("another owner reused a pending widget host")
	}
	close(release)
	awaitWidget(t, w.Settled())
	if notified != 0 || cleanups != 1 || widgetWorkers != beforeWorkers || len(parent.children) != 0 || !host.Value.Get("firstChild").Equal(fallback) {
		t.Fatal("late constructor escaped cancellation compensation", notified, cleanups)
	}
}

func TestWidgetRejectsInvalidDependenciesAndManagedFallbackBeforeMount(t *testing.T) {
	parent, host, _, f := widgetFixture(t)
	mounts := 0
	valid := WidgetOptions{Mount: func(context.Context, *Scope) error { mounts++; return nil }, Ready: func(error) {}}
	for _, dependency := range []scripts.Script{{}, {ID: "early", Src: "https://example.test/widget.js", Strategy: scripts.BeforeInteractive}} {
		options := valid
		options.Scripts = []scripts.Script{dependency}
		if _, err := parent.Widget(host, options); err == nil {
			t.Fatal("accepted invalid/early dependency")
		}
	}
	host.Value.Call("appendChild", f.Call("make", "div", map[string]any{"data-nextgo-island": "nested"}))
	if _, err := parent.Widget(host, valid); err == nil || mounts != 0 || len(parent.children) != 0 {
		t.Fatal("nested framework markup entered opaque widget")
	}
	var zero Widget
	zero.Close()
	awaitWidget(t, zero.Settled())
	if zero.Ready() {
		t.Fatal("zero widget is ready")
	}
}

func TestWidgetCancellationRetainsPhysicalCapacityUntilWorkersReturn(t *testing.T) {
	parent, _, _, f := widgetFixture(t)
	beforeLive, beforeWorkers, beforeOwners := liveWidgets, widgetWorkers, len(widgetOwners)
	started := make(chan struct{}, maxWidgetWorkers)
	release := make(chan struct{})
	widgets := []*Widget{}
	for i := 0; i < maxWidgetWorkers; i++ {
		host := f.Call("make", "div")
		parent.Root.Value.Call("appendChild", host)
		w, err := parent.Widget(Element{host}, WidgetOptions{Mount: func(context.Context, *Scope) error {
			started <- struct{}{}
			<-release
			return nil
		}, Ready: func(error) { t.Error("canceled worker notified") }})
		if err != nil {
			t.Fatal(err)
		}
		widgets = append(widgets, w)
	}
	for range widgets {
		<-started
	}
	for _, w := range widgets {
		w.Close()
	}
	host := f.Call("make", "div")
	parent.Root.Value.Call("appendChild", host)
	if _, err := parent.Widget(Element{host}, WidgetOptions{Mount: func(context.Context, *Scope) error { return nil }, Ready: func(error) {}}); err == nil {
		t.Fatal("canceling startup manufactured physical worker capacity")
	}
	if liveWidgets != beforeLive || widgetWorkers != beforeWorkers+maxWidgetWorkers || len(parent.children) != maxWidgetWorkers {
		t.Fatal("incorrect live/physical ownership after cancellation")
	}
	close(release)
	for _, w := range widgets {
		awaitWidget(t, w.Settled())
	}
	if widgetWorkers != beforeWorkers || len(parent.children) != 0 || len(widgetOwners) != beforeOwners {
		t.Fatal("completed workers retained capacity/owners")
	}
}

func TestWidgetLiveCapacityAndAncestorDisposalReleaseAllOwners(t *testing.T) {
	parent, _, _, f := widgetFixture(t)
	beforeLive, beforeWorkers, beforeOwners := liveWidgets, widgetWorkers, len(widgetOwners)
	widgets := []*Widget{}
	for i := 0; i < maxLiveWidgets; i++ {
		host := f.Call("make", "div")
		parent.Root.Value.Call("appendChild", host)
		w, err := parent.Widget(Element{host}, WidgetOptions{Mount: func(context.Context, *Scope) error { return nil }, Ready: func(error) {}})
		if err != nil {
			t.Fatal(err)
		}
		awaitWidget(t, w.Settled())
		widgets = append(widgets, w)
	}
	host := f.Call("make", "div")
	parent.Root.Value.Call("appendChild", host)
	if _, err := parent.Widget(Element{host}, WidgetOptions{Mount: func(context.Context, *Scope) error { return nil }, Ready: func(error) {}}); err == nil {
		t.Fatal("live widget bound ignored")
	}
	parent.dispose()
	for _, w := range widgets {
		if w.Ready() {
			t.Fatal("widget remains ready under closed ancestor")
		}
	}
	if liveWidgets != beforeLive || widgetWorkers != beforeWorkers || len(widgetOwners) != beforeOwners {
		t.Fatal("ancestor disposal retained widget owners")
	}
}
