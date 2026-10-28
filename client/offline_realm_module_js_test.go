//go:build js && wasm && nextgo_module

package client

import (
	"context"
	"errors"
	"syscall/js"
	"testing"
)

func TestOfflineFreshRealmModuleRejectsBeforeDocumentAccess(t *testing.T) {
	c := &offlineController{}
	if _, err := c.updateInFreshRealm(context.Background(), js.Undefined(), js.Undefined()); !errors.Is(err, ErrOfflineUnsupported) {
		t.Fatal(err)
	}
	if offlineRealmInUse != nil {
		t.Fatal("module allocated a recovery realm")
	}
}
