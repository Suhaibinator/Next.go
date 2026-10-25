package devrefresh

import (
	"encoding/json"

	"github.com/Suhaibinator/Next.go/internal/ascii"
	"github.com/Suhaibinator/Next.go/internal/wirejson"
)

// Cell is an explicitly registered state value. No Go pointer, function, DOM
// reference, File handle, provider or pending operation belongs in this payload.
// The typed state registry must additionally enforce its allowed Go value types.
type Cell struct {
	Key   string          `json:"key"`
	Value json.RawMessage `json:"value"`
}

// ScopeSnapshot binds explicit instance identity to the source module's mount
// handle and the SHA-256 of exact raw server props. A handle is not a DOM root
// reference and must never be reused as a replacement module's mount handle.
type ScopeSnapshot struct {
	Handle      int    `json:"handle"`
	Instance    string `json:"instance"`
	PropsDigest string `json:"propsDigest"`
	Cells       []Cell `json:"cells"`
}

// ModuleSnapshot describes the SOURCE module; its Epoch intentionally differs
// from the replacement module's epoch. The persistent coordinator checks source
// identity and actual root ownership before importing under matching schemas.
type ModuleSnapshot struct {
	Version     int             `json:"version"`
	Name        string          `json:"name"`
	Epoch       string          `json:"epoch"`
	StateSchema string          `json:"stateSchema"`
	DOMSchema   string          `json:"domSchema"`
	Scopes      []ScopeSnapshot `json:"scopes"`
}

// Snapshot is an aggregate in-memory handoff. It is not an HTTP/SSE message.
type Snapshot struct {
	Version  int              `json:"version"`
	Revision uint64           `json:"revision"`
	Modules  []ModuleSnapshot `json:"modules"`
}

func (c Cell) Validate() error {
	if !ValidToken(c.Key) || validJSON(c.Value, MaxCellBytes) != nil {
		return ErrInvalid
	}
	return nil
}

func (m ModuleSnapshot) validate() error {
	if m.Version != Version || !validModuleName(m.Name) || !ascii.LowerHex(m.Epoch, 64) || !ValidToken(m.StateSchema) || !ValidToken(m.DOMSchema) || m.Scopes == nil || len(m.Scopes) > MaxScopesPerModule {
		return ErrInvalid
	}
	instances := make(map[string]bool, len(m.Scopes))
	handles := make(map[int]bool, len(m.Scopes))
	count, bytes := 0, 0
	for _, scope := range m.Scopes {
		if scope.Handle < 1 || uint64(scope.Handle) > MaxRevision || !ValidToken(scope.Instance) || !ascii.LowerHex(scope.PropsDigest, 64) || instances[scope.Instance] || handles[scope.Handle] || scope.Cells == nil || len(scope.Cells) > MaxCellsPerModule-count {
			return ErrInvalid
		}
		instances[scope.Instance], handles[scope.Handle] = true, true
		count += len(scope.Cells)
		keys := make(map[string]bool, len(scope.Cells))
		for _, cell := range scope.Cells {
			if cell.Validate() != nil || keys[cell.Key] || len(cell.Value) > MaxStateBytes-bytes {
				return ErrInvalid
			}
			keys[cell.Key] = true
			bytes += len(cell.Value)
		}
	}
	return nil
}

func (m ModuleSnapshot) Validate() error {
	if m.validate() != nil {
		return ErrInvalid
	}
	data, err := json.Marshal(m)
	if err != nil || len(data) > MaxStateBytes {
		return ErrInvalid
	}
	return nil
}

func (s Snapshot) Validate() error {
	if s.Version != Version || s.Revision > MaxRevision || s.Modules == nil || len(s.Modules) > MaxModules {
		return ErrInvalid
	}
	seen := make(map[string]bool, len(s.Modules))
	bytes := 0
	for _, module := range s.Modules {
		if module.validate() != nil || seen[module.Name] {
			return ErrInvalid
		}
		seen[module.Name] = true
		for _, scope := range module.Scopes {
			for _, cell := range scope.Cells {
				if len(cell.Value) > MaxStateBytes-bytes {
					return ErrInvalid
				}
				bytes += len(cell.Value)
			}
		}
	}
	data, err := json.Marshal(s)
	if err != nil || len(data) > MaxStateBytes {
		return ErrInvalid
	}
	return nil
}

var snapshotModuleKeys = []string{"version", "name", "epoch", "stateSchema", "domSchema", "scopes"}
var scopeKeys = []string{"handle", "instance", "propsDigest", "cells"}
var cellKeys = []string{"key", "value"}

func snapshotShape(data []byte) error {
	object, err := exactObject(data, snapshotModuleKeys...)
	if err != nil || exactArray(object["scopes"], scopeKeys, MaxScopesPerModule) != nil {
		return ErrInvalid
	}
	var scopes []json.RawMessage
	if json.Unmarshal(object["scopes"], &scopes) != nil {
		return ErrInvalid
	}
	for _, raw := range scopes {
		scope, _ := exactObject(raw, scopeKeys...)
		if exactArray(scope["cells"], cellKeys, MaxCellsPerModule) != nil {
			return ErrInvalid
		}
	}
	return nil
}

func ParseModuleSnapshot(data []byte) (ModuleSnapshot, error) {
	var value ModuleSnapshot
	if validJSON(data, MaxStateBytes) != nil || snapshotShape(data) != nil || wirejson.Strict(data, &value) != nil || value.Validate() != nil {
		return ModuleSnapshot{}, ErrInvalid
	}
	return value, nil
}

func ParseSnapshot(data []byte) (Snapshot, error) {
	var value Snapshot
	if validJSON(data, MaxStateBytes) != nil {
		return value, ErrInvalid
	}
	object, err := exactObject(data, "version", "revision", "modules")
	if err != nil || exactArray(object["modules"], snapshotModuleKeys, MaxModules) != nil {
		return Snapshot{}, ErrInvalid
	}
	var modules []json.RawMessage
	if json.Unmarshal(object["modules"], &modules) != nil {
		return Snapshot{}, ErrInvalid
	}
	for _, module := range modules {
		if snapshotShape(module) != nil {
			return Snapshot{}, ErrInvalid
		}
	}
	if wirejson.Strict(data, &value) != nil || value.Validate() != nil {
		return Snapshot{}, ErrInvalid
	}
	return value, nil
}

const (
	PhaseReady    = "ready"
	PhasePrepared = "prepared"
	PhaseStopping = "stopping"
	PhaseStopped  = "stopped"
)

// Status carries activity counts only, never application state or diagnostics.
type Status struct {
	Version int    `json:"version"`
	Phase   string `json:"phase"`
	Pending int    `json:"pending"`
	Scopes  int    `json:"scopes"`
	Frozen  bool   `json:"frozen"`
}

func (s Status) Validate() error {
	if s.Version != ExtensionVersion || s.Pending < 0 || s.Pending > 1<<20 || s.Scopes < 0 || s.Scopes > MaxScopesPerModule {
		return ErrInvalid
	}
	switch s.Phase {
	case PhaseReady, PhasePrepared, PhaseStopping, PhaseStopped:
		return nil
	}
	return ErrInvalid
}

func ParseStatus(data []byte) (Status, error) {
	var value Status
	if validJSON(data, 1024) != nil {
		return value, ErrInvalid
	}
	if _, err := exactObject(data, "version", "phase", "pending", "scopes", "frozen"); err != nil || wirejson.Strict(data, &value) != nil || value.Validate() != nil {
		return Status{}, ErrInvalid
	}
	return value, nil
}
