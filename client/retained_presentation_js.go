//go:build js && wasm

package client

import (
	"math"
	"syscall/js"
)

// A single optional post-commit job finalizes saved nested positions. It never
// delays a commit callback, replays focus or writes window/history/DOM structure.
const retainedSettlementDeadlineMS = 250
const retainedSettlementNodes = 128

type retainedSettlementSnapshot struct {
	view                   *retainedView
	app, focus             js.Value
	intent, epoch, privacy uint64
	url, entry             string
	selection              bool
	start, end             int
	direction              string
	positions              []retainedScroll
}
type retainedSettlementPlatform struct {
	window, document                     js.Value
	request, cancel, set, clear          js.Value
	docAdd, docRemove, winAdd, winRemove js.Value
}
type retainedSettlementCallback struct {
	fn                                        js.Func
	made, released                            bool
	running                                   int
	mayOwn, delivered, known, cancelAttempted bool
	registering, canceling                    bool
	handle                                    js.Value
}
type retainedSettlementListener struct {
	target, add, remove                            js.Value
	event                                          string
	mayOwn, registering, removing, removeAttempted bool
}
type retainedSettlementJob struct {
	snapshot               *retainedSettlementSnapshot
	platform               retainedSettlementPlatform
	lease                  *refreshActivityLease
	frames                 [2]*retainedSettlementCallback
	deadline, guard        *retainedSettlementCallback
	listeners              []retainedSettlementListener
	completedFrames, setup int
	closed                 bool
	checkingFocus          bool
}

// Physical capacity, not logical activity: an uncertain native cancel/detach
// keeps this one slot and inert callbacks until delivery or document teardown.
var retainedSettlement *retainedSettlementJob
var retainedSettlementCallbacks int

func retainedSettlementFinite(value float64) bool {
	return !math.IsNaN(value) && !math.IsInf(value, 0) && math.Abs(value) <= 1<<31
}
func captureRetainedSettlement(view *retainedView) (result *retainedSettlementSnapshot) {
	defer func() {
		if recover() != nil {
			result = nil
		}
	}()
	if !retainedRoutingEnabled() || moduleMode || view == nil || !view.valid || view.activityErr != nil || view != retainedCurrent || len(view.scroll) == 0 || len(view.scroll) > retainedSettlementNodes || len(currentURL) > 8192 {
		return nil
	}
	focus := view.focus
	if !focus.Truthy() || !doc.Get("activeElement").Equal(focus) {
		return nil
	}
	state := win.Get("history").Get("state")
	if _, ok := trackedHistory(state, currentURL); !ok {
		return nil
	}
	key := state.Get("nextgoEntry")
	if key.Type() != js.TypeString || len(key.String()) != 32 {
		return nil
	}
	result = &retainedSettlementSnapshot{view: view, app: doc.Call("getElementById", "nextgo-app"), focus: focus, intent: navigationID, epoch: cacheEpoch, privacy: mergePrivacy, url: currentURL, entry: key.String()}
	start, end, direction := focus.Get("selectionStart"), focus.Get("selectionEnd"), focus.Get("selectionDirection")
	if start.Type() == js.TypeNumber && end.Type() == js.TypeNumber && direction.Type() == js.TypeString {
		if !retainedSettlementFinite(start.Float()) || !retainedSettlementFinite(end.Float()) || start.Float() < 0 || end.Float() < 0 {
			return nil
		}
		result.selection = true
		result.start = start.Int()
		result.end = end.Int()
		result.direction = direction.String()
		if len(result.direction) > 16 {
			return nil
		}
	}
	for _, position := range view.scroll {
		if !retainedSettlementFinite(position.x) || !retainedSettlementFinite(position.y) {
			return nil
		}
		result.positions = append(result.positions, position)
	}
	if !retainedSettlementCurrent(result) {
		return nil
	}
	return result
}

