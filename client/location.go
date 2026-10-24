package client

import "net/url"

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
