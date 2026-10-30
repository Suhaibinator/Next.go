//go:build js && wasm && !nextgo_module

package client

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"syscall/js"
	"testing"
	"time"

	"github.com/Suhaibinator/Next.go/internal/navigation"
	"github.com/Suhaibinator/Next.go/internal/paths"
)

// These tests exercise the real FetchStream/Fetch and navigation decoder with
// native Response/Promise/ReadableStream objects. The prepared DOMParser below
// accepts only empty HTML: this is authority/transport coverage, not HTML parsing
// or actual cross-runtime module/browser qualification.
type navigationAuthorityEnvironment struct {
	fixture, app, status         js.Value
	target, build, configuration string
	callbacks                    []js.Func
	streams                      []js.Value
	prefetchCanceled             int
}

func navigationAuthorityEnvironmentFor(t *testing.T) *navigationAuthorityEnvironment {
	t.Helper()
	fixture, app, status := lazyPrivacyEnvironment(t)
	e := &navigationAuthorityEnvironment{fixture: fixture, app: app, status: status, target: "https://authority.test/app/page", build: strings.Repeat("a", 20), configuration: strings.Repeat("c", 64)}
	oldDeployment, oldManifest, oldURL, oldState, oldID, oldCancel := deployment, routeManifest, currentURL, currentState, navigationID, cancelNavigation
	oldCacheEpoch, oldResourceEpoch, oldStores := cacheEpoch, resourceEpoch, resourceStores
	oldPrefetch, oldViewport := prefetches, viewportPrefetch
	oldViews, oldOwners, oldCurrent, oldFocus := retainedViews, retainedOwners, retainedCurrent, retainedFocus
	oldFreshID, oldFreshEpoch := retainedFreshID, retainedFreshEpoch
	oldStreamID, oldStream, oldRetry := streamShellCommittedID, activeNavigationStream, streamRetry
	oldStaticFetch := registeredStaticShellFetch
	oldDev, oldModuleRefresh, oldFence, oldMounting := devRefresh, moduleRefresh, refreshModuleFence, refreshModuleMounting
	refreshActivityMu.Lock()
	oldActivity := refreshActivity
	if oldActivity.busy != 0 {
		refreshActivityMu.Unlock()
		t.Fatal("prior physical refresh work is still active", oldActivity.busy)
	}
	idle := make(chan struct{})
	close(idle)
	refreshActivity = refreshActivityTracker{enabled: true, idle: idle}
	refreshActivityMu.Unlock()
	pageCache.mu.Lock()
	oldEntries, oldSequence, oldBytes, oldWeigh, oldNow := pageCache.entries, pageCache.sequence, pageCache.bytes, pageCache.weigh, pageCache.now
	pageCache.entries = nil
	pageCache.sequence = 0
	pageCache.bytes = 0
	pageCache.weigh = envelopeBytes
	pageCache.now = nil
	pageCache.mu.Unlock()
	deployment = paths.Config{BasePath: "/app"}
	routeManifest = navigation.Manifest{}
	currentURL = e.target
	currentState = js.Undefined()
	navigationID = 7091
	cancelNavigation = nil
	cacheEpoch = 17
	resourceEpoch = 23
	resourceStores = map[*ResourceStore]*Scope{}
	prefetches = map[string]*prefetchRequest{"sentinel": {done: make(chan struct{}), cancel: func() { e.prefetchCanceled++ }}}
	viewportPrefetch = nil
	retainedViews = map[string]*retainedView{}
	retainedOwners = retainedIndex{}
	retainedCurrent = nil
	retainedFocus = nil
	retainedFreshID = 0
	retainedFreshEpoch = 0
	streamShellCommittedID = 0
	activeNavigationStream = nil
	streamRetry = nil
	registeredStaticShellFetch = nil
	devRefresh = nil
	moduleRefresh = nil
	refreshModuleFence = nil
	refreshModuleMounting = false
	app.Set("hidden", false)
	app.Call("removeAttribute", "inert")
	doc.Set("baseURI", "https://authority.test/app/")
	body := doc.Get("body")
	body.Call("setAttribute", "data-nextgo-build", e.build)
	body.Call("setAttribute", "data-nextgo-configuration", e.configuration)
	win.Set("location", js.Global().Get("URL").New(e.target))
	win.Set("Object", js.Global().Get("Object"))
	// Empty prepared selectors cannot claim real parser/native layout behavior.
	win.Set("DOMParser", js.Global().Get("Function").New(`return class {parseFromString(html){if(html!=='')throw Error('authority fixture accepts only empty HTML');const body={querySelectorAll(){return []}};return {body,querySelectorAll(){return []}}}}`).Invoke())
	pageCache.put("sentinel", envelope{Title: "old useful cache"}, time.Minute)
	t.Cleanup(func() {
		// All test-owned lazy waiters/Fetch callers are joined by their later LIFO
		// cleanups before this restoration. Join reader cleanup before releasing its
		// Go pull/cancel/getter functions, including after an earlier Fatal.
		for _, stream := range e.streams {
			propsWait(t, func() bool { return !stream.Get("locked").Bool() })
			_, err := awaitBrowserPromise(context.Background(), stream.Call("cancel"), js.Global().Get("AbortController").New())
			if err != nil {
				t.Error("owned native stream cleanup", err)
			}
		}
		propsWait(t, func() bool {
			return propsWorkerCount() == 0 && mergeCallbackReads == 0 && refreshActivityState().Busy == 0
		})
		for _, callback := range e.callbacks {
			callback.Release()
		}
		deployment, routeManifest, currentURL, currentState, navigationID, cancelNavigation = oldDeployment, oldManifest, oldURL, oldState, oldID, oldCancel
		cacheEpoch, resourceEpoch, resourceStores = oldCacheEpoch, oldResourceEpoch, oldStores
		prefetches, viewportPrefetch = oldPrefetch, oldViewport
		retainedViews, retainedOwners, retainedCurrent, retainedFocus = oldViews, oldOwners, oldCurrent, oldFocus
		retainedFreshID, retainedFreshEpoch = oldFreshID, oldFreshEpoch
		streamShellCommittedID, activeNavigationStream, streamRetry = oldStreamID, oldStream, oldRetry
		registeredStaticShellFetch = oldStaticFetch
		devRefresh, moduleRefresh, refreshModuleFence, refreshModuleMounting = oldDev, oldModuleRefresh, oldFence, oldMounting
		pageCache.mu.Lock()
		pageCache.entries, pageCache.sequence, pageCache.bytes, pageCache.weigh, pageCache.now = oldEntries, oldSequence, oldBytes, oldWeigh, oldNow
		pageCache.mu.Unlock()
		refreshActivityMu.Lock()
		refreshActivity = oldActivity
		refreshActivityMu.Unlock()
	})
	return e
}
func (e *navigationAuthorityEnvironment) callback(fn func(js.Value, []js.Value) any) js.Func {
	value := js.FuncOf(fn)
	e.callbacks = append(e.callbacks, value)
	return value
}
func (e *navigationAuthorityEnvironment) headers() map[string]any {
	return map[string]any{"Content-Type": "application/json", navigation.ProtocolHeader: fmt.Sprint(navigation.ProtocolVersion), navigation.DraftHeader: "", navigation.ConfigurationHeader: e.configuration, navigation.StreamHeader: fmt.Sprint(navigation.StreamVersion)}
}
func (e *navigationAuthorityEnvironment) envelope(t *testing.T, build string, status int) []byte {
	t.Helper()
	value := envelope{Version: navigation.ProtocolVersion, Configuration: e.configuration, Build: build, URL: e.target, Status: status, Document: navigation.Document{Language: "en", Direction: "ltr", Viewport: navigation.DefaultViewport}, HTML: "", Title: "Fresh accepted JSON"}
	raw, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	return raw
}

