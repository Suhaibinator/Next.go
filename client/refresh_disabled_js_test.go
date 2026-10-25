//go:build js && wasm && !nextgo_devrefresh

package client

import "testing"

func TestRefreshProductionCannotEnableCoordinatorOrPhysicalLedger(t *testing.T) {
	previous, activity := devRefresh, refreshActivity
	t.Cleanup(func() { devRefresh, refreshActivity = previous, activity })
	devRefresh = nil
	refreshActivity = refreshActivityTracker{}
	RegisterDevRefresh("not a development registration")
	if devRefresh != nil || devRefreshEnabled() || refreshFrozen() || refreshBridgeQualified() {
		t.Fatal("production opted into development refresh")
	}
	if bootstrapDevRefresh(func() { t.Error("production deferred hydration") }) {
		t.Fatal("production installed startup refresh")
	}
	enableRefreshActivity()
	lease, err := beginRefreshActivity()
	if err != nil || lease != nil || retainRefreshActivity() != nil || refreshActivity.enabled || refreshActivityState() != (refreshActivityStatus{}) {
		t.Fatal("production retained refresh accounting", err)
	}
	lease.Done()
}
