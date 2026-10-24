// Package client supplies observable state and the js/wasm island lifecycle.
package client

import "fmt"

// State explicitly notifies subscribers; it does not rerender HTML. Browser
// components use it from the Go/WASM event loop.
type State[T any] struct {
	value       T
	next        int
	subscribers map[int]func(T)
	effectNext  int
	effects     map[int]func()
}

func NewState[T any](value T) *State[T] {
	return &State[T]{value: value, subscribers: map[int]func(T){}}
}
func (s *State[T]) Get() T { return s.value }
func (s *State[T]) Set(value T) {
	s.value = value
	callbacks := make([]func(T), 0, len(s.subscribers))
	for _, fn := range s.subscribers {
		callbacks = append(callbacks, fn)
	}
	for _, fn := range callbacks {
		fn(value)
	}
	callbacksChanged := make([]func(), 0, len(s.effects))
	for _, fn := range s.effects {
		callbacksChanged = append(callbacksChanged, fn)
	}
	for _, fn := range callbacksChanged {
		fn()
	}
}

// Subscribe synchronizes the view immediately and returns an unsubscribe function.
func (s *State[T]) Subscribe(fn func(T)) func() {
	id := s.next
	s.next++
	s.subscribers[id] = fn
	fn(s.value)
	return func() { delete(s.subscribers, id) }
}

func (s *State[T]) subscribeEffect(fn func()) (func(), error) {
	if s == nil || fn == nil {
		return nil, fmt.Errorf("nextgo: nil effect state dependency")
	}
	if len(s.effects) >= 256 {
		return nil, fmt.Errorf("nextgo: state effect subscriber limit exceeded")
	}
	if s.effects == nil {
		s.effects = map[int]func(){}
	}
	s.effectNext++
	id := s.effectNext
	s.effects[id] = fn
	return func() { delete(s.effects, id) }, nil
}
