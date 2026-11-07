//go:build js && wasm && !nextgo_module

package client

import (
	"encoding/json"
	"errors"
	"syscall/js"
	"testing"

	"github.com/Suhaibinator/Next.go/internal/navigation"
)

func mergeApplicationTestEnvironment(t *testing.T) (js.Value, js.Value, js.Value) {
	t.Helper()
	propsBrowserEnvironment(t)
	mergeQueueTestState(t)
	previous, oldDoc, oldWin, oldMounted := mergeApplication, doc, win, mounted
	oldEntries, oldOrder := mergeEntryHistory, mergeEntryOrder
	t.Cleanup(func() {
		mergeApplication, doc, win, mounted = previous, oldDoc, oldWin, oldMounted
		mergeEntryHistory, mergeEntryOrder = oldEntries, oldOrder
	})
	fixture := clientOnlyDOM(t)
	app := fixture.Get("outer")
	app.Call("setAttribute", "id", "nextgo-app")
	body := fixture.Call("make", "body")
	body.Call("appendChild", app)
	status := js.Global().Get("Object").New()
	status.Set("hidden", true)
	status.Set("textContent", "")
	doc = fixture.Get("owner")
	doc.Set("body", body)
	get := js.FuncOf(func(_ js.Value, args []js.Value) any {
		if args[0].String() == "nextgo-app" {
			if body.Call("contains", app).Bool() {
				return app
			}
			return js.Null()
		}
		if args[0].String() == "nextgo-status" {
			return status
		}
		return js.Null()
	})
	t.Cleanup(get.Release)
	doc.Set("getElementById", get)
	win = js.Global().Get("Object").New()
	win.Set("console", js.Global().Get("console"))
	win.Set("URL", js.Global().Get("URL"))
	mergeCurrent = nil
	mounted = nil
	installMergeApplicationAuthority(app, nil, nil)
	return fixture, app, status
}

