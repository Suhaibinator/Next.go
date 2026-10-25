//go:build js && wasm

package client

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"net/url"
	"reflect"
	"sort"
	"syscall/js"

	"github.com/Suhaibinator/Next.go/internal/ascii"
	"github.com/Suhaibinator/Next.go/internal/devrefresh"
)

// RegisterDevRefresh is emitted only by a compiler-proved development build.
// It does not change the production component ABI or navigation protocol.
func RegisterDevRefresh(compatibility string) {
	if !devRefreshBuild {
		return
	}
	if moduleMode || devRefresh != nil || !ascii.LowerHex(compatibility, 64) {
		panic("invalid development refresh registration")
	}
	devRefresh = &refreshCoordinator{compatibility: compatibility}
}

type refreshCoordinator struct {
	compatibility        string
	frozen, disabled     bool
	initialized, running bool
	current, seen        devrefresh.Manifest
	pending              *devrefresh.Manifest
	swaps                int
	composing            bool
	cancel               context.CancelFunc
	startupReady         func()
	startupLoading       bool
	startupScope         *Scope
	startupMessage       js.Value
	startupRetry         js.Value
}

var devRefresh *refreshCoordinator

// The module lifecycle installs its private fence; it never owns the router.
var refreshModuleFence func() bool
var refreshModuleMounting bool

func devRefreshEnabled() bool { return devRefreshBuild && devRefresh != nil && !devRefresh.disabled }
func refreshFrozen() (frozen bool) {
	if !devRefreshBuild {
		return false
	}
	defer func() {
		if recover() != nil {
			frozen = true
		}
	}()
	if moduleMode {
		if refreshModuleFence == nil {
			return false
		}
		if refreshModuleFence() {
			return true
		}
		if refreshModuleMounting {
			return false
		}
		body := doc.Get("body")
		if body.IsUndefined() || body.IsNull() {
			return false
		}
		return body.Call("hasAttribute", "data-nextgo-refresh-frozen").Bool()
	}
	return devRefresh != nil && devRefresh.frozen
}

var errRefreshFrozen = fmt.Errorf("nextgo: development refresh is pending")

func refreshBridgeQualified() bool {
	if !devRefreshBuild {
		return false
	}
	bridge := win.Get("__nextgoDevRuntime")
	return bridge.Type() == js.TypeObject && !bridge.IsNull() && bridge.Get("version").Type() == js.TypeNumber && bridge.Get("version").Float() == 1 && bridge.Get("shimDigest").String() == devrefresh.ShimDigest && bridge.Get("start").Type() == js.TypeFunction
}

// The persistent Go core obtains the latest proved descriptor table before any
// island hydration. Only the supervisor's explicit disabled response authorizes
// baseline hydration: an unavailable latest table may hide a newer kept build.
func bootstrapDevRefresh(ready func()) bool {
	if !devRefreshBuild {
		return false
	}
	c := devRefresh
	if c == nil {
		return false
	}
	c.startupReady = ready
	enableRefreshActivity()
	callback := js.FuncOf(func(_ js.Value, args []js.Value) any {
		if len(args) != 1 || args[0].Type() != js.TypeString {
			if c.initialized || c.disabled {
				c.reload()
			}
			return nil
		}
		c.receive([]byte(args[0].String()))
		return nil
	})
	globalFunctions = append(globalFunctions, callback)
	win.Set("__nextgoRefresh", callback)
	listen(doc, "compositionstart", func(js.Value) { c.composing = true })
	listen(doc, "compositionend", func(js.Value) { c.composing = false })
	for _, event := range []string{"beforeinput", "keydown", "pointerdown", "mousedown", "click", "submit", "reset"} {
		listen(doc, event, func(e js.Value) {
			if refreshFrozen() {
				e.Call("preventDefault")
			}
		})
	}
	c.createStartupStatus()
	c.startBootstrap()
	return true
}

