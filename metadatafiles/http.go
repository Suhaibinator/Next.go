//go:build !js

package metadatafiles

import (
	"net/http"
	"strconv"
	"strings"
)

// ServeHTTP matches final public escaped paths exactly. Mount Set at the root
// application dispatcher rather than stripping its BasePath a second time.
// These are public immutable startup bytes, independent of request credentials.
func (s *Set) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	name := ""
	if s != nil {
		name = s.byPath[r.URL.EscapedPath()]
	}
	if name == "" {
		w.Header().Set("Cache-Control", "no-store")
		http.NotFound(w, r)
		return
	}
	if r.Method != http.MethodGet && r.Method != http.MethodHead {
		w.Header().Set("Allow", "GET, HEAD")
		w.Header().Set("Cache-Control", "no-store")
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	resource := s.byName[name]
	entry := resource.entry
	w.Header().Set("Content-Type", entry.ContentType)
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.Header().Set("Cache-Control", "public, max-age=0, must-revalidate")
	w.Header().Set("ETag", entry.ETag)
	if matchETag(r.Header.Values("If-None-Match"), entry.ETag) {
		w.WriteHeader(http.StatusNotModified)
		return
	}
	w.Header().Set("Content-Length", strconv.FormatInt(entry.Size, 10))
	if r.Method != http.MethodHead {
		_, _ = w.Write(resource.data)
	}
}

func matchETag(values []string, want string) bool {
	// Oversized conditional metadata is ignored; it cannot allocate an
	// unbounded intermediate list or change the representation's authority.
	total := 0
	for _, line := range values {
		if len(line) > 8192-total {
			return false
		}
		total += len(line)
	}
	for _, line := range values {
		for _, part := range strings.Split(line, ",") {
			part = strings.TrimSpace(part)
			if part == "*" || strings.TrimPrefix(part, "W/") == want {
				return true
			}
		}
	}
	return false
}