// Pure Go authority checks also reject an in-place revoked snapshot alias.
func retainedSettlementFence(snapshot *retainedSettlementSnapshot) bool {
	if snapshot == nil || snapshot.view == nil || len(snapshot.positions) == 0 || len(snapshot.positions) > retainedSettlementNodes || moduleMode || draftFrozen || actionViewFrozen || refreshFrozen() {
		return false
	}
	if snapshot.intent != navigationID || snapshot.epoch != cacheEpoch || snapshot.privacy != mergePrivacy || snapshot.url != currentURL {
		return false
	}
	return snapshot.view == retainedCurrent && snapshot.view.valid && snapshot.view.activityErr == nil && retainedViews[snapshot.view.info.ID] == snapshot.view && retainedOwners.active == snapshot.view.info.ID && retainedOwners.aliases[snapshot.entry] == snapshot.view.info.ID
}
func retainedSettlementCurrent(snapshot *retainedSettlementSnapshot) (current bool) {
	defer func() {
		if recover() != nil {
			current = false
		}
	}()
	if !retainedSettlementFence(snapshot) || !retainedRoutingEnabled() || !retainedSettlementFence(snapshot) {
		return false
	}
	// One DOM operation per boundary; never continue reading private presentation
	// fields after a getter/call reentrantly clears the shared snapshot object.
	get := func(target js.Value, name string) (js.Value, bool) {
		if !retainedSettlementFence(snapshot) {
			return js.Undefined(), false
		}
		value := target.Get(name)
		return value, retainedSettlementFence(snapshot)
	}
	call := func(target js.Value, name string, args ...any) (js.Value, bool) {
		if !retainedSettlementFence(snapshot) {
			return js.Undefined(), false
		}
		value := target.Call(name, args...)
		return value, retainedSettlementFence(snapshot)
	}
	visibility, ok := get(doc, "visibilityState")
	if !ok || visibility.Type() != js.TypeString || visibility.String() != "visible" {
		return false
	}
	app, ok := call(doc, "getElementById", "nextgo-app")
	if !ok || !app.Equal(snapshot.app) {
		return false
	}
	connected, ok := get(snapshot.app, "isConnected")
	if !ok || connected.Type() != js.TypeBoolean || !connected.Bool() {
		return false
	}
	hidden, ok := get(snapshot.app, "hidden")
	if !ok || hidden.Type() != js.TypeBoolean || hidden.Bool() {
		return false
	}
	inert, ok := call(snapshot.app, "hasAttribute", "inert")
	if !ok || inert.Bool() {
		return false
	}
	connected, ok = get(snapshot.view.root, "isConnected")
	if !ok || connected.Type() != js.TypeBoolean || !connected.Bool() {
		return false
	}
	hidden, ok = get(snapshot.view.root, "hidden")
	if !ok || hidden.Type() != js.TypeBoolean || hidden.Bool() {
		return false
	}
	inert, ok = call(snapshot.view.root, "hasAttribute", "inert")
	if !ok || inert.Bool() {
		return false
	}
	active, ok := call(snapshot.view.root, "hasAttribute", "data-nextgo-view-active")
	if !ok || !active.Bool() {
		return false
	}
	parent, ok := get(snapshot.view.root, "parentNode")
	if !ok || !parent.Equal(snapshot.app) {
		return false
	}
	connected, ok = get(snapshot.focus, "isConnected")
	if !ok || connected.Type() != js.TypeBoolean || !connected.Bool() {
		return false
	}
	contained, ok := call(snapshot.view.root, "contains", snapshot.focus)
	if !ok || !contained.Bool() {
		return false
	}
	focused, ok := get(doc, "activeElement")
	if !ok || !focused.Equal(snapshot.focus) {
		return false
	}
	if snapshot.selection {
		start, ok := get(snapshot.focus, "selectionStart")
		if !ok || start.Type() != js.TypeNumber || !retainedSettlementFinite(start.Float()) || start.Int() != snapshot.start {
			return false
		}
		end, ok := get(snapshot.focus, "selectionEnd")
		if !ok || end.Type() != js.TypeNumber || !retainedSettlementFinite(end.Float()) || end.Int() != snapshot.end {
			return false
		}
		direction, ok := get(snapshot.focus, "selectionDirection")
		if !ok || direction.Type() != js.TypeString || direction.String() != snapshot.direction {
			return false
		}
	}
	history, ok := get(win, "history")
	if !ok {
		return false
	}
	state, ok := get(history, "state")
	if !ok {
		return false
	}
	if _, owned := trackedHistory(state, snapshot.url); !owned || !retainedSettlementFence(snapshot) {
		return false
	}
	key, ok := get(state, "nextgoEntry")
	if !ok || key.Type() != js.TypeString || key.String() != snapshot.entry {
		return false
	}
	location, ok := get(win, "location")
	if !ok {
		return false
	}
	href, ok := get(location, "href")
	return ok && href.Type() == js.TypeString && href.String() == snapshot.url && retainedSettlementFence(snapshot)
}
func (snapshot *retainedSettlementSnapshot) clear() {
	if snapshot == nil {
		return
	}
	// Clear backing elements too: an in-flight/caller slice alias must not keep
	// the entire saved node set after logical revocation. A single current native
	// operation may necessarily retain its one receiver until that call returns.
	for i := range snapshot.positions {
		snapshot.positions[i] = retainedScroll{node: js.Undefined()}
	}
	snapshot.positions = nil
	snapshot.view = nil
	snapshot.app = js.Undefined()
	snapshot.focus = js.Undefined()
	snapshot.url = ""
	snapshot.entry = ""
	snapshot.direction = ""
	snapshot.selection = false
	snapshot.start = 0
	snapshot.end = 0
	snapshot.intent = 0
	snapshot.epoch = 0
	snapshot.privacy = 0
}

