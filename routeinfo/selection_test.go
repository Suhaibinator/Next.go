package routeinfo

import (
	"encoding/json"
	"reflect"
	"strings"
	"testing"
)

func TestSegmentsFollowLogicalMatchedRoute(t *testing.T) {
	for _, tc := range []struct {
		pattern, path, base string
		want                []string
	}{
		{"/", "/", "/", []string{}},
		{"/posts/:slug", "/posts/caf%C3%A9", "/", []string{"posts", "café"}},
		{"/posts/:slug", "/posts/caf%C3%A9", "/posts", []string{"café"}},
		{"/teams/:id/posts/:slug", "/teams/one/posts/two", "/teams/:id", []string{"posts", "two"}},
		{"/docs/*parts", "/docs/a/b", "/docs", []string{"a/b"}},
		{"/docs/*parts?", "/docs", "/docs", []string{}},
		{"/docs/*parts?", "/docs/a/b", "/docs/*parts?", []string{}},
	} {
		got, err := Segments(tc.pattern, tc.path, tc.base)
		if err != nil || !reflect.DeepEqual(got, tc.want) {
			t.Errorf("%+v: %v, %v", tc, got, err)
		}
	}
	for _, tc := range [][3]string{{"/posts/:slug", "/other/a", "/"}, {"/posts/:slug", "/posts/a", "/outside"}, {"/docs/:id", "/docs/a/b", "/docs/*parts"}, {"/", "/", "relative"}} {
		if _, err := Segments(tc[0], tc[1], tc[2]); err == nil {
			t.Errorf("accepted mismatched selection %v", tc)
		}
	}
}

func TestSelectionSnapshotsAndBounds(t *testing.T) {
	original := Selection{Scope: strings.Repeat("a", 64), Name: "children", Segments: []string{"<café>"}}
	encoded, _ := json.Marshal(original)
	parsed, err := Parse(string(encoded))
	if err != nil || !parsed.Equal(original) {
		t.Fatal(parsed, err)
	}
	copy := original.Clone()
	copy.Segments[0] = "changed"
	if original.Segments[0] != "<café>" {
		t.Fatal("clone aliases its source")
	}
	if first, ok := original.Segment(); !ok || first != "<café>" {
		t.Fatal(first, ok)
	}
	if _, ok := (Selection{}).Segment(); ok {
		t.Fatal("empty selection has a first segment")
	}
	for _, mutate := range []func(*Selection){
		func(s *Selection) { s.Scope = strings.Repeat("A", 64) },
		func(s *Selection) { s.Name = "bad-name" },
		func(s *Selection) { s.Segments = []string{""} },
		func(s *Selection) { s.Segments = []string{"bad\nsegment"} },
		func(s *Selection) { s.Segments = []string{strings.Repeat("x", 4097)} },
		func(s *Selection) { s.Segments = make([]string, MaxSegments+1) },
	} {
		bad := original.Clone()
		mutate(&bad)
		if bad.Validate() == nil {
			t.Fatal("accepted invalid selection", bad)
		}
	}
	if _, err := Parse(strings.Repeat(" ", MaxSelectionBytes+1)); err == nil {
		t.Fatal("accepted oversized encoding")
	}
}
