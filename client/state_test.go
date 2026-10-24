package client

import "testing"

func TestStateSubscriptionsAndIsolation(t *testing.T) {
	first, second := NewState(3), NewState(10)
	var values []int
	stop := first.Subscribe(func(v int) { values = append(values, v) })
	first.Set(4)
	stop()
	stop()
	first.Set(5)
	if len(values) != 2 || values[0] != 3 || values[1] != 4 || first.Get() != 5 || second.Get() != 10 {
		t.Fatalf("state: %v %d %d", values, first.Get(), second.Get())
	}
}
