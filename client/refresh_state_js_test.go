//go:build js && wasm && nextgo_devrefresh

package client

import (
	"encoding/json"
	"strings"
	"syscall/js"
	"testing"

	"github.com/Suhaibinator/Next.go/internal/devrefresh"
)

func refreshStateFixture(t *testing.T, epoch string) {
	t.Helper()
	before := refreshStateRuntime
	resetRefreshState()
	if err := initRefreshState("counter", strings.Repeat(epoch, 64), "counter-v1", "dom-v1"); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { resetRefreshState(); refreshStateRuntime = before })
}
func refreshBoundScope(t *testing.T, handle, previous int, props, instance string) *Scope {
	t.Helper()
	scope := &Scope{}
	if err := bindRefreshScope(scope, handle, props, previous); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(scope.dispose)
	if err := scope.RefreshInstance(instance); err != nil {
		t.Fatal(err)
	}
	return scope
}
func TestRefreshStateCaptureRestoreClaimsAndCopies(t *testing.T) {
	refreshStateFixture(t, "a")
	left := refreshBoundScope(t, 4, 0, `{"side":"left"}`, "left")
	right := refreshBoundScope(t, 9, 0, `{"side":"right"}`, "right")
	a, err := NewRefreshState(left, "count", 1)
	if err != nil {
		t.Fatal(err)
	}
	a.Set(42)
	b, err := NewRefreshState(right, "labels", []string{"initial"})
	if err != nil {
		t.Fatal(err)
	}
	b.Set([]string{"retained"})
	snapshot, err := snapshotRefreshState(map[int]*Scope{4: left, 9: right})
	if err != nil || len(snapshot.Scopes) != 2 {
		t.Fatal(snapshot, err)
	}
	left.dispose()
	right.dispose()
	if len(refreshStateRuntime.scopes) != 0 || refreshStateRuntime.cells != 0 {
		t.Fatal("disposed scope registry retained")
	}
	resetRefreshState()
	if err := initRefreshState("counter", strings.Repeat("b", 64), "counter-v1", "dom-v1"); err != nil {
		t.Fatal(err)
	}
	if err := importRefreshState(snapshot); err != nil {
		t.Fatal(err)
	}
	// Import owns immutable copies of the transient wire bytes.
	snapshot.Scopes[0].Cells[0].Value[0] = '0'
	newLeft := refreshBoundScope(t, 1, 4, `{"side":"left"}`, "left")
	newRight := refreshBoundScope(t, 2, 9, `{"side":"right"}`, "right")
	restoredA, err := NewRefreshState(newLeft, "count", 0)
	if err != nil || restoredA.Get() != 42 {
		t.Fatal(restoredA, err)
	}
	restoredB, err := NewRefreshState(newRight, "labels", []string{})
	if err != nil || restoredB.Get()[0] != "retained" {
		t.Fatal(restoredB, err)
	}
	if err := finishRefreshStateClaims(); err != nil {
		t.Fatal(err)
	}
	if refreshStateRuntime.imported != nil || refreshStateRuntime.scopes[newLeft].imported != nil {
		t.Fatal("retained handoff JSON after claims")
	}
	restoredA.Set(50)
	restoredB.Get()[0] = "changed"
	if a.Get() != 42 || b.Get()[0] != "retained" {
		t.Fatal("retained old runtime Go objects")
	}
	again, err := snapshotRefreshState(map[int]*Scope{1: newLeft, 2: newRight})
	if err != nil || again.Epoch != strings.Repeat("b", 64) {
		t.Fatal(again, err)
	}
}
func refreshSnapshotForTests(t *testing.T) devrefresh.ModuleSnapshot {
	t.Helper()
	refreshStateFixture(t, "a")
	scope := refreshBoundScope(t, 10, 0, `{"value":1}`, "instance")
	if _, err := NewRefreshState(scope, "count", 5); err != nil {
		t.Fatal(err)
	}
	snapshot, err := snapshotRefreshState(map[int]*Scope{10: scope})
	if err != nil {
		t.Fatal(err)
	}
	scope.dispose()
	resetRefreshState()
	if err := initRefreshState("counter", strings.Repeat("b", 64), "counter-v1", "dom-v1"); err != nil {
		t.Fatal(err)
	}
	return snapshot
}
func TestRefreshStateRejectsMismatchesAndUnclaimedHandoffs(t *testing.T) {
	for _, scenario := range []string{"schema", "props", "instance", "previous-handle", "duplicate-handle", "missing-cell", "extra-cell", "duplicate-cell", "missing-scope", "disposed-candidate", "malformed-cell", "changed-type"} {
		t.Run(scenario, func(t *testing.T) {
			snapshot := refreshSnapshotForTests(t)
			if scenario == "schema" {
				snapshot.StateSchema = "changed"
				if importRefreshState(snapshot) == nil {
					t.Fatal("accepted schema mismatch")
				}
				return
			}
			if scenario == "malformed-cell" {
				snapshot.Scopes[0].Cells[0].Value = json.RawMessage(`{`)
				if importRefreshState(snapshot) == nil {
					t.Fatal("accepted malformed JSON")
				}
				return
			}
			if err := importRefreshState(snapshot); err != nil {
				t.Fatal(err)
			}
			scope := &Scope{}
			defer scope.dispose()
			props := `{"value":1}`
			previous := 10
			if scenario == "props" {
				props = `{"value":2}`
			}
			if scenario == "previous-handle" {
				previous = 99
			}
			err := bindRefreshScope(scope, 1, props, previous)
			if scenario == "props" || scenario == "previous-handle" {
				if err == nil {
					t.Fatal("accepted binding mismatch")
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if scenario == "duplicate-handle" {
				if bindRefreshScope(&Scope{}, 2, props, 10) == nil {
					t.Fatal("claimed previous handle twice")
				}
				return
			}
			instance := "instance"
			if scenario == "instance" {
				instance = "changed"
			}
			err = scope.RefreshInstance(instance)
			if scenario == "instance" {
				if err == nil {
					t.Fatal("accepted instance mismatch")
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			switch scenario {
			case "missing-cell":
				if finishRefreshStateClaims() == nil {
					t.Fatal("accepted unclaimed cell")
				}
			case "missing-scope":
				scope.dispose()
				if finishRefreshStateClaims() == nil {
					t.Fatal("accepted missing scope")
				}
			case "disposed-candidate":
				scope.dispose()
				if _, err := NewRefreshState(scope, "count", 0); err == nil {
					t.Fatal("accepted disposed candidate")
				}
			case "changed-type":
				if _, err := NewRefreshState(scope, "count", ""); err == nil {
					t.Fatal("accepted changed representation")
				}
			case "extra-cell":
				if _, err := NewRefreshState(scope, "new", 0); err == nil {
					t.Fatal("accepted extra cell")
				}
			case "duplicate-cell":
				if _, err := NewRefreshState(scope, "count", 0); err != nil {
					t.Fatal(err)
				}
				if _, err := NewRefreshState(scope, "count", 0); err == nil {
					t.Fatal("claimed cell twice")
				}
			}
		})
	}
}
func TestRefreshStateRegistrationAndCleanupBounds(t *testing.T) {
	refreshStateFixture(t, "a")
	scope := refreshBoundScope(t, 1, 0, `{}`, "first")
	if err := scope.RefreshInstance("first"); err == nil {
		t.Fatal("declared instance twice")
	}
	resetRefreshState()
	if err := initRefreshState("counter", strings.Repeat("a", 64), "counter-v1", "dom-v1"); err != nil {
		t.Fatal(err)
	}
	left := refreshBoundScope(t, 1, 0, `{}`, "left")
	right := refreshBoundScope(t, 2, 0, `{}`, "right")
	for i := 0; i < devrefresh.MaxCellsPerModule; i++ {
		owner := left
		if i%2 != 0 {
			owner = right
		}
		key := "cell/" + strings.Repeat("x", i+1)
		if _, err := NewRefreshState(owner, key, i); err != nil {
			t.Fatal(i, err)
		}
	}
	if _, err := NewRefreshState(right, "overflow", 0); err == nil {
		t.Fatal("bounded cells per scope instead of module")
	}
	left.dispose()
	right.dispose()
	if refreshStateRuntime.cells != 0 || len(refreshStateRuntime.instances) != 0 || len(refreshStateRuntime.handles) != 0 {
		t.Fatal("registry retained disposed ownership")
	}
}
func TestRefreshStateRejectsOversizedOrUnsafeCurrentState(t *testing.T) {
	for _, scenario := range []string{"current-cell", "aggregate", "static-interface"} {
		t.Run(scenario, func(t *testing.T) {
			refreshStateFixture(t, "a")
			scope := refreshBoundScope(t, 1, 0, `{}`, "instance")
			switch scenario {
			case "static-interface":
				if _, err := NewRefreshState[any](scope, "count", 1); err == nil {
					t.Fatal("accepted interface type")
				}
				return
			case "current-cell":
				state, err := NewRefreshState(scope, "label", "safe")
				if err != nil {
					t.Fatal(err)
				}
				state.Set(strings.Repeat("x", devrefresh.MaxCellBytes))
			case "aggregate":
				for i := 0; i < 17; i++ {
					if _, err := NewRefreshState(scope, "cell/"+strings.Repeat("x", i+1), strings.Repeat("x", 16000)); err != nil {
						t.Fatal(err)
					}
				}
			}
			if _, err := snapshotRefreshState(map[int]*Scope{1: scope}); err == nil {
				t.Fatal("accepted oversized snapshot")
			}
		})
	}
}
func TestRefreshStateMountFailureAndExactScopeSet(t *testing.T) {
	refreshStateFixture(t, "a")
	scope := &Scope{}
	if err := bindRefreshScope(scope, 1, `{}`, 0); err != nil {
		t.Fatal(err)
	}
	if err := mountScoped(scope, func(s *Scope, _ json.RawMessage) error {
		if err := s.RefreshInstance("instance"); err != nil {
			return err
		}
		if _, err := NewRefreshState(s, "count", 0); err != nil {
			return err
		}
		return devrefresh.ErrInvalid
	}, nil); err == nil {
		t.Fatal("expected failed mount")
	}
	if len(refreshStateRuntime.scopes) != 0 || refreshStateRuntime.cells != 0 {
		t.Fatal("mount failure retained registry")
	}
	live := refreshBoundScope(t, 2, 0, `{}`, "new")
	if _, err := NewRefreshState(live, "count", 0); err != nil {
		t.Fatal(err)
	}
	if _, err := snapshotRefreshState(map[int]*Scope{}); err == nil {
		t.Fatal("snapshot omitted live root")
	}
}

func TestRefreshStateRejectsBrowserHandles(t *testing.T) {
	refreshStateFixture(t, "a")
	scope := refreshBoundScope(t, 1, 0, `{}`, "instance")
	if _, err := NewRefreshState(scope, "browser", js.Global()); err == nil {
		t.Fatal("accepted browser handle")
	}
}
