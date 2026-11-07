//go:build js && wasm && !nextgo_module

package client

import (
	"context"
	"errors"
	"syscall/js"
	"testing"
	"time"
)

func mergeQueueTestState(t *testing.T) *mergeCommitLease {
	t.Helper()
	oldLease, oldCurrent, oldPrivacy, oldInstall, oldBoot := mergeActiveLease, mergeCurrent, mergePrivacy, mergeInstallActive, mergeBooting
	oldWaiting, oldNative, oldIntent := mergeWaiting, mergeNativePending, mergeNativeIntent
	oldRequested, oldRequestedIntent := mergeNativeRequestedPending, mergeNativeRequestedIntent
	oldKind, oldTarget, oldRevision, oldNativePrivacy, oldDocument := mergeNativePendingKind, mergeNativeTarget, mergeNativeRevision, mergeNativePrivacy, mergeNativeDocument
	lease := &mergeCommitLease{done: make(chan struct{}), intent: navigationID, queue: map[string]mergeNotification{}, failures: map[*Scope]error{}}
	mergeActiveLease, mergeCurrent, mergeInstallActive, mergeBooting = lease, nil, false, false
	mergeWaiting, mergeNativePending = nil, nil
	mergeNativeRequestedPending = nil
	t.Cleanup(func() {
		mergeActiveLease, mergeCurrent, mergePrivacy, mergeInstallActive, mergeBooting = oldLease, oldCurrent, oldPrivacy, oldInstall, oldBoot
		mergeWaiting, mergeNativePending, mergeNativeIntent = oldWaiting, oldNative, oldIntent
		mergeNativeRequestedPending, mergeNativeRequestedIntent = oldRequested, oldRequestedIntent
		mergeNativePendingKind, mergeNativeTarget, mergeNativeRevision, mergeNativePrivacy, mergeNativeDocument = oldKind, oldTarget, oldRevision, oldNativePrivacy, oldDocument
	})
	return lease
}
func TestMergeLinkObserverSameKeyReentryQueuesAfterOneConsumedWrapperToken(t *testing.T) {
	lease := mergeQueueTestState(t)
	scope := &Scope{}
	calls, depth, maxDepth := 0, 0, 0
	observer := &linkStatusObserver{id: 7341, owner: scope}
	observer.fn = func(value LinkStatus) {
		depth++
		if depth > maxDepth {
			maxDepth = depth
		}
		defer func() { depth-- }()
		calls++
		if calls < 4 {
			dispatchLinkStatus(observer, LinkStatus{Pending: !value.Pending})
		}
	}
	dispatchLinkStatus(observer, LinkStatus{})
	if calls != 0 {
		t.Fatal("observer ran before committed delivery")
	}
	failures := flushMergeNotifications(func() bool { return true })
	if len(failures) != 0 || calls != 4 || maxDepth != 1 || len(lease.order) != 0 || len(lease.queue) != 0 {
		t.Fatalf("same-key recursion bypass: calls%d depth%d pending%d/%d errors%v", calls, maxDepth, len(lease.order), len(lease.queue), failures)
	}
}
func TestMergeManagedResourceReentryDeliversLatestInBoundedFollowup(t *testing.T) {
	lease := mergeQueueTestState(t)
	scope := &Scope{}
	// Teardown is outside the delivery gate; no test-owned resource is stranded.
	t.Cleanup(func() { mergeActiveLease = nil; scope.dispose() })
	store, err := scope.ResourceStore(ResourceStoreOptions{})
	if err != nil {
		t.Fatal(err)
	}
	source := resourceSource(t, store, "merge", time.Minute, func(context.Context) ([]byte, error) { return []byte("unused"), nil })
	value, err := scope.AsyncResource(source, AsyncResourceOptions{})
	if err != nil {
		t.Fatal(err)
	}
	calls, depth, maxDepth := 0, 0, 0
	phases := []ResourcePhase{}
	_, err = value.Subscribe(func(snapshot ResourceSnapshot) {
		depth++
		if depth > maxDepth {
			maxDepth = depth
		}
		defer func() { depth-- }()
		calls++
		phases = append(phases, snapshot.Phase)
		if calls < 3 {
			store.Invalidate()
		}
	})
	if err != nil {
		t.Fatal(err)
	}
	if calls != 0 {
		t.Fatal("resource observer escaped install gate")
	}
	failures := flushMergeNotifications(func() bool { return true })
	if len(failures) != 0 || calls != 3 || maxDepth != 1 || phases[0] != ResourceIdle || phases[1] != ResourceInvalidated || phases[2] != ResourceInvalidated || len(lease.order) != 0 {
		t.Fatalf("resource reentry was recursive/dropped: calls%d depth%d phases%v queue%d failures%v", calls, maxDepth, phases, len(lease.order), failures)
	}
}
func TestMergeNotificationFeedbackFailsAtOneDocumentDeliveryBound(t *testing.T) {
	lease := mergeQueueTestState(t)
	scope := &Scope{}
	calls := 0
	observer := &linkStatusObserver{id: 7342, owner: scope}
	observer.fn = func(value LinkStatus) { calls++; dispatchLinkStatus(observer, LinkStatus{Pending: !value.Pending}) }
	dispatchLinkStatus(observer, LinkStatus{})
	failures := flushMergeNotifications(func() bool { return true })
	if calls > mergeNotificationLimit*4 || len(failures) != 1 || len(lease.order) != 0 || len(lease.queue) != 0 {
		t.Fatalf("notification feedback unbounded: calls%d queue%d/%d errors%v", calls, len(lease.order), len(lease.queue), failures)
	}
	// A later flush or document release cannot restart the exhausted loop.
	_ = flushMergeNotifications(func() bool { return true })
	if calls > mergeNotificationLimit*4 {
		t.Fatal("follow-up flush restarted feedback")
	}
}
func TestMergeUnexpectedScopedEventIsNotReplayedAsMutation(t *testing.T) {
	lease := mergeQueueTestState(t)
	scope := &Scope{}
	target := js.Global().Get("Object").New()
	var handler js.Value
	add := js.FuncOf(func(_ js.Value, args []js.Value) any { handler = args[1]; return nil })
	defer add.Release()
	remove := js.FuncOf(func(js.Value, []js.Value) any { return nil })
	defer remove.Release()
	target.Set("addEventListener", add)
	target.Set("removeEventListener", remove)
	called, prevented := 0, 0
	scope.On(Element{target}, "click", func(Event) { called++ })
	event := js.Global().Get("Object").New()
	event.Set("cancelable", true)
	prevent := js.FuncOf(func(js.Value, []js.Value) any { prevented++; return nil })
	defer prevent.Release()
	event.Set("preventDefault", prevent)
	mergeInstallActive = true
	handler.Invoke(event)
	mergeInstallActive = false
	if called != 0 || prevented != 1 || len(lease.failures) != 1 {
		t.Fatalf("event replay/missed recovery: called%d prevented%d failures%d", called, prevented, len(lease.failures))
	}
	mergeActiveLease = nil
	scope.dispose()
	if called != 0 {
		t.Fatal("old mutation callback replayed during cleanup")
	}
}
func TestMergeColdCommitWaitsForBootWithoutReservingLegacyWorkers(t *testing.T) {
	oldLease, oldCurrent, oldBoot, oldID := mergeActiveLease, mergeCurrent, mergeBooting, navigationID
	oldCapacity := mergeCallbackCapacity
	mergeActiveLease, mergeCurrent, mergeBooting = nil, nil, true
	navigationID = 911
	t.Cleanup(func() {
		mergeActiveLease, mergeCurrent, mergeBooting, navigationID = oldLease, oldCurrent, oldBoot, oldID
		mergeCallbackCapacity = oldCapacity
	})
	release, err := acquireMergeCommit(context.Background(), 911, false)
	if err != nil {
		t.Fatal(err)
	}
	if mergeActiveLease == nil || mergeActiveLease.callbackRelease != nil || mergeCallbackCapacity != oldCapacity {
		t.Fatal("legacy boot reserved callback capacity")
	}
	navigationID = 912
	ctx, cancel := context.WithCancel(context.Background())
	started, done := make(chan struct{}), make(chan error, 1)
	go func() {
		close(started)
		next, err := acquireMergeCommit(ctx, 912, false)
		if next != nil {
			next()
		}
		done <- err
	}()
	<-started
	select {
	case err := <-done:
		t.Fatal("new commit passed useful boot", err)
	default:
	}
	cancel()
	if err := <-done; !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	release()
}
func TestMergePublishedNotificationsFinishWhenOrdinaryNewRequestStarts(t *testing.T) {
	lease := mergeQueueTestState(t)
	oldID := navigationID
	t.Cleanup(func() { navigationID = oldID })
	first, second := &Scope{}, &Scope{}
	calls := []string{}
	a := &linkStatusObserver{id: 7343, owner: first, fn: func(LinkStatus) { calls = append(calls, "A"); navigationID++ }}
	b := &linkStatusObserver{id: 7344, owner: second, fn: func(LinkStatus) { calls = append(calls, "B") }}
	dispatchLinkStatus(a, LinkStatus{})
	dispatchLinkStatus(b, LinkStatus{})
	failures := flushMergeNotifications(func() bool { return true })
	if len(failures) != 0 || len(calls) != 2 || calls[0] != "A" || calls[1] != "B" || len(lease.order) != 0 {
		t.Fatalf("new request canceled published sibling: %v %v", calls, failures)
	}
}

