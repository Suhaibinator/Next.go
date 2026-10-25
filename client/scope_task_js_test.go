//go:build js && wasm

package client

import (
	"context"
	"errors"
	"testing"
)

func TestRefreshTaskAdmissionBoundsAndProductionCleanup(t *testing.T) {
	previous := refreshActivity
	refreshActivity = refreshActivityTracker{}
	t.Cleanup(func() { refreshActivity = previous })
	scope := &Scope{}
	if err := scope.Go(nil); err == nil {
		t.Fatal("nil task admitted")
	}
	scope.taskWorkers = 64
	if err := scope.Go(func(context.Context) {}); err == nil {
		t.Fatal("unbounded task admission")
	}
	scope.taskWorkers = 0
	done := make(chan struct{})
	if err := scope.Go(func(ctx context.Context) { <-ctx.Done(); close(done) }); err != nil {
		t.Fatal(err)
	}
	if refreshActivity.enabled || refreshActivityState().Busy != 0 {
		t.Fatal("production enabled the refresh ledger")
	}
	scope.dispose()
	<-done
	if err := scope.Go(func(context.Context) {}); !errors.Is(err, ErrLifetimeClosed) {
		t.Fatal("closed scope admitted work", err)
	}
	var absent *Scope
	if err := absent.Go(func(context.Context) {}); !errors.Is(err, ErrLifetimeClosed) {
		t.Fatal("nil scope admitted work", err)
	}
}
