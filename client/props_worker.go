package client

import (
	"context"
	"errors"
	"sync"
)

var errPropsClosed = errors.New("nextgo: props source is closed or revoked")
var errPropsStale = errors.New("nextgo: props authority changed")
var errPropsCapacity = errors.New("nextgo: props admission exceeded")
var errPropsCallback = errors.New("nextgo: props callback failed")
var errPropsInvalid = errors.New("nextgo: invalid props source")

const propsWorkerLimit = 128

var propsWorkerMu sync.Mutex
var propsWorkers int
var propsWorkerByteCount int

const propsWorkerByteLimit = 1 << 20

func propsWorkerBytes() int {
	propsWorkerMu.Lock()
	defer propsWorkerMu.Unlock()
	return propsWorkerByteCount
}

// The browser adds the core physical lease; portable tests retain this local
// bound. The hook is configured once during runtime initialization.
var propsPhysicalAdmission = func() (func(), error) { return nil, nil }

func propsInvoke[T any](ctx context.Context, valid func() bool, fn func() (T, error)) (T, error) {
	return propsInvokeWeighted(ctx, valid, 0, fn)
}
func propsInvokeWeighted[T any](ctx context.Context, valid func() bool, weight int, fn func() (T, error)) (T, error) {
	var zero T
	if weight < 0 || weight > propsWorkerByteLimit {
		return zero, errPropsCapacity
	}
	if ctx == nil || fn == nil || valid == nil {
		return zero, errPropsInvalid
	}
	if ctx.Err() != nil || !valid() {
		return zero, errPropsStale
	}
	propsWorkerMu.Lock()
	if propsWorkers >= propsWorkerLimit || propsWorkerByteCount > propsWorkerByteLimit-weight {
		propsWorkerMu.Unlock()
		return zero, errPropsCapacity
	}
	propsWorkers++
	propsWorkerByteCount += weight
	propsWorkerMu.Unlock()
	release, err := propsPhysicalAdmission()
	if err != nil {
		propsWorkerMu.Lock()
		propsWorkers--
		propsWorkerByteCount -= weight
		propsWorkerMu.Unlock()
		return zero, err
	}
	type result struct {
		value T
		err   error
	}
	done := make(chan result, 1)
	go func() {
		returned := false
		out := result{err: errPropsCallback}
		defer func() {
			if recover() != nil || !returned {
				out = result{err: errPropsCallback}
			}
			if release != nil {
				release()
			}
			propsWorkerMu.Lock()
			propsWorkers--
			propsWorkerByteCount -= weight
			propsWorkerMu.Unlock()
			// Buffered delivery cannot retain the worker after logical cancellation.
			done <- out
		}()
		if ctx.Err() != nil || !valid() {
			out.err = errPropsStale
			returned = true
			return
		}
		out.value, out.err = fn()
		returned = true
	}()
	select {
	case out := <-done:
		if ctx.Err() != nil || !valid() {
			return zero, errPropsStale
		}
		return out.value, out.err
	case <-ctx.Done():
		return zero, errPropsStale
	}
}
