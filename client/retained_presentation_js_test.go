//go:build js && wasm && !nextgo_module && !nextgo_devrefresh

package client

import (
	"strings"
	"syscall/js"
	"testing"
)

type retainedSettlementFixture struct {
	router   *retainedRouterFixture
	browser  js.Value
	snapshot *retainedSettlementSnapshot
}

// The fake owns finite callback maps; it has no real RAF/timer, browser renderer
// or native-caret model. Its explicit destroy is the test-only physical detach
// witness for uncertain native cancellation, never a production recovery path.
func retainedSettlementEnvironment(t *testing.T) *retainedSettlementFixture {
	t.Helper()
	if retainedSettlement != nil || retainedSettlementCallbacks != 0 {
		t.Fatal("another physical settlement owner exists")
	}
	oldWin, oldPrivacy := win, mergePrivacy
	oldDraft, oldAction, oldModule := draftFrozen, actionViewFrozen, moduleMode
	oldMerge, oldApplication := mergeCurrent, mergeApplication
	// Restore global authority only after the fixture has disposed its own scope.
	t.Cleanup(func() {
		win, mergePrivacy = oldWin, oldPrivacy
		draftFrozen, actionViewFrozen, moduleMode = oldDraft, oldAction, oldModule
		mergeCurrent, mergeApplication = oldMerge, oldApplication
	})
	draftFrozen, actionViewFrozen, moduleMode = false, false, false
	mergeCurrent, mergeApplication = nil, nil
	router := retainedRouterEnvironment(t)
	browser := js.Global().Get("Function").New("document", `
return (()=>{
 let next=1;const frames=new Map(),timers=new Map(),listeners=[];
 const mode={};const state={nextgoEntry:'cccccccccccccccccccccccccccccccc',nextgoX:0,nextgoY:500};
 const result={mode,location:{href:'https://retained.test/a'},history:{state,replaceState(){result.historyWrites++;}},
  focusCalls:0,windowWrites:0,historyWrites:0,frameRequests:0,timerRequests:0,cancels:0,clears:0,removes:0,probe:null,
  scrollTo(){result.windowWrites++;},
  requestAnimationFrame(callback){const id=next++;result.frameRequests++;if(mode.syncRAF)callback(0);else frames.set(id,callback);if(result.probe)result.probe();if(mode.throwRAF)throw Error('registered RAF then threw');return id;},
  cancelAnimationFrame(id){result.cancels++;if(mode.fireDuringCancel){const callback=frames.get(id);frames.delete(id);callback?.(0);}if(mode.throwCancel)throw Error('cancel uncertain');frames.delete(id);},
  setTimeout(callback,delay){const id=next++;result.timerRequests++;if(delay!==250)throw Error('deadline changed');if(mode.syncTimer)callback();else timers.set(id,callback);if(result.probe)result.probe();if(mode.throwTimer)throw Error('registered timer then threw');return id;},
  clearTimeout(id){result.clears++;if(mode.throwClear)throw Error('clear uncertain');timers.delete(id);},
  frame(){const first=frames.entries().next();if(first.done)return false;const[id,callback]=first.value;frames.delete(id);callback(0);return true;},
  timeout(){const first=timers.entries().next();if(first.done)return false;const[id,callback]=first.value;timers.delete(id);callback();return true;},
  counts(){return {frames:frames.size,timers:timers.size,listeners:listeners.length};},
  emit(type,trusted=true){for(const entry of [...listeners])if(entry.type===type)entry.callback({type,isTrusted:trusted,target:document.activeElement});},
  destroy(){frames.clear();timers.clear();listeners.length=0;}
 };
 function add(type,callback,options){
  if(options?.capture!==true||options?.passive!==true)throw Error('listener options');
  if(mode.intentBeforeAdd)callback({type:'wheel',isTrusted:true});
  listeners.push({target:this,type,callback});
  if(mode.intentAfterAdd)callback({type:'focusin',isTrusted:false});
  if(result.probe)result.probe();
 }
 function remove(type,callback,capture){result.removes++;if(capture!==true)throw Error('capture mismatch');if(mode.throwRemove)throw Error('detach uncertain');for(let i=listeners.length-1;i>=0;i--)if(listeners[i].target===this&&listeners[i].type===type&&listeners[i].callback===callback)listeners.splice(i,1);}
 result.addEventListener=add;result.removeEventListener=remove;document.addEventListener=add;document.removeEventListener=remove;
 document.visibilityState='visible';
 return result;
})();`).Invoke(doc)
	win = browser
	t.Cleanup(func() {
		// Destroying the fake tables proves no mock browser can call these functions
		// again. Only then may this test acknowledge uncertain handles/detaches.
		browser.Call("destroy")
		if job := retainedSettlement; job != nil {
			job.closed = true
			if job.snapshot != nil {
				job.snapshot.clear()
				job.snapshot = nil
			}
			for _, ticket := range []*retainedSettlementCallback{job.frames[0], job.frames[1], job.deadline} {
				if ticket != nil {
					ticket.mayOwn = false
					ticket.registering = false
					ticket.canceling = false
				}
			}
			for i := range job.listeners {
				job.listeners[i].mayOwn = false
				job.listeners[i].registering = false
				job.listeners[i].removing = false
			}
			job.setup = 0
			job.retire()
		}
		if retainedSettlement != nil || retainedSettlementCallbacks != 0 {
			t.Error("physical callback accounting did not retire after mock realm destroy")
		}
		win, mergePrivacy = oldWin, oldPrivacy
		draftFrozen, actionViewFrozen, moduleMode = oldDraft, oldAction, oldModule
	})
	key := strings.Repeat("c", 32)
	ownedHistory.put(key, historyRecord{URL: currentURL})
	retainedOwners.bind(key, retainedCurrent.info.ID)
	currentState = browser.Get("history").Get("state")
	js.Global().Get("Function").New("draft", "document", "browser", `
draft.selectionStart=2;draft.selectionEnd=7;draft.selectionDirection='backward';
draft.focus=function(options){if(options?.preventScroll!==true)throw Error('focus contract');browser.focusCalls++;document.activeElement=this;};
`).Invoke(router.draft, doc, browser)
	router.old.Set("scrollTop", 200)
	captureRetained(retainedCurrent)
	doc.Set("activeElement", router.fresh)
	retainedFocus = retainedCurrent
	snapshot := restoreRetainedFocus(true)
	if snapshot == nil {
		t.Fatal("fake did not admit an owned restored snapshot")
	}
	fixture := &retainedSettlementFixture{router: router, browser: browser, snapshot: snapshot}

	return fixture
}
func (f *retainedSettlementFixture) start(t *testing.T) *retainedSettlementJob {
	t.Helper()
	scheduleRetainedSettlement(f.snapshot)
	if retainedSettlement == nil {
		t.Fatal("expected owned job")
	}
	return retainedSettlement
}
func (f *retainedSettlementFixture) empty(t *testing.T) {
	t.Helper()
	counts := f.browser.Call("counts")
	if retainedSettlement != nil || retainedSettlementCallbacks != 0 || counts.Get("frames").Int() != 0 || counts.Get("timers").Int() != 0 || counts.Get("listeners").Int() != 0 {
		t.Fatal("physical settlement resources remain")
	}
}
func (f *retainedSettlementFixture) drift() { f.router.old.Set("scrollTop", 1283) }

