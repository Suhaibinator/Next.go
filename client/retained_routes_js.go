//go:build js && wasm

package client

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"
	"syscall/js"

	"github.com/Suhaibinator/Next.go/internal/navigation"
	"github.com/Suhaibinator/Next.go/internal/wirejson"
)

type retainedScroll struct {
	node js.Value
	x, y float64
}
type retainedView struct {
	info                                     retainedInfo
	root                                     js.Value
	descriptor                               navigation.Retention
	valid                                    bool
	activityErr                              error
	focus                                    js.Value
	selectionStart, selectionEnd             int
	selectionDirection                       string
	scroll                                   []retainedScroll
	originalHidden, originalInert            bool
	originalHiddenValue, originalInertValue  string
	originalDisplay, originalDisplayPriority string
}

var retainedViews = map[string]*retainedView{}
var retainedOwners retainedIndex
var retainedCurrent *retainedView
var retainedFocus *retainedView
var retainedFreshID, retainedFreshEpoch uint64

// A root owns at most one current synchronous report token. Ancestor lookup
// covers composed scopes; save/restore permits nested calls without a global
// report fence. Arbitrary blocked callback stacks/async errors are not bounded
// or attributed by this token; the existing root-owner ledgers still apply.
type retainedScopeReport struct {
	id       uint64
	messages []string
}
type retainedActivityFailure struct{ cause error }

func (e *retainedActivityFailure) Error() string { return retainedRecoveryMessage }
func (e *retainedActivityFailure) Unwrap() error { return e.cause }

func retainedReportOwner(scope *Scope) *Scope {
	for scope != nil && scope.parent != nil {
		scope = scope.parent
	}
	return scope
}

func reportScopeError(scope *Scope, message string) {
	owner := retainedReportOwner(scope)
	if owner != nil && owner.retainedReport != nil {
		token := owner.retainedReport
		// Keep the diagnostic ledger bounded independently of callback behavior.
		if len(message) > 2048 {
			message = message[:2048]
		}
		if len(token.messages) < 64 {
			token.messages = append(token.messages, strings.Clone(message))
		}
		win.Get("console").Call("error", "Next.go retained activity: "+message)
		return
	}
	report(message)
}

func withRetainedScopeReport(scope *Scope, id uint64, work func() error) (err error) {
	owner := retainedReportOwner(scope)
	if owner == nil {
		return work()
	}
	previous := owner.retainedReport
	token := &retainedScopeReport{id: id}
	owner.retainedReport = token
	defer func() { owner.retainedReport = previous }()
	err = work()
	for _, message := range token.messages {
		err = errors.Join(err, fmt.Errorf("retained activity: %s", message))
	}
	if err != nil {
		message := err.Error()
		if len(message) > 2048 {
			message = message[:2048]
		}
		win.Get("console").Call("error", "Next.go retained activity: "+message)
		return &retainedActivityFailure{cause: err}
	}
	return err
}

func retainedRoutingEnabled() bool {
	return !moduleMode && routeManifest.RetainRoutes && !routeManifest.Development && !devRefreshBuild && !routeManifest.Draft && routeManifest.Parallel == "" && routeManifest.Locale == "" && routeManifest.Scripts == "" && !staticExport() && !offlineNativeNavigation() && doc.Get("body").Call("getAttribute", "data-nextgo-preserve-layouts").String() != "true"
}

// Only this owner participates in app queries. Status and announcer remain
// document-owned siblings outside nextgo-app, never cached view children.
func applicationContentRoot() js.Value {
	root := doc.Call("getElementById", "nextgo-app")
	if retainedCurrent != nil && retainedCurrent.root.Get("parentNode").Equal(root) {
		return retainedCurrent.root
	}
	return root
}

func applicationQuery(selector string) js.Value {
	root := applicationContentRoot()
	if retainedCurrent != nil && root.Call("matches", selector).Bool() {
		return root
	}
	return root.Call("querySelector", selector)
}

func applicationHydrationError() bool {
	if retainedCurrent != nil {
		return applicationQuery(`[data-nextgo-hydrated="error"]`).Truthy()
	}
	return doc.Call("querySelector", `[data-nextgo-hydrated="error"]`).Truthy()
}

