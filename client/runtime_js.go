//go:build js && wasm

package client

import (
	"context"
	"encoding/json"
	"fmt"
	"github.com/Suhaibinator/Next.go/internal/jsonld"
	"github.com/Suhaibinator/Next.go/internal/navigation"
	"github.com/Suhaibinator/Next.go/internal/publicconfig"
	"net/url"
	"strings"
	"syscall/js"
	"time"
	"unicode"
)

type Mount func(*Scope, json.RawMessage) error

var registry = map[string]Mount{}
var registeredModuleHostInstaller func()
var mounted []*Scope
var globalFunctions []js.Func
var doc = js.Global().Get("document")
var win = js.Global()
var currentURL string
var currentState js.Value
var cancelNavigation context.CancelFunc
var navigationID uint64
var pageCache = navigationCache[envelope]{weigh: envelopeBytes}
var prefetches = map[string]*prefetchRequest{}
var cacheEpoch uint64
var routeManifest navigation.Manifest

// Document-only implementations register these hooks from the core target.
// Lazy components keep the existing location/path snapshot and host API.
var registeredStaticShellInitializer func() error
var registeredStaticShellFetch func(context.Context, string) (data envelope, ttl time.Duration, handled, fallback bool, err error)

type prefetchContextKey struct{}

type prefetchRequest struct {
	done     chan struct{}
	cancel   context.CancelFunc
	viewport bool
}

func Register(name string, mount Mount) {
	if registry[name] != nil || lazyModules[name] != nil {
		panic("duplicate island " + name)
	}
	registry[name] = mount
}

func report(message string) {
	win.Get("console").Call("error", "Next.go: "+message)
	status := doc.Call("getElementById", "nextgo-status")
	status.Set("hidden", false)
	status.Set("textContent", message)
}
func hydrate() { hydrateManaged(nil, nil) }

func hydrateManaged(onError func(js.Value, error), authority func() bool) {
	reportError := func(root js.Value, err error) {
		if onError != nil {
			onError(root, err)
		} else {
			report(err.Error())
		}
	}
	if refreshFrozen() {
		return
	}
	if err := hydrateScripts(); err != nil {
		reportError(js.Null(), fmt.Errorf("Invalid script declaration: %w", err))
		doc.Get("body").Call("setAttribute", "data-nextgo-hydration", "error")
		return
	}
	snapshot, selectionErr := selectionNodes(applicationContentRoot())
	if selectionErr != nil {
		reportError(js.Null(), fmt.Errorf("Invalid selection snapshot: %w", selectionErr))
		doc.Get("body").Call("setAttribute", "data-nextgo-hydration", "error")
		return
	}
	selections = snapshot
	syncLazyModules()

	nodes := applicationIslandNodes()
	// Record fallback ancestry before any mount detaches fallback nodes. The
	// static NodeList retains those nodes after replacement.
	fallbacks := make([]bool, nodes.Length())
	for i := range fallbacks {
		fallbacks[i] = insideClientFallback(nodes.Index(i))
	}
	ok := true
	for i := 0; i < nodes.Length(); i++ {
		if authority != nil && !authority() {
			return
		}
		root := nodes.Index(i)
		// A static query snapshot can retain fallback descendants even after a
		// preceding boundary mounted. Never initialize behavior inside fallback.
		if fallbacks[i] || insideClientFallback(root) {
			continue
		}
		recordResourceOrigin(root)
		if root.Call("hasAttribute", "data-nextgo-hydrated").Bool() {
			if state := root.Call("getAttribute", "data-nextgo-hydrated").String(); state == "error" || state == "loading" {
				ok = false
			}
			continue
		}
		if _, err := validateClientBoundary(root); err != nil {
			root.Call("setAttribute", "data-nextgo-hydrated", "error")
			reportError(root, err)
			ok = false
			continue
		}
		scope := &Scope{Root: Element{root}}
		name := root.Call("getAttribute", "data-nextgo-island").String()
		if lazyModules[name] != nil {
			if _, err := lazyHydrate(scope, name); err != nil {
				if scope.mergeRevoked || mergeApplicationRevoked() {
					return
				}
				root.Call("setAttribute", "data-nextgo-hydrated", "error")
				reportError(root, err)
			}
			ok = false
			continue
		}
		err := func() (err error) {
			defer func() {
				if v := recover(); v != nil {
					err = fmt.Errorf("mount panic: %v", v)
				}
			}()
			name := root.Call("getAttribute", "data-nextgo-island").String()
			mount := registry[name]
			if mount == nil {
				return fmt.Errorf("unregistered island %q", name)
			}
			return withMergeApplicationMount(scope, func() error {
				if onError != nil {
					work := func() error {
						return mountScoped(scope, mount, json.RawMessage(root.Call("getAttribute", "data-nextgo-props").String()))
					}
					if mergeCurrent != nil {
						return withMergeMountReport(scope, work)
					}
					return withMergeMountDiagnostics(scope, work)
				}
				return mountScoped(scope, mount, json.RawMessage(root.Call("getAttribute", "data-nextgo-props").String()))
			})
		}()
		if authority != nil && !authority() {
			scope.revokeMergeAuthority()
			_ = withMergeCleanupReport(scope, func() error { scope.dispose(); return nil })
			return
		}
		if err != nil {
			scope.dispose()
			if scope.mergeRevoked || mergeApplicationRevoked() {
				return
			}
			root.Call("setAttribute", "data-nextgo-hydrated", "error")
			reportError(root, err)
			ok = false
			continue
		}
		root.Call("setAttribute", "data-nextgo-hydrated", "true")
		mounted = append(mounted, scope)
	}
	state := "ready"
	if retainedCurrent != nil && retainedCurrent.activityErr != nil {
		ok = false
	}
	if !ok {
		state = "loading"
		if applicationHydrationError() {
			state = "error"
		}
	}
	if authority != nil && !authority() {
		return
	}
	doc.Get("body").Call("setAttribute", "data-nextgo-hydration", state)
}