func TestMergeCallbackReaderKeepsBothPropsSourcesAndLocationUntilWriterCanInstall(t *testing.T) {
	propsBrowserEnvironment(t)
	oldID, oldInstall := navigationID, mergeInstallActive
	navigationID = 1941
	mergeInstallActive = false
	t.Cleanup(func() { navigationID, mergeInstallActive = oldID, oldInstall })
	_, a, _ := propsClientRoot(t, `1`, PropsOptions[int]{Clone: func(v int) int { return v }}, nil)
	_, b, _ := propsClientRoot(t, `2`, PropsOptions[int]{Clone: func(v int) int { return v }}, nil)
	release, err := mergeEnterCallbackRead()
	if err != nil {
		t.Fatal(err)
	}
	defer release()
	first, err := a.GetSnapshot()
	if err != nil {
		t.Fatal(err)
	}
	entered, done := make(chan struct{}), make(chan error, 1)
	go func() { close(entered); done <- mergeAwaitCallbackReads(context.Background(), 1941) }()
	<-entered
	select {
	case err := <-done:
		t.Fatal("writer passed held callback", err)
	default:
	}
	second, err := b.GetSnapshot()
	if err != nil {
		t.Fatal(err)
	}
	location, _ := CurrentLocation()
	if first.Revision != second.Revision || first.Location != second.Location || first.Location != location {
		t.Fatal("owned callback observed torn source/location frame", first, second, location)
	}
	release()
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	if mergeCallbackReads != 0 {
		t.Fatal("reader physical charge leaked", mergeCallbackReads)
	}
}
func TestMergeWaitingWriterCancellationAndInstallRefusalDoNotRetireReaderEarly(t *testing.T) {
	oldID, oldInstall := navigationID, mergeInstallActive
	navigationID = 1942
	mergeInstallActive = false
	t.Cleanup(func() { navigationID, mergeInstallActive = oldID, oldInstall })
	release, err := mergeEnterCallbackRead()
	if err != nil {
		t.Fatal(err)
	}
	defer release()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan error, 1)
	go func() { done <- mergeAwaitCallbackReads(ctx, 1942) }()
	cancel()
	if err := <-done; !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	if mergeCallbackReads != 1 {
		t.Fatal("logical writer cancellation retired actual callback read", mergeCallbackReads)
	}
	// The finite core-only install has no ordinary event-loop interleaving. A
	// synchronous authored reentry is rejected explicitly rather than replayed.
	mergeInstallActive = true
	if next, err := mergeEnterCallbackRead(); !errors.Is(err, errPropsStale) || next != nil {
		t.Fatal("install admitted partial-frame read", err)
	}
	mergeInstallActive = false
	release()
	if mergeCallbackReads != 0 {
		t.Fatal("reader release was not exact")
	}
}

