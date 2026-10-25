//go:build js && wasm && !nextgo_module

package client

import (
	"reflect"
	"syscall/js"
	"testing"

	"github.com/Suhaibinator/Next.go/internal/devrefresh"
)

func TestLinkStatusIdentityCommitSupersessionAndCleanup(t *testing.T) {
	f := linkStatusEnvironment(t)
	var first, duplicate, second []bool
	for i, seen := range []*[]bool{&first, &duplicate, &second} {
		if _, err := f.scope.ObserveLinkStatus(f.links[i], func(value LinkStatus) { *seen = append(*seen, value.Pending) }); err != nil {
			t.Fatal(err)
		}
	}
	beginLinkNavigation(10, f.links[0].Value)
	if !reflect.DeepEqual(first, []bool{false}) {
		t.Fatal("cached transition published pending", first)
	}
	finishLinkNavigation(10)
	beginLinkNavigation(11, f.links[0].Value)
	waitLinkNavigation(11)
	waitLinkNavigation(11)
	if !reflect.DeepEqual(first, []bool{false, true}) || !reflect.DeepEqual(duplicate, []bool{false}) {
		t.Fatal("link identity or deduplication lost", first, duplicate)
	}
	beginLinkNavigation(12, f.links[2].Value)
	waitLinkNavigation(12)
	finishLinkNavigation(11)
	if !currentLinkStatus(f.links[2].Value).Pending || !reflect.DeepEqual(first, []bool{false, true, false}) {
		t.Fatal("old completion cleared new link", first, second)
	}
	finishLinkNavigation(12)
	if !reflect.DeepEqual(second, []bool{false, true, false}) {
		t.Fatal("commit did not settle link", second)
	}
	beginLinkNavigation(13, f.links[0].Value)
	waitLinkNavigation(13)
	f.scope.dispose()
	if len(linkStatusObservers) != 0 {
		t.Fatal("disposed island retained observers")
	}
	finishLinkNavigation(13)
	if linkNavigationNode.Truthy() {
		t.Fatal("completed navigation retained its source node")
	}
	if !reflect.DeepEqual(first, []bool{false, true, false, true}) {
		t.Fatal("disposed callback ran", first)
	}
}

func TestLinkStatusRejectsUnownedNodesAndBoundsRegistrations(t *testing.T) {
	f := linkStatusEnvironment(t)
	if _, err := f.scope.ObserveLinkStatus(f.links[0], nil); err == nil {
		t.Fatal("nil callback accepted")
	}
	old := committedLocation
	committedLocation = Location{}
	if _, err := f.scope.ObserveLinkStatus(f.links[0], func(LinkStatus) {}); err == nil {
		t.Fatal("subscription before Run accepted")
	}
	committedLocation = old
	f.links[0].Value.Set("fixtureOwner", js.Null())
	if _, err := f.scope.ObserveLinkStatus(f.links[0], func(LinkStatus) {}); err == nil {
		t.Fatal("foreign anchor accepted")
	}
	f.links[0].Value.Set("fixtureOwner", f.scope.Root.Value)
	f.links[0].Value.Set("tagName", "BUTTON")
	if _, err := f.scope.ObserveLinkStatus(f.links[0], func(LinkStatus) {}); err == nil {
		t.Fatal("button accepted")
	}
	f.links[0].Value.Set("tagName", "A")
	for range 1000 {
		stop, err := f.scope.ObserveLinkStatus(f.links[0], func(LinkStatus) {})
		if err != nil {
			t.Fatal(err)
		}
		stop()
		stop()
	}
	if len(f.scope.cleanup) != 1 {
		t.Fatal("unsubscription accumulated cleanup closures", len(f.scope.cleanup))
	}
	var stop func()
	for i := range maxLinkStatusObservers {
		next, err := f.scope.ObserveLinkStatus(f.links[0], func(LinkStatus) {})
		if err != nil {
			t.Fatal(i, err)
		}
		stop = next
	}
	if _, err := f.scope.ObserveLinkStatus(f.links[0], func(LinkStatus) {}); err == nil {
		t.Fatal("unbounded registrations")
	}
	stop()
	if _, err := f.scope.ObserveLinkStatus(f.links[0], func(LinkStatus) {}); err != nil {
		t.Fatal("released budget unavailable", err)
	}
	if refreshScopeSupported(f.scope) {
		t.Fatal("link observation claimed unsupported state transfer")
	}
}

