//go:build js && wasm && !nextgo_module && !nextgo_devrefresh

package client

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"syscall/js"
	"testing"

	"github.com/Suhaibinator/Next.go/internal/navigation"
)

// This deliberately small ownership fixture tests Go transaction ordering and
// collectors. It is not a browser/CSS implementation. The real-browser suite
// separately exercises selectors, native groups, ancestry, focus and layout.
type retainedRouterFixture struct {
	app, old, incoming, fresh, draft, status js.Value
	makeNode                                 js.Value
	scope                                    *Scope
	state                                    *State[int]
	setups, cleanups, clicks                 int
	data                                     envelope
}

func retainedRouterEnvironment(t *testing.T) *retainedRouterFixture {
	t.Helper()
	previousDoc, previousManifest := doc, routeManifest
	previousViews, previousOwners := retainedViews, retainedOwners
	previousCurrent, previousFocus := retainedCurrent, retainedFocus
	previousID, previousEpoch := navigationID, cacheEpoch
	previousFreshID, previousFreshEpoch := retainedFreshID, retainedFreshEpoch
	previousMounted, previousRegistry := mounted, registry
	previousURL, previousState := currentURL, currentState
	previousHistory, previousOffline := ownedHistory, documentOffline
	previousAdmission := retainedTaskAdmission
	previousSelections := selections
	previousStreaming, previousMetadata, previousMetadataGeneration := streamingActive, initialMetadata, initialMetadataGeneration
	retainedViews, retainedOwners = map[string]*retainedView{}, retainedIndex{}
	retainedCurrent, retainedFocus = nil, nil
	mounted, registry = nil, map[string]Mount{}
	ownedHistory, documentOffline = historyRegistry{}, nil
	routeManifest = navigation.Manifest{RetainRoutes: true}
	navigationID, cacheEpoch = 9, 4
	retainedFreshID, retainedFreshEpoch = navigationID, cacheEpoch
	currentURL, currentState = "https://retained.test/a", js.Null()
	fixture := js.Global().Get("Function").New(`
return (() => {
 function node(tag) {
  const attrs = new Map(), callbacks = new Set(), styles = new Map();
  const n = {nodeType:1,tagName:tag.toUpperCase(),parentNode:null,childNodes:[],shadowRoot:null,
   hidden:false,scrollLeft:0,scrollTop:0,value:'',isConnected:true, adds:0,removes:0,
   style:{getPropertyValue(k){return styles.get(k)?.[0]||'';},getPropertyPriority(k){return styles.get(k)?.[1]||'';},setProperty(k,v,p=''){styles.set(k,[v,p]);},removeProperty(k){styles.delete(k);}},
   hasAttribute(k){return attrs.has(k);},getAttribute(k){return attrs.has(k)?attrs.get(k):null;},
   setAttribute(k,v){attrs.set(k,String(v));if(k==='hidden')this.hidden=true;},removeAttribute(k){attrs.delete(k);if(k==='hidden')this.hidden=false;},
   contains(other){return other===this||this.childNodes.some(child=>child.contains?.(other));},
   matches(selector){return selector.split(',').some(part=>{part=part.trim();if(part==='*')return true;if(part==='[data-nextgo-view][hidden]')return attrs.has('data-nextgo-view')&&attrs.has('hidden');const attr=/^([a-z][a-z0-9-]*)?\[([^=\]]+)(?:="([^"]*)")?\]$/i.exec(part);if(attr)return (!attr[1]||attr[1].toLowerCase()===tag.toLowerCase())&&attrs.has(attr[2])&&(attr[3]===undefined||attrs.get(attr[2])===attr[3]);return part.toLowerCase()===tag.toLowerCase();});},
   closest(selector){for(let candidate=this;candidate;candidate=candidate.parentNode)if(candidate.matches(selector))return candidate;return null;},
   querySelectorAll(selector){const result=[];for(const child of this.childNodes){if(child.nodeType===1){if(child.matches(selector))result.push(child);result.push(...child.querySelectorAll(selector));}}return result;},
   querySelector(selector){return this.querySelectorAll(selector)[0]||null;},
   appendChild(child){if(child.parentNode)child.remove();this.childNodes.push(child);child.parentNode=this;child.isConnected=this.isConnected;return child;},
   remove(){if(this.parentNode){this.parentNode.childNodes=this.parentNode.childNodes.filter(child=>child!==this);this.parentNode=null;}this.isConnected=false;},
   replaceChildren(){for(const child of [...this.childNodes])child.remove();},
   addEventListener(type,callback){this.adds++;callbacks.add(callback);},removeEventListener(type,callback){this.removes++;callbacks.delete(callback);},
   fire(){for(const callback of [...callbacks])callback({target:this});},focus(){document.activeElement=this;}
  };
  Object.defineProperty(n,'hidden',{get(){return attrs.has('hidden');},set(v){if(v)attrs.set('hidden','');else attrs.delete('hidden');}});
  return n;
 }
 const app=node('div'),old=node('main'),incoming=node('body'),fresh=node('main'),draft=node('input'),body=node('body'),status=node('div');
 draft.value='retained draft';old.appendChild(draft);app.appendChild(old);incoming.appendChild(fresh);
 incoming.body=incoming;
 const document={body,title:'Before',head:node('head'),documentElement:node('html'),activeElement:draft,getElementById(id){return id==='nextgo-app'?app:id==='nextgo-status'?status:null;},querySelector(selector){return app.querySelector(selector);},querySelectorAll(selector){return app.querySelectorAll(selector);},createDocumentFragment(){return node('fragment');}};
 return {app,old,incoming,fresh,draft,status,document,makeNode:node};
})();`).Invoke()
	doc = fixture.Get("document")
	f := &retainedRouterFixture{app: fixture.Get("app"), old: fixture.Get("old"), incoming: fixture.Get("incoming"), fresh: fixture.Get("fresh"), draft: fixture.Get("draft"), status: fixture.Get("status"), makeNode: fixture.Get("makeNode"), state: NewState(7)}
	descriptor, err := navigation.NewRetention("public-demo", "<main>fresh</main>")
	if err != nil {
		t.Fatal(err)
	}
	f.data = envelope{Status: 200, HTML: "<main>fresh</main>", Retention: descriptor}
	view := prepareRetainedView(f.old, descriptor, currentURL, 512, strings.Repeat("a", 32))
	registerRetained(view)
	retainedCurrent = view
	showRetained(view)
	f.scope = &Scope{Root: Element{f.old}}
	mounted = []*Scope{f.scope}
	life, err := f.scope.Lifetime()
	if err != nil {
		t.Fatal(err)
	}
	life.Effect(func() (func(), error) { f.setups++; return func() { f.cleanups++ }, nil })
	f.scope.On(Element{f.old}, "click", func(Event) { f.clicks++; f.state.Set(f.state.Get() + 1) })
	t.Cleanup(func() {
		f.scope.dispose()
		doc, routeManifest = previousDoc, previousManifest
		retainedViews, retainedOwners = previousViews, previousOwners
		retainedCurrent, retainedFocus = previousCurrent, previousFocus
		navigationID, cacheEpoch = previousID, previousEpoch
		retainedFreshID, retainedFreshEpoch = previousFreshID, previousFreshEpoch
		mounted, registry = previousMounted, previousRegistry
		currentURL, currentState = previousURL, previousState
		ownedHistory, documentOffline = previousHistory, previousOffline
		retainedTaskAdmission = previousAdmission
		selections = previousSelections
		streamingActive, initialMetadata, initialMetadataGeneration = previousStreaming, previousMetadata, previousMetadataGeneration
	})
	return f
}