// Run hydrates once and installs navigation. Generated main keeps the Go runtime alive.
func Run() {
	if moduleMode {
		panic("client.Run cannot own a document from a component module")
	}
	hydrateInitial := func() {
		// Shell documents keep their useful build fallback until descriptor,
		// manifest and document ownership have all been checked.
		if doc.Get("body").Call("getAttribute", "data-nextgo-static-shell").IsNull() {
			hydrate()
		}
	}
	// Islands still hydrate when document validation disables SPA routing. Give
	// their location subscriptions an initial snapshot without enabling Navigate
	// (currentURL remains unset until all router validation succeeds).
	setLocation(win.Get("location").Get("href").String())
	deployment.TrailingSlash = doc.Get("body").Call("getAttribute", "data-nextgo-trailing-slash").String() == "true"
	deployment.BasePath = doc.Get("body").Call("getAttribute", "data-nextgo-basepath").String()
	deployment.AssetPrefix = doc.Get("body").Call("getAttribute", "data-nextgo-assets").String()
	// Missing attributes support shells generated before path configuration.
	if doc.Get("body").Call("getAttribute", "data-nextgo-basepath").IsNull() {
		deployment.BasePath = ""
	}
	if doc.Get("body").Call("getAttribute", "data-nextgo-assets").IsNull() {
		deployment.AssetPrefix = ""
	}
	if err := deployment.Validate(); err != nil {
		report("Invalid deployment paths")
		return
	}
	if err := primeEarlyScripts(); err != nil {
		report(err.Error())
		return
	}
	if install := registeredModuleHostInstaller; install != nil {
		install()
	}
	installActionViewLifecycle()
	if raw := doc.Get("body").Call("getAttribute", "data-nextgo-navigation"); !raw.IsNull() {
		if err := json.Unmarshal([]byte(raw.String()), &routeManifest); err != nil {
			hydrateInitial()
			report("Invalid navigation manifest")
			return
		}
	}

	public, publicErr := publicconfig.Clone(routeManifest.Public)
	if publicErr != nil {
		routeManifest.Public = nil
		report("Invalid public configuration")
		return
	}
	routeManifest.Public = public

	if mode := documentDraft(); (routeManifest.Draft && mode != "0" && mode != "1") || (!routeManifest.Draft && mode != "") {
		hydrateInitial()
		report("Invalid draft session marker; reload the page")
		return
	}
	configuration := doc.Get("body").Call("getAttribute", "data-nextgo-configuration")
	expected := navigation.ConfigurationID(doc.Get("body").Call("getAttribute", "data-nextgo-build").String(), deployment, routeManifest, doc.Get("body").Call("getAttribute", "data-nextgo-preserve-layouts").String() == "true", doc.Get("body").Call("getAttribute", "data-nextgo-transitions").String() == "true")
	if configuration.IsNull() || configuration.String() != expected {
		hydrateInitial()
		report("Incompatible document configuration; reload the page")
		return
	}

	viewport := doc.Call("querySelector", `meta[name="viewport"]`)
	initialDocument := navigation.Document{Language: doc.Get("documentElement").Call("getAttribute", "lang").String(), Direction: doc.Get("documentElement").Call("getAttribute", "dir").String()}
	if !viewport.IsNull() {
		initialDocument.Viewport = viewport.Call("getAttribute", "content").String()
	}
	if err := initialDocument.Validate(); err != nil {
		hydrateInitial()
		report("Invalid document attributes; reload the page")
		return
	}
	if initialize := registeredStaticShellInitializer; initialize != nil {
		if err := initialize(); err != nil {
			// Keep the build-rendered fallback; a malformed descriptor must not
			// mount a shell at an unvalidated deep URL or trigger a reload loop.
			report("Invalid static shell document: " + err.Error())
			return
		}
	}
	savedOffline, offlineErr := initializeOfflineDocument()
	if offlineErr != nil {
		hydrateInitial()
		report("Invalid offline document configuration")
		return
	}
	if savedOffline {
		savedOfflineHydrate()
		return
	}
	if bootstrapDevRefresh(runDocument) {
		return
	}
	runDocument()
}

func runDocument() {
	installMotionPreference()
	currentURL = win.Get("location").Get("href").String()
	setLocation(currentURL)
	initializeMergeDocument()
	installMergeApplicationAuthority(doc.Call("getElementById", "nextgo-app"), mergeCurrent, initialMergeRetention())
	if mergeCurrent == nil {
		initializeRetainedDocument()
	}
	mergeBooting = true
	releaseBoot, bootErr := acquireMergeCommit(context.Background(), navigationID, mergeCurrent != nil)
	if bootErr != nil {
		mergeBooting = false
		report(mergePublicFailure)
		return
	}
	bootIntent, bootPrivacy, initial := navigationID, mergePrivacy, mergeCurrent
	bootApplication := mergeApplicationToken()
	defer func() { mergeBooting = false; releaseBoot() }()
	if mergeActiveLease != nil {
		mergeActiveLease.published = initial
	}
	win.Get("history").Set("scrollRestoration", "manual")
	saveScroll()
	currentState = win.Get("history").Get("state")
	startStreaming()
	hydrateManaged(func(root js.Value, err error) {
		if initial != nil {
			mergeHydrationDiagnostic(root, err)
			return
		}
		if bootIntent == navigationID {
			report(err.Error())
		} else {
			win.Get("console").Call("error", "Next.go initial mount: "+err.Error())
		}
	}, func() bool {
		return mergeApplicationCurrent(bootApplication) && mergeCurrent == initial && bootPrivacy == mergePrivacy && (initial == nil || !initial.revoked)
	})
	captureMergeBindings()
	installDraftLifecycle()
	installViewportPrefetch()
	listen(doc, "click", func(e js.Value) {
		if e.Get("defaultPrevented").Bool() || e.Get("button").Int() != 0 || e.Get("metaKey").Bool() || e.Get("ctrlKey").Bool() || e.Get("shiftKey").Bool() || e.Get("altKey").Bool() {
			return
		}
		link, u, ok := eligibleLink(e.Get("target"))
		if !ok {
			return
		}
		path := u.Get("pathname").String()
		old := win.Get("URL").New(currentURL)
		if path == old.Get("pathname").String() && u.Get("search").String() == old.Get("search").String() {
			cancelPendingNavigation()
			if link.Call("getAttribute", "data-nextgo-replace").String() == "true" {
				e.Call("preventDefault")
				if err := Replace(u.Get("href").String()); err != nil {
					report(err.Error())
				}
			}
			return
		}
		e.Call("preventDefault")
		saveScroll()
		startLinkNavigation(u.Get("href").String(), false, js.Undefined(), NavigationOptions{Transition: linkTransition(link), Replace: link.Call("getAttribute", "data-nextgo-replace").String() == "true"}, link)
	})
	for _, event := range []string{"pointerover", "focusin"} {
		listen(doc, event, func(e js.Value) {
			a, u, ok := eligibleLink(e.Get("target"))
			if !ok || a.Call("getAttribute", "data-nextgo-prefetch").String() == "false" {
				return
			}
			old := win.Get("URL").New(currentURL)
			if u.Get("pathname").String() == old.Get("pathname").String() && u.Get("search").String() == old.Get("search").String() {
				return
			}
			prefetch(u.Get("href").String())
		})
	}
	listen(win, "popstate", func(e js.Value) {
		target := win.Get("location").Get("href").String()
		old, u := win.Get("URL").New(currentURL), win.Get("URL").New(target)
		if old.Get("pathname").String() == u.Get("pathname").String() && old.Get("search").String() == u.Get("search").String() && ((mergeCurrent != nil && !mergeCurrent.revoked) || len(propsBindings) > 0 || retainedPopSameView(e.Get("state"), target)) {
			saved := e.Get("state")
			cancelPendingNavigation()
			intent := navigationID
			if mergeDeferNativeCommit(mergeNativePop, target, func() { commitMergeNativePop(target, saved, intent) }) {
				return
			}
			if mergeCurrent != nil || len(propsBindings) > 0 {
				commitMergeNativePop(target, saved, intent)
				return
			}
			currentURL = target
			setLocation(target)
			currentState = e.Get("state")
			restoreScroll(target, true, currentState)
			if retainedCurrent != nil {
				saveScroll()
			}
			notifyLocationObservers()
			return
		}
		startNavigation(target, true, e.Get("state"))
	})
	listen(win, "hashchange", func(_ js.Value) {
		target := win.Get("location").Get("href").String()
		old, next := win.Get("URL").New(currentURL), win.Get("URL").New(target)
		// A hash event may follow popstate. Do not publish twice or publish a
		// pending cross-page history target before its server response commits.
		if target == currentURL || old.Get("pathname").String() != next.Get("pathname").String() || old.Get("search").String() != next.Get("search").String() {
			return
		}
		if mergeDuplicateNativeHash(target) {
			return
		}
		cancelPendingNavigation()
		intent := navigationID
		if mergeDeferNativeCommit(mergeNativeHash, target, func() { commitMergeNativeHash(target, intent) }) {
			return
		}
		commitMergeNativeHash(target, intent)
	})
	listen(win, "scroll", func(_ js.Value) {
		if !draftFrozen && doc.Get("visibilityState").String() != "hidden" && cancelNavigation == nil {
			saveScroll()
		}
	})
}

