//go:build js && wasm && !nextgo_module

package client

import (
	"context"
	"errors"
	"reflect"
	"strings"
	"syscall/js"
	"testing"
	"time"
)

type offlineRealmFixture struct {
	f                                                            *offlineTestFixture
	frame, host, childDoc, childWin, childSW, candidate, waiting js.Value
	events                                                       map[string]js.Value
	trace                                                        []string
	parentReads, childReads, lookups, created, removed, updates  int
	childEntries, parentEntries                                  []js.Value
	beforeChildRead, beforeLookup, beforeUpdate                  func()
	afterRemove                                                  func()
	holdRead, holdUpdate                                         js.Value
	reply                                                        func(string, js.Value) js.Value
	removeFailure, listenerFailure, errorLoad                    bool
}

func freshOfflineRealmFixture(t *testing.T) *offlineRealmFixture {
	t.Helper()
	f := offlineBrowserFixture(t)
	x := &offlineRealmFixture{f: f, events: map[string]js.Value{}}
	if offlineRealmInUse != nil {
		t.Fatal("previous realm charge survived fixture")
	}
	t.Cleanup(func() {
		if offlineRealmInUse != nil {
			t.Error("realm/global activity still charged")
		}
		f.balanced(t)
	})
	f.registration.Set("active", js.Null())
	f.sw.Set("controller", f.worker)
	x.parentEntries = []js.Value{f.registration}
	f.sw.Set("getRegistrations", f.fn(func(this js.Value, _ []js.Value) any {
		if !this.Equal(f.sw) {
			t.Error("parent inventory receiver changed")
		}
		x.parentReads++
		x.trace = append(x.trace, "parent-list")
		value := f.resolve(reacquireArray(x.parentEntries))
		if x.reply != nil {
			return x.reply("parent-list", value)
		}
		return value
	}))
	f.sw.Set("getRegistration", f.fn(func(this js.Value, args []js.Value) any {
		if !this.Equal(f.sw) || len(args) != 1 || args[0].String() != f.c.descriptor.Scope {
			t.Error("parent target escaped descriptor")
		}
		x.trace = append(x.trace, "parent-target")
		value := f.resolve(f.registration)
		if x.reply != nil {
			return x.reply("parent-target", value)
		}
		return value
	}))
	x.waiting = js.ValueOf(map[string]any{"scriptURL": f.c.descriptor.WorkerURL, "state": "installed", "postMessage": f.worker.Get("postMessage")})
	x.candidate = js.ValueOf(map[string]any{"scope": f.c.descriptor.Scope, "active": f.worker, "waiting": js.Null(), "installing": js.Null()})
	x.candidate.Set("update", f.fn(func(this js.Value, _ []js.Value) any {
		if !this.Equal(x.candidate) || !x.frame.Get("isConnected").Bool() {
			t.Error("update lost live exact child receiver")
		}
		x.updates++
		x.trace = append(x.trace, "update")
		if x.beforeUpdate != nil {
			x.beforeUpdate()
		}
		x.candidate.Set("waiting", x.waiting)
		if x.holdUpdate.Truthy() {
			return x.holdUpdate
		}
		value := f.resolve(x.candidate)
		if x.reply != nil {
			return x.reply("candidate", value)
		}
		return value
	}))
	x.childEntries = []js.Value{x.candidate}
	x.childSW = js.ValueOf(map[string]any{"controller": f.worker})
	x.childSW.Set("getRegistrations", f.fn(func(this js.Value, _ []js.Value) any {
		if !this.Equal(x.childSW) {
			t.Error("fresh inventory receiver changed")
		}
		x.childReads++
		x.trace = append(x.trace, "child-list")
		if x.beforeChildRead != nil {
			x.beforeChildRead()
		}
		if x.holdRead.Truthy() {
			return x.holdRead
		}
		value := f.resolve(reacquireArray(x.childEntries))
		if x.reply != nil {
			return x.reply("child-list", value)
		}
		return value
	}))
	x.childSW.Set("getRegistration", f.fn(func(this js.Value, args []js.Value) any {
		x.lookups++
		x.trace = append(x.trace, "child-target")
		if !this.Equal(x.childSW) || len(args) != 1 || args[0].String() != f.c.descriptor.Scope {
			t.Error("fresh target escaped descriptor")
		}
		if x.beforeLookup != nil {
			x.beforeLookup()
		}
		value := f.resolve(x.candidate)
		if x.reply != nil {
			return x.reply("candidate", value)
		}
		return value
	}))
	x.childDoc = js.ValueOf(map[string]any{"readyState": "complete", "body": map[string]any{"childNodes": js.Global().Get("Array").New()}})
	x.childWin = js.ValueOf(map[string]any{"origin": "https://reader.test", "closed": false, "location": map[string]any{"href": "about:blank"}, "navigator": map[string]any{"serviceWorker": x.childSW}})
	x.frame = js.ValueOf(map[string]any{"isConnected": false, "ownerDocument": doc, "parentNode": js.Null(), "contentDocument": x.childDoc, "contentWindow": x.childWin})
	x.frame.Set("setAttribute", f.fn(func(js.Value, []js.Value) any { return nil }))
	// Throwing is modeled in JS: a panic inside a Go syscall/js callback would
	// instead terminate the WASM program and would not model a host exception.
	removeListener := js.Global().Get("Function").New("events", "state", `return function(type,fn){if(state.fail)throw new Error('remove failed');if(events[type]===fn)delete events[type]}`)
	eventObject := js.Global().Get("Object").New()
	listenerState := js.ValueOf(map[string]any{"fail": false})
	x.frame.Set("addEventListener", f.fn(func(_ js.Value, args []js.Value) any {
		x.events[args[0].String()] = args[1]
		eventObject.Set(args[0].String(), args[1])
		return nil
	}))
	x.frame.Set("removeEventListener", removeListener.Invoke(eventObject, listenerState))
	x.host = js.ValueOf(map[string]any{})
	x.host.Set("appendChild", f.fn(func(_ js.Value, args []js.Value) any {
		if !args[0].Equal(x.frame) {
			t.Error("unexpected appended frame")
		}
		x.frame.Set("parentNode", x.host)
		x.frame.Set("isConnected", true)
		if x.listenerFailure {
			listenerState.Set("fail", true)
		}
		event := "load"
		if x.errorLoad {
			event = "error"
		}
		if callback, ok := x.events[event]; ok {
			callback.Invoke()
		}
		return x.frame
	}))
	// Native removal failure is an actual throwing JS host method too.
	removeState := js.ValueOf(map[string]any{"fail": false})
	nativeRemove := js.Global().Get("Function").New("state", "remove", `return function(frame){if(state.fail)throw new Error('remove failed');return remove(frame)}`)
	remove := f.fn(func(_ js.Value, args []js.Value) any {
		if !args[0].Equal(x.frame) {
			t.Error("removed unrelated frame")
		}
		x.removed++
		x.trace = append(x.trace, "remove")
		x.frame.Set("isConnected", false)
		x.frame.Set("parentNode", js.Null())
		x.childWin.Set("closed", true)
		if x.afterRemove != nil {
			x.afterRemove()
		}
		return x.frame
	})
	x.host.Set("removeChild", nativeRemove.Invoke(removeState, remove))
	doc.Set("documentElement", x.host)
	doc.Set("createElement", f.fn(func(_ js.Value, args []js.Value) any {
		if len(args) != 1 || args[0].String() != "iframe" {
			t.Error("recovery created a non-frame node")
		}
		x.created++
		if x.removeFailure {
			removeState.Set("fail", true)
		}
		return x.frame
	}))
	// Test-only reset functions let quarantine tests perform a proven cleanup at
	// the end; production never forgets a failed charge on a timer.
	x.frame.Set("testAllowCleanup", f.fn(func(js.Value, []js.Value) any {
		removeState.Set("fail", false)
		listenerState.Set("fail", false)
		return nil
	}))
	original := f.respond
	f.respond = func(request offlineRequest) offlineReply {
		x.trace = append(x.trace, "status")
		reply := original(request)
		reply.State = "ready"
		reply.Enabled = true
		reply.ActiveGeneration = strings.Repeat("d", 64)
		return reply
	}
	return x
}