func TestLinkStatusReentrantCallbacksFollowLatestState(t *testing.T) {
	f := linkStatusEnvironment(t)
	var seen []bool
	_, err := f.scope.ObserveLinkStatus(f.links[0], func(value LinkStatus) {
		seen = append(seen, value.Pending)
		if value.Pending {
			beginLinkNavigation(21, f.links[1].Value)
			waitLinkNavigation(21)
		}
	})
	if err != nil {
		t.Fatal(err)
	}
	var other []bool
	_, err = f.scope.ObserveLinkStatus(f.links[1], func(value LinkStatus) { other = append(other, value.Pending) })
	if err != nil {
		t.Fatal(err)
	}
	beginLinkNavigation(20, f.links[0].Value)
	waitLinkNavigation(20)
	finishLinkNavigation(20)
	if linkNavigationID != 21 || !reflect.DeepEqual(seen, []bool{false, true, false}) || !reflect.DeepEqual(other, []bool{false, true}) {
		t.Fatal("reentrant navigation regressed", linkNavigationID, seen, other)
	}
	finishLinkNavigation(21)
	if !reflect.DeepEqual(other, []bool{false, true, false}) {
		t.Fatal(other)
	}
	_, err = f.scope.ObserveLinkStatus(f.links[2], func(value LinkStatus) {
		if value.Pending {
			panic("application observer panic")
		}
	})
	if err != nil {
		t.Fatal(err)
	}
	beginLinkNavigation(22, f.links[2].Value)
	waitLinkNavigation(22)
	finishLinkNavigation(22)
	if linkNavigationNode.Truthy() {
		t.Fatal("panic stranded source")
	}
}

