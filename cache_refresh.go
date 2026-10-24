package nextgo

import (
	"context"

	"github.com/Suhaibinator/Next.go/cache"
	"github.com/Suhaibinator/Next.go/internal/postresponse"
	"github.com/Suhaibinator/Next.go/telemetry"
)

// CacheRefresh adapts the bounded request-completion scheduler to cache refresh
// work. Native HTTP runs it after the handler; Lambda publishes the response,
// then drains work before the next event. Capacity/closed/scope errors make the
// cache refresh synchronously. Refresh functions receive a fresh request Memo.
// Shared public-page loaders cannot reserve nested refresh work; the containing
// page's lifetime governs its snapshot instead. Cancellation remains cooperative.
func CacheRefresh(ctx context.Context) cache.RefreshScheduler {
	return func(run func(context.Context) error, finish func()) error {
		return postresponse.RegisterFinally(ctx, func(work context.Context) error {
			return telemetry.Run(WithRequestMemo(work), "nextgo.cache.refresh", run)
		}, finish)
	}
}