// Validate exact response bytes before HTML parsing can normalize them. Only
// opted-in navigation adds stricter retention-field parsing; old defaults stay.
func validateRetainedEnvelope(body []byte, data *envelope) error {
	if !routeManifest.RetainRoutes && data.Retention == nil {
		return nil
	}
	if wirejson.Unique(body) != nil {
		return navigation.ErrRetention
	}
	var fields map[string]json.RawMessage
	if json.Unmarshal(body, &fields) != nil {
		return navigation.ErrRetention
	}
	for name := range fields {
		if name != "retention" && strings.EqualFold(name, "retention") {
			return navigation.ErrRetention
		}
	}
	raw, exists := fields["retention"]
	if !exists {
		data.Retention = nil
		return nil
	}
	if !routeManifest.RetainRoutes || data.Status != 200 || strings.TrimSpace(string(raw)) == "null" {
		return navigation.ErrRetention
	}
	value, err := navigation.ParseRetention(string(raw), data.HTML)
	if err != nil || value == nil {
		return navigation.ErrRetention
	}
	data.Retention = value
	return nil
}

func hiddenRetainedNode(node js.Value) bool {
	if len(retainedViews) == 0 || node.Type() != js.TypeObject || node.IsNull() {
		return false
	}
	// Only registered owners suppress ordinary document behavior. The bounded
	// registry also avoids treating arbitrary author markers as retained views.
	for _, view := range retainedViews {
		if view.root.Call("hasAttribute", "hidden").Bool() && view.root.Call("contains", node).Bool() {
			return true
		}
	}
	return false
}

func retainedViewID() (string, error) {
	var key [16]byte
	if _, err := rand.Read(key[:]); err != nil {
		return "", err
	}
	return hex.EncodeToString(key[:]), nil
}

// Count first, then run selectors: even a hostile oversized tree does not cause
// unbounded materialization of a querySelectorAll result during admission.
func retainedDOMInfo(region js.Value, hydrated bool) (nodes, islands int, ok bool) {
	if region.Type() != js.TypeObject || region.IsNull() {
		return 0, 0, false
	}
	stack := []js.Value{region}
	for len(stack) != 0 {
		node := stack[len(stack)-1]
		stack = stack[:len(stack)-1]
		nodes++
		if nodes > retainedViewNodes {
			return 0, 0, false
		}
		children := node.Get("childNodes")
		if children.Type() == js.TypeObject && !children.IsNull() {
			if children.Length() > retainedViewNodes-nodes-len(stack) {
				return 0, 0, false
			}
			for i := children.Length() - 1; i >= 0; i-- {
				stack = append(stack, children.Index(i))
			}
		}
	}
	unsafe := `[id],[form],input[type="radio"],details[name],a[name],form,dialog,iframe,video,audio,canvas,object,embed,script,style,link,base,meta,map,area,template,nextgo-layout,nextgo-outlet,nextgo-slot,nextgo-template,[data-nextgo-client],[data-nextgo-selection],[data-nextgo-boundary],[data-nextgo-script],[data-nextgo-chunk],[data-nextgo-metadata],[data-nextgo-metadata-ready]`
	if region.Call("matches", unsafe).Bool() || region.Call("querySelector", unsafe).Truthy() {
		return 0, 0, false
	}
	if region.Call("querySelector", "[data-nextgo-view],[data-nextgo-view-active]").Truthy() {
		return 0, 0, false
	}
	elements := retainedElementNodes(region)
	for i := 0; i < elements.Length(); i++ {
		node := elements.Index(i)
		if strings.Contains(node.Get("tagName").String(), "-") || node.Get("shadowRoot").Truthy() {
			return 0, 0, false
		}
	}
	roots := retainedIslandNodes(region)
	if roots.Length() > retainedViewIslands {
		return 0, 0, false
	}
	for i := 0; i < roots.Length(); i++ {
		node := roots.Index(i)
		name := node.Call("getAttribute", "data-nextgo-island").String()
		if registry[name] == nil || lazyModules[name] != nil || hydrated && node.Call("getAttribute", "data-nextgo-hydrated").String() != "true" {
			return 0, 0, false
		}
	}
	return nodes, roots.Length(), true
}

func retainedScopes(region js.Value) ([]*Scope, bool) {
	var scopes []*Scope
	for _, scope := range mounted {
		if region.Call("contains", scope.Root.Value).Bool() {
			if !scope.retentionCapable() || len(scopes) >= retainedViewIslands {
				return nil, false
			}
			scopes = append(scopes, scope)
		}
	}
	return scopes, true
}

