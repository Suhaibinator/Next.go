//go:build js && wasm

package client

import (
	"context"
	"errors"
	"testing"
	"time"
)

func TestRefreshTaskAccountsBeforeSpawnAndThroughCanceledWorker(t *testing.T) {
	refreshActivityTestRuntime(t)
	enableRefreshActivity()
	scope := &Scope{}
	started := make(chan context.Context, 1)
	release := make(chan struct{})
	returned := make(chan struct{})
	if err := scope.Go(func(ctx context.Context) {
		started <- ctx
		<-release // Deliberately ignore cancellation until physical completion.
		close(returned)
	}); err != nil {
		t.Fatal(err)
	}
	if refreshActivityState().Busy != 1 {
		t.Fatal("worker was not accounted before spawn")
	}
	ctx := <-started
	scope.dispose()
	if !errors.Is(ctx.Err(), context.Canceled) {
		t.Fatal("scope disposal did not cancel owned task")
	}
	freezeRefreshActivity()
	drain, cancel := context.WithTimeout(context.Background(), time.Millisecond)
	defer cancel()
	if err := waitRefreshActivity(drain); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatal("logical cancellation discarded physical worker", err)
	}
	close(release)
	<-returned
	refreshActivityAwaitCount(t, 0)
	if scope.taskWorkers != 0 {
		t.Fatal("completed task retained its slot")
	}
}

func TestRefreshTaskAdmissionBoundsAndProductionCleanup(t *testing.T) {
	refreshActivityTestRuntime(t)
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