func TestRetainedPresentationTwoFramesFinalNestedOnly(t *testing.T) {
	f := retainedSettlementEnvironment(t)
	// Existing synchronous restoration is unchanged, including one window write.
	if f.router.old.Get("scrollTop").Int() != 200 || f.browser.Get("focusCalls").Int() != 1 || !doc.Get("activeElement").Equal(f.router.draft) {
		t.Fatal("synchronous restore changed")
	}
	restoreScroll(currentURL, true, currentState)
	job := f.start(t)
	f.drift()
	if !f.browser.Call("frame").Bool() || job.completedFrames != 1 || f.router.old.Get("scrollTop").Int() != 1283 {
		t.Fatal("wrote before two frames")
	}
	if !f.browser.Call("frame").Bool() || f.router.old.Get("scrollTop").Int() != 200 {
		t.Fatal("one final reconciliation absent")
	}
	if f.browser.Get("frameRequests").Int() != 2 || f.browser.Get("timerRequests").Int() != 1 || f.browser.Get("windowWrites").Int() != 1 || f.browser.Get("historyWrites").Int() != 0 || f.browser.Get("focusCalls").Int() != 1 {
		t.Fatal("replayed focus/window/history or scheduling")
	}
	if f.router.draft.Get("value").String() != "retained draft" || !f.router.old.Equal(retainedCurrent.root) {
		t.Fatal("changed draft or owner identity")
	}
	f.empty(t)
}
func TestRetainedPresentationEqualPositionsHaveNoSetter(t *testing.T) {
	f := retainedSettlementEnvironment(t)
	js.Global().Get("Function").New("node", `
let value=node.scrollTop;node.sets=0;Object.defineProperty(node,'scrollTop',{configurable:true,get(){return value},set(next){node.sets++;value=next}});
`).Invoke(f.router.old)
	f.start(t)
	f.browser.Call("frame")
	f.browser.Call("frame")
	if f.router.old.Get("sets").Int() != 0 {
		t.Fatal("equal nested position was blindly replayed")
	}
	f.empty(t)
}
func TestRetainedPresentationTrustedInputAndFocusIntentWin(t *testing.T) {
	for _, event := range []string{"pointerdown", "wheel", "touchstart", "keydown", "beforeinput", "focusin"} {
		t.Run(event, func(t *testing.T) {
			f := retainedSettlementEnvironment(t)
			job := f.start(t)
			f.browser.Call("frame")
			f.drift()
			f.browser.Call("emit", event, event != "focusin") // Programmatic focus is also intent.
			if !job.closed || job.snapshot != nil || f.router.old.Get("scrollTop").Int() != 1283 {
				t.Fatal("overrode input/focus or kept private snapshot")
			}
			f.empty(t)
		})
	}
	t.Run("synthetic-wheel-does-not-claim-user-intent", func(t *testing.T) {
		f := retainedSettlementEnvironment(t)
		job := f.start(t)
		f.browser.Call("emit", "wheel", false)
		if job.closed {
			t.Fatal("untrusted wheel treated as user input")
		}
		cancelRetainedSettlement()
		f.empty(t)
	})
}
func TestRetainedPresentationOwnerAndHistoryFences(t *testing.T) {
	for _, kind := range []string{"navigation", "cache", "privacy", "history-entry", "url", "focus", "selection", "owner-invalid", "owner-replaced", "alias-changed", "app-hidden", "node-detached", "hidden", "pagehide"} {
		t.Run(kind, func(t *testing.T) {
			f := retainedSettlementEnvironment(t)
			job := f.start(t)
			f.browser.Call("frame")
			f.drift()
			switch kind {
			case "navigation":
				navigationID++
			case "cache":
				cacheEpoch++
			case "privacy":
				mergePrivacy++
			case "history-entry":
				win.Get("history").Get("state").Set("nextgoEntry", strings.Repeat("d", 32))
			case "url":
				win.Get("location").Set("href", "https://retained.test/other")
			case "focus":
				doc.Set("activeElement", f.router.fresh)
			case "selection":
				f.router.draft.Set("selectionStart", 3)
			case "owner-invalid":
				retainedCurrent.valid = false
			case "owner-replaced":
				retainedCurrent = nil
			case "alias-changed":
				retainedOwners.aliases[f.snapshot.entry] = strings.Repeat("e", 32)
			case "app-hidden":
				f.router.app.Set("hidden", true)
			case "node-detached":
				f.router.old.Set("isConnected", false)
			case "hidden":
				doc.Set("visibilityState", "hidden")
				f.browser.Call("emit", "visibilitychange", false)
			case "pagehide":
				f.browser.Call("emit", "pagehide", true)
			}
			f.browser.Call("frame")
			if !job.closed || job.snapshot != nil || f.router.old.Get("scrollTop").Int() != 1283 {
				t.Fatal("stale/hidden job wrote", kind)
			}
			f.empty(t)
		})
	}
}
func TestRetainedPresentationScopeOverlapAndSeparateOwner(t *testing.T) {
	for _, kind := range []string{"ancestor", "descendant", "separate"} {
		t.Run(kind, func(t *testing.T) {
			f := retainedSettlementEnvironment(t)
			job := f.start(t)
			f.drift()
			region := f.router.fresh
			if kind == "ancestor" {
				region = f.router.app
			}
			if kind == "descendant" {
				region = f.router.draft
			}
			scope := &Scope{Root: Element{region}}
			scope.dispose()
			if kind == "separate" {
				if job.closed {
					t.Fatal("separate retired owner canceled current presentation")
				}
				f.browser.Call("frame")
				f.browser.Call("frame")
				if f.router.old.Get("scrollTop").Int() != 200 {
					t.Fatal("current owner lost settlement")
				}
			} else if !job.closed || job.snapshot != nil {
				t.Fatal("overlapping unmount kept job")
			}
			f.empty(t)
		})
	}
}
func TestRetainedPresentationLifecycleHooksCancelWithoutNavigationContext(t *testing.T) {
	for _, kind := range []string{"cancel", "hide", "retire", "clear", "revoke"} {
		t.Run(kind, func(t *testing.T) {
			f := retainedSettlementEnvironment(t)
			job := f.start(t)
			switch kind {
			case "cancel":
				// Exercise the real early-return hook with no navigation context, while
				// isolating unrelated document workers/observers from this fixture.
				oldCancel, oldAction, oldTransition := cancelNavigation, activeActionView, activeTransition
				oldLazy, oldStream, oldRetry := lazyModules, activeNavigationStream, streamRetry
				oldLinkID, oldLinkNode, oldWaiting, oldObservers := linkNavigationID, linkNavigationNode, linkNavigationWaiting, linkStatusObservers
				defer func() {
					cancelNavigation, activeActionView, activeTransition = oldCancel, oldAction, oldTransition
					lazyModules, activeNavigationStream, streamRetry = oldLazy, oldStream, oldRetry
					linkNavigationID, linkNavigationNode, linkNavigationWaiting, linkStatusObservers = oldLinkID, oldLinkNode, oldWaiting, oldObservers
				}()
				cancelNavigation, activeActionView, activeTransition = nil, nil, nil
				lazyModules, activeNavigationStream, streamRetry = nil, nil, nil
				linkNavigationID, linkNavigationNode, linkNavigationWaiting, linkStatusObservers = 0, js.Undefined(), false, nil
				cancelPendingNavigation()
			case "hide":
				hideRetained(retainedCurrent)
			case "retire":
				retireRetainedOwnership(retainedCurrent)
			case "clear":
				_ = clearRetainedViews(true)
			case "revoke":
				revokeMergeDocument()
			}
			if !job.closed || job.snapshot != nil {
				t.Fatal("logical cancellation missing")
			}
			f.empty(t)
		})
	}
}
func TestRetainedPresentationNoFrameAndHiddenFallback(t *testing.T) {
	for _, kind := range []string{"deadline", "missing-RAF", "missing-cancel", "initial-hidden"} {
		t.Run(kind, func(t *testing.T) {
			f := retainedSettlementEnvironment(t)
			switch kind {
			case "missing-RAF":
				win.Set("requestAnimationFrame", js.Undefined())
			case "missing-cancel":
				win.Set("cancelAnimationFrame", js.Undefined())
			case "initial-hidden":
				doc.Set("visibilityState", "hidden")
			}
			scheduleRetainedSettlement(f.snapshot)
			if kind == "deadline" {
				job := retainedSettlement
				f.drift()
				if !f.browser.Call("timeout").Bool() || !job.closed || job.snapshot != nil || f.router.old.Get("scrollTop").Int() != 1283 {
					t.Fatal("deadline wrote or failed to retire")
				}
			}
			f.empty(t)
		})
	}
}
func TestRetainedPresentationUncertainRegistrationAndPhysicalRetirement(t *testing.T) {
	for _, kind := range []string{"throwRAF", "throwTimer", "throwCancel", "throwClear", "throwRemove"} {
		t.Run(kind, func(t *testing.T) {
			f := retainedSettlementEnvironment(t)
			f.browser.Get("mode").Set(kind, true)
			scheduleRetainedSettlement(f.snapshot)
			job := retainedSettlement
			if kind == "throwCancel" || kind == "throwClear" || kind == "throwRemove" {
				cancelRetainedSettlement()
			}
			if job == nil || !job.closed || job.snapshot != nil || retainedSettlementCallbacks == 0 {
				t.Fatal("uncertain browser ownership released early")
			}
			fresh := captureRetainedSettlement(retainedCurrent)
			if fresh == nil {
				t.Fatal("blocked quota witness needs a fresh live snapshot")
			}
			before := retainedSettlementCallbacks
			scheduleRetainedSettlement(fresh)
			if retainedSettlement != job || retainedSettlementCallbacks != before {
				t.Fatal("physical quarantine accumulated another job")
			}
			f.drift()
			f.browser.Call("frame")
			f.browser.Call("timeout")
			if f.router.old.Get("scrollTop").Int() != 1283 {
				t.Fatal("inert late callback wrote after cancellation")
			}
			if kind != "throwRemove" {
				f.empty(t)
			} else if retainedSettlement != job || retainedSettlementCallbacks != 1 {
				t.Fatal("listener detach uncertainty was forged retired")
			}
		})
	}
}
func TestRetainedPresentationSynchronousDeliveryAndRegistrationCancellation(t *testing.T) {
	for _, kind := range []string{"syncRAF", "syncTimer", "intentBeforeAdd", "intentAfterAdd", "fireDuringCancel"} {
		t.Run(kind, func(t *testing.T) {
			f := retainedSettlementEnvironment(t)
			f.browser.Get("mode").Set(kind, true)
			// Probe is called while addEventListener has not returned: cancellation may
			// not release its shared callback or physical slot while setup is in flight.
			probe := js.FuncOf(func(js.Value, []js.Value) any {
				job := retainedSettlement
				if job == nil || retainedSettlementCallbacks == 0 {
					t.Error("released inside registration")
					return nil
				}
				for _, ticket := range []*retainedSettlementCallback{job.frames[0], job.frames[1], job.deadline, job.guard} {
					if ticket != nil && ticket.released && (ticket.running > 0 || ticket.registering || ticket.canceling || job.setup > 0) {
						t.Error("released ticket inside registration/invocation")
					}
				}
				for _, listener := range job.listeners {
					if listener.registering && job.guard.released {
						t.Error("released guard before add returned")
					}
				}
				return nil
			})
			defer probe.Release()
			f.browser.Set("probe", probe)
			scheduleRetainedSettlement(f.snapshot)
			if kind == "syncRAF" {
				if f.browser.Get("frameRequests").Int() != 2 {
					t.Fatal("synchronous frame callback lost next ordinal")
				}
			}
			if kind == "fireDuringCancel" {
				cancelRetainedSettlement()
			}
			f.empty(t)
		})
	}
}
func TestRetainedPresentationReentrantGetterStopsRemainingWrites(t *testing.T) {
	f := retainedSettlementEnvironment(t)
	f.start(t)
	f.browser.Call("frame")
	callback := js.FuncOf(func(js.Value, []js.Value) any { cancelRetainedSettlement(); return nil })
	defer callback.Release()
	js.Global().Get("Function").New("node", "cancel", `
node.writes=0;Object.defineProperty(node,'scrollTop',{configurable:true,get(){cancel();return 1283},set(){node.writes++}});
`).Invoke(f.router.old, callback)
	f.browser.Call("frame")
	if f.router.old.Get("writes").Int() != 0 {
		t.Fatal("wrote after getter revoked owner")
	}
	f.empty(t)
}
func TestRetainedPresentationProgrammaticScrollAmbiguityIsExplicit(t *testing.T) {
	f := retainedSettlementEnvironment(t)
	f.start(t)
	// There is intentionally no provenance oracle for raw script scrollTop writes
	// without any observable input/focus/navigation intent. Such a mutation can
	// be reconciled as drift; this witness prevents claiming otherwise.
	f.router.old.Set("scrollTop", 777)
	f.browser.Call("frame")
	f.browser.Call("frame")
	if f.router.old.Get("scrollTop").Int() != 200 {
		t.Fatal("documented bounded reconciliation not observed")
	}
	f.empty(t)
}