// A single document-owned panel and listener survive manual retries. They live
// outside the application, so neither useful SSR nor a build-error banner is
// replaced. The native GET link also works while a transport is still pending.
func (c *refreshCoordinator) createStartupStatus() {
	panel := doc.Call("createElement", "div")
	panel.Set("id", "nextgo-dev-refresh-startup")
	panel.Call("setAttribute", "role", "status")
	message := doc.Call("createElement", "p")
	retry := doc.Call("createElement", "button")
	retry.Set("type", "button")
	retry.Set("textContent", "Retry loading Go")
	reload := doc.Call("createElement", "a")
	reload.Set("textContent", "Reload page")
	reload.Call("setAttribute", "href", win.Get("location").Get("href").String())
	reload.Call("setAttribute", "data-nextgo-reload", "")
	panel.Call("appendChild", message)
	panel.Call("appendChild", retry)
	panel.Call("appendChild", reload)
	doc.Get("body").Call("appendChild", panel)
	scope := &Scope{Root: Element{panel}}
	scope.On(Element{retry}, "click", func(Event) { c.startBootstrap() })
	scope.Cleanup(func() { panel.Call("remove") })
	c.startupScope, c.startupMessage, c.startupRetry = scope, message, retry
}

func (c *refreshCoordinator) startBootstrap() {
	if c.startupLoading || c.initialized || c.disabled || c.startupReady == nil {
		return
	}
	c.startupLoading = true
	c.startupRetry.Set("disabled", true)
	c.startupMessage.Set("textContent", "Checking current development modules…")
	c.startupScope.Root.Value.Call("setAttribute", "role", "status")
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), devrefresh.Watchdog)
		defer cancel()
		body := doc.Get("body")
		target := devrefresh.ManifestPath + "?build=" + url.QueryEscape(body.Call("getAttribute", "data-nextgo-build").String()) + "&generation=" + url.QueryEscape(body.Call("getAttribute", "data-nextgo-dev-generation").String())
		// A canceled, non-cooperative transport must not admit another physical
		// retry. Ordinary Fetch cancellation semantics remain unchanged.
		response, err := Fetch(ctx, target, FetchOptions{MaxBytes: devrefresh.MaxManifestBytes, Cache: "no-store", Redirect: "error", physicalSettlement: true})
		c.startupLoading = false
		if err == nil && response.StatusCode == 204 {
			c.disabled, c.pending = true, nil
			// An authoritative disabled response restores ordinary hydration,
			// including after a failed startup and an explicit retry.
			body.Call("removeAttribute", "data-nextgo-refresh-state")
			body.Call("removeAttribute", "data-nextgo-refresh")
			c.finishBootstrap()
			return
		}
		if err == nil && response.StatusCode == 200 {
			var manifest devrefresh.Manifest
			manifest, err = devrefresh.ParseManifest(response.Body)
			if err == nil && (!routeManifest.Development || !refreshBridgeQualified() || deployment.AssetPrefix != "") {
				err = devrefresh.ErrIncompatible
			}
			if err == nil {
				err = c.validateBaseline(manifest)
			}
			if err == nil && c.pending != nil {
				pending := *c.pending
				if pending.Revision > manifest.Revision {
					err = c.validateBaseline(pending)
					if err == nil {
						manifest = pending
					}
				} else if pending.Revision == manifest.Revision && !reflect.DeepEqual(pending, manifest) {
					err = devrefresh.ErrRevision
				}
				c.pending = nil
			}
			if err == nil {
				for _, module := range manifest.Modules {
					overlayLazyDescriptor(lazyModules[module.Name], module)
				}
				c.current, c.seen, c.initialized = manifest, manifest, true
				c.mark("ready")
				c.finishBootstrap()
				if c.pending != nil {
					pending := *c.pending
					c.pending = nil
					if pending.Revision > c.current.Revision {
						c.receiveManifest(pending)
					}
				}
				return
			}
		}
		// Do not hydrate old descriptors, reload, or retry automatically. SSE
		// received while failed remains a bounded hint until an explicit retry
		// has obtained the supervisor's current authoritative full table.
		c.startupMessage.Set("textContent", "Could not load current development modules. Server HTML is preserved. Retry loading Go or reload the page.")
		c.startupScope.Root.Value.Call("setAttribute", "role", "alert")
		c.startupRetry.Set("disabled", false)
		// Publish only after recovery is usable. Go-backed DOM calls can
		// reenter observers before this goroutine resumes.
		c.mark("startup-error")
	}()
}