func retainedSettlementBrowser() (platform retainedSettlementPlatform, ok bool) {
	defer func() {
		if recover() != nil {
			ok = false
		}
	}()
	platform = retainedSettlementPlatform{window: win, document: doc, request: win.Get("requestAnimationFrame"), cancel: win.Get("cancelAnimationFrame"), set: win.Get("setTimeout"), clear: win.Get("clearTimeout"), docAdd: doc.Get("addEventListener"), docRemove: doc.Get("removeEventListener"), winAdd: win.Get("addEventListener"), winRemove: win.Get("removeEventListener")}
	for _, fn := range []js.Value{platform.request, platform.cancel, platform.set, platform.clear, platform.docAdd, platform.docRemove, platform.winAdd, platform.winRemove} {
		if fn.Type() != js.TypeFunction {
			return platform, false
		}
	}
	return platform, true
}
func retainedSettlementCall(fn, receiver js.Value, args ...any) (value js.Value, ok bool) {
	defer func() {
		if recover() != nil {
			ok = false
		}
	}()
	supplied := make([]any, 0, len(args)+1)
	supplied = append(supplied, receiver)
	supplied = append(supplied, args...)
	value = fn.Call("call", supplied...)
	return value, true
}
func retainedSettlementHandle(value js.Value) bool {
	if value.Type() != js.TypeNumber {
		return false
	}
	number := value.Float()
	return number >= 0 && !math.IsNaN(number) && !math.IsInf(number, 0) && math.Trunc(number) == number && number <= 1<<53-1
}

