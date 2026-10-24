package nextgo

import (
	"fmt"
	"github.com/Suhaibinator/Next.go/seo"
	"strings"
)

// MetadataFunc returns this directory's contribution. parent is an independent
// snapshot of already merged ancestors; the framework merges the return value.
// Functions run on the server after a successful page/layout load and before
// any document or stream is committed. They share the request Memo scope.
type MetadataFunc func(*Context, seo.Metadata) (seo.Metadata, error)

// MetadataLayer pairs a provider with the safe outer layout prefix used if it
// denies access. File routing generates these in outermost-to-innermost order.
type MetadataLayer struct {
	Load    MetadataFunc
	Layouts int
}

func (a *App) resolveMetadata(c *Context, route Route, page Page) (Page, error) {
	var err error
	page, err = normalizePageMetadata(page)
	if err != nil {
		return Page{}, err
	}
	inherited := a.localeMetadata(Page{}).Metadata
	for _, layer := range route.Metadata {
		if err := c.Err(); err != nil {
			return Page{}, err
		}
		contribution, err := layer.Load(c, seo.Merge(seo.Metadata{}, inherited))
		if err != nil {
			return Page{}, &layoutError{index: layer.Layouts, cause: err}
		}
		if err := c.Err(); err != nil {
			return Page{}, err
		}
		inherited, err = seo.ResolveContribution(inherited, contribution)
		if err != nil {
			return Page{}, err
		}
	}
	page.Metadata, err = seo.ResolveContribution(inherited, page.Metadata)
	return page, err
}

// Explicit page fields keep their legacy precedence. Named metadata is
// case-insensitive; normalize once so SSR and client updates agree on ownership.
func normalizePageMetadata(page Page) (Page, error) {
	normalized := make(map[string]string, len(page.Meta))
	for name, value := range page.Meta {
		key := strings.ToLower(name)
		if _, exists := normalized[key]; exists {
			return Page{}, fmt.Errorf("nextgo: duplicate case-insensitive metadata name %q", key)
		}
		normalized[key] = value
	}
	if description, ok := normalized["description"]; ok {
		if page.Description == "" && page.Metadata.Description == "" {
			page.Description = description
		}
		delete(normalized, "description")
	}
	if _, reserved := normalized["viewport"]; reserved {
		return Page{}, fmt.Errorf("nextgo: use Metadata.Viewport for the framework-owned viewport")
	}
	page.Meta = normalized
	return page, nil
}