// Revoked owners cannot be suspended or resumed through their lifetime API.
// They may only retire after a fresh admitted view is published. Require exact
// current app ownership and the existing bounded scope tree; this exception
// never makes a revoked scope eligible for reuse or clears any authority flag.
func retainedRevokedScopes(region js.Value) ([]*Scope, bool) {
	application := mergeApplication
	app := doc.Call("getElementById", "nextgo-app")
	if application == nil || !application.revoked || !app.Equal(application.root) || !app.Get("isConnected").Bool() || !region.Get("parentNode").Equal(app) {
		return nil, false
	}
	var scopes []*Scope
	for _, scope := range mounted {
		if !region.Call("contains", scope.Root.Value).Bool() {
			continue
		}
		if scope.parent != nil || !scope.mergeRevoked || len(scopes) >= retainedViewIslands {
			return nil, false
		}
		if _, owned := application.scopes[scope]; !owned {
			return nil, false
		}
		if _, err := scope.retainedScopeTree(); err != nil {
			return nil, false
		}
		scopes = append(scopes, scope)
	}
	return scopes, true
}

// Stop framework visibility work without invoking authored effect or terminal
// cleanup. Physical task charges remain owned until their goroutines return.
func pauseRevokedRetirement(scope *Scope) error {
	tree, err := scope.retainedScopeTree()
	if err != nil {
		return err
	}
	for _, owner := range tree {
		owner.suspended = true
		owner.activityEpoch++
		owner.stopVisibilityTasks()
		for resource := range owner.asyncResources {
			resource.suspendForRetention()
		}
		for _, listener := range owner.listeners {
			listener.detach()
		}
	}
	return nil
}

// A supported owner is already an ordinary direct child in SSR. Keeping it
// avoids adding wrappers that would change child-combinator CSS and ancestry.
func retainedOwner(region js.Value) (js.Value, bool) {
	owner := js.Null()
	nodes := region.Get("childNodes")
	if nodes.Type() != js.TypeObject || nodes.IsNull() || nodes.Length() > retainedViewNodes {
		return owner, false
	}
	for i := 0; i < nodes.Length(); i++ {
		node := nodes.Index(i)
		if node.Get("nodeType").Int() == 3 && strings.TrimSpace(node.Get("nodeValue").String()) == "" {
			continue
		}
		if node.Get("nodeType").Int() != 1 || owner.Truthy() {
			return js.Null(), false
		}
		owner = node
	}
	return owner, owner.Truthy()
}

func retainedElementNodes(root js.Value) js.Value {
	result := js.Global().Get("Array").New()
	result.Call("push", root)
	children := root.Call("querySelectorAll", "*")
	for i := 0; i < children.Length(); i++ {
		result.Call("push", children.Index(i))
	}
	return result
}
func retainedIslandNodes(root js.Value) js.Value {
	result := js.Global().Get("Array").New()
	if root.Call("hasAttribute", "data-nextgo-island").Bool() {
		result.Call("push", root)
	}
	children := root.Call("querySelectorAll", "[data-nextgo-island]")
	for i := 0; i < children.Length(); i++ {
		result.Call("push", children.Index(i))
	}
	return result
}
func applicationIslandNodes() js.Value {
	if retainedCurrent != nil {
		return retainedIslandNodes(applicationContentRoot())
	}
	return applicationContentRoot().Call("querySelectorAll", "[data-nextgo-island]")
}
func hideRetained(view *retainedView) {
	view.root.Call("removeAttribute", "data-nextgo-view-active")
	view.root.Set("hidden", true)
	view.root.Call("setAttribute", "inert", "")
	view.root.Get("style").Call("setProperty", "display", "none", "important")
}
func captureRetainedPresentation(view *retainedView) {
	view.originalHidden = view.root.Call("hasAttribute", "hidden").Bool()
	view.originalInert = view.root.Call("hasAttribute", "inert").Bool()
	view.originalHiddenValue = view.root.Call("getAttribute", "hidden").String()
	view.originalInertValue = view.root.Call("getAttribute", "inert").String()
	view.originalDisplay = view.root.Get("style").Call("getPropertyValue", "display").String()
	view.originalDisplayPriority = view.root.Get("style").Call("getPropertyPriority", "display").String()
}
func showRetained(view *retainedView) {
	if view.originalHidden {
		view.root.Call("setAttribute", "hidden", view.originalHiddenValue)
	} else {
		view.root.Call("removeAttribute", "hidden")
	}
	if view.originalInert {
		view.root.Call("setAttribute", "inert", view.originalInertValue)
	} else {
		view.root.Call("removeAttribute", "inert")
	}
	if view.originalDisplay == "" {
		view.root.Get("style").Call("removeProperty", "display")
	} else {
		view.root.Get("style").Call("setProperty", "display", view.originalDisplay, view.originalDisplayPriority)
	}
	view.root.Call("setAttribute", "data-nextgo-view-active", "")
}
func prepareRetainedView(owner js.Value, descriptor *navigation.Retention, target string, size int, id string) *retainedView {
	nodes, islands, _ := retainedDOMInfo(owner, false)
	return &retainedView{root: owner, descriptor: *descriptor, valid: true, focus: js.Null(),
		info:           retainedInfo{ID: id, URL: requestURL(target), Partition: descriptor.Partition, Fingerprint: descriptor.Fingerprint, Nodes: nodes, Islands: islands, Bytes: size},
		originalHidden: owner.Call("hasAttribute", "hidden").Bool(), originalInert: owner.Call("hasAttribute", "inert").Bool(),
		originalHiddenValue: owner.Call("getAttribute", "hidden").String(), originalInertValue: owner.Call("getAttribute", "inert").String(),
		originalDisplay: owner.Get("style").Call("getPropertyValue", "display").String(), originalDisplayPriority: owner.Get("style").Call("getPropertyPriority", "display").String()}
}
func registerRetained(view *retainedView) {
	view.root.Call("setAttribute", "data-nextgo-view", view.info.ID)
	retainedViews[view.info.ID] = view
	retainedOwners.put(view.info)
}

