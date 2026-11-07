//go:build js && wasm

package client

import (
	"encoding/json"
	"errors"
	"strings"
	"syscall/js"
	"testing"

	"github.com/Suhaibinator/Next.go/ui"
)

func moduleAuthorityEnvironment(t *testing.T) func(bool) string {
	t.Helper()
	propsBrowserEnvironment(t)
	oldScopes, oldPending, oldRevoked := moduleScopes, pendingModuleScopes, moduleAuthorityRevoked
	oldModules, oldDeployment, oldPublic := lazyModules, deployment, routeManifest.Public
	oldSelections, oldSelectionObservers, oldObservers := selections, selectionObservers, locationObservers
	oldDoc, oldWin := doc, win
	t.Cleanup(func() {
		moduleScopes, pendingModuleScopes, moduleAuthorityRevoked = oldScopes, oldPending, oldRevoked
		lazyModules, deployment, routeManifest.Public = oldModules, oldDeployment, oldPublic
		selections, selectionObservers, locationObservers = oldSelections, oldSelectionObservers, oldObservers
		doc, win = oldDoc, oldWin
	})
	moduleScopes = map[int]*Scope{}
	pendingModuleScopes = map[*Scope]struct{}{}
	moduleAuthorityRevoked = false
	lazyModules = map[string]*lazyState{}
	selections = map[string]Selection{}
	selectionObservers = map[uint64]selectionObserver{}
	locationObservers = map[uint64]*locationObserver{}
	fixture := clientOnlyDOM(t)
	body := fixture.Call("make", "body")
	body.Call("setAttribute", "data-nextgo-configuration", strings.Repeat("a", 64))
	doc = fixture.Get("owner")
	doc.Set("body", body)
	win = js.Global().Get("Object").New()
	win.Set("URL", js.Global().Get("URL"))
	win.Set("console", js.Global().Get("console"))
	return func(revoked bool) string {
		b, err := json.Marshal(moduleState{Configuration: strings.Repeat("a", 64), ResourceEpoch: resourceEpoch, Paths: deployment, Location: Location{URL: "https://props.test/module"}, Selections: map[string]Selection{}, Revoked: revoked})
		if err != nil {
			t.Fatal(err)
		}
		return string(b)
	}
}
func TestPropsModuleRevocationFencesPublishedSourcesBeforeObserversAndDoesNotReviveOldScopes(t *testing.T) {
	snapshot := moduleAuthorityEnvironment(t)
	clicks, observed, cleanups := 0, 0, 0
	scope, source, fixture := propsClientRoot(t, `7`, PropsOptions[int]{Clone: func(value int) int { return value }}, func(child *Scope, _ *PropsSource[int]) error {
		button, err := child.Require("[data-click]")
		if err != nil {
			return err
		}
		child.On(button, "click", func(Event) { clicks++ })
		child.Cleanup(func() { cleanups++ })
		return nil
	})
	moduleScopes[1] = scope
	locationObservers[987] = &locationObserver{owner: scope, fn: func(Location) { observed++ }}
	if err := syncModule(snapshot(true)); err != nil {
		t.Fatal(err)
	}
	fixture.Get("button").Call("dispatchEvent", js.Global().Get("Event").New("click"))
	if _, err := source.GetSnapshot(); !errors.Is(err, errPropsClosed) || scope.active() || !scope.mergeRevoked || clicks != 0 || observed != 0 || cleanups != 0 {
		t.Fatal("module revocation left source/event/observer authority or ran terminal cleanup", err, clicks, observed, cleanups)
	}
	fresh := &Scope{}
	if release, err := beginModuleMountAuthority(fresh); !errors.Is(err, errPropsClosed) || release != nil {
		t.Fatal("denied runtime admitted new authored Mount", err)
	}
	if err := syncModule(snapshot(false)); err != nil {
		t.Fatal(err)
	}
	release, err := beginModuleMountAuthority(fresh)
	if err != nil {
		t.Fatal(err)
	}
	release()
	if scope.active() || !scope.mergeRevoked || !fresh.active() {
		t.Fatal("fresh authority revived an old module Scope")
	}
	moduleScopes[2] = fresh
	var invalid moduleState
	if err := json.Unmarshal([]byte(snapshot(true)), &invalid); err != nil {
		t.Fatal(err)
	}
	invalid.Configuration = "different"
	raw, _ := json.Marshal(invalid)
	if err := syncModule(string(raw)); err == nil || fresh.active() || !fresh.mergeRevoked {
		t.Fatal("configuration error ran before the privileged module fence", err)
	}
	scope.dispose()
	if cleanups != 1 {
		t.Fatal("revoked module did not settle cleanup exactly once", cleanups)
	}
}
func TestPropsModulePendingMountRevocationPrecedesLateReturnWithoutClientViewRollback(t *testing.T) {
	snapshot := moduleAuthorityEnvironment(t)
	fixture := clientOnlyDOM(t)
	scope := &Scope{Root: Element{fixture.Get("root")}}
	t.Cleanup(scope.dispose)
	entered, release, settled := make(chan struct{}), make(chan struct{}), make(chan struct{})
	result := make(chan error, 1)
	cleanups, clicks := 0, 0
	var source *PropsSource[int]
	go func() {
		defer close(settled)
		done, err := beginModuleMountAuthority(scope)
		if err != nil {
			result <- err
			return
		}
		defer done()
		result <- mountScoped(scope, func(owner *Scope, raw json.RawMessage) error {
			var err error
			source, err = Props(owner, raw, PropsOptions[int]{Clone: func(value int) int { return value }})
			if err != nil {
				return err
			}
			child, err := owner.ClientView(ui.El("button", ui.Attrs{"data-click": ""}, ui.Text("module")))
			if err != nil {
				return err
			}
			button, err := child.Require("[data-click]")
			if err != nil {
				return err
			}
			child.On(button, "click", func(Event) { clicks++ })
			child.Cleanup(func() { cleanups++ })
			close(entered)
			<-release
			return nil
		}, json.RawMessage(`7`))
	}()
	t.Cleanup(func() {
		select {
		case <-release:
		default:
			close(release)
		}
		<-settled
		propsWait(t, func() bool { return propsWorkerCount() == 0 })
	})
	select {
	case <-entered:
	case err := <-result:
		t.Fatal("module Mount failed before in-flight ownership", err)
	}
	if _, exists := pendingModuleScopes[scope]; !exists {
		t.Fatal("module Mount was not registered before authored code")
	}
	if err := syncModule(snapshot(true)); err != nil {
		t.Fatal(err)
	}
	fixture.Get("button").Call("dispatchEvent", js.Global().Get("Event").New("click"))
	if _, err := source.GetSnapshot(); !errors.Is(err, errPropsClosed) || scope.active() || !scope.mergeRevoked || cleanups != 0 || clicks != 0 {
		t.Fatal("in-flight module Mount retained authority or cleaned up early", err, cleanups, clicks)
	}
	close(release)
	if err := <-result; !errors.Is(err, errPropsClosed) {
		t.Fatal("module Mount reported late success", err)
	}
	<-settled
	if len(pendingModuleScopes) != 0 || !scope.closed || cleanups != 1 || fixture.Get("fallback").Get("isConnected").Bool() || !scope.Root.Value.Get("firstChild").Equal(fixture.Get("fresh")) {
		t.Fatal("revoked module Mount leaked or rolled back old DOM", len(pendingModuleScopes), cleanups)
	}
}

