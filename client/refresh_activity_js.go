//go:build js && wasm

package client

import (
	"context"
	"errors"
	"sync"
)

var errRefreshActivityFrozen = errors.New("nextgo: development refresh is stopping this runtime")

type refreshActivityStatus struct {
	Busy   int
	Frozen bool
}
type refreshActivityTracker struct {
	enabled, frozen bool
	busy            int
	idle            chan struct{}
}

// Each Go runtime owns its callback ledger. The development coordinator freezes
// document admission and joins every module ledger before letting any main exit.
var refreshActivity refreshActivityTracker
var refreshActivityMu sync.Mutex

type refreshActivityLease struct {
	tracker *refreshActivityTracker
	done    bool
}

func enableRefreshActivity() {
	if refreshActivity.enabled {
		return
	}
	refreshActivity.enabled = true
	refreshActivity.idle = make(chan struct{})
	close(refreshActivity.idle)
}

// begin rejects new operations before they create browser work. Disabled builds
// retain the existing production path without allocating a lease or channel.
func beginRefreshActivity() (*refreshActivityLease, error) {
	if !refreshActivity.enabled {
		return nil, nil
	}
	if refreshFrozen() {
		return nil, errRefreshActivityFrozen
	}
	refreshActivityMu.Lock()
	defer refreshActivityMu.Unlock()
	if refreshActivity.frozen {
		return nil, errRefreshActivityFrozen
	}
	return retainRefreshActivityLocked(), nil
}

// A Promise passed to awaitBrowserPromise already exists. Even after freeze its
// settlement callbacks must be retained/accounted; refusing to attach would lose
// the ability to drain cleanup and continuations of previously admitted work.
func retainRefreshActivity() *refreshActivityLease {
	if !refreshActivity.enabled {
		return nil
	}
	refreshActivityMu.Lock()
	defer refreshActivityMu.Unlock()
	return retainRefreshActivityLocked()
}
func retainRefreshActivityLocked() *refreshActivityLease {
	if refreshActivity.busy == 0 {
		refreshActivity.idle = make(chan struct{})
	}
	refreshActivity.busy++
	return &refreshActivityLease{tracker: &refreshActivity}
}
func (lease *refreshActivityLease) Done() {
	if lease == nil {
		return
	}
	refreshActivityMu.Lock()
	defer refreshActivityMu.Unlock()
	if lease.done {
		return
	}
	lease.done = true
	tracker := lease.tracker
	tracker.busy--
	if tracker.busy == 0 {
		close(tracker.idle)
	}
}
func refreshActivityState() refreshActivityStatus {
	if !refreshActivity.enabled {
		return refreshActivityStatus{}
	}
	refreshActivityMu.Lock()
	defer refreshActivityMu.Unlock()
	return refreshActivityStatus{Busy: refreshActivity.busy, Frozen: refreshActivity.frozen}
}
func freezeRefreshActivity() refreshActivityStatus {
	if refreshActivity.enabled {
		refreshActivityMu.Lock()
		refreshActivity.frozen = true
		refreshActivityMu.Unlock()
	}
	return refreshActivityState()
}

// Only the persistent development host thaws after every child has drained.
// A retiring module keeps its fence permanently closed.
func thawRefreshActivity() error {
	if !refreshActivity.enabled {
		return nil
	}
	refreshActivityMu.Lock()
	defer refreshActivityMu.Unlock()
	if refreshActivity.enabled && refreshActivity.busy != 0 {
		return errors.New("nextgo: development refresh still has physical work")
	}
	refreshActivity.frozen = false
	return nil
}

// Call only from a goroutine after freezing admission and disposing owned
// scopes. A timed-out join must fall back to document reload, never main return.
func waitRefreshActivity(ctx context.Context) error {
	if ctx == nil {
		return errors.New("nextgo: refresh drain requires a context")
	}
	for refreshActivity.enabled {
		refreshActivityMu.Lock()
		if refreshActivity.busy == 0 {
			refreshActivityMu.Unlock()
			return ctx.Err()
		}
		idle := refreshActivity.idle
		refreshActivityMu.Unlock()
		select {
		case <-idle:
		case <-ctx.Done():
			return ctx.Err()
		}
	}
	return ctx.Err()
}
