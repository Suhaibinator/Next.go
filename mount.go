package nextgo

import (
	"io/fs"
	"net/http"
	"net/url"
	"strings"

	"github.com/Suhaibinator/Next.go/httpx"
	"github.com/Suhaibinator/Next.go/internal/paths"
	"github.com/Suhaibinator/Next.go/telemetry"
)

func localRequest(r *http.Request, base string) (*http.Request, bool) {
	escaped, ok := paths.Strip(base, r.URL.EscapedPath())
	if !ok {
		return nil, false
	}
	decoded, err := url.PathUnescape(escaped)
	if err != nil {
		return nil, false
	}
	copy := r.Clone(r.Context())
	copy.URL.Path = decoded
	copy.URL.RawPath = escaped
	return copy, true
}
func assetRequest(r *http.Request) bool {
	if strings.HasPrefix(r.URL.Path, "/assets/") {
		return true
	}
	if !strings.HasPrefix(r.URL.Path, "/_nextgo/") {
		return false
	}
	name := strings.SplitN(strings.TrimPrefix(r.URL.Path, "/_nextgo/"), "/", 2)[0]
	switch name {
	case "actions", "loading", "image", "events", "draft", "offline":
		return false
	}
	return true
}

// AssetHandler serves only the embedded public/runtime asset trees at their
// unprefixed paths. It intentionally has no application middleware, loaders or
// endpoints. Explicit HeaderRules apply to the unprefixed asset path.
// Mount it with http.StripPrefix for a separate public asset origin.
// Public files must never contain secrets. CORS permits anonymous WASM/font use.
func (a *App) AssetHandler() http.Handler { return a.assetHandler }
func (a *App) serveAssets(w http.ResponseWriter, r *http.Request) bool {
	if !assetRequest(r) {
		return false
	}
	telemetry.SetRoute(r.Context(), "/_nextgo/:build/*asset")
	if strings.HasPrefix(r.URL.Path, "/assets/") {
		telemetry.SetRoute(r.Context(), "/assets/*asset")
	}
	for _, segment := range strings.Split(r.URL.Path, "/") {
		segment = strings.ToLower(segment)
		if segment == ".env" || strings.HasPrefix(segment, ".env.") {
			http.NotFound(w, r)
			return true
		}
	}
	if a.config.Assets == nil {
		http.NotFound(w, r)
		return true
	}
	if r.Method != "GET" && r.Method != "HEAD" {
		w.Header().Set("Allow", "GET, HEAD")
		w.WriteHeader(405)
		return true
	}
	sub, prefix := "_nextgo", "/_nextgo/"
	if strings.HasPrefix(r.URL.Path, "/assets/") {
		sub, prefix = "public", "/assets/"
	}
	files, err := fs.Sub(a.config.Assets, sub)
	if err != nil {
		http.NotFound(w, r)
		return true
	}
	// Never advertise immutable caching for missing versions or directories.
	if sub == "_nextgo" {
		name := strings.TrimPrefix(r.URL.Path, prefix)
		if info, err := fs.Stat(files, name); err == nil && info.Mode().IsRegular() {
			w.Header().Set("Cache-Control", "public, max-age=31536000, immutable")
		}
	}
	w.Header().Set("X-Content-Type-Options", "nosniff")
	http.StripPrefix(prefix, http.FileServer(http.FS(files))).ServeHTTP(w, r)
	return true
}
func (a *App) initAssetHandler() {
	a.assetHandler = httpx.Compression(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Access-Control-Allow-Origin", "*")
		w.Header().Set("X-Content-Type-Options", "nosniff")
		var headerOK bool
		w, r, headerOK = a.bindHeaderRules(w, r)
		if !headerOK {
			return
		}
		if !a.serveAssets(w, r) {
			http.NotFound(w, r)
		}
	}))
}
