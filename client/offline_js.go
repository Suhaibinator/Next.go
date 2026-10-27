//go:build js && wasm

package client

import (
	"context"
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
	"syscall/js"
	"time"

	"github.com/Suhaibinator/Next.go/internal/offlinejson"
	"github.com/Suhaibinator/Next.go/offline"
)

type offlineObserver struct {
	scope    *Scope
	callback func(OfflineState)
}

type offlineController struct {
	descriptor        offline.ClientDescriptor
	state             OfflineState
	busy              bool
	request           uint64
	confirmedDisabled bool
	nativeMode        bool
	conflictReason    string
}

var documentOffline *offlineController
var offlineObservers = map[*offlineObserver]struct{}{}

// OfflineStatus returns the most recently confirmed worker state. A saved page
// always describes public snapshot data; ordinary network actions are never
// queued or replayed by these APIs. This first control API is eager-core only.
func OfflineStatus() OfflineState {
	if moduleMode {
		return OfflineState{Phase: "unavailable", LastError: ErrOfflineUnsupported.Error()}
	}
	if documentOffline == nil {
		return OfflineState{Phase: "unavailable"}
	}
	return documentOffline.state
}

// RefreshOfflineStatus performs one explicit serialized status operation for
// the owned active and waiting workers, as applicable, and publishes a fresh
// snapshot. Unlike OfflineStatus, this is asynchronous: call
// from Scope.Go, never a JS event callback. It shares the single-operation
// admission/cancellation rules and never registers, updates or repairs a worker.
// An unconfirmed result is not automatically retried.
func RefreshOfflineStatus(ctx context.Context) error { return offlineOperation(ctx, "status") }

// SubscribeOffline immediately delivers a snapshot, including on unconfigured
// or unsupported documents. The callback must return promptly. Its subscription
// is removed with scope cleanup. Lazy-module controls are explicitly unsupported.
func SubscribeOffline(scope *Scope, callback func(OfflineState)) error {
	if moduleMode {
		return ErrOfflineUnsupported
	}
	if scope == nil || !scope.active() || callback == nil {
		return fmt.Errorf("nextgo: offline subscription needs an active scope and callback")
	}
	if len(offlineObservers) >= 128 {
		return ErrOfflineBusy
	}
	observer := &offlineObserver{scope: scope, callback: callback}
	offlineObservers[observer] = struct{}{}
	scope.Cleanup(func() { delete(offlineObservers, observer); observer.callback = nil; observer.scope = nil })
	deliverOffline(observer, OfflineStatus())
	return nil
}

func deliverOffline(observer *offlineObserver, state OfflineState) {
	if _, present := offlineObservers[observer]; !present || observer.callback == nil || observer.scope == nil || !observer.scope.active() {
		return
	}
	defer func() {
		if recover() != nil {
			offlineQuiet(func() { report("offline status callback failed") })
		}
	}()
	observer.callback(state)
}
func (c *offlineController) notify() {
	mode := offlineNativeNavigation()
	if mode != c.nativeMode {
		c.nativeMode = mode
		if mode && currentURL != "" {
			cancelPendingNavigation()
			invalidateCache()
		}
		if !mode && viewportPrefetch != nil {
			viewportPrefetch.requestScan()
		}
	}
	c.state.Controlling = c.controlling()
	list := make([]*offlineObserver, 0, len(offlineObservers))
	for observer := range offlineObservers {
		list = append(list, observer)
	}
	for _, observer := range list {
		deliverOffline(observer, c.state)
	}
}

// EnableOffline explicitly requests saving only native-approved anonymous public
// pages. Call from Scope.Go or another goroutine, never a JS event callback.
func EnableOffline(ctx context.Context) error { return offlineOperation(ctx, "enable") }

// CheckOfflineUpdate asks the browser to check the owned registration. A waiting
// worker stays waiting: this API never calls skipWaiting or clients.claim, and
// never asks an old worker to install a new deployment under its old runtime.
func CheckOfflineUpdate(ctx context.Context) error { return offlineOperation(ctx, "update") }

// DisableOffline stops saved-document serving and unregisters the exact owner.
// Controlled pages can retain charged public payloads: CleanupPending reports
// that state. Later explicit reconciliation after those pages close is needed;
// unregistering is not a guaranteed background purge or private-data erase API.
func DisableOffline(ctx context.Context) error { return offlineOperation(ctx, "disable") }

