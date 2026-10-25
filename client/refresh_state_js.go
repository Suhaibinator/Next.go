//go:build js && wasm

package client

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"sort"

	"github.com/Suhaibinator/Next.go/internal/devrefresh"
)

type refreshStateCell struct {
	encode func() (json.RawMessage, error)
}
type refreshScopeState struct {
	scope            *Scope
	handle, previous int
	instance, props  string
	cells            map[string]refreshStateCell
	imported         *refreshScopeImport
}
type refreshScopeImport struct {
	snapshot devrefresh.ScopeSnapshot
	bound    bool
	claimed  map[string]bool
}
type refreshStateRegistry struct {
	name, epoch, stateSchema, domSchema string
	scopes                              map[*Scope]*refreshScopeState
	handles                             map[int]*Scope
	instances                           map[string]*Scope
	imported                            map[int]*refreshScopeImport
	importing                           bool
	cells                               int
	failed                              error
}

// Nil until the compiler-selected development-only module entry opts in.
var refreshStateRuntime *refreshStateRegistry

func initRefreshState(name, epoch, stateSchema, domSchema string) error {
	if refreshStateRuntime != nil {
		return fmt.Errorf("nextgo: refresh state runtime already initialized")
	}
	descriptor := devrefresh.ModuleSnapshot{Version: devrefresh.Version, Name: name, Epoch: epoch, StateSchema: stateSchema, DOMSchema: domSchema, Scopes: []devrefresh.ScopeSnapshot{}}
	if descriptor.Validate() != nil {
		return fmt.Errorf("nextgo: invalid refresh state identity")
	}
	refreshStateRuntime = &refreshStateRegistry{name: name, epoch: epoch, stateSchema: stateSchema, domSchema: domSchema, scopes: map[*Scope]*refreshScopeState{}, handles: map[int]*Scope{}, instances: map[string]*Scope{}}
	return nil
}
func resetRefreshState() { refreshStateRuntime = nil }
func refreshStateError(r *refreshStateRegistry) error {
	if r == nil {
		return fmt.Errorf("nextgo: refresh state runtime is unavailable")
	}
	if r.failed != nil {
		return r.failed
	}
	return nil
}
func rejectRefreshState(r *refreshStateRegistry) error {
	err := fmt.Errorf("nextgo: refresh state handoff is incompatible")
	if r != nil && r.failed == nil {
		r.failed = err
	}
	return err
}
func bindRefreshScope(scope *Scope, handle int, rawProps string, previousHandle int) error {
	r := refreshStateRuntime
	if r == nil {
		return nil
	}
	if refreshStateError(r) != nil || scope == nil || scope.closed || handle < 1 || uint64(handle) > devrefresh.MaxRevision || previousHandle < 0 || len(rawProps) > 64<<10 || r.scopes[scope] != nil || r.handles[handle] != nil || len(r.scopes) >= devrefresh.MaxScopesPerModule {
		return rejectRefreshState(r)
	}
	sum := sha256.Sum256([]byte(rawProps))
	props := hex.EncodeToString(sum[:])
	state := &refreshScopeState{scope: scope, handle: handle, previous: previousHandle, props: props, cells: map[string]refreshStateCell{}}
	if r.importing {
		pending := r.imported[previousHandle]
		if previousHandle == 0 || pending == nil || pending.bound || pending.snapshot.PropsDigest != props {
			return rejectRefreshState(r)
		}
		pending.bound = true
		state.imported = pending
	} else if previousHandle != 0 {
		return rejectRefreshState(r)
	}
	r.scopes[scope] = state
	r.handles[handle] = scope
	scope.Cleanup(func() {
		if r.scopes[scope] != state {
			return
		}
		delete(r.scopes, scope)
		delete(r.handles, handle)
		if state.instance != "" && r.instances[state.instance] == scope {
			delete(r.instances, state.instance)
		}
		r.cells -= len(state.cells)
		if r.importing {
			rejectRefreshState(r)
		}
		state.cells = nil
	})
	return nil
}

// RefreshInstance declares a stable public instance key for an opted-in lazy
// component. Call once, before NewRefreshState. It stores no state in ordinary
// production or non-refresh components.
func (scope *Scope) RefreshInstance(instanceKey string) error {
	if scope == nil || !scope.active() {
		return ErrLifetimeClosed
	}
	if !devrefresh.ValidToken(instanceKey) {
		return fmt.Errorf("nextgo: invalid refresh instance key")
	}
	r := refreshStateRuntime
	if r == nil {
		return nil
	}
	state := r.scopes[scope]
	if refreshStateError(r) != nil || state == nil || state.instance != "" || r.instances[instanceKey] != nil {
		return rejectRefreshState(r)
	}
	if state.imported != nil && state.imported.snapshot.Instance != instanceKey {
		return rejectRefreshState(r)
	}
	state.instance = instanceKey
	r.instances[instanceKey] = scope
	return nil
}

