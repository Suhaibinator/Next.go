//go:build js && wasm

package client

import "fmt"

// NewScopedOptimistic closes its model automatically when the island unmounts
// or hydration fails. Register transport cancellation with Scope.Cleanup too.
func NewScopedOptimistic[T, A any](scope *Scope, initial T, reduce func(T, A) T, options ...OptimisticOptions[T, A]) (*Optimistic[T, A], error) {
	if !scope.active() {
		return nil, fmt.Errorf("nextgo: optimistic state requires an open island scope")
	}
	if len(options) > 1 {
		return nil, fmt.Errorf("nextgo: optimistic state accepts at most one options value")
	}
	var opts OptimisticOptions[T, A]
	if len(options) == 1 {
		opts = options[0]
	}
	if opts.OnObserverError == nil {
		opts.OnObserverError = func(err error) { report(err.Error()) }
	}
	model, err := NewOptimistic(initial, reduce, opts)
	if err != nil {
		return nil, err
	}
	scope.Cleanup(model.Close)
	return model, nil
}
