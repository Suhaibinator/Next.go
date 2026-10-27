//go:build js && wasm

package client

import (
	"context"
	"encoding/json"
	"fmt"
	"strconv"
	"syscall/js"

	"github.com/Suhaibinator/Next.go/internal/navigation"
)

// mergeDocument is authority for one accepted rendering, never a URL-to-State
// cache. Each boundary retains its own non-reused Scope/source instance.
type mergeDocument struct {
	identity          *mergeDocumentIdentity
	descriptor        *navigation.MergeDescriptor
	boundaries        map[string]*mergeBoundary
	scopes            map[*Scope]struct{}
	revision, privacy uint64
	revoked           bool
}

// Tokens contain no back-reference to their document, DOM, sibling props or
// prepared plan. A physically blocked clone may retain a validity method after
// logical cancellation without retaining an otherwise retired navigation plan.
// The non-zero-sized token keeps distinct allocated identities distinct.
type mergeDocumentIdentity struct{ marker byte }

func mergeDocumentToken(current *mergeDocument) *mergeDocumentIdentity {
	if current == nil {
		return nil
	}
	if current.identity == nil {
		current.identity = &mergeDocumentIdentity{}
	}
	return current.identity
}
func mergeTokenCurrent(token *mergeDocumentIdentity, privacy uint64) bool {
	if privacy != mergePrivacy {
		return false
	}
	current := mergeCurrent
	if token == nil {
		return current == nil
	}
	return current != nil && !current.revoked && current.privacy == privacy && current.identity == token
}

// Preparation carries only an identity token, scalar transport fences and the
// cancellation channel. The channel has no reference back to its Context or
// navigation plan. After installation starts, useful publication ignores newer
// transport intent while continuing to enforce document/privacy authority.
type mergePreparationFence struct {
	token                  *mergeDocumentIdentity
	canceled               <-chan struct{}
	intent, epoch, privacy uint64
	started                bool
}

func newMergePreparationFence(ctx context.Context, current *mergeDocument, intent, epoch, privacy uint64) *mergePreparationFence {
	return &mergePreparationFence{token: mergeDocumentToken(current), canceled: ctx.Done(), intent: intent, epoch: epoch, privacy: privacy}
}
func (f *mergePreparationFence) valid() bool {
	if f == nil || !mergeTokenCurrent(f.token, f.privacy) {
		return false
	}
	if f.started {
		return true
	}
	select {
	case <-f.canceled:
		return false
	default:
	}
	return f.intent == navigationID && f.epoch == cacheEpoch
}

type mergePublishedFence struct {
	token   *mergeDocumentIdentity
	privacy uint64
}

func (f mergePublishedFence) valid() bool { return mergeTokenCurrent(f.token, f.privacy) }
func mergePublishedAuthority(current *mergeDocument, privacy uint64) func() bool {
	return (mergePublishedFence{token: mergeDocumentToken(current), privacy: privacy}).valid
}

type mergeBoundary struct {
	island         navigation.MergeIsland
	address        string
	root, fallback js.Value
	scope          *Scope
	instance       uint64
	valid          bool
}
type mergeNotification struct {
	owner  *Scope
	key    string
	notify func()
}
type mergeCommitLease struct {
	done            chan struct{}
	intent          uint64
	published       *mergeDocument
	queue           map[string]mergeNotification
	order           []string
	delivering      string
	bypass          bool
	delivered       int
	callbackRelease func()
	failures        map[*Scope]error
	released        bool
}
type mergeWaiter struct {
	replaced chan struct{}
	closed   bool
}

var mergeCurrent *mergeDocument
var mergePrivacy uint64
var mergeActiveLease *mergeCommitLease
var mergeWaiting *mergeWaiter
var mergeInstallActive bool
var mergeBooting bool
var mergeNativePending func()
var mergeNativeIntent uint64
var mergeNativeRequestedPending func()
var mergeNativeRequestedIntent uint64

