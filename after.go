package nextgo

import (
	"context"
	"errors"

	"github.com/Suhaibinator/Next.go/internal/postresponse"
	"github.com/Suhaibinator/Next.go/telemetry"
)

// AfterOptions configures bounded, best-effort work after request handling.
// Defaults: four running callbacks, 64 pending callbacks per application,
// sixteen callbacks per request, and a ten-second budget including queue time.
// See After for cancellation and deployment semantics.
type AfterOptions = postresponse.Options

var (
	ErrAfterScope    = postresponse.ErrScope
	ErrAfterCapacity = postresponse.ErrCapacity
	ErrAppClosed     = postresponse.ErrClosed
	ErrAfterCache    = errors.New("nextgo: After is unavailable in shared public-page loaders; use request middleware")
)

// After reserves work while an application request is active. Native HTTP starts
// it after the application handler finishes; this does not certify that a proxy
// or client has received the response. Lambda Run waits until the Runtime API
// accepts the response, then drains the work before requesting another event.
//
// The callback receives request context values, a fresh Memo scope, and a bounded
// context independent of client disconnects. It must respect cancellation. Copy
// needed inputs during the request; never retain a ResponseWriter, request body,
// or mutable request/page state. Registration errors are synchronous; callback
// errors/panics are logged by type without changing an already produced response.
// Shared public-page loaders return ErrAfterCache; use request middleware for
// per-request work. Build-time parameter enumeration has no response lifecycle.
// This process-local facility has no persistence or retries. Use a durable queue
// for required mutations, payments, or other work that must survive process loss.
func After(ctx context.Context, run func(context.Context) error) error {
	if ctx != nil {
		if state, ok := ctx.Value(deferredMetadataReadonlyKey{}).(*responseState); ok {
			state.mu.Lock()
			state.err = ErrAfterDeferredMetadata
			state.mu.Unlock()
			return ErrAfterDeferredMetadata
		}
	}
	if run == nil {
		return ErrAfterScope
	}
	return postresponse.Register(ctx, func(work context.Context) error { return telemetry.Run(WithRequestMemo(work), "nextgo.after", run) })
}

// Close stops new After reservations, drains accepted work and shuts down tracing.
// Call it after http.Server.Shutdown, using an independent bounded context. An expired drain
// cancels callbacks; Go cannot terminate callbacks that ignore cancellation.
// Requests after Close remain servable but cannot register new After work.
func (a *App) Close(ctx context.Context) error {
	afterErr := a.after.Close(ctx)
	traceErr := a.config.Tracing.Shutdown(ctx)
	a.proxy.closeIdle()
	return errors.Join(afterErr, traceErr)
}

// FlushTelemetry processes pending recorder work. Platform adapters should call
// it after accepted completion callbacks and before freezing/reusing a worker.
func (a *App) FlushTelemetry(ctx context.Context) error { return a.config.Tracing.Flush(ctx) }
