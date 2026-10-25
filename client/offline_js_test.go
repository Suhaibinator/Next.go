//go:build js && wasm && !nextgo_module

package client

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"syscall/js"
	"testing"
	"time"

	"github.com/Suhaibinator/Next.go/internal/navigation"
	"github.com/Suhaibinator/Next.go/internal/paths"
	"github.com/Suhaibinator/Next.go/offline"
)

type offlineTestFixture struct {
	c                                        *offlineController
	worker, registration, sw, channels       js.Value
	functions                                []js.Func
	attrs                                    map[string]string
	commands                                 []offlineRequest
	registrations                            []js.Value
	registers, unregisters, updates, reloads int
	assigned, replaced                       string
	respond                                  func(offlineRequest) offlineReply
}

func offlineBrowserFixture(t *testing.T) *offlineTestFixture {
	t.Helper()
	f := &offlineTestFixture{attrs: map[string]string{}}
	oldDoc, oldWin, oldOffline, oldObservers := doc, win, documentOffline, offlineObservers
	oldPaths, oldManifest, oldURL, oldDraft, oldRefresh := deployment, routeManifest, currentURL, draftFrozen, devRefresh
	oldCancel := cancelNavigation
	t.Cleanup(func() {
		if f.c.busy {
			t.Error("offline fixture still owns a physical operation")
		}
		doc, win, documentOffline, offlineObservers = oldDoc, oldWin, oldOffline, oldObservers
		deployment, routeManifest, currentURL, draftFrozen, devRefresh = oldPaths, oldManifest, oldURL, oldDraft, oldRefresh
		cancelNavigation = oldCancel
		for _, fn := range f.functions {
			fn.Release()
		}
	})
	f.c = &offlineController{descriptor: offline.ClientDescriptor{Version: 1, Scope: "https://reader.test/reader", WorkerURL: "https://reader.test/reader/_nextgo/offline/sw.js", CatalogURL: "https://reader.test/reader/_nextgo/offline/catalog", OwnerPolicyID: "reader", RuntimeID: strings.Repeat("a", 20), ConfigurationID: strings.Repeat("b", 64)}, state: OfflineState{Configured: true, Supported: true, Phase: "disabled"}}
	f.c.state.Scope, f.c.state.OwnerPolicyID = f.c.descriptor.Scope, "reader"
	f.attrs["data-nextgo-build"], f.attrs["data-nextgo-configuration"] = f.c.descriptor.RuntimeID, f.c.descriptor.ConfigurationID
	f.channels = js.ValueOf(map[string]any{"created": 0, "closed": 0, "listeners": 0})
	channelClass := js.Global().Get("Function").New("stats", `return class { constructor() { stats.created++; let callback; this.port1={addEventListener(type,fn){callback=fn;stats.listeners++},removeEventListener(){if(callback){callback=undefined;stats.listeners--}},start(){},close(){stats.closed++}}; this.port2={postMessage(value){if(callback)callback({data:value})},close(){stats.closed++}}; } }`).Invoke(f.channels)
	f.worker = js.ValueOf(map[string]any{"scriptURL": f.c.descriptor.WorkerURL, "state": "activated"})
	f.respond = func(request offlineRequest) offlineReply {
		reply := offlineReply{Version: 1, RequestID: request.RequestID, OwnerPolicyID: "reader", RuntimeID: strings.Repeat("c", 20), ConfigurationID: strings.Repeat("d", 64), Generation: strings.Repeat("e", 64), State: "disabled", Supported: true}
		switch request.Command {
		case "enable":
			reply.State = "ready"
			reply.Enabled = true
			reply.ActiveGeneration = reply.Generation
		case "disable":
			reply.DeferredCleanup = true
			reply.ActiveGeneration = reply.Generation
		}
		return reply
	}
	f.worker.Set("postMessage", f.fn(func(_ js.Value, args []js.Value) any {
		raw := js.Global().Get("JSON").Call("stringify", args[0]).String()
		var request offlineRequest
		if err := json.Unmarshal([]byte(raw), &request); err != nil {
			t.Error(err)
			return nil
		}
		f.commands = append(f.commands, request)
		reply, _ := json.Marshal(f.respond(request))
		args[1].Index(0).Call("postMessage", js.Global().Get("JSON").Call("parse", string(reply)))
		return nil
	}))
	f.registration = js.ValueOf(map[string]any{"scope": f.c.descriptor.Scope, "active": f.worker, "installing": js.Null(), "waiting": js.Null()})
	f.registration.Set("unregister", f.fn(func(js.Value, []js.Value) any { f.unregisters++; f.registrations = nil; return f.resolve(true) }))
	f.registration.Set("update", f.fn(func(js.Value, []js.Value) any { f.updates++; return f.resolve(js.Undefined()) }))
	f.registrations = []js.Value{f.registration}
	f.sw = js.ValueOf(map[string]any{"controller": js.Null()})
	f.sw.Set("getRegistrations", f.fn(func(js.Value, []js.Value) any {
		items := js.Global().Get("Array").New(len(f.registrations))
		for i, r := range f.registrations {
			items.SetIndex(i, r)
		}
		return f.resolve(items)
	}))
	f.sw.Set("register", f.fn(func(_ js.Value, args []js.Value) any {
		f.registers++
		if args[0].String() != f.c.descriptor.WorkerURL || args[1].Get("scope").String() != f.c.descriptor.Scope {
			t.Error("registration escaped its exact descriptor")
		}
		f.registrations = []js.Value{f.registration}
		return f.resolve(f.registration)
	}))
	noop := f.fn(func(js.Value, []js.Value) any { return nil })
	body := js.ValueOf(map[string]any{"getAttribute": f.fn(func(_ js.Value, args []js.Value) any {
		if v, ok := f.attrs[args[0].String()]; ok {
			return v
		}
		return js.Null()
	}), "hasAttribute": f.fn(func(_ js.Value, args []js.Value) any { _, ok := f.attrs[args[0].String()]; return ok })})
	doc = js.ValueOf(map[string]any{"body": body, "baseURI": "https://reader.test/reader", "getElementById": f.fn(func(js.Value, []js.Value) any { return js.ValueOf(map[string]any{"removeAttribute": noop}) })})
	win = js.ValueOf(map[string]any{"URL": js.Global().Get("URL"), "MessageChannel": channelClass, "isSecureContext": true, "navigator": map[string]any{"serviceWorker": f.sw, "locks": map[string]any{"request": noop}}, "location": map[string]any{"origin": "https://reader.test", "href": "https://reader.test/reader", "assign": f.fn(func(_ js.Value, args []js.Value) any { f.assigned = args[0].String(); return nil }), "replace": f.fn(func(_ js.Value, args []js.Value) any { f.replaced = args[0].String(); return nil }), "reload": f.fn(func(js.Value, []js.Value) any { f.reloads++; return nil })}})
	documentOffline, offlineObservers = f.c, map[*offlineObserver]struct{}{}
	deployment, routeManifest, currentURL, draftFrozen, devRefresh = paths.Config{BasePath: "/reader"}, navigation.Manifest{}, "", false, nil
	cancelNavigation = nil
	return f
}
func (f *offlineTestFixture) fn(callback func(js.Value, []js.Value) any) js.Func {
	fn := js.FuncOf(callback)
	f.functions = append(f.functions, fn)
	return fn
}
func (f *offlineTestFixture) resolve(value any) js.Value {
	return js.Global().Get("Promise").Call("resolve", value)
}
func (f *offlineTestFixture) balanced(t *testing.T) {
	t.Helper()
	if f.channels.Get("listeners").Int() != 0 || f.channels.Get("closed").Int() != 2*f.channels.Get("created").Int() {
		t.Fatal("message ports/listeners were not physically cleaned")
	}
}