type mergeNativeKind uint8

const (
	mergeNativeRequested mergeNativeKind = iota
	mergeNativePop
	mergeNativeHash
)

var mergeNativePendingKind mergeNativeKind
var mergeNativeTarget string
var mergeNativeRevision, mergeNativePrivacy uint64
var mergeNativeDocument *mergeDocument

const mergeNotificationLimit = 512
const mergePublicFailure = "Could not update this component. Reload or navigate to recover."

func mergeRuntimeEnabled() bool {
	return routeManifest.MergeVersion == navigation.MergeVersion && retainedRoutingEnabled()
}
func mergeRootScope(scope *Scope) *Scope {
	if scope == nil {
		return nil
	}
	for scope.parent != nil {
		scope = scope.parent
	}
	return scope
}

// Commit callbacks read one frame until this lease completes. A newer ordinary
// request may run, but cannot tear a callback's props/Location/head reads.
func acquireMergeCommit(ctx context.Context, intent uint64, incoming bool) (func(), error) {
	for mergeActiveLease != nil {
		lease := mergeActiveLease
		waiter := &mergeWaiter{replaced: make(chan struct{})}
		if mergeWaiting != nil && !mergeWaiting.closed {
			mergeWaiting.closed = true
			close(mergeWaiting.replaced)
		}
		mergeWaiting = waiter
		select {
		case <-ctx.Done():
			if mergeWaiting == waiter {
				mergeWaiting = nil
			}
			return nil, ctx.Err()
		case <-waiter.replaced:
			return nil, context.Canceled
		case <-lease.done:
			if mergeWaiting == waiter {
				mergeWaiting = nil
			}
		}
		if intent != navigationID {
			return nil, context.Canceled
		}
	}
	lease := &mergeCommitLease{done: make(chan struct{}), intent: intent, queue: map[string]mergeNotification{}, failures: map[*Scope]error{}}
	mergeActiveLease = lease
	if incoming || mergeCurrent != nil {
		if err := ensureMergeCallbackCapacity(); err != nil {
			mergeActiveLease = nil
			return nil, err
		}
	}
	return func() { releaseMergeLease(lease) }, nil
}
func releaseMergeLease(lease *mergeCommitLease) {
	if lease.released {
		return
	}
	lease.released = true
	// A canceled PRECOMMIT B must still deliver queued useful A notifications.
	// Published B also drains mandatory owned notifications before C commits.
	current := mergeCurrent
	authority := func() bool {
		return mergeCurrent == current && (current == nil || (!current.revoked && current.privacy == mergePrivacy))
	}
	if !mergeInstallActive && authority() {
		mergeRecoverFailures(current, flushMergeNotifications(authority))
	}
	if mergeActiveLease == lease {
		mergeInstallActive = false
		mergeActiveLease = nil
	}
	if lease.callbackRelease != nil {
		lease.callbackRelease()
		lease.callbackRelease = nil
	}
	close(lease.done)
	// Native committed-location synchronization is framework metadata, not
	// replay of an application mutation or event handler.
	mergeFinishNativeIfIdle()
}

// Native metadata publication also finishes observers before a writer may
// replace its frame. This lightweight lease reserves no callback worker pool.
func beginMergeNativePublication(intent uint64) (func(), error) {
	if moduleMode || (mergeCurrent == nil && len(propsBindings) == 0) {
		return func() {}, nil
	}
	if mergeActiveLease != nil || mergeCallbackReads != 0 {
		return nil, errPropsStale
	}
	lease := &mergeCommitLease{done: make(chan struct{}), intent: intent, published: mergeCurrent, queue: map[string]mergeNotification{}, failures: map[*Scope]error{}}
	mergeActiveLease = lease
	return func() { releaseMergeLease(lease) }, nil
}

// Owned callbacks join synchronously, including native event handlers. No event
// is queued, replayed, or moved outside its native callback. The ledger remains
// charged until the authored callback actually returns, including Goexit.
var mergeCallbackReads int
var mergeCallbackReadChanged = make(chan struct{})

