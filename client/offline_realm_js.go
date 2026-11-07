//go:build js && wasm

package client

import (
	"context"
	"strconv"
	"syscall/js"
	"time"

	"github.com/Suhaibinator/Next.go/offline"
)

// A logical cancellation cannot discard the global which owns an outstanding
// native job's promise. This one slot survives controller replacement until
// listeners, promises and the exact owned frame have actually been retired,
// and through the final parent read/status API return after child retirement.
var offlineRealmInUse *offlineProofRealm

type offlineRealmParent struct {
	c                                       *offlineController
	descriptor                              offline.ClientDescriptor
	document, window, container, controller js.Value
	origin                                  string
}

func (p offlineRealmParent) fence(ctx context.Context) error {
	if ctx.Err() != nil {
		return ErrOfflineUnknown
	}
	if documentOffline != p.c || p.c.descriptor != p.descriptor || !doc.Equal(p.document) || !win.Equal(p.window) ||
		p.window.Get("location").Get("origin").String() != p.origin ||
		!p.window.Get("navigator").Get("serviceWorker").Equal(p.container) {
		return ErrOfflineUnknown
	}
	current, err := p.c.registrationController(p.container)
	if err != nil {
		return err
	}
	if !current.Truthy() || !current.Equal(p.controller) || !p.c.ownedWorker(current) {
		return p.c.ownershipConflict("parent-controller-changed")
	}
	if ctx.Err() != nil {
		return ErrOfflineUnknown
	}
	return nil
}

type offlineRealmListener struct {
	target    js.Value
	event     string
	callback  js.Func
	installed bool
}

type offlineProofRealm struct {
	parent                                               offlineRealmParent
	frame, host, window, document, container, controller js.Value
	listeners                                            []*offlineRealmListener
	ports                                                []js.Value
	pending                                              int
	retired                                              bool
	activity                                             *refreshActivityLease
	helperURL                                            string
	navigationPending, navigationFailed                  bool
	navigationCallbacks                                  int
}

type offlineFreshUpdateResult struct {
	status  offlineReply
	waiting string
	parent  offlineRealmParent // Only live-parent objects escape frame retirement.
}

func (c *offlineController) updateInFreshRealm(ctx context.Context, container, controller js.Value) (result offlineFreshUpdateResult, err error) {
	if moduleMode {
		return result, ErrOfflineUnsupported
	}
	if offlineRealmInUse != nil {
		return result, ErrOfflineBusy
	}
	p := offlineRealmParent{c: c, descriptor: c.descriptor, document: doc, window: win, container: container, controller: controller, origin: win.Get("location").Get("origin").String()}
	if err = p.fence(ctx); err != nil {
		return result, err
	}
	// Existing minimal/documentless callers fail closed without allocating a realm.
	if doc.Get("createElement").Type() != js.TypeFunction || !doc.Get("documentElement").Truthy() {
		return result, c.ownershipConflict("registration-proof-empty-lookup")
	}
	helperURL, e := offline.RegistrationHelperURL(p.descriptor.WorkerURL)
	if e != nil {
		return result, ErrOfflineUnknown
	}
	activity, e := beginRefreshActivity()
	if e != nil {
		return result, e
	}
	r := &offlineProofRealm{parent: p, activity: activity, helperURL: helperURL}
	offlineRealmInUse = r
	defer func() {
		if recover() != nil {
			err = ErrOfflineUnknown
		}
		if closeErr := r.close(); closeErr != nil {
			err = closeErr
		}
	}()
	if err = r.open(ctx); err != nil {
		return result, err
	}
	// Child registrations and workers are confined to this call, before close.
	waiting, e := r.update(ctx)
	if e != nil {
		return result, e
	}
	if err = r.retireFrame(); err != nil {
		return result, err
	}
	// Frame removal is not a claim that a native client ID has disappeared. The
	// parent's real inventory/controller and worker status remain authoritative.
	if err = p.fence(ctx); err != nil {
		return result, err
	}
	// The child globals are gone, but replacement of documentOffline must not
	// admit another operation while this parent native Promise is still live.
	inventory, e := func() (js.Value, error) {
		r.pending++
		defer func() { r.pending-- }()
		return c.promise(ctx, p.container.Call("getRegistrations"))
	}()
	if e != nil {
		return result, e
	}
	checked, e := c.inspectRegistrationInventory(inventory)
	if e != nil {
		return result, e
	}
	if !checked.exact.Truthy() {
		return result, c.ownershipConflict("registration-proof-missing")
	}
	if err = p.fence(ctx); err != nil {
		return result, err
	}
	status, e := c.message(ctx, p.controller, "status", p.descriptor.RuntimeID, p.descriptor.ConfigurationID)
	if e != nil {
		return result, e
	}
	if err = p.fence(ctx); err != nil {
		return result, err
	}
	if !status.Supported {
		return result, ErrOfflineUnsupported
	}
	return offlineFreshUpdateResult{status: status, waiting: waiting, parent: p}, nil
}