// Signal only when the real Promise's then is attached: call() has already
// charged pending work by then, so cancellation assertions need no sleeps.
func offlineRealmHeldPromise(x *offlineRealmFixture) (js.Value, js.Value, <-chan struct{}) {
	var resolve js.Value
	held := js.Global().Get("Promise").New(x.f.fn(func(_ js.Value, args []js.Value) any { resolve = args[0]; return nil }))
	started := make(chan struct{}, 1)
	nativeThen := held.Get("then")
	held.Set("then", x.f.fn(func(this js.Value, args []js.Value) any {
		next := nativeThen.Call("call", this, args[0], args[1])
		started <- struct{}{}
		return next
	}))
	return held, resolve, started
}

func TestOfflineFreshRealmUpdateUsesBoundedLiveProofAndParentTail(t *testing.T) {
	x := freshOfflineRealmFixture(t)
	if err := CheckOfflineUpdate(context.Background()); err != nil {
		t.Fatal(err)
	}
	want := []string{"parent-list", "parent-target", "child-list", "child-target", "parent-list", "child-list", "update", "parent-list", "child-list", "status", "remove", "parent-list", "status"}
	if !reflect.DeepEqual(x.trace, want) {
		t.Fatalf("proof/update/retirement order: %v", x.trace)
	}
	if x.parentReads != 4 || x.childReads != 3 || x.lookups != 1 || x.updates != 1 || x.created != 1 || x.removed != 1 || offlineRealmInUse != nil {
		t.Fatalf("recovery exceeded or skipped exact work: %+v", x)
	}
	if got := OfflineStatus(); got.ActiveGeneration != strings.Repeat("d", 64) || got.WaitingGeneration != strings.Repeat("e", 64) || got.Phase != "waiting" {
		t.Fatalf("wrong live-parent status/waiting copy: %+v", got)
	}
	if x.f.registers != 0 || x.f.unregisters != 0 {
		t.Fatal("update crossed enable/disable boundary")
	}
	for _, request := range x.f.commands {
		if request.Command != "status" {
			t.Fatal("fresh path sent mutating worker message")
		}
	}
}

