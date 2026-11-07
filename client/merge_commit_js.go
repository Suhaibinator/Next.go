//go:build js && wasm

package client

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"syscall/js"
	"unicode/utf8"

	"github.com/Suhaibinator/Next.go/internal/navigation"
	"github.com/Suhaibinator/Next.go/internal/wirejson"
)

func validateMergeEnvelope(body []byte, data *envelope) error {
	if routeManifest.MergeVersion == 0 && len(data.Merge) == 0 {
		return nil
	}
	if !utf8.Valid(body) || wirejson.Unique(body) != nil {
		return fmt.Errorf("invalid merge envelope")
	}
	fields := map[string]json.RawMessage{}
	if json.Unmarshal(body, &fields) != nil {
		return fmt.Errorf("invalid merge envelope")
	}
	for name := range fields {
		if name != "merge" && strings.EqualFold(name, "merge") {
			return fmt.Errorf("aliased merge field")
		}
	}
	raw, exists := fields["merge"]
	if !exists {
		data.Merge = nil
		data.merge = nil
		return nil
	}
	if routeManifest.MergeVersion != navigation.MergeVersion || data.Status != 200 || strings.TrimSpace(string(raw)) == "null" {
		return fmt.Errorf("invalid merge descriptor presence")
	}
	value, err := navigation.ParseMerge(string(raw), data.HTML)
	if err != nil || value == nil {
		return fmt.Errorf("invalid merge descriptor")
	}
	data.Merge = raw
	data.merge = value
	return nil
}
func withMergeMountReport(scope *Scope, work func() error) (err error) {
	id := navigationID
	if mergeActiveLease != nil {
		id = mergeActiveLease.intent
	}
	current := mergeCurrent
	if current == nil || current.revoked || current.privacy != mergePrivacy {
		return errPropsClosed
	}
	app := doc.Call("getElementById", "nextgo-app")
	if scope == nil || app.IsNull() || !app.Call("contains", scope.Root.Value).Bool() {
		return errPropsInvalid
	}
	if current.scopes == nil {
		current.scopes = map[*Scope]struct{}{}
	}
	if len(current.scopes) >= retainedViewNodes {
		return errPropsCapacity
	}
	// Register before authored Mount may yield: ordinary roots and in-flight
	// ClientView roots are document-owned even before entering mounted/bindings.
	current.scopes[scope] = struct{}{}
	defer func() {
		if err != nil {
			delete(current.scopes, scope)
		}
	}()
	if err := scope.enableMergeCallbacks(); err != nil {
		return err
	}
	err = withRetainedScopeReport(scope, id, func() error {
		var workErr error
		if err := runMergeCallback(func() { workErr = work() }); err != nil {
			return err
		}
		return workErr
	})
	if mergeCurrent != current || current.revoked || current.privacy != mergePrivacy {
		scope.revokeMergeAuthority()
		return errPropsClosed
	}
	return err
}