func TestMergeOrdinaryOnlyPropsDenialRevokesBeforeHeldCloneAndReadSettleAndColdRecovers(t *testing.T) {
	fixture, app, status := mergeApplicationTestEnvironment(t)
	root := fixture.Get("root")
	root.Call("removeAttribute", clientBoundaryAttribute)
	scope := &Scope{Root: Element{root}}
	t.Cleanup(scope.dispose)
	outsideNode := fixture.Call("make", "button")
	doc.Get("body").Call("appendChild", outsideNode)
	outside := &Scope{Root: Element{outsideNode}}
	t.Cleanup(outside.dispose)
	clicks, outsideClicks := 0, 0
	outside.On(Element{outsideNode}, "click", func(Event) { outsideClicks++ })
	held := false
	cloneEntered, releaseClone := make(chan struct{}), make(chan struct{})
	readReturned, releaseRead, settled := make(chan struct{}), make(chan struct{}), make(chan struct{})
	var source *PropsSource[int]
	err := withMergeApplicationMount(scope, func() error {
		return mountScoped(scope, func(owner *Scope, raw json.RawMessage) error {
			if _, exists := mergeApplication.scopes[owner]; !exists {
				return errPropsInvalid
			}
			var err error
			source, err = Props(owner, raw, PropsOptions[int]{Clone: func(value int) int {
				if held {
					close(cloneEntered)
					<-releaseClone
				}
				return value
			}})
			if err != nil {
				return err
			}
			owner.On(Element{fixture.Get("fallback")}, "click", func(Event) { clicks++ })
			return nil
		}, json.RawMessage(`7`))
	})
	if err != nil || source == nil || mergeCurrent != nil || scope.mergeClientReady() {
		t.Fatal("ordinary initial-only source setup changed descriptor or failed", err)
	}
	held = true
	var readErr error
	result := make(chan error, 1)
	go func() {
		defer close(settled)
		result <- runMergeCallback(func() { _, readErr = source.GetSnapshot(); close(readReturned); <-releaseRead })
	}()
	t.Cleanup(func() {
		select {
		case <-releaseClone:
		default:
			close(releaseClone)
		}
		select {
		case <-releaseRead:
		default:
			close(releaseRead)
		}
		<-settled
		propsWait(t, func() bool { return propsWorkerCount() == 0 && mergeCallbackReads == 0 })
	})
	select {
	case <-cloneEntered:
	case <-readReturned:
		t.Fatal("snapshot returned before held Clone", readErr)
	}
	if propsWorkerCount() != 2 || mergeCallbackReads != 1 {
		t.Fatal("actual clone/read ownership not held", propsWorkerCount(), mergeCallbackReads)
	}
	privacy := mergePrivacy
	mergeIncomingAuthority(nil, nil, 403)
	<-readReturned
	fixture.Get("fallback").Call("dispatchEvent", js.Global().Get("Event").New("click"))
	outsideNode.Call("dispatchEvent", js.Global().Get("Event").New("click"))
	if mergePrivacy != privacy+1 || !mergeApplication.revoked || !scope.mergeRevoked || scope.active() || !app.Get("hidden").Bool() || !app.Call("hasAttribute", "inert").Bool() || status.Get("hidden").Bool() || clicks != 0 || outsideClicks != 1 || !outside.active() {
		t.Fatal("descriptor-free denial left published authority or touched outside owner", clicks, outsideClicks)
	}
	if _, err := source.GetSnapshot(); !errors.Is(err, errPropsClosed) {
		t.Fatal("denial left ordinary Props readable", err)
	}
	if !errors.Is(readErr, errPropsStale) || propsWorkerCount() != 2 || mergeCallbackReads != 1 {
		t.Fatal("logical denial retired held physical work", readErr, propsWorkerCount(), mergeCallbackReads)
	}
	// Repeating the same recognized fence is idempotent; build/config paths use
	// this same helper and must not revive or re-stamp the denied document.
	revokeMergeDocument()
	if mergePrivacy != privacy+1 {
		t.Fatal("repeated denial changed authority epoch")
	}
	close(releaseClone)
	propsWait(t, func() bool { return propsWorkerCount() == 1 })
	close(releaseRead)
	if err := <-result; err != nil {
		t.Fatal(err)
	}
	<-settled
	propsWait(t, func() bool { return propsWorkerCount() == 0 && mergeCallbackReads == 0 })
	held = false
	scope.dispose()
	// Exercise the real cold-frame installation with no MergeDescriptor. Its new
	// application authority must reveal only the fresh accepted ordinary source.
	setLocation("https://props.test/fresh")
	if err := installColdMergeFrame(envelope{}, app, "https://props.test/fresh", mergePrivacy); err != nil {
		t.Fatal(err)
	}
	fresh := fixture.Call("make", "div")
	fresh.Call("setAttribute", "data-nextgo-island", "fresh")
	app.Call("appendChild", fresh)
	next := &Scope{Root: Element{fresh}}
	t.Cleanup(next.dispose)
	var newer *PropsSource[int]
	if err := withMergeApplicationMount(next, func() error {
		return mountScoped(next, func(owner *Scope, raw json.RawMessage) error {
			var err error
			newer, err = Props(owner, raw, PropsOptions[int]{Clone: func(value int) int { return value }})
			return err
		}, json.RawMessage(`9`))
	}); err != nil {
		t.Fatal(err)
	}
	snapshot, err := newer.GetSnapshot()
	if err != nil || snapshot.Value != 9 || snapshot.Location.URL != "https://props.test/fresh" || mergeCurrent != nil || mergeApplication.revoked || app.Get("hidden").Bool() || app.Call("hasAttribute", "inert").Bool() {
		t.Fatal("fresh ordinary cold page did not recover authority", snapshot, err)
	}
	if _, err := source.GetSnapshot(); !errors.Is(err, errPropsClosed) {
		t.Fatal("cold recovery revived retired source", err)
	}
}

