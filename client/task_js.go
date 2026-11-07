//go:build js && wasm

package client

import (
	"context"
	"fmt"
)

// Go starts an owned asynchronous component task without blocking a browser
// callback. Its context is canceled when the scope unmounts. Tasks must observe
// cancellation before updating the DOM; cancellation cannot stop arbitrary Go
// code. Development refresh accounts the whole worker, including time before
// its first request and after its last browser promise settles. Retained views
// cancel the current visible period without replaying this function on resume.
func (s *Scope) Go(work func(context.Context)) error {
	if !s.active() {
		return ErrLifetimeClosed
	}
	if work == nil || s.taskWorkers >= 64 {
		return fmt.Errorf("nextgo: invalid component task or task capacity exceeded")
	}
	lease, err := beginRefreshActivity()
	if err != nil {
		return err
	}
	physical, err := beginRetainedTask()
	if err != nil {
		lease.Done()
		return err
	}
	if s.taskContext == nil {
		s.taskContext, s.taskCancel = context.WithCancel(context.Background())
		if !s.taskCleanup {
			s.taskCleanup = true
			s.Cleanup(s.stopVisibilityTasks)
		}
	}
	ctx := s.taskContext
	s.taskWorkers++
	go func() {
		defer lease.Done()
		defer physical.Done()
		defer func() {
			s.taskWorkers--
			if recover() != nil {
				report("component task failed")
			}
		}()
		work(ctx)
	}()
	return nil
}
