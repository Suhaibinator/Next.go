package nextgo

import (
	"net/http"
	"net/url"
	"strings"

	"github.com/Suhaibinator/Next.go/internal/paths"
)

// Middleware and file-routed pages/APIs use logical app paths, independent of
// their public slash spelling. Configured HTTP subrouters retain their raw URL.
func (a *App) logicalRequest(r *http.Request) *http.Request {
	if !a.paths.TrailingSlash || !a.paths.SlashApplies(r.URL.EscapedPath()) {
		return r
	}
	logical := paths.Logical(r.URL.EscapedPath())
	if logical == r.URL.EscapedPath() {
		return r
	}
	if _, err := a.paths.URL(logical); err != nil {
		return r
	}
	clone := r.Clone(r.Context())
	clone.URL.Path, _ = url.PathUnescape(logical)
	clone.URL.RawPath = logical
	return clone
}
func (a *App) sourceRequest(r *http.Request) *http.Request {
	source, err := url.ParseRequestURI(requestURI(r))
	if err != nil {
		return r
	}
	local, ok := a.paths.Strip(source.EscapedPath())
	if !ok {
		return r
	}
	clone := r.Clone(r.Context())
	clone.URL.Path, _ = url.PathUnescape(local)
	clone.URL.RawPath = local
	return clone
}
func (a *App) redirectCanonical(w http.ResponseWriter, r *http.Request) bool {
	source, err := url.ParseRequestURI(requestURI(r))
	if err != nil {
		return false
	}
	local, inside := a.paths.Strip(source.EscapedPath())
	if !inside {
		return false
	}
	if !a.paths.TrailingSlash && !strings.HasSuffix(source.EscapedPath(), "/") {
		return false
	}
	// Preserve the original v1 handling of repeated leading/trailing separators
	// when removing slashes. New slash-appending mode rejects malformed segments.
	if !a.paths.TrailingSlash {
		local = "/" + strings.TrimLeft(strings.TrimRight(local, "/"), "/")
	}
	if source.RawQuery != "" || source.ForceQuery {
		local += "?" + source.RawQuery
	}
	canonical, err := a.paths.URL(local)
	if err != nil {
		http.NotFound(w, r)
		return true
	}
	if canonical != source.RequestURI() {
		http.Redirect(w, r, canonical, http.StatusPermanentRedirect)
		return true
	}
	return false
}
