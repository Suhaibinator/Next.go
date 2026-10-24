package routeinfo

import (
	"strings"

	"github.com/Suhaibinator/Next.go/internal/routing"
)

// BuildPath builds an escaped application-relative path from decoded segments.
// Generated routes packages use it on the server and in the browser. Resolve
// its result with nextgo.URL or client.URL to apply deployment/locale prefixes.
func BuildPath(pattern string, parameters map[string][]string) (string, error) {
	return routing.Build(pattern, parameters)
}

// SplitParam converts a decoded, slash-joined server catchall parameter into an
// independently owned segment slice. Empty optional parameters return nil.
func SplitParam(value string) []string {
	if value == "" {
		return nil
	}
	return strings.Split(value, "/")
}