func initializeRetainedDocument() {
	retainedTaskAdmission = retainedRoutingEnabled()
	if !retainedTaskAdmission {
		return
	}
	raw := doc.Get("body").Call("getAttribute", navigation.RetentionAttribute)
	if raw.Type() != js.TypeString {
		return
	}
	descriptor, err := navigation.ParseRetentionDescriptor(raw.String())
	if err != nil || descriptor == nil {
		return
	}
	root := doc.Call("getElementById", "nextgo-app")
	owner, ok := retainedOwner(root)
	if !ok {
		return
	}
	if _, _, safe := retainedDOMInfo(owner, false); !safe {
		return
	}
	if owner.Call("hasAttribute", "data-nextgo-view").Bool() || owner.Call("hasAttribute", "data-nextgo-view-active").Bool() {
		return
	}
	id, err := retainedViewID()
	if err != nil {
		return
	}
	// There is intentionally no initial raw-HTML snapshot to measure. Charge
	// its full server-advertised body ceiling rather than undercounting it.
	view := prepareRetainedView(owner, descriptor, currentURL, navigation.MaxRetentionHTML+len(raw.String())+len(currentURL)+512, id)
	if _, err := retainedOwners.plan(view.info); err != nil {
		return
	}
	// Initial stamp is issued by the already validated server document. It is
	// not a client digest of normalized DOM, nor a MAC against same-origin JS.
	registerRetained(view)
	retainedCurrent = view
	showRetained(view)
}

// This phase invokes no application cleanup. Retiring an owner before adding
// its replacement keeps real registry/byte ledgers bounded at the commit point.
// Detached scopes remain suspended until the visible replacement is hydrated.
func retireRetainedOwnership(view *retainedView) {
	if view == nil {
		return
	}
	view.root.Call("remove")
	delete(retainedViews, view.info.ID)
	retainedOwners.remove(view.info.ID)
	if retainedFocus == view {
		retainedFocus = nil
	}
	if retainedCurrent == view {
		retainedCurrent = nil
	}
	view.scroll = nil
	view.focus = js.Null()
}

func disposeRetainedActivity(view *retainedView, id uint64) error {
	if view == nil {
		return nil
	}
	owned := map[*Scope]bool{}
	var ordered []*Scope
	for _, scope := range mounted {
		if view.root.Call("contains", scope.Root.Value).Bool() {
			owned[scope] = true
			ordered = append(ordered, scope)
		}
	}
	var failures []error
	for _, scope := range ordered {
		if err := withRetainedScopeReport(scope, id, func() error { scope.dispose(); return nil }); err != nil {
			failures = append(failures, err)
		}
	}
	kept := mounted[:0]
	for _, scope := range mounted {
		if !owned[scope] {
			kept = append(kept, scope)
		}
	}
	mounted = kept
	return errors.Join(failures...)
}

func forgetRetained(view *retainedView) error {
	if view == nil {
		return nil
	}
	id := navigationID
	retireRetainedOwnership(view)
	return disposeRetainedActivity(view, id)
}