func (f *retainedRouterFixture) unchanged(t *testing.T) {
	t.Helper()
	if !f.old.Get("parentNode").Equal(f.app) || f.old.Get("hidden").Bool() || f.old.Call("hasAttribute", "inert").Bool() || f.old.Get("style").Call("getPropertyValue", "display").String() != "" || f.draft.Get("value").String() != "retained draft" || f.state.Get() != 7 || f.setups != 1 || f.cleanups != 0 || f.old.Get("adds").Int() != 1 || f.old.Get("removes").Int() != 0 || !f.scope.active() || len(retainedViews) != 1 || retainedOwners.active != strings.Repeat("a", 32) || !f.fresh.Get("parentNode").Equal(f.incoming) || len(ownedHistory.records) != 0 {
		t.Fatal("precommit failure changed visible ownership, DOM, state, activity or history")
	}
}

func TestRetainedPreparationAllocationAndAdmissionFailBeforeVisibleMutation(t *testing.T) {
	for _, kind := range []string{"identity", "admission", "collision"} {
		t.Run(kind, func(t *testing.T) {
			f := retainedRouterEnvironment(t)
			injected := errors.New("injected preparation failure")
			newID := func() (string, error) { return strings.Repeat("b", 32), nil }
			admit := retainedAdmit
			switch kind {
			case "identity":
				newID = func() (string, error) { return "", injected }
			case "admission":
				admit = func(*retainedIndex, retainedInfo) ([]string, error) { return nil, injected }
			case "collision":
				newID = func() (string, error) { return strings.Repeat("a", 32), nil }
			}
			plan, err := prepareRetainedDOM(f.app, f.incoming, f.data, "https://retained.test/b", false, js.Null(), newID, admit)
			if err == nil || plan != nil {
				t.Fatal("injected fallible admission unexpectedly committed", err)
			}
			f.unchanged(t)
		})
	}
}

func TestRetainedStaleGenerationFailsBeforeSuspension(t *testing.T) {
	for _, kind := range []string{"navigation", "cache"} {
		t.Run(kind, func(t *testing.T) {
			f := retainedRouterEnvironment(t)
			if kind == "navigation" {
				retainedFreshID--
			} else {
				retainedFreshEpoch--
			}
			handled, activate, err := commitRetainedDOM(f.app, f.incoming, f.data, "https://retained.test/b", false, js.Null())
			if handled || activate != nil || err == nil {
				t.Fatal("stale validation admitted", err)
			}
			f.unchanged(t)
		})
	}
}

