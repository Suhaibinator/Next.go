package nextgo

import (
	"crypto/sha256"
	"fmt"
	"net/http"
)

type documentRootKey struct{}

// Templates remount within a document; the outermost persistent layout owns
// its root. Unidentified/manual wrappers share the implicit document root.
func routeRootIdentity(route Route) string {
	for _, id := range route.LayoutIDs {
		if id != "" {
			return fmt.Sprintf("%x", sha256.Sum256([]byte("nextgo-root-layout-v1\x00"+id)))
		}
	}
	return fmt.Sprintf("%x", sha256.Sum256([]byte("nextgo-implicit-root-v1")))
}

func (a *App) initDocumentRoots() {
	roots := make(map[string]bool)
	for _, route := range a.routes {
		if route.Page != nil {
			roots[routeRootIdentity(route)] = true
		}
	}
	a.multipleRoots = len(roots) > 1
}

func (a *App) routeDocumentRoot(route Route) string {
	if !a.multipleRoots || route.Page == nil {
		return ""
	}
	return routeRootIdentity(route)
}

// Bind at route selection, after rewrites/interception and outside cached page
// snapshots. A cache hit must have exactly the same root as a fresh render.
func (a *App) requestDocumentRoot(r *http.Request) string {
	if !a.multipleRoots {
		return ""
	}
	if root, ok := r.Context().Value(documentRootKey{}).(string); ok {
		return root
	}
	// Global unmatched/error documents cannot borrow any route's root.
	return fmt.Sprintf("%x", sha256.Sum256([]byte("nextgo-unmatched-root-v1")))
}
