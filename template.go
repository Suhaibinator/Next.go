package nextgo

import (
	"fmt"
	"github.com/Suhaibinator/Next.go/internal/routing"
	"github.com/Suhaibinator/Next.go/ui"
	"strings"
)

// Template creates a segment-keyed wrapper. Generated routes use this for
// template.go exports; applications normally just define Template(c, children).
// pattern is the template directory's URL pattern, excluding transparent groups.
// The immediate child segment (including dynamic values) keys its DOM. Deeper
// segments and search parameters preserve it. Catch-all segments key all values.
func Template(id, pattern string, render LayoutFunc) LayoutFunc {
	_, patternErr := routing.Validate(pattern)
	return func(c *Context, children ui.Node) (ui.Node, error) {
		if patternErr != nil {
			return nil, patternErr
		}
		if id == "" || render == nil || c == nil || c.Request == nil || c.Request.URL == nil {
			return nil, fmt.Errorf("nextgo: invalid template configuration or context")
		}
		key := "template:" + id + ":" + templateKey(pattern, c.Request.URL.Path)
		outlet := ui.El("nextgo-outlet", ui.Attrs{"data-nextgo-outlet": key}, children)
		body, err := render(c, outlet)
		if err != nil {
			return nil, err
		}
		return ui.El("nextgo-layout", ui.Attrs{"data-nextgo-layout": key}, body), nil
	}
}

func templateKey(pattern, path string) string {
	parts := strings.Split(strings.TrimPrefix(path, "/"), "/")
	depth := 0
	if pattern != "/" {
		depth = len(strings.Split(strings.TrimPrefix(pattern, "/"), "/"))
	}
	if strings.Contains(pattern, "*") {
		depth = len(parts)
	} else {
		depth++
	}
	if depth > len(parts) {
		depth = len(parts)
	}
	return "/" + strings.Join(parts[:depth], "/")
}
