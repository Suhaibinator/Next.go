package nextgo

// RequiresServerHeader marks a page whose runtime dependencies require an HTTP
// server. The static exporter rejects the canonical value "1" before publishing.
const RequiresServerHeader = "X-Nextgo-Requires-Server"

// RequireServer declares that this page depends on runtime HTTP behavior which
// static publication cannot provide, such as an application API endpoint.
// Call it in a page, layout or boundary before returning, and propagate its
// error. It uses staged response controls: rendering/deferred work returns
// ErrHeadersCommitted, and public cached loaders return ErrPublicCacheResponse.
// It does not configure caching, authentication or deployment routing.
func (c *Context) RequireServer() error {
	return c.SetHeader(RequiresServerHeader, "1")
}
