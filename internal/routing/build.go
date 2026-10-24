package routing

import (
	"fmt"
	"net/url"
	"strings"
)

// Build constructs an escaped application path from decoded parameter segments.
// Each single parameter requires exactly one segment. Catchalls take a slice;
// optional catchalls permit an empty slice. Unknown parameters are rejected.
func Build(pattern string, values map[string][]string) (string, error) {
	parts, _, err := parse(pattern)
	if err != nil {
		return "", err
	}
	used := 0
	result := []string{}
	for _, part := range parts {
		if part.kind == static {
			result = append(result, url.PathEscape(part.value))
			continue
		}
		segments, present := values[part.value]
		if present {
			used++
		}
		if part.kind == parameter && len(segments) != 1 || part.kind == catchall && len(segments) == 0 {
			return "", fmt.Errorf("route %q: parameter %q has an invalid segment count", pattern, part.value)
		}
		for _, segment := range segments {
			if !safeSegment(segment) {
				return "", fmt.Errorf("route %q: parameter %q contains an invalid segment", pattern, part.value)
			}
			result = append(result, url.PathEscape(segment))
		}
	}
	if used != len(values) {
		return "", fmt.Errorf("route %q: unknown parameter", pattern)
	}
	return "/" + strings.Join(result, "/"), nil
}
