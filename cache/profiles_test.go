package cache

import (
	"fmt"
	"strings"
	"testing"
	"time"
)

func duration(value time.Duration) *time.Duration { return &value }

func TestProfileSpecsResolveEffectiveDefaultAndExplicitZero(t *testing.T) {
	registry, err := ResolveProfiles(nil, map[string]ProfileSpec{
		"default":   {Stale: duration(2 * time.Minute), Revalidate: duration(3 * time.Minute), Expire: duration(time.Hour)},
		"inherited": {},
		"immediate": {Stale: duration(0), Revalidate: duration(0)},
		"hours":     {Expire: duration(Never)},
	})
	if err != nil {
		t.Fatal(err)
	}
	want := Profile{Stale: 2 * time.Minute, Revalidate: 3 * time.Minute, Expire: time.Hour}
	if registry["default"] != want || registry["inherited"] != want {
		t.Fatal("default did not propagate", registry)
	}
	if p := registry["immediate"]; p.Stale != 0 || p.Revalidate != 0 || p.Expire != time.Hour {
		t.Fatal("explicit zeros inherited", p)
	}
	if p := registry["hours"]; p.Stale != want.Stale || p.Revalidate != want.Revalidate || p.Expire != Never {
		t.Fatal("overridden preset did not use default", p)
	}
	if registry["minutes"] != BuiltinProfiles()["minutes"] {
		t.Fatal("unmentioned preset changed")
	}
}

func TestProfileSpecsLiteralDefinitionsRemainLiteral(t *testing.T) {
	literal := Profile{Expire: time.Minute}
	registry, err := ResolveProfiles(map[string]Profile{"default": {Stale: time.Second, Revalidate: 2 * time.Second, Expire: Never}, "literal": literal}, map[string]ProfileSpec{"partial": {}})
	if err != nil || registry["literal"] != literal || registry["partial"] != registry["default"] {
		t.Fatal(registry, err)
	}
	registry["default"] = Profile{}
	if BuiltinProfiles()["default"].Expire != Never {
		t.Fatal("registry changed builtins")
	}
}

func TestProfileSpecResolutionOwnsValuesAndAllowsInlineDefaults(t *testing.T) {
	stale, fresh, expire := time.Second, 2*time.Second, time.Minute
	spec := ProfileSpec{Stale: &stale, Revalidate: &fresh, Expire: &expire}
	result, err := spec.Resolve(Profile{})
	if err != nil {
		t.Fatal(err)
	}
	registry, err := ResolveProfiles(nil, map[string]ProfileSpec{"value": spec})
	if err != nil {
		t.Fatal(err)
	}
	stale = 0
	fresh = 0
	expire = 0
	want := Profile{Stale: time.Second, Revalidate: 2 * time.Second, Expire: time.Minute}
	if result != want || registry["value"] != want {
		t.Fatal("resolved values retained pointers", result, registry)
	}
	if result, err := (ProfileSpec{}).Resolve(BuiltinProfiles()["default"]); err != nil || result.Expire != Never {
		t.Fatal(result, err)
	}
}

func TestProfileSpecValidationIsCompleteAndDeterministic(t *testing.T) {
	cases := []struct {
		full     map[string]Profile
		specs    map[string]ProfileSpec
		contains string
	}{
		{nil, map[string]ProfileSpec{"bad name": {}}, "name"},
		{nil, map[string]ProfileSpec{"default": {Expire: duration(0)}}, "default"},
		{nil, map[string]ProfileSpec{"bad": {Expire: duration(0)}}, "bad"},
		{nil, map[string]ProfileSpec{"bad": {Stale: duration(Never)}}, "bad"},
		{nil, map[string]ProfileSpec{"bad": {Revalidate: duration(Never)}}, "bad"},
		{nil, map[string]ProfileSpec{"bad": {Expire: duration(-2)}}, "bad"},
		{nil, map[string]ProfileSpec{"default": {Revalidate: duration(time.Minute)}, "bad": {Expire: duration(time.Minute)}}, "bad"},
		{map[string]Profile{"duplicate": {Expire: time.Minute}}, map[string]ProfileSpec{"duplicate": {}}, "both"},
		{map[string]Profile{"default": {Expire: time.Minute}}, map[string]ProfileSpec{"default": {}}, "both"},
		{nil, map[string]ProfileSpec{"z-invalid": {Expire: duration(0)}, "a-invalid": {Expire: duration(0)}}, "a-invalid"},
	}
	for _, tc := range cases {
		for i := 0; i < 8; i++ {
			r, err := ResolveProfiles(tc.full, tc.specs)
			if err == nil || r != nil || !strings.Contains(err.Error(), tc.contains) {
				t.Fatal(tc.contains, r, err)
			}
		}
	}
	full := map[string]Profile{}
	for i := 0; i < 256; i++ {
		full[fmt.Sprintf("p%d", i)] = Profile{Expire: time.Minute}
	}
	if _, err := ResolveProfiles(full, nil); err != nil {
		t.Fatal(err)
	}
	if _, err := ResolveProfiles(full, map[string]ProfileSpec{"extra": {}}); err == nil {
		t.Fatal("combined registry limit ignored")
	}
}