func TestLinkStatusLazyHostSharesBudgetAndRequiresOwner(t *testing.T) {
	f := linkStatusEnvironment(t)
	if moduleABI != devrefresh.ModuleABI {
		t.Fatal("development and lazy ABI differ")
	}
	var seen []bool
	callback := f.function(func(_ js.Value, args []js.Value) any { seen = append(seen, args[0].Bool()); return nil })
	result := js.ValueOf(observeLinkStatusHost([]js.Value{f.scope.Root.Value, f.links[0].Value, callback.Value}))
	if result.Get("error").String() != "" || result.Get("pending").Bool() {
		t.Fatal("registration failed", result)
	}
	id := result.Get("id")
	for range maxLinkStatusObservers - 1 {
		if _, err := f.scope.ObserveLinkStatus(f.links[1], func(LinkStatus) {}); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := f.scope.ObserveLinkStatus(f.links[1], func(LinkStatus) {}); err == nil {
		t.Fatal("lazy registration escaped shared budget")
	}
	beginLinkNavigation(30, f.links[0].Value)
	waitLinkNavigation(30)
	if !reflect.DeepEqual(seen, []bool{true}) {
		t.Fatal("remote callback not delivered", seen)
	}
	if result := unobserveLinkStatusHost([]js.Value{f.links[1].Value, id}); result == "" {
		t.Fatal("foreign owner removed registration")
	}
	if result := unobserveLinkStatusHost([]js.Value{f.scope.Root.Value, id}); result != "" {
		t.Fatal(result)
	}
	finishLinkNavigation(30)
	if !reflect.DeepEqual(seen, []bool{true}) || linkNavigationNode.Truthy() {
		t.Fatal("unregistered module retained callback/node", seen)
	}
	if _, err := f.scope.ObserveLinkStatus(f.links[1], func(LinkStatus) {}); err != nil {
		t.Fatal("remote budget not released", err)
	}
}

func TestLinkStatusLateSubscriptionReadsCurrentNavigation(t *testing.T) {
	f := linkStatusEnvironment(t)
	beginLinkNavigation(40, f.links[0].Value)
	waitLinkNavigation(40)
	var seen []bool
	stop, err := f.scope.ObserveLinkStatus(f.links[0], func(value LinkStatus) { seen = append(seen, value.Pending) })
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(seen, []bool{true}) {
		t.Fatal("late observer missed current pending source", seen)
	}
	stop()
	var replacement []bool
	_, err = f.scope.ObserveLinkStatus(f.links[0], func(value LinkStatus) { replacement = append(replacement, value.Pending) })
	if err != nil {
		t.Fatal(err)
	}
	finishLinkNavigation(40)
	if !reflect.DeepEqual(replacement, []bool{true, false}) || !reflect.DeepEqual(seen, []bool{true}) {
		t.Fatal("subscription lifetime changed source identity", seen, replacement)
	}
}

func TestLinkStatusHostAllowsCommittedNativeDocumentWithoutRouter(t *testing.T) {
	f := linkStatusEnvironment(t)
	oldRefresh := devRefresh
	devRefresh = nil
	t.Cleanup(func() { devRefresh = oldRefresh })
	currentURL = ""
	if _, ready := CurrentLocation(); !ready {
		t.Fatal("fixture must retain the Run-established location without a SPA router")
	}
	var eager []bool
	stop, err := f.scope.ObserveLinkStatus(f.links[0], func(status LinkStatus) { eager = append(eager, status.Pending) })
	if err != nil || !reflect.DeepEqual(eager, []bool{false}) {
		t.Fatal("eager native-document observation did not start idle", eager, err)
	}
	defer stop()
	var remote []bool
	callback := f.function(func(_ js.Value, args []js.Value) any {
		if len(args) != 1 || args[0].Type() != js.TypeBoolean {
			t.Error("invalid native-document link-status callback")
			return nil
		}
		remote = append(remote, args[0].Bool())
		return nil
	})
	args := []js.Value{f.scope.Root.Value, f.links[1].Value, callback.Value}
	result := js.ValueOf(observeLinkStatusHost(args))
	if result.Get("error").String() != "" || result.Get("pending").Type() != js.TypeBoolean || result.Get("pending").Bool() || len(linkStatusObservers) != 2 {
		t.Fatal("lazy host rejected or marked a committed native document pending", result)
	}
	if len(remote) != 0 || linkNavigationID != 0 || linkNavigationNode.Truthy() || linkNavigationWaiting || currentURL != "" {
		t.Fatal("idle host registration started navigation or invoked a change callback")
	}
	id := result.Get("id")
	for range 2 {
		if result := unobserveLinkStatusHost([]js.Value{f.scope.Root.Value, id}); result != "" {
			t.Fatal("native-document host cleanup failed", result)
		}
	}
	stop()
	if len(linkStatusObservers) != 0 {
		t.Fatal("native-document cleanup retained observer budget")
	}
	rejected := func(reason string) {
		t.Helper()
		result := js.ValueOf(observeLinkStatusHost(args))
		if result.Get("error").String() != "link status document is not active" || len(linkStatusObservers) != 0 || len(remote) != 0 {
			t.Fatal(reason, result)
		}
	}
	location := committedLocation
	committedLocation = Location{}
	currentURL = "https://site.test/current"
	rejected("a router URL without an initialized location was accepted")
	committedLocation, currentURL = location, ""
	draftFrozen = true
	rejected("draft-frozen native document accepted a registration")
	draftFrozen = false
	if devRefreshBuild {
		devRefresh = &refreshCoordinator{frozen: true}
		rejected("refresh-frozen native document accepted a registration")
		devRefresh = nil
	}
	result = js.ValueOf(observeLinkStatusHost(args))
	if result.Get("error").String() != "" || result.Get("pending").Bool() || len(linkStatusObservers) != 1 {
		t.Fatal("rejected registrations consumed the native-document observer budget", result)
	}
	if result := unobserveLinkStatusHost([]js.Value{f.scope.Root.Value, result.Get("id")}); result != "" || len(linkStatusObservers) != 0 {
		t.Fatal("recovered native-document registration did not release its budget", result)
	}
}
