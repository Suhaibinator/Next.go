//go:build !js

package slug

import (
	"context"
	nextgo "github.com/Suhaibinator/Next.go"
	"github.com/Suhaibinator/Next.go/examples/demo/data"
)

// StaticParams declares public build-time post URLs; ordinary server requests
// can still load other slugs. The static exporter uses these when -paths is absent.
func StaticParams(ctx context.Context) ([]nextgo.Params, error) {
	posts, err := data.All(ctx)
	if err != nil {
		return nil, err
	}
	result := make([]nextgo.Params, 0, len(posts))
	for _, post := range posts {
		result = append(result, nextgo.Params{"slug": post.Slug})
	}
	return result, nil
}