// The getter hook runs after awaitBrowserPromise has checked its context but
// before fetchBrowser invokes onHeaders. EOF runs on a done:true native read,
// after the last data callback, reproducing the missing post-Fetch ID fence.
func (e *navigationAuthorityEnvironment) response(body []byte, status int, headers map[string]any, finalURL string, atHeaders, atEOF func()) (js.Value, *int) {
	pulls, bodyReads := 0, 0
	pull := e.callback(func(_ js.Value, args []js.Value) any {
		controller := args[0]
		pulls++
		if pulls == 1 && len(body) > 0 {
			data := js.Global().Get("Uint8Array").New(len(body))
			js.CopyBytesToJS(data, body)
			controller.Call("enqueue", data)
			return nil
		}
		if atEOF != nil {
			atEOF()
		}
		controller.Call("close")
		return nil
	})
	cancel := e.callback(func(js.Value, []js.Value) any { return nil })
	stream := js.Global().Get("ReadableStream").New(map[string]any{"pull": pull, "cancel": cancel}, map[string]any{"highWaterMark": 0})
	e.streams = append(e.streams, stream)
	response := js.Global().Get("Response").New(stream, map[string]any{"status": status, "headers": headers})
	object := js.Global().Get("Object")
	object.Call("defineProperty", response, "url", map[string]any{"value": finalURL})
	if atHeaders != nil {
		nativeHeaders := response.Get("headers")
		getter := e.callback(func(js.Value, []js.Value) any { atHeaders(); return nativeHeaders })
		object.Call("defineProperty", response, "headers", map[string]any{"get": getter})
	}
	getter := e.callback(func(js.Value, []js.Value) any { bodyReads++; return stream })
	object.Call("defineProperty", response, "body", map[string]any{"get": getter})
	return js.Global().Get("Promise").Call("resolve", response), &bodyReads
}
func (e *navigationAuthorityEnvironment) ordinarySource(t *testing.T) (*Scope, *PropsSource[int], *int, *int) {
	t.Helper()
	root := e.fixture.Get("root")
	root.Call("removeAttribute", clientBoundaryAttribute)
	scope := &Scope{Root: Element{root}}
	t.Cleanup(scope.dispose)
	clicks, cleanups := 0, 0
	var source *PropsSource[int]
	err := withMergeApplicationMount(scope, func() error {
		return mountScoped(scope, func(owner *Scope, raw json.RawMessage) error {
			var err error
			source, err = Props(owner, raw, PropsOptions[int]{Clone: func(value int) int { return value }})
			if err != nil {
				return err
			}
			owner.On(Element{e.fixture.Get("fallback")}, "click", func(Event) { clicks++ })
			owner.Cleanup(func() { cleanups++ })
			return nil
		}, json.RawMessage(`7`))
	})
	if err != nil {
		t.Fatal(err)
	}
	root.Call("setAttribute", "data-nextgo-hydrated", "true")
	if value, err := source.GetSnapshot(); err != nil || value.Value != 7 {
		t.Fatal("ordinary initial source unreadable", value, err)
	}
	return scope, source, &clicks, &cleanups
}
func (e *navigationAuthorityEnvironment) unchanged(t *testing.T, source *PropsSource[int], epoch, resource uint64) {
	t.Helper()
	if value, err := source.GetSnapshot(); err != nil || value.Value != 7 || mergeApplication.revoked || e.app.Get("hidden").Bool() || e.app.Call("hasAttribute", "inert").Bool() {
		t.Fatal("non-authority response revoked useful application", value, err)
	}
	if cacheEpoch != epoch || resourceEpoch != resource || e.prefetchCanceled != 0 {
		t.Fatal("stale response invalidated live cache/resource/prefetch", cacheEpoch, resourceEpoch, e.prefetchCanceled)
	}
	if _, ok := pageCache.get("sentinel"); !ok {
		t.Fatal("stale response discarded useful cached page")
	}
}