func TestOfflineFreshRealmRejectsForeignOrChangedProofBeforeUpdate(t *testing.T) {
	for _, mode := range []string{"foreign", "overlap", "duplicate", "empty", "replaced", "parent-controller", "child-controller", "document-content", "document-replaced"} {
		t.Run(mode, func(t *testing.T) {
			x := freshOfflineRealmFixture(t)
			switch mode {
			case "foreign":
				x.candidate.Set("active", js.ValueOf(map[string]any{"scriptURL": "https://reader.test/foreign.js"}))
			case "overlap":
				x.childEntries = append(x.childEntries, js.ValueOf(map[string]any{"scope": "https://reader.test/", "active": x.f.worker}))
			case "duplicate":
				x.childEntries = append(x.childEntries, x.candidate)
			case "empty":
				x.candidate.Set("active", js.Null())
			case "replaced":
				x.beforeLookup = func() { x.childEntries = []js.Value{x.f.registration} }
			case "parent-controller":
				x.beforeLookup = func() { x.f.sw.Set("controller", js.Null()) }
			case "child-controller":
				x.beforeLookup = func() { x.childSW.Set("controller", js.Null()) }
			case "document-content":
				x.beforeLookup = func() { x.childDoc.Get("body").Set("childNodes", js.ValueOf([]any{"unexpected"})) }
			case "document-replaced":
				x.beforeLookup = func() { x.frame.Set("contentDocument", js.ValueOf(map[string]any{})) }
			}
			if err := CheckOfflineUpdate(context.Background()); err == nil {
				t.Fatal("unsafe proof succeeded")
			}
			if x.updates != 0 || len(x.f.commands) != 0 || x.removed != 1 || offlineRealmInUse != nil {
				t.Fatal("rejected proof mutated worker or escaped physical cleanup")
			}
		})
	}
}

