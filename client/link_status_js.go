//go:build js && wasm

package client

import (
	"fmt"
	"math"
	"sort"
	"syscall/js"
)

const maxLinkStatusObservers = 512

type linkStatusObserver struct {
	owner      *Scope
	link, root js.Value
	fn         func(LinkStatus)
	remote     js.Value
	stopRemote func()
	last       LinkStatus
	notified   bool
	stopped    bool
}

// The document core owns the one registration budget, including lazy modules.
// Module-local entries only own their callbacks and unregister on scope cleanup.
var linkStatusObservers = map[uint64]*linkStatusObserver{}
var nextLinkStatusObserver uint64
var moduleLinkCallbackCount int
var linkNavigationID uint64
var linkNavigationNode js.Value
var linkNavigationWaiting bool

// ObserveLinkStatus observes a specific anchor owned by this scope. It reports
// its current status immediately, then changes only. Two links to the same URL
// have independent identity; only the latest intercepted click can be pending.
// Native/modifier/fragment clicks and programmatic navigation do not select a
// link. Callbacks must return promptly. Cleanup unsubscribes automatically; stop
// allows earlier, idempotent removal. The document allows 512 registrations.
func (s *Scope) ObserveLinkStatus(link Element, fn func(LinkStatus)) (stop func(), err error) {
	if !s.active() || fn == nil {
		return nil, fmt.Errorf("link status requires an open scope and callback")
	}
	if _, ready := CurrentLocation(); !ready {
		return nil, fmt.Errorf("link status requires Run")
	}
	if !s.ownsElement(link.Value, true) || link.Value.Get("tagName").String() != "A" || !link.Value.Get("isConnected").Bool() {
		return nil, fmt.Errorf("link status requires a connected anchor owned by the scope")
	}
	observer := &linkStatusObserver{owner: s, link: link.Value, fn: fn}
	var initial LinkStatus
	if moduleMode {
		initial, err = observeModuleLinkStatus(observer)
		if err != nil {
			return nil, err
		}
	} else {
		if len(linkStatusObservers) >= maxLinkStatusObservers || nextLinkStatusObserver >= 9007199254740991 {
			return nil, fmt.Errorf("link status observer bound exceeded")
		}
		initial = currentLinkStatus(link.Value)
	}
	nextLinkStatusObserver++
	id := nextLinkStatusObserver
	linkStatusObservers[id] = observer
	stop = func() { stopLinkStatusObserver(id) }
	if !s.linkStatusCleanup {
		s.linkStatusCleanup = true
		s.Cleanup(func() {
			for id, item := range linkStatusObservers {
				if item.owner == s {
					stopLinkStatusObserver(id)
				}
			}
		})
	}
	dispatchLinkStatus(observer, initial)
	return stop, nil
}

func stopLinkStatusObserver(id uint64) {
	observer := linkStatusObservers[id]
	if observer == nil {
		return
	}
	delete(linkStatusObservers, id)
	observer.stopped = true
	if observer.stopRemote != nil {
		observer.stopRemote()
	}
}

func currentLinkStatus(link js.Value) LinkStatus {
	return LinkStatus{Pending: linkNavigationWaiting && linkNavigationNode.Truthy() && link.Equal(linkNavigationNode)}
}
func dispatchLinkStatus(observer *linkStatusObserver, status LinkStatus) {
	if observer.stopped || observer.owner != nil && !observer.owner.active() || observer.notified && observer.last == status {
		return
	}
	observer.last, observer.notified = status, true
	defer func() {
		if recover() != nil {
			report("Link status observer failed")
		}
	}()
	if observer.remote.Type() == js.TypeFunction {
		observer.remote.Invoke(status.Pending)
	} else {
		observer.fn(status)
	}
}
func notifyLinkStatusObservers() {
	ids := make([]uint64, 0, len(linkStatusObservers))
	for id := range linkStatusObservers {
		ids = append(ids, id)
	}
	sort.Slice(ids, func(i, j int) bool { return ids[i] < ids[j] })
	for _, id := range ids {
		if observer := linkStatusObservers[id]; observer != nil {
			dispatchLinkStatus(observer, currentLinkStatus(observer.link))
		}
	}
}

func beginLinkNavigation(id uint64, source js.Value) {
	linkNavigationID, linkNavigationNode, linkNavigationWaiting = id, js.Undefined(), false
	// Keep one actual source independently of subscriptions, so a component
	// mounting while the request is in flight receives its current status.
	// Completion/cancellation/supersession releases this document-owned node.
	if source.Truthy() {
		linkNavigationNode = source
	}
	notifyLinkStatusObservers()
}
func waitLinkNavigation(id uint64) {
	if linkNavigationID != id || linkNavigationWaiting {
		return
	}
	linkNavigationWaiting = true
	notifyLinkStatusObservers()
}
func finishLinkNavigation(id uint64) {
	if linkNavigationID != id {
		return
	}
	linkNavigationID, linkNavigationNode, linkNavigationWaiting = 0, js.Undefined(), false
	notifyLinkStatusObservers()
}