const mergeCallbackReadLimit = 128

func mergeEnterCallbackRead() (func(), error) {
	if moduleMode {
		return func() {}, nil
	}
	if mergeInstallActive {
		return nil, errPropsStale
	}
	if mergeCallbackReads >= mergeCallbackReadLimit {
		return nil, errPropsCapacity
	}
	mergeCallbackReads++
	released := false
	return func() {
		if released {
			return
		}
		released = true
		mergeCallbackReads--
		close(mergeCallbackReadChanged)
		mergeCallbackReadChanged = make(chan struct{})
		mergeFinishNativeIfIdle()
	}, nil
}

// A newest writer can prepare detached data while callbacks read the useful
// frame. Before touching live DOM/frame it waits for those reads to finish.
// Context/intent cancellation bounds waiting without canceling the callbacks.
// Sustained cooperative input may delay a writer; events are never dropped
// merely because a request is waiting. Privileged revocation is immediate.
func mergeAwaitCallbackReads(ctx context.Context, intent uint64) error {
	for mergeCallbackReads > 0 {
		changed := mergeCallbackReadChanged
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-changed:
		}
		if intent != navigationID {
			return context.Canceled
		}
	}
	if ctx.Err() != nil {
		return ctx.Err()
	}
	if intent != navigationID {
		return context.Canceled
	}
	return nil
}

// Upgrade a lightweight legacy boot lease only when an explicit source needs
// owned callback capacity. Existing no-Props boot does not reserve worker slots.
func ensureMergeCallbackCapacity() error {
	lease := mergeActiveLease
	if lease == nil || lease.callbackRelease != nil {
		return nil
	}
	release, err := reserveMergeCallbackCapacity()
	if err != nil {
		return err
	}
	lease.callbackRelease = release
	return nil
}

// Only observer/data notifications are queued. Event and mutation bodies are
// never stored or replayed. The matching delivery key bypasses one queue check.
func mergeQueueNotification(owner *Scope, key string, notify func()) bool {
	lease := mergeActiveLease
	if lease == nil {
		return false
	}
	owner = mergeRootScope(owner)
	if owner != nil && !owner.live() {
		return true
	}
	if mergeCurrent != nil && mergeCurrent.revoked {
		return true
	}
	composite := "remote:" + key
	if owner != nil {
		composite = strconv.FormatUint(owner.mergeInstance(), 10) + ":" + key
	}
	if lease.delivering == composite && lease.bypass {
		lease.bypass = false
		return false
	}
	if notify == nil || len(key) == 0 || len(key) > 256 {
		lease.failures[owner] = fmt.Errorf("nextgo: invalid merge notification")
		return true
	}
	if _, exists := lease.queue[composite]; !exists {
		if len(lease.order) >= mergeNotificationLimit {
			lease.failures[owner] = fmt.Errorf("nextgo: merge notification bound exceeded")
			return true
		}
		lease.order = append(lease.order, composite)
	}
	lease.queue[composite] = mergeNotification{owner: owner, key: composite, notify: notify}
	return true
}
func mergeEventDuringInstall(owner *Scope, event js.Value) bool {
	if !mergeInstallActive {
		return false
	}
	owner = mergeRootScope(owner)
	if lease := mergeActiveLease; lease != nil && owner != nil {
		lease.failures[owner] = fmt.Errorf("nextgo: synchronous event during merge install")
	}
	// A surprising synchronous event is an explicit recovery condition; never
	// replay a click/input/submit later or let its handler read a partial frame.
	if event.Type() == js.TypeObject && !event.IsNull() && event.Get("cancelable").Type() == js.TypeBoolean && event.Get("cancelable").Bool() {
		event.Call("preventDefault")
	}
	return true
}
func flushMergeNotifications(authority func() bool) []propsFailure {
	lease := mergeActiveLease
	if lease == nil {
		return nil
	}
	for len(lease.order) > 0 && authority() {
		key := lease.order[0]
		lease.order = lease.order[1:]
		item, ok := lease.queue[key]
		delete(lease.queue, key)
		if !ok || (item.owner != nil && !item.owner.active()) {
			continue
		}
		lease.delivered++
		if lease.delivered > mergeNotificationLimit*4 {
			lease.failures[item.owner] = fmt.Errorf("nextgo: merge notification feedback bound exceeded")
			lease.queue = map[string]mergeNotification{}
			lease.order = nil
			break
		}
		lease.delivering = key
		lease.bypass = true
		var failure error
		if item.owner != nil {
			failure = withRetainedScopeReport(item.owner, lease.intent, func() error { item.notify(); return nil })
		} else {
			failure = lifetimeGuard(item.notify)
		}
		lease.delivering = ""
		lease.bypass = false
		if failure != nil {
			lease.failures[item.owner] = failure
		}
	}
	result := []propsFailure{}
	for scope, err := range lease.failures {
		result = append(result, propsFailure{Scope: scope, Err: err})
	}
	lease.failures = map[*Scope]error{}
	return result
}