func TestRetainedPresentationReentrantSetterPreservesNewIntent(t *testing.T) {
	f := retainedSettlementEnvironment(t)
	f.snapshot.positions[0].x = 5
	job := f.start(t)
	f.browser.Call("frame")
	f.drift()
	callback := js.FuncOf(func(js.Value, []js.Value) any {
		cancelRetainedSettlement()
		navigationID++
		f.router.old.Set("scrollTop", 777)
		if !job.closed || job.snapshot != nil || retainedSettlementCallbacks == 0 {
			t.Error("released running callback or kept private snapshot")
		}
		return nil
	})
	defer callback.Release()
	js.Global().Get("Function").New("node", "cancel", `
node.leftWrites=0;Object.defineProperty(node,'scrollLeft',{configurable:true,get(){return 0},set(){node.leftWrites++;cancel();}});
`).Invoke(f.router.old, callback)
	f.browser.Call("frame")
	if f.router.old.Get("leftWrites").Int() != 1 || f.router.old.Get("scrollTop").Int() != 777 {
		t.Fatal("old callback wrote after reentrant new intent")
	}
	f.empty(t)
}

func retainedSettlementDescendant(t *testing.T, f *retainedSettlementFixture) js.Value {
	t.Helper()
	node := f.router.makeNode.Invoke("div")
	f.router.old.Call("appendChild", node)
	node.Set("scrollLeft", 0)
	node.Set("scrollTop", 1283)
	f.snapshot.positions = []retainedScroll{{node: node, x: 5, y: 200}}
	return node
}
func TestRetainedPresentationDescendantAxisOwnershipAndReadOrder(t *testing.T) {
	for _, kind := range []string{"x-getter-detaches", "y-getter-detaches", "x-setter-detaches", "x-getter-moves", "x-getter-cancels", "x-setter-updates-y"} {
		t.Run(kind, func(t *testing.T) {
			f := retainedSettlementEnvironment(t)
			node := retainedSettlementDescendant(t, f)
			cancel := js.FuncOf(func(js.Value, []js.Value) any { cancelRetainedSettlement(); return nil })
			defer cancel.Release()
			js.Global().Get("Function").New("node", "other", "cancel", "kind", `
let x=0,y=1283;node.xSets=0;node.ySets=0;node.yReads=0;
Object.defineProperty(node,'scrollLeft',{configurable:true,get(){if(kind==='x-getter-detaches')node.remove();if(kind==='x-getter-moves')other.appendChild(node);if(kind==='x-getter-cancels')cancel();return x;},set(value){node.xSets++;x=value;if(kind==='x-setter-detaches')node.remove();if(kind==='x-setter-updates-y')y=200;}});
Object.defineProperty(node,'scrollTop',{configurable:true,get(){node.yReads++;if(kind==='y-getter-detaches')node.remove();return y;},set(value){node.ySets++;y=value;}});
`).Invoke(node, f.router.fresh, cancel, kind)
			job := f.start(t)
			f.browser.Call("frame")
			f.browser.Call("frame")
			if !job.closed || job.snapshot != nil {
				t.Fatal("job did not retire")
			}
			switch kind {
			case "x-getter-detaches", "x-getter-moves", "x-getter-cancels":
				if node.Get("xSets").Int() != 0 || node.Get("yReads").Int() != 0 || node.Get("ySets").Int() != 0 {
					t.Fatal("read/wrote target after X getter revoked it")
				}
			case "x-setter-detaches":
				if node.Get("xSets").Int() != 1 || node.Get("yReads").Int() != 0 || node.Get("ySets").Int() != 0 {
					t.Fatal("read/wrote detached target after X setter")
				}
			case "y-getter-detaches":
				if node.Get("yReads").Int() != 1 || node.Get("ySets").Int() != 0 {
					t.Fatal("wrote target detached by Y getter")
				}
			case "x-setter-updates-y":
				if node.Get("xSets").Int() != 1 || node.Get("yReads").Int() != 1 || node.Get("ySets").Int() != 0 {
					t.Fatal("used stale pre-X Y instead of current axis value")
				}
			}
			f.empty(t)
		})
	}
}
func TestRetainedPresentationRevocationClearsHeldSnapshotAliases(t *testing.T) {
	f := retainedSettlementEnvironment(t)
	alias := f.snapshot
	positions := alias.positions
	f.start(t)
	f.browser.Call("frame")
	cancel := js.FuncOf(func(js.Value, []js.Value) any {
		cancelRetainedSettlement()
		if alias.view != nil || alias.positions != nil || !alias.app.IsUndefined() || !alias.focus.IsUndefined() || alias.url != "" || alias.entry != "" || alias.direction != "" || alias.selection || alias.start != 0 || alias.end != 0 || alias.intent != 0 || alias.epoch != 0 || alias.privacy != 0 {
			t.Error("held snapshot alias retained private references")
		}
		for _, position := range positions {
			if !position.node.IsUndefined() || position.x != 0 || position.y != 0 {
				t.Error("held backing slice retained saved nodes")
			}
		}
		return nil
	})
	defer cancel.Release()
	js.Global().Get("Function").New("node", "cancel", `Object.defineProperty(node,'scrollLeft',{configurable:true,get(){cancel();return 0},set(){throw Error('must not write')}});`).Invoke(f.router.old, cancel)
	f.browser.Call("frame")
	f.empty(t)
}