func TestMergeOrdinaryInflightMountIsRegisteredBeforePropsAndLateSuccessCannotPublish(t *testing.T) {
	fixture, app, _ := mergeApplicationTestEnvironment(t)
	root := fixture.Get("root")
	root.Call("removeAttribute", clientBoundaryAttribute)
	scope := &Scope{Root: Element{root}}
	t.Cleanup(scope.dispose)
	entered, release, settled := make(chan struct{}), make(chan struct{}), make(chan struct{})
	result := make(chan error, 1)
	after := false
	go func() {
		defer close(settled)
		result <- withMergeApplicationMount(scope, func() error {
			return mountScoped(scope, func(owner *Scope, _ json.RawMessage) error {
				if _, exists := mergeApplication.scopes[owner]; !exists {
					return errPropsInvalid
				}
				close(entered)
				<-release
				after = true
				return nil
			}, nil)
		})
	}()
	t.Cleanup(func() {
		select {
		case <-release:
		default:
			close(release)
		}
		<-settled
	})
	select {
	case <-entered:
	case err := <-result:
		t.Fatal("ordinary Mount failed before held callback", err)
	}
	// Actual build/config rejection call sites invoke revokeMergeDocument before
	// reload/recovery. A pending ordinary Mount is captured even before Props.
	revokeMergeDocument()
	if !scope.mergeRevoked || !app.Get("hidden").Bool() || !app.Call("hasAttribute", "inert").Bool() {
		t.Fatal("build/config fence missed ordinary in-flight Mount")
	}
	close(release)
	if err := <-result; !errors.Is(err, errPropsClosed) {
		t.Fatal("late ordinary Mount retained document authority", err)
	}
	<-settled
	if !after || !scope.closed {
		t.Fatal("physical Mount was not allowed to settle and dispose")
	}
}

func TestMergeApplicationRegistryKeepsLegacyNativeTimingAndRejectsChangedTopOwner(t *testing.T) {
	fixture, app, _ := mergeApplicationTestEnvironment(t)
	root := fixture.Get("root")
	root.Call("removeAttribute", clientBoundaryAttribute)
	scope := &Scope{Root: Element{root}}
	t.Cleanup(scope.dispose)
	clicks := 0
	if err := withMergeApplicationMount(scope, func() error {
		return mountScoped(scope, func(owner *Scope, _ json.RawMessage) error {
			owner.On(Element{fixture.Get("fallback")}, "click", func(event Event) { clicks++; event.PreventDefault() })
			return nil
		}, nil)
	}); err != nil {
		t.Fatal(err)
	}
	event := js.Global().Get("Event").New("click", map[string]any{"cancelable": true})
	fixture.Get("fallback").Call("dispatchEvent", event)
	if clicks != 1 || !event.Get("defaultPrevented").Bool() || scope.mergeCallbacks || propsWorkerCount() != 0 || mergeCallbackReads != 0 {
		t.Fatal("NoProps registration changed direct/native semantics", clicks, scope.mergeCallbacks, propsWorkerCount(), mergeCallbackReads)
	}
	other := &Scope{Root: Element{root}}
	t.Cleanup(other.dispose)
	err := withMergeApplicationMount(other, func() error { doc.Get("body").Call("removeChild", app); return nil })
	if !errors.Is(err, errMergeOwnership) || !other.mergeRevoked {
		t.Fatal("changed application root accepted late publication", err)
	}
}