func TestRetainedCleanupNavigationRestoresOutgoingVisibilityAndActivity(t *testing.T) {
	f := retainedRouterEnvironment(t)
	life, err := f.scope.Lifetime()
	if err != nil {
		t.Fatal(err)
	}
	_, err = life.Effect(func() (func(), error) { return func() { navigationID++ }, nil })
	if err != nil {
		t.Fatal(err)
	}
	handled, activate, err := commitRetainedDOM(f.app, f.incoming, f.data, "https://retained.test/b", false, js.Null())
	if handled || activate != nil || err == nil {
		t.Fatal("cleanup-superseded navigation committed", err)
	}
	if f.old.Get("hidden").Bool() || f.old.Call("hasAttribute", "inert").Bool() || !f.scope.active() || !f.old.Get("parentNode").Equal(f.app) || !f.fresh.Get("parentNode").Equal(f.incoming) || f.state.Get() != 7 || f.draft.Get("value").String() != "retained draft" || len(retainedViews) != 1 || len(ownedHistory.records) != 0 || f.setups != 2 || f.cleanups != 1 || f.old.Get("adds").Int() != 2 || f.old.Get("removes").Int() != 1 {
		t.Fatal("reentrant cleanup left outgoing view hidden or lost state/activity")
	}
}

func TestRetainedDescriptorFreeSuccessEvictsPrivateOwnersAndAliases(t *testing.T) {
	f := retainedRouterEnvironment(t)
	retainedOwners.bind(strings.Repeat("c", 32), retainedCurrent.info.ID)
	other := prepareRetainedView(f.fresh, f.data.Retention, "https://retained.test/hidden", 512, strings.Repeat("d", 32))
	f.app.Call("appendChild", f.fresh)
	registerRetained(other)
	hideRetained(other)
	retainedOwners.bind(strings.Repeat("e", 32), other.info.ID)
	f.data.Retention = nil
	handled, activate, err := commitRetainedDOM(f.app, f.incoming, f.data, "https://retained.test/b", false, js.Null())
	if handled || activate != nil || err != nil || len(retainedViews) != 0 || len(retainedOwners.entries) != 0 || len(retainedOwners.aliases) != 0 || retainedCurrent != nil || f.old.Get("isConnected").Bool() || f.fresh.Get("isConnected").Bool() || !f.scope.closed || f.cleanups != 1 {
		t.Fatal("descriptor-free response retained reachable private state", err)
	}
}

func TestRetainedSoleOwnerCollectorsIncludeIslandErrorAndScroll(t *testing.T) {
	f := retainedRouterEnvironment(t)
	view := retainedCurrent
	unowned := f.makeNode.Invoke("a")
	unowned.Call("setAttribute", "data-nextgo-view", view.info.ID)
	unowned.Call("setAttribute", "hidden", "")
	// Legacy document fixtures may return their anchor for any closest selector.
	// No registered owner must leave that ordinary/default behavior untouched.
	unowned.Set("closest", js.Global().Get("Function").New("return this;"))
	retainedViews = map[string]*retainedView{}
	routeManifest.RetainRoutes = false
	if hiddenRetainedNode(unowned) {
		t.Fatal("default-off document without retained owners suppressed an ordinary node")
	}
	routeManifest.RetainRoutes = true
	if hiddenRetainedNode(unowned) {
		t.Fatal("opt-in without registered owners suppressed an ordinary node")
	}
	retainedViews[view.info.ID] = view
	if hiddenRetainedNode(unowned) || hiddenRetainedNode(f.draft) || hiddenRetainedNode(f.old) {
		t.Fatal("unowned marker or visible registered owner was treated as hidden")
	}
	f.old.Call("setAttribute", "data-nextgo-island", "root")
	f.old.Call("setAttribute", "data-nextgo-hydrated", "error")
	registry["root"] = func(*Scope, json.RawMessage) error { return nil }
	nodes := applicationIslandNodes()
	if nodes.Length() != 1 || !nodes.Index(0).Equal(f.old) || !applicationHydrationError() {
		t.Fatal("root island/error disappeared from active collectors")
	}
	if _, _, ok := retainedDOMInfo(f.old, true); ok {
		t.Fatal("failed owner became eligible for retention")
	}
	f.old.Call("setAttribute", "data-nextgo-hydrated", "true")
	if _, islands, ok := retainedDOMInfo(f.old, true); !ok || islands != 1 {
		t.Fatal("eager owner island failed eligibility")
	}
	f.old.Set("scrollLeft", 19)
	f.old.Set("scrollTop", 73)
	captureRetained(retainedCurrent)
	if len(retainedCurrent.scroll) != 1 || !retainedCurrent.scroll[0].node.Equal(f.old) || retainedCurrent.scroll[0].x != 19 || retainedCurrent.scroll[0].y != 73 {
		t.Fatal("owner scroll container was omitted")
	}
	hideRetained(retainedCurrent)
	if !f.draft.Get("isConnected").Bool() || !hiddenRetainedNode(f.draft) || !hiddenRetainedNode(f.old) {
		t.Fatal("connected hidden focus/event target was treated as visible")
	}
	if hiddenRetainedNode(unowned) {
		t.Fatal("hidden registered owner suppressed an unrelated node with a copied marker")
	}
	delete(retainedViews, view.info.ID)
	if hiddenRetainedNode(f.draft) || hiddenRetainedNode(f.old) {
		t.Fatal("unregistered hidden owner still suppressed document behavior")
	}
	retainedViews[view.info.ID] = view
	showRetained(view)
	if hiddenRetainedNode(f.draft) || hiddenRetainedNode(f.old) {
		t.Fatal("shown owner remained classified as hidden")
	}
}

