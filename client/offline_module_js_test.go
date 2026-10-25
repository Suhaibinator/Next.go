//go:build js && wasm && nextgo_module

package client

import (
	"context"
	"errors"
	"testing"
)

func TestOfflineModuleControlsAreExplicitlyUnsupported(t *testing.T) {
	for _, operation := range []func(context.Context) error{EnableOffline, CheckOfflineUpdate, DisableOffline, RefreshOfflineStatus} {
		if err := operation(context.Background()); !errors.Is(err, ErrOfflineUnsupported) {
			t.Fatal("module control must fail before touching browser registration", err)
		}
	}
	if err := SubscribeOffline(&Scope{}, func(OfflineState) { t.Error("unsupported module callback invoked") }); !errors.Is(err, ErrOfflineUnsupported) {
		t.Fatal(err)
	}
	state := OfflineStatus()
	if state.Supported || state.Configured || state.Enabled || state.LastError == "" {
		t.Fatalf("unsupported module advertised controls: %+v", state)
	}
}