func browserOfflineSupport() (supported bool) {
	defer func() {
		if recover() != nil {
			supported = false
		}
	}()
	secure := win.Get("isSecureContext")
	if secure.Type() != js.TypeBoolean || !secure.Bool() {
		return false
	}
	navigator := win.Get("navigator")
	if navigator.Type() != js.TypeObject || navigator.IsNull() {
		return false
	}
	sw, locks := navigator.Get("serviceWorker"), navigator.Get("locks")
	return sw.Type() == js.TypeObject && !sw.IsNull() && sw.Get("register").Type() == js.TypeFunction && sw.Get("getRegistrations").Type() == js.TypeFunction && locks.Type() == js.TypeObject && !locks.IsNull() && locks.Get("request").Type() == js.TypeFunction && win.Get("MessageChannel").Type() == js.TypeFunction
}

// initializeOfflineDocument runs only after normal deployment/configuration and
// document validation. Unconfigured pages perform no service-worker inspection.
func initializeOfflineDocument() (saved bool, err error) {
	if moduleMode {
		return false, ErrOfflineUnsupported
	}
	body := doc.Get("body")
	raw := body.Call("getAttribute", offline.ClientDescriptorAttribute)
	marker := body.Call("getAttribute", "data-nextgo-offline-saved")
	if raw.IsNull() && marker.IsNull() {
		return false, nil
	}
	saved = !marker.IsNull()
	c := &offlineController{state: OfflineState{Phase: "unavailable", SavedDocument: saved}}
	documentOffline = c
	defer func() {
		if failure := recover(); failure != nil {
			err = ErrOfflineProtocol
			c.state.LastError = err.Error()
			c.state.Phase = "error"
		}
	}()
	if raw.IsNull() || raw.Type() != js.TypeString || len(raw.String()) > offline.MaxClientDescriptorBytes || saved && marker.String() != "1" {
		return saved, ErrOfflineProtocol
	}
	var descriptor offline.ClientDescriptor
	if offlinejson.Decode([]byte(raw.String()), &descriptor) != nil || offline.ValidateClientDescriptor(descriptor, win.Get("location").Get("origin").String(), deployment.BasePath, body.Call("getAttribute", "data-nextgo-build").String(), documentConfiguration()) != nil {
		return saved, ErrOfflineProtocol
	}
	c.descriptor = descriptor
	c.state.Configured = true
	c.state.Scope = descriptor.Scope
	c.state.OwnerPolicyID = descriptor.OwnerPolicyID
	c.state.Supported = !routeManifest.Development && browserOfflineSupport()
	if !c.state.Supported {
		c.state.LastError = ErrOfflineUnsupported.Error()
		return saved, nil
	}
	c.state.Phase = "checking"
	go func() { _ = offlineOperation(context.Background(), "status") }()
	return saved, nil
}

