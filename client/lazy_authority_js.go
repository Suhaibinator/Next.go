//go:build js && wasm

package client

import (
	"encoding/json"
	"fmt"
	"syscall/js"
)

var moduleAuthorityRevoked bool
var pendingModuleScopes = map[*Scope]struct{}{}

// This operation invokes no authored callback or terminal cleanup. It runs
// before normal sync dispatch so current and in-flight module roots fence reads,
// events and ClientView rollback while physical work remains charged.
func syncModuleAuthority(revoked bool) {
	moduleAuthorityRevoked = revoked
	if !revoked {
		return
	}
	for _, scope := range moduleScopes {
		scope.revokeMergeAuthority()
	}
	for scope := range pendingModuleScopes {
		scope.revokeMergeAuthority()
	}
}
func beginModuleMountAuthority(scope *Scope) (func(), error) {
	if moduleAuthorityRevoked || scope == nil {
		return nil, errPropsClosed
	}
	if len(pendingModuleScopes) >= mergeApplicationScopeLimit {
		return nil, errPropsCapacity
	}
	pendingModuleScopes[scope] = struct{}{}
	return func() { delete(pendingModuleScopes, scope) }, nil
}
func syncLazyRevocation() {
	if moduleMode {
		return
	}
	snapshot := moduleSnapshot()
	for _, state := range lazyModules {
		if !state.exports.Truthy() {
			continue
		}
		func() {
			defer func() {
				if recover() != nil {
					win.Get("console").Call("error", "Next.go module authority synchronization failed")
				}
			}()
			result := state.exports.Call("sync", snapshot)
			if result.Type() == js.TypeString && result.String() != "" {
				win.Get("console").Call("error", "Next.go module authority synchronization failed")
			}
		}()
	}
}

// A frozen runtime must still accept a one-way privileged revoke while its
// physical callbacks exist. It must not dispatch ordinary synchronization or
// accept a false snapshot that would unfreeze old owners.
func syncModuleExport(raw string, frozen bool) error {
	if !frozen {
		return syncModule(raw)
	}
	var state struct{ Revoked bool }
	if json.Unmarshal([]byte(raw), &state) != nil || !state.Revoked {
		return fmt.Errorf("development refresh module sync is unavailable")
	}
	syncModuleAuthority(true)
	return nil
}
