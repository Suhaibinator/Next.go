package routing

import (
	"reflect"
	"strings"
	"testing"
)

func TestBuildEscapesAndRoundTripsDecodedSegments(t *testing.T) {
	for _, tc := range []struct {
		pattern string
		values  map[string][]string
		want    string
		params  map[string]string
	}{
		{"/", nil, "/", map[string]string{}},
		{"/static space/a+b", nil, "/static%20space/a+b", map[string]string{}},
		{"/posts/:slug", map[string][]string{"slug": {"Go WASM?#%"}}, "/posts/Go%20WASM%3F%23%25", map[string]string{"slug": "Go WASM?#%"}},
		{"/team/:id/docs/*path", map[string][]string{"id": {"東京"}, "path": {"a b", "%2f", "intro"}}, "/team/%E6%9D%B1%E4%BA%AC/docs/a%20b/%252f/intro", map[string]string{"id": "東京", "path": "a b/%2f/intro"}},
		{"/catalog/*path?", nil, "/catalog", map[string]string{"path": ""}},
	} {
		got, err := Build(tc.pattern, tc.values)
		if err != nil || got != tc.want {
			t.Fatalf("%s: %q %v", tc.pattern, got, err)
		}
		params, ok := Match(tc.pattern, got)
		if !ok || !reflect.DeepEqual(params, tc.params) {
			t.Fatalf("%s: round trip %v %v", tc.pattern, params, ok)
		}
	}
}

func TestBuildRejectsInvalidCardinalityAndUnsafeSegments(t *testing.T) {
	for _, tc := range []struct {
		pattern string
		values  map[string][]string
	}{
		{"/posts/:slug", nil}, {"/posts/:slug", map[string][]string{"slug": {"one", "two"}}},
		{"/docs/*path", map[string][]string{"path": nil}}, {"/catalog/*path?", map[string][]string{"extra": {"x"}}},
		{"/about", map[string][]string{"extra": {"x"}}}, {"relative", nil},
	} {
		if _, err := Build(tc.pattern, tc.values); err == nil {
			t.Fatal("accepted", tc)
		}
	}
	for _, value := range []string{"", ".", "..", "a/b", `a\b`, "\x00", "\x1f", "\x7f", string([]byte{255})} {
		for _, pattern := range []string{"/:value", "/*value", "/*value?"} {
			if _, err := Build(pattern, map[string][]string{"value": {value}}); err == nil {
				t.Fatalf("accepted unsafe %q", value)
			}
		}
	}
	errValue := "private/secret"
	_, err := Build("/:value", map[string][]string{"value": {errValue}})
	if strings.Contains(err.Error(), errValue) {
		t.Fatal("error contains parameter value")
	}
}

func FuzzBuildMatchRoundTrip(f *testing.F) {
	for _, s := range []string{"hello", "Go WASM", "%2f", "東京", "a?b#c", "a/b", ".."} {
		f.Add(s)
	}
	f.Fuzz(func(t *testing.T, value string) {
		path, err := Build("/posts/:slug", map[string][]string{"slug": {value}})
		if err != nil {
			return
		}
		params, ok := Match("/posts/:slug", path)
		if !ok || params["slug"] != value {
			t.Fatalf("round trip failed: %q -> %q -> %v", value, path, params)
		}
	})
}