// The fixture verifies setter admission and synchronous restoration, not the
// browser's native caret scrolling. The real-browser owner/history assertions
// independently require the retained container/window positions to stay exact.
func TestRetainedFocusPreservesUnchangedNativeSelection(t *testing.T) {
	for _, changed := range []string{"unchanged", "start", "end", "direction"} {
		t.Run(changed, func(t *testing.T) {
			f := retainedRouterEnvironment(t)
			view := retainedCurrent
			js.Global().Get("Function").New("node", `
node.selectionStart=2;node.selectionEnd=7;node.selectionDirection='backward';
node.selectionCalls=0;node.focusCalls=0;node.preventedScroll=false;
node.focus=function(options){this.focusCalls++;this.preventedScroll=options?.preventScroll===true;};
node.setSelectionRange=function(start,end,direction){this.selectionCalls++;this.selectionStart=start;this.selectionEnd=end;this.selectionDirection=direction;};
`).Invoke(f.draft)
			f.old.Set("scrollTop", 73)
			captureRetained(view)
			switch changed {
			case "start":
				f.draft.Set("selectionStart", 3)
			case "end":
				f.draft.Set("selectionEnd", 8)
			case "direction":
				f.draft.Set("selectionDirection", "forward")
			}
			f.old.Set("scrollTop", 0)
			retainedFocus = view
			restoreRetainedFocus(true)
			wantCalls := 1
			if changed == "unchanged" {
				wantCalls = 0
			}
			if got := f.draft.Get("selectionCalls").Int(); got != wantCalls {
				t.Fatalf("selection setters = %d, want %d", got, wantCalls)
			}
			if f.draft.Get("selectionStart").Int() != 2 || f.draft.Get("selectionEnd").Int() != 7 || f.draft.Get("selectionDirection").String() != "backward" {
				t.Fatal("saved differing selection was not restored")
			}
			if f.draft.Get("focusCalls").Int() != 1 || !f.draft.Get("preventedScroll").Bool() || f.old.Get("scrollTop").Int() != 73 || retainedFocus != nil {
				t.Fatal("focus, owner scroll, or one-use focus ownership was not restored")
			}
			restoreRetainedFocus(true)
			if f.draft.Get("selectionCalls").Int() != wantCalls || f.draft.Get("focusCalls").Int() != 1 {
				t.Fatal("consumed retained focus was replayed")
			}
		})
	}
}

func TestRetainedChangedOutputPlansColdOwnerAndEvictsOldHistorySnapshot(t *testing.T) {
	f := retainedRouterEnvironment(t)
	id, key := strings.Repeat("d", 32), strings.Repeat("e", 32)
	hidden := prepareRetainedView(f.draft, f.data.Retention, "https://retained.test/a", 512, id)
	f.app.Call("appendChild", f.draft)
	registerRetained(hidden)
	hideRetained(hidden)
	retainedOwners.active = retainedCurrent.info.ID
	retainedOwners.bind(key, id)
	ownedHistory.put(key, historyRecord{URL: "https://retained.test/a"})
	descriptor, err := navigation.NewRetention("public-demo", "<main>new output</main>")
	if err != nil {
		t.Fatal(err)
	}
	f.data.HTML, f.data.Retention = "<main>new output</main>", descriptor
	state := js.ValueOf(map[string]any{"nextgoEntry": key})
	plan, err := prepareRetainedDOM(f.app, f.incoming, f.data, "https://retained.test/a", true, state, func() (string, error) { return strings.Repeat("b", 32), nil }, retainedAdmit)
	if err != nil || plan == nil || plan.restored || !plan.selected.root.Equal(f.fresh) || len(plan.evictions) != 1 || plan.evictions[0] != id {
		t.Fatal("changed fresh output kept stale history ownership", err)
	}
	if !f.fresh.Get("parentNode").Equal(f.incoming) || !f.old.Get("parentNode").Equal(f.app) || f.old.Get("hidden").Bool() {
		t.Fatal("planning changed SSR ancestry or visibility")
	}
}