func scheduleRetainedSettlement(snapshot *retainedSettlementSnapshot) {
	if retainedSettlement != nil || !retainedSettlementCurrent(snapshot) {
		return
	}
	platform, ok := retainedSettlementBrowser()
	if !ok {
		return
	}
	lease, err := beginRefreshActivity()
	if err != nil {
		return
	}
	job := &retainedSettlementJob{snapshot: snapshot, platform: platform, lease: lease, setup: 1}
	retainedSettlement = job // Register ownership before any browser callback exists.
	defer func() {
		if recover() != nil {
			job.stop()
		}
		job.setup--
		job.retire()
	}()
	job.guard = job.callback(func(args []js.Value) { job.intent(args) })
	job.deadline = job.callback(func([]js.Value) { job.deadline.mayOwn = false; job.deadline.delivered = true; job.stop() })
	for _, event := range []string{"pointerdown", "wheel", "touchstart", "keydown", "beforeinput", "focusin", "visibilitychange"} {
		job.listeners = append(job.listeners, retainedSettlementListener{target: platform.document, add: platform.docAdd, remove: platform.docRemove, event: event})
	}
	job.listeners = append(job.listeners, retainedSettlementListener{target: platform.window, add: platform.winAdd, remove: platform.winRemove, event: "pagehide"})
	for i := range job.listeners {
		if job.closed {
			return
		}
		listener := &job.listeners[i]
		listener.mayOwn = true
		listener.registering = true
		_, ok = retainedSettlementCall(listener.add, listener.target, listener.event, job.guard.fn, map[string]any{"capture": true, "passive": true})
		listener.registering = false
		if !ok || job.closed {
			job.stop()
			return
		}
	}
	if !job.valid() {
		job.stop()
		return
	}
	timer := job.deadline
	timer.mayOwn = true
	timer.registering = true
	handle, ok := retainedSettlementCall(platform.set, platform.window, timer.fn, retainedSettlementDeadlineMS)
	timer.registering = false
	if !timer.delivered && ok && retainedSettlementHandle(handle) {
		timer.handle = handle
		timer.known = true
	}
	if !ok || !timer.delivered && !timer.known || job.closed {
		job.stop()
		return
	}
	job.requestFrame(0)
}
func (job *retainedSettlementJob) callback(work func([]js.Value)) *retainedSettlementCallback {
	callback := &retainedSettlementCallback{}
	callback.fn = js.FuncOf(func(_ js.Value, args []js.Value) any {
		callback.running++
		defer func() {
			if recover() != nil {
				job.stop()
			}
			callback.running--
			job.retire()
		}()
		work(args)
		return nil
	})
	callback.made = true
	retainedSettlementCallbacks++
	return callback
}
func (job *retainedSettlementJob) valid() bool {
	snapshot := job.snapshot
	return !job.closed && retainedSettlement == job && retainedSettlementCurrent(snapshot) && !job.closed && job.snapshot == snapshot
}
func (job *retainedSettlementJob) requestFrame(index int) {
	if !job.valid() || index < 0 || index >= len(job.frames) {
		job.stop()
		return
	}
	ticket := job.callback(func([]js.Value) { job.frame(index) })
	job.frames[index] = ticket
	ticket.mayOwn = true
	ticket.registering = true
	handle, ok := retainedSettlementCall(job.platform.request, job.platform.window, ticket.fn)
	ticket.registering = false
	// Synchronous delivery/cancellation during registration must not resurrect a
	// delivered ticket or release a Func before the registering API has returned.
	if !ticket.delivered && ok && retainedSettlementHandle(handle) {
		ticket.handle = handle
		ticket.known = true
	}
	if !ok || !ticket.delivered && !ticket.known || job.closed {
		job.stop()
	}
	job.retire()
}
func (job *retainedSettlementJob) frame(index int) {
	ticket := job.frames[index]
	if ticket == nil {
		return
	}
	if ticket.delivered {
		return
	}
	ticket.delivered = true
	ticket.mayOwn = false
	if job.closed {
		return
	}
	if index != job.completedFrames || !job.valid() {
		job.stop()
		return
	}
	job.completedFrames++
	if index == 0 {
		job.requestFrame(1)
		return
	}
	job.reconcile()
	job.stop()
}

// An event for the exact already-focused control does not by itself revoke the
// saved presentation. Trust alone does not distinguish native restoration from
// user focus; actual trusted input and every existing authority fence still win.
func (job *retainedSettlementJob) ownsFocusEvent(event js.Value) bool {
	// Validity/target getters can reenter the listener. A nested focus check must
	// revoke rather than recursively revalidate or renew the snapshot.
	if job.checkingFocus {
		return false
	}
	job.checkingFocus = true
	defer func() { job.checkingFocus = false }()
	snapshot := job.snapshot
	if snapshot == nil || !job.valid() {
		return false
	}
	target := event.Get("target")
	if !job.valid() || job.snapshot != snapshot {
		return false
	}
	return target.Equal(snapshot.focus) && job.valid()
}
func (job *retainedSettlementJob) intent(args []js.Value) {
	if job.closed || len(args) == 0 {
		return
	}
	event := args[0]
	kind := event.Get("type").String()
	switch kind {
	case "focusin":
		if !job.ownsFocusEvent(event) {
			job.stop()
		}
	case "pagehide":
		job.stop()
	case "visibilitychange":
		if doc.Get("visibilityState").String() != "visible" {
			job.stop()
		}
	case "pointerdown", "wheel", "touchstart", "keydown", "beforeinput":
		if trusted := event.Get("isTrusted"); trusted.Type() == js.TypeBoolean && trusted.Bool() {
			job.stop()
		}
	}
}
func (job *retainedSettlementJob) targetCurrent(node js.Value) bool {
	if !job.valid() {
		return false
	}
	connected := node.Get("isConnected")
	if !job.valid() || connected.Type() != js.TypeBoolean || !connected.Bool() {
		return false
	}
	contained := job.snapshot.view.root.Call("contains", node)
	return job.valid() && contained.Type() == js.TypeBoolean && contained.Bool()
}
func (job *retainedSettlementJob) reconcile() {
	for index := 0; ; index++ {
		if !job.valid() {
			return
		}
		if index >= len(job.snapshot.positions) {
			return
		}
		// Keep one operation's receiver/coordinates, not a whole view/slice copy,
		// across native boundaries. Stop as soon as either job OR target is revoked.
		position := job.snapshot.positions[index]
		if !job.targetCurrent(position.node) {
			job.stop()
			return
		}
		x := position.node.Get("scrollLeft")
		if !job.targetCurrent(position.node) {
			job.stop()
			return
		}
		if x.Type() != js.TypeNumber || !retainedSettlementFinite(x.Float()) {
			job.stop()
			return
		}
		if x.Float() != position.x {
			if !job.targetCurrent(position.node) {
				job.stop()
				return
			}
			position.node.Set("scrollLeft", position.x)
			if !job.targetCurrent(position.node) {
				job.stop()
				return
			}
		}
		// Read Y after X's setter: it may already have changed Y or ownership.
		y := position.node.Get("scrollTop")
		if !job.targetCurrent(position.node) {
			job.stop()
			return
		}
		if y.Type() != js.TypeNumber || !retainedSettlementFinite(y.Float()) {
			job.stop()
			return
		}
		if y.Float() != position.y {
			if !job.targetCurrent(position.node) {
				job.stop()
				return
			}
			position.node.Set("scrollTop", position.y)
			if !job.targetCurrent(position.node) {
				job.stop()
				return
			}
		}
	}
}