func TestOfflineFreshRealmIsUpdateOnlyAndRequiresTypedEmptyLookup(t *testing.T) {
	for _, command := range []string{"status", "enable", "disable", "update-owned", "update-foreign", "update-no-controller", "canceled"} {
		t.Run(command, func(t *testing.T) {
			x := freshOfflineRealmFixture(t)
			ctx := context.Background()
			operation := "update"
			switch command {
			case "status", "enable", "disable":
				operation = command
			case "update-owned":
				x.f.registration.Set("active", x.f.worker)
			case "update-foreign":
				x.f.registration.Set("active", js.ValueOf(map[string]any{"scriptURL": "https://reader.test/not-owned.js"}))
			case "update-no-controller":
				x.f.sw.Set("controller", js.Null())
			case "canceled":
				var cancel context.CancelFunc
				ctx, cancel = context.WithCancel(ctx)
				cancel()
			}
			_ = offlineOperation(ctx, operation)
			if x.created != 0 || x.updates != 0 || offlineRealmInUse != nil {
				t.Fatal("non-trigger path allocated child authority")
			}
		})
	}
}

func TestOfflineFreshRealmCancellationRetainsPhysicalPromiseAndFrame(t *testing.T) {
	for _, phase := range []string{"read", "update"} {
		t.Run(phase, func(t *testing.T) {
			x := freshOfflineRealmFixture(t)
			held, resolve, started := offlineRealmHeldPromise(x)
			if phase == "read" {
				x.holdRead = held
			} else {
				x.holdUpdate = held
			}
			ctx, cancel := context.WithCancel(context.Background())
			done := make(chan error, 1)
			joined := false
			defer func() {
				cancel()
				resolve.Invoke(js.Undefined())
				if !joined {
					select {
					case <-done:
					case <-time.After(time.Second):
						t.Error("fixture could not join held native work")
					}
				}
			}()
			go func() { done <- CheckOfflineUpdate(ctx) }()
			select {
			case <-started:
			case <-time.After(time.Second):
				t.Fatal("native operation did not start")
			}
			cancel()
			if err := CheckOfflineUpdate(context.Background()); !errors.Is(err, ErrOfflineBusy) {
				t.Fatal("second operation entered charged frame", err)
			}
			select {
			case err := <-done:
				joined = true
				t.Fatal("logical cancel discarded physical job", err)
			default:
			}
			if offlineRealmInUse == nil || offlineRealmInUse.pending != 1 || !x.frame.Get("isConnected").Bool() || x.removed != 0 || !x.f.c.busy {
				t.Fatal("live physical work lost frame/admission")
			}
			resolve.Invoke(reacquireArray(x.childEntries))
			select {
			case err := <-done:
				joined = true
				if !errors.Is(err, ErrOfflineUnknown) {
					t.Fatal(err)
				}
			case <-time.After(time.Second):
				t.Fatal("physical promise did not join")
			}
			if x.removed != 1 || offlineRealmInUse != nil || x.f.c.busy || len(x.f.commands) != 0 {
				t.Fatal("canceled operation published or leaked resources")
			}
		})
	}
}