func TestNavigationStreamAuthorityHeadersRevokeBeforeBodyAndFenceOwnedScopes(t *testing.T) {
	for _, kind := range []string{"draft", "protocol", "protocol-status500", "stream-configuration"} {
		t.Run(kind, func(t *testing.T) {
			e := navigationAuthorityEnvironmentFor(t)
			scope, source, clicks, cleanups := e.ordinarySource(t)
			e.fixture.Get("fallback").Call("dispatchEvent", js.Global().Get("Event").New("click"))
			if *clicks != 1 {
				t.Fatal("initial Props-owned event not bound")
			}
			// A separate bare eager owner retains direct native cancellation semantics;
			// its authority must not depend on registering any PropsSource or lifetime.
			eagerRoot := e.fixture.Call("make", "button")
			e.app.Call("appendChild", eagerRoot)
			eager := &Scope{Root: Element{eagerRoot}}
			t.Cleanup(eager.dispose)
			eagerClicks, eagerCleanups := 0, 0
			if err := withMergeApplicationMount(eager, func() error {
				return mountScoped(eager, func(owner *Scope, _ json.RawMessage) error {
					owner.On(Element{eagerRoot}, "click", func(event Event) { eagerClicks++; event.PreventDefault() })
					owner.Cleanup(func() { eagerCleanups++ })
					return nil
				}, nil)
			}); err != nil {
				t.Fatal(err)
			}
			event := js.Global().Get("Event").New("click", map[string]any{"cancelable": true})
			eagerRoot.Call("dispatchEvent", event)
			if eagerClicks != 1 || !event.Get("defaultPrevented").Bool() || eager.mergeCallbacks {
				t.Fatal("bare eager owner lost direct native timing", eagerClicks, eager.mergeCallbacks)
			}
			lazyRoot := e.fixture.Call("make", "div")
			lazyRoot.Call("setAttribute", "data-nextgo-island", "deferred")
			lazyRoot.Call("setAttribute", "data-nextgo-props", "null")
			e.app.Call("appendChild", lazyRoot)
			proxy := &Scope{Root: Element{lazyRoot}}
			t.Cleanup(proxy.dispose)
			ready := make(chan struct{})
			state := &lazyState{loading: true, ready: ready, exports: js.Undefined()}
			lazyModules["deferred"] = state
			done, err := lazyHydrate(proxy, "deferred")
			t.Cleanup(func() {
				select {
				case <-ready:
				default:
					close(ready)
				}
				if done != nil {
					<-done
				}
			})
			if err != nil {
				t.Fatal(err)
			}
			headers, status := e.headers(), 200
			switch kind {
			case "draft":
				headers[navigation.DraftHeader] = "1"
			case "protocol":
				headers[navigation.ProtocolHeader] = "999"
			case "protocol-status500":
				headers[navigation.ProtocolHeader] = "999"
				status = 500
			case "stream-configuration":
				headers["Content-Type"] = navigation.StreamMediaType
				headers[navigation.ConfigurationHeader] = strings.Repeat("d", 64)
			}
			response, bodyReads := e.response(nil, status, headers, e.target, nil, nil)
			mockBrowserFetch(t, func([]js.Value) any { return response })
			_, streamed, fallback, err := fetchActiveNavigation(context.Background(), e.target, e.target, false, js.Undefined(), navigationID, NavigationOptions{}, nil)
			if err != nil || streamed || !fallback || *bodyReads != 0 {
				t.Fatal("header fence acquired body or changed fallback", err, streamed, fallback, *bodyReads)
			}
			if value, err := source.GetSnapshot(); !errors.Is(err, errPropsClosed) {
				t.Fatal("header mismatch left ordinary props readable", value, err)
			}
			e.fixture.Get("fallback").Call("dispatchEvent", js.Global().Get("Event").New("click"))
			eagerRoot.Call("dispatchEvent", js.Global().Get("Event").New("click"))
			if scope.active() || eager.active() || eager.mergeCallbacks || proxy.active() || *clicks != 1 || eagerClicks != 1 || *cleanups != 0 || eagerCleanups != 0 || !mergeApplication.revoked || !e.app.Get("hidden").Bool() || !e.app.Call("hasAttribute", "inert").Bool() || !e.app.Get("isConnected").Bool() || !e.app.Call("contains", scope.Root.Value).Bool() || !e.app.Call("contains", eagerRoot).Bool() {
				t.Fatal("header fence waited for cleanup, replaced DOM or left eager/pending owners live", *clicks, *cleanups, eagerClicks, eagerCleanups)
			}
			calls := 0
			exports := js.Global().Get("Object").New()
			mount := e.callback(func(js.Value, []js.Value) any { calls++; return map[string]any{"id": 1, "error": ""} })
			dispose := e.callback(func(js.Value, []js.Value) any { return "" })
			exports.Set("mount", mount)
			exports.Set("dispose", dispose)
			state.exports = exports
			close(ready)
			<-done
			if calls != 0 || *cleanups != 0 || lazyRoot.Call("getAttribute", "data-nextgo-hydrated").String() != "loading" {
				t.Fatal("late ready module mounted denied old DOM", calls, *cleanups)
			}
		})
	}
}

