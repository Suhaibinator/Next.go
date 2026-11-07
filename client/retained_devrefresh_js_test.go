//go:build js && wasm && !nextgo_module && nextgo_devrefresh

package client

import (
	"encoding/json"
	"strconv"
	"syscall/js"
	"testing"

	"github.com/Suhaibinator/Next.go/internal/navigation"
)

// This prepared DOM exercises the real commit/hydration/history tail. It does
// not parse HTML or model browser layout; the real browser campaign owns those
// claims. Manager fixtures directly seed retained owners and remain production
// only. No manager helper or lifecycle-test helper is used here.
func retainedDevCommitEnvironment(t *testing.T) js.Value {
	t.Helper()
	previousDoc, previousWin, previousManifest := doc, win, routeManifest
	previousViews, previousOwners := retainedViews, retainedOwners
	previousCurrent, previousFocus := retainedCurrent, retainedFocus
	previousID, previousEpoch := navigationID, cacheEpoch
	previousFreshID, previousFreshEpoch := retainedFreshID, retainedFreshEpoch
	previousMounted, previousRegistry := mounted, registry
	previousURL, previousState, previousHistory := currentURL, currentState, ownedHistory
	previousLocation, previousLocationObservers := committedLocation, locationObservers
	previousSelections, previousSelectionObservers := selections, selectionObservers
	previousStreaming, previousMetadata, previousMetadataGeneration := streamingActive, initialMetadata, initialMetadataGeneration
	previousLazy, previousOffline := lazyModules, documentOffline
	previousAdmission, previousRefresh, previousActivity := retainedTaskAdmission, devRefresh, refreshActivity
	previousOpener, previousModalFocus := navigationOpener, modalReturnFocus
	previousApplication, previousMerge, previousFrame := mergeApplication, mergeCurrent, currentPropsFrame
	previousEntries, previousEntryOrder, previousInstall := mergeEntryHistory, mergeEntryOrder, mergeInstallActive
	fixture := js.Global().Get("Function").New(`
return (() => {
 let document;
 function node(tag, type=1) {
  const attrs=new Map(), callbacks=new Map();
  return {
   nodeType:type, tagName:tag.toUpperCase(), localName:tag.toLowerCase(),
   parentNode:null, childNodes:[], textContent:'', scrollLeft:0, scrollTop:0,
   get parentElement(){return this.parentNode?.nodeType===1?this.parentNode:null;},
   get firstChild(){return this.childNodes[0]||null;},
   get firstElementChild(){return this.childNodes.find(n=>n.nodeType===1)||null;},
   get isConnected(){return !!this.connectedRoot||!!this.parentNode?.isConnected;},
   get hidden(){return attrs.has('hidden');},set hidden(v){if(v)attrs.set('hidden','');else attrs.delete('hidden');},
   get attributes(){return [...attrs].map(([name,value])=>({name,value}));},
   hasAttribute(k){return attrs.has(k);},getAttribute(k){return attrs.get(k)??null;},
   setAttribute(k,v){attrs.set(k,String(v));},removeAttribute(k){attrs.delete(k);},
   matches(selector){return this.nodeType===1&&selector.split(',').some(part=>{
    part=part.trim(); if(part==='*')return true;
    const attr=/^([a-z][a-z0-9-]*)?\[([^=\]]+)(?:="([^"]*)")?\]$/i.exec(part);
    if(attr)return (!attr[1]||attr[1].toLowerCase()===tag.toLowerCase())&&attrs.has(attr[2])&&(attr[3]===undefined||attrs.get(attr[2])===attr[3]);
    return part.toLowerCase()===tag.toLowerCase();
   });},
   contains(other){return other===this||this.childNodes.some(child=>child.contains(other));},
   closest(selector){for(let n=this;n;n=n.parentElement)if(n.matches(selector))return n;return null;},
   querySelectorAll(selector){const found=[];for(const child of this.childNodes){if(child.matches(selector))found.push(child);found.push(...child.querySelectorAll(selector));}return found;},
   querySelector(selector){return this.querySelectorAll(selector)[0]||null;},
   appendChild(child){if(child.nodeType===11){for(const n of [...child.childNodes])this.appendChild(n);return child;}child.remove();this.childNodes.push(child);child.parentNode=this;return child;},
   remove(){if(this.parentNode){this.parentNode.childNodes=this.parentNode.childNodes.filter(n=>n!==this);this.parentNode=null;}},
   replaceChildren(...children){for(const child of [...this.childNodes])child.remove();for(const child of children)this.appendChild(child);},
   addEventListener(type,fn){if(!callbacks.has(type))callbacks.set(type,new Set());callbacks.get(type).add(fn);},
   removeEventListener(type,fn){callbacks.get(type)?.delete(fn);},
   fire(type='click'){for(const fn of [...(callbacks.get(type)||[])])fn({target:this});},
   listenerCount(){return [...callbacks.values()].reduce((n,set)=>n+set.size,0);},
   focus(){document.activeElement=this;}
  };
 }
 const html=node('html'),head=node('head'),body=node('body'),app=node('div'),status=node('div');
 html.connectedRoot=true;html.appendChild(head);html.appendChild(body);body.appendChild(app);body.appendChild(status);
 app.setAttribute('id','nextgo-app');status.setAttribute('id','nextgo-status');status.hidden=true;
 const viewport=node('meta'),description=node('meta');viewport.setAttribute('name','viewport');description.setAttribute('name','description');head.appendChild(viewport);head.appendChild(description);
 body.setAttribute('data-nextgo-configuration','dev-fixture');
 document={body,head,documentElement:html,activeElement:null,title:'A before',
  get baseURI(){return window.location.href;},
  createElement:tag=>node(tag),createDocumentFragment:()=>node('',11),
  querySelectorAll:selector=>html.querySelectorAll(selector),querySelector:selector=>html.querySelector(selector),
  getElementById(id){return html.querySelectorAll('[id]').find(n=>n.getAttribute('id')===id)||null;}
 };
 const errors=[],window={Object,Array,URL,console:{error(message){errors.push(String(message));}},scrollX:0,scrollY:0,
  location:new URL('https://retained-dev.test/a'),scrollTo(x,y){this.scrollX=x;this.scrollY=y;}};
 const history={state:null,pushes:[],replaces:[],
  pushState(state,unused,target){this.state=state;this.pushes.push(String(target));window.location=new URL(String(target),window.location);},
  replaceState(state,unused,target){this.state=state;this.replaces.push(String(target));window.location=new URL(String(target),window.location);}};
 window.history=history;
 function page(name){
  const incoming=node('body'),main=node('main'),island=node('div'),button=node('button'),output=node('span');
  island.setAttribute('data-nextgo-island','retained-dev-counter');island.setAttribute('data-nextgo-props',JSON.stringify({page:name}));
  button.setAttribute('data-increment','');output.setAttribute('data-count','');output.textContent='0';
  island.appendChild(button);island.appendChild(output);main.appendChild(island);incoming.appendChild(main);
  incoming.body=incoming;return {incoming,main,island,button,output};
 }
 const initial=page('A');app.appendChild(initial.main);
 return {document,window,app,status,initial,page,errors,viewport,description};
})();`).Invoke()
	doc, win = fixture.Get("document"), fixture.Get("window")
	routeManifest = navigation.Manifest{}
	retainedViews, retainedOwners = map[string]*retainedView{}, retainedIndex{}
	retainedCurrent, retainedFocus = nil, nil
	mounted, registry = nil, map[string]Mount{}
	currentURL, currentState, ownedHistory = "https://retained-dev.test/a", js.Null(), historyRegistry{}
	committedLocation, locationObservers = Location{}, map[uint64]*locationObserver{}
	selections, selectionObservers = map[string]Selection{}, map[uint64]selectionObserver{}
	streamingActive, initialMetadata = false, nil
	lazyModules, documentOffline = map[string]*lazyState{}, nil
	retainedTaskAdmission, devRefresh, refreshActivity = false, nil, refreshActivityTracker{}
	// Each prepared document has its own bootstrap authority and props frame.
	// A previous cold commit must never make the next fixture inherit its app.
	mergeApplication, mergeCurrent, mergeInstallActive = nil, nil, false
	currentPropsFrame = &propsFrame{identity: &propsFrameIdentity{}, revision: 1, entries: map[uint64]propsDatum{}}
	mergeEntryHistory, mergeEntryOrder = map[string]*mergeEntryFocus{}, nil
	navigationID, cacheEpoch = 1, 1
	retainedFreshID, retainedFreshEpoch = navigationID, cacheEpoch
	setLocation(currentURL)
	t.Cleanup(func() {
		for _, scope := range mounted {
			scope.dispose()
		}
		doc, win, routeManifest = previousDoc, previousWin, previousManifest
		retainedViews, retainedOwners = previousViews, previousOwners
		retainedCurrent, retainedFocus = previousCurrent, previousFocus
		navigationID, cacheEpoch = previousID, previousEpoch
		retainedFreshID, retainedFreshEpoch = previousFreshID, previousFreshEpoch
		mounted, registry = previousMounted, previousRegistry
		currentURL, currentState, ownedHistory = previousURL, previousState, previousHistory
		committedLocation, locationObservers = previousLocation, previousLocationObservers
		selections, selectionObservers = previousSelections, previousSelectionObservers
		streamingActive, initialMetadata, initialMetadataGeneration = previousStreaming, previousMetadata, previousMetadataGeneration
		lazyModules, documentOffline = previousLazy, previousOffline
		retainedTaskAdmission, devRefresh, refreshActivity = previousAdmission, previousRefresh, previousActivity
		navigationOpener, modalReturnFocus = previousOpener, previousModalFocus
		mergeApplication, mergeCurrent, currentPropsFrame = previousApplication, previousMerge, previousFrame
		mergeEntryHistory, mergeEntryOrder, mergeInstallActive = previousEntries, previousEntryOrder, previousInstall
	})
	return fixture
}