func (c *refreshCoordinator) finishBootstrap() {
	ready := c.startupReady
	c.startupReady = nil
	c.startupScope.dispose()
	c.startupScope = nil
	c.startupMessage, c.startupRetry = js.Undefined(), js.Undefined()
	ready()
}

func (c *refreshCoordinator) validateBaseline(m devrefresh.Manifest) error {
	body := doc.Get("body")
	if m.Validate() != nil || m.Compatibility != c.compatibility || m.ServingBuild != body.Call("getAttribute", "data-nextgo-build").String() || m.ServingGeneration != body.Call("getAttribute", "data-nextgo-dev-generation").String() {
		return devrefresh.ErrIncompatible
	}
	count := 0
	for _, state := range lazyModules {
		if state.descriptor.StateSchema != "" {
			count++
		}
	}
	if count != len(m.Modules) {
		return devrefresh.ErrIncompatible
	}
	for _, module := range m.Modules {
		state := lazyModules[module.Name]
		if state == nil || state.descriptor.StateSchema != module.StateSchema || state.descriptor.DOMSchema != module.DOMSchema {
			return devrefresh.ErrIncompatible
		}
	}
	return nil
}
func (c *refreshCoordinator) receive(raw []byte) {
	m, err := devrefresh.ParseManifest(raw)
	if err != nil {
		if c.initialized || c.disabled {
			c.reload()
		}
		return
	}
	c.receiveManifest(m)
}
func (c *refreshCoordinator) receiveManifest(m devrefresh.Manifest) {
	if c.disabled {
		c.reload()
		return
	}
	if !c.initialized {
		c.pending = &m
		return
	}
	if m.Revision == c.seen.Revision && reflect.DeepEqual(m, c.seen) {
		return
	}
	if err := m.Next(c.seen); err != nil {
		c.reload()
		return
	}
	// Validate every observed parent while retaining only the latest pending
	// full table. Skipped network revisions still force a fresh document.
	c.seen = m
	c.pending = &m
	if c.running {
		return
	}
	c.running = true
	go c.run()
}
func (c *refreshCoordinator) run() {
	defer func() {
		c.running = false
		if recover() != nil {
			c.reload()
		}
	}()
	for c.pending != nil && !c.disabled {
		next := *c.pending
		c.pending = nil
		if err := c.apply(next); err != nil {
			c.reload()
			return
		}
	}
}
func (c *refreshCoordinator) mark(phase string) {
	body := doc.Get("body")
	body.Call("setAttribute", "data-nextgo-refresh", fmt.Sprint(c.current.Revision))
	body.Call("setAttribute", "data-nextgo-refresh-state", phase)
}
func clearRefreshBuildError() {
	status := doc.Call("getElementById", "nextgo-status")
	if status.Truthy() && status.Call("hasAttribute", "data-nextgo-dev-build-error").Bool() {
		status.Call("removeAttribute", "data-nextgo-dev-build-error")
		status.Set("textContent", "")
		status.Set("hidden", true)
	}
}
func (c *refreshCoordinator) reload() {
	if c.disabled && c.frozen {
		return
	}
	c.disabled, c.frozen = true, true
	c.pending = nil
	if c.cancel != nil {
		c.cancel()
	}
	doc.Get("body").Call("setAttribute", "data-nextgo-refresh-frozen", "")
	c.mark("reloading")
	win.Get("location").Call("reload")
}
func overlayLazyDescriptor(state *lazyState, m devrefresh.Module) {
	state.descriptor = LazyModule{File: "module-" + m.Digest + ".wasm", Digest: m.Digest, Epoch: m.Epoch, Size: m.Size, StateSchema: m.StateSchema, DOMSchema: m.DOMSchema}
	state.url = m.URL
}

type refreshReplacement struct {
	module   devrefresh.Module
	old      *lazyState
	compiled js.Value
	snapshot devrefresh.ModuleSnapshot
	raw      string
	mounts   []*lazyMount
	nodes    [][]js.Value
}

