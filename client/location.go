package client

import (
	"net/url"

	"github.com/Suhaibinator/Next.go/internal/navigation"
)

// Location is a value snapshot of the committed browser URL. Pathname is escaped
// and includes deployment and locale prefixes. Search and Hash include their
// leading '?' and '#' when present; URL is absolute. It describes the visible
// address, including intercepted routes, rather than any retained slot's route.
type Location struct {
	URL      string
	Pathname string
	Search   string
	Hash     string
}

// Query returns a new, independently mutable map using Go's net/url query
// parsing rules. Search retains the original encoding, including malformed
// fields that net/url may omit and repeated values that Get returns only once.
func (l Location) Query() url.Values {
	raw := l.Search
	if len(raw) > 0 && raw[0] == '?' {
		raw = raw[1:]
	}
	values, _ := url.ParseQuery(raw)
	return values
}

// RouteParams matches this committed escaped pathname against a caller-provided
// application route pattern. BasePath must be a complete literal deployment
// prefix. The returned decoded map is independently owned; optional catchalls
// have an empty value at their zero-tail path. A match does not authorize access
// or identify the active page. Search and Hash remain available separately.
func (l Location) RouteParams(pattern, basePath string) (map[string]string, bool) {
	return navigation.MatchRouteParams(pattern, basePath, l.Pathname)
}