func TestOfflineFreshRealmQuarantinesUnconfirmedCleanupAcrossControllerReplacement(t *testing.T) {
	for _, failure := range []string{"frame", "listener"} {
		t.Run(failure, func(t *testing.T) {
			x := freshOfflineRealmFixture(t)
			if failure == "frame" {
				x.removeFailure = true
			} else {
				x.listenerFailure = true
			}
			err := CheckOfflineUpdate(context.Background())
			if !errors.Is(err, ErrOfflineUnknown) || offlineRealmInUse == nil || !x.frame.Get("isConnected").Bool() {
				t.Fatal("failed cleanup claimed physical retirement", err)
			}
			old := documentOffline
			replacement := *old
			documentOffline = &replacement
			if err := CheckOfflineUpdate(context.Background()); !errors.Is(err, ErrOfflineBusy) {
				t.Fatal("controller replacement bypassed quarantined slot", err)
			}
			documentOffline = old
			x.frame.Call("testAllowCleanup")
			if err := offlineRealmInUse.close(); err != nil {
				t.Fatal("test could not physically retire fixture", err)
			}
			if offlineRealmInUse != nil || x.removed != 1 {
				t.Fatal("proven cleanup did not release slot")
			}
		})
	}
}

func TestOfflineFreshRealmTailRejectsControllerChangeAfterRemoval(t *testing.T) {
	x := freshOfflineRealmFixture(t)
	x.afterRemove = func() { x.f.sw.Set("controller", js.Null()) }
	if err := CheckOfflineUpdate(context.Background()); err == nil {
		t.Fatal("stale post-frame parent authority succeeded")
	}
	if x.updates != 1 || x.removed != 1 || len(x.f.commands) != 1 || OfflineStatus().WaitingGeneration != "" {
		t.Fatal("stale parent status was published")
	}
}

func TestOfflineFreshRealmEveryRecoveryReadRetainsPhysicalSettlement(t *testing.T) {
	// Trigger reads use the old path and its existing tests. These seven reads
	// belong to the new recovery transaction, including the live-parent tail.
	for _, at := range []int{1, 2, 3, 4, 5, 6, 7} {
		t.Run(string(rune('0'+at)), func(t *testing.T) {
			x := freshOfflineRealmFixture(t)
			held, resolve, started := offlineRealmHeldPromise(x)
			recoveryReads := 0
			x.reply = func(label string, value js.Value) js.Value {
				// The parent trigger runs before a frame is created. Update's ignored
				// resolution also uses candidate; only the first candidate is lookup.
				if x.created == 0 || label == "candidate" && x.updates != 0 {
					return value
				}
				recoveryReads++
				if recoveryReads == at {
					return held
				}
				return value
			}
			ctx, cancel := context.WithCancel(context.Background())
			done := make(chan error, 1)
			joined := false
			defer func() {
				cancel()
				resolve.Invoke(js.Undefined())
				if !joined {
					select {
					case <-done:
					case <-time.After(time.Second):
						t.Error("fixture could not join held native read")
					}
				}
			}()
			go func() { done <- CheckOfflineUpdate(ctx) }()
			select {
			case <-started:
			case <-time.After(time.Second):
				t.Fatal("read did not reach selected physical boundary")
			}
			cancel()
			select {
			case err := <-done:
				joined = true
				t.Fatal("read canceled without settlement", err)
			default:
			}
			if !x.f.c.busy {
				t.Fatal("operation admission released before physical read")
			}
			if at < 7 {
				if offlineRealmInUse == nil || offlineRealmInUse.pending != 1 || !x.frame.Get("isConnected").Bool() {
					t.Fatal("child read lost live frame")
				}
			} else if offlineRealmInUse == nil || !offlineRealmInUse.retired || offlineRealmInUse.pending != 1 || !offlineRealmInUse.frame.IsUndefined() || !offlineRealmInUse.window.IsUndefined() || !offlineRealmInUse.document.IsUndefined() || !offlineRealmInUse.container.IsUndefined() || !offlineRealmInUse.controller.IsUndefined() || x.removed != 1 {
				t.Fatal("parent tail lost its admission lease or retained a child object")
			}
			// Result values after cancellation must never be inspected or published.
			resolve.Invoke(js.Undefined())
			select {
			case err := <-done:
				joined = true
				if !errors.Is(err, ErrOfflineUnknown) {
					t.Fatal(err)
				}
			case <-time.After(time.Second):
				t.Fatal("settled read did not join")
			}
			if x.removed != 1 || offlineRealmInUse != nil || x.f.c.busy || OfflineStatus().WaitingGeneration != "" {
				t.Fatal("canceled recovery escaped cleanup or published status")
			}
		})
	}
}

