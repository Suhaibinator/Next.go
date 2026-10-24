// Package routeinfo contains browser-safe values describing active Go routes.
package routeinfo

import (
	"encoding/json"
	"fmt"
	"net/url"
	"strings"
	"unicode/utf8"

	"github.com/Suhaibinator/Next.go/internal/routing"
)

const MaxSelections = 128
const MaxSelectionBytes = 64 << 10
const MaxSegments = 256

// Selection describes the active logical URL segments below a layout/slot.
// Scope is an opaque, parent-bound identity. Segments excludes query strings and
// transparent Go group folders; a catch-all value occupies one joined segment.
type Selection struct {
	Scope    string   `json:"scope"`
	Name     string   `json:"name"`
	Segments []string `json:"segments"`
}

func (s Selection) Clone() Selection { s.Segments = append([]string{}, s.Segments...); return s }
func (s Selection) Segment() (string, bool) {
	if len(s.Segments) == 0 {
		return "", false
	}
	return s.Segments[0], true
}
func (s Selection) Equal(other Selection) bool {
	if s.Scope != other.Scope || s.Name != other.Name || len(s.Segments) != len(other.Segments) {
		return false
	}
	for i := range s.Segments {
		if s.Segments[i] != other.Segments[i] {
			return false
		}
	}
	return true
}
func (s Selection) Validate() error {
	if len(s.Scope) != 64 || s.Name == "" || len(s.Name) > 128 || len(s.Segments) > MaxSegments {
		return fmt.Errorf("invalid selection identity/count")
	}
	for _, r := range s.Scope {
		if !(r >= 'a' && r <= 'f' || r >= '0' && r <= '9') {
			return fmt.Errorf("invalid selection scope")
		}
	}
	for _, r := range s.Name {
		if !(r == '_' || r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9') {
			return fmt.Errorf("invalid selection name")
		}
	}
	total := 0
	for _, segment := range s.Segments {
		total += len(segment)
		if segment == "" || !utf8.ValidString(segment) || strings.ContainsAny(segment, "\x00\r\n") {
			return fmt.Errorf("invalid selected segment")
		}
	}
	if total > 4096 {
		return fmt.Errorf("selected segment byte bound exceeded")
	}
	return nil
}
func Parse(raw string) (Selection, error) {
	var result Selection
	if len(raw) > MaxSelectionBytes || json.Unmarshal([]byte(raw), &result) != nil {
		return result, fmt.Errorf("invalid selection encoding")
	}
	return result, result.Validate()
}

// Segments follows a matched logical pattern and removes the owner's prefix.
// Catch-all values are joined as one entry. All returned strings are decoded.
func Segments(pattern, path, base string) ([]string, error) {
	if _, err := routing.Validate(base); err != nil {
		return nil, err
	}
	params, ok := routing.Match(pattern, path)
	if !ok {
		return nil, fmt.Errorf("selection path %q does not match pattern %q", path, pattern)
	}
	if strings.Contains(base, "*") {
		if _, ok := routing.Match(base, path); !ok {
			return nil, fmt.Errorf("selection path is outside layout")
		}
		return []string{}, nil
	}
	depth := 0
	if base != "/" {
		depth = len(strings.Split(strings.TrimPrefix(base, "/"), "/"))
	}
	parts := strings.Split(strings.TrimPrefix(path, "/"), "/")
	if path == "/" {
		parts = nil
	}
	if len(parts) < depth {
		return nil, fmt.Errorf("selection path is outside layout")
	}
	prefix := "/" + strings.Join(parts[:depth], "/")
	if _, ok := routing.Match(base, prefix); !ok {
		return nil, fmt.Errorf("selection path is outside layout")
	}
	patterns := strings.Split(strings.TrimPrefix(pattern, "/"), "/")
	if pattern == "/" {
		patterns = nil
	}
	result := []string{}
	for i, part := range patterns {
		if i < depth {
			continue
		}
		switch {
		case strings.HasPrefix(part, "*"):
			value := params[strings.TrimSuffix(strings.TrimPrefix(part, "*"), "?")]
			if value != "" {
				result = append(result, value)
			}
		case strings.HasPrefix(part, ":"):
			result = append(result, params[part[1:]])
		default:
			value, err := url.PathUnescape(parts[i])
			if err != nil {
				return nil, err
			}
			result = append(result, value)
		}
	}
	return result, nil
}
