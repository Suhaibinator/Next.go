//go:build js && wasm

package client

import (
	"github.com/Suhaibinator/Next.go/internal/navigation"
	"strings"
	"syscall/js"
)

// Application authority exists even when there are no explicit merge islands.
// Ordinary initial-only Props and in-flight ordinary Mounts belong to this
// published document; absence of a merge descriptor must not waive revocation.
// Its token has no reverse reference to scopes, DOM or the owner registry.
type mergeApplicationIdentity struct{ marker byte }
type mergeApplicationAuthority struct {
	identity  *mergeApplicationIdentity
	root      js.Value
	scopes    map[*Scope]struct{}
	privacy   uint64
	partition string
	revoked   bool
}

var mergeApplication *mergeApplicationAuthority

const mergeApplicationScopeLimit = retainedTotalNodes

func installMergeApplicationAuthority(root js.Value, current *mergeDocument, retention *navigation.Retention) {
	if moduleMode {
		return
	}
	scopes := map[*Scope]struct{}{}
	if current != nil {
		if current.scopes == nil {
			current.scopes = map[*Scope]struct{}{}
		}
		scopes = current.scopes
	}
	// Legacy V1 activation may reuse already-hydrated roots without calling
	// Mount again. Keep every still-owned mounted root, including suspended
	// hidden V1 views, in the new application's revocation cohort.
	for _, scope := range mounted {
		if scope != nil && !scope.closed && root.Call("contains", scope.Root.Value).Bool() {
			scopes[scope] = struct{}{}
		}
	}
	mergeApplication = &mergeApplicationAuthority{identity: &mergeApplicationIdentity{}, root: root, scopes: scopes, privacy: mergePrivacy, partition: mergeKnownPartition(currentDescriptor(current), retention)}
	if len(scopes) > mergeApplicationScopeLimit {
		revokeMergeDocument()
	}
}
func mergeApplicationToken() *mergeApplicationIdentity {
	if mergeApplication == nil {
		return nil
	}
	return mergeApplication.identity
}
func mergeApplicationCurrent(token *mergeApplicationIdentity) bool {
	if token == nil {
		return mergeApplication == nil
	}
	current := mergeApplication
	return current != nil && !current.revoked && current.privacy == mergePrivacy && current.identity == token
}
func mergeApplicationRevoked() bool {
	return mergeApplication != nil && (mergeApplication.revoked || mergeApplication.privacy != mergePrivacy)
}

// This wrapper adds no worker, callback guard or deferred native event. Legacy
// NoProps Mount/event execution keeps its direct timing. Register before Mount
// can call Props or yield, then recheck exact live app ownership before hydration
// may append the scope, stamp ready, or publish a late error.
func withMergeApplicationMount(scope *Scope, work func() error) (err error) {
	current := mergeApplication
	if moduleMode || current == nil {
		return work()
	}
	token := current.identity
	if scope == nil || scope.parent != nil || !mergeApplicationCurrent(token) {
		return errPropsClosed
	}
	root := doc.Call("getElementById", "nextgo-app")
	if !root.Equal(current.root) || !root.Get("isConnected").Bool() || !root.Call("contains", scope.Root.Value).Bool() {
		scope.revokeMergeAuthority()
		return errMergeOwnership
	}
	if _, exists := current.scopes[scope]; !exists && len(current.scopes) >= mergeApplicationScopeLimit {
		return errPropsCapacity
	}
	current.scopes[scope] = struct{}{}
	defer func() {
		if err != nil {
			delete(current.scopes, scope)
		}
	}()
	err = work()
	if !mergeApplicationCurrent(token) {
		scope.revokeMergeAuthority()
		return errPropsClosed
	}
	root = doc.Call("getElementById", "nextgo-app")
	if !root.Equal(current.root) || !root.Get("isConnected").Bool() || !root.Call("contains", scope.Root.Value).Bool() {
		scope.revokeMergeAuthority()
		return errMergeOwnership
	}
	return err
}

func currentDescriptor(current *mergeDocument) *navigation.MergeDescriptor {
	if current == nil {
		return nil
	}
	return current.descriptor
}
func mergeKnownPartition(value *navigation.MergeDescriptor, retention *navigation.Retention) string {
	if value != nil && value.ValidateDescriptor() == nil {
		return strings.Clone(value.Partition)
	}
	if retention != nil && retention.ValidateDescriptor() == nil {
		return strings.Clone(retention.Partition)
	}
	return ""
}
func initialMergeRetention() *navigation.Retention {
	raw := doc.Get("body").Call("getAttribute", navigation.RetentionAttribute)
	if raw.Type() != js.TypeString {
		return nil
	}
	value, err := navigation.ParseRetentionDescriptor(raw.String())
	if err != nil {
		return nil
	}
	return value
}