// Navigate starts an asynchronous same-origin page navigation and pushes a
// history entry. Relative URLs resolve against the document base. Network
// errors preserve the current view and appear in the status region.
func Navigate(target string) error { return navigateTo(target, false) }

// Replace navigates with the same behavior as Navigate but replaces the current
// history entry. Full-document fallbacks also use location.replace.
func Replace(target string) error { return navigateTo(target, true) }

// Prefetch starts bounded, asynchronous intent-style page loading. Only a valid
// response explicitly declared public by the server can enter the route cache.
// This is advisory: it does not report completion and may skip duplicate/full
// queues. Refresh invalidates cached data and cancels pending prefetches.
func Prefetch(target string) (err error) {
	if refreshFrozen() {
		return errRefreshFrozen
	}
	if moduleMode {
		return moduleCall("prefetch", target)
	}
	if offlineNativeNavigation() {
		return ErrOfflineNativeNavigation
	}
	if draftFrozen {
		return nil
	}
	defer func() {
		if v := recover(); v != nil {
			err = fmt.Errorf("prefetch setup failed: %v", v)
		}
	}()
	u, err := pageURL(target)
	if err != nil {
		return err
	}
	prefetch(u.Get("href").String())
	return nil
}

func pageURL(target string) (u js.Value, err error) {
	if refreshFrozen() {
		return js.Undefined(), errRefreshFrozen
	}
	if currentURL == "" {
		return js.Undefined(), fmt.Errorf("navigation runtime has not started")
	}
	defer func() {
		if v := recover(); v != nil {
			err = fmt.Errorf("invalid navigation URL: %v", v)
		}
	}()
	u = win.Get("URL").New(target, doc.Get("baseURI"))
	if !eligibleURL(u) {
		return u, fmt.Errorf("navigation requires a same-origin page URL without credentials")
	}
	return u, nil
}
func navigateTo(target string, replace bool) error {
	return NavigateWithOptions(target, NavigationOptions{Replace: replace})
}

// NavigateWithOptions starts Navigate/Replace with per-navigation transition
// controls. Invalid options fail before canceling or changing the current view.
func NavigateWithOptions(target string, options NavigationOptions) (err error) {
	if moduleMode {
		data, _ := json.Marshal(options)
		return moduleCall("navigate", target, string(data))
	}
	if draftFrozen {
		return fmt.Errorf("preview session verification is pending")
	}
	if err := options.Transition.validate(); err != nil {
		return err
	}
	replace := options.Replace
	defer func() {
		if v := recover(); v != nil {
			err = fmt.Errorf("navigation setup failed: %v", v)
		}
	}()
	if offlineNativeNavigation() {
		return offlineNavigate(target, replace)
	}
	u, err := pageURL(target)
	if err != nil {
		return err
	}
	old := win.Get("URL").New(currentURL)
	if u.Get("pathname").String() == old.Get("pathname").String() && u.Get("search").String() == old.Get("search").String() {
		if u.Get("href").String() != win.Get("location").Get("href").String() {
			queuedTarget := u.Get("href").String()
			if mergeDeferNativeCommit(mergeNativeRequested, queuedTarget, func() { _ = NavigateWithOptions(queuedTarget, options) }) {
				return nil
			}
			release, err := beginMergeNativePublication(navigationID)
			if err != nil {
				return err
			}
			defer release()
		}
		cancelPendingNavigation()
		href := u.Get("href").String()
		if href != win.Get("location").Get("href").String() {
			if replace {
				previous := currentURL
				active := doc.Get("activeElement")
				win.Get("history").Call("replaceState", navigationHistoryState(href, previous, true), "", href)
				currentURL = href
				setLocation(href)
				completeAccessibleNavigation(previous, href, active, doc.Get("title").String())
				restoreScroll(href, false, js.Undefined())
				saveScroll()
				notifyLocationObservers()
			} else {
				win.Get("location").Set("href", href)
			}
		}
		return nil
	}
	saveScroll()
	startNavigationOptions(u.Get("href").String(), false, js.Undefined(), options)
	return nil
}

// Refresh invalidates all prefetched data, cancels pending prefetches, and loads
// the current route again without adding history or resetting scroll position.
func Refresh() {
	if refreshFrozen() {
		return
	}
	if moduleMode {
		_ = moduleCall("refresh")
		return
	}
	if offlineNativeNavigation() {
		win.Get("location").Call("reload")
		return
	}
	if draftFrozen {
		draftRefreshPending = true
		return
	}
	invalidateCache()
	saveScroll()
	startNavigation(currentURL, true, currentState)
}

func eligibleURL(u js.Value) bool {
	if refreshFrozen() {
		return false
	}
	if u.Get("origin").String() != win.Get("location").Get("origin").String() || (u.Get("protocol").String() != "http:" && u.Get("protocol").String() != "https:") {
		return false
	}
	if u.Get("username").String() != "" || u.Get("password").String() != "" {
		return false
	}
	path, inside := deployment.Strip(u.Get("pathname").String())
	if !inside {
		return false
	}
	decoded, err := url.PathUnescape(path)
	if err != nil || routeManifest.IsEndpoint(path, decoded) {
		return false
	}
	return path != "/api" && !strings.HasPrefix(path, "/api/") && !strings.HasPrefix(path, "/assets/") && !strings.HasPrefix(path, "/_nextgo/")
}
func eligibleLink(target js.Value) (js.Value, js.Value, bool) {
	if offlineNativeNavigation() {
		return js.Undefined(), js.Undefined(), false
	}
	if target.Get("nodeType").Int() != 1 {
		target = target.Get("parentElement")
	}
	if target.IsNull() {
		return js.Undefined(), js.Undefined(), false
	}
	if hiddenRetainedNode(target) {
		return js.Null(), js.Null(), false
	}
	a := target.Call("closest", "a[href]")
	if a.IsNull() || a.Call("hasAttribute", "download").Bool() || a.Call("hasAttribute", "data-nextgo-reload").Bool() {
		return a, js.Undefined(), false
	}
	t := a.Call("getAttribute", "target")
	if !t.IsNull() && t.String() != "" && t.String() != "_self" {
		return a, js.Undefined(), false
	}
	u := win.Get("URL").New(a.Call("getAttribute", "href").String(), doc.Get("baseURI"))
	return a, u, eligibleURL(u)
}
func requestURL(target string) string {
	u := win.Get("URL").New(target)
	u.Set("hash", "")
	return u.Get("href").String()
}
func invalidateCache() {
	cancelRetainedSettlement()
	if moduleMode {
		_ = moduleCall("invalidate")
		return
	}
	invalidateResourceStores()
	cacheEpoch++
	clearRetainedViews(true)
	pageCache.clear()
	for _, pending := range prefetches {
		pending.cancel()
	}
	prefetches = map[string]*prefetchRequest{}
	if viewportPrefetch != nil {
		viewportPrefetch.requestScan()
	}
	syncLazyModules()
}
func prefetch(target string) { prefetchWithOrigin(target, false) }