func TestNavigationStreamStaleHeadersEOFAndActualDeadlinePreserveAuthority(t *testing.T) {
	for _, kind := range []string{"superseded-headers", "canceled-headers", "superseded-json-eof"} {
		t.Run(kind, func(t *testing.T) {
			e := navigationAuthorityEnvironmentFor(t)
			_, source, _, _ := e.ordinarySource(t)
			epoch, resource, id := cacheEpoch, resourceEpoch, navigationID
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			headers := e.headers()
			body := e.envelope(t, strings.Repeat("b", 20), 200)
			var atHeaders, atEOF func()
			mutations := 0
			if kind == "superseded-json-eof" {
				atEOF = func() { mutations++; navigationID++ }
			} else {
				headers[navigation.ProtocolHeader] = "999"
				if kind == "superseded-headers" {
					atHeaders = func() { mutations++; navigationID++ }
				} else {
					atHeaders = func() { mutations++; cancel() }
				}
			}
			response, bodyReads := e.response(body, 200, headers, e.target, atHeaders, atEOF)
			mockBrowserFetch(t, func([]js.Value) any { return response })
			_, streamed, fallback, err := fetchActiveNavigation(ctx, e.target, e.target, false, js.Undefined(), id, NavigationOptions{}, nil)
			if !errors.Is(err, context.Canceled) || streamed || fallback || mutations != 1 {
				t.Fatal("superseded/canceled response reached fallback/decoder or missed race point", err, streamed, fallback, mutations)
			}
			expectedReads := 0
			if kind == "superseded-json-eof" {
				expectedReads = 1
			}
			if *bodyReads != expectedReads {
				t.Fatal("race used wrong header/body boundary", kind, *bodyReads)
			}
			e.unchanged(t, source, epoch, resource)
		})
	}
	t.Run("actual-deadline-flight", func(t *testing.T) {
		e := navigationAuthorityEnvironmentFor(t)
		_, source, _, _ := e.ordinarySource(t)
		epoch, resource := cacheEpoch, resourceEpoch
		// Observe the actual Go-installed Promise handler return. The native wrapper
		// forwards its real value/error once; it does not implement await or Fetch.
		makeFlight := js.Global().Get("Function").New("signal", `const r={callbackReturned:false,aborted:false};let reject;const p=new Promise((_,no)=>reject=no);const then=p.then;p.then=function(ok,no){return then.call(this,v=>{try{return ok(v)}finally{r.callbackReturned=true}},e=>{try{return no(e)}finally{r.callbackReturned=true}})};signal.addEventListener('abort',()=>{r.aborted=true;reject(new Error('owned native abort'))},{once:true});r.promise=p;return r;`)
		flight := js.Undefined()
		started := make(chan struct{})
		finished := make(chan struct{})
		result := make(chan error, 1)
		mockBrowserFetch(t, func(args []js.Value) any {
			flight = makeFlight.Invoke(args[1].Get("signal"))
			close(started)
			return flight.Get("promise")
		})
		ctx, cancel := context.WithTimeout(context.Background(), time.Second)
		t.Cleanup(func() {
			cancel()
			<-finished
			if !flight.IsUndefined() {
				propsWait(t, func() bool { return flight.Get("callbackReturned").Bool() && refreshActivityState().Busy == 0 })
			}
		})
		go func() {
			defer close(finished)
			_, streamed, fallback, err := fetchActiveNavigation(ctx, e.target, e.target, false, js.Undefined(), navigationID, NavigationOptions{}, nil)
			if streamed || fallback {
				result <- fmt.Errorf("deadline changed navigation result")
				return
			}
			result <- err
		}()
		select {
		case <-started:
		case <-finished:
			t.Fatal("deadline elapsed before native fetch flight started")
		}
		if err := <-result; !errors.Is(err, context.DeadlineExceeded) {
			t.Fatal("post-Fetch fence lost deadline cause", err)
		}
		<-finished
		propsWait(t, func() bool { return flight.Get("callbackReturned").Bool() && refreshActivityState().Busy == 0 })
		if !flight.Get("aborted").Bool() {
			t.Fatal("deadline did not abort actual native fetch signal")
		}
		e.unchanged(t, source, epoch, resource)
	})
}