func TestOfflineControlsBindOldWorkerAndRetainDeferredCleanup(t *testing.T) {
	f := offlineBrowserFixture(t)
	f.sw.Set("controller", f.worker)
	if !offlineNativeNavigation() {
		t.Fatal("unknown owned controller must use native navigation")
	}
	if err := EnableOffline(context.Background()); err != nil {
		t.Fatal(err)
	}
	if len(f.commands) != 2 || f.commands[0].Command != "status" || f.commands[1].Command != "enable" || f.commands[1].RuntimeID != strings.Repeat("c", 20) || f.commands[1].ConfigurationID != strings.Repeat("d", 64) {
		t.Fatalf("mutation was not bound to actual active identity: %+v", f.commands)
	}
	if !OfflineStatus().Enabled || !offlineNativeNavigation() {
		t.Fatal("enabled controlled page lost native boundary")
	}
	if err := DisableOffline(context.Background()); err != nil {
		t.Fatal(err)
	}
	state := OfflineStatus()
	if state.Enabled || !state.CleanupPending || !state.Controlling || state.ActiveGeneration == "" || f.unregisters != 1 {
		t.Fatalf("deferred cleanup incorrectly claimed erased payloads: %+v", state)
	}
	if offlineNativeNavigation() {
		t.Fatal("confirmed disabled normal online document should regain SPA")
	}
	f.c.state.SavedDocument = true
	if !offlineNativeNavigation() {
		t.Fatal("saved document must stay native after disable")
	}
	f.balanced(t)
}

