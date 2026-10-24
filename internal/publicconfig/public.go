// Package publicconfig validates the bounded document-visible runtime settings.
package publicconfig

import (
	"fmt"
	"strings"
	"unicode/utf8"
)

// Clone returns an owned snapshot. Values are public data, never secrets.
func Clone(values map[string]string) (map[string]string, error) {
	if len(values) > 64 {
		return nil, fmt.Errorf("public configuration exceeds 64 keys")
	}
	if values == nil {
		return nil, nil
	}
	result := make(map[string]string, len(values))
	total := 0
	for key, value := range values {
		if len(key) == 0 || len(key) > 128 || !utf8.ValidString(key) || strings.ContainsAny(key, "\x00\r\n") || len(value) > 1024 || !utf8.ValidString(value) || strings.ContainsRune(value, 0) {
			return nil, fmt.Errorf("invalid public configuration key or value")
		}
		total += len(key) + len(value)
		if total > 16<<10 {
			return nil, fmt.Errorf("public configuration exceeds 16 KiB")
		}
		result[key] = value
	}
	return result, nil
}