// Lightweight boot diagnostics preserve the original returned Mount error and
// need no worker reservation on no-Props paths. Scoped reports are captured so
// a yielding initial Mount cannot overwrite a newer request's loading/error.
func withMergeMountDiagnostics(scope *Scope, work func() error) error {
	id := navigationID
	if mergeActiveLease != nil {
		id = mergeActiveLease.intent
	}
	var original error
	captured := withRetainedScopeReport(scope, id, func() error { original = work(); return original })
	if original != nil {
		return original
	}
	return captured
}
func withMergeCleanupReport(scope *Scope, work func() error) error {
	id := navigationID
	if mergeActiveLease != nil {
		id = mergeActiveLease.intent
	}
	// Framework teardown loops execute directly; their individually registered
	// authored callbacks already have owned guards, even after logical revoke.
	return withRetainedScopeReport(scope, id, work)
}
func mergeHydrationDiagnostic(root js.Value, err error) {
	win.Get("console").Call("error", fmt.Sprintf("Next.go component update: %v", err))
	if mergeApplicationRevoked() || (mergeCurrent != nil && mergeCurrent.revoked) {
		return
	}
	if root.Type() == js.TypeObject && !root.IsNull() {
		if mergeCurrent != nil {
			for _, boundary := range mergeCurrent.boundaries {
				if boundary.root.Equal(root) {
					boundary.valid = false
				}
			}
		}
	}
	if mergeActiveLease == nil || mergeActiveLease.intent == navigationID {
		status := doc.Call("getElementById", "nextgo-status")
		status.Set("hidden", false)
		status.Set("textContent", mergePublicFailure)
	}
}
func mergeRetainMounted(scopes []*Scope) {
	keep := mounted[:0]
	for _, scope := range mounted {
		retired := false
		for _, old := range scopes {
			if old == scope {
				retired = true
				break
			}
		}
		if !retired {
			keep = append(keep, scope)
		}
	}
	mounted = keep
}
func mergeRecoverFailures(current *mergeDocument, failures []propsFailure) {
	if current == nil || mergeCurrent != current || current.revoked || current.privacy != mergePrivacy {
		return
	}
	seen := map[*Scope]bool{}
	for _, failure := range failures {
		scope := mergeRootScope(failure.Scope)
		if scope == nil || seen[scope] {
			continue
		}
		seen[scope] = true
		var boundary *mergeBoundary
		for _, candidate := range current.boundaries {
			if candidate.scope == scope {
				boundary = candidate
				break
			}
		}
		if boundary == nil {
			continue
		}
		boundary.valid = false
		scope.revokeMergeAuthority()
		mergeRetainMounted([]*Scope{scope})
		_ = withMergeCleanupReport(scope, func() error { scope.dispose(); return nil })
		if mergeCurrent != current || current.revoked || current.privacy != mergePrivacy {
			return
		}
		// The fallback is the newly accepted server fallback, never the initial
		// mount transaction's detached nodes. No stale State or event is replayed.
		fragment := doc.Call("createDocumentFragment")
		for boundary.fallback.Get("firstChild").Truthy() {
			fragment.Call("appendChild", boundary.fallback.Get("firstChild"))
		}
		boundary.root.Call("replaceChildren", fragment)
		boundary.root.Call("setAttribute", "data-nextgo-hydrated", "error")
		boundary.root.Call("setAttribute", "data-nextgo-merge-error", "1")
		doc.Get("body").Call("setAttribute", "data-nextgo-hydration", "error")
		mergeHydrationDiagnostic(boundary.root, failure.Err)
	}
}
func closeMergeRetiring(scopes []*Scope, current *mergeDocument) {
	for _, scope := range scopes {
		if err := withMergeCleanupReport(scope, func() error { scope.dispose(); return nil }); err != nil {
			mergeHydrationDiagnostic(js.Null(), err)
		}
		// Terminal cleanup cannot interrupt useful B merely by starting C. A
		// privileged privacy reset alone prevents any further old-frame UI work.
		if current != nil && (mergeCurrent != current || current.revoked || current.privacy != mergePrivacy) {
			return
		}
	}
}
func installMergeHead(data envelope, head js.Value) {
	doc.Get("documentElement").Call("setAttribute", "lang", data.Document.Language)
	doc.Get("documentElement").Call("setAttribute", "dir", data.Document.Direction)
	doc.Call("querySelector", `meta[name="viewport"]`).Call("setAttribute", "content", data.Document.Viewport)
	doc.Set("title", data.Title)
	doc.Call("querySelector", `meta[name="description"]`).Call("setAttribute", "content", data.Description)
	for _, selector := range []string{"meta[data-nextgo-meta]", "[data-nextgo-head]"} {
		nodes := doc.Call("querySelectorAll", selector)
		for i := 0; i < nodes.Length(); i++ {
			nodes.Index(i).Call("remove")
		}
	}
	doc.Get("head").Call("appendChild", head)
	for name, value := range data.Meta {
		node := doc.Call("createElement", "meta")
		node.Call("setAttribute", "data-nextgo-meta", "")
		node.Call("setAttribute", "name", name)
		node.Call("setAttribute", "content", value)
		doc.Get("head").Call("appendChild", node)
	}
}
func installMergeHistory(target, previous string, pop bool, state js.Value, replace bool) {
	if !pop {
		method := "pushState"
		if replace {
			method = "replaceState"
		}
		win.Get("history").Call(method, navigationHistoryState(target, previous, replace), "", target)
	} else if win.Get("location").Get("href").String() != target {
		prior, _ := trackedHistory(state, win.Get("location").Get("href").String())
		win.Get("history").Call("replaceState", newHistoryState(target, prior.Previous), "", target)
	}
	currentURL = target
	setLocation(target)
}
func commitStationaryMerge(ctx context.Context, parsed, root js.Value, data envelope, target string, pop bool, state js.Value, replace bool) (handled bool, err error) {
	if mergeContainsLazyBoundary(root) || mergeContainsLazyBoundary(parsed.Get("body")) {
		return false, nil
	}
	plan, err := prepareMergeDOM(ctx, root, parsed.Get("body"), data, target)
	if err != nil || plan == nil {
		return false, err
	}
	prior := plan.previous
	defer func() {
		mergeInstallActive = false
		var failures []propsFailure
		if plan.cohort != nil {
			failures = plan.cohort.Close()
		}
		if plan.installed {
			mergeRecoverFailures(plan.next, failures)
		} else {
			mergeRecoverFailures(prior, failures)
		}
		for _, ticket := range plan.prepared {
			ticket.Close()
		}
	}()
	head, err := buildHead(data.Head)
	if err != nil {
		return true, err
	}
	if data.metadata != nil {
		if err := data.metadata.capture(head); err != nil {
			return true, err
		}
		data.metadata.committedURL = target
	}
	snapshot, err := selectionNodes(parsed)
	if err != nil {
		return true, err
	}
	previous, active := currentURL, doc.Get("activeElement")
	stopStreaming()
	if ctx.Err() != nil || plan.beforeID != navigationID || plan.beforeEpoch != cacheEpoch || mergeCurrent != prior || prior.revoked || plan.privacy != mergePrivacy {
		return true, context.Canceled
	}
	if err := mergeAwaitCallbackReads(ctx, plan.beforeID); err != nil {
		return true, err
	}
	if plan.beforeEpoch != cacheEpoch || mergeCurrent != prior || prior.revoked || plan.privacy != mergePrivacy {
		return true, context.Canceled
	}
	// No authored callback or await occurs from this reader barrier through the
	// single frame publication. Native events keep their synchronous semantics;
	// surprising synchronous dispatch during this core-only section is recovery.
	mergeInstallActive = true
	pre := plan.fence.valid
	post := mergePublishedAuthority(plan.next, plan.privacy)
	plan.cohort, err = preparePropsCohort(plan.scopes, plan.prepared, mergeLocation(target), plan.next.revision, propsGuards{Precommit: pre, Published: post, Retiring: plan.retiring})
	if err != nil {
		return true, err
	}
	if err := validatePreparedMergePlan(plan, data); err != nil {
		if err != errMergeUnsupported {
			return true, err
		}
		return false, nil
	}
	if plan.staticBytes+propsRegisteredBytes+propsWorkerByteLimit+propsTransientBytes() > retainedMetadataBytes {
		return true, errRetainedCapacity
	}
	// From the first live mutation, new request intent cannot veto completion.
	// Only privileged identity revocation can stop/hide this accepted cohort.
	plan.fence.started = true
	for _, scope := range plan.retiring {
		scope.revokeMergeAuthority()
	}
	mergeRetainMounted(plan.retiring)
	installMergeCarrier(plan.tree, true)
	installMergeHead(data, head)
	installMergeHistory(target, previous, pop, state, replace)
	notifySelections := installSelectionsSilent(snapshot)
	if plan.privacy != mergePrivacy || prior.revoked {
		return true, context.Canceled
	}
	if err := plan.cohort.InstallFrame(); err != nil {
		return true, err
	}
	mergeCurrent = plan.next
	plan.installed = true
	installMergeApplicationAuthority(root, plan.next, data.Retention)
	if mergeActiveLease != nil {
		mergeActiveLease.published = plan.next
	}
	mergePublishDescriptor(data.merge)
	mergeShowAuthorized()
	mergeInstallActive = false
	authority := post
	failures := plan.cohort.Publish()
	failures = append(failures, plan.cohort.Close()...)
	mergeRecoverFailures(plan.next, failures)
	mergeRecoverFailures(plan.next, flushMergeNotifications(authority))
	if !authority() {
		return true, nil
	}
	// New/cold roots mount only after every retained source installed/published.
	// The document lease keeps newer C commits out until B is fully useful.
	hydrateManaged(mergeHydrationDiagnostic, authority)
	captureMergeBindings()
	retireMergeHistory(plan.next)
	if !authority() {
		return true, nil
	}
	closeMergeRetiring(plan.retiring, plan.next)
	if !authority() {
		return true, nil
	}
	mergeRecoverFailures(plan.next, flushMergeNotifications(authority))
	notifySelections()
	if !authority() {
		return true, nil
	}
	syncNavigationLinks(target)
	if mergeActiveLease == nil || mergeActiveLease.intent == navigationID {
		if !applicationHydrationError() {
			doc.Call("getElementById", "nextgo-status").Set("hidden", true)
		}
	}
	completeAccessibleNavigation(previous, target, active, data.Title)
	restoreScroll(target, pop, state)
	restoreMergeHistory(pop, state, previous == target)
	saveScroll()
	notifyLocationObservers()
	mergeRecoverFailures(plan.next, flushMergeNotifications(authority))
	return true, nil
}