func (r *offlineProofRealm) listen(target js.Value, event string, notify chan<- bool, value bool) *offlineRealmListener {
	l := &offlineRealmListener{target: target, event: event}
	l.callback = js.FuncOf(func(js.Value, []js.Value) any {
		select {
		case notify <- value:
		default:
		}
		return nil
	})
	r.listeners = append(r.listeners, l)
	// Mark before calling: if a host throws after registration we still retain
	// and attempt removal instead of releasing a possibly installed callback.
	l.installed = true
	target.Call("addEventListener", event, l.callback)
	return l
}

func (r *offlineProofRealm) removeListeners() (err error) {
	defer func() {
		if recover() != nil {
			err = ErrOfflineUnknown
		}
	}()
	for len(r.listeners) > 0 {
		l := r.listeners[0]
		if l.installed {
			l.target.Call("removeEventListener", l.event, l.callback)
			l.installed = false
		}
		l.callback.Release()
		l.target = js.Undefined()
		r.listeners[0] = nil
		r.listeners = r.listeners[1:]
	}
	return nil
}

// A single immutable HTTP src is set before first connection. HTML's special
// initial about:blank insertion load therefore cannot settle this attempt.
// Only the native exact-target load is a high-level navigation terminal event;
// it says nothing about successful content, socket closure or client-ID removal.
func (r *offlineProofRealm) listenNavigation(event string, completed chan<- struct{}) {
	l := &offlineRealmListener{target: r.frame, event: event}
	l.callback = js.FuncOf(func(_ js.Value, args []js.Value) any {
		r.navigationCallbacks++
		defer func() {
			r.navigationCallbacks--
			if !r.navigationPending && r.navigationCallbacks == 0 {
				select {
				case completed <- struct{}{}:
				default:
				}
			}
		}()
		// Synthetic events and another target cannot retire native navigation.
		// A malformed host event leaves the one charge quarantined.
		defer func() { _ = recover() }()
		if !r.navigationPending || len(args) != 1 || args[0].Type() != js.TypeObject {
			return nil
		}
		e := args[0]
		if e.Get("isTrusted").Type() != js.TypeBoolean || !e.Get("isTrusted").Bool() || !e.Get("target").Equal(r.frame) {
			return nil
		}
		if event == "load" {
			r.navigationPending = false
		} else {
			// Iframe error alone has no proved terminal lifecycle guarantee.
			// Preserve the failure and await the corresponding native load.
			r.navigationFailed = true
		}
		return nil
	})
	r.listeners = append(r.listeners, l)
	l.installed = true
	r.frame.Call("addEventListener", event, l.callback)
}

// Cancellation/deadline revoke authority but cannot abandon navigation work.
// Missing native load retains one frame/global lease indefinitely for outer
// containment; no second attempt, src retarget or remove-as-abort is allowed.
// The channel argument is the real timer in production and a deterministic
// deadline seam in source tests, not an alternative browser completion source.
func (r *offlineProofRealm) awaitNavigation(ctx context.Context, completed <-chan struct{}, expired <-chan time.Time) error {
	canceled := ctx.Done()
	var logical error
	for r.navigationPending || r.navigationCallbacks != 0 {
		select {
		case <-completed:
		case <-canceled:
			logical = ErrOfflineUnknown
			canceled = nil
		case <-expired:
			if logical == nil {
				logical = r.parent.c.ownershipConflict("registration-proof-empty-lookup")
			}
			expired = nil
		}
	}
	if ctx.Err() != nil {
		return ErrOfflineUnknown
	}
	if logical != nil {
		return logical
	}
	if r.navigationFailed {
		return r.parent.c.ownershipConflict("registration-proof-empty-lookup")
	}
	return nil
}