func TestOfflineDisableReconcilesWithoutEnablingOrUnregisteringOnError(t *testing.T) {
	f := offlineBrowserFixture(t)
	f.registrations = nil
	old := f.respond
	f.respond = func(request offlineRequest) offlineReply {
		r := old(request)
		r.DeferredCleanup = false
		r.ActiveGeneration = ""
		return r
	}
	if err := DisableOffline(context.Background()); err != nil {
		t.Fatal(err)
	}
	if f.registers != 1 || f.unregisters != 1 || OfflineStatus().CleanupPending {
		t.Fatalf("explicit cleanup was not completed: %+v", OfflineStatus())
	}
	for _, request := range f.commands {
		if request.Command == "enable" {
			t.Fatal("cleanup enabled saved documents")
		}
	}
	f.registrations = []js.Value{f.registration}
	f.respond = func(request offlineRequest) offlineReply {
		r := old(request)
		if request.Command == "disable" {
			r.State = "failed"
			r.Error = "client-capacity"
		}
		return r
	}
	if err := DisableOffline(context.Background()); err == nil || f.unregisters != 1 {
		t.Fatal("refused cleanup unregistered an owner with retained live clients")
	}
	f.balanced(t)
}

func TestOfflineUpdateObservesWaitingWithoutActivatingOrSendingCheck(t *testing.T) {
	f := offlineBrowserFixture(t)
	waiting := js.ValueOf(map[string]any{"scriptURL": f.c.descriptor.WorkerURL, "postMessage": f.worker.Get("postMessage")})
	f.registration.Set("waiting", waiting)
	if err := CheckOfflineUpdate(context.Background()); err != nil {
		t.Fatal(err)
	}
	if f.updates != 1 || OfflineStatus().WaitingGeneration != strings.Repeat("e", 64) || OfflineStatus().Phase != "waiting" {
		t.Fatalf("waiting update was not observed: %+v", OfflineStatus())
	}
	for _, request := range f.commands {
		if request.Command != "status" {
			t.Fatalf("update attempted a worker mutation: %+v", request)
		}
	}
	f.balanced(t)
}

func TestOfflineRegistrationRejectsEveryOverlappingForeignOwner(t *testing.T) {
	for _, scope := range []string{"https://reader.test/", "https://reader.test/reader", "https://reader.test/reader/child", "https://reader.test/reader2"} {
		t.Run(scope, func(t *testing.T) {
			f := offlineBrowserFixture(t)
			foreign := js.ValueOf(map[string]any{"scope": scope, "active": map[string]any{"scriptURL": "https://reader.test/foreign.js"}})
			f.registrations = []js.Value{foreign}
			if _, err := f.c.registration(context.Background()); !errors.Is(err, ErrOfflineConflict) {
				t.Fatal("foreign overlapping registration accepted", err)
			}
			if len(f.commands) != 0 {
				t.Fatal("untrusted worker received a command")
			}
		})
	}
}

func TestOfflineSavedNavigationAndOptOutSubscriptions(t *testing.T) {
	f := offlineBrowserFixture(t)
	f.c.state.SavedDocument = true
	if err := Navigate("/reader/next?q=one#part"); err != nil || f.assigned != "https://reader.test/reader/next?q=one#part" {
		t.Fatal("saved Navigate did not use real document navigation", err, f.assigned)
	}
	if err := Replace("/reader"); err != nil || f.replaced != "https://reader.test/reader" {
		t.Fatal("saved Replace did not use real document replacement", err)
	}
	Refresh()
	if f.reloads != 1 {
		t.Fatal("saved Refresh did not reload document")
	}
	if !errors.Is(Prefetch("/reader/next"), ErrOfflineNativeNavigation) {
		t.Fatal("saved document prefetched JSON navigation")
	}
	for _, target := range []string{"https://foreign.test/reader", "/reader/api/private", "/outside", "https://user@reader.test/reader"} {
		if err := Navigate(target); err == nil {
			t.Fatal("unsafe native target accepted", target)
		}
	}
	documentOffline = nil
	called := 0
	scope := &Scope{}
	if err := SubscribeOffline(scope, func(state OfflineState) {
		called++
		if state.Configured || state.Phase != "unavailable" {
			t.Error("optout subscription fabricated configuration")
		}
	}); err != nil || called != 1 {
		t.Fatal("unconfigured controls failed graceful hydration", err)
	}
	scope.dispose()
	if len(offlineObservers) != 0 {
		t.Fatal("detached scope retained offline callback")
	}
	if saved, err := initializeOfflineDocument(); saved || err != nil || documentOffline != nil || f.registers != 0 {
		t.Fatal("ordinary optout document inspected or registered service worker", err)
	}
}

