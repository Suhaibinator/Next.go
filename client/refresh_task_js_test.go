//go:build js && wasm && nextgo_devrefresh

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