const retainedRecoveryMessage = "A component could not resume. Navigate to another page or reload."
const retainedCleanupMessage = "A component could not finish closing. Navigate to another page or reload."

func failRetainedActivity(view *retainedView, scope *Scope, err error, id uint64) {
	if view == nil || err == nil {
		return
	}
	view.valid = false
	view.activityErr = errors.Join(view.activityErr, err)
	retainedOwners.dropAliases(view.info.ID)
	boundary := view.root
	if scope != nil {
		boundary = scope.Root.Value
	}
	boundary.Call("setAttribute", "data-nextgo-hydrated", "error")
	if scope != nil {
		_ = withRetainedScopeReport(scope, id, func() error { scope.dispose(); return nil })
		kept := mounted[:0]
		for _, owner := range mounted {
			if owner != scope {
				kept = append(kept, owner)
			}
		}
		mounted = kept
	}
	win.Get("console").Call("error", "Next.go retained activity: "+err.Error())
	if retainedCurrent == view {
		doc.Get("body").Call("setAttribute", "data-nextgo-hydration", "error")
		if id == navigationID {
			report(retainedRecoveryMessage)
		}
	}
}

// preserveActive is used for mutation fences: active UI continues normally, but
// cannot later become a reusable snapshot. Hidden private state is discarded.
func clearRetainedViews(preserveActive bool) error {
	snapshot := make([]*retainedView, 0, len(retainedViews))
	for _, view := range retainedViews {
		snapshot = append(snapshot, view)
	}
	var failures []error
	for _, view := range snapshot {
		if preserveActive && view == retainedCurrent {
			continue
		}
		if err := forgetRetained(view); err != nil {
			failures = append(failures, err)
		}
	}
	if preserveActive && retainedCurrent != nil {
		retainedCurrent.valid = false
		retainedOwners.dropAliases(retainedCurrent.info.ID)
	}
	retainedFocus = nil
	return errors.Join(failures...)
}

func bindRetainedHistory() {
	if retainedCurrent == nil || !retainedCurrent.valid {
		return
	}
	if _, ok := trackedHistory(currentState, currentURL); !ok {
		return
	}
	key := currentState.Get("nextgoEntry")
	if key.Type() == js.TypeString {
		retainedOwners.bind(key.String(), retainedCurrent.info.ID)
	}
}

// Equal URLs can still represent distinct pushed visits. Only a tracked alias
// of this exact owner is a same-view/hash traversal; every other opted-in pop
// must pass fresh server validation before it can reveal any retained state.
func retainedPopSameView(state js.Value, target string) bool {
	if !retainedRoutingEnabled() {
		return true
	}
	if retainedCurrent == nil || !retainedCurrent.valid {
		return false
	}
	if _, owned := trackedHistory(state, target); !owned {
		return false
	}
	key := state.Get("nextgoEntry")
	return key.Type() == js.TypeString && retainedOwners.aliases[key.String()] == retainedCurrent.info.ID
}

func captureRetained(view *retainedView) {
	view.focus = doc.Get("activeElement")
	if !view.root.Call("contains", view.focus).Bool() {
		view.focus = js.Null()
	}
	view.selectionDirection = ""
	if view.focus.Truthy() && view.focus.Get("selectionStart").Type() == js.TypeNumber && view.focus.Get("selectionEnd").Type() == js.TypeNumber {
		view.selectionStart, view.selectionEnd = view.focus.Get("selectionStart").Int(), view.focus.Get("selectionEnd").Int()
		if direction := view.focus.Get("selectionDirection"); direction.Type() == js.TypeString {
			view.selectionDirection = direction.String()
		}
	}
	view.scroll = nil
	nodes := retainedElementNodes(view.root)
	for i := 0; i < nodes.Length() && len(view.scroll) < 128; i++ {
		node := nodes.Index(i)
		if node.Get("scrollTop").Type() == js.TypeNumber && node.Get("scrollLeft").Type() == js.TypeNumber && (node.Get("scrollTop").Float() != 0 || node.Get("scrollLeft").Float() != 0) {
			view.scroll = append(view.scroll, retainedScroll{node, node.Get("scrollLeft").Float(), node.Get("scrollTop").Float()})
		}
	}
}

