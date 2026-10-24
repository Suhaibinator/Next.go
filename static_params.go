package nextgo

import (
	"context"
	"encoding/json"
	"fmt"
	"maps"
	"reflect"
	"slices"
	"strings"
	"unicode/utf8"

	"github.com/Suhaibinator/Next.go/internal/routing"
)

// StaticParamsLayer enumerates one ancestor prefix, outermost first. ID names
// the declaration, not a route or a data value. Reusing an ID declares the same
// callback/pattern across descendant pages. Generated IDs are Go import paths.
// A callback can bind parameters at or above Pattern, never below that prefix.
type StaticParamsLayer struct {
	ID      string
	Pattern string
	Load    StaticParamsFunc
}
type staticParentKey struct{}

// StaticParentParams returns an owned snapshot of already-bound ancestor values
// during StaticParams execution. It is nil outside enumeration and at the root.
// Mutating the returned map cannot change another generator's input.
func StaticParentParams(ctx context.Context) Params {
	if ctx == nil {
		return nil
	}
	params, _ := ctx.Value(staticParentKey{}).(Params)
	return maps.Clone(params)
}

type staticDeclaration struct {
	pattern string
	code    uintptr
}

func (a *App) initStaticParams() error {
	declarations := map[string]staticDeclaration{}
	register := func(id, pattern string, load StaticParamsFunc) error {
		if len(id) == 0 || len(id) > 1024 || !utf8.ValidString(id) || strings.ContainsAny(id, "\x00\r\n") || load == nil {
			return fmt.Errorf("invalid static parameter declaration")
		}
		declaration := staticDeclaration{pattern, reflect.ValueOf(load).Pointer()}
		if old, ok := declarations[id]; ok && old != declaration {
			return fmt.Errorf("conflicting static parameter declaration ID")
		}
		declarations[id] = declaration
		return nil
	}
	for i := range a.routes {
		r := &a.routes[i]
		r.StaticParamsLayers = slices.Clone(r.StaticParamsLayers)
		if r.StaticParamsID != "" {
			if err := register(r.StaticParamsID, r.Pattern, r.StaticParams); err != nil {
				return fmt.Errorf("route %s: %w", r.Pattern, err)
			}
		}
		if len(r.StaticParamsLayers) > 32 {
			return fmt.Errorf("route %s: static parameter ancestry exceeds 32 layers", r.Pattern)
		}
		if len(r.StaticParamsLayers) > 0 && (r.API != nil || !strings.ContainsAny(r.Pattern, ":*")) {
			return fmt.Errorf("route %s: static parameter ancestry requires a dynamic page", r.Pattern)
		}
		previous := "/"
		for _, layer := range r.StaticParamsLayers {
			if _, err := routing.Validate(layer.Pattern); err != nil {
				return fmt.Errorf("route %s: invalid static parameter prefix", r.Pattern)
			}
			if !strings.ContainsAny(layer.Pattern, ":*") || !staticPrefix(previous, layer.Pattern) || !staticPrefix(layer.Pattern, r.Pattern) {
				return fmt.Errorf("route %s: static parameter prefixes must follow route ancestry", r.Pattern)
			}
			if err := register(layer.ID, layer.Pattern, layer.Load); err != nil {
				return fmt.Errorf("route %s: %w", r.Pattern, err)
			}
			previous = layer.Pattern
		}
	}
	return nil
}
func staticPrefix(prefix, pattern string) bool {
	left, right := segments(prefix), segments(pattern)
	return len(left) <= len(right) && slices.Equal(left, right[:min(len(left), len(right))])
}

type staticEnumeration struct {
	loaded      map[string][]Params
	rows, bytes int
}