func TestRetainedDocumentContextElementsBypassAtOwnerAndDescendant(t *testing.T) {
	for _, element := range []struct{ tag, attr, value string }{
		{"base", "", ""}, {"meta", "", ""}, {"map", "", ""}, {"area", "", ""},
		{"input", "type", "radio"}, {"details", "name", "group"}, {"a", "name", "target"},
	} {
		t.Run(element.tag+"/"+element.attr, func(t *testing.T) {
			f := retainedRouterEnvironment(t)
			unsafe := f.makeNode.Invoke(element.tag)
			if element.attr != "" {
				unsafe.Call("setAttribute", element.attr, element.value)
			}
			if _, _, ok := retainedDOMInfo(unsafe, false); ok {
				t.Fatal("document-context owner admitted", element)
			}
			f.fresh.Call("appendChild", unsafe)
			if _, _, ok := retainedDOMInfo(f.fresh, false); ok {
				t.Fatal("document-context descendant admitted", element)
			}
		})
	}
	// Compound matching must retain its tag/value constraints rather than
	// rejecting every input or every element with a name/type attribute.
	for _, element := range []struct{ tag, attr, value string }{
		{"input", "type", "text"}, {"details", "", ""}, {"a", "href", "/ordinary"},
		{"span", "name", "target"}, {"div", "type", "radio"},
	} {
		t.Run("ordinary/"+element.tag, func(t *testing.T) {
			f := retainedRouterEnvironment(t)
			ordinary := f.makeNode.Invoke(element.tag)
			if element.attr != "" {
				ordinary.Call("setAttribute", element.attr, element.value)
			}
			if _, _, ok := retainedDOMInfo(ordinary, false); !ok {
				t.Fatal("ordinary owner incorrectly refused", element)
			}
			f.fresh.Call("appendChild", ordinary)
			if _, _, ok := retainedDOMInfo(f.fresh, false); !ok {
				t.Fatal("ordinary descendant incorrectly refused", element)
			}
		})
	}
}

func TestRetainedSameURLPopShortcutRequiresExactCurrentVisitAlias(t *testing.T) {
	retainedRouterEnvironment(t)
	key := strings.Repeat("e", 32)
	state := js.ValueOf(map[string]any{"nextgoEntry": key})
	ownedHistory.put(key, historyRecord{URL: currentURL})
	if retainedPopSameView(state, currentURL) {
		t.Fatal("unknown same-URL visit bypassed server validation")
	}
	retainedOwners.bind(key, retainedCurrent.info.ID)
	if !retainedPopSameView(state, currentURL) {
		t.Fatal("tracked same-owner hash traversal lost its shortcut")
	}
	other := retainedFixtureInfo(2)
	other.URL = currentURL
	retainedOwners.entries[other.ID] = other
	retainedOwners.bind(key, other.ID)
	if retainedPopSameView(state, currentURL) {
		t.Fatal("distinct same-URL retained visit reused active UI")
	}
	retainedOwners.bind(key, retainedCurrent.info.ID)
	retainedCurrent.valid = false
	if retainedPopSameView(state, currentURL) {
		t.Fatal("invalidated snapshot bypassed fresh validation")
	}
	routeManifest.RetainRoutes = false
	if !retainedPopSameView(js.Null(), currentURL) {
		t.Fatal("default router semantics changed")
	}
}

func TestRetainedRestoreCommitsRevalidatedNodeAndEnvelopeCharges(t *testing.T) {
	f := retainedRouterEnvironment(t)
	owner := f.makeNode.Invoke("section")
	hidden := prepareRetainedView(owner, f.data.Retention, "https://retained.test/a", 1, strings.Repeat("d", 32))
	f.app.Call("appendChild", owner)
	registerRetained(hidden)
	hideRetained(hidden)
	retainedOwners.active = retainedCurrent.info.ID
	key := strings.Repeat("e", 32)
	retainedOwners.bind(key, hidden.info.ID)
	ownedHistory.put(key, historyRecord{URL: "https://retained.test/a"})
	owner.Call("appendChild", f.makeNode.Invoke("span"))
	state := js.ValueOf(map[string]any{"nextgoEntry": key})
	plan, err := prepareRetainedDOM(f.app, f.incoming, f.data, "https://retained.test/a", true, state, retainedViewID, retainedAdmit)
	if err != nil || plan == nil || !plan.restored || plan.selectedInfo.Nodes != 2 || plan.selectedInfo.Bytes != int(envelopeBytes(f.data)) || hidden.info.Nodes != 1 || hidden.info.Bytes != 1 {
		t.Fatal("restore planning ignored fresh charges or mutated record early", err)
	}
	handled, activate, err := commitRetainedDOM(f.app, f.incoming, f.data, "https://retained.test/a", true, state)
	if err != nil || !handled || activate == nil {
		t.Fatal("valid restore not committed", err)
	}
	activate.Activate()
	activate.Retire()
	entry := retainedOwners.entries[hidden.info.ID]
	if retainedCurrent != hidden || entry.Nodes != 2 || entry.Bytes != int(envelopeBytes(f.data)) || hidden.info.Nodes != entry.Nodes || hidden.info.Bytes != entry.Bytes {
		t.Fatal("restored ledger retained stale charges")
	}
}

