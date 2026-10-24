package routing

import "testing"

func TestPatternCoverageUsesRouteLanguages(t *testing.T) {
	for _, tc := range []struct {
		pattern, target string
		want            bool
	}{
		{"/", "/", true}, {"/", "/:id", false}, {"/:id", "/", false},
		{"/photos/:id", "/photos/:slug", true}, {"/photos/:id", "/photos/one", true}, {"/photos/one", "/photos/:id", false},
		{"/photos/*all?", "/photos/*rest?", true}, {"/photos/*all?", "/photos", true}, {"/photos/*all", "/photos/*rest?", false},
		{"/photos/:id", "/photos/*rest", false}, {"/photos/:id/*rest?", "/photos/*all", true}, {"/photos/:id/*rest?", "/photos/*all?", false},
		{"/photos/:id/*rest", "/photos/*all", false}, {"/photos/*all", "/photos/:id/*rest?", true},
		{"/photos/static/*all?", "/photos/*rest", false}, {"/photos/:id/static/*all?", "/photos/*rest", false},
		{"/photos/*all", "/other/*all", false}, {"relative", "/", false}, {"/", "relative", false},
	} {
		if got := Covers(tc.pattern, tc.target); got != tc.want {
			t.Errorf("Covers(%q,%q)=%v want %v", tc.pattern, tc.target, got, tc.want)
		}
	}
}

func FuzzPatternCoverageNeverAcceptsAnUncoveredConcretePath(f *testing.F) {
	f.Add("/photos/:id/*rest?", "/photos/*all", "/photos/one/two")
	f.Add("/photos/:id", "/photos/*all", "/photos/one/two")
	f.Add("/*all?", "/", "/")
	f.Fuzz(func(t *testing.T, pattern, target, path string) {
		if !Covers(pattern, target) {
			return
		}
		if _, matched := Match(target, path); matched {
			if _, covered := Match(pattern, path); !covered {
				t.Fatalf("coverage accepts %q over %q but misses %q", pattern, target, path)
			}
		}
	})
}
