package devrefresh

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"
)

func sampleSnapshot() ModuleSnapshot {
	return ModuleSnapshot{Version: Version, Name: "counter", Epoch: strings.Repeat("a", 64), StateSchema: "counter/v1", DOMSchema: "counter/v1", Scopes: []ScopeSnapshot{{Handle: 1, Instance: "first", PropsDigest: strings.Repeat("b", 64), Cells: []Cell{{Key: "count", Value: json.RawMessage(`3`)}}}}}
}

func TestSnapshotScopeIdentityAndGlobalModuleCellBound(t *testing.T) {
	valid := sampleSnapshot()
	if _, err := ParseModuleSnapshot(jsonBytes(t, valid)); err != nil {
		t.Fatal(err)
	}
	for name, edit := range map[string]func(*ModuleSnapshot){
		"duplicate instance": func(m *ModuleSnapshot) { m.Scopes = append(m.Scopes, m.Scopes[0]); m.Scopes[1].Handle = 2 },
		"duplicate handle":   func(m *ModuleSnapshot) { m.Scopes = append(m.Scopes, m.Scopes[0]); m.Scopes[1].Instance = "second" },
		"duplicate cell":     func(m *ModuleSnapshot) { m.Scopes[0].Cells = append(m.Scopes[0].Cells, m.Scopes[0].Cells[0]) },
		"negative handle":    func(m *ModuleSnapshot) { m.Scopes[0].Handle = -1 },
		"missing props":      func(m *ModuleSnapshot) { m.Scopes[0].PropsDigest = "" },
		"absent cells":       func(m *ModuleSnapshot) { m.Scopes[0].Cells = nil },
		"absent scopes":      func(m *ModuleSnapshot) { m.Scopes = nil },
		"unsafe value":       func(m *ModuleSnapshot) { m.Scopes[0].Cells[0].Value = json.RawMessage(`{"a":1,"a":2}`) },
	} {
		t.Run(name, func(t *testing.T) {
			candidate := sampleSnapshot()
			edit(&candidate)
			if candidate.Validate() == nil {
				t.Fatal("ambiguous scope or cells admitted")
			}
		})
	}
	bounded := sampleSnapshot()
	bounded.Scopes = nil
	for i := 0; i < MaxScopes; i++ {
		bounded.Scopes = append(bounded.Scopes, ScopeSnapshot{Handle: i + 1, Instance: fmt.Sprintf("instance%d", i), PropsDigest: strings.Repeat("b", 64), Cells: []Cell{{Key: "count", Value: json.RawMessage(`1`)}}})
	}
	if bounded.Validate() != nil {
		t.Fatal("inclusive scope/cell cap rejected")
	}
	bounded.Scopes[0].Cells = append(bounded.Scopes[0].Cells, Cell{Key: "extra", Value: json.RawMessage(`1`)})
	if bounded.Validate() == nil {
		t.Fatal("cell count must apply across the module, not per scope")
	}
}

func TestSnapshotByteLimitsIncludeMetadata(t *testing.T) {
	cell := Cell{Key: "public", Value: json.RawMessage(`"` + strings.Repeat("a", MaxCellBytes-2) + `"`)}
	if cell.Validate() != nil {
		t.Fatal("inclusive cell byte cap rejected")
	}
	cell.Value = json.RawMessage(`"` + strings.Repeat("a", MaxCellBytes-1) + `"`)
	if cell.Validate() == nil {
		t.Fatal("cell byte cap missing")
	}
	module := sampleSnapshot()
	module.Scopes[0].Cells = nil
	for i := 0; i < 16; i++ {
		module.Scopes[0].Cells = append(module.Scopes[0].Cells, Cell{Key: fmt.Sprintf("cell%d", i), Value: json.RawMessage(`"` + strings.Repeat("a", MaxCellBytes-2) + `"`)})
	}
	if module.Validate() == nil {
		t.Fatal("full snapshot metadata must count toward aggregate byte cap")
	}
	module.Scopes[0].Cells = module.Scopes[0].Cells[:8]
	aggregate := Snapshot{Version: Version, Modules: []ModuleSnapshot{module}}
	if aggregate.Validate() != nil {
		t.Fatal("bounded snapshot rejected")
	}
	other := sampleSnapshot()
	other.Name = "other"
	other.Scopes[0].Cells = append([]Cell(nil), module.Scopes[0].Cells...)
	aggregate.Modules = append(aggregate.Modules, other)
	if aggregate.Validate() == nil {
		t.Fatal("aggregate byte cap must include all modules")
	}
}

func TestStrictMemoryOnlySnapshotWire(t *testing.T) {
	valid := string(jsonBytes(t, sampleSnapshot()))
	for name, wire := range map[string]string{
		"nested case":    strings.Replace(valid, `"propsDigest":`, `"PropsDigest":`, 1),
		"duplicate cell": strings.Replace(valid, `"key":"count"`, `"key":"count","key":"other"`, 1),
		"unknown field":  strings.Replace(valid, `"handle":1`, `"handle":1,"root":"#counter"`, 1),
		"missing field":  strings.Replace(valid, `"handle":1,`, "", 1),
		"null handle":    strings.Replace(valid, `"handle":1`, `"handle":null`, 1),
		"null scope":     strings.Replace(valid, `"scopes":[`, `"scopes":null,"ignored":[`, 1),
		"bad JSON value": strings.Replace(valid, `"value":3`, `"value":{"a":1,"\u0061":2}`, 1),
	} {
		t.Run(name, func(t *testing.T) {
			if _, err := ParseModuleSnapshot([]byte(wire)); err == nil {
				t.Fatal("ambiguous snapshot admitted")
			}
		})
	}
	for _, value := range []json.RawMessage{json.RawMessage(`null`), json.RawMessage(`{"known":[true,3,"public"]}`)} {
		m := sampleSnapshot()
		m.Scopes[0].Cells[0].Value = value
		snapshot := Snapshot{Version: Version, Revision: 0, Modules: []ModuleSnapshot{m}}
		if _, err := ParseSnapshot(jsonBytes(t, snapshot)); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := ParseSnapshot([]byte(strings.Repeat(" ", MaxStateBytes+1))); err == nil {
		t.Fatal("oversized aggregate wire accepted")
	}
}

func TestStatusIsStrictActivityMetadata(t *testing.T) {
	for _, phase := range []string{PhaseReady, PhasePrepared, PhaseStopping, PhaseStopped} {
		status := Status{Version: ExtensionVersion, Phase: phase, Frozen: phase != PhaseReady}
		if _, err := ParseStatus(jsonBytes(t, status)); err != nil {
			t.Fatal(err)
		}
	}
	for _, raw := range []string{
		`{"version":1,"phase":"ready","pending":0,"scopes":0,"frozen":null}`,
		`{"version":1,"phase":"ready","pending":-1,"scopes":0,"frozen":false}`,
		`{"version":1,"phase":"ready","pending":0,"scopes":65,"frozen":false}`,
		`{"version":1,"phase":"ready","pending":0,"scopes":0,"frozen":false,"state":"private"}`,
	} {
		if _, err := ParseStatus([]byte(raw)); err == nil {
			t.Fatal("invalid status accepted")
		}
	}
}