func TestMergeNativeLocationSynchronizationWaitsForOwnedCallbackReads(t *testing.T) {
	oldLease, oldPending, oldIntent, oldID := mergeActiveLease, mergeNativePending, mergeNativeIntent, navigationID
	oldRequested, oldRequestedIntent := mergeNativeRequestedPending, mergeNativeRequestedIntent
	mergeActiveLease, mergeNativePending, mergeNativeRequestedPending = nil, nil, nil
	navigationID = 1943
	t.Cleanup(func() {
		mergeActiveLease, mergeNativePending, mergeNativeIntent, navigationID = oldLease, oldPending, oldIntent, oldID
		mergeNativeRequestedPending, mergeNativeRequestedIntent = oldRequested, oldRequestedIntent
	})
	release, err := mergeEnterCallbackRead()
	if err != nil {
		t.Fatal(err)
	}
	defer release()
	calls := 0
	if !mergeDeferNativeCommit(mergeNativeRequested, "", func() { calls++ }) || calls != 0 {
		t.Fatal("native hash metadata tore an owned callback")
	}
	release()
	release()
	if calls != 1 || mergeNativeRequestedPending != nil {
		t.Fatal("native metadata was lost/repeated", calls)
	}
}

func TestMergeDeferredPopKeepsEntryFocusScrollAndNewerFailureThroughDuplicateHash(t *testing.T) {
	propsBrowserEnvironment(t)
	oldWin, oldDoc, oldURL, oldState := win, doc, currentURL, currentState
	oldLease, oldCurrent, oldID, oldCancel := mergeActiveLease, mergeCurrent, navigationID, cancelNavigation
	oldPending, oldIntent, oldKind, oldTarget := mergeNativePending, mergeNativeIntent, mergeNativePendingKind, mergeNativeTarget
	oldRevision, oldPrivacy, oldDocument := mergeNativeRevision, mergeNativePrivacy, mergeNativeDocument
	oldRequested, oldRequestedIntent := mergeNativeRequestedPending, mergeNativeRequestedIntent
	oldHistory, oldEntries, oldOrder := ownedHistory, mergeEntryHistory, mergeEntryOrder
	oldRetained, oldObservers, oldLazy := retainedCurrent, locationObservers, lazyModules
	defer func() {
		win, doc, currentURL, currentState = oldWin, oldDoc, oldURL, oldState
		mergeActiveLease, mergeCurrent, navigationID, cancelNavigation = oldLease, oldCurrent, oldID, oldCancel
		mergeNativePending, mergeNativeIntent, mergeNativePendingKind, mergeNativeTarget = oldPending, oldIntent, oldKind, oldTarget
		mergeNativeRevision, mergeNativePrivacy, mergeNativeDocument = oldRevision, oldPrivacy, oldDocument
		mergeNativeRequestedPending, mergeNativeRequestedIntent = oldRequested, oldRequestedIntent
		ownedHistory, mergeEntryHistory, mergeEntryOrder = oldHistory, oldEntries, oldOrder
		retainedCurrent, locationObservers, lazyModules = oldRetained, oldObservers, oldLazy
	}()
	fixture := clientOnlyDOM(t)
	app, root, input := fixture.Get("outer"), fixture.Get("root"), fixture.Get("fallback")
	app.Call("setAttribute", "id", "nextgo-app")
	body := fixture.Call("make", "body")
	body.Call("appendChild", app)
	status := js.Global().Get("Object").New()
	status.Set("hidden", false)
	status.Set("textContent", "Navigation failed. Try again or reload.")
	doc = fixture.Get("owner")
	doc.Set("body", body)
	doc.Set("activeElement", body)
	get := js.FuncOf(func(_ js.Value, args []js.Value) any {
		if args[0].String() == "nextgo-app" {
			return app
		}
		if args[0].String() == "nextgo-status" {
			return status
		}
		return js.Null()
	})
	defer get.Release()
	doc.Set("getElementById", get)
	win = js.Global().Get("Object").New()
	win.Set("Object", js.Global().Get("Object"))
	win.Set("URL", js.Global().Get("URL"))
	start, target := "https://props.test/a#one", "https://props.test/a#two"
	location := js.Global().Get("Object").New()
	location.Set("href", target)
	win.Set("location", location)
	key := "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
	state := js.Global().Get("Object").New()
	state.Set("nextgoEntry", key)
	state.Set("nextgoX", 0)
	state.Set("nextgoY", 500)
	history := js.Global().Get("Object").New()
	history.Set("state", state)
	win.Set("history", history)
	writes := 0
	replace := js.FuncOf(func(_ js.Value, args []js.Value) any { writes++; history.Set("state", args[0]); return nil })
	defer replace.Release()
	history.Set("replaceState", replace)
	scroll := js.FuncOf(func(_ js.Value, args []js.Value) any {
		win.Set("scrollX", args[0])
		win.Set("scrollY", args[1])
		return nil
	})
	defer scroll.Release()
	win.Set("scrollTo", scroll)
	win.Set("scrollX", 0)
	win.Set("scrollY", 25)
	ownedHistory = historyRegistry{}
	if !ownedHistory.put(key, historyRecord{target, start}) {
		t.Fatal("history fixture refused")
	}
	scope := &Scope{Root: Element{root}}
	instance := scope.mergeInstance()
	root.Set("scrollLeft", 0)
	root.Set("scrollTop", 0)
	mergeCurrent = &mergeDocument{privacy: mergePrivacy, boundaries: map[string]*mergeBoundary{"a": {scope: scope, root: root, instance: instance, valid: true}}}
	mergeEntryHistory = map[string]*mergeEntryFocus{key: {entry: key, url: target, scope: scope, instance: instance, node: input, selection: true, start: 1, end: 4, direction: "forward", scroll: []retainedScroll{{node: root, y: 160}}}}
	mergeEntryOrder = []string{key}
	mergeActiveLease, mergeNativePending, mergeNativeRequestedPending = nil, nil, nil
	retainedCurrent = nil
	locationObservers = nil
	lazyModules = nil
	navigationID = 1944
	currentURL = start
	setLocation(start)
	canceled := false
	cancelNavigation = func() { canceled = true }
	release, err := mergeEnterCallbackRead()
	if err != nil {
		t.Fatal(err)
	}
	defer release()
	if !mergeDeferNativeCommit(mergeNativePop, target, func() { commitMergeNativePop(target, state, 1944) }) {
		t.Fatal("held native pop was not deferred")
	}
	// A scroll or a newer request's save must not relabel the already traversed
	// incoming history entry with the still-visible old committed URL.
	saveScroll()
	if writes != 0 || state.Get("nextgoEntry").String() != key {
		t.Fatal("pending native traversal corrupted history")
	}
	if !mergeDeferNativeCommit(mergeNativeHash, target, func() { commitMergeNativeHash(target, 1944) }) {
		t.Fatal("duplicate hash was not coalesced")
	}
	requestedCalls := 0
	if !mergeDeferNativeCommit(mergeNativeRequested, "https://props.test/a#three", func() { requestedCalls++ }) {
		t.Fatal("later fragment request was not bounded/deferred")
	}
	navigationID++ // C is only a newer intent; its network then fails, no commit.
	release()
	committed, ready := CurrentLocation()
	if currentURL != target || !ready || committed.URL != target || committed.Hash != "#two" || !currentState.Equal(state) || !history.Get("state").Equal(state) {
		t.Fatal("native location/entry metadata lost", currentURL, committed)
	}
	if win.Get("scrollY").Int() != 500 || root.Get("scrollTop").Int() != 160 || !doc.Get("activeElement").Equal(input) || input.Get("selectionStart").Int() != 1 || input.Get("selectionEnd").Int() != 4 {
		t.Fatal("deferred pop lost native scroll/focus/selection")
	}
	if requestedCalls != 0 {
		t.Fatal("older requested fragment overrode newer C intent")
	}
	if canceled || status.Get("hidden").Bool() || status.Get("textContent").String() != "Navigation failed. Try again or reload." || writes != 1 {
		t.Fatal("native metadata canceled/replaced newer failed navigation status", canceled, writes)
	}
	// Native input selection already survives a stationary owner. Repeated pop
	// restoration must not invoke a redundant native setter; a changed selection
	// still restores the accepted entry rather than silently skipping it.
	originalSelection := input.Get("setSelectionRange")
	selectionWrites := 0
	observeSelection := js.FuncOf(func(_ js.Value, args []js.Value) any {
		selectionWrites++
		originalSelection.Call("call", input, args[0], args[1], args[2])
		return nil
	})
	input.Set("setSelectionRange", observeSelection)
	t.Cleanup(func() { input.Set("setSelectionRange", originalSelection); observeSelection.Release() })
	restoreMergeHistory(true, state, false)
	if selectionWrites != 0 {
		t.Fatal("unchanged native merge selection was rewritten", selectionWrites)
	}
	input.Set("selectionStart", 2)
	restoreMergeHistory(true, state, false)
	if selectionWrites != 1 || input.Get("selectionStart").Int() != 1 || input.Get("selectionEnd").Int() != 4 || input.Get("selectionDirection").String() != "forward" {
		t.Fatal("changed native merge selection was not restored", selectionWrites)
	}
	input.Set("selectionDirection", "backward")
	restoreMergeHistory(true, state, false)
	if selectionWrites != 2 || input.Get("selectionDirection").String() != "forward" {
		t.Fatal("changed merge selection direction was not restored", selectionWrites)
	}
}