func offlineOperation(parent context.Context, command string) (err error) {
	if moduleMode {
		return ErrOfflineUnsupported
	}
	c := documentOffline
	if c == nil || !c.state.Configured {
		return ErrOfflineUnavailable
	}
	if !c.state.Supported {
		return ErrOfflineUnsupported
	}
	if parent == nil {
		return fmt.Errorf("nextgo: offline operation needs a context")
	}
	if parent.Err() != nil {
		return context.Cause(parent)
	}
	if c.busy {
		return ErrOfflineBusy
	}
	c.busy = true
	c.conflictReason = ""
	phase := map[string]string{"status": "checking", "enable": "enabling", "update": "updating", "disable": "disabling"}[command]
	if phase == "" {
		c.busy = false
		return ErrOfflineProtocol
	}
	c.state.Phase = phase
	c.state.LastError = ""
	c.notify()
	defer func() {
		if failure := recover(); failure != nil {
			err = fmt.Errorf("nextgo: offline browser operation failed")
		}
		c.busy = false
		if err != nil {
			c.state.Phase = "error"
			c.state.LastError = err.Error()
			if err == ErrOfflineConflict && c.conflictReason != "" {
				c.state.LastError += " [" + c.conflictReason + "]"
			}
		}
		c.notify()
	}()
	limit := 120 * time.Second
	if command == "status" {
		limit = 5 * time.Second
	}
	ctx, cancel := context.WithTimeout(parent, limit)
	defer cancel()
	use := offlineRegistrationMutation
	if command == "status" {
		use = offlineRegistrationStatus
	}
	selection, err := c.registrationForOperation(ctx, use)
	if err != nil {
		return err
	}
	registration := selection.registration
	controllerOwned := c.controlling()
	if (command == "enable" || command == "disable" && !controllerOwned) && !registration.Truthy() {
		sw := win.Get("navigator").Get("serviceWorker")
		registration, err = c.promise(ctx, sw.Call("register", c.descriptor.WorkerURL, map[string]any{"scope": c.descriptor.Scope, "updateViaCache": "none", "type": "classic"}))
		if err != nil {
			return err
		}
		if err = c.validateRegistration(registration); err != nil {
			return err
		}
	}
	if command == "update" {
		if !registration.Truthy() {
			return ErrOfflineUnavailable
		}
		if selection.recovered {
			if err = c.recoveredRegistrationFence(ctx, selection, js.Undefined(), false); err != nil {
				return err
			}
		}
		if _, err = c.promise(ctx, registration.Call("update")); err != nil {
			return err
		}
		if err = c.waitInstallation(ctx, registration); err != nil {
			return err
		}
	}
	worker := selection.statusWorker
	if registration.Truthy() {
		worker = registration.Get("active")
		if !worker.Truthy() && (command == "enable" || command == "disable") {
			if err = c.waitActivation(ctx, registration); err != nil {
				return err
			}
			worker = registration.Get("active")
		}
	}
	if !worker.Truthy() {
		controller := win.Get("navigator").Get("serviceWorker").Get("controller")
		if c.ownedWorker(controller) {
			worker = controller
		}
	}
	if worker.Truthy() {
		status, e := c.message(ctx, worker, "status", c.descriptor.RuntimeID, c.descriptor.ConfigurationID)
		if e != nil {
			return e
		}
		c.apply(status)
		if !status.Supported {
			return ErrOfflineUnsupported
		}
		if command == "enable" || command == "disable" {
			if registration.Truthy() && registration.Get("active").Truthy() && !registration.Get("active").Equal(worker) {
				return c.ownershipConflict("active-worker-changed")
			}
			if command == "enable" {
				c.confirmedDisabled = false
				c.notify()
			}
			if selection.recovered {
				if err = c.recoveredRegistrationFence(ctx, selection, worker, true); err != nil {
					return err
				}
			}
			result, e := c.message(ctx, worker, command, status.RuntimeID, status.ConfigurationID)
			if e != nil {
				return e
			}
			c.apply(result)
			if result.Error != "" {
				return fmt.Errorf("nextgo: offline worker refused operation (%s)", result.Error)
			}
			if command == "enable" && (!result.Enabled || result.ActiveGeneration == "") {
				return ErrOfflineProtocol
			}
			if command == "disable" && result.Enabled {
				return ErrOfflineProtocol
			}
		}
	} else {
		c.state.Enabled = false
		c.confirmedDisabled = true
		c.state.ActiveGeneration = ""
		c.state.Phase = "disabled"
		if command == "enable" {
			return ErrOfflineUnknown
		}
	}
	if command == "disable" && registration.Truthy() {
		if selection.recovered {
			if err = c.recoveredRegistrationFence(ctx, selection, worker, true); err != nil {
				return err
			}
		}
		if _, err = c.promise(ctx, registration.Call("unregister")); err != nil {
			return err
		}
		c.state.Enabled = false
		if !c.state.CleanupPending {
			c.state.ActiveGeneration = ""
		}
		c.state.WaitingGeneration = ""
		c.state.Phase = "disabled"
	}
	if command != "disable" && registration.Truthy() {
		waiting := registration.Get("waiting")
		if waiting.Truthy() {
			result, e := c.message(ctx, waiting, "status", c.descriptor.RuntimeID, c.descriptor.ConfigurationID)
			if e != nil {
				return e
			}
			c.state.WaitingGeneration = result.Generation
			if c.state.WaitingGeneration != "" {
				c.state.Phase = "waiting"
			}
		} else {
			c.state.WaitingGeneration = ""
		}
	}
	return nil
}