// Native wrappers may synchronously reenter dispose, but logical admission must
// already be closed. The wrapper invokes Go at most once, so the old ordering
// fails assertions without depending on unbounded recursion or stack overflow.
func TestRetainedPresentationScopeDisposalRejectsSynchronousReentry(t *testing.T) {
	for _, boundary := range []string{"contains", "cancel-RAF"} {
		t.Run(boundary, func(t *testing.T) {
			f := retainedSettlementEnvironment(t)
			f.router.old.Call("setAttribute", "data-nextgo-island", "settlement-root")
			scope, stop, err := f.router.scope.Subscope(Element{f.router.draft})
			if err != nil {
				t.Fatal("construct real child scope:", err)
			}
			var job *retainedSettlementJob
			cleanupCalls, reentries := 0, 0
			scope.Cleanup(func() {
				cleanupCalls++
				if !scope.closed || scope.active() {
					t.Error("application cleanup ran with admission open")
				}
				if job == nil || !job.closed || job.snapshot != nil || f.snapshot.view != nil {
					t.Error("application cleanup preceded snapshot revocation")
				}
				if retainedSettlement != nil || retainedSettlementCallbacks != 0 {
					t.Error("application cleanup preceded physical callback retirement")
				}
			})
			reenter := js.FuncOf(func(js.Value, []js.Value) any {
				reentries++
				if !scope.closed || scope.active() {
					t.Error("native wrapper observed open scope admission")
				}
				if _, err := scope.Lifetime(); err != ErrLifetimeClosed {
					t.Error("native wrapper admitted a lifetime after close")
				}
				if _, _, err := scope.Subscope(Element{js.Undefined()}); err != ErrLifetimeClosed {
					t.Error("native wrapper admitted child work before validating close")
				}
				if boundary == "cancel-RAF" && (job == nil || !job.closed || job.snapshot != nil || retainedSettlementCallbacks == 0) {
					t.Error("cancel wrapper did not observe revoked snapshot and owned in-flight callbacks")
				}
				stop()
				return nil
			})
			defer reenter.Release()
			var original js.Value
			if boundary == "contains" {
				// Admit the job before wrapping contains: admission also queries ownership.
				// The wrapper must witness disposal, never the earlier scheduling checks.
				job = f.start(t)
				original = f.router.old.Get("contains")
				f.router.old.Set("contains", js.Global().Get("Function").New("original", "reenter", `
let calls=0;return function(other){calls++;this.settlementContainsCalls=calls;if(calls===1)reenter();return original.call(this,other);};
`).Invoke(original, reenter))
				defer f.router.old.Set("contains", original)
			} else {
				original = win.Get("cancelAnimationFrame")
				win.Set("cancelAnimationFrame", js.Global().Get("Function").New("original", "reenter", `
let calls=0;return function(handle){calls++;this.settlementCancelCalls=calls;if(calls===1)reenter();return original.call(this,handle);};
`).Invoke(original, reenter))
				defer win.Set("cancelAnimationFrame", original)
				job = f.start(t)
			}
			stop()
			if reentries != 1 || cleanupCalls != 1 {
				t.Fatal("bounded wrapper/cleanup counts:", reentries, cleanupCalls)
			}
			if boundary == "contains" && f.router.old.Get("settlementContainsCalls").Int() != 1 {
				t.Fatal("same scope reentered overlap traversal")
			}
			if boundary == "cancel-RAF" && win.Get("settlementCancelCalls").Int() != 1 {
				t.Fatal("same scope repeated native cancellation")
			}
			stop()
			if cleanupCalls != 1 {
				t.Fatal("second disposal repeated application cleanup")
			}
			f.empty(t)
		})
	}
}
