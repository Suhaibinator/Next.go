//go:build js && wasm

package client

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"
)

func TestLazyRegistrationRejectsInvalidAndMixedGenerations(t *testing.T) {
	oldModules, oldRegistry := lazyModules, registry
	t.Cleanup(func() { lazyModules, registry = oldModules, oldRegistry })
	good := LazyModule{File: "module-" + strings.Repeat("a", 64) + ".wasm", Digest: strings.Repeat("a", 64), Epoch: strings.Repeat("b", 64), Size: 123}
	rejects := func(fn func()) {
		t.Helper()
		panicked := false
		func() { defer func() { panicked = recover() != nil }(); fn() }()
		if !panicked {
			t.Fatal("invalid descriptor accepted")
		}
	}
	for _, invalid := range []LazyModule{{}, {File: good.File, Digest: good.Digest, Epoch: strings.Repeat("x", 64), Size: 123}, {File: good.File, Digest: strings.Repeat("c", 64), Epoch: good.Epoch, Size: 123}, {File: good.File, Digest: good.Digest, Epoch: good.Epoch, Size: 8<<20 + 1}} {
		lazyModules = map[string]*lazyState{}
		registry = map[string]Mount{}
		rejects(func() { RegisterLazy("bad", invalid) })
	}
	lazyModules = map[string]*lazyState{}
	registry = map[string]Mount{}
	RegisterLazy("good", good)
	rejects(func() { RegisterLazy("good", good) })
	rejects(func() { Register("good", func(*Scope, json.RawMessage) error { return nil }) })
}

func TestMountScopedDisposesPartialInitializationForErrorsAndPanics(t *testing.T) {
	for _, panics := range []bool{false, true} {
		scope := &Scope{}
		cleaned := 0
		err := mountScoped(scope, func(scope *Scope, _ json.RawMessage) error {
			scope.Cleanup(func() { cleaned++ })
			if panics {
				panic("deliberate mount panic")
			}
			return fmt.Errorf("deliberate mount error")
		}, nil)
		if err == nil || !scope.closed || cleaned != 1 {
			t.Fatalf("partial mount survived failure: %v, %d", err, cleaned)
		}
		scope.dispose()
		if cleaned != 1 {
			t.Fatal("cleanup ran twice")
		}
	}
	scope := &Scope{}
	if err := mountScoped(scope, func(*Scope, json.RawMessage) error { return nil }, nil); err != nil || scope.closed {
		t.Fatal("successful scope was disposed", err)
	}
}