func newStaticEnumeration() *staticEnumeration {
	return &staticEnumeration{loaded: map[string][]Params{}}
}
func (s *staticEnumeration) charge(ctx context.Context, rows, bytes int) error {
	if rows > 10000-s.rows || bytes > 16<<20-s.bytes {
		return fmt.Errorf("static generator output exceeds 10000 rows or 16 MiB")
	}
	s.rows += rows
	s.bytes += bytes
	if shared, ok := ctx.Value(localeStaticBudgetKey{}).(*localeStaticBudget); ok {
		if shared.generated.Add(int64(rows)) > 10000 || shared.bytes.Add(int64(bytes)) > 16<<20 {
			return fmt.Errorf("locale static generator output exceeds bounds")
		}
	}
	return nil
}
func (s *staticEnumeration) load(ctx context.Context, id, pattern string, load StaticParamsFunc, parent Params) ([]Params, error) {
	encoded, _ := json.Marshal(parent)
	key := id + "\x00" + pattern + "\x00" + string(encoded)
	if values, ok := s.loaded[key]; ok {
		return values, nil
	}
	if err := s.charge(ctx, 0, len(key)); err != nil {
		return nil, err
	}
	ctx = context.WithValue(ctx, staticParentKey{}, maps.Clone(parent))
	values, err := enumerateParams(ctx, load)
	if err != nil {
		return nil, err
	}
	if err := s.charge(ctx, len(values), 0); err != nil {
		return nil, err
	}
	output := make([]Params, 0, len(values))
	for _, value := range values {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		if len(value) > len(segments(pattern)) {
			return nil, fmt.Errorf("too many generated parameter names")
		}
		merged := maps.Clone(parent)
		if merged == nil {
			merged = Params{}
		}
		for name, v := range value {
			if err := s.charge(ctx, 0, len(name)+len(v)); err != nil {
				return nil, err
			}
			if old, bound := parent[name]; bound && old != v {
				return nil, fmt.Errorf("static generator cannot replace parent parameter %s", name)
			}
			merged[name] = v
		}
		path, err := staticPath(pattern, merged)
		if err != nil {
			return nil, err
		}
		if len(path) > 8192 {
			return nil, fmt.Errorf("static generator path exceeds 8 KiB")
		}
		output = append(output, merged)
	}
	s.loaded[key] = output
	return output, nil
}
func (s *staticEnumeration) route(ctx context.Context, r Route) ([]Params, error) {
	if r.StaticParams == nil && len(r.StaticParamsLayers) == 0 {
		return nil, nil
	}
	// Incomplete ancestry does not invent values for a missing child enumerator.
	if r.StaticParams == nil {
		bound := map[string]bool{}
		for _, part := range segments(r.StaticParamsLayers[len(r.StaticParamsLayers)-1].Pattern) {
			if strings.HasPrefix(part, ":") || strings.HasPrefix(part, "*") {
				bound[strings.TrimSuffix(part[1:], "?")] = true
			}
		}
		for _, part := range segments(r.Pattern) {
			if (strings.HasPrefix(part, ":") || strings.HasPrefix(part, "*")) && !strings.HasSuffix(part, "?") && !bound[part[1:]] {
				return nil, nil
			}
		}
	}
	values := []Params{nil}
	for _, layer := range r.StaticParamsLayers {
		var next []Params
		for _, parent := range values {
			rows, err := s.load(ctx, layer.ID, layer.Pattern, layer.Load, parent)
			if err != nil {
				return nil, err
			}
			if len(rows) > 10000-len(next) {
				return nil, fmt.Errorf("static parameter expansion exceeds 10000 rows")
			}
			next = append(next, rows...)
		}
		values = next
	}
	if r.StaticParams != nil {
		var next []Params
		for _, parent := range values {
			id := r.StaticParamsID
			if id == "" {
				id = "\x00leaf:" + r.Pattern
			}
			rows, err := s.load(ctx, id, r.Pattern, r.StaticParams, parent)
			if err != nil {
				return nil, err
			}
			if len(rows) > 10000-len(next) {
				return nil, fmt.Errorf("static parameter expansion exceeds 10000 rows")
			}
			next = append(next, rows...)
		}
		values = next
	}
	return values, nil
}

func staticParameterIdentity(r Route) [][2]string {
	var layers [][2]string
	for _, layer := range r.StaticParamsLayers {
		layers = append(layers, [2]string{layer.ID, layer.Pattern})
	}
	if r.StaticParams != nil {
		layers = append(layers, [2]string{r.StaticParamsID, r.Pattern})
	}
	return layers
}