func TestOfflineMessageCancellationClosesPortsWithoutRetry(t *testing.T) {
	f := offlineBrowserFixture(t)
	f.worker.Set("postMessage", f.fn(func(js.Value, []js.Value) any { return nil }))
	ctx, cancel := context.WithTimeout(context.Background(), time.Millisecond)
	defer cancel()
	if _, err := f.c.message(ctx, f.worker, "status", f.c.descriptor.RuntimeID, f.c.descriptor.ConfigurationID); !errors.Is(err, ErrOfflineUnknown) {
		t.Fatal("canceled message pretended operation was confirmed", err)
	}
	if f.channels.Get("created").Int() != 1 {
		t.Fatal("canceled command was automatically retried")
	}
	f.balanced(t)
}

func TestOfflineCanceledRegistrationRetainsSinglePhysicalOperation(t *testing.T) {
	f := offlineBrowserFixture(t)
	started := make(chan struct{})
	resolve := js.Undefined()
	executor := f.fn(func(_ js.Value, args []js.Value) any { resolve = args[0]; return nil })
	pending := js.Global().Get("Promise").New(executor)
	f.sw.Set("getRegistrations", f.fn(func(js.Value, []js.Value) any { close(started); return pending }))
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	settled := make(chan error, 1)
	go func() { settled <- EnableOffline(ctx) }()
	joined := false
	defer func() {
		// Failure cleanup still settles and joins the actual browser operation
		// before the fixture restores globals or releases its js.Func values.
		resolve.Invoke(js.Global().Get("Array").New())
		if !joined {
			select {
			case <-settled:
			case <-time.After(time.Second):
				t.Error("offline registration worker did not physically settle")
			}
		}
	}()
	<-started
	cancel()
	if err := DisableOffline(context.Background()); !errors.Is(err, ErrOfflineBusy) {
		t.Fatal("caller cancellation admitted another operation before browser settlement", err)
	}
	select {
	case err := <-settled:
		t.Fatal("registration promise was abandoned before physical settlement", err)
	default:
	}
	resolve.Invoke(js.Global().Get("Array").New())
	err := <-settled
	joined = true
	if !errors.Is(err, ErrOfflineUnknown) {
		t.Fatal("unabortable registration reported a confirmed result after cancellation", err)
	}
	if f.c.busy || f.registers != 0 || len(f.commands) != 0 {
		t.Fatal("canceled registration progressed to a mutation")
	}
}

func TestOfflineSavedDescriptorIsValidatedEvenWhenCapabilityUnsupported(t *testing.T) {
	f := offlineBrowserFixture(t)
	raw, _ := json.Marshal(f.c.descriptor)
	f.attrs[offline.ClientDescriptorAttribute] = string(raw)
	f.attrs["data-nextgo-offline-saved"] = "1"
	win.Get("navigator").Set("locks", js.Null())
	saved, err := initializeOfflineDocument()
	if err != nil || !saved || !OfflineStatus().Configured || OfflineStatus().Supported || !offlineNativeNavigation() {
		t.Fatalf("unsupported browser lost useful saved HTML/native boundary: %+v %v", OfflineStatus(), err)
	}
	if err := EnableOffline(context.Background()); !errors.Is(err, ErrOfflineUnsupported) {
		t.Fatal("unsupported browser attempted registration", err)
	}
	if len(f.commands) != 0 || f.registers != 0 {
		t.Fatal("unsupported configuration reached a worker")
	}
	descriptor := f.c.descriptor
	descriptor.Scope += "/" // Mount root must be controlled without a trailing slash.
	raw, _ = json.Marshal(descriptor)
	f.attrs[offline.ClientDescriptorAttribute] = string(raw)
	if _, err := initializeOfflineDocument(); !errors.Is(err, ErrOfflineProtocol) {
		t.Fatal("inconsistent mounted scope was accepted", err)
	}
}
