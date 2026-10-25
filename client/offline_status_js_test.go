//go:build js && wasm && !nextgo_module

package client

import (
	"context"
	"errors"
	"strings"
	"syscall/js"
	"testing"
	"time"
)

func TestOfflineExplicitStatusQueriesFreshWorkerWithoutInstallation(t *testing.T) {
	f := offlineBrowserFixture(t)
	f.c.state.LastError = ErrOfflineUnknown.Error()
	f.c.state.Phase = "error"
	f.c.state.ActiveGeneration = strings.Repeat("a", 64)
	old := f.respond
	f.respond = func(request offlineRequest) offlineReply {
		reply := old(request)
		reply.Enabled, reply.State = true, "ready"
		reply.ActiveGeneration = reply.Generation
		return reply
	}
	if snapshot := OfflineStatus(); snapshot.LastError == "" || len(f.commands) != 0 {
		t.Fatal("cached snapshot getter must not query the worker")
	}
	if err := RefreshOfflineStatus(context.Background()); err != nil {
		t.Fatal(err)
	}
	state := OfflineStatus()
	if state.LastError != "" || !state.Enabled || state.Phase != "ready" || state.ActiveGeneration != strings.Repeat("e", 64) {
		t.Fatalf("explicit query did not replace an unconfirmed snapshot: %+v", state)
	}
	if len(f.commands) != 1 || f.commands[0].Command != "status" {
		t.Fatalf("without a waiting worker, one explicit operation must mean one active query: %+v", f.commands)
	}
	first := f.commands[0].RequestID
	_ = OfflineStatus()
	if len(f.commands) != 1 {
		t.Fatal("reading a snapshot performed an implicit retry")
	}
	if err := RefreshOfflineStatus(context.Background()); err != nil {
		t.Fatal(err)
	}
	if len(f.commands) != 2 || f.commands[1].Command != "status" || f.commands[1].RequestID == first {
		t.Fatal("a second explicit query did not have independent ownership")
	}
	if f.registers != 0 || f.updates != 0 || f.unregisters != 0 {
		t.Fatal("status installed, updated or removed a worker")
	}
	f.balanced(t)
}

func TestOfflineExplicitStatusWithoutOwnerDoesNotRegister(t *testing.T) {
	f := offlineBrowserFixture(t)
	f.registrations = nil
	if err := RefreshOfflineStatus(context.Background()); err != nil {
		t.Fatal(err)
	}
	if state := OfflineStatus(); state.Enabled || state.Phase != "disabled" || state.ActiveGeneration != "" {
		t.Fatalf("missing owner fabricated a saved edition: %+v", state)
	}
	if f.registers != 0 || f.updates != 0 || f.unregisters != 0 || len(f.commands) != 0 {
		t.Fatal("an explicit read-only query created or repaired a worker")
	}
}

func TestOfflineExplicitStatusCancellationRetainsPhysicalAdmission(t *testing.T) {
	f := offlineBrowserFixture(t)
	started := make(chan struct{})
	resolve := js.Undefined()
	pending := js.Global().Get("Promise").New(f.fn(func(_ js.Value, args []js.Value) any { resolve = args[0]; return nil }))
	f.sw.Set("getRegistrations", f.fn(func(js.Value, []js.Value) any { close(started); return pending }))
	ctx, cancel := context.WithCancelCause(context.Background())
	cause := errors.New("the caller canceled its explicit status query")
	defer cancel(cause)
	settled := make(chan error, 1)
	go func() { settled <- RefreshOfflineStatus(ctx) }()
	joined := false
	defer func() {
		resolve.Invoke(js.Global().Get("Array").New())
		if !joined {
			select {
			case <-settled:
			case <-time.After(time.Second):
				t.Error("status operation did not physically settle before fixture cleanup")
			}
		}
	}()
	<-started
	cancel(cause)
	if err := RefreshOfflineStatus(context.Background()); !errors.Is(err, ErrOfflineBusy) {
		t.Fatal("another explicit query bypassed the held physical operation", err)
	}
	select {
	case err := <-settled:
		t.Fatal("canceled browser registration lookup was abandoned before settlement", err)
	default:
	}
	resolve.Invoke(js.Global().Get("Array").New())
	err := <-settled
	joined = true
	if !errors.Is(err, ErrOfflineUnknown) {
		t.Fatal("canceled in-flight query claimed a confirmed result", err)
	}
	if f.c.busy || len(f.commands) != 0 || f.registers != 0 || f.updates != 0 {
		t.Fatal("canceled explicit status progressed or automatically retried")
	}
	if err := RefreshOfflineStatus(ctx); !errors.Is(err, cause) {
		t.Fatal("a caller canceled before admission lost its cause", err)
	}
}