func (c *refreshCoordinator) idle() error {
	if c.frozen || c.composing || draftFrozen || actionViewFrozen || cancelNavigation != nil || activeActionView != nil || activeNavigationStream != nil || activeTransition != nil || len(prefetches) != 0 || refreshActivityState().Busy != 0 {
		return errRefreshFrozen
	}
	q := coreMutationQueue()
	q.mu.Lock()
	busy := q.active != nil || len(q.waiting) != 0 || q.frozen
	q.mu.Unlock()
	if busy || doc.Get("body").Call("getAttribute", "data-nextgo-hydration").String() != "ready" || doc.Call("querySelector", "form,dialog[open]").Truthy() {
		return errRefreshFrozen
	}
	for _, scope := range mounted {
		if !refreshScopeSupported(scope) {
			return errRefreshFrozen
		}
	}
	for _, state := range lazyModules {
		if state.loading || state.err != nil {
			return errRefreshFrozen
		}
		if state.exports.Truthy() {
			if state.descriptor.StateSchema == "" {
				return errRefreshFrozen
			}
			status, err := refreshModuleStatus(state)
			if err != nil || status.Phase != devrefresh.PhaseReady || status.Frozen || status.Pending != 0 || status.Scopes != len(state.mounts) {
				return errRefreshFrozen
			}
		}
	}
	return nil
}
func refreshModuleStatus(state *lazyState) (devrefresh.Status, error) {
	extension := state.exports.Get("refresh")
	if extension.Type() != js.TypeObject || extension.IsNull() || extension.Get("version").Type() != js.TypeNumber || extension.Get("version").Float() != devrefresh.ExtensionVersion {
		return devrefresh.Status{}, devrefresh.ErrIncompatible
	}
	for _, name := range []string{"status", "prepare", "stop", "import", "claim"} {
		if extension.Get(name).Type() != js.TypeFunction {
			return devrefresh.Status{}, devrefresh.ErrIncompatible
		}
	}
	return devrefresh.ParseStatus([]byte(extension.Call("status").String()))
}

