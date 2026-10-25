package nextgo

import (
	"fmt"
	"slices"
	"strings"

	"github.com/Suhaibinator/Next.go/internal/paths"
)

const maxDocumentPaths = 64
const maxDocumentPathBytes = 64 << 10

// freezeDocumentPaths owns and canonicalizes literal logical subtree prefixes.
// This controls browser navigation only: it neither mounts another handler nor
// grants authority to serve a path, and it does not change native route matching.
func freezeDocumentPaths(c *Config) error {
	if len(c.DocumentPaths) > maxDocumentPaths {
		return fmt.Errorf("nextgo: at most %d document paths are allowed", maxDocumentPaths)
	}
	total := 0
	for _, prefix := range c.DocumentPaths {
		total += len(prefix)
		if total > maxDocumentPathBytes {
			return fmt.Errorf("nextgo: document paths exceed %d bytes", maxDocumentPathBytes)
		}
		if prefix == "" {
			return fmt.Errorf("nextgo: document paths require a non-root literal prefix")
		}
		if err := (paths.Config{BasePath: prefix}).Validate(); err != nil {
			return fmt.Errorf("nextgo: document path %q: %w", prefix, err)
		}
	}
	if len(c.DocumentPaths) == 0 {
		c.DocumentPaths = nil
		return nil
	}
	prefixes := make([]string, len(c.DocumentPaths))
	for i, prefix := range c.DocumentPaths {
		prefixes[i] = strings.Clone(prefix)
	}
	slices.Sort(prefixes)
	c.DocumentPaths = slices.Compact(prefixes)
	return nil
}

// The existing route grammar's optional catchall covers both the exact prefix
// and its descendants while preserving complete-segment boundaries. Matching
// follows route semantics: escaped segments are decoded once, never as slashes.
func documentPathPatterns(prefixes []string) []string {
	if len(prefixes) == 0 {
		return nil
	}
	patterns := make([]string, len(prefixes))
	for i, prefix := range prefixes {
		patterns[i] = prefix + "/*nextgoDocument?"
	}
	return patterns
}