func (r *offlineProofRealm) open(ctx context.Context) error {
	if err := r.parent.fence(ctx); err != nil {
		return err
	}
	r.host = r.parent.document.Get("documentElement")
	r.frame = r.parent.document.Call("createElement", "iframe")
	r.frame.Set("hidden", true)
	r.frame.Set("inert", true)
	r.frame.Set("tabIndex", -1)
	r.frame.Call("setAttribute", "aria-hidden", "true")
	r.frame.Set("loading", "eager")
	r.frame.Set("referrerPolicy", "no-referrer")
	r.frame.Set("src", r.helperURL)
	completed := make(chan struct{}, 1)
	r.listenNavigation("load", completed)
	r.listenNavigation("error", completed)
	// Setup may call host code. Do not begin a new HTTP navigation after a
	// known cancellation/controller change or with a reused/retargeted node.
	if err := r.parent.fence(ctx); err != nil {
		return err
	}
	if !r.frame.Get("ownerDocument").Equal(r.parent.document) || r.frame.Get("isConnected").Bool() || r.frame.Get("parentNode").Truthy() || r.frame.Get("src").String() != r.helperURL {
		return ErrOfflineUnknown
	}
	// Final detached-node validation itself can call host getters. Recheck the
	// request/parent after those calls, immediately before starting native work.
	if err := r.parent.fence(ctx); err != nil {
		return err
	}
	// Mark before the native append: a host throw after possible navigation
	// must not be mistaken for a never-started or physically retired request.
	r.navigationPending = true
	r.host.Call("appendChild", r.frame)
	timer := time.NewTimer(5 * time.Second)
	defer timer.Stop()
	if err := r.awaitNavigation(ctx, completed, timer.C); err != nil {
		return err
	}
	if err := r.removeListeners(); err != nil {
		return err
	}
	if err := r.parent.fence(ctx); err != nil {
		return err
	}
	r.window = r.frame.Get("contentWindow")
	r.document = r.frame.Get("contentDocument")
	if !r.window.Truthy() || !r.document.Truthy() {
		return r.parent.c.ownershipConflict("registration-proof-empty-lookup")
	}
	r.container = r.window.Get("navigator").Get("serviceWorker")
	if !r.container.Truthy() || r.container.Get("getRegistration").Type() != js.TypeFunction || r.container.Get("getRegistrations").Type() != js.TypeFunction {
		return r.parent.c.ownershipConflict("registration-proof-empty-lookup")
	}
	r.controller = r.container.Get("controller")
	// Child control is not registry mutation authority. The captured native
	// null/owned identity must stay stable; parent control remains mandatory.
	if !r.controller.IsNull() && !r.parent.c.ownedWorker(r.controller) {
		return r.parent.c.ownershipConflict("child-controller-invalid")
	}
	return r.fence(ctx)
}

func (r *offlineProofRealm) fence(ctx context.Context) error {
	if err := r.parent.fence(ctx); err != nil {
		return err
	}
	if offlineRealmInUse != r || !r.frame.Truthy() || !r.frame.Get("isConnected").Bool() ||
		!r.frame.Get("ownerDocument").Equal(r.parent.document) || !r.frame.Get("parentNode").Equal(r.host) ||
		!r.frame.Get("contentWindow").Equal(r.window) || !r.frame.Get("contentDocument").Equal(r.document) ||
		r.window.Get("closed").Bool() || r.window.Get("location").Get("href").String() != r.helperURL || r.frame.Get("src").String() != r.helperURL ||
		r.window.Get("origin").String() != r.parent.origin || r.document.Get("readyState").String() != "complete" ||
		!r.document.Get("documentElement").Truthy() || r.document.Get("documentElement").Call("getAttribute", "data-nextgo-offline-helper").String() != "1" ||
		!r.document.Get("head").Truthy() || r.document.Get("head").Get("childNodes").Length() != 0 ||
		!r.document.Get("body").Truthy() || r.document.Get("body").Get("childNodes").Length() != 0 ||
		!r.window.Get("navigator").Get("serviceWorker").Equal(r.container) {
		return ErrOfflineUnknown
	}
	current := r.container.Get("controller")
	if !current.IsNull() && !r.parent.c.ownedWorker(current) {
		return r.parent.c.ownershipConflict("child-controller-invalid")
	}
	if !current.Equal(r.controller) {
		return r.parent.c.ownershipConflict("child-controller-changed")
	}
	return r.parent.fence(ctx)
}

