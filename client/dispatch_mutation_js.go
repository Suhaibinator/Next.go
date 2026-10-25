//go:build js && wasm

package client

import (
	"context"
	"fmt"
	"sync"
)

var documentMutationQueueOnce sync.Once
var documentMutationDispatchQueue *mutationDispatchQueue

func coreMutationQueue() *mutationDispatchQueue {
	documentMutationQueueOnce.Do(func() { documentMutationDispatchQueue = newMutationDispatchQueue(64) })
	return documentMutationDispatchQueue
}

// DispatchMutation owns one typed action transport in the document-wide FIFO.
// At most one typed request is active, with64 waiting admissions. Cancellation
// while queued never dispatches a POST. Active cancellation aborts transport but
// keeps its lease until native fetch/read/cancel promises settle. An error after
// dispatch does not establish rollback and must not trigger an automatic retry.
// validate must synchronously decode/check the response; it must not start or
// await another typed mutation (that operation would wait for this lease).
func DispatchMutation(ctx context.Context, target string, request FetchOptions, validate func(Response) error) error {
	if moduleMode {
		return dispatchMutationFromHost(ctx, target, request, validate)
	}
	if ctx == nil || validate == nil {
		return fmt.Errorf("mutation requires context and response validator")
	}
	u := win.Get("URL").New(target, win.Get("location").Get("href"))
	if u.Get("origin").String() != win.Get("location").Get("origin").String() || u.Get("username").String() != "" || u.Get("password").String() != "" || u.Get("hash").String() != "" {
		return fmt.Errorf("mutation endpoint must be same-origin without credentials or fragment")
	}
	release, err := coreMutationQueue().acquire(ctx)
	if err != nil {
		return err
	}
	defer release()
	if actionViewFrozen || draftFrozen {
		return ErrMutationQueueFrozen
	}
	if request.beforeMutationDispatch != nil {
		if err := request.beforeMutationDispatch(); err != nil {
			return err
		}
	}
	request.Method = "POST"
	request.Cache = "no-store"
	request.Redirect = "error"
	request.physicalSettlement = true
	response, err := Fetch(ctx, target, request)
	if err != nil {
		return err
	}
	return validate(response)
}
