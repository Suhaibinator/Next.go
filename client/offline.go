package client

import "errors"

// OfflineState describes this document's explicitly opted-in, anonymous public
// saved pages. It is not authentication state or a promise that arbitrary URLs,
// forms, API responses or application data will work without a network.
type OfflineState struct {
	Supported, Configured, Enabled, Controlling, SavedDocument bool
	// CleanupPending means public owned payloads remain charged after Disable.
	// Cleanup needs a later explicit reconciliation after controlled pages close.
	CleanupPending                                        bool
	Phase, ActiveGeneration, WaitingGeneration, LastError string
	Scope, OwnerPolicyID                                  string
}

var (
	ErrOfflineUnsupported      = errors.New("nextgo: offline controls require a supported secure document browser with Web Locks")
	ErrOfflineUnavailable      = errors.New("nextgo: this document has no validated offline configuration")
	ErrOfflineConflict         = errors.New("nextgo: a conflicting service worker owns an overlapping scope")
	ErrOfflineBusy             = errors.New("nextgo: an offline operation is already in progress")
	ErrOfflineNativeNavigation = errors.New("nextgo: saved public mode uses native document navigation")
	ErrOfflineProtocol         = errors.New("nextgo: invalid offline worker response")
	ErrOfflineUnknown          = errors.New("nextgo: offline operation outcome is unknown; no automatic retry was made")
)