// Registration/update promises cannot be aborted. Retain the single operation
// and its callbacks until physical settlement, even if its caller cancels. The
// worker separately retains its storage lease after a message port is closed.
func (c *offlineController) promise(ctx context.Context, promise js.Value) (js.Value, error) {
	controller := js.Global().Get("AbortController").New()
	value, err := awaitPhysicalBrowserPromise(ctx, promise, controller)
	if ctx.Err() != nil {
		return js.Undefined(), ErrOfflineUnknown
	}
	return value, err
}

// ownershipConflict records only a fixed diagnostic category. Keep returning
// the exact sentinel: ownership decisions and callers' error identity stay intact.
func (c *offlineController) ownershipConflict(reason string) error {
	c.conflictReason = reason
	return ErrOfflineConflict
}

func (c *offlineController) ownedWorker(worker js.Value) bool {
	return worker.Type() == js.TypeObject && !worker.IsNull() && worker.Get("scriptURL").Type() == js.TypeString && worker.Get("scriptURL").String() == c.descriptor.WorkerURL
}
func (c *offlineController) controlling() (owned bool) {
	defer func() {
		if recover() != nil {
			owned = false
		}
	}()
	return c.ownedWorker(win.Get("navigator").Get("serviceWorker").Get("controller"))
}

// A record with the exact scope but no worker slots does not prove ownership.
// Status may instead query an independently validated controller; mutations must
// still establish ownership from the registration itself.
type offlineRegistrationOwnership uint8

const (
	offlineRegistrationUnproven offlineRegistrationOwnership = iota
	offlineRegistrationOwned
)

func (c *offlineController) registrationOwnership(registration js.Value) (offlineRegistrationOwnership, error) {
	if registration.Type() != js.TypeObject || registration.IsNull() || registration.Get("scope").Type() != js.TypeString || registration.Get("scope").String() != c.descriptor.Scope {
		return offlineRegistrationUnproven, c.ownershipConflict("registration-scope")
	}
	provedWorker := false
	for _, key := range []string{"active", "waiting", "installing"} {
		worker := registration.Get(key)
		if worker.Truthy() {
			provedWorker = true
		}
		if worker.Truthy() && !c.ownedWorker(worker) {
			return offlineRegistrationUnproven, c.ownershipConflict("registration-" + key + "-worker")
		}
	}
	if !provedWorker {
		return offlineRegistrationUnproven, nil
	}
	return offlineRegistrationOwned, nil
}

func (c *offlineController) validateRegistration(registration js.Value) error {
	ownership, err := c.registrationOwnership(registration)
	if err != nil {
		return err
	}
	if ownership == offlineRegistrationUnproven {
		return c.ownershipConflict("registration-empty")
	}
	return nil
}

// statusWorker is read-only controller authority. A recovered registration has
// independent inventory/worker proof and uses synchronous pre-mutation fences.
type offlineRegistrationSelection struct {
	registration js.Value
	statusWorker js.Value
	container    js.Value
	recovered    bool
}

type offlineRegistrationUse uint8

const (
	offlineRegistrationStrict offlineRegistrationUse = iota
	offlineRegistrationStatus
	offlineRegistrationMutation
)

type offlineRegistrationInventory struct {
	exact    js.Value
	unproven bool
}

func (c *offlineController) registration(ctx context.Context) (js.Value, error) {
	selection, err := c.registrationForOperation(ctx, offlineRegistrationStrict)
	if err != nil {
		return js.Undefined(), err
	}
	return selection.registration, nil
}

// Inspect every entry before permitting recovery. An empty exact record still
// counts as an overlap, so it cannot hide a later foreign owner or duplicate.
func (c *offlineController) inspectRegistrationInventory(registrations js.Value) (offlineRegistrationInventory, error) {
	inventory := offlineRegistrationInventory{exact: js.Undefined()}
	if registrations.Type() != js.TypeObject || registrations.IsNull() || registrations.Length() > 64 {
		return inventory, c.ownershipConflict("registration-list")
	}
	for i := 0; i < registrations.Length(); i++ {
		registration := registrations.Index(i)
		scope := registration.Get("scope")
		if scope.Type() != js.TypeString {
			return inventory, c.ownershipConflict("registration-scope-type")
		}
		// Native scope matching is literal prefix matching, including reader2.
		if strings.HasPrefix(scope.String(), c.descriptor.Scope) || strings.HasPrefix(c.descriptor.Scope, scope.String()) {
			ownership, err := c.registrationOwnership(registration)
			if err != nil {
				return inventory, err
			}
			if inventory.exact.Truthy() {
				return inventory, c.ownershipConflict("registration-duplicate")
			}
			inventory.exact = registration
			inventory.unproven = ownership == offlineRegistrationUnproven
		}
	}
	return inventory, nil
}

