package navigation

import (
	"net/url"
	"testing"
)

func TestDocumentSubtreesPrecedePagesAndRespectEscapedSegments(t *testing.T) {
	m := Manifest{Documents: []string{"/blog/*nextgoDocument?"}, Routes: []Route{{Pattern: "/blog/new"}, {Pattern: "/blog/:slug"}, {Pattern: "/health", Endpoint: true}}}
	for _, tc := range []struct {
		path string
		want bool
	}{
		{"/blog", true}, {"/blog/", true}, {"/blog/new", true}, {"/blog/a%20b", true}, {"/blog/a/b", true},
		{"/%62log/a", true}, // Conservative decoded route matching; not a native mount alias.
		{"/blogger", false}, {"/blogs/a", false}, {"/Blog/a", false}, {"/other/blog", false},
		{"/blog%2Fa", false}, {"/blog/a%2Fb", false}, {"/blog/%2e%2e", false}, {"/blog/%5c", false}, {"/blog/%00", false}, {"/blog/%zz", false},
		{"/health", true}, {"/missing", false},
	} {
		decoded, _ := url.PathUnescape(tc.path)
		if got := m.IsEndpoint(tc.path, decoded); got != tc.want {
			t.Errorf("%q: endpoint=%v want %v", tc.path, got, tc.want)
		}
	}
	// URLs, rather than bare path strings, own query/fragment separation.
	for _, target := range []string{"/blog?q=one#part", "/blog/a?next=/other#part"} {
		u, err := url.Parse(target)
		if err != nil || !m.IsEndpoint(u.EscapedPath(), u.Path) {
			t.Fatal("query or fragment altered subtree selection", target, err)
		}
	}
}