// The receiver belongs to the live child realm; c.promise joins the actual
// native settlement even after cancellation. No frame teardown runs in parallel.
func (r *offlineProofRealm) call(ctx context.Context, target js.Value, method string, args ...any) (value js.Value, err error) {
	if err = r.fence(ctx); err != nil {
		return js.Undefined(), err
	}
	promise := target.Call(method, args...)
	r.pending++
	defer func() { r.pending-- }()
	value, err = r.parent.c.promise(ctx, promise)
	if err == nil {
		err = r.fence(ctx)
	}
	return value, err
}

func (r *offlineProofRealm) parentInventory(ctx context.Context) error {
	if err := r.fence(ctx); err != nil {
		return err
	}
	value, err := r.call(ctx, r.parent.container, "getRegistrations")
	if err != nil {
		return err
	}
	inventory, err := r.parent.c.inspectRegistrationInventory(value)
	if err != nil {
		return err
	}
	if !inventory.exact.Truthy() {
		return r.parent.c.ownershipConflict("registration-proof-missing")
	}
	return r.fence(ctx)
}

func (r *offlineProofRealm) proof(ctx context.Context, candidate js.Value) error {
	if err := r.parentInventory(ctx); err != nil {
		return err
	}
	// The fresh final inventory is the LAST awaited read before dispatch.
	value, err := r.call(ctx, r.container, "getRegistrations")
	if err != nil {
		return err
	}
	inventory, err := r.parent.c.inspectRegistrationInventory(value)
	if err != nil {
		return err
	}
	if !inventory.exact.Truthy() || !candidate.Equal(inventory.exact) {
		return r.parent.c.ownershipConflict("registration-proof-changed")
	}
	if err = r.parent.c.requireRegistrationProof(candidate, "registration-proof-empty-final-inventory"); err != nil {
		return err
	}
	return r.fence(ctx)
}

func (r *offlineProofRealm) update(ctx context.Context) (string, error) {
	c := r.parent.c
	value, err := r.call(ctx, r.container, "getRegistrations")
	if err != nil {
		return "", err
	}
	inventory, err := c.inspectRegistrationInventory(value)
	if err != nil {
		return "", err
	}
	if !inventory.exact.Truthy() {
		return "", c.ownershipConflict("registration-proof-missing")
	}
	candidate, err := r.call(ctx, r.container, "getRegistration", c.descriptor.Scope)
	if err != nil {
		return "", err
	}
	if err = c.requireRegistrationProof(candidate, "registration-proof-empty-lookup"); err != nil {
		return "", err
	}
	if err = r.proof(ctx, candidate); err != nil {
		return "", err
	}
	if _, err = r.call(ctx, candidate, "update"); err != nil {
		return "", err
	}
	if err = r.waitInstallation(ctx, candidate); err != nil {
		return "", err
	}
	if err = r.proof(ctx, candidate); err != nil {
		return "", err
	}
	waiting := candidate.Get("waiting")
	if !waiting.Truthy() {
		return "", nil
	}
	reply, err := r.waitingStatus(ctx, waiting)
	if err != nil {
		return "", err
	}
	if err = r.fence(ctx); err != nil {
		return "", err
	}
	if err = c.requireRegistrationProof(candidate, "registration-proof-empty-message"); err != nil {
		return "", err
	}
	if !candidate.Get("waiting").Equal(waiting) {
		return "", c.ownershipConflict("waiting-worker-changed")
	}
	return reply.Generation, nil
}

func (r *offlineProofRealm) waitInstallation(ctx context.Context, candidate js.Value) (err error) {
	c := r.parent.c
	worker := candidate.Get("installing")
	if !worker.Truthy() {
		return c.validateRegistration(candidate)
	}
	if !c.ownedWorker(worker) {
		return c.ownershipConflict("worker-state-target")
	}
	changed := make(chan bool, 1)
	r.listen(worker, "statechange", changed, true)
	defer func() {
		if e := r.removeListeners(); e != nil {
			err = e
		}
	}()
	for {
		if err = r.fence(ctx); err != nil {
			return err
		}
		if err = c.requireRegistrationProof(candidate, "registration-proof-empty-installing"); err != nil {
			return err
		}
		state := worker.Get("state").String()
		if state == "installed" || state == "activated" {
			return nil
		}
		if state == "redundant" {
			return ErrOfflineProtocol
		}
		select {
		case <-changed:
		case <-ctx.Done():
			return ErrOfflineUnknown
		}
	}
}