func TestMergePrivacyRevokesOrdinaryAndInflightMountBeforePhysicalSettlement(t *testing.T) {
	propsBrowserEnvironment(t)
	mergeQueueTestState(t)
	oldDoc, oldWin := doc, win
	oldEntries, oldOrder := mergeEntryHistory, mergeEntryOrder
	t.Cleanup(func() { doc, win = oldDoc, oldWin; mergeEntryHistory, mergeEntryOrder = oldEntries, oldOrder })
	fixture := clientOnlyDOM(t)
	app := fixture.Get("outer")
	body := fixture.Call("make", "body")
	body.Call("appendChild", app)
	ordinaryNode, outsideNode := fixture.Call("make", "button"), fixture.Call("make", "button")
	app.Call("appendChild", ordinaryNode)
	body.Call("appendChild", outsideNode)
	status := js.Global().Get("Object").New()
	doc = fixture.Get("owner")
	doc.Set("body", body)
	get := js.FuncOf(func(_ js.Value, args []js.Value) any {
		if args[0].String() == "nextgo-app" {
			return app
		}
		if args[0].String() == "nextgo-status" {
			return status
		}
		return js.Null()
	})
	t.Cleanup(get.Release)
	doc.Set("getElementById", get)
	win = js.Global().Get("Object").New()
	win.Set("console", js.Global().Get("console"))
	retained, ordinary, outside := &Scope{Root: Element{fixture.Get("root")}}, &Scope{Root: Element{ordinaryNode}}, &Scope{Root: Element{outsideNode}}
	current := &mergeDocument{privacy: mergePrivacy, boundaries: map[string]*mergeBoundary{"retained": {scope: retained, root: retained.Root.Value, valid: true}}, scopes: map[*Scope]struct{}{retained: {}}}
	mergeCurrent = current
	calls, cleanups, outsideCalls := 0, 0, 0
	outside.On(Element{outsideNode}, "click", func(Event) { outsideCalls++ })
	started, release, settled := make(chan struct{}), make(chan struct{}), make(chan struct{})
	result := make(chan error, 1)
	go func() {
		defer close(settled)
		result <- withMergeMountReport(ordinary, func() error {
			ordinary.On(Element{ordinaryNode}, "click", func(Event) { calls++ })
			ordinary.Cleanup(func() { cleanups++ })
			close(started)
			<-release
			return nil
		})
	}()
	t.Cleanup(func() {
		select {
		case <-release:
		default:
			close(release)
		}
		<-settled
		ordinary.dispose()
		outside.dispose()
		propsWait(t, func() bool { return propsWorkerCount() == 0 && mergeCallbackReads == 0 })
	})
	<-started
	if _, exists := current.scopes[ordinary]; !exists {
		t.Fatal("in-flight ordinary Mount was not captured before callback")
	}
	if mergeCallbackReads != 1 {
		t.Fatal("Mount did not own actual read", mergeCallbackReads)
	}
	revokeMergeDocument()
	ordinaryNode.Call("dispatchEvent", js.Global().Get("Event").New("click"))
	outsideNode.Call("dispatchEvent", js.Global().Get("Event").New("click"))
	if calls != 0 || outsideCalls != 1 || ordinary.active() || retained.active() || !outside.active() || !app.Get("hidden").Bool() || !app.Call("hasAttribute", "inert").Bool() {
		t.Fatal("privacy revocation missed ordinary/inflight or touched outside owner", calls, outsideCalls)
	}
	if cleanups != 0 || mergeCallbackReads != 1 || propsWorkerCount() != 1 {
		t.Fatal("logical privacy fence waited/retired physical Mount", cleanups, mergeCallbackReads, propsWorkerCount())
	}
	close(release)
	if err := <-result; !errors.Is(err, errPropsClosed) {
		t.Fatal("late successful old Mount retained authority", err)
	}
	<-settled
	ordinary.dispose()
	propsWait(t, func() bool { return propsWorkerCount() == 0 && mergeCallbackReads == 0 })
	if cleanups != 1 {
		t.Fatal("terminal cleanup did not settle once", cleanups)
	}
}