func (c *offlineController) registrationController(sw js.Value) (js.Value, error) {
	controller := sw.Get("controller")
	if controller.Truthy() && !c.ownedWorker(controller) {
		return js.Undefined(), c.ownershipConflict("controller-worker")
	}
	return controller, nil
}

func (c *offlineController) registrationForOperation(ctx context.Context, use offlineRegistrationUse) (offlineRegistrationSelection, error) {
	sw := win.Get("navigator").Get("serviceWorker")
	registrations, err := c.promise(ctx, sw.Call("getRegistrations"))
	if err != nil {
		return offlineRegistrationSelection{}, err
	}
	inventory, err := c.inspectRegistrationInventory(registrations)
	if err != nil {
		return offlineRegistrationSelection{}, err
	}
	controller, err := c.registrationController(sw)
	if err != nil {
		return offlineRegistrationSelection{}, err
	}
	if inventory.unproven {
		switch use {
		case offlineRegistrationStatus:
			if !controller.Truthy() {
				return offlineRegistrationSelection{}, ErrOfflineUnavailable
			}
			return offlineRegistrationSelection{registration: js.Undefined(), statusWorker: controller}, nil
		case offlineRegistrationMutation:
			return c.reacquireRegistration(ctx, sw)
		default:
			return offlineRegistrationSelection{}, c.ownershipConflict("registration-empty-selection")
		}
	}
	return offlineRegistrationSelection{registration: inventory.exact, statusWorker: js.Undefined()}, nil
}

func (c *offlineController) requireRegistrationProof(registration js.Value) error {
	ownership, err := c.registrationOwnership(registration)
	if err != nil {
		return err
	}
	if ownership != offlineRegistrationOwned {
		return c.ownershipConflict("registration-proof-empty")
	}
	return nil
}

// One acquisition transaction: initial inventory, one exact-scope lookup, one
// final inventory. The same-environment native object map binds the lookup to
// the latest entry; no equality with the initial record or UA repair is assumed.
func (c *offlineController) reacquireRegistration(ctx context.Context, sw js.Value) (offlineRegistrationSelection, error) {
	if ctx.Err() != nil {
		return offlineRegistrationSelection{}, context.Cause(ctx)
	}
	if sw.Get("getRegistration").Type() != js.TypeFunction {
		return offlineRegistrationSelection{}, c.ownershipConflict("registration-proof-unavailable")
	}
	if ctx.Err() != nil {
		return offlineRegistrationSelection{}, context.Cause(ctx)
	}
	candidate, err := c.promise(ctx, sw.Call("getRegistration", c.descriptor.Scope))
	if err != nil {
		return offlineRegistrationSelection{}, err
	}
	if !candidate.Truthy() {
		return offlineRegistrationSelection{}, c.ownershipConflict("registration-proof-missing")
	}
	if err = c.requireRegistrationProof(candidate); err != nil {
		return offlineRegistrationSelection{}, err
	}
	if ctx.Err() != nil {
		return offlineRegistrationSelection{}, context.Cause(ctx)
	}
	registrations, err := c.promise(ctx, sw.Call("getRegistrations"))
	if err != nil {
		return offlineRegistrationSelection{}, err
	}
	inventory, err := c.inspectRegistrationInventory(registrations)
	if err != nil {
		return offlineRegistrationSelection{}, err
	}
	if _, err = c.registrationController(sw); err != nil {
		return offlineRegistrationSelection{}, err
	}
	if !inventory.exact.Truthy() {
		return offlineRegistrationSelection{}, c.ownershipConflict("registration-proof-missing")
	}
	if !candidate.Equal(inventory.exact) {
		return offlineRegistrationSelection{}, c.ownershipConflict("registration-proof-changed")
	}
	// Re-read strict fields AFTER the last awaited read and controller check.
	if err = c.requireRegistrationProof(candidate); err != nil {
		return offlineRegistrationSelection{}, err
	}
	if ctx.Err() != nil {
		return offlineRegistrationSelection{}, context.Cause(ctx)
	}
	return offlineRegistrationSelection{registration: candidate, statusWorker: js.Undefined(), container: sw, recovered: true}, nil
}