func TestRetainedColdTeardownCannotCommitAfterCleanupSupersedesOrInvalidates(t *testing.T) {
	for _, branch := range []string{"missing", "denied", "unsupported"} {
		for _, cause := range []string{"navigation", "cache"} {
			t.Run(branch+"/"+cause, func(t *testing.T) {
				f := retainedRouterEnvironment(t)
				f.scope.Cleanup(func() {
					if cause == "navigation" {
						navigationID++
					} else {
						cacheEpoch++
					}
				})
				switch branch {
				case "missing":
					f.data.Retention = nil
				case "denied":
					f.data.Status = 403
					f.data.Retention = nil
				case "unsupported":
					f.fresh.Call("setAttribute", "id", "author-id")
				}
				err := commit(f.incoming, f.app, f.data, "https://retained.test/b", false, js.Null(), false)
				if err == nil || !f.scope.closed || len(retainedViews) != 0 || len(retainedOwners.aliases) != 0 || retainedCurrent != nil || f.old.Get("isConnected").Bool() {
					t.Fatal("cold privacy teardown was skipped or stale response committed", err)
				}
				if currentURL != "https://retained.test/a" || doc.Get("title").String() != "Before" || !f.fresh.Get("parentNode").Equal(f.incoming) || f.app.Get("childNodes").Length() != 0 || len(ownedHistory.records) != 0 {
					t.Fatal("superseded cold response overwrote DOM/head/history/location")
				}
			})
		}
	}
}

func TestOrdinaryUnmountCleanupIsFencedBeforeDOMHeadAndHistory(t *testing.T) {
	for _, cause := range []string{"navigation", "cache"} {
		t.Run(cause, func(t *testing.T) {
			f := retainedRouterEnvironment(t)
			retainedViews, retainedOwners, retainedCurrent = map[string]*retainedView{}, retainedIndex{}, nil
			f.old.Call("removeAttribute", "data-nextgo-view")
			f.old.Call("removeAttribute", "data-nextgo-view-active")
			routeManifest.RetainRoutes = false
			f.data.Retention = nil
			f.scope.Cleanup(func() {
				if cause == "navigation" {
					navigationID++
				} else {
					cacheEpoch++
				}
			})
			err := commit(f.incoming, f.app, f.data, "https://retained.test/b", false, js.Null(), false)
			if !errors.Is(err, context.Canceled) || !f.scope.closed || !f.old.Get("parentNode").Equal(f.app) || !f.fresh.Get("parentNode").Equal(f.incoming) || currentURL != "https://retained.test/a" || doc.Get("title").String() != "Before" || len(ownedHistory.records) != 0 {
				t.Fatal("ordinary cleanup committed stale DOM/head/history/location", err)
			}
		})
	}
}

func TestRetainedRetirementPublishesVisibleReadyUIWithinRealLedgersBeforeCleanup(t *testing.T) {
	for _, cause := range []string{"partition", "capacity"} {
		t.Run(cause, func(t *testing.T) {
			f := retainedRouterEnvironment(t)
			if cause == "partition" {
				descriptor, err := navigation.NewRetention("new-public-partition", f.data.HTML)
				if err != nil {
					t.Fatal(err)
				}
				f.data.Retention = descriptor
			} else {
				retainedCurrent.info.Bytes = 3 << 20
				entry := retainedOwners.entries[retainedCurrent.info.ID]
				entry.Bytes = 3 << 20
				retainedOwners.entries[entry.ID] = entry
				for _, id := range []string{strings.Repeat("c", 32), strings.Repeat("d", 32)} {
					owner := f.makeNode.Invoke("section")
					view := prepareRetainedView(owner, f.data.Retention, "https://retained.test/hidden/"+id, 512<<10, id)
					f.app.Call("appendChild", owner)
					registerRetained(view)
					hideRetained(view)
				}
				retainedOwners.active = retainedCurrent.info.ID
				f.data.Description = strings.Repeat("m", 2<<20)
			}
			withinLedger := func() bool {
				nodes, bytes := 0, 0
				for _, entry := range retainedOwners.entries {
					nodes += entry.Nodes
					bytes += entry.Bytes
				}
				return len(retainedOwners.entries) <= retainedViewLimit && nodes <= retainedTotalNodes && bytes <= retainedMetadataBytes
			}
			calls := 0
			observed := false
			f.scope.Cleanup(func() {
				calls++
				observed = retainedCurrent != nil && retainedCurrent.root.Equal(f.fresh) && !f.fresh.Get("hidden").Bool() && f.fresh.Get("parentNode").Equal(f.app) && doc.Get("body").Call("getAttribute", "data-nextgo-hydration").String() == "ready" && withinLedger() && currentURL == "https://retained.test/b"
				navigationID++
				f.status.Set("textContent", "Newer navigation loading")
				f.status.Set("hidden", false)
			})
			handled, activity, err := commitRetainedDOM(f.app, f.incoming, f.data, "https://retained.test/b", false, js.Null())
			if err != nil || !handled || activity == nil {
				t.Fatal("valid retirement not admitted", err)
			}
			if f.scope.closed || f.old.Get("isConnected").Bool() || f.fresh.Get("hidden").Bool() || !withinLedger() || retainedViews[strings.Repeat("a", 32)] != nil {
				t.Fatal("ownership commit exceeded peak ledgers or ran terminal callbacks early")
			}
			// Model the common metadata/history commit, which precedes Activate and
			// final Retire in runtime.commit. Real navigation is tested in Playwright.
			currentURL = "https://retained.test/b"
			doc.Set("title", "B")
			activity.Activate()
			activity.Retire()
			activity.Retire()
			if !observed || calls != 1 || !f.scope.closed || f.status.Get("textContent").String() != "Newer navigation loading" || !withinLedger() {
				t.Fatal("terminal cleanup started from hidden/unhydrated/over-budget UI")
			}
			// A failed newer request must preserve the already committed visible UI.
			report("Navigation failed. Try again or reload.")
			if retainedCurrent == nil || !retainedCurrent.root.Equal(f.fresh) || f.fresh.Get("hidden").Bool() || currentURL != "https://retained.test/b" || doc.Get("title").String() != "B" || f.status.Get("hidden").Bool() {
				t.Fatal("failed superseding request stranded the active owner")
			}
		})
	}
}