func TestMergeBindingCapturePreservesPublishedOwnersUntilPrivacyRevocation(t *testing.T) {
	propsBrowserEnvironment(t)
	oldDoc, oldWin, oldMounted, oldCurrent, oldPrivacy := doc, win, mounted, mergeCurrent, mergePrivacy
	oldEntries, oldOrder := mergeEntryHistory, mergeEntryOrder
	t.Cleanup(func() {
		doc, win, mounted, mergeCurrent, mergePrivacy = oldDoc, oldWin, oldMounted, oldCurrent, oldPrivacy
		mergeEntryHistory, mergeEntryOrder = oldEntries, oldOrder
	})
	clicks := 0
	scope, source, fixture := propsClientRoot(t, `7`, PropsOptions[int]{Clone: func(value int) int { return value }}, func(child *Scope, _ *PropsSource[int]) error {
		button, err := child.Require("[data-click]")
		if err != nil {
			return err
		}
		child.On(button, "click", func(Event) { clicks++ })
		return nil
	})
	app := fixture.Get("outer")
	body := fixture.Call("make", "body")
	body.Call("appendChild", app)
	ordinaryNode := fixture.Call("make", "button")
	app.Call("appendChild", ordinaryNode)
	ordinary := &Scope{Root: Element{ordinaryNode}}
	t.Cleanup(ordinary.dispose)
	ordinaryClicks := 0
	ordinary.On(Element{ordinaryNode}, "click", func(Event) { ordinaryClicks++ })
	status := js.Global().Get("Object").New()
	doc = fixture.Get("owner")
	doc.Set("body", body)
	get := js.FuncOf(func(_ js.Value, args []js.Value) any {
		if args[0].String() == "nextgo-app" {
			return app
		}
		if args[0].String() == "nextgo-status" {
			return status
		}
		return js.Null()
	})
	t.Cleanup(get.Release)
	doc.Set("getElementById", get)
	boundary := &mergeBoundary{root: scope.Root.Value}
	mergeCurrent = &mergeDocument{privacy: mergePrivacy, boundaries: map[string]*mergeBoundary{"client": boundary}, scopes: map[*Scope]struct{}{scope: {}, ordinary: {}}}
	mounted = []*Scope{scope, ordinary}
	for i := 0; i < 2; i++ {
		captureMergeBindings()
		snapshot, err := source.GetSnapshot()
		if err != nil || snapshot.Value != 7 || !scope.active() || !ordinary.active() || !boundary.valid || boundary.scope != scope || boundary.instance != scope.mergeInstance() {
			t.Fatal("ordinary binding capture revoked a published owner", snapshot, err, boundary.valid)
		}
		fixture.Get("button").Call("dispatchEvent", js.Global().Get("Event").New("click"))
		ordinaryNode.Call("dispatchEvent", js.Global().Get("Event").New("click"))
	}
	if clicks != 2 || ordinaryClicks != 2 {
		t.Fatal("binding capture disabled published events", clicks, ordinaryClicks)
	}
	revokeMergeDocument()
	fixture.Get("button").Call("dispatchEvent", js.Global().Get("Event").New("click"))
	ordinaryNode.Call("dispatchEvent", js.Global().Get("Event").New("click"))
	if _, err := source.GetSnapshot(); !errors.Is(err, errPropsClosed) || scope.active() || ordinary.active() || boundary.valid || clicks != 2 || ordinaryClicks != 2 || !app.Get("hidden").Bool() || !app.Call("hasAttribute", "inert").Bool() {
		t.Fatal("recognized privacy revocation did not fence published owners", err, clicks, ordinaryClicks)
	}
}

