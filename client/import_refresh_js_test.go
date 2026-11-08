//go:build js && wasm && nextgo_devrefresh && !nextgo_module

package client

import (
	"context"
	"errors"
	"testing"

	"github.com/Suhaibinator/Next.go/scripts"
)

func TestImportModuleRefreshDrainRetainsCanceledPhysicalImport(t *testing.T) {
	f := importEnvironment(t)
	refreshActivityTestRuntime(t)
	enableRefreshActivity()
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	const source = "https://modules.test/slow.mjs"
	go func() { _, err := ImportModule(ctx, scripts.Module{ID: "slow", Src: source}); done <- err }()
	importUntil(t, func() bool { return f.Get("calls").Length() == 1 })
	cancel()
	if err := <-done; !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	if state := freezeRefreshActivity(); state.Busy == 0 || !state.Frozen {
		t.Fatal("canceled import no longer protects physical callbacks", state)
	}
	if _, err := ImportModule(context.Background(), scripts.Module{ID: "new", Src: "https://modules.test/new.mjs"}); !errors.Is(err, errRefreshActivityFrozen) {
		t.Fatal("frozen runtime admitted an import", err)
	}
	f.Call("resolve", source)
	if err := waitRefreshActivity(context.Background()); err != nil {
		t.Fatal(err)
	}
	if err := thawRefreshActivity(); err != nil {
		t.Fatal(err)
	}
}