// These synchronous fences protect the next recovered-path dispatch after an
// awaited message. They are not an atomic registry lock against other writers,
// nor a rollback of a worker message which has already completed.
func (c *offlineController) recoveredRegistrationFence(ctx context.Context, selection offlineRegistrationSelection, worker js.Value, requireActive bool) error {
	if ctx.Err() != nil {
		return context.Cause(ctx)
	}
	if _, err := c.registrationController(selection.container); err != nil {
		return err
	}
	if err := c.requireRegistrationProof(selection.registration); err != nil {
		return err
	}
	if requireActive {
		active := selection.registration.Get("active")
		if !active.Truthy() || !active.Equal(worker) {
			return c.ownershipConflict("active-worker-changed")
		}
	}
	return context.Cause(ctx)
}

func (c *offlineController) waitInstallation(ctx context.Context, registration js.Value) error {
	worker := registration.Get("installing")
	if !worker.Truthy() {
		return c.validateRegistration(registration)
	}
	return c.waitWorker(ctx, worker, func(state string) bool { return state == "installed" || state == "activated" })
}

func (c *offlineController) waitActivation(ctx context.Context, registration js.Value) error {
	if active := registration.Get("active"); active.Truthy() {
		return c.validateRegistration(registration)
	}
	worker := registration.Get("installing")
	if !worker.Truthy() {
		return ErrOfflineUnavailable
	}
	if err := c.waitWorker(ctx, worker, func(state string) bool { return state == "activated" }); err != nil {
		return err
	}
	if !registration.Get("active").Truthy() {
		return ErrOfflineUnavailable
	}
	return c.validateRegistration(registration)
}

func (c *offlineController) waitWorker(ctx context.Context, worker js.Value, complete func(string) bool) error {
	if !c.ownedWorker(worker) {
		return c.ownershipConflict("worker-state-target")
	}
	done := make(chan struct{}, 1)
	callback := js.FuncOf(func(js.Value, []js.Value) any {
		select {
		case done <- struct{}{}:
		default:
		}
		return nil
	})
	listening := false
	defer func() {
		if listening {
			offlineQuiet(func() { worker.Call("removeEventListener", "statechange", callback) })
		}
		callback.Release()
	}()
	worker.Call("addEventListener", "statechange", callback)
	listening = true
	for {
		state := worker.Get("state").String()
		if state == "redundant" {
			return ErrOfflineProtocol
		}
		if complete(state) {
			return nil
		}
		select {
		case <-done:
		case <-ctx.Done():
			return ErrOfflineUnknown
		}
	}
}

func offlineQuiet(fn func()) { defer func() { _ = recover() }(); fn() }

func (c *offlineController) apply(reply offlineReply) {
	c.confirmedDisabled = reply.State == "disabled" && !reply.Enabled
	c.state.CleanupPending = reply.DeferredCleanup
	c.state.Supported = reply.Supported
	c.state.Enabled = reply.Enabled
	c.state.ActiveGeneration = reply.ActiveGeneration
	c.state.WaitingGeneration = reply.StagingGeneration
	switch reply.State {
	case "staged":
		c.state.Phase = "waiting"
	case "failed":
		c.state.Phase = "error"
	case "busy":
		c.state.Phase = "busy"
	default:
		c.state.Phase = reply.State
	}
	if reply.Error != "" {
		c.state.LastError = reply.Error
	}
}