func (c *refreshCoordinator) apply(next devrefresh.Manifest) error {
	if err := c.validateBaseline(next); err != nil {
		return err
	}
	source, navigation := currentURL, navigationID
	configuration, build, draft, history := documentConfiguration(), doc.Get("body").Call("getAttribute", "data-nextgo-build").String(), documentDraft(), actionHistoryEntry()
	ctx, cancel := context.WithTimeout(context.Background(), devrefresh.Watchdog)
	c.cancel = cancel
	defer func() { cancel(); c.cancel = nil }()
	var replacements []*refreshReplacement
	for _, module := range next.Modules {
		old := lazyModules[module.Name]
		if old.descriptor.Digest == module.Digest && old.descriptor.Epoch == module.Epoch {
			continue
		}
		if old.loading || !old.exports.Truthy() && old.runtime.Truthy() {
			return devrefresh.ErrIncompatible
		}
		compiled := js.Undefined()
		if old.exports.Truthy() {
			var err error
			compiled, err = compileRefreshModule(ctx, module)
			if err != nil {
				return err
			}
		}
		replacements = append(replacements, &refreshReplacement{module: module, old: old, compiled: compiled})
	}
	if source != currentURL || navigation != navigationID {
		return devrefresh.ErrIncompatible
	}
	loaded := 0
	for _, r := range replacements {
		if r.old.exports.Truthy() {
			loaded++
		}
	}
	if c.swaps+loaded > devrefresh.MaxSwaps {
		return devrefresh.ErrIncompatible
	}
	if loaded == 0 {
		for _, module := range next.Modules {
			overlayLazyDescriptor(lazyModules[module.Name], module)
		}
		c.current = next
		c.mark("ready")
		clearRefreshBuildError()
		return nil
	}
	if err := c.idle(); err != nil {
		return err
	}
	c.mark("preparing")
	c.frozen = true
	doc.Get("body").Call("setAttribute", "data-nextgo-refresh-frozen", "")
	coreMutationQueue().freeze()
	freezeRefreshActivity()
	state := devrefresh.Snapshot{Version: devrefresh.Version, Revision: next.Revision, Modules: []devrefresh.ModuleSnapshot{}}
	totalNodes, snapshotBytes := 0, 0
	for _, r := range replacements {
		if !r.old.exports.Truthy() {
			continue
		}
		for _, mount := range r.old.mounts {
			r.mounts = append(r.mounts, mount)
		}
		sort.Slice(r.mounts, func(i, j int) bool { return r.mounts[i].handle < r.mounts[j].handle })
		for _, mount := range r.mounts {
			if !validRefreshMount(mount, r.module.Name) {
				return devrefresh.ErrIncompatible
			}
			nodes, err := refreshDOMNodes(mount.root, 8192-totalNodes)
			if err != nil {
				return err
			}
			totalNodes += len(nodes)
			r.nodes = append(r.nodes, nodes)
		}
		prepared := r.old.exports.Get("refresh").Call("prepare")
		if prepared.Get("error").Type() != js.TypeString || prepared.Get("error").String() != "" || prepared.Get("snapshot").Type() != js.TypeString {
			return devrefresh.ErrIncompatible
		}
		r.raw = prepared.Get("snapshot").String()
		if len(r.raw) > devrefresh.MaxStateBytes-snapshotBytes {
			return devrefresh.ErrIncompatible
		}
		snapshotBytes += len(r.raw)
		var err error
		r.snapshot, err = devrefresh.ParseModuleSnapshot([]byte(r.raw))
		if err != nil {
			return err
		}
		if err = validateRefreshSnapshot(r); err != nil {
			return err
		}
		status, err := refreshModuleStatus(r.old)
		if err != nil || status.Phase != devrefresh.PhasePrepared || !status.Frozen || status.Pending != 0 {
			return devrefresh.ErrIncompatible
		}
		state.Modules = append(state.Modules, r.snapshot)
	}
	if state.Validate() != nil {
		return devrefresh.ErrIncompatible
	}
	controls, err := captureRefreshControls()
	if err != nil {
		return err
	}
	// No old runtime is disturbed until every candidate and snapshot validates.
	c.mark("applying")
	for _, r := range replacements {
		if !r.old.exports.Truthy() {
			continue
		}
		r.old.expectedStop = true
		for _, mount := range r.mounts {
			if !validRefreshMount(mount, r.module.Name) {
				return devrefresh.ErrIncompatible
			}
			removeRefreshScope(mount.scope)
			mount.scope.dispose()
		}
		if value := r.old.exports.Get("refresh").Call("stop"); value.Type() != js.TypeString || value.String() != "" {
			return devrefresh.ErrIncompatible
		}
		select {
		case <-r.old.runDone:
		case <-ctx.Done():
			return ctx.Err()
		}
		if err := validateRetiredRuntime(r.old); err != nil {
			return err
		}
		r.old.exports = js.Undefined()
		r.old.runtime = js.Undefined()
		r.old.runPromise = js.Undefined()
	}
	if err := waitRefreshActivity(ctx); err != nil {
		return err
	}
	for _, r := range replacements {
		if r.raw == "" {
			overlayLazyDescriptor(r.old, r.module)
			continue
		}
		candidate := &lazyState{loading: true, exports: js.Undefined()}
		overlayLazyDescriptor(candidate, r.module)
		lazyModules[r.module.Name] = candidate
		if err := instantiateLazyModule(ctx, r.module.Name, candidate, nil, r.compiled); err != nil {
			return err
		}
		candidate.loading = false
		candidate.ready = make(chan struct{})
		close(candidate.ready)
		if _, err := refreshModuleStatus(candidate); err != nil {
			return err
		}
		if value := candidate.exports.Get("refresh").Call("import", r.raw); value.Type() != js.TypeString || value.String() != "" {
			return devrefresh.ErrIncompatible
		}
		for i, mount := range r.mounts {
			if !validRefreshRoot(mount, r.module.Name) {
				return devrefresh.ErrIncompatible
			}
			result := candidate.exports.Call("mount", mount.root, mount.props, mount.opener, mount.handle)
			if result.Get("error").Type() != js.TypeString || result.Get("error").String() != "" || result.Get("id").Type() != js.TypeNumber || result.Get("id").Float() != float64(result.Get("id").Int()) || result.Get("id").Int() < 1 {
				return devrefresh.ErrIncompatible
			}
			if !sameRefreshDOM(mount.root, r.nodes[i]) || !validRefreshRoot(mount, r.module.Name) {
				return devrefresh.ErrIncompatible
			}
			scope := &Scope{Root: Element{mount.root}}
			trackLazyMount(candidate, scope, mount.root, mount.props, mount.opener, candidate.exports, result.Get("id").Int())
			mounted = append(mounted, scope)
		}
		if value := candidate.exports.Get("refresh").Call("claim"); value.Type() != js.TypeString || value.String() != "" {
			return devrefresh.ErrIncompatible
		}
		status, err := refreshModuleStatus(candidate)
		if err != nil || status.Phase != devrefresh.PhaseReady || status.Pending != 0 || status.Frozen || status.Scopes != len(r.mounts) {
			return devrefresh.ErrIncompatible
		}
		c.swaps++
	}
	for _, module := range next.Modules {
		overlayLazyDescriptor(lazyModules[module.Name], module)
	}
	for _, r := range replacements {
		for i, mount := range r.mounts {
			if !validRefreshRoot(mount, r.module.Name) || !sameRefreshDOM(mount.root, r.nodes[i]) {
				return devrefresh.ErrIncompatible
			}
		}
	}
	if ctx.Err() != nil || c.disabled || draftFrozen || actionViewFrozen || c.composing || activeActionView != nil || activeNavigationStream != nil || activeTransition != nil || source != currentURL || source != win.Get("location").Get("href").String() || navigation != navigationID || configuration != documentConfiguration() || build != doc.Get("body").Call("getAttribute", "data-nextgo-build").String() || draft != documentDraft() || history != actionHistoryEntry() || !controls.unchanged() {
		return devrefresh.ErrIncompatible
	}
	if err := thawRefreshActivity(); err != nil {
		return err
	}
	c.current = next
	c.frozen = false
	doc.Get("body").Call("removeAttribute", "data-nextgo-refresh-frozen")
	coreMutationQueue().reopen()
	c.mark("ready")
	clearRefreshBuildError()
	return nil
}

