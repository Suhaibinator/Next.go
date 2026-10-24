// Package routing defines the route-pattern grammar shared by the application
// server and file-route generator. Patterns use decoded static segments, :name
// parameters, and terminal *name or *name? catchalls.
package routing

import (
	"fmt"
	"net/url"
	"strings"
	"unicode/utf8"
)

type kind uint8

const (
	static kind = iota
	parameter
	catchall
	optionalCatchall
)

type segment struct {
	kind  kind
	value string
}

func parse(pattern string) ([]segment, string, error) {
	if pattern == "/" {
		return nil, "/", nil
	}
	if !strings.HasPrefix(pattern, "/") || strings.HasSuffix(pattern, "/") {
		return nil, "", fmt.Errorf("invalid route %q: use an absolute path without a trailing slash", pattern)
	}
	parts := strings.Split(pattern[1:], "/")
	result := make([]segment, 0, len(parts))
	shape := make([]string, 0, len(parts))
	seen := map[string]bool{}
	for i, part := range parts {
		if !safeSegment(part) {
			return nil, "", fmt.Errorf("invalid segment in route %q", pattern)
		}
		s := segment{value: part}
		switch part[0] {
		case ':':
			s.kind, s.value = parameter, part[1:]
		case '*':
			s.kind, s.value = catchall, part[1:]
			if strings.HasSuffix(s.value, "?") {
				s.kind, s.value = optionalCatchall, strings.TrimSuffix(s.value, "?")
			}
			if i != len(parts)-1 {
				return nil, "", fmt.Errorf("catchall must be the final segment in route %q", pattern)
			}
		}
		if s.kind != static {
			if !identifier(s.value) || seen[s.value] {
				return nil, "", fmt.Errorf("invalid or repeated parameter %q in route %q", s.value, pattern)
			}
			seen[s.value] = true
		} else if strings.ContainsAny(part, "?*#") {
			return nil, "", fmt.Errorf("invalid static segment %q in route %q", part, pattern)
		}
		result = append(result, s)
		switch s.kind {
		case parameter:
			shape = append(shape, ":")
		case catchall:
			shape = append(shape, "*")
		case optionalCatchall:
			shape = append(shape, "*?")
		default:
			shape = append(shape, part)
		}
	}
	return result, "/" + strings.Join(shape, "/"), nil
}

func identifier(value string) bool {
	if value == "" {
		return false
	}
	for i, r := range value {
		if !(r == '_' || r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || i > 0 && r >= '0' && r <= '9') {
			return false
		}
	}
	return true
}

func safeSegment(value string) bool {
	if value == "" || value == "." || value == ".." || !utf8.ValidString(value) {
		return false
	}
	for _, r := range value {
		if r == '/' || r == '\\' || r < 32 || r == 127 {
			return false
		}
	}
	return true
}

// Validate checks one pattern and returns its canonical shape. Parameter names
// are erased from shapes, while required and optional catchalls stay distinct.
func Validate(pattern string) (string, error) {
	_, shape, err := parse(pattern)
	return shape, err
}

// Shape is an alias for Validate, useful when registering generated routes.
func Shape(pattern string) (string, error) { return Validate(pattern) }

// Covers reports whether every path matching target also matches pattern.
// Parameter names do not affect coverage. Both inputs use route grammar,
// rather than treating target placeholders as literal URL path segments.
func Covers(pattern, target string) bool {
	a, _, err := parse(pattern)
	if err != nil {
		return false
	}
	b, _, err := parse(target)
	if err != nil {
		return false
	}
	minimum := func(parts []segment) int {
		n := 0
		for _, part := range parts {
			if part.kind != optionalCatchall {
				n++
			}
		}
		return n
	}
	for i := 0; ; i++ {
		if i == len(a) {
			return i == len(b)
		}
		if a[i].kind == optionalCatchall {
			return true
		}
		if a[i].kind == catchall {
			return minimum(b[min(i, len(b)):]) > 0
		}
		if i == len(b) {
			return false
		}
		if b[i].kind == catchall || b[i].kind == optionalCatchall {
			// An arbitrary terminal tail can only be covered by parameters
			// followed by an unrestricted tail with no larger minimum length.
			for _, part := range a[i:] {
				if part.kind == static {
					return false
				}
			}
			last := a[len(a)-1].kind
			return (last == catchall || last == optionalCatchall) && minimum(a[i:]) <= minimum(b[i:])
		}
		if a[i].kind == static && (b[i].kind != static || a[i].value != b[i].value) {
			return false
		}
	}
}