func restoreRetainedFocus(pop bool) {
	defer func() { _ = recover() }()
	view := retainedFocus
	retainedFocus = nil
	if !pop || view == nil || view != retainedCurrent {
		return
	}
	if view.focus.Truthy() && view.focus.Get("isConnected").Bool() && view.root.Call("contains", view.focus).Bool() {
		// Materialize the restored control before focusing it. Otherwise an
		// engine may attach its renderer while focused and queue a later reveal.
		// Saved nested positions are restored after focus, with no delayed replay.
		_ = view.focus.Get("offsetWidth")
		view.focus.Call("focus", map[string]any{"preventScroll": true})
		if view.selectionDirection != "" && view.focus.Get("setSelectionRange").Type() == js.TypeFunction {
			start, end, direction := view.focus.Get("selectionStart"), view.focus.Get("selectionEnd"), view.focus.Get("selectionDirection")
			// Retained inputs already preserve their native selection. Avoid a
			// redundant setter: it can schedule native caret scrolling after the
			// synchronous focus and scroll restoration below.
			unchanged := start.Type() == js.TypeNumber && end.Type() == js.TypeNumber && direction.Type() == js.TypeString && start.Int() == view.selectionStart && end.Int() == view.selectionEnd && direction.String() == view.selectionDirection
			if !unchanged {
				view.focus.Call("setSelectionRange", view.selectionStart, view.selectionEnd, view.selectionDirection)
			}
		}
	}
	for _, entry := range view.scroll {
		if view.root.Call("contains", entry.node).Bool() {
			entry.node.Set("scrollLeft", entry.x)
			entry.node.Set("scrollTop", entry.y)
		}
	}
}

type retainedDOMPlan struct {
	selected, outgoing     *retainedView
	outgoingScopes         []*Scope
	keepOutgoing, restored bool
	retireRevoked          bool
	outgoingInfo           retainedInfo
	selectedInfo           retainedInfo
	evictions              []string
	id, epoch              uint64
}
type retainedAdmission func(*retainedIndex, retainedInfo) ([]string, error)
type retainedDOMCommit struct{ Activate, Retire func() }

func retainedAdmit(index *retainedIndex, info retainedInfo) ([]string, error) {
	return index.plan(info)
}
func cloneRetainedIndex() retainedIndex {
	copy := retainedIndex{entries: map[string]retainedInfo{}, active: retainedOwners.active, sequence: retainedOwners.sequence}
	for id, info := range retainedOwners.entries {
		copy.entries[id] = info
	}
	return copy
}