func TestOfflineFreshRealmInstallationKeepsLiveOwnerAndRemovesStateListener(t *testing.T) {
	for _, finish := range []string{"installed", "activated", "redundant", "canceled"} {
		t.Run(finish, func(t *testing.T) {
			x := freshOfflineRealmFixture(t)
			installing := js.ValueOf(map[string]any{"scriptURL": x.f.c.descriptor.WorkerURL, "state": "installing"})
			var callback js.Value
			attached := make(chan struct{}, 1)
			removed := 0
			installing.Set("addEventListener", x.f.fn(func(_ js.Value, args []js.Value) any {
				if args[0].String() != "statechange" {
					t.Error("unexpected worker listener")
				}
				callback = args[1]
				attached <- struct{}{}
				return nil
			}))
			installing.Set("removeEventListener", x.f.fn(func(_ js.Value, args []js.Value) any {
				if !args[1].Equal(callback) {
					t.Error("removed another worker callback")
				}
				removed++
				return nil
			}))
			x.beforeUpdate = func() { x.candidate.Set("installing", installing) }
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			done := make(chan error, 1)
			joined := false
			defer func() {
				cancel()
				if !joined {
					select {
					case <-done:
					case <-time.After(time.Second):
						t.Error("fixture could not join installation")
					}
				}
			}()
			go func() { done <- CheckOfflineUpdate(ctx) }()
			select {
			case <-attached:
			case <-time.After(time.Second):
				t.Fatal("installation listener not attached")
			}
			if !x.frame.Get("isConnected").Bool() || offlineRealmInUse == nil || x.removed != 0 {
				t.Fatal("installation lost live realm")
			}
			select {
			case err := <-done:
				joined = true
				t.Fatal("installation returned before state", err)
			default:
			}
			if finish == "canceled" {
				cancel()
			} else {
				installing.Set("state", finish)
				callback.Invoke()
			}
			select {
			case err := <-done:
				joined = true
				if (finish == "installed" || finish == "activated") && err != nil {
					t.Fatal(err)
				}
				if finish == "redundant" && !errors.Is(err, ErrOfflineProtocol) {
					t.Fatal(err)
				}
				if finish == "canceled" && !errors.Is(err, ErrOfflineUnknown) {
					t.Fatal(err)
				}
			case <-time.After(time.Second):
				t.Fatal("installation state did not settle")
			}
			if removed != 1 || x.removed != 1 || offlineRealmInUse != nil {
				t.Fatal("installation did not retire listener before frame")
			}
		})
	}
}

func TestOfflineFreshRealmNativeLoadErrorRejectsWithoutUpdate(t *testing.T) {
	x := freshOfflineRealmFixture(t)
	x.errorLoad = true
	if err := CheckOfflineUpdate(context.Background()); !errors.Is(err, ErrOfflineConflict) {
		t.Fatal("load error did not fail closed", err)
	}
	if x.updates != 0 || x.childReads != 0 || len(x.f.commands) != 0 || x.removed != 1 || offlineRealmInUse != nil {
		t.Fatal("failed load acquired registration or left realm charged")
	}
}