// ValidatePatterns checks both the grammar and ambiguity of a complete route
// set. Static/parameter overlaps are allowed and resolved by Less.
func ValidatePatterns(patterns []string) error {
	for i, pattern := range patterns {
		if _, err := Validate(pattern); err != nil {
			return err
		}
		for _, previous := range patterns[:i] {
			if Conflicts(previous, pattern) {
				return fmt.Errorf("ambiguous routes %q and %q", previous, pattern)
			}
		}
	}
	return nil
}

// Conflicts reports ambiguous registrations. Equivalent parameter names,
// required/optional catchalls at the same prefix, and an optional catchall with
// its exact parent conflict. Invalid patterns conservatively conflict too.
func Conflicts(a, b string) bool {
	x, xs, xe := parse(a)
	y, ys, ye := parse(b)
	if xe != nil || ye != nil || xs == ys {
		return true
	}
	isCatch := func(s []segment) bool { return len(s) > 0 && s[len(s)-1].kind >= catchall }
	if isCatch(x) && isCatch(y) && len(x) == len(y) {
		return parentShape(xs) == parentShape(ys)
	}
	if len(x) > 0 && x[len(x)-1].kind == optionalCatchall && parentShape(xs) == ys {
		return true
	}
	return len(y) > 0 && y[len(y)-1].kind == optionalCatchall && parentShape(ys) == xs
}

func parentShape(shape string) string {
	i := strings.LastIndexByte(shape, '/')
	if i == 0 {
		return "/"
	}
	return shape[:i]
}

// Less orders patterns by a lexicographic specificity vector, then their
// spelling. Static segments precede parameters, which precede catchalls. A
// completed exact route precedes its extensions. This is a strict total order,
// including across different depths; invalid patterns fall back to spelling.
func Less(a, b string) bool {
	x, _, xe := parse(a)
	y, _, ye := parse(b)
	if xe != nil || ye != nil {
		// Keep invalid and valid patterns in separate groups so error fallback
		// cannot introduce comparator cycles when callers sort before validation.
		if (xe != nil) != (ye != nil) {
			return xe == nil
		}
		return a < b
	}
	for i := 0; i < len(x) && i < len(y); i++ {
		if x[i].kind != y[i].kind {
			return x[i].kind < y[i].kind
		}
	}
	if len(x) != len(y) {
		return len(x) < len(y)
	}
	return a < b
}

// Match matches an escaped URL path (without query or fragment). Each segment
// is decoded exactly once; encoded slashes, backslashes, dot segments, controls,
// invalid UTF-8, and empty segments do not match. Catchall values join decoded
// segments with '/'; an optional catchall with no tail has value "".
func Match(pattern, escapedPath string) (map[string]string, bool) {
	patternParts, _, err := parse(pattern)
	if err != nil || !strings.HasPrefix(escapedPath, "/") || strings.ContainsAny(escapedPath, "?#") {
		return nil, false
	}
	var parts []string
	if escapedPath != "/" {
		for _, escaped := range strings.Split(escapedPath[1:], "/") {
			value, err := url.PathUnescape(escaped)
			if err != nil || !safeSegment(value) {
				return nil, false
			}
			parts = append(parts, value)
		}
	}
	params := map[string]string{}
	for i, s := range patternParts {
		if s.kind >= catchall {
			if len(parts) < i || s.kind == catchall && len(parts) == i {
				return nil, false
			}
			params[s.value] = strings.Join(parts[i:], "/")
			return params, true
		}
		if i >= len(parts) {
			return nil, false
		}
		if s.kind == parameter {
			params[s.value] = parts[i]
		} else if s.value != parts[i] {
			return nil, false
		}
	}
	return params, len(patternParts) == len(parts)
}