// NewRefreshState opts one public JSON value into a same-schema development
// handoff. State is still updated explicitly; application code owns DOM updates.
// Pointers, interfaces, recursive types and custom codecs are not migrated.
func NewRefreshState[T any](scope *Scope, key string, initial T) (*State[T], error) {
	if scope == nil || !scope.active() {
		return nil, ErrLifetimeClosed
	}
	if !devrefresh.ValidToken(key) {
		return nil, fmt.Errorf("nextgo: invalid refresh state key")
	}
	r := refreshStateRuntime
	if r == nil {
		return NewState(initial), nil
	}
	owner := r.scopes[scope]
	if refreshStateError(r) != nil || owner == nil || owner.instance == "" || owner.cells[key].encode != nil || r.cells >= devrefresh.MaxCellsPerModule {
		return nil, rejectRefreshState(r)
	}
	if err := validateRefreshType[T](); err != nil {
		r.failed = err
		return nil, err
	}
	if _, err := encodeRefreshValue(initial); err != nil {
		r.failed = err
		return nil, err
	}
	value := initial
	if owner.imported != nil {
		found := false
		for _, cell := range owner.imported.snapshot.Cells {
			if cell.Key == key {
				if owner.imported.claimed[key] {
					return nil, rejectRefreshState(r)
				}
				restored, err := decodeRefreshValue[T](cell.Value)
				if err != nil {
					r.failed = err
					return nil, err
				}
				value = restored
				owner.imported.claimed[key] = true
				found = true
				break
			}
		}
		if !found {
			return nil, rejectRefreshState(r)
		}
	}
	state := NewState(value)
	owner.cells[key] = refreshStateCell{encode: func() (json.RawMessage, error) { return encodeRefreshValue(state.Get()) }}
	r.cells++
	return state, nil
}

// Eligibility intentionally reads closed directly: prepare freezes ordinary
// Scope callbacks before capture, while keeping live scope ownership intact.
func refreshStateEligibility() error {
	r := refreshStateRuntime
	if err := refreshStateError(r); err != nil {
		return err
	}
	if r.importing {
		return fmt.Errorf("nextgo: refresh state import has not been claimed")
	}
	for scope, state := range r.scopes {
		if scope.closed || state.instance == "" {
			return rejectRefreshState(r)
		}
	}
	return nil
}
func snapshotRefreshState(scopes map[int]*Scope) (devrefresh.ModuleSnapshot, error) {
	var empty devrefresh.ModuleSnapshot
	if err := refreshStateEligibility(); err != nil {
		return empty, err
	}
	r := refreshStateRuntime
	if len(scopes) != len(r.scopes) {
		return empty, rejectRefreshState(r)
	}
	snapshot := devrefresh.ModuleSnapshot{Version: devrefresh.Version, Name: r.name, Epoch: r.epoch, StateSchema: r.stateSchema, DOMSchema: r.domSchema, Scopes: []devrefresh.ScopeSnapshot{}}
	handles := make([]int, 0, len(scopes))
	for handle := range scopes {
		handles = append(handles, handle)
	}
	sort.Ints(handles)
	size := 0
	for _, handle := range handles {
		scope := scopes[handle]
		state := r.scopes[scope]
		if state == nil || state.handle != handle || scope.closed {
			return empty, rejectRefreshState(r)
		}
		record := devrefresh.ScopeSnapshot{Handle: handle, Instance: state.instance, PropsDigest: state.props, Cells: []devrefresh.Cell{}}
		keys := make([]string, 0, len(state.cells))
		for key := range state.cells {
			keys = append(keys, key)
		}
		sort.Strings(keys)
		for _, key := range keys {
			raw, err := state.cells[key].encode()
			if err != nil {
				return empty, err
			}
			if len(raw) > devrefresh.MaxStateBytes-size {
				return empty, fmt.Errorf("nextgo: refresh state aggregate exceeds 256 KiB")
			}
			size += len(raw)
			record.Cells = append(record.Cells, devrefresh.Cell{Key: key, Value: raw})
		}
		snapshot.Scopes = append(snapshot.Scopes, record)
	}
	if snapshot.Validate() != nil {
		return empty, fmt.Errorf("nextgo: refresh state snapshot exceeds protocol bounds")
	}
	return snapshot, nil
}
func importRefreshState(snapshot devrefresh.ModuleSnapshot) error {
	r := refreshStateRuntime
	if refreshStateError(r) != nil || r.importing || len(r.scopes) != 0 || snapshot.Validate() != nil || snapshot.Name != r.name || snapshot.StateSchema != r.stateSchema || snapshot.DOMSchema != r.domSchema {
		return rejectRefreshState(r)
	}
	pending := make(map[int]*refreshScopeImport, len(snapshot.Scopes))
	for _, scope := range snapshot.Scopes {
		copyScope := scope
		copyScope.Cells = make([]devrefresh.Cell, len(scope.Cells))
		for i, cell := range scope.Cells {
			copyScope.Cells[i] = devrefresh.Cell{Key: cell.Key, Value: append(json.RawMessage(nil), cell.Value...)}
		}
		pending[scope.Handle] = &refreshScopeImport{snapshot: copyScope, claimed: map[string]bool{}}
	}
	r.imported = pending
	r.importing = true
	return nil
}
func finishRefreshStateClaims() error {
	r := refreshStateRuntime
	if refreshStateError(r) != nil || !r.importing {
		return rejectRefreshState(r)
	}
	for _, pending := range r.imported {
		if !pending.bound || len(pending.claimed) != len(pending.snapshot.Cells) {
			return rejectRefreshState(r)
		}
	}
	for scope, state := range r.scopes {
		if scope.closed || state.instance == "" || state.imported == nil {
			return rejectRefreshState(r)
		}
	}
	// Release raw copies after the candidate has claimed every value exactly once.
	for _, state := range r.scopes {
		state.imported = nil
	}
	r.imported = nil
	r.importing = false
	return nil
}