func TestNavigationStreamTransportRejectionsAndValidJSONDoNotRevokeAuthority(t *testing.T) {
	for _, kind := range []string{"valid-json", "json-status500", "valid-stream-headers-malformed-frame", "stream-status503", "stream-status503-bad-config", "stream-codec", "stream-codec-bad-config", "metadata-version", "metadata-on-json", "media", "malformed-json", "network", "external", "outside-base"} {
		t.Run(kind, func(t *testing.T) {
			e := navigationAuthorityEnvironmentFor(t)
			_, source, _, _ := e.ordinarySource(t)
			epoch, resource := cacheEpoch, resourceEpoch
			headers, status, final := e.headers(), 200, e.target
			body := e.envelope(t, e.build, 200)
			wantFallback, wantError := true, false
			switch kind {
			case "valid-json":
				wantFallback = false
			case "json-status500":
				status = 500
				body = e.envelope(t, e.build, status)
				wantFallback = false
			case "valid-stream-headers-malformed-frame":
				headers["Content-Type"] = navigation.StreamMediaType
				body = []byte("malformed frame\n")
				wantFallback = false
				wantError = true
			case "stream-status503", "stream-status503-bad-config":
				status = 503
				headers["Content-Type"] = navigation.StreamMediaType
				if strings.HasSuffix(kind, "bad-config") {
					headers[navigation.ConfigurationHeader] = strings.Repeat("d", 64)
				}
			case "stream-codec", "stream-codec-bad-config":
				headers["Content-Type"] = navigation.StreamMediaType
				headers[navigation.StreamHeader] = "999"
				if strings.HasSuffix(kind, "bad-config") {
					headers[navigation.ConfigurationHeader] = strings.Repeat("d", 64)
				}
			case "metadata-version":
				headers[navigation.MetadataHeader] = "999"
			case "metadata-on-json":
				headers[navigation.MetadataHeader] = fmt.Sprint(navigation.MetadataVersion)
			case "media":
				headers["Content-Type"] = "text/html"
			case "malformed-json":
				body = []byte("{")
				wantFallback = false
				wantError = true
			case "network":
				wantFallback = false
				wantError = true
			case "external":
				final = "https://other.test/app/page"
				headers[navigation.ProtocolHeader] = "999"
				wantFallback = false
				wantError = true
			case "outside-base":
				final = "https://authority.test/outside"
				headers[navigation.ProtocolHeader] = "999"
			}
			response, bodyReads := e.response(body, status, headers, final, nil, nil)
			if kind == "network" {
				response = js.Global().Get("Promise").Call("reject", js.Global().Get("Error").New("owned transport failure"))
			}
			mockBrowserFetch(t, func([]js.Value) any { return response })
			data, streamed, fallback, err := fetchActiveNavigation(context.Background(), e.target, e.target, false, js.Undefined(), navigationID, NavigationOptions{}, nil)
			if streamed || fallback != wantFallback || (err != nil) != wantError {
				t.Fatal("transport acceptance changed", kind, err, streamed, fallback)
			}
			if kind == "valid-json" || kind == "json-status500" {
				if data.Build != e.build || data.Status != status || data.Title != "Fresh accepted JSON" || *bodyReads != 1 {
					t.Fatal("complete valid JSON did not traverse actual body/decoder", data, *bodyReads)
				}
			}
			if kind == "valid-stream-headers-malformed-frame" && *bodyReads != 1 {
				t.Fatal("valid stream headers did not reach malformed body", *bodyReads)
			}
			if wantFallback && *bodyReads != 0 {
				t.Fatal("header-only fallback acquired a body", kind, *bodyReads)
			}
			// Ordinary header transport fallback deliberately invalidates reuse, but it
			// must not hide/revoke useful published component authority.
			if value, err := source.GetSnapshot(); err != nil || value.Value != 7 || mergeApplication.revoked || e.app.Get("hidden").Bool() || e.app.Call("hasAttribute", "inert").Bool() {
				t.Fatal("transport-only failure revoked useful source", kind, value, err)
			}
			if !wantFallback {
				e.unchanged(t, source, epoch, resource)
			}
		})
	}
}