func prefetchWithOrigin(target string, viewport bool) *prefetchRequest {
	if offlineNativeNavigation() {
		return nil
	}
	// Managed HttpOnly preview sessions require a fresh request on navigation;
	// speculative responses cannot be reused and only duplicate private work.
	if draftFrozen || routeManifest.Draft {
		return nil
	}
	key := requestURL(target)
	if _, ok := pageCache.get(key); ok {
		return nil
	}
	if pending := prefetches[key]; pending != nil {
		return pending
	}
	if len(prefetches) >= navigationCacheLimit {
		return nil
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	ctx = context.WithValue(ctx, prefetchContextKey{}, true)
	pending := &prefetchRequest{done: make(chan struct{}), cancel: cancel, viewport: viewport}
	prefetches[key] = pending
	epoch := cacheEpoch
	go func() {
		defer cancel()
		defer close(pending.done)
		defer func() {
			if prefetches[key] == pending {
				delete(prefetches, key)
			}
		}()
		data, ttl, _, err := fetchEnvelope(ctx, key)
		if err == nil && ctx.Err() == nil && epoch == cacheEpoch && ttl > 0 {
			pageCache.putUntil(key, data, data.cacheUntil)
		}
	}()
	return pending
}

func listen(target js.Value, event string, fn func(js.Value)) {
	f := js.FuncOf(func(_ js.Value, args []js.Value) any {
		defer func() {
			if v := recover(); v != nil {
				report(fmt.Sprintf("browser event error: %v", v))
			}
		}()
		fn(args[0])
		return nil
	})
	globalFunctions = append(globalFunctions, f)
	target.Call("addEventListener", event, f)
}
func saveScroll() {
	// Native history may already have moved while its metadata waits for an
	// owned callback. Never relabel that incoming entry with the old URL/state.
	if mergeNativeActualPending() && currentURL != mergeNativeTarget {
		return
	}
	history := win.Get("history")
	state := history.Get("state")
	if state.IsNull() || state.IsUndefined() || state.Type() != js.TypeObject {
		state = win.Get("Object").New()
	}
	if _, ok := trackedHistory(state, currentURL); !ok {
		tracking := newHistoryState(currentURL, "")
		if key := tracking.Get("nextgoEntry"); key.Type() == js.TypeString {
			state.Set("nextgoEntry", key)
		}
	}
	state.Set("nextgoX", win.Get("scrollX"))
	state.Set("nextgoY", win.Get("scrollY"))
	if slots, err := currentSlotState(); err == nil {
		state.Set("nextgoSlots", slots)
	}
	history.Call("replaceState", state, "", win.Get("location").Get("href"))
	currentState = state
	bindRetainedHistory()
	saveMergeHistory(state, currentURL)
}

// Same-document history changes also supersede an outstanding page request.
// Invalidating its ID prevents a response already in flight from committing.
func cancelPendingNavigation() {
	cancelRetainedSettlement()
	cancelActionView()
	skipViewTransition()
	stopNavigationStream()
	clearStreamRetry()
	linkID := linkNavigationID
	if cancelNavigation == nil {
		finishLinkNavigation(linkID)
		return
	}
	cancelNavigation()
	cancelNavigation = nil
	navigationID++
	doc.Call("getElementById", "nextgo-app").Call("removeAttribute", "aria-busy")
	doc.Call("getElementById", "nextgo-status").Set("hidden", true)
	finishLinkNavigation(linkID)
}

func startNavigation(target string, pop bool, state js.Value) {
	startNavigationMode(target, pop, state, false)
}

func startNavigationMode(target string, pop bool, state js.Value, replace bool) {
	startNavigationOptions(target, pop, state, NavigationOptions{Replace: replace})
}

func startNavigationOptions(target string, pop bool, state js.Value, options NavigationOptions) {
	startLinkNavigation(target, pop, state, options, js.Undefined())
}

func startLinkNavigation(target string, pop bool, state js.Value, options NavigationOptions, source js.Value) {
	cancelRetainedSettlement()
	if draftFrozen || refreshFrozen() {
		return
	}
	if offlineNativeNavigation() {
		if err := offlineNavigate(target, true); err != nil {
			report(err.Error())
		}
		return
	}
	cancelActionView()
	skipViewTransition()
	stopNavigationStream()
	clearStreamRetry()
	if cancelNavigation != nil {
		cancelNavigation()
	}
	navigationID++
	id := navigationID
	ctx, cancel := context.WithCancel(context.Background())
	if pop && !state.IsNull() && !state.IsUndefined() {
		if saved := state.Get("nextgoSlots"); saved.Type() == js.TypeString {
			ctx = context.WithValue(ctx, slotHistoryKey{}, saved.String())
		}
	}
	cancelNavigation = cancel
	status := doc.Call("getElementById", "nextgo-status")
	status.Set("hidden", false)
	status.Set("textContent", "Loading…")
	doc.Call("getElementById", "nextgo-app").Call("setAttribute", "aria-busy", "true")
	beginLinkNavigation(id, source)
	if id != navigationID || ctx.Err() != nil {
		cancel()
		return
	}
	if !staticExport() {
		go loadNavigationFallback(ctx, target, id)
	}
	go func() {
		defer cancel()
		err := navigate(ctx, target, pop, state, id, options)
		if id != navigationID {
			return
		}
		cancelNavigation = nil
		finishLinkNavigation(id)
		if id != navigationID {
			return
		}
		doc.Call("getElementById", "nextgo-app").Call("removeAttribute", "aria-busy")
		if err != nil {
			// A failed pop must not leave the address bar describing the old view.
			if pop {
				win.Get("history").Call("replaceState", currentState, "", currentURL)
			}
			if retainedCurrent != nil && retainedCurrent.activityErr != nil {
				report(retainedRecoveryMessage)
			} else if _, ok := err.(*committedStreamError); ok {
				showStreamRetry()
			} else {
				report("Navigation failed. Try again or reload. " + err.Error())
			}
		}
	}()
}

type headElement struct {
	Tag   string            `json:"tag"`
	Attrs map[string]string `json:"attrs"`
	Text  string            `json:"text"`
}

type envelope struct {
	metadata      *metadataHead
	Document      navigation.Document   `json:"document"`
	Version       int                   `json:"version"`
	Configuration string                `json:"configuration"`
	Head          []headElement         `json:"head"`
	URL           string                `json:"url"`
	Build         string                `json:"build"`
	HTML          string                `json:"html"`
	Retention     *navigation.Retention `json:"retention,omitempty"`
	Merge         json.RawMessage       `json:"merge,omitempty"`
	merge         *navigation.MergeDescriptor
	Title         string            `json:"title"`
	Description   string            `json:"description"`
	Meta          map[string]string `json:"meta"`
	Status        int               `json:"status"`
	Shell         string            `json:"shell,omitempty"`
	ShellRef      bool              `json:"shellRef,omitempty"`
	shellEpoch    uint64
	cacheUntil    time.Time
}

// Account for payload strings plus conservative per-entry/map overhead. This
// bounds retained response data; browser DOM and the Go heap are separate costs.
func envelopeBytes(data envelope) int64 {
	size := int64(512 + len(data.URL) + len(data.Build) + len(data.Configuration) + len(data.Shell) + len(data.Document.Language) + len(data.Document.Direction) + len(data.Document.Viewport) + len(data.HTML) + len(data.Title) + len(data.Description))
	for key, value := range data.Meta {
		size += int64(96 + len(key) + len(value))
	}
	for _, element := range data.Head {
		size += int64(256 + len(element.Tag) + len(element.Text))
		for key, value := range element.Attrs {
			size += int64(96 + len(key) + len(value))
		}
	}
	return size
}

func navigate(ctx context.Context, target string, pop bool, state js.Value, id uint64, options NavigationOptions) (err error) {
	replace := options.Replace
	defer func() {
		if v := recover(); v != nil {
			err = fmt.Errorf("browser navigation error: %v", v)
		}
	}()
	key := requestURL(target)
	data, cached := pageCache.get(key)
	retaining := retainedRoutingEnabled() || mergeRuntimeEnabled()
	if retaining {
		cached = false
		retainedFreshID, retainedFreshEpoch = id, cacheEpoch
	}
	if !cached || data.Shell != "" {
		waitLinkNavigation(id)
		if id != navigationID || ctx.Err() != nil {
			return context.Canceled
		}
	}
	var hint *shellResumeHint
	if !cached && !retaining {
		if pending := prefetches[key]; pending != nil && pending.viewport {
			pending.cancel()
			delete(prefetches, key)
		}
		if pending := prefetches[key]; pending != nil {
			select {
			case <-pending.done:
			case <-ctx.Done():
				return ctx.Err()
			}
			data, cached = pageCache.get(key)
		}
	}
	if cached && data.Shell != "" {
		hint = &shellResumeHint{page: data, epoch: cacheEpoch}
		cached = false
	}
	if !cached {
		epoch := cacheEpoch
		var fallback bool
		var streamed bool
		data, streamed, fallback, err = fetchActiveNavigation(ctx, key, target, pop, state, id, options, hint)
		if streamed {
			return err
		}
		if err != nil {
			return err
		}
		if id != navigationID || ctx.Err() != nil {
			return ctx.Err()
		}
		if fallback {
			reloadPage(target, replace)
			return nil
		}
		if epoch == cacheEpoch && data.Shell == "" && !retaining {
			pageCache.putUntil(key, data, data.cacheUntil)
		}
	}
	if id != navigationID || ctx.Err() != nil {
		return ctx.Err()
	}
	return commitNavigation(ctx, target, pop, state, id, options, data)
}

func commitNavigation(ctx context.Context, target string, pop bool, state js.Value, id uint64, options NavigationOptions, data envelope) error {
	replace := options.Replace
	// The validated envelope records the final application URL so history
	// matches rendered content after a same-origin redirect.
	canonical := win.Get("URL").New(data.URL, target)
	if !eligibleURL(canonical) {
		reloadPage(target, replace)
		return nil
	}
	requestedURL := win.Get("URL").New(target)
	if staticExport() {
		canonical.Set("search", requestedURL.Get("search"))
	}
	canonical.Set("hash", requestedURL.Get("hash"))
	target = canonical.Get("href").String()
	parsed := win.Get("DOMParser").New().Call("parseFromString", data.HTML, "text/html")
	if _, err := scriptMarkers(parsed.Get("body")); err != nil {
		return err
	}
	root := doc.Call("getElementById", "nextgo-app")
	transition := options.Transition
	transition.Disabled = transition.Disabled || doc.Get("body").Call("getAttribute", "data-nextgo-transitions").String() != "true"
	if len(transition.Types) == 0 {
		kind := "navigation"
		if pop {
			kind = "history"
		}
		if target == currentURL {
			kind = "refresh"
		}
		transition.Types = []string{kind}
	}
	return Transition(ctx, transition, func() error {
		if guard, ok := ctx.Value(actionViewCommitKey{}).(func() error); ok {
			if err := guard(); err != nil {
				return err
			}
		}
		if id != navigationID || ctx.Err() != nil {
			return context.Canceled
		}
		if data.ShellRef && data.shellEpoch != cacheEpoch {
			return fmt.Errorf("cached shell was invalidated before navigation commit")
		}
		if err := commitWithContext(ctx, parsed, root, data, target, pop, state, replace); err != nil {
			return err
		}
		finishLinkNavigation(id)
		return nil
	})
}

func staticExport() bool {
	return doc.Get("body").Call("getAttribute", "data-nextgo-export").String() == "true"
}

// Loading UI is a separate, uncached request so the current page remains useful
// while its server loader runs. A late fallback can never overwrite a new view.
func loadNavigationFallback(ctx context.Context, target string, id uint64) {
	defer func() { _ = recover() }()
	u, err := url.Parse(requestURL(target))
	if err != nil {
		return
	}
	loadingURL := win.Get("URL").New(MustURL("/_nextgo/loading"), win.Get("location").Get("href"))
	loadingURL.Set("search", "?path="+url.QueryEscape(u.RequestURI()))
	resp, err := Fetch(ctx, loadingURL.Get("href").String(), FetchOptions{MaxBytes: 256 << 10, Cache: "no-store"})
	if err != nil {
		return
	}
	if !sameOriginResponse(resp.URL) || resp.Header.Get(navigation.DraftHeader) != documentDraft() || resp.Header.Get(navigation.ConfigurationHeader) != documentConfiguration() || resp.StatusCode != 200 || !strings.Contains(resp.Header.Get("Content-Type"), "text/html") {
		return
	}
	body := resp.Body
	if len(body) == 0 || ctx.Err() != nil || id != navigationID || cancelNavigation == nil || streamShellCommittedID == id {
		return
	}
	parsed := win.Get("DOMParser").New().Call("parseFromString", string(body), "text/html")
	scripts := parsed.Call("querySelectorAll", "script")
	for i := 0; i < scripts.Length(); i++ {
		scripts.Index(i).Call("remove")
	}
	fragment := doc.Call("createDocumentFragment")
	for parsed.Get("body").Get("firstChild").Truthy() {
		fragment.Call("appendChild", parsed.Get("body").Get("firstChild"))
	}
	status := doc.Call("getElementById", "nextgo-status")
	status.Call("replaceChildren", fragment)
	status.Set("hidden", false)
}

// Navigation HTML is trusted only when delivered by the application's own
// origin. An envelope's claimed URL/build cannot establish its provenance.
func sameOriginResponse(target string) bool {
	if target == "" {
		return false
	}
	u := win.Get("URL").New(target)
	_, inside := deployment.Strip(u.Get("pathname").String())
	return inside && sameResponseOrigin(target)
}

func documentConfiguration() string {
	return doc.Get("body").Call("getAttribute", "data-nextgo-configuration").String()
}
func sameResponseOrigin(target string) bool {
	if target == "" {
		return false
	}
	u := win.Get("URL").New(target)
	return u.Get("origin").String() == win.Get("location").Get("origin").String() && (u.Get("protocol").String() == "http:" || u.Get("protocol").String() == "https:") && u.Get("username").String() == "" && u.Get("password").String() == ""
}

func fetchEnvelope(ctx context.Context, target string) (data envelope, ttl time.Duration, fallback bool, err error) {
	started := time.Now()
	defer func() {
		if v := recover(); v != nil {
			err = fmt.Errorf("prefetch error: %v", v)
		}
	}()
	fetchTarget := target
	if staticExport() {
		if fetch := registeredStaticShellFetch; fetch != nil {
			if shell, lifetime, handled, reload, failure := fetch(ctx, target); handled {
				return shell, lifetime, reload, failure
			}
		}
		u := win.Get("URL").New(target)
		local, inside := deployment.Strip(u.Get("pathname").String())
		if !inside {
			return data, 0, false, fmt.Errorf("static navigation is outside the application base")
		}
		path := strings.TrimRight(local, "/")
		if path == "" {
			path = "/index"
		}
		u.Set("pathname", MustURL("/_nextgo/data"+path+".json"))
		u.Set("search", "")
		fetchTarget = u.Get("href").String()
	}
	headers := map[string]string{"Cache-Control": "no-cache"}
	if !staticExport() {
		headers["X-Nextgo-Navigate"] = "1"
		if intent, _ := ctx.Value(prefetchContextKey{}).(bool); intent {
			headers[navigation.PurposeHeader] = "prefetch"
			headers[navigation.ShellProtocolHeader] = fmt.Sprint(navigation.ShellVersion)
		}
		if routeManifest.Parallel != "" {
			selected, selectionErr := slotRequestState(ctx)
			if selectionErr != nil {
				return data, 0, false, selectionErr
			}
			headers[navigation.SlotsHeader] = selected
		}
	}
	resp, err := Fetch(ctx, fetchTarget, FetchOptions{Headers: headers, Cache: "no-store"})
	if err != nil {
		return data, 0, false, err
	}
	intent, _ := ctx.Value(prefetchContextKey{}).(bool)
	return decodeEnvelopeResponseWithShell(resp, resp.Body, target, started, false, shellDecodeOptions{prefetch: intent})
}

func decodeEnvelopeResponse(resp Response, body []byte, target string, started time.Time, stream bool) (data envelope, ttl time.Duration, fallback bool, err error) {
	return decodeEnvelopeResponseWithShell(resp, body, target, started, stream, shellDecodeOptions{})
}
func decodeEnvelopeResponseWithShell(resp Response, body []byte, target string, started time.Time, stream bool, shellOptions shellDecodeOptions) (data envelope, ttl time.Duration, fallback bool, err error) {
	if !sameOriginResponse(resp.URL) {
		if sameResponseOrigin(resp.URL) {
			return data, 0, true, nil
		}
		return data, 0, false, fmt.Errorf("navigation redirected outside the application origin")
	}
	if !staticExport() && resp.Header.Get(navigation.DraftHeader) != documentDraft() {
		if !shellOptions.prefetch {
			revokeMergeDocument()
		}
		invalidateCache()
		return data, 0, true, nil
	}
	if !staticExport() && resp.Header.Get(navigation.ProtocolHeader) != fmt.Sprint(navigation.ProtocolVersion) {
		if !shellOptions.prefetch {
			revokeMergeDocument()
		}
		return data, 0, true, nil
	}
	if !stream && !strings.Contains(resp.Header.Get("Content-Type"), "application/json") {
		return data, 0, true, nil
	}
	if err = json.Unmarshal(body, &data); err != nil {
		return data, 0, false, err
	}
	if data.Version != navigation.ProtocolVersion {
		if !shellOptions.prefetch {
			revokeMergeDocument()
		}
		invalidateCache()
		return data, 0, true, nil
	}
	if data.Configuration != documentConfiguration() || !navigation.ValidConfigurationID(data.Configuration) || (!staticExport() && resp.Header.Get(navigation.ConfigurationHeader) != data.Configuration) {
		if !shellOptions.prefetch {
			revokeMergeDocument()
		}
		invalidateCache()
		return data, 0, true, nil
	}
	if data.Build != doc.Get("body").Call("getAttribute", "data-nextgo-build").String() {
		if !shellOptions.prefetch {
			revokeMergeDocument()
		}
		invalidateCache()
		return data, 0, true, nil
	}
	if err := data.Document.Validate(); err != nil {
		return data, 0, false, fmt.Errorf("invalid navigation document: %w", err)
	}
	for name := range data.Meta {
		lower := strings.ToLower(name)
		if lower == "viewport" || lower == "description" {
			return data, 0, false, fmt.Errorf("navigation metadata overrides framework-owned %s", lower)
		}
	}
	if data.Status != resp.StatusCode || data.Status < 200 || data.Status >= 600 || data.URL == "" {
		return data, 0, false, fmt.Errorf("invalid navigation response")
	}
	canonical := win.Get("URL").New(data.URL, target)
	if !eligibleURL(canonical) {
		return data, 0, true, nil
	}
	if !shellOptions.prefetch && (resp.StatusCode == 401 || resp.StatusCode == 403) && data.Status == resp.StatusCode && data.Version == navigation.ProtocolVersion && data.Configuration == documentConfiguration() && resp.Header.Get(navigation.ConfigurationHeader) == data.Configuration && data.Build == doc.Get("body").Call("getAttribute", "data-nextgo-build").String() && data.URL != "" {
		deniedURL := win.Get("URL").New(data.URL, target)
		if eligibleURL(deniedURL) {
			revokeMergeDocument()
		}
	}
	if err = validateMergeEnvelope(body, &data); err != nil {
		return data, 0, false, err
	}
	// A validated authority partition change fences old private UI immediately;
	// malformed optional metadata/retention cannot postpone that revocation.
	if !shellOptions.prefetch {
		mergeIncomingAuthority(data.merge, nil, data.Status)
	}
	if err = validateRetainedEnvelope(body, &data); err != nil {
		return data, 0, false, err
	}
	if data.merge != nil && data.Retention != nil && data.merge.Partition != data.Retention.Partition {
		return data, 0, false, fmt.Errorf("conflicting route authority partitions")
	}
	if !shellOptions.prefetch {
		mergeIncomingAuthority(data.merge, data.Retention, data.Status)
	}
	if err = validateShellFields(body, data); err != nil {
		return data, 0, false, err
	}
	// Validate fresh server metadata before borrowing any cached shell bytes.
	// This fragment remains detached; commit creates its own managed head.
	if _, headErr := buildHead(data.Head); headErr != nil {
		return data, 0, false, headErr
	}
	if data.Shell != "" || data.ShellRef {
		if staticExport() || len(resp.Header.Values(navigation.ShellProtocolHeader)) != 1 || resp.Header.Get(navigation.ShellProtocolHeader) != fmt.Sprint(navigation.ShellVersion) || data.Status != 200 || (!stream && !shellOptions.prefetch) || (!data.ShellRef && data.HTML == "") || (!stream && data.ShellRef) {
			return data, 0, false, fmt.Errorf("invalid public shell envelope")
		}
		if data.ShellRef {
			if err = applyShellReference(&data, shellOptions.hint, target); err != nil {
				return data, 0, false, err
			}
		}
	}
	parsedSlots := win.Get("DOMParser").New().Call("parseFromString", data.HTML, "text/html")
	if _, scriptErr := scriptMarkers(parsedSlots.Get("body")); scriptErr != nil {
		return data, 0, false, scriptErr
	}
	if _, slotErr := slotNodes(parsedSlots); slotErr != nil {
		return data, 0, false, slotErr
	}
	if _, err := selectionNodes(parsedSlots); err != nil {
		return data, 0, false, err
	}
	// Only a fully validated response from this runtime can report transient
	// admission failure. Preserve the current view rather than committing its
	// generic busy document; ordinary 503 pages keep their existing semantics.
	if data.Status == 503 && resp.Header.Get(navigation.StreamAdmissionHeader) == "busy" {
		return data, 0, false, fmt.Errorf("the server is busy; please try again shortly")
	}
	if data.Status == 200 {
		lifetime := cacheLifetime(resp.Header)
		if data.Shell != "" {
			lifetime = 0
			if shellOptions.prefetch && !data.ShellRef {
				lifetime = shellCacheLifetime(resp.Header, data.Shell)
			}
		}
		data.cacheUntil = started.Add(lifetime)
		ttl = max(0, time.Until(data.cacheUntil))
	}
	return data, ttl, false, nil
}

func reloadPage(target string, replace bool) {
	method := "assign"
	if replace {
		method = "replace"
	}
	win.Get("location").Call(method, target)
}

func commit(parsed, root js.Value, data envelope, target string, pop bool, state js.Value, replace bool) error {
	return commitWithContext(context.Background(), parsed, root, data, target, pop, state, replace)
}
func commitWithContext(ctx context.Context, parsed, root js.Value, data envelope, target string, pop bool, state js.Value, replace bool) error {
	intent, epoch, privacy := navigationID, cacheEpoch, mergePrivacy
	release, err := acquireMergeCommit(ctx, intent, data.merge != nil)
	if err != nil {
		return err
	}
	defer release()
	if handled, err := commitStationaryMerge(ctx, parsed, root, data, target, pop, state, replace); handled || err != nil {
		return err
	}
	// Aborting a prepared cohort can run pending useful old-frame effects. A
	// navigation they start must not relabel this old response as that new intent.
	if ctx.Err() != nil || intent != navigationID || epoch != cacheEpoch || privacy != mergePrivacy {
		return context.Canceled
	}
	return commitLegacy(ctx, parsed, root, data, target, pop, state, replace)
}
func commitLegacy(ctx context.Context, parsed, root js.Value, data envelope, target string, pop bool, state js.Value, replace bool) error {
	commitID := navigationID
	commitEpoch := cacheEpoch
	mergeManaged := data.merge != nil || mergeCurrent != nil || propsReservations > 0
	mergeStarted := false
	mergeEpoch := mergePrivacy
	defer func() {
		if mergeManaged {
			mergeInstallActive = false
		}
	}()
	current := func() bool {
		if mergeManaged && mergeStarted {
			return mergeEpoch == mergePrivacy
		}
		return commitID == navigationID && commitEpoch == cacheEpoch
	}
	snapshot, selectionErr := selectionNodes(parsed)
	if selectionErr != nil {
		return selectionErr
	}
	previous := currentURL
	active := doc.Get("activeElement")
	head, err := buildHead(data.Head)
	if err != nil {
		return err
	}
	if data.metadata != nil {
		if err := data.metadata.capture(head); err != nil {
			return err
		}
		data.metadata.committedURL = target
	}
	navigationOpener = active
	modalReturnFocus = js.Undefined()
	defer func() { navigationOpener = js.Undefined(); modalReturnFocus = js.Undefined() }()
	stopStreaming()
	if !current() {
		return context.Canceled
	}
	if err := mergeAwaitCallbackReads(ctx, commitID); err != nil {
		return err
	}
	// The two managers never co-own one DOM root. Explicit merge documents and
	// transitions out of them use ordinary fresh replacement on the cold path.
	retentionData := data
	if mergeCurrent != nil || data.merge != nil || len(propsBindings) > 0 {
		retentionData.Retention = nil
	}
	handled, activate, retentionErr := commitRetainedDOM(root, parsed.Get("body"), retentionData, target, pop, state)
	if retentionErr != nil {
		return retentionErr
	}
	if activate != nil {
		defer activate.Retire()
	}
	if !current() {
		return context.Canceled
	}
	if !handled {
		old, incoming := root, parsed.Get("body")
		// Denials discard retained layout DOM and effects too. Unsupported
		// retained-route structures have already discarded every hidden owner.
		_, currentSelectionErr := selectionNodes(root)
		retain := data.Status != 401 && data.Status != 403 && currentSelectionErr == nil
		if len(propsBindings) > 0 {
			retain = false
		}
		if retain {
			old, incoming = navigationRegion(root, parsed.Get("body"))
		}
		if retain {
			if err := reconcileSlots(root, parsed, old, incoming); err != nil {
				return err
			}
		}
		if !current() {
			return context.Canceled
		}
		disposeWithin(old)
		if !current() {
			return context.Canceled
		}
		fragment := doc.Call("createDocumentFragment")
		for incoming.Get("firstChild").Truthy() {
			fragment.Call("appendChild", incoming.Get("firstChild"))
		}
		if err := mergeAwaitCallbackReads(ctx, commitID); err != nil {
			return err
		}
		if !current() {
			return context.Canceled
		}
		if mergeManaged {
			mergeStarted = true
			mergeInstallActive = true
		}
		old.Call("replaceChildren", fragment)
	}
	if !current() {
		return context.Canceled
	}
	doc.Get("documentElement").Call("setAttribute", "lang", data.Document.Language)
	doc.Get("documentElement").Call("setAttribute", "dir", data.Document.Direction)
	doc.Call("querySelector", `meta[name="viewport"]`).Call("setAttribute", "content", data.Document.Viewport)
	doc.Set("title", data.Title)
	doc.Call("querySelector", `meta[name="description"]`).Call("setAttribute", "content", data.Description)
	meta := doc.Call("querySelectorAll", "meta[data-nextgo-meta]")
	for i := 0; i < meta.Length(); i++ {
		meta.Index(i).Call("remove")
	}
	richMeta := doc.Call("querySelectorAll", "[data-nextgo-head]")
	for i := 0; i < richMeta.Length(); i++ {
		richMeta.Index(i).Call("remove")
	}
	doc.Get("head").Call("appendChild", head)
	for name, value := range data.Meta {
		m := doc.Call("createElement", "meta")
		m.Call("setAttribute", "data-nextgo-meta", "")
		m.Call("setAttribute", "name", name)
		m.Call("setAttribute", "content", value)
		doc.Get("head").Call("appendChild", m)
	}
	if !pop {
		method := "pushState"
		if replace {
			method = "replaceState"
		}
		win.Get("history").Call(method, navigationHistoryState(target, previous, replace), "", target)
	} else if win.Get("location").Get("href").String() != target {
		// Redirects encountered during back/forward must also canonicalize the
		// traversed entry; otherwise the address bar and committed view disagree.
		prior, _ := trackedHistory(state, win.Get("location").Get("href").String())
		win.Get("history").Call("replaceState", newHistoryState(target, prior.Previous), "", target)
	}
	currentURL = target
	setLocation(target)
	if err := installColdMergeFrame(data, root, target, mergeEpoch); err != nil {
		return err
	}
	installSelections(snapshot)
	if activate != nil {
		activate.Activate()
	}
	if (commitID != navigationID && !mergeManaged) || (mergeManaged && mergeEpoch != mergePrivacy) {
		return nil
	}
	syncNavigationLinks(target)
	status := doc.Call("getElementById", "nextgo-status")
	if (!mergeManaged || commitID == navigationID) && !applicationHydrationError() && (retainedCurrent == nil || retainedCurrent.activityErr == nil) {
		status.Set("hidden", true)
	}
	if !handled {
		if mergeManaged {
			application := mergeApplicationToken()
			hydrateManaged(mergeHydrationDiagnostic, func() bool { return mergeApplicationCurrent(application) && mergeEpoch == mergePrivacy })
		} else {
			hydrate()
		}
	}
	if mergeManaged {
		captureMergeBindings()
	}
	completeAccessibleNavigation(previous, target, active, data.Title)
	settlement := restoreRetainedFocus(pop)
	// Finish retained focus, selection and nested positions before the single
	// explicit window restore.
	restoreScroll(target, pop, state)
	saveScroll()
	notifyLocationObservers()
	// Owned presentation work neither yields the atomic commit nor keeps its
	// normally canceled navigation context alive. It never writes the window.
	scheduleRetainedSettlement(settlement)
	return nil
}

func syncNavigationLinks(target string) {
	active := win.Get("URL").New(target)
	links := doc.Call("querySelectorAll", "nav a[href]")
	for i := 0; i < links.Length(); i++ {
		link := links.Index(i)
		if hiddenRetainedNode(link) {
			continue
		}
		href := link.Call("getAttribute", "href").String()
		if strings.HasPrefix(href, "#") {
			continue
		}
		u := win.Get("URL").New(href, doc.Get("baseURI"))
		if u.Get("origin").String() == active.Get("origin").String() && u.Get("pathname").String() == active.Get("pathname").String() && (u.Get("search").String() == "" || u.Get("search").String() == active.Get("search").String()) {
			link.Call("setAttribute", "aria-current", "page")
		} else if link.Call("getAttribute", "aria-current").String() == "page" {
			link.Call("removeAttribute", "aria-current")
		}
	}
}

func disposeWithin(region js.Value) {
	retained := mounted[:0]
	for _, scope := range mounted {
		if region.Call("contains", scope.Root.Value).Bool() {
			scope.dispose()
		} else {
			retained = append(retained, scope)
		}
	}
	mounted = retained
}

// A shared layout's wrapper and its islands survive navigation. Only the
// deepest common outlet is replaced; diverging child layouts unmount normally.
func navigationRegion(old, incoming js.Value) (js.Value, js.Value) {
	if doc.Get("body").Call("getAttribute", "data-nextgo-preserve-layouts").String() != "true" {
		return old, incoming
	}
	for {
		x, y := old.Get("firstElementChild"), incoming.Get("firstElementChild")
		if x.IsNull() || y.IsNull() || x.Get("tagName").String() != "NEXTGO-LAYOUT" || y.Get("tagName").String() != "NEXTGO-LAYOUT" {
			break
		}
		id := x.Call("getAttribute", "data-nextgo-layout")
		if !sameOwnedSlots(x, y) {
			break
		}
		if id.IsNull() || id.String() != y.Call("getAttribute", "data-nextgo-layout").String() {
			break
		}
		xout, yout := layoutOutlet(x, id.String()), layoutOutlet(y, id.String())
		if xout.IsNull() || yout.IsNull() {
			break
		}
		old, incoming = xout, yout
	}
	return old, incoming
}
func layoutOutlet(layout js.Value, id string) js.Value {
	outlets := layout.Call("querySelectorAll", "nextgo-outlet[data-nextgo-outlet]")
	for i := 0; i < outlets.Length(); i++ {
		outlet := outlets.Index(i)
		if outlet.Call("getAttribute", "data-nextgo-outlet").String() == id && outlet.Call("closest", "nextgo-layout[data-nextgo-layout]").Equal(layout) {
			return outlet
		}
	}
	return js.Null()
}

// Validate structured head data before mutating the active document. The
// navigation protocol never accepts raw head HTML or event-bearing attributes.
func buildHead(elements []headElement) (js.Value, error) {
	fragment := doc.Call("createDocumentFragment")
	jsonCount, jsonBytes := 0, 0
	for _, element := range elements {
		if element.Tag == "script" {
			jsonCount++
			jsonBytes += len(element.Text)
			if jsonCount > 16 || jsonBytes > 512<<10 || len(element.Attrs) != 1 || element.Attrs["type"] != "application/ld+json" {
				return fragment, fmt.Errorf("invalid navigation structured-data node")
			}
			if err := jsonld.Validate([]byte(element.Text)); err != nil {
				return fragment, err
			}
			node := doc.Call("createElement", "script")
			node.Call("setAttribute", "type", "application/ld+json")
			node.Call("setAttribute", "data-nextgo-head", "")
			if nonce := scriptNonce(); nonce != "" {
				node.Set("nonce", nonce)
			}
			node.Set("textContent", element.Text)
			fragment.Call("appendChild", node)
			continue
		}
		if element.Text != "" {
			return fragment, fmt.Errorf("unexpected navigation head text")
		}
		if element.Tag != "meta" && element.Tag != "link" {
			return fragment, fmt.Errorf("invalid navigation head tag %q", element.Tag)
		}
		if element.Tag == "meta" && (strings.EqualFold(element.Attrs["name"], "viewport") || strings.EqualFold(element.Attrs["name"], "description")) {
			return fragment, fmt.Errorf("navigation head overrides framework-owned metadata")
		}
		for name := range element.Attrs {
			valid := element.Tag == "meta" && (name == "name" || name == "property" || name == "content" || name == "media" && element.Attrs["name"] == "theme-color")
			valid = valid || element.Tag == "link" && (name == "rel" || name == "href" || name == "hreflang" || name == "type" || name == "sizes" || name == "media" || name == "as" || name == "crossorigin" || name == "integrity")
			if !valid {
				return fragment, fmt.Errorf("invalid navigation head attribute %q", name)
			}
		}
		if element.Tag == "link" {
			switch element.Attrs["rel"] {
			case "canonical", "alternate", "icon", "apple-touch-icon", "mask-icon", "manifest", "stylesheet", "preload":
			default:
				return fragment, fmt.Errorf("invalid navigation metadata link relation")
			}
			rel := element.Attrs["rel"]
			if rel == "manifest" {
				href := element.Attrs["href"]
				parsed, err := url.Parse(href)
				if err != nil || href == "" || len(href) > 32<<10 || strings.HasPrefix(href, "//") || strings.Contains(href, "\\") || strings.IndexFunc(href, func(r rune) bool { return unicode.IsSpace(r) || unicode.IsControl(r) }) >= 0 {
					return fragment, fmt.Errorf("invalid navigation manifest URL")
				}
				rootRelative := strings.HasPrefix(href, "/") && parsed.Scheme == "" && parsed.Host == ""
				absolute := (parsed.Scheme == "http" || parsed.Scheme == "https") && parsed.Hostname() != ""
				if parsed.User != nil || parsed.Opaque != "" || !(rootRelative || absolute) {
					return fragment, fmt.Errorf("invalid navigation manifest URL")
				}
				for name, value := range element.Attrs {
					if name != "rel" && name != "href" && !(name == "type" && value == "application/manifest+json") {
						return fragment, fmt.Errorf("invalid navigation manifest attribute")
					}
				}
			}
			u := win.Get("URL").New(element.Attrs["href"], doc.Get("baseURI"))
			if rel == "stylesheet" || rel == "preload" {
				href := element.Attrs["href"]
				_, inside := deployment.Strip(u.Get("pathname").String())
				local := strings.HasPrefix(href, "/") && !strings.HasPrefix(href, "//") && inside && u.Get("origin").String() == win.Get("location").Get("origin").String()
				if !local && !deployment.IsAssetURL(href) {
					return fragment, fmt.Errorf("invalid navigation resource URL")
				}
				if rel == "preload" && (element.Attrs["as"] != "font" || (element.Attrs["type"] != "font/woff" && element.Attrs["type"] != "font/woff2") || element.Attrs["crossorigin"] != "anonymous") {
					return fragment, fmt.Errorf("invalid navigation font preload")
				}
				if mode, ok := element.Attrs["crossorigin"]; ok && mode != "anonymous" {
					return fragment, fmt.Errorf("invalid navigation resource CORS mode")
				}
			}
			if u.Get("protocol").String() != "http:" && u.Get("protocol").String() != "https:" {
				return fragment, fmt.Errorf("invalid navigation metadata URL")
			}
		}
		if element.Tag == "meta" && element.Attrs["name"] == "description" {
			continue
		}
		node := doc.Call("createElement", element.Tag)
		node.Call("setAttribute", "data-nextgo-head", "")
		for name, value := range element.Attrs {
			node.Call("setAttribute", name, value)
		}
		fragment.Call("appendChild", node)
	}
	return fragment, nil
}

func restoreScroll(target string, pop bool, state js.Value) {
	x, y := 0, 0
	hasPosition := false
	if pop && !state.IsNull() && !state.IsUndefined() {
		if v := state.Get("nextgoX"); v.Type() == js.TypeNumber {
			x = v.Int()
		}
		if v := state.Get("nextgoY"); v.Type() == js.TypeNumber {
			y = v.Int()
			hasPosition = true
		}
	}
	win.Call("scrollTo", x, y)
	if !hasPosition {
		u := win.Get("URL").New(target)
		hash := u.Get("hash").String()
		if len(hash) > 1 {
			decoded, err := url.PathUnescape(hash[1:])
			if err != nil {
				decoded = hash[1:]
			}
			el := doc.Call("getElementById", decoded)
			if !el.IsNull() {
				el.Call("scrollIntoView")
			}
		}
	}
}

func documentDraft() string {
	v := doc.Get("body").Call("getAttribute", "data-nextgo-draft")
	if v.IsNull() {
		return ""
	}
	return v.String()
}