func TestPropsFrozenModulePrivilegedRevokeFencesHeldCloneWithoutSyncOrUnfreeze(t *testing.T) {
	snapshot := moduleAuthorityEnvironment(t)
	held := false
	entered, release, returned := make(chan struct{}), make(chan struct{}), make(chan struct{})
	scope, source, _ := propsClientRoot(t, `7`, PropsOptions[int]{Clone: func(value int) int {
		if held {
			close(entered)
			<-release
		}
		return value
	}}, nil)
	moduleScopes[1] = scope
	held = true
	var readErr error
	go func() { defer close(returned); _, readErr = source.GetSnapshot() }()
	t.Cleanup(func() {
		select {
		case <-release:
		default:
			close(release)
		}
		<-returned
		propsWait(t, func() bool { return propsWorkerCount() == 0 })
	})
	select {
	case <-entered:
	case <-returned:
		t.Fatal("clone did not hold actual module work", readErr)
	}
	before, epoch := committedLocation, resourceEpoch
	var value moduleState
	if err := json.Unmarshal([]byte(snapshot(true)), &value); err != nil {
		t.Fatal(err)
	}
	value.Configuration = "changed"
	value.ResourceEpoch++
	raw, _ := json.Marshal(value)
	if err := syncModuleExport(string(raw), true); err != nil {
		t.Fatal(err)
	}
	<-returned
	if !errors.Is(readErr, errPropsStale) || !scope.mergeRevoked || scope.active() || propsWorkerCount() != 1 || committedLocation != before || resourceEpoch != epoch {
		t.Fatal("frozen revoke dispatched normal sync or retired physical work", readErr, propsWorkerCount())
	}
	if err := syncModuleExport(snapshot(false), true); err == nil || !moduleAuthorityRevoked || scope.active() {
		t.Fatal("false frozen snapshot revived module authority", err)
	}
	if err := syncModuleExport("{", true); err == nil {
		t.Fatal("invalid frozen snapshot accepted")
	}
	close(release)
	propsWait(t, func() bool { return propsWorkerCount() == 0 })
}