// Only recognized authority changes call this. Ordinary navigation intent or
// resource invalidation must never revoke the useful currently published UI.
func revokeMergeDocument() {
	current := mergeCurrent
	application := mergeApplication
	if (current == nil || current.revoked) && (application == nil || application.revoked) {
		return
	}
	mergePrivacy++
	if current != nil {
		current.revoked = true
	}
	if application != nil {
		application.revoked = true
		for scope := range application.scopes {
			scope.revokeMergeAuthority()
		}
	}
	mergeNativePending = nil
	mergeNativeRequestedPending = nil
	mergeEntryHistory = map[string]*mergeEntryFocus{}
	mergeEntryOrder = nil
	if current != nil {
		for scope := range current.scopes {
			scope.revokeMergeAuthority()
		}
		for _, boundary := range current.boundaries {
			boundary.valid = false
			if boundary.scope != nil {
				boundary.scope.revokeMergeAuthority()
			}
		}
	}
	app := doc.Call("getElementById", "nextgo-app")
	if !app.IsNull() && !app.IsUndefined() {
		app.Set("hidden", true)
		app.Call("setAttribute", "inert", "")
	}
	doc.Get("body").Call("setAttribute", "data-nextgo-merge-state", "revoked")
	status := doc.Call("getElementById", "nextgo-status")
	if !status.IsNull() && !status.IsUndefined() {
		status.Set("hidden", false)
		status.Set("textContent", "The page identity changed. Loading a fresh page…")
	}
	syncLazyRevocation()
}
func mergeNativeActualPending() bool {
	return mergeNativePending != nil && mergeNativePendingKind != mergeNativeRequested && mergeNativeRevision == propsDocumentRevision() && mergeNativePrivacy == mergePrivacy && mergeNativeDocument == mergeCurrent && win.Get("location").Get("href").String() == mergeNativeTarget
}
func mergeDuplicateNativeHash(target string) bool {
	return mergeNativeActualPending() && mergeNativePendingKind == mergeNativePop && mergeNativeTarget == target
}
func mergeFinishNativeIfIdle() {
	if mergeActiveLease != nil || mergeCallbackReads != 0 {
		return
	}
	pending, valid := mergeNativePending, mergeNativeActualPending()
	mergeNativePending = nil
	if pending != nil && valid {
		pending()
	}
	if mergeActiveLease != nil || mergeCallbackReads != 0 {
		return
	}
	requested, epoch := mergeNativeRequestedPending, mergeNativeRequestedIntent
	mergeNativeRequestedPending = nil
	if requested != nil && epoch == navigationID {
		requested()
	}
}
func mergeDeferNativeCommit(kind mergeNativeKind, target string, fn func()) bool {
	if mergeActiveLease == nil && mergeCallbackReads == 0 {
		return false
	}
	// Keep one already-visible native change and one later requested fragment.
	// A requested mutation cannot erase actual native history synchronization.
	if kind == mergeNativeRequested {
		mergeNativeRequestedPending = fn
		mergeNativeRequestedIntent = navigationID
		return true
	}
	// popstate and its hashchange are one native traversal. Keep the richer
	// entry/scroll/focus commit instead of replacing it with hash-only metadata.
	if kind == mergeNativeHash && mergeDuplicateNativeHash(target) {
		return true
	}
	mergeNativeRequestedPending = nil
	mergeNativePending = fn
	mergeNativeIntent = navigationID
	mergeNativePendingKind = kind
	mergeNativeTarget = target
	mergeNativeRevision, mergeNativePrivacy, mergeNativeDocument = propsDocumentRevision(), mergePrivacy, mergeCurrent
	return true
}
func mergeIncomingAuthority(value *navigation.MergeDescriptor, retention *navigation.Retention, status int) {
	if status == 401 || status == 403 {
		revokeMergeDocument()
		return
	}
	known := ""
	if mergeApplication != nil {
		known = mergeApplication.partition
	}
	if known == "" && mergeCurrent != nil {
		known = mergeKnownPartition(mergeCurrent.descriptor, nil)
	}
	incoming := mergeKnownPartition(value, retention)
	if known != "" && incoming != "" && known != incoming {
		revokeMergeDocument()
	}
}