func TestMergeWorkerAuthorityKeepsTransportPublicationAndPhysicalCancellation(t *testing.T) {
	propsBrowserEnvironment(t)
	oldCurrent, oldPrivacy, oldID, oldEpoch := mergeCurrent, mergePrivacy, navigationID, cacheEpoch
	t.Cleanup(func() { mergeCurrent, mergePrivacy, navigationID, cacheEpoch = oldCurrent, oldPrivacy, oldID, oldEpoch })
	mergePrivacy = 73
	navigationID = 9271
	cacheEpoch = 41
	prior := &mergeDocument{privacy: mergePrivacy}
	mergeCurrent = prior
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	preparing := newMergePreparationFence(ctx, prior, navigationID, cacheEpoch, mergePrivacy)
	valid := preparing.valid
	if !valid() {
		t.Fatal("fresh preparation refused")
	}
	navigationID++
	if valid() {
		t.Fatal("superseded preparation retained transport authority")
	}
	navigationID--
	cacheEpoch++
	if valid() {
		t.Fatal("old cache epoch retained transport authority")
	}
	cacheEpoch--
	cancel()
	if valid() {
		t.Fatal("canceled preparation retained transport authority")
	}
	usefulContext, cancelUseful := context.WithCancel(context.Background())
	t.Cleanup(cancelUseful)
	useful := newMergePreparationFence(usefulContext, prior, navigationID, cacheEpoch, mergePrivacy)
	useful.started = true
	cancelUseful()
	navigationID++
	cacheEpoch++
	if !useful.valid() {
		t.Fatal("new transport intent interrupted useful installed publication")
	}
	prior.revoked = true
	if useful.valid() {
		t.Fatal("revoked document retained installed authority")
	}
	prior.revoked = false
	mergePrivacy++
	if useful.valid() {
		t.Fatal("privacy change retained installed authority")
	}
	mergePrivacy--
	next := &mergeDocument{privacy: mergePrivacy}
	published := mergePublishedAuthority(next, mergePrivacy)
	if published() {
		t.Fatal("unpublished document granted frame authority")
	}
	mergeCurrent = next
	if !published() {
		t.Fatal("published exact document refused")
	}
	navigationID++
	cacheEpoch++
	if !published() {
		t.Fatal("ordinary newer intent revoked useful published frame")
	}
	next.revoked = true
	if published() {
		t.Fatal("revoked published document remained authorized")
	}
	next.revoked = false
	mergeCurrent = &mergeDocument{privacy: mergePrivacy}
	if published() {
		t.Fatal("different same-privacy document reused published authority")
	}
	if mergeDocumentToken(prior) == mergeDocumentToken(mergeCurrent) {
		t.Fatal("distinct documents reused identity")
	}
	nilPublished := mergePublishedAuthority(nil, mergePrivacy)
	if nilPublished() {
		t.Fatal("nil-document frame matched a live document")
	}
	mergeCurrent = nil
	if !nilPublished() {
		t.Fatal("authorized nil-document frame refused")
	}
	mergePrivacy++
	if nilPublished() {
		t.Fatal("nil-document frame survived privacy change")
	}
	mergePrivacy--
	// Logical cancellation does not release the actual worker or its selected
	// DTO estimate. Only scalar/token authority reaches this validity callback.
	mergeCurrent = prior
	heldContext, cancelHeld := context.WithCancel(context.Background())
	held := newMergePreparationFence(heldContext, prior, navigationID, cacheEpoch, mergePrivacy)
	started, release := make(chan struct{}), make(chan struct{})
	result := make(chan error, 1)
	t.Cleanup(func() {
		cancelHeld()
		select {
		case <-release:
		default:
			close(release)
		}
		propsWait(t, func() bool { return propsWorkerCount() == 0 && propsWorkerBytes() == 0 })
	})
	go func() {
		_, err := propsInvokeWeighted(heldContext, held.valid, 1, func() (int, error) { close(started); <-release; return 1, nil })
		result <- err
	}()
	<-started
	cancelHeld()
	if err := <-result; !errors.Is(err, errPropsStale) {
		t.Fatal("logical canceled worker did not settle caller", err)
	}
	mergeCurrent = next
	if held.valid() || propsWorkerCount() != 1 || propsWorkerBytes() != 1 {
		t.Fatal("document replacement retired blocked physical work", propsWorkerCount(), propsWorkerBytes())
	}
	close(release)
	propsWait(t, func() bool { return propsWorkerCount() == 0 && propsWorkerBytes() == 0 })
}