func (job *retainedSettlementJob) cancelTicket(ticket *retainedSettlementCallback, cancel js.Value) {
	if ticket == nil || !ticket.mayOwn || ticket.registering || ticket.canceling || !ticket.known || ticket.cancelAttempted {
		return
	}
	ticket.cancelAttempted = true
	ticket.canceling = true
	_, ok := retainedSettlementCall(cancel, job.platform.window, ticket.handle)
	ticket.canceling = false
	if ok || ticket.delivered {
		ticket.mayOwn = false
	}
}
func (job *retainedSettlementJob) stop() {
	// Drop every private presentation reference at logical revocation, even if a
	// hostile/failed native cancel leaves one inert physical ticket quarantined.
	job.closed = true
	if snapshot := job.snapshot; snapshot != nil {
		snapshot.clear()
		job.snapshot = nil
	}
	for _, ticket := range job.frames {
		job.cancelTicket(ticket, job.platform.cancel)
	}
	job.cancelTicket(job.deadline, job.platform.clear)
	for i := range job.listeners {
		listener := &job.listeners[i]
		if !listener.mayOwn || listener.registering || listener.removing || listener.removeAttempted {
			continue
		}
		listener.removeAttempted = true
		listener.removing = true
		_, ok := retainedSettlementCall(listener.remove, listener.target, listener.event, job.guard.fn, true)
		listener.removing = false
		if ok {
			listener.mayOwn = false
		}
	}
	job.retire()
}
func (job *retainedSettlementJob) release(callback *retainedSettlementCallback, owned bool) {
	if callback == nil || !callback.made || callback.released || owned || callback.running != 0 || callback.registering || callback.canceling || job.setup != 0 {
		return
	}
	callback.released = true
	callback.fn.Release()
	retainedSettlementCallbacks--
}
func (job *retainedSettlementJob) retire() {
	for _, ticket := range job.frames {
		if ticket != nil {
			job.release(ticket, ticket.mayOwn)
		}
	}
	if job.deadline != nil {
		job.release(job.deadline, job.deadline.mayOwn)
	}
	attached := false
	for _, listener := range job.listeners {
		attached = attached || listener.mayOwn || listener.registering || listener.removing
	}
	job.release(job.guard, attached)
	if !job.closed || job.setup != 0 {
		return
	}
	for _, ticket := range []*retainedSettlementCallback{job.frames[0], job.frames[1], job.deadline, job.guard} {
		if ticket != nil && ticket.made && !ticket.released {
			return
		}
	}
	if retainedSettlement == job {
		retainedSettlement = nil
	}
	if job.lease != nil {
		job.lease.Done()
		job.lease = nil
	}
}
func cancelRetainedSettlement() {
	if job := retainedSettlement; job != nil {
		job.stop()
	}
}
func cancelRetainedSettlementFor(view *retainedView) {
	if job := retainedSettlement; job != nil && job.snapshot != nil && job.snapshot.view == view {
		job.stop()
	}
}
func cancelRetainedSettlementWithin(region js.Value) {
	job := retainedSettlement
	if job == nil || job.snapshot == nil {
		return
	}
	defer func() {
		if recover() != nil {
			job.stop()
		}
	}()
	root := job.snapshot.view.root
	if root.Equal(region) || root.Call("contains", region).Bool() || region.Call("contains", root).Bool() {
		job.stop()
	}
}