func TestRetainedResumeFailureStaysGenericErroneousAndRecoversCold(t *testing.T) {
	f := retainedRouterEnvironment(t)
	owner := f.makeNode.Invoke("section")
	owner.Call("setAttribute", "data-nextgo-island", "recover")
	owner.Call("setAttribute", "data-nextgo-hydrated", "true")
	mounts := 0
	registry["recover"] = func(*Scope, json.RawMessage) error { mounts++; return nil }
	view := prepareRetainedView(owner, f.data.Retention, "https://retained.test/recover", 512, strings.Repeat("d", 32))
	f.app.Call("appendChild", owner)
	registerRetained(view)
	hideRetained(view)
	retainedOwners.active = retainedCurrent.info.ID
	scope := &Scope{Root: Element{owner}}
	mounted = append(mounted, scope)
	t.Cleanup(scope.dispose)
	life, err := scope.Lifetime()
	if err != nil {
		t.Fatal(err)
	}
	setups := 0
	_, err = life.Effect(func() (func(), error) {
		setups++
		if setups == 2 {
			return nil, errors.New("private diagnostic: resume failed")
		}
		return func() {}, nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if err = scope.suspendForRetention(); err != nil {
		t.Fatal(err)
	}
	key := strings.Repeat("e", 32)
	retainedOwners.bind(key, view.info.ID)
	ownedHistory.put(key, historyRecord{URL: view.info.URL})
	state := js.ValueOf(map[string]any{"nextgoEntry": key})
	handled, activity, err := commitRetainedDOM(f.app, f.incoming, f.data, view.info.URL, true, state)
	if err != nil || !handled || activity == nil {
		t.Fatal("valid restore not prepared", err)
	}
	activity.Activate()
	activity.Retire()
	hydrate()
	if !scope.closed || view.valid || view.activityErr == nil || setups != 2 || mounts != 0 || len(retainedOwners.aliases) != 0 || owner.Get("hidden").Bool() || owner.Call("getAttribute", "data-nextgo-hydrated").String() != "error" || doc.Get("body").Call("getAttribute", "data-nextgo-hydration").String() != "error" || f.status.Get("hidden").Bool() || f.status.Get("textContent").String() != retainedRecoveryMessage {
		t.Fatal("failed resume was replayed, hidden, leaked diagnostics or claimed ready")
	}
	clearRetainedViews(true)
	retainedFreshID, retainedFreshEpoch = navigationID, cacheEpoch
	handled, activity, err = commitRetainedDOM(f.app, f.incoming, f.data, view.info.URL, false, js.Null())
	if err != nil || handled || activity != nil || owner.Get("isConnected").Bool() {
		t.Fatal("ordinary fresh recovery attempted to reuse the failed scope", err)
	}
}

func TestRetainedRollbackFailureCannotReplaceNewerLoadingStatus(t *testing.T) {
	f := retainedRouterEnvironment(t)
	life, err := f.scope.Lifetime()
	if err != nil {
		t.Fatal(err)
	}
	setups := 0
	_, err = life.Effect(func() (func(), error) {
		setups++
		if setups > 1 {
			return nil, errors.New("old rollback diagnostic")
		}
		return func() { navigationID++; f.status.Set("textContent", "Newer navigation loading") }, nil
	})
	if err != nil {
		t.Fatal(err)
	}
	handled, activity, err := commitRetainedDOM(f.app, f.incoming, f.data, "https://retained.test/b", false, js.Null())
	if err == nil || handled || activity != nil || !f.scope.closed || retainedCurrent == nil || retainedCurrent.valid || retainedCurrent.activityErr == nil || f.old.Get("hidden").Bool() || f.status.Get("textContent").String() != "Newer navigation loading" || f.scope.retainedReport != nil {
		t.Fatal("rollback failure was reusable or clobbered newer loading status", err)
	}
}

func TestRetainedScopedReportTokensRestoreNestedAncestorAndPanicPaths(t *testing.T) {
	f := retainedRouterEnvironment(t)
	f.status.Set("textContent", "Existing status")
	err := withRetainedScopeReport(f.scope, navigationID, func() error {
		outer := f.scope.retainedReport
		child := &Scope{parent: f.scope}
		innerErr := withRetainedScopeReport(child, navigationID+1, func() error { reportScopeError(child, "inner private diagnostic"); return nil })
		if innerErr == nil || f.scope.retainedReport != outer {
			t.Error("nested token did not restore its ancestor lease")
		}
		child.Cleanup(func() { panic("child cleanup diagnostic") })
		child.dispose()
		if len(outer.messages) != 1 || f.status.Get("textContent").String() != "Existing status" {
			t.Error("pre-unlink report owner or generic routing was lost")
		}
		return nil
	})
	if err == nil || f.scope.retainedReport != nil {
		t.Fatal("outer token failed to release", err)
	}
	func() {
		defer func() {
			if recover() == nil {
				t.Error("expected injected panic")
			}
		}()
		_ = withRetainedScopeReport(f.scope, navigationID, func() error { panic("injected report-token exit") })
	}()
	if f.scope.retainedReport != nil {
		t.Fatal("panic retained an activity report lease")
	}
	reportScopeError(f.scope, "ordinary default diagnostic")
	if f.status.Get("textContent").String() != "ordinary default diagnostic" {
		t.Fatal("default reporting behavior changed")
	}
}

func TestRetainedColdCleanupPanicCannotVetoAuthorizedFreshRendering(t *testing.T) {
	for _, later := range []string{"none", "navigation", "cache", "new-error"} {
		t.Run(later, func(t *testing.T) {
			f := retainedRouterEnvironment(t)
			f.scope.Cleanup(func() { panic("private old cleanup diagnostic") })
			f.data.Retention = nil
			handled, completion, err := commitRetainedDOM(f.app, f.incoming, f.data, "https://retained.test/b", false, js.Null())
			if err != nil || handled || completion == nil || !f.scope.closed || f.old.Get("isConnected").Bool() || len(retainedViews) != 0 || f.scope.retainedReport != nil {
				t.Fatal("caught terminal cleanup vetoed the authorized fresh path", err)
			}
			// Model successful ordinary rendering. The real cold-render/metadata/
			// hydration path is covered by the production-server browser scenario.
			f.app.Call("appendChild", f.fresh)
			currentURL = "https://retained.test/b"
			doc.Set("title", "B")
			f.status.Set("hidden", true)
			doc.Get("body").Call("setAttribute", "data-nextgo-hydration", "ready")
			completion.Activate()
			switch later {
			case "navigation":
				navigationID++
				f.status.Set("hidden", false)
				f.status.Set("textContent", "Newer loading")
			case "cache":
				cacheEpoch++
				f.status.Set("hidden", false)
				f.status.Set("textContent", "Newer loading")
			case "new-error":
				f.status.Set("hidden", false)
				f.status.Set("textContent", "Current component error")
			}
			completion.Retire()
			completion.Retire()
			if !f.fresh.Get("parentNode").Equal(f.app) || f.fresh.Get("hidden").Bool() || currentURL != "https://retained.test/b" || doc.Get("title").String() != "B" {
				t.Fatal("old cleanup diagnostic left the fresh committed view blank")
			}
			if later == "none" {
				if f.status.Get("hidden").Bool() || f.status.Get("textContent").String() != retainedCleanupMessage {
					t.Fatal("terminal diagnostic lost generic postcommit warning")
				}
			} else if later == "new-error" {
				if f.status.Get("textContent").String() != "Current component error" {
					t.Fatal("old warning clobbered a current error")
				}
			} else if f.status.Get("textContent").String() != "Newer loading" {
				t.Fatal("old warning clobbered a newer generation")
			}
		})
	}
}

func TestRetainedRawEnvelopeRequiresExactBodyBeforeDOMAccess(t *testing.T) {
	f := retainedRouterEnvironment(t)
	rawDescriptor, err := json.Marshal(f.data.Retention)
	if err != nil {
		t.Fatal(err)
	}
	valid := `{"status":200,"html":"<main>fresh</main>","retention":` + string(rawDescriptor) + `}`
	for _, body := range []string{valid,
		strings.Replace(valid, "<main>fresh</main>", "<main>changed</main>", 1),
		strings.Replace(valid, `"retention":`, `"Retention":`, 1),
		strings.Replace(valid, string(rawDescriptor), `null`, 1),
		strings.Replace(valid, `"version":1`, `"version":1,"version":1`, 1),
		strings.Replace(valid, `"version":1`, `"version":1,"unknown":true`, 1),
	} {
		var data envelope
		if err := json.Unmarshal([]byte(body), &data); err != nil {
			t.Fatal(err)
		}
		prior := doc
		doc = js.Undefined()
		err := validateRetainedEnvelope([]byte(body), &data)
		doc = prior
		if body == valid {
			if err != nil || data.Retention == nil {
				t.Fatal("valid exact envelope rejected", err)
			}
		} else if err == nil {
			t.Fatal("invalid raw envelope accepted", body)
		}
	}
}