func compileRefreshModule(ctx context.Context, m devrefresh.Module) (js.Value, error) {
	expected := win.Get("URL").New(m.URL, win.Get("location").Get("href")).Get("href").String()
	response, err := Fetch(ctx, m.URL, FetchOptions{MaxBytes: int64(m.Size), Redirect: "error", Cache: "no-store"})
	if err != nil {
		return js.Undefined(), err
	}
	if response.StatusCode != 200 || response.URL != expected || len(response.Body) != m.Size {
		return js.Undefined(), devrefresh.ErrIncompatible
	}
	sum := sha256.Sum256(response.Body)
	if hex.EncodeToString(sum[:]) != m.Digest {
		return js.Undefined(), devrefresh.ErrIncompatible
	}
	bytes := win.Get("Uint8Array").New(len(response.Body))
	js.CopyBytesToJS(bytes, response.Body)
	controller := win.Get("AbortController").New()
	defer controller.Call("abort")
	return awaitBrowserPromise(ctx, win.Get("WebAssembly").Call("compile", bytes), controller)
}
func validateRefreshSnapshot(r *refreshReplacement) error {
	s := r.snapshot
	if s.Name != r.module.Name || s.Epoch != r.old.descriptor.Epoch || s.StateSchema != r.old.descriptor.StateSchema || s.DOMSchema != r.old.descriptor.DOMSchema || len(s.Scopes) != len(r.mounts) {
		return devrefresh.ErrIncompatible
	}
	byHandle := map[int]*lazyMount{}
	for _, mount := range r.mounts {
		if byHandle[mount.handle] != nil {
			return devrefresh.ErrIncompatible
		}
		byHandle[mount.handle] = mount
	}
	for _, scope := range s.Scopes {
		mount := byHandle[scope.Handle]
		if mount == nil {
			return devrefresh.ErrIncompatible
		}
		sum := sha256.Sum256([]byte(mount.props))
		if scope.PropsDigest != hex.EncodeToString(sum[:]) {
			return devrefresh.ErrIncompatible
		}
		delete(byHandle, scope.Handle)
	}
	if len(byHandle) != 0 {
		return devrefresh.ErrIncompatible
	}
	return nil
}
func validateRetiredRuntime(s *lazyState) error {
	select {
	case <-s.runDone:
	default:
		return devrefresh.ErrIncompatible
	}
	if s.runError != nil || !s.expectedStop || s.runtime.Type() != js.TypeObject || s.runPromise.Type() != js.TypeObject {
		return devrefresh.ErrIncompatible
	}
	code, timers := s.runtime.Get("__nextgoDevExitCode"), s.runtime.Get("_scheduledTimeouts")
	if code.Type() != js.TypeNumber || code.Float() != 0 || timers.Type() != js.TypeObject || timers.IsNull() || timers.Get("size").Type() != js.TypeNumber || timers.Get("size").Float() != 0 {
		return devrefresh.ErrIncompatible
	}
	return nil
}
func validRefreshRoot(m *lazyMount, name string) bool {
	app := doc.Call("getElementById", "nextgo-app")
	return app.Truthy() && app.Call("contains", m.root).Bool() && m.root.Get("parentNode").Equal(m.parent) && m.root.Get("isConnected").Bool() && m.root.Call("getAttribute", "data-nextgo-island").String() == name && m.root.Call("getAttribute", "data-nextgo-props").String() == m.props
}
func validRefreshMount(m *lazyMount, name string) bool {
	return !m.scope.closed && validRefreshRoot(m, name)
}
func removeRefreshScope(scope *Scope) {
	for i, item := range mounted {
		if item == scope {
			copy(mounted[i:], mounted[i+1:])
			mounted[len(mounted)-1] = nil
			mounted = mounted[:len(mounted)-1]
			return
		}
	}
}
func refreshDOMNodes(root js.Value, limit int) ([]js.Value, error) {
	var result []js.Value
	stack := []js.Value{root}
	for len(stack) > 0 {
		node := stack[len(stack)-1]
		stack = stack[:len(stack)-1]
		if len(result) >= limit {
			return nil, devrefresh.ErrIncompatible
		}
		result = append(result, node)
		children := node.Get("childNodes")
		if children.Length() > limit-len(result)-len(stack) {
			return nil, devrefresh.ErrIncompatible
		}
		for i := children.Length() - 1; i >= 0; i-- {
			stack = append(stack, children.Index(i))
		}
	}
	return result, nil
}
func sameRefreshDOM(root js.Value, want []js.Value) bool {
	got, err := refreshDOMNodes(root, len(want)+1)
	if err != nil || len(got) != len(want) {
		return false
	}
	for i, node := range got {
		if !node.Equal(want[i]) {
			return false
		}
	}
	return true
}

