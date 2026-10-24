package routing

import (
	"reflect"
	"sort"
	"strings"
	"testing"
)

func TestValidate(t *testing.T) {
	for _, tc := range []struct{ pattern, shape string }{
		{"/", "/"}, {"/about", "/about"}, {"/posts/:slug", "/posts/:"},
		{"/:section/:post_2", "/:/:"}, {"/docs/*rest", "/docs/*"},
		{"/docs/*rest?", "/docs/*?"}, {"/*rest?", "/*?"},
		{"/a:literal/hello world/日本語", "/a:literal/hello world/日本語"},
	} {
		t.Run(tc.pattern, func(t *testing.T) {
			got, err := Validate(tc.pattern)
			if err != nil || got != tc.shape {
				t.Fatalf("got %q, %v; want %q", got, err, tc.shape)
			}
			if alias, err := Shape(tc.pattern); err != nil || alias != got {
				t.Fatalf("Shape mismatch: %q %v", alias, err)
			}
		})
	}
	for _, pattern := range []string{
		"", "about", "/a/", "//a", "/a//b", "/a/./b", "/a/../b", "/a\\b", "/a\x00b", "/a\x7fb", "/\xff",
		"/:id/:id", "/:id/*id", "/:", "/:9id", "/:a-b", "/:id?", "/:a:b", "/:日本語",
		"/*", "/*?", "/*9rest", "/*rest/b", "/*rest?/b", "/*rest??", "/a?query", "/a#hash", "/a*b",
	} {
		t.Run(pattern, func(t *testing.T) {
			if _, err := Validate(pattern); err == nil {
				t.Fatalf("accepted %q", pattern)
			}
		})
	}
}

func TestMatch(t *testing.T) {
	for _, tc := range []struct {
		pattern, path string
		want          map[string]string
	}{
		{"/", "/", map[string]string{}},
		{"/about", "/about", map[string]string{}},
		{"/hello world", "/hello%20world", map[string]string{}},
		{"/日本語", "/%E6%97%A5%E6%9C%AC%E8%AA%9E", map[string]string{}},
		{"/posts/:slug", "/posts/hello%20world", map[string]string{"slug": "hello world"}},
		{"/posts/:slug", "/posts/a%3Fb%23c", map[string]string{"slug": "a?b#c"}},
		{"/posts/:slug", "/posts/%252F", map[string]string{"slug": "%2F"}},
		{"/docs/*rest", "/docs/one", map[string]string{"rest": "one"}},
		{"/docs/*rest", "/docs/one/two%20words/three", map[string]string{"rest": "one/two words/three"}},
		{"/docs/*rest?", "/docs", map[string]string{"rest": ""}},
		{"/docs/*rest?", "/docs/one/two", map[string]string{"rest": "one/two"}},
		{"/*rest?", "/", map[string]string{"rest": ""}},
		{"/*rest", "/one/two", map[string]string{"rest": "one/two"}},
		{"/:lang/docs/*rest?", "/en/docs", map[string]string{"lang": "en", "rest": ""}},
		{"/:lang/docs/*rest", "/en/docs/start/install", map[string]string{"lang": "en", "rest": "start/install"}},
	} {
		t.Run(tc.pattern+" "+tc.path, func(t *testing.T) {
			got, ok := Match(tc.pattern, tc.path)
			if !ok || !reflect.DeepEqual(got, tc.want) {
				t.Fatalf("got %v, %v; want %v", got, ok, tc.want)
			}
		})
	}
	for _, tc := range [][2]string{
		{"/", ""}, {"/", "//"}, {"/about", "/About"}, {"/about", "/about/"}, {"/about", "//about"},
		{"/posts/:id", "/posts"}, {"/posts/:id", "/posts/a/b"}, {"/docs/*rest", "/docs"},
		{"/docs/*rest?", "/doc"}, {"/*rest", "/"}, {"/:id/docs/*rest?", "/docs"},
		{"/invalid/", "/invalid"}, {"/*rest/no", "/a/no"},
	} {
		if got, ok := Match(tc[0], tc[1]); ok {
			t.Errorf("matched %q to %q: %v", tc[0], tc[1], got)
		}
	}
}

func TestEscapedPathSafety(t *testing.T) {
	for _, path := range []string{
		"docs/a", "/docs/", "/docs//a", "/docs/a/", "/docs/%", "/docs/%GG", "/docs/a%2fb", "/docs/%2Fa",
		"/docs/a%5Cb", "/docs/a\\b", "/docs/%00", "/docs/%1f", "/docs/%7f", "/docs/%ff", "/docs/.",
		"/docs/..", "/docs/%2E", "/docs/%2e%2e", "/docs/a?query", "/docs/a#fragment",
	} {
		for _, pattern := range []string{"/docs/:id", "/docs/*rest", "/docs/*rest?"} {
			if got, ok := Match(pattern, path); ok {
				t.Errorf("unsafe path %q matched %q: %v", path, pattern, got)
			}
		}
	}
}