// All fallible allocation, parsing, eligibility and admission work happens
// before any outgoing listener/effect/DOM mutation. Dependencies permit precise
// failure-order tests without exposing test controls to applications.
func prepareRetainedDOM(root, incoming js.Value, data envelope, target string, pop bool, state js.Value, newID func() (string, error), admit retainedAdmission) (*retainedDOMPlan, error) {
	owner, ok := retainedOwner(incoming)
	if !ok {
		return nil, nil
	}
	if _, _, safe := retainedDOMInfo(owner, false); !safe {
		return nil, nil
	}
	if owner.Call("hasAttribute", "data-nextgo-view").Bool() || owner.Call("hasAttribute", "data-nextgo-view-active").Bool() {
		return nil, nil
	}
	if envelopeBytes(data) > retainedMetadataBytes {
		return nil, nil
	}
	plan := &retainedDOMPlan{outgoing: retainedCurrent, id: navigationID, epoch: cacheEpoch}
	index := cloneRetainedIndex()
	remove := map[string]bool{}
	if retainedCurrent != nil && retainedCurrent.descriptor.Partition != data.Retention.Partition {
		for id := range index.entries {
			remove[id] = true
			index.remove(id)
		}
	}
	if pop {
		if _, owned := trackedHistory(state, target); owned {
			key := state.Get("nextgoEntry")
			if key.Type() == js.TypeString {
				id, match := retainedOwners.lookup(key.String(), requestURL(target), data.Retention.Partition, data.Retention.Fingerprint)
				if match && !remove[id] {
					plan.selected = retainedViews[id]
				} else if stale := retainedViews[id]; stale != nil {
					remove[id] = true
					index.remove(id)
				}
			}
		}
	}
	if plan.selected != nil {
		nodes, islands, safe := retainedDOMInfo(plan.selected.root, true)
		_, scoped := retainedScopes(plan.selected.root)
		if !plan.selected.valid || !safe || !scoped || !plan.selected.root.Get("parentNode").Equal(root) {
			remove[plan.selected.info.ID] = true
			index.remove(plan.selected.info.ID)
			plan.selected = nil
		} else {
			plan.restored = true
			plan.selectedInfo = plan.selected.info
			plan.selectedInfo.Nodes, plan.selectedInfo.Islands = nodes, islands
		}
	}
	if plan.outgoing != nil {
		nodes, islands, safe := retainedDOMInfo(plan.outgoing.root, true)
		plan.keepOutgoing = plan.outgoing.valid && !remove[plan.outgoing.info.ID]
		scopes, scoped := retainedScopes(plan.outgoing.root)
		if !plan.keepOutgoing && mergeApplicationRevoked() {
			scopes, scoped = retainedRevokedScopes(plan.outgoing.root)
			plan.retireRevoked = scoped
		}
		if !safe || !scoped {
			return nil, nil
		}
		plan.outgoingScopes = scopes
		if plan.keepOutgoing {
			info := index.entries[plan.outgoing.info.ID]
			info.Nodes, info.Islands = nodes, islands
			plan.outgoingInfo = info
			index.entries[info.ID] = info
		} else {
			remove[plan.outgoing.info.ID] = true
			index.remove(plan.outgoing.info.ID)
		}
	}
	if plan.selected == nil {
		id, err := newID()
		if err != nil {
			return nil, err
		}
		if !retainedID(id) || retainedViews[id] != nil {
			return nil, fmt.Errorf("nextgo: invalid or colliding retained view identity")
		}
		plan.selected = prepareRetainedView(owner, data.Retention, target, int(envelopeBytes(data)), id)
		plan.selectedInfo = plan.selected.info
	}
	plan.selectedInfo.Bytes = int(envelopeBytes(data))
	info := plan.selectedInfo
	evictions, err := admit(&index, info)
	if errors.Is(err, errRetainedCapacity) && plan.keepOutgoing && plan.outgoing != plan.selected {
		plan.keepOutgoing = false
		remove[plan.outgoing.info.ID] = true
		index.remove(plan.outgoing.info.ID)
		evictions, err = admit(&index, info)
	}
	if err != nil {
		return nil, err
	}
	for _, id := range evictions {
		remove[id] = true
	}
	for id := range remove {
		if plan.selected.info.ID != id {
			plan.evictions = append(plan.evictions, id)
		}
	}
	sort.Strings(plan.evictions)
	return plan, nil
}