func TestOfflineFreshRealmExaminesInventoryBoundaryAndLastEntry(t *testing.T) {
	for _, mode := range []string{"64-unrelated", "65", "64-last-foreign", "64-last-malformed", "parent-late-foreign"} {
		t.Run(mode, func(t *testing.T) {
			x := freshOfflineRealmFixture(t)
			for len(x.childEntries) < 64 {
				x.childEntries = append(x.childEntries, js.ValueOf(map[string]any{"scope": "https://reader.test/unrelated/"}))
			}
			switch mode {
			case "65":
				x.childEntries = append(x.childEntries, js.ValueOf(map[string]any{"scope": "https://reader.test/unrelated/"}))
			case "64-last-foreign":
				x.childEntries[63] = js.ValueOf(map[string]any{"scope": x.f.c.descriptor.Scope + "/foreign", "active": map[string]any{"scriptURL": "https://reader.test/foreign.js"}})
			case "64-last-malformed":
				x.childEntries[63] = js.ValueOf(map[string]any{"scope": 42})
			case "parent-late-foreign":
				x.beforeLookup = func() {
					x.parentEntries = append(x.parentEntries, js.ValueOf(map[string]any{"scope": "https://reader.test/", "active": map[string]any{"scriptURL": "https://reader.test/foreign.js"}}))
				}
			}
			err := CheckOfflineUpdate(context.Background())
			if mode == "64-unrelated" {
				if err != nil || x.updates != 1 {
					t.Fatal("bounded unrelated inventory incorrectly refused", err)
				}
			} else if err == nil || x.updates != 0 || len(x.f.commands) != 0 {
				t.Fatal("late or oversized inventory granted update authority", err)
			}
			if x.removed != 1 || offlineRealmInUse != nil {
				t.Fatal("inventory outcome did not retire frame")
			}
		})
	}
}

func TestOfflineFreshRealmWaitingReceiptQuarantinesUnconfirmedPortCleanup(t *testing.T) {
	for _, failure := range []string{"listener", "port1", "port2"} {
		t.Run(failure, func(t *testing.T) {
			x := freshOfflineRealmFixture(t)
			state := js.ValueOf(map[string]any{"failure": failure})
			// Forward the actual fixture channel operations, throwing before just the
			// selected native operation. Successful closure still has original counters.
			channel := js.Global().Get("Function").New("Base", "state", `return class extends Base {constructor(){super();for(const [name,port] of [['port1',this.port1],['port2',this.port2]]){const close=port.close;port.close=function(){if(state.failure===name)throw new Error('close failed');return close.call(this)};}const remove=this.port1.removeEventListener;this.port1.removeEventListener=function(...args){if(state.failure==='listener')throw new Error('remove failed');return remove.apply(this,args)}}}`).Invoke(win.Get("MessageChannel"), state)
			win.Set("MessageChannel", channel)
			err := CheckOfflineUpdate(context.Background())
			if !errors.Is(err, ErrOfflineUnknown) || offlineRealmInUse == nil || x.removed != 0 || !x.frame.Get("isConnected").Bool() {
				t.Fatal("unknown waiting-port cleanup released frame authority", err)
			}
			if x.updates != 1 || len(x.f.commands) != 1 || OfflineStatus().WaitingGeneration != "" {
				t.Fatal("failed receipt cleanup published or sent a parent tail")
			}
			if err := CheckOfflineUpdate(context.Background()); !errors.Is(err, ErrOfflineBusy) {
				t.Fatal("unconfirmed ports allowed another realm", err)
			}
			state.Set("failure", "")
			if err := offlineRealmInUse.close(); err != nil {
				t.Fatal("test could not physically close retained ports", err)
			}
			if x.removed != 1 || offlineRealmInUse != nil {
				t.Fatal("known closure did not retire quarantine")
			}
			x.f.balanced(t)
		})
	}
}