func TestConflicts(t *testing.T) {
	for _, tc := range []struct {
		a, b string
		want bool
	}{
		{"/", "/", true}, {"/posts/:id", "/posts/:slug", true},
		{"/docs/*rest", "/docs/*tail", true}, {"/docs/*rest?", "/docs/*tail?", true},
		{"/docs/*rest", "/docs/*tail?", true}, {"/docs", "/docs/*rest?", true},
		{"/", "/*rest?", true}, {"/:lang/docs", "/:locale/docs/*rest?", true},
		{"/:lang/docs/*rest", "/:locale/docs/*tail?", true},
		{"/docs", "/docs/*rest", false}, {"/", "/*rest", false},
		{"/docs/:page", "/docs/*rest?", false}, {"/docs/static", "/docs/*rest", false},
		{"/docs/*rest?", "/blog/*rest?", false}, {"/docs/*rest?", "/docs/start/*rest?", false},
		{"/a/:id", "/:section/b", false}, {"/a", "/b", false},
		{"/bad/", "/good", true},
	} {
		if got := Conflicts(tc.a, tc.b); got != tc.want {
			t.Errorf("Conflicts(%q, %q) = %v, want %v", tc.a, tc.b, got, tc.want)
		}
		if got := Conflicts(tc.b, tc.a); got != tc.want {
			t.Errorf("Conflicts(%q, %q) asymmetric: %v, want %v", tc.b, tc.a, got, tc.want)
		}
	}
	if err := ValidatePatterns([]string{"/docs", "/docs/:id", "/docs/*rest"}); err != nil {
		t.Fatal(err)
	}
	for _, patterns := range [][]string{
		{"/docs", "/docs/*rest?"}, {"/docs/*rest", "/docs/*rest?"}, {"/:id", "/:name"}, {"/bad/"},
	} {
		if err := ValidatePatterns(patterns); err == nil {
			t.Errorf("accepted conflicting/invalid route set %v", patterns)
		}
	}
}

func TestLessIsStrictOrder(t *testing.T) {
	patterns := []string{
		"/a/:id", "/b/c", "/b", "/", "/a", "/a/b", "/a/b/c", "/a/b/:id", "/a/:id/c", "/:section/b",
		"/a/*rest", "/a/*rest?", "/*rest", "/*rest?", "/a/:id/*rest", "/a/:id/*rest?", "/z",
		"/a/:name", "bad", "/bad/", "",
	}
	for _, a := range patterns {
		if Less(a, a) {
			t.Fatalf("Less(%q, itself)", a)
		}
		for _, b := range patterns {
			if a != b && Less(a, b) == Less(b, a) {
				t.Fatalf("not asymmetric for %q, %q", a, b)
			}
			for _, c := range patterns {
				if Less(a, b) && Less(b, c) && !Less(a, c) {
					t.Fatalf("not transitive for %q < %q < %q", a, b, c)
				}
			}
		}
	}
}

func TestSpecificityWithCatchallsAcrossDepths(t *testing.T) {
	patterns := []string{"/*fallback", "/a/*rest", "/a/:id", "/a/new", "/a", "/a/new/edit", "/:section/new"}
	var permutations func(int)
	permutations = func(index int) {
		if index < len(patterns) {
			for i := index; i < len(patterns); i++ {
				patterns[index], patterns[i] = patterns[i], patterns[index]
				permutations(index + 1)
				patterns[index], patterns[i] = patterns[i], patterns[index]
			}
			return
		}
		ordered := append([]string(nil), patterns...)
		sort.Slice(ordered, func(i, j int) bool { return Less(ordered[i], ordered[j]) })
		for _, tc := range [][2]string{
			{"/a", "/a"}, {"/a/new", "/a/new"}, {"/a/new/edit", "/a/new/edit"}, {"/a/other", "/a/:id"},
			{"/a/other/more", "/a/*rest"}, {"/b/new", "/:section/new"}, {"/other/path", "/*fallback"},
		} {
			selected := ""
			for _, pattern := range ordered {
				if _, ok := Match(pattern, tc[0]); ok {
					selected = pattern
					break
				}
			}
			if selected != tc[1] {
				t.Fatalf("%v: %s matched %s, want %s", ordered, tc[0], selected, tc[1])
			}
		}
	}
	permutations(0)
}

// Invalid generated patterns and hostile escaped paths must remain harmless,
// and any successful parameter must consist solely of valid path segments.
func FuzzMatch(f *testing.F) {
	for _, seed := range [][2]string{{"/posts/:slug", "/posts/hello%20world"}, {"/docs/*path?", "/docs/a/b"}, {"/:x", "/%2F"}, {"/:x", "/%00"}, {"/", "/"}, {"/:x", "/%FF"}} {
		f.Add(seed[0], seed[1])
	}
	f.Fuzz(func(t *testing.T, pattern, path string) {
		params, ok := Match(pattern, path)
		if !ok {
			return
		}
		if _, err := Validate(pattern); err != nil {
			t.Fatalf("matched invalid pattern %q", pattern)
		}
		for _, value := range params {
			if value == "" {
				continue
			} // optional catchall
			for _, part := range strings.Split(value, "/") {
				if !safeSegment(part) {
					t.Fatalf("unsafe decoded parameter %q", value)
				}
			}
		}
	})
}