func TestRetainedDevRefreshUsesRealOrdinaryCommitWithoutRetainingViews(t *testing.T) {
	previousApplication, previousMerge, previousFrame := mergeApplication, mergeCurrent, currentPropsFrame
	for _, optIn := range []bool{false, true} {
		name := "default"
		if optIn {
			name = "retention opt-in bypass"
		}
		t.Run(name, func(t *testing.T) {
			f := retainedDevCommitEnvironment(t)
			routeManifest.RetainRoutes = optIn
			// Development is deliberately false: even a retention-enabled
			// manifest cannot override the compiler's devRefreshBuild policy.
			if !devRefreshBuild || routeManifest.Development {
				t.Fatal("wrong compiler personality")
			}
			// Match runDocument: authority also covers ordinary NoProps islands in
			// development, even though both retention managers remain disabled.
			initializeMergeDocument()
			installMergeApplicationAuthority(f.Get("app"), mergeCurrent, initialMergeRetention())
			initializeRetainedDocument()
			if retainedRoutingEnabled() || retainedTaskAdmission {
				t.Fatal("development target enabled route retention")
			}
			mounts, cleanups := 0, 0
			registry["retained-dev-counter"] = func(scope *Scope, raw json.RawMessage) error {
				if _, err := DecodeProps[struct{ Page string }](raw); err != nil {
					return err
				}
				button, err := scope.Require("[data-increment]")
				if err != nil {
					return err
				}
				output, err := scope.Require("[data-count]")
				if err != nil {
					return err
				}
				mounts++
				count := NewState(0)
				scope.Cleanup(count.Subscribe(func(value int) { output.Text(strconv.Itoa(value)) }))
				scope.Cleanup(func() { cleanups++ })
				scope.On(button, "click", func(Event) { count.Set(count.Get() + 1) })
				return nil
			}
			hydrate()
			initial := f.Get("initial")
			for i := 0; i < 9; i++ {
				initial.Get("button").Call("fire")
				if initial.Get("output").Get("textContent").String() != strconv.Itoa(i+1) {
					t.Fatal("ordinary NoProps event did not complete synchronously", i)
				}
			}
			if initial.Get("output").Get("textContent").String() != "9" || mounts != 1 || len(mounted) != 1 {
				t.Fatal("initial eager hydration failed", mounts, len(mounted), f.Get("status").Get("textContent").String())
			}
			if doc.Get("body").Call("getAttribute", "data-nextgo-hydration").String() != "ready" || initial.Get("island").Call("getAttribute", "data-nextgo-hydrated").String() != "true" || !f.Get("status").Get("hidden").Bool() || f.Get("errors").Length() != 0 {
				t.Fatal("ordinary initial hydration did not publish ready without errors")
			}
			firstScope := mounted[0]
			if mergeApplication == nil || mergeApplication.revoked || !mergeApplication.root.Equal(f.Get("app")) || mergeCurrent != nil || !f.Get("app").Get("firstChild").Equal(initial.Get("main")) || !firstScope.Root.Value.Equal(initial.Get("island")) || firstScope.mergeCallbacks {
				t.Fatal("ordinary bootstrap lost current authority, SSR identity or direct event timing")
			}
			if _, exists := mergeApplication.scopes[firstScope]; !exists {
				t.Fatal("ordinary initial scope escaped document authority")
			}
			var secondScope *Scope
			for index, page := range []string{"B", "A"} {
				incoming := f.Get("page").Invoke(page)
				target := []string{"https://retained-dev.test/b", "https://retained-dev.test/a"}[index]
				data := envelope{Status: 200, HTML: "<main>" + page + "</main>", Title: page + " title", Description: page + " description", Document: navigation.Document{Language: "en", Direction: "ltr", Viewport: "width=device-width"}}
				if optIn {
					var err error
					data.Retention, err = navigation.NewRetention("public-demo", data.HTML)
					if err != nil {
						t.Fatal(err)
					}
				}
				navigationID++
				if err := commit(incoming.Get("incoming"), f.Get("app"), data, target, false, js.Null(), false); err != nil {
					t.Fatal(err)
				}
				if currentURL != target || win.Get("location").Get("href").String() != target || committedLocation.URL != target || doc.Get("title").String() != data.Title || f.Get("description").Call("getAttribute", "content").String() != data.Description || f.Get("viewport").Call("getAttribute", "content").String() != data.Document.Viewport || doc.Get("documentElement").Call("getAttribute", "lang").String() != "en" || doc.Get("documentElement").Call("getAttribute", "dir").String() != "ltr" {
					t.Fatal("ordinary commit did not publish URL/title/metadata together")
				}
				if !f.Get("app").Get("firstChild").Equal(incoming.Get("main")) || incoming.Get("main").Equal(initial.Get("main")) || initial.Get("main").Get("isConnected").Bool() || !firstScope.closed || initial.Get("button").Call("listenerCount").Int() != 0 {
					t.Fatal("ordinary commit reused stale DOM or leaked outgoing island")
				}
				initial.Get("button").Call("fire")
				if initial.Get("output").Get("textContent").String() != "9" {
					t.Fatal("detached old listener remained active")
				}
				if doc.Get("body").Call("getAttribute", "data-nextgo-hydration").String() != "ready" || incoming.Get("island").Call("getAttribute", "data-nextgo-hydrated").String() != "true" || len(mounted) != 1 || mounts != index+2 || cleanups != index+1 || incoming.Get("output").Get("textContent").String() != "0" {
					t.Fatal("fresh commit did not hydrate/reset its eager Go state", mounts, cleanups)
				}
				if retainedCurrent != nil || len(retainedViews) != 0 || len(retainedOwners.entries) != 0 || len(retainedOwners.aliases) != 0 || retainedTaskAdmission {
					t.Fatal("development commit created retained owners or aliases")
				}
				history := win.Get("history")
				if history.Get("pushes").Length() != index+1 || history.Get("replaces").Length() != index+1 || history.Get("pushes").Index(index).String() != target {
					t.Fatal("ordinary history push was missing or duplicated")
				}
				if record, ok := trackedHistory(history.Get("state"), target); !ok || record.Previous != []string{"https://retained-dev.test/a", "https://retained-dev.test/b"}[index] {
					t.Fatal("ordinary history record was not published", record, ok)
				}
				if index == 0 {
					secondScope = mounted[0]
				} else if secondScope == nil || !secondScope.closed {
					t.Fatal("second outgoing scope was retained")
				}
				incoming.Get("button").Call("fire")
				if incoming.Get("output").Get("textContent").String() != "1" {
					t.Fatal("fresh eager Go event did not work")
				}
			}
			if f.Get("errors").Length() != 0 {
				t.Fatal("ordinary commit reported errors", f.Get("errors"))
			}
		})
		if mergeApplication != previousApplication || mergeCurrent != previousMerge || currentPropsFrame != previousFrame {
			t.Fatal("prepared document leaked authority or props frame into the next subcase", name)
		}
	}
}