func (c *offlineController) message(ctx context.Context, worker js.Value, command, runtime, configuration string) (reply offlineReply, err error) {
	if ctx.Err() != nil {
		return reply, context.Cause(ctx)
	}
	if !c.ownedWorker(worker) {
		return reply, c.ownershipConflict("message-worker")
	}
	c.request++
	request := offlineRequest{Version: offlineMessageVersion, Command: command, OwnerPolicyID: c.descriptor.OwnerPolicyID, RuntimeID: runtime, ConfigurationID: configuration, RequestID: "offline-" + strconv.FormatUint(c.request, 36)}
	channel := win.Get("MessageChannel").New()
	port := channel.Get("port1")
	other := channel.Get("port2")
	type result struct {
		reply offlineReply
		err   error
	}
	received := make(chan result, 1)
	callback := js.FuncOf(func(_ js.Value, args []js.Value) any {
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
	listening := false
	defer func() {
		if listening {
			offlineQuiet(func() { port.Call("removeEventListener", "message", callback) })
		}
		offlineQuiet(func() { port.Call("close") })
		offlineQuiet(func() { other.Call("close") })
		callback.Release()
	}()
	port.Call("addEventListener", "message", callback, map[string]any{"once": true})
	listening = true
	port.Call("start")
	worker.Call("postMessage", map[string]any{"version": request.Version, "command": request.Command, "ownerPolicyID": request.OwnerPolicyID, "runtimeID": request.RuntimeID, "configurationID": request.ConfigurationID, "requestID": request.RequestID}, []any{other})
	select {
	case value := <-received:
		return value.reply, value.err
	case <-ctx.Done():
		return reply, ErrOfflineUnknown
	}
}

// Inspect the fixed scalar shape before copying strings or marshaling. The
// transferred private port is dedicated to one bounded status/control reply.
func decodeOfflineMessage(value js.Value, request offlineRequest) (offlineReply, error) {
	if value.Type() != js.TypeObject || value.IsNull() || js.Global().Get("Object").Call("keys", value).Length() != 16 {
		return offlineReply{}, ErrOfflineProtocol
	}
	object := map[string]any{}
	for _, key := range []string{"requestID", "ownerPolicyID", "runtimeID", "configurationID", "generation", "activeGeneration", "previousGeneration", "stagingGeneration", "state", "error"} {
		v := value.Get(key)
		if v.Type() != js.TypeString || js.Global().Get("Object").Invoke(v).Get("length").Int() > 64 {
			return offlineReply{}, ErrOfflineProtocol
		}
		object[key] = v.String()
	}
	for _, key := range []string{"enabled", "supported", "deferredCleanup"} {
		v := value.Get(key)
		if v.Type() != js.TypeBoolean {
			return offlineReply{}, ErrOfflineProtocol
		}
		object[key] = v.Bool()
	}
	for _, key := range []string{"version", "savedAtUnix", "highestObservedUnix"} {
		v := value.Get(key)
		if v.Type() != js.TypeNumber {
			return offlineReply{}, ErrOfflineProtocol
		}
		n := v.Float()
		if n < 0 || n > 1<<53-1 || n != float64(int64(n)) {
			return offlineReply{}, ErrOfflineProtocol
		}
		object[key] = int64(n)
	}
	raw, err := json.Marshal(object)
	if err != nil {
		return offlineReply{}, ErrOfflineProtocol
	}
	return decodeOfflineReply(raw, request)
}

func savedOfflineHydrate() {
	// currentURL deliberately remains unset. Core/module Navigate, Refresh and
	// Prefetch therefore cannot fetch or commit a JSON navigation envelope.
	// Native anchors, history and forms remain browser/service-worker documents.
	installMotionPreference()
	hydrate()
}

// A saved document stays native even after Disable until a new document loads.
// A normal document with an owned controller is conservative until an owned
// status confirms Disabled; opt-out/uncontrolled documents retain their router.
func offlineNativeNavigation() bool {
	c := documentOffline
	if c == nil || !c.state.Configured {
		return false
	}
	if c.state.SavedDocument {
		return true
	}
	return c.controlling() && (c.state.Enabled || !c.confirmedDisabled)
}

func offlineNavigate(target string, replace bool) (err error) {
	defer func() {
		if recover() != nil {
			err = fmt.Errorf("nextgo: invalid native document navigation")
		}
	}()
	u := win.Get("URL").New(target, doc.Get("baseURI"))
	if !eligibleURL(u) {
		return fmt.Errorf("navigation requires a same-origin page URL without credentials")
	}
	cancelPendingNavigation()
	if replace {
		win.Get("location").Call("replace", u.Get("href").String())
	} else {
		win.Get("location").Call("assign", u.Get("href").String())
	}
	return nil
}