func TestNavigationStreamPrefetchAndStaticDelegateKeepTheirExistingJSONTransport(t *testing.T) {
	for _, kind := range []string{"prefetch-draft", "prefetch-protocol", "prefetch-configuration", "prefetch-build", "static-active"} {
		t.Run(kind, func(t *testing.T) {
			e := navigationAuthorityEnvironmentFor(t)
			_, source, _, _ := e.ordinarySource(t)
			headers := e.headers()
			body := e.envelope(t, e.build, 200)
			ctx := context.Background()
			target := e.target
			if kind == "static-active" {
				doc.Get("body").Call("setAttribute", "data-nextgo-export", "true")
			} else {
				ctx = context.WithValue(ctx, prefetchContextKey{}, true)
			}
			switch kind {
			case "prefetch-draft":
				headers[navigation.DraftHeader] = "1"
			case "prefetch-protocol":
				headers[navigation.ProtocolHeader] = "999"
			case "prefetch-configuration":
				headers[navigation.ConfigurationHeader] = strings.Repeat("d", 64)
			case "prefetch-build":
				body = e.envelope(t, strings.Repeat("b", 20), 200)
			}
			fetched, navigateHeader, streamHeader, purpose, metadata, shellProtocol := "", "", "", "", "", ""
			if kind == "static-active" {
				target = "https://authority.test/app/_nextgo/data/page.json"
			}
			response, _ := e.response(body, 200, headers, target, nil, nil)
			mockBrowserFetch(t, func(args []js.Value) any {
				fetched = args[0].String()
				h := args[1].Get("headers")
				get := func(name string) string {
					value := h.Call("get", name)
					if value.IsNull() {
						return ""
					}
					return value.String()
				}
				navigateHeader = get("X-Nextgo-Navigate")
				streamHeader = get(navigation.StreamHeader)
				purpose = get(navigation.PurposeHeader)
				metadata = get(navigation.MetadataHeader)
				shellProtocol = get(navigation.ShellProtocolHeader)
				return response
			})
			if kind == "static-active" {
				data, streamed, fallback, err := fetchActiveNavigation(ctx, e.target, e.target, false, js.Undefined(), navigationID, NavigationOptions{}, nil)
				if err != nil || streamed || fallback || data.Build != e.build || fetched != "https://authority.test/app/_nextgo/data/page.json" || navigateHeader != "" || streamHeader != "" || purpose != "" || metadata != "" || shellProtocol != "" {
					t.Fatal("static active delegate changed JSON fetch semantics", err, streamed, fallback, fetched, navigateHeader, streamHeader, purpose)
				}
			} else {
				_, _, fallback, err := fetchEnvelope(ctx, e.target)
				if err != nil || !fallback || fetched != e.target || navigateHeader != "1" || purpose != "prefetch" || streamHeader != "" || metadata != "" || shellProtocol != fmt.Sprint(navigation.ShellVersion) {
					t.Fatal("prefetch stopped using independent JSON transport", err, fallback, fetched, navigateHeader, purpose, streamHeader)
				}
			}
			if value, err := source.GetSnapshot(); err != nil || value.Value != 7 || mergeApplication.revoked || e.app.Get("hidden").Bool() || e.app.Call("hasAttribute", "inert").Bool() {
				t.Fatal("prefetch/static transport revoked active component", kind, value, err)
			}
		})
	}
}