func TestOfflineFreshRealmCanceledWaitingMessageClosesParentPortsBeforeFrame(t *testing.T) {
	x := freshOfflineRealmFixture(t)
	started := make(chan struct{}, 1)
	var transferred js.Value
	x.waiting.Set("postMessage", x.f.fn(func(_ js.Value, args []js.Value) any {
		transferred = args[1].Index(0)
		started <- struct{}{}
		return nil
	}))
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	joined := false
	defer func() {
		cancel()
		if !joined {
			select {
			case <-done:
			case <-time.After(time.Second):
				t.Error("waiting message fixture did not join")
			}
		}
	}()
	go func() { done <- CheckOfflineUpdate(ctx) }()
	select {
	case <-started:
	case <-time.After(time.Second):
		t.Fatal("waiting status did not start")
	}
	if offlineRealmInUse == nil || offlineRealmInUse.pending != 0 || len(offlineRealmInUse.ports) != 2 || !x.frame.Get("isConnected").Bool() {
		t.Fatal("waiting receipt lost its owned resources")
	}
	cancel()
	select {
	case err := <-done:
		joined = true
		if !errors.Is(err, ErrOfflineUnknown) {
			t.Fatal(err)
		}
	case <-time.After(time.Second):
		t.Fatal("canceled waiting message did not close")
	}
	if offlineRealmInUse != nil || x.removed != 1 || x.f.c.busy || OfflineStatus().WaitingGeneration != "" {
		t.Fatal("canceled waiting status published or leaked")
	}
	x.f.balanced(t)
	// A worker may finish its own read-only status later. Its transferred parent
	// port has been closed and the Go listener removed before frame detachment.
	transferred.Call("postMessage", map[string]any{"late": true})
	if OfflineStatus().WaitingGeneration != "" {
		t.Fatal("late reply reached retired Go callback")
	}
}

func TestOfflineFreshRealmParentTailLeaseBlocksReplacementUntilTrueJoin(t *testing.T) {
	x := freshOfflineRealmFixture(t)
	held, resolve, started := offlineRealmHeldPromise(x)
	x.reply = func(label string, value js.Value) js.Value {
		if label == "parent-list" && x.removed == 1 {
			return held
		}
		return value
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	joined := false
	defer func() {
		cancel()
		resolve.Invoke(reacquireArray(x.parentEntries))
		if !joined {
			select {
			case <-done:
			case <-time.After(time.Second):
				t.Error("parent tail fixture did not physically join")
			}
		}
	}()
	go func() { done <- CheckOfflineUpdate(ctx) }()
	select {
	case <-started:
	case <-time.After(time.Second):
		t.Fatal("parent physical tail did not start")
	}
	lease := offlineRealmInUse
	if lease == nil || !lease.retired || lease.pending != 1 || !lease.frame.IsUndefined() || !lease.window.IsUndefined() || !lease.document.IsUndefined() || !lease.container.IsUndefined() || !lease.controller.IsUndefined() || x.removed != 1 {
		t.Fatal("child retirement and parent lease were not separated")
	}
	old := documentOffline
	replacement := *old
	replacement.busy = false
	replacement.state.Phase = "ready"
	replacement.state.LastError = ""
	documentOffline = &replacement
	defer func() { documentOffline = old }()
	scope := &Scope{}
	defer scope.dispose()
	observed := []OfflineState{}
	if err := SubscribeOffline(scope, func(state OfflineState) { observed = append(observed, state) }); err != nil {
		t.Fatal(err)
	}
	if len(observed) != 1 || observed[0].Phase != "ready" {
		t.Fatal("replacement subscription did not receive its own state")
	}
	if err := CheckOfflineUpdate(context.Background()); !errors.Is(err, ErrOfflineBusy) {
		t.Fatal("new controller bypassed physically outstanding parent read", err)
	}
	cancel()
	select {
	case err := <-done:
		joined = true
		t.Fatal("logical cancellation released parent native promise", err)
	default:
	}
	if offlineRealmInUse != lease {
		t.Fatal("canceled parent tail forgot its charge")
	}
	resolve.Invoke(reacquireArray(x.parentEntries))
	select {
	case err := <-done:
		joined = true
		if !errors.Is(err, ErrOfflineUnknown) {
			t.Fatal(err)
		}
	case <-time.After(time.Second):
		t.Fatal("settled parent tail did not release")
	}
	if offlineRealmInUse != nil || old.busy || len(x.f.commands) != 1 || old.state.WaitingGeneration != "" {
		t.Fatal("old parent tail published or retained admission after true join")
	}
	if len(observed) != 1 || replacement.state.Phase != "ready" || replacement.state.LastError != "" {
		t.Fatal("old operation notified replacement subscribers with stale outcome")
	}
}