// Cold entry is still a fresh server render. It installs the accepted props
// revision before Mount; no old source is silently re-stamped into this frame.
func installColdMergeFrame(data envelope, root js.Value, target string, privacy uint64) error {
	revision := propsDocumentRevision() + 1
	if revision == 0 {
		return fmt.Errorf("props revision exhausted")
	}
	var next *mergeDocument
	if data.merge != nil && mergeRuntimeEnabled() && !mergeContainsLazyBoundary(root) {
		value, err := mergeDocumentFromDOM(data.merge, root, revision, data.HTML, 0)
		if err == nil {
			next = value
		}
	}
	prior := mergeCurrent
	pre := func() bool { return mergePrivacy == privacy }
	post := mergePublishedAuthority(next, privacy)
	cohort, err := preparePropsCohort(nil, nil, mergeLocation(target), revision, propsGuards{Precommit: pre, Published: post})
	if err != nil {
		return err
	}
	defer cohort.Close()
	if err := cohort.InstallFrame(); err != nil {
		return err
	}
	mergeCurrent = next
	installMergeApplicationAuthority(root, next, data.Retention)
	if next == nil {
		mergeEntryHistory = map[string]*mergeEntryFocus{}
		mergeEntryOrder = nil
	} else {
		retireMergeHistory(next)
	}
	if mergeActiveLease != nil {
		mergeActiveLease.published = next
	}
	mergePublishDescriptor(data.merge)
	if mergePrivacy == privacy {
		mergeShowAuthorized()
	}
	_ = prior
	mergeInstallActive = false
	return nil
}
func commitMergeNativePop(target string, state js.Value, intent uint64) {
	if mergeApplicationRevoked() || (mergeCurrent != nil && mergeCurrent.revoked) {
		return
	}
	release, err := beginMergeNativePublication(intent)
	if err != nil {
		return
	}
	defer release()
	currentURL = target
	setLocation(target)
	currentState = state
	restoreScroll(target, true, state)
	restoreMergeHistory(true, state, false)
	saveScroll()
	notifyLocationObservers()
}

func commitMergeNativeHash(target string, intent uint64) {
	if mergeApplicationRevoked() || (mergeCurrent != nil && mergeCurrent.revoked) {
		return
	}
	release, err := beginMergeNativePublication(intent)
	if err != nil {
		return
	}
	defer release()
	currentURL = target
	setLocation(target)
	saveScroll()
	notifyLocationObservers()
}
