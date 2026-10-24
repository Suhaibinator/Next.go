package cache

import (
	"fmt"
	"slices"
	"time"
)

// ProfileSpec is a partial profile. Nil inherits an age from the supplied
// default; a non-nil pointer explicitly sets its value, including zero or Never.
// Resolve copies values and retains no pointers. A zero Expire is invalid.
type ProfileSpec struct{ Stale, Revalidate, Expire *time.Duration }

// Resolve overlays explicitly supplied ages onto defaults, then validates the
// complete result. It does not mutate either input or introduce caching scopes.
func (s ProfileSpec) Resolve(defaults Profile) (Profile, error) {
	p := defaults
	if s.Stale != nil {
		p.Stale = *s.Stale
	}
	if s.Revalidate != nil {
		p.Revalidate = *s.Revalidate
	}
	if s.Expire != nil {
		p.Expire = *s.Expire
	}
	if err := p.Validate(); err != nil {
		return Profile{}, err
	}
	return p, nil
}

// ResolveProfiles returns an owned registry including built-ins. Full profiles
// are literal values; specs inherit the effective default. Resolve default first
// against the built-in default, then all other specs against that result. An
// overridden built-in spec also inherits default, not the old named preset.
// Unmentioned built-ins retain their explicit preset values. Names are bounded
// to 64 ASCII letters/digits/hyphens/underscores and 256 configured definitions.
// A name in both maps is an error. Inputs must not mutate during this call.
func ResolveProfiles(full map[string]Profile, specs map[string]ProfileSpec) (map[string]Profile, error) {
	if len(full)+len(specs) > 256 {
		return nil, fmt.Errorf("cache: at most 256 configured cache profiles")
	}
	names := make([]string, 0, len(full)+len(specs))
	for name := range full {
		names = append(names, name)
	}
	for name := range specs {
		names = append(names, name)
	}
	slices.Sort(names)
	for i, name := range names {
		if !validProfileName(name) {
			return nil, fmt.Errorf("cache: invalid cache profile name %q", name)
		}
		if i > 0 && name == names[i-1] {
			return nil, fmt.Errorf("cache: profile %q is defined in both full and partial maps", name)
		}
	}
	result := BuiltinProfiles()
	if p, ok := full["default"]; ok {
		if err := p.Validate(); err != nil {
			return nil, fmt.Errorf("cache profile default: %w", err)
		}
		result["default"] = p
	} else if spec, ok := specs["default"]; ok {
		p, err := spec.Resolve(result["default"])
		if err != nil {
			return nil, fmt.Errorf("cache profile default: %w", err)
		}
		result["default"] = p
	}
	for _, name := range names {
		if name == "default" {
			continue
		}
		p, ok := full[name]
		var err error
		if ok {
			err = p.Validate()
		} else {
			p, err = specs[name].Resolve(result["default"])
		}
		if err != nil {
			return nil, fmt.Errorf("cache profile %s: %w", name, err)
		}
		result[name] = p
	}
	return result, nil
}

func validProfileName(name string) bool {
	if len(name) == 0 || len(name) > 64 {
		return false
	}
	for _, r := range name {
		if !(r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' || r == '-' || r == '_') {
			return false
		}
	}
	return true
}