func mergeLocation(target string) Location {
	value := win.Get("URL").New(target)
	return Location{URL: value.Get("href").String(), Pathname: value.Get("pathname").String(), Search: value.Get("search").String(), Hash: value.Get("hash").String()}
}
func mergePublishDescriptor(value *navigation.MergeDescriptor) {
	body := doc.Get("body")
	if value == nil {
		body.Call("removeAttribute", navigation.MergeAttribute)
		return
	}
	raw, err := json.Marshal(value)
	if err == nil {
		body.Call("setAttribute", navigation.MergeAttribute, string(raw))
	}
}
func initializeMergeDocument() {
	if !mergeRuntimeEnabled() {
		return
	}
	raw := doc.Get("body").Call("getAttribute", navigation.MergeAttribute)
	if raw.IsNull() || raw.IsUndefined() || raw.String() == "" {
		return
	}
	value, err := navigation.ParseMergeDescriptor(raw.String())
	if err != nil {
		win.Get("console").Call("error", "Next.go merge bootstrap: "+err.Error())
		return
	}
	root := doc.Call("getElementById", "nextgo-app")
	if mergeContainsLazyBoundary(root) {
		return
	}
	current, err := mergeDocumentFromDOM(value, root, 1, "", 0)
	if err != nil {
		win.Get("console").Call("error", "Next.go merge bootstrap: "+err.Error())
		return
	}
	mergeCurrent = current
	// All managed physical work, including props callbacks, shares the existing
	// actual-return ledger. This is not a second uncharged task pool.
	retainedTaskAdmission = true
}
func captureMergeBindings() {
	current := mergeCurrent
	if current == nil || current.revoked {
		return
	}
	for _, boundary := range current.boundaries {
		boundary.valid = false
		for _, scope := range mounted {
			if scope.Root.Value.Equal(boundary.root) && scope.mergeClientReady() && mergeClientTreeSafe(boundary.root) {
				boundary.scope = scope
				boundary.instance = scope.mergeInstance()
				boundary.valid = true
				break
			}
		}
	}
}
func mergeShowAuthorized() {
	if mergeApplicationRevoked() {
		return
	}
	body := doc.Get("body")
	if body.Call("getAttribute", "data-nextgo-merge-state").String() != "revoked" {
		return
	}
	app := doc.Call("getElementById", "nextgo-app")
	app.Set("hidden", false)
	app.Call("removeAttribute", "inert")
	body.Call("removeAttribute", "data-nextgo-merge-state")
}
