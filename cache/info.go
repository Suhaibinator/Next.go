package cache

import "time"

// Info describes the generation selected for a successful read. Ages use the
// Store clock; they are not independently refreshed by a cache hit. Miss denotes
// a completed/coalesced load, Fresh a fresh hit, and Stale a stale hit.
type Info struct {
	State                    State
	Age, FreshFor, ExpiresIn time.Duration
	// NoExpiry means there is no hard deadline. ExpiresIn is then zero;
	// eviction, invalidation and refresh error policies still apply.
	NoExpiry bool
}
type State string

const (
	Miss   State = "miss"
	Fresh  State = "fresh"
	Stale  State = "stale"
	Bypass State = "bypass"
)

func readInfo(now, loaded, fresh, expires time.Time, state State, noExpiry bool) Info {
	info := Info{State: state, Age: max(0, now.Sub(loaded)), FreshFor: max(0, fresh.Sub(now)), NoExpiry: noExpiry}
	if !noExpiry {
		info.ExpiresIn = max(0, expires.Sub(now))
	}
	return info
}
