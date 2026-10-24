package nextgo

import (
	"context"
	"fmt"
	pathconfig "github.com/Suhaibinator/Next.go/internal/paths"
	"github.com/Suhaibinator/Next.go/internal/postresponse"
	"net/url"
	"sort"
	"strings"
	"unicode/utf8"
)

// Params names decoded route values. Catch-all values use slash-separated
// segments, matching Context.Params; optional catch-alls may be absent/empty.
type Params map[string]string
type StaticParamsFunc func(context.Context) ([]Params, error)

// StaticPaths composes ancestor/page StaticParams enumerators and includes static
// page routes. Dynamic routes without an enumerator are omitted. Every result
// must select its declaring route (not a more-specific page/endpoint). Results
// are escaped, deduplicated and sorted, with at most 10,000 input rows/paths,
// 8 KiB per path and 16 MiB total URL data. Enumerators must honor cancellation;
// returned values are public build data, never request-specific secrets.
func (a *App) StaticPaths(ctx context.Context) ([]string, error) {
	if ctx == nil {
		return nil, fmt.Errorf("nextgo: StaticPaths requires a context")
	}
	if a.locales != nil {
		return a.locales.staticPaths(ctx, "")
	}
	if a.config.localeScope != nil {
		ctx = context.WithValue(ctx, localeContextKey{}, a.config.localeScope)
	}
	ctx = pathconfig.WithContext(ctx, a.paths)
	ctx = context.WithValue(ctx, cacheProfileContextKey{}, a.profiles)
	paths := map[string]bool{}
	enumeration := newStaticEnumeration()
	rows, total := 0, 0
	for _, route := range a.routes {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		if route.API != nil {
			continue
		}
		var values []Params
		if strings.ContainsAny(route.Pattern, ":*") {
			if route.StaticParams == nil && len(route.StaticParamsLayers) == 0 {
				continue
			}
			var err error
			values, err = enumeration.route(ctx, route)
			if err != nil {
				return nil, fmt.Errorf("static parameters for %s: %w", route.Pattern, err)
			}
		} else {
			values = []Params{nil}
		}
		if budget, ok := ctx.Value(localeStaticBudgetKey{}).(*localeStaticBudget); ok && budget.rows.Add(int64(len(values))) > 10000 {
			return nil, fmt.Errorf("locale static enumeration exceeds 10000 rows")
		}
		if len(values) > 10000-rows {
			return nil, fmt.Errorf("static enumeration exceeds 10000 rows")
		}
		rows += len(values)
		for _, params := range values {
			if err := ctx.Err(); err != nil {
				return nil, err
			}
			path, err := staticPath(route.Pattern, params)
			if err != nil {
				return nil, fmt.Errorf("static parameters for %s: %w", route.Pattern, err)
			}
			if len(path) > 8192 {
				return nil, fmt.Errorf("static path exceeds 8 KiB")
			}
			for _, selected := range a.routes {
				if _, ok := match(selected.Pattern, path); ok {
					if selected.Pattern != route.Pattern {
						return nil, fmt.Errorf("static path %q for %s is shadowed by %s", path, route.Pattern, selected.Pattern)
					}
					break
				}
			}
			if !paths[path] {
				total += len(path)
				if total > 16<<20 {
					return nil, fmt.Errorf("static paths exceed 16 MiB")
				}
				paths[path] = true
			}
		}
	}
	result := make([]string, 0, len(paths))
	for path := range paths {
		result = append(result, path)
	}
	sort.Strings(result)
	return result, nil
}

// StaticPathsForDomain enumerates one configured domain using that host's
// locale defaults and request-context URL helpers. Empty domain uses StaticPaths.
// Bounds apply to the complete selected artifact, across all its languages.
func (a *App) StaticPathsForDomain(ctx context.Context, domain string) ([]string, error) {
	if domain == "" {
		return a.StaticPaths(ctx)
	}
	if ctx == nil {
		return nil, fmt.Errorf("nextgo: StaticPathsForDomain requires a context")
	}
	if a.locales == nil {
		return nil, fmt.Errorf("nextgo: static export domain requires configured locale domains")
	}
	return a.locales.staticPaths(ctx, domain)
}

func enumerateParams(ctx context.Context, load StaticParamsFunc) ([]Params, error) {
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	ctx = WithRequestMemo(postresponse.BlockRegistration(ctx, ErrAfterScope))
	var values []Params
	err := fmt.Errorf("static parameter loader exited without returning")
	done := make(chan struct{})
	go func() {
		defer close(done)
		defer func() {
			if failure := recover(); failure != nil {
				err = fmt.Errorf("static parameter loader panic (%T)", failure)
			}
		}()
		values, err = load(ctx)
	}()
	select {
	case <-done:
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		return values, err
	case <-ctx.Done():
		return nil, ctx.Err()
	}
}

func staticPath(pattern string, params Params) (string, error) {
	if len(pattern) > 8192 {
		return "", fmt.Errorf("static path exceeds 8 KiB")
	}
	parts := segments(pattern)
	if len(params) > len(parts) {
		return "", fmt.Errorf("too many parameter names")
	}
	known := map[string]bool{}
	var output []string
	for _, part := range parts {
		if !strings.HasPrefix(part, ":") && !strings.HasPrefix(part, "*") {
			output = append(output, url.PathEscape(part))
			continue
		}
		optional := strings.HasSuffix(part, "?")
		name := strings.TrimSuffix(part[1:], "?")
		known[name] = true
		value, present := params[name]
		if len(value) > 8192 {
			return "", fmt.Errorf("parameter %s exceeds 8 KiB", name)
		}
		if !present || value == "" {
			if optional {
				continue
			}
			return "", fmt.Errorf("missing nonempty parameter %s", name)
		}
		parts := []string{value}
		if strings.HasPrefix(part, "*") {
			parts = strings.Split(value, "/")
		}
		for _, segment := range parts {
			if !utf8.ValidString(segment) || segment == "" || segment == "." || segment == ".." || strings.ContainsAny(segment, "/\\") {
				return "", fmt.Errorf("invalid segment for %s", name)
			}
			for _, r := range segment {
				if r < 32 || r == 127 {
					return "", fmt.Errorf("invalid control character for %s", name)
				}
			}
			output = append(output, url.PathEscape(segment))
		}
	}
	for name := range params {
		if !known[name] {
			return "", fmt.Errorf("unknown parameter %s", name)
		}
	}
	path := "/" + strings.Join(output, "/")
	if _, ok := match(pattern, path); !ok {
		return "", fmt.Errorf("parameters do not match route")
	}
	return path, nil
}
