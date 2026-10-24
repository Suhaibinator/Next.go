package nextgo

import (
	"fmt"
	"time"

	"github.com/Suhaibinator/Next.go/ui"
)

// StreamingOptions bounds rendering of pages which explicitly set Page.Stream.
// Zero fields select 32 admitted renders, 64 running deferred callbacks and a
// 30-second render budget. Admission is immediate; a full render budget returns
// HTTP 503 before the shell is committed. Admitted workers wait for loader
// capacity within the render deadline. Limits are shared by locale mounts.
//
// The budget starts after page/layout/metadata functions return. It does not
// limit ordinary handlers, non-streaming pages, arbitrary allocations or
// goroutines created by application code. Callbacks must respect context; a
// callback ignoring cancellation keeps its loader slot until it returns.
// Progressive render loops stop at the deadline even if a callback is stuck;
// buffered legacy/HEAD rendering calls callbacks synchronously and therefore
// also retains its render slot until such a callback returns.
type StreamingOptions struct {
	MaxStreams int
	MaxLoaders int
	Timeout    time.Duration
}

func initStreaming(c *Config) error {
	if c.Streaming.MaxStreams == 0 {
		c.Streaming.MaxStreams = 32
	}
	if c.Streaming.MaxLoaders == 0 {
		c.Streaming.MaxLoaders = 64
	}
	if c.Streaming.Timeout == 0 {
		c.Streaming.Timeout = 30 * time.Second
	}
	if c.Streaming.MaxStreams < 1 || c.Streaming.MaxStreams > 1024 || c.Streaming.MaxLoaders < 1 || c.Streaming.MaxLoaders > 4096 || c.Streaming.Timeout < time.Millisecond || c.Streaming.Timeout > 5*time.Minute {
		return fmt.Errorf("nextgo: streaming requires 1..1024 streams, 1..4096 loaders and a 1ms..5m timeout")
	}
	if c.streamLimiter == nil {
		var err error
		c.streamLimiter, err = ui.NewStreamLimiter(c.Streaming.MaxStreams, c.Streaming.MaxLoaders)
		return err
	}
	return nil
}

func streamBusyPage() Page {
	return Page{Title: "Temporarily unavailable", Body: ui.El("main", nil,
		ui.El("h1", nil, ui.Text("Temporarily unavailable")),
		ui.El("p", nil, ui.Text("Too many pages are loading. Please try again shortly."))),
	}
}
