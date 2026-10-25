package nextgo

import (
	"context"

	"github.com/Suhaibinator/Next.go/internal/paths"
)

// PathConfig describes public URL placement. Application route patterns and
// handler keys stay relative to the app, regardless of its deployment prefix.
type PathConfig = paths.Config

// URL resolves an application-absolute URL through the current request's base
// path. Inputs are app paths, e.g. /posts, not already-mounted browser URLs.
// Ordinary relative/external HTML links retain their browser semantics.
func URL(ctx context.Context, local string) (string, error) { return requestPaths(ctx).URL(local) }
func MustURL(ctx context.Context, local string) string {
	value, err := URL(ctx, local)
	if err != nil {
		panic(err)
	}
	return value
}

// AssetURL resolves a path inside public/ using the configured public/runtime
// asset prefix (default: BasePath). CDN deployments must publish those assets
// with appropriate CORS headers; these values are public browser information.
func AssetURL(ctx context.Context, local string) (string, error) {
	if value, handled, err := managedOfflineAssetURL(ctx, local); handled {
		return value, err
	}
	return requestPaths(ctx).AssetURL(local)
}
func MustAssetURL(ctx context.Context, local string) string {
	value, err := AssetURL(ctx, local)
	if err != nil {
		panic(err)
	}
	return value
}

// Paths returns this application's validated deployment URL configuration.
func (a *App) Paths() PathConfig { return a.paths }

func mustPath(value string, err error) string {
	if err != nil {
		panic(err)
	}
	return value
}

func requestPaths(ctx context.Context) paths.Config {
	if request, ok := ctx.(*Context); ok {
		if request == nil || request.Context == nil {
			return paths.Config{}
		}
		ctx = request.Context
	}
	return paths.FromContext(ctx)
}
