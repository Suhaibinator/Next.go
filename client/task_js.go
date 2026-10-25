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
// its first request and after its last browser promise settles.
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
	if s.taskContext == nil {
		ctx, cancel := context.WithCancel(context.Background())
		s.taskContext = ctx
		s.Cleanup(cancel)
	}
	s.taskWorkers++
	go func() {
		defer lease.Done()
		defer func() {
			s.taskWorkers--
			if recover() != nil {
				report("component task failed")
			}
		}()
		work(s.taskContext)
	}()
	return nil
}
