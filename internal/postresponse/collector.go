package postresponse

import (
	"context"
	"sync"
)

type collectorKey struct{}
type completion func(context.Context, bool) <-chan struct{}

// Collector lets a platform release accepted work after publishing the response
// and await it before freezing/reusing the invocation. Finish must always run,
// including transport failures (accepted=false), to release reservations.
type Collector struct {
	mu      sync.Mutex
	sealed  bool
	entries []completion
}

func Collect(ctx context.Context) (context.Context, *Collector) {
	c := &Collector{}
	return context.WithValue(ctx, collectorKey{}, c), c
}
func (c *Collector) add(entry completion) bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.sealed {
		return false
	}
	c.entries = append(c.entries, entry)
	return true
}
func (c *Collector) Finish(ctx context.Context, accepted bool) {
	c.mu.Lock()
	if c.sealed {
		c.mu.Unlock()
		return
	}
	c.sealed = true
	entries := c.entries
	c.entries = nil
	c.mu.Unlock()
	waits := make([]<-chan struct{}, 0, len(entries))
	for _, entry := range entries {
		waits = append(waits, entry(ctx, accepted))
	}
	for _, done := range waits {
		select {
		case <-done:
		case <-ctx.Done():
			return
		}
	}
}