// Only the fresh-child waiting receipt needs this checked cleanup path. The
// legacy parent status/control message implementation remains unchanged. Ports
// are created in the parent realm; no child-native Promise is introduced.
func (r *offlineProofRealm) waitingStatus(ctx context.Context, worker js.Value) (reply offlineReply, err error) {
	if err = r.fence(ctx); err != nil {
		return reply, err
	}
	c := r.parent.c
	if !c.ownedWorker(worker) {
		return reply, c.ownershipConflict("message-worker")
	}
	c.request++
	request := offlineRequest{Version: offlineMessageVersion, Command: "status", OwnerPolicyID: r.parent.descriptor.OwnerPolicyID, RuntimeID: r.parent.descriptor.RuntimeID, ConfigurationID: r.parent.descriptor.ConfigurationID, RequestID: "offline-" + strconv.FormatUint(c.request, 36)}
	channel := r.parent.window.Get("MessageChannel").New()
	port, other := channel.Get("port1"), channel.Get("port2")
	r.ports = append(r.ports, port, other)
	defer func() {
		if e := r.removeListeners(); e != nil {
			err = e
		}
		if e := r.closePorts(); e != nil {
			err = e
		}
	}()
	type result struct {
		reply offlineReply
		err   error
	}
	received := make(chan result, 1)
	l := &offlineRealmListener{target: port, event: "message"}
	l.callback = js.FuncOf(func(_ js.Value, args []js.Value) any {
		value := result{err: ErrOfflineProtocol}
		if len(args) == 1 {
			value.reply, value.err = decodeOfflineMessage(args[0].Get("data"), request)
		}
		select {
		case received <- value:
		default:
		}
		return nil
	})
	r.listeners = append(r.listeners, l)
	l.installed = true
	port.Call("addEventListener", "message", l.callback, map[string]any{"once": true})
	port.Call("start")
	worker.Call("postMessage", map[string]any{"version": request.Version, "command": request.Command, "ownerPolicyID": request.OwnerPolicyID, "runtimeID": request.RuntimeID, "configurationID": request.ConfigurationID, "requestID": request.RequestID}, []any{other})
	select {
	case value := <-received:
		return value.reply, value.err
	case <-ctx.Done():
		return reply, ErrOfflineUnknown
	}
}

func (r *offlineProofRealm) closePorts() (err error) {
	defer func() {
		if recover() != nil {
			err = ErrOfflineUnknown
		}
	}()
	for len(r.ports) > 0 {
		r.ports[0].Call("close")
		r.ports[0] = js.Undefined()
		r.ports = r.ports[1:]
	}
	return nil
}

// On a host failure, retain callbacks/global admission/activity instead of
// asserting cleanup. A later operation cannot create a second charged frame.
func (r *offlineProofRealm) retireFrame() (err error) {
	if r.retired {
		return nil
	}
	if offlineRealmInUse != r {
		return ErrOfflineUnknown
	}
	defer func() {
		if recover() != nil {
			err = ErrOfflineUnknown
		}
	}()
	if r.pending != 0 || r.navigationPending || r.navigationCallbacks != 0 {
		return ErrOfflineUnknown
	}
	if err = r.removeListeners(); err != nil {
		return err
	}
	if err = r.closePorts(); err != nil {
		return err
	}
	if r.frame.Truthy() {
		parent := r.frame.Get("parentNode")
		if parent.Truthy() {
			if !parent.Equal(r.host) || !r.frame.Get("ownerDocument").Equal(r.parent.document) {
				return ErrOfflineUnknown
			}
			parent.Call("removeChild", r.frame)
		}
		if r.frame.Get("isConnected").Bool() || r.frame.Get("parentNode").Truthy() {
			return ErrOfflineUnknown
		}
	}
	r.frame, r.host, r.window, r.document, r.container, r.controller = js.Undefined(), js.Undefined(), js.Undefined(), js.Undefined(), js.Undefined(), js.Undefined()
	r.retired = true
	return nil
}

// A retired child cannot grant fresh authority or retain native child values.
// Its parent-only admission/activity lease lasts through the final physical
// parent inventory Promise and legacy parent status API return. That legacy
// message helper's best-effort cleanup is not a new physical-port-join claim.
func (r *offlineProofRealm) close() (err error) {
	if offlineRealmInUse != r {
		return nil
	}
	defer func() {
		if recover() != nil {
			err = ErrOfflineUnknown
		}
	}()
	if r.pending != 0 || r.navigationPending || r.navigationCallbacks != 0 {
		return ErrOfflineUnknown
	}
	if err := r.retireFrame(); err != nil {
		return err
	}
	r.activity.Done()
	r.activity = nil
	offlineRealmInUse = nil
	return nil
}
