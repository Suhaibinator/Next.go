package paths

import (
	"net/url"
	"path"
	"strings"
)

// SlashPath canonicalizes an escaped application path. File-like paths,
// .well-known and framework/public asset namespaces always omit the optional
// terminal slash. Encoded delimiters stay escaped; callers validate URL input.
func (c Config) SlashPath(escaped string) string {
	trimmed := strings.TrimRight(escaped, "/")
	if trimmed == "" {
		return "/"
	}
	if c.TrailingSlash && c.SlashApplies(trimmed) {
		return trimmed + "/"
	}
	return trimmed
}
func (c Config) SlashApplies(escaped string) bool {
	trimmed := strings.Trim(escaped, "/")
	first, _, _ := strings.Cut(trimmed, "/")
	first, _ = url.PathUnescape(first)
	if first == "_nextgo" || first == "assets" || first == ".well-known" {
		return false
	}
	last := trimmed
	if index := strings.LastIndexByte(last, '/'); index >= 0 {
		last = last[index+1:]
	}
	last, _ = url.PathUnescape(last)
	return path.Ext(last) == ""
}

// Logical removes only the optional terminal slash for matching route patterns.
// It is not a general URL-cleaning operation and never collapses empty segments.
func Logical(escaped string) string {
	if escaped != "/" && strings.HasSuffix(escaped, "/") && !strings.HasSuffix(escaped, "//") {
		return strings.TrimSuffix(escaped, "/")
	}
	return escaped
}