func TestMergeApplicationReplacementIncludesAlreadyHydratedLegacyRootsAndKeepsOutsideIndependent(t *testing.T) {
	fixture, app, _ := mergeApplicationTestEnvironment(t)
	root := fixture.Get("root")
	root.Call("removeAttribute", clientBoundaryAttribute)
	scope := &Scope{Root: Element{root}}
	t.Cleanup(scope.dispose)
	if err := withMergeApplicationMount(scope, func() error { return mountScoped(scope, func(*Scope, json.RawMessage) error { return nil }, nil) }); err != nil {
		t.Fatal(err)
	}
	outsideNode := fixture.Call("make", "button")
	doc.Get("body").Call("appendChild", outsideNode)
	outside := &Scope{Root: Element{outsideNode}}
	t.Cleanup(outside.dispose)
	mounted = []*Scope{scope, outside}
	oldToken := mergeApplicationToken()
	installMergeApplicationAuthority(app, nil, nil)
	if mergeApplicationCurrent(oldToken) {
		t.Fatal("fresh application reused old publication identity")
	}
	if _, exists := mergeApplication.scopes[scope]; !exists {
		t.Fatal("already-hydrated V1 owner escaped fresh application cohort")
	}
	if _, exists := mergeApplication.scopes[outside]; exists {
		t.Fatal("outside application owner entered revocation cohort")
	}
	mergeIncomingAuthority(nil, nil, 401)
	if scope.active() || !scope.mergeRevoked || !outside.active() || outside.mergeRevoked {
		t.Fatal("legacy/outside authority was misattributed")
	}
}

func TestMergeOrdinaryKnownRetentionPartitionChangeRevokesHeldReaderAndColdInstallsFreshPartition(t *testing.T) {
	fixture, app, _ := mergeApplicationTestEnvironment(t)
	first, _ := navigation.NewRetention("first", "first body")
	second, _ := navigation.NewRetention("second", "fresh body")
	raw, _ := json.Marshal(first)
	doc.Get("body").Call("setAttribute", navigation.RetentionAttribute, string(raw))
	installMergeApplicationAuthority(app, nil, initialMergeRetention())
	root := fixture.Get("root")
	root.Call("removeAttribute", clientBoundaryAttribute)
	scope := &Scope{Root: Element{root}}
	t.Cleanup(scope.dispose)
	var source *PropsSource[int]
	if err := withMergeApplicationMount(scope, func() error {
		return mountScoped(scope, func(owner *Scope, raw json.RawMessage) error {
			var err error
			source, err = Props(owner, raw, PropsOptions[int]{Clone: func(value int) int { return value }})
			return err
		}, json.RawMessage(`7`))
	}); err != nil {
		t.Fatal(err)
	}
	if mergeCurrent != nil || mergeApplication.partition != "first" {
		t.Fatal("ordinary bootstrap partition requires no merge descriptor")
	}
	entered, release, settled := make(chan struct{}), make(chan struct{}), make(chan struct{})
	result := make(chan error, 1)
	var after error
	go func() {
		defer close(settled)
		result <- runMergeCallback(func() { close(entered); <-release; _, after = source.GetSnapshot() })
	}()
	t.Cleanup(func() {
		select {
		case <-release:
		default:
			close(release)
		}
		<-settled
		propsWait(t, func() bool { return propsWorkerCount() == 0 && mergeCallbackReads == 0 })
	})
	<-entered
	mergeIncomingAuthority(nil, first, 200)
	mergeIncomingAuthority(nil, nil, 200)
	mergeIncomingAuthority(nil, &navigation.Retention{Partition: "unvalidated"}, 200)
	if !scope.active() || mergeApplication.revoked || mergeCallbackReads != 1 {
		t.Fatal("same/unknown/unvalidated partition revoked useful source")
	}
	mergeIncomingAuthority(nil, second, 200)
	if !scope.mergeRevoked || scope.active() || !app.Get("hidden").Bool() || !app.Call("hasAttribute", "inert").Bool() || mergeCallbackReads != 1 || propsWorkerCount() != 1 {
		t.Fatal("known partition change waited for reader or retired it early")
	}
	close(release)
	if err := <-result; err != nil {
		t.Fatal(err)
	}
	<-settled
	if !errors.Is(after, errPropsClosed) {
		t.Fatal("held reader retained source authority", after)
	}
	scope.dispose()
	if err := installColdMergeFrame(envelope{HTML: "fresh body", Retention: second}, app, "https://props.test/fresh", mergePrivacy); err != nil {
		t.Fatal(err)
	}
	if mergeApplication.partition != "second" || mergeApplication.revoked || app.Get("hidden").Bool() {
		t.Fatal("cold frame borrowed stale bootstrap partition")
	}
}