// A descriptor-free or unsupported success uses ordinary fresh rendering and
// evicts hidden state. It cannot leave an old privacy partition reachable.
func commitRetainedDOM(root, incoming js.Value, data envelope, target string, pop bool, state js.Value) (bool, *retainedDOMCommit, error) {
	retainedFocus = nil
	id, epoch := navigationID, cacheEpoch
	cold := func() (bool, *retainedDOMCommit, error) {
		err := clearRetainedViews(false)
		if id != navigationID || epoch != cacheEpoch {
			return false, nil, errors.Join(err, fmt.Errorf("nextgo: navigation changed during retained teardown"))
		}
		if err == nil {
			return false, nil, nil
		}
		// Terminal cleanup diagnostics cannot veto an otherwise authorized fresh
		// page after private old DOM has already retired. Report only after the
		// ordinary render/hydration tail, and never replace a newer status/error.
		reported := false
		finish := func() {
			if reported {
				return
			}
			reported = true
			if id != navigationID || epoch != cacheEpoch {
				return
			}
			status := doc.Call("getElementById", "nextgo-status")
			if doc.Get("body").Call("getAttribute", "data-nextgo-hydration").String() == "ready" && status.Get("hidden").Bool() {
				report(retainedCleanupMessage)
			}
		}
		return false, &retainedDOMCommit{Activate: func() {}, Retire: finish}, nil
	}
	if !retainedRoutingEnabled() || data.Retention == nil || data.Status != 200 || data.metadata != nil || data.Shell != "" {
		return cold()
	}
	if err := data.Retention.Validate(data.HTML); err != nil {
		clearRetainedViews(true)
		return false, nil, err
	}
	if retainedFreshID != navigationID || retainedFreshEpoch != cacheEpoch {
		return false, nil, fmt.Errorf("nextgo: retained navigation was superseded or invalidated")
	}
	plan, err := prepareRetainedDOM(root, incoming, data, target, pop, state, retainedViewID, retainedAdmit)
	if err != nil {
		return false, nil, err
	}
	if plan == nil {
		return cold()
	}
	fence := func() bool { return plan.id == navigationID && plan.epoch == cacheEpoch }
	// Framework DOM is not hidden before rollback. Failed activity is disposed,
	// marked persistently erroneous and never silently readmitted for reuse.
	resumeOutgoing := func() error {
		if plan.retireRevoked {
			return nil
		}
		var failures []error
		if plan.outgoing != nil {
			for _, scope := range plan.outgoingScopes {
				if failure := withRetainedScopeReport(scope, plan.id, scope.resumeFromRetention); failure != nil {
					failures = append(failures, failure)
					failRetainedActivity(plan.outgoing, scope, failure, plan.id)
				}
			}
		}
		return errors.Join(failures...)
	}
	abort := func(cause error) (bool, *retainedDOMCommit, error) {
		return false, nil, errors.Join(cause, resumeOutgoing())
	}
	if plan.outgoing != nil && plan.outgoing != plan.selected {
		captureRetained(plan.outgoing)
		for _, scope := range plan.outgoingScopes {
			suspend := scope.suspendForRetention
			if plan.retireRevoked {
				suspend = func() error { return pauseRevokedRetirement(scope) }
			}
			if err := withRetainedScopeReport(scope, plan.id, suspend); err != nil {
				failRetainedActivity(plan.outgoing, scope, err, plan.id)
				return abort(err)
			}
			if !fence() {
				return abort(fmt.Errorf("nextgo: retained navigation changed during suspension"))
			}
		}
	}
	// Existing hidden evictions still run before commit; a cleanup-triggered
	// superseding request resumes the outgoing view (or reports its failure).
	for _, id := range plan.evictions {
		view := retainedViews[id]
		if view == plan.outgoing {
			continue
		}
		if err := forgetRetained(view); err != nil {
			return abort(err)
		}
		if !fence() {
			return abort(fmt.Errorf("nextgo: retained navigation changed during eviction"))
		}
	}
	if !fence() {
		return abort(fmt.Errorf("nextgo: retained navigation was canceled before commit"))
	}
	if plan.outgoing == nil {
		disposeWithin(root)
		if !fence() {
			return false, nil, fmt.Errorf("nextgo: retained navigation changed during ordinary unmount")
		}
		root.Call("replaceChildren")
	}
	// Callback-free ownership commit. Nonkept outgoing ownership and DOM retire
	// before the candidate enters the real ledgers; physical workers remain
	// charged until return. Terminal callbacks wait for a visible hydrated view.
	var retired *retainedView
	if plan.outgoing != nil && plan.outgoing != plan.selected {
		captureRetainedPresentation(plan.outgoing)
		if plan.keepOutgoing {
			plan.outgoing.info = plan.outgoingInfo
			retainedOwners.entries[plan.outgoing.info.ID] = plan.outgoingInfo
			hideRetained(plan.outgoing)
		} else {
			retired = plan.outgoing
			retireRetainedOwnership(retired)
		}
	}
	plan.selected.info = plan.selectedInfo
	if !plan.restored {
		root.Call("appendChild", plan.selected.root)
		registerRetained(plan.selected)
	} else {
		retainedOwners.put(plan.selected.info)
		retainedFocus = plan.selected
	}
	retainedCurrent = plan.selected
	showRetained(plan.selected)
	activated := false
	activate := func() {
		if activated {
			return
		}
		activated = true
		if retainedCurrent != plan.selected {
			return
		}
		if plan.restored {
			scopes, safe := retainedScopes(plan.selected.root)
			if !safe {
				failRetainedActivity(plan.selected, nil, fmt.Errorf("retained ownership changed before resume"), plan.id)
				_ = disposeRetainedActivity(plan.selected, plan.id)
			} else {
				for _, scope := range scopes {
					if retainedCurrent != plan.selected {
						break
					}
					if err := withRetainedScopeReport(scope, plan.id, scope.resumeFromRetention); err != nil {
						failRetainedActivity(plan.selected, scope, err, plan.id)
					}
				}
			}
		}
		if retainedCurrent == plan.selected {
			if plan.epoch != cacheEpoch {
				plan.selected.valid = false
				retainedOwners.dropAliases(plan.selected.info.ID)
			}
			// Fresh islands mount before any retired terminal callback can start a
			// newer request. Supersession therefore leaves useful committed UI.
			hydrate()
		}
	}
	closed := false
	retire := func() {
		if closed {
			return
		}
		closed = true
		if err := disposeRetainedActivity(retired, plan.id); err != nil {
			failRetainedActivity(plan.selected, nil, err, plan.id)
		}
	}
	return true, &retainedDOMCommit{Activate: activate, Retire: retire}, nil
}