func observeModuleLinkStatus(observer *linkStatusObserver) (initial LinkStatus, err error) {
	var callback js.Func
	safeToRelease := true
	allocated := false
	release := func() {
		if allocated {
			allocated = false
			moduleLinkCallbackCount--
			callback.Release()
		}
	}
	defer func() {
		if failure := recover(); failure != nil {
			err = fmt.Errorf("link status module host unavailable")
		}
		if err != nil {
			observer.stopped = true
			if safeToRelease {
				release()
			}
		}
	}()
	if !moduleHost.Truthy() || moduleHost.Get("version").Type() != js.TypeNumber || moduleHost.Get("version").Int() != moduleABI {
		return initial, fmt.Errorf("incompatible link status module host")
	}
	if moduleLinkCallbackCount >= maxLinkStatusObservers {
		return initial, fmt.Errorf("link status callback bound exceeded; reload the page")
	}
	if moduleHost.Get("observeLinkStatus").Type() != js.TypeFunction || moduleHost.Get("unobserveLinkStatus").Type() != js.TypeFunction {
		return initial, fmt.Errorf("link status host capability unavailable")
	}
	callback = js.FuncOf(func(_ js.Value, args []js.Value) any {
		if len(args) == 1 && args[0].Type() == js.TypeBoolean {
			dispatchLinkStatus(observer, LinkStatus{Pending: args[0].Bool()})
		}
		return nil
	})
	allocated = true
	moduleLinkCallbackCount++
	safeToRelease = false
	root := observer.owner.islandRoot()
	result := moduleHost.Call("observeLinkStatus", root, observer.link, callback)
	if result.Type() != js.TypeObject || result.IsNull() || result.Get("error").Type() != js.TypeString {
		return initial, fmt.Errorf("invalid link status host response")
	}
	if failure := result.Get("error").String(); failure != "" {
		safeToRelease = true // Rejection never registers the callback.
		return initial, fmt.Errorf("%s", failure)
	}
	handle := result.Get("id")
	if handle.Type() != js.TypeNumber || handle.Float() < 1 || handle.Float() > 9007199254740991 || math.Trunc(handle.Float()) != handle.Float() || result.Get("pending").Type() != js.TypeBoolean {
		return initial, fmt.Errorf("invalid link status host handle")
	}
	observer.stopRemote = func() {
		// Unregister synchronously before releasing the callable value. If the
		// host is broken, retain the inactive callback until document teardown;
		// releasing a value the core might still call would be unsafe.
		if failure := moduleCall("unobserveLinkStatus", root, handle); failure != nil {
			report("Link status cleanup failed; reload the page")
			return
		}
		release()
	}
	return LinkStatus{Pending: result.Get("pending").Bool()}, nil
}

func observeLinkStatusHost(args []js.Value) any {
	failure := func(message string) any { return map[string]any{"error": message} }
	if len(args) != 3 || args[0].Type() != js.TypeObject || args[0].IsNull() || args[1].Type() != js.TypeObject || args[1].IsNull() || args[2].Type() != js.TypeFunction {
		return failure("invalid link status registration")
	}
	if _, ready := CurrentLocation(); !ready || draftFrozen || refreshFrozen() {
		return failure("link status document is not active")
	}
	owner := &Scope{Root: Element{args[0]}}
	if !owner.ownsElement(args[1], true) || args[1].Get("tagName").String() != "A" || !args[1].Get("isConnected").Bool() {
		return failure("link status requires an owned anchor")
	}
	if len(linkStatusObservers) >= maxLinkStatusObservers || nextLinkStatusObserver >= 9007199254740991 {
		return failure("link status observer bound exceeded")
	}
	nextLinkStatusObserver++
	id := nextLinkStatusObserver
	linkStatusObservers[id] = &linkStatusObserver{root: args[0], link: args[1], remote: args[2], last: currentLinkStatus(args[1]), notified: true}
	return map[string]any{"id": id, "pending": currentLinkStatus(args[1]).Pending, "error": ""}
}
func unobserveLinkStatusHost(args []js.Value) any {
	if len(args) != 2 || args[1].Type() != js.TypeNumber || args[1].Float() < 1 || args[1].Float() > 9007199254740991 || math.Trunc(args[1].Float()) != args[1].Float() {
		return "invalid link status unregistration"
	}
	id := uint64(args[1].Float())
	if observer := linkStatusObservers[id]; observer != nil {
		if !observer.root.Equal(args[0]) || observer.remote.Type() != js.TypeFunction {
			return "link status owner differs"
		}
		stopLinkStatusObserver(id)
	}
	return ""
}