type refreshControl struct {
	node       js.Value
	form       js.Value
	kind       string
	value      string
	checked    bool
	start, end int
	direction  string
}
type refreshControls struct {
	items []refreshControl
	focus js.Value
}

func captureRefreshControls() (refreshControls, error) {
	result := refreshControls{focus: doc.Get("activeElement")}
	nodes := doc.Call("querySelectorAll", "input")
	if nodes.Length() > 256 {
		return result, devrefresh.ErrIncompatible
	}
	for i := 0; i < nodes.Length(); i++ {
		node := nodes.Index(i)
		kind := node.Get("type").String()
		if (kind != "text" && kind != "checkbox") || node.Get("form").Truthy() {
			return result, devrefresh.ErrIncompatible
		}
		item := refreshControl{node: node, form: node.Get("form"), kind: kind, value: node.Get("value").String(), checked: node.Get("checked").Bool()}
		if kind == "text" {
			item.start = node.Get("selectionStart").Int()
			item.end = node.Get("selectionEnd").Int()
			item.direction = node.Get("selectionDirection").String()
		}
		result.items = append(result.items, item)
	}
	return result, nil
}
func (s refreshControls) unchanged() bool {
	if !doc.Get("activeElement").Equal(s.focus) {
		return false
	}
	for _, item := range s.items {
		if !item.node.Get("isConnected").Bool() || item.node.Get("type").String() != item.kind || !item.node.Get("form").Equal(item.form) || item.node.Get("value").String() != item.value || item.node.Get("checked").Bool() != item.checked {
			return false
		}
		if item.node.Get("type").String() == "text" && (item.node.Get("selectionStart").Int() != item.start || item.node.Get("selectionEnd").Int() != item.end || item.node.Get("selectionDirection").String() != item.direction) {
			return false
		}
	}
	return true
}
