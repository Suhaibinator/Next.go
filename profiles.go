package nextgo

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"slices"
	"strings"
	"time"

	"github.com/Suhaibinator/Next.go/cache"
	"github.com/Suhaibinator/Next.go/internal/navigation"
)

type cacheProfileContextKey struct{}

var ErrCacheProfileScope = errors.New("nextgo: cache profiles require an application context")

// LookupCacheProfile returns an owned value from this application's effective
// registry, including configured overrides. It is available in requests,
// completion work and StaticPaths enumeration. It does not namespace data-cache
// keys or implicitly cache a loader; use the returned Lifetime explicitly.
func LookupCacheProfile(ctx context.Context, name string) (cache.Profile, error) {
	if ctx == nil {
		return cache.Profile{}, ErrCacheProfileScope
	}
	profiles, _ := ctx.Value(cacheProfileContextKey{}).(map[string]cache.Profile)
	if profiles == nil {
		return cache.Profile{}, ErrCacheProfileScope
	}
	profile, ok := profiles[name]
	if !ok {
		return cache.Profile{}, fmt.Errorf("nextgo: unknown cache profile %q", name)
	}
	return profile, nil
}

// RevalidateTagProfile marks tagged server snapshots stale using the named
// profile's Expire window. The original hard deadline is never extended. It
// schedules no work until a later read; no browser/replica broadcast occurs.
// Use RevalidateTag instead when reads must wait for a fresh generation.
func (a *App) RevalidateTagProfile(tag, name string) error {
	profile, ok := a.profiles[name]
	if !ok {
		return fmt.Errorf("nextgo: unknown cache profile %q", name)
	}
	_, err := a.config.Cache.MarkStaleTag(tag, profile.Expire)
	return err
}

// RevalidatePathProfile marks path labels at or beneath path using a named
// profile's Expire window. Only Expire controls this revalidation window.
func (a *App) RevalidatePathProfile(path, name string) error {
	profile, ok := a.profiles[name]
	if !ok {
		return fmt.Errorf("nextgo: unknown cache profile %q", name)
	}
	_, err := a.config.Cache.MarkStalePathPrefix(path, profile.Expire)
	return err
}

func clearNavigationReuse(header http.Header) {
	header.Del("X-Nextgo-Prefetch")
	header.Del("Age")
	header.Del(navigation.StaleHeader)
}

func (a *App) initCacheProfiles() error {
	var err error
	a.profiles, err = cache.ResolveProfiles(a.config.CacheProfiles, a.config.CacheProfileSpecs)
	if err != nil {
		return fmt.Errorf("nextgo: %w", err)
	}
	for i := range a.routes {
		policy := a.routes[i].Cache
		if policy == nil {
			continue
		}
		owned := *policy
		owned.resolvedProfile = nil
		owned.Tags = slices.Clone(policy.Tags)
		if policy.Lifetime != nil {
			life := *policy.Lifetime
			owned.Lifetime = &life
		}
		a.routes[i].Cache = &owned
		if owned.Profile != "" || owned.ProfileSpec != nil {
			if owned.TTL != 0 || owned.Lifetime != nil || owned.Profile != "" && owned.ProfileSpec != nil {
				return fmt.Errorf("route %s: choose TTL, Lifetime, Profile or ProfileSpec", a.routes[i].Pattern)
			}
			var profile cache.Profile
			if owned.Profile != "" {
				var ok bool
				profile, ok = a.profiles[owned.Profile]
				if !ok {
					return fmt.Errorf("route %s: unknown cache profile %q", a.routes[i].Pattern, owned.Profile)
				}
			} else {
				profile, err = owned.ProfileSpec.Resolve(a.profiles["default"])
				if err != nil {
					return fmt.Errorf("route %s: inline cache profile: %w", a.routes[i].Pattern, err)
				}
			}
			owned.resolvedProfile = &profile
			owned.ProfileSpec = nil // Runtime retains resolved values, never caller pointers.
		}
	}
	return nil
}

func (a *App) cacheLifetime(policy *CachePolicy) (*cache.Lifetime, time.Duration) {
	if policy != nil && policy.resolvedProfile != nil {
		profile := *policy.resolvedProfile
		life := profile.Lifetime()
		return &life, profile.Stale
	}
	if policy != nil {
		return policy.Lifetime, 0
	}
	return nil, 0
}

func (a *App) cachePolicyIdentity() string {
	type policyIdentity struct {
		Pattern     string
		TTL         time.Duration
		Lifetime    *cache.Lifetime
		Stale       time.Duration
		Tags        []string
		SharedScope string   `json:",omitempty"`
		LayoutIDs   []string `json:",omitempty"`
		LayoutBases []string `json:",omitempty"`
	}
	var policies []policyIdentity
	for _, route := range a.routes {
		p := route.Cache
		if p == nil {
			continue
		}
		life, stale := a.cacheLifetime(p)
		if p.TTL <= 0 && life == nil {
			continue
		}
		tags := slices.Clone(p.Tags)
		slices.Sort(tags)
		tags = slices.Compact(tags)
		if len(tags) == 0 {
			tags = nil
		}
		scope := ""
		if p.Shared {
			scope = a.config.SharedCache.ScopeID()
		}
		var ids, bases []string
		if p.Shared {
			ids = route.LayoutIDs
			bases = route.LayoutBases
		}
		policies = append(policies, policyIdentity{route.Pattern, p.TTL, life, stale, tags, scope, ids, bases})
	}
	if len(policies) == 0 {
		return ""
	}
	slices.SortFunc(policies, func(a, b policyIdentity) int { return strings.Compare(a.Pattern, b.Pattern) })
	encoded, _ := json.Marshal(policies)
	sum := sha256.Sum256(encoded)
	return hex.EncodeToString(sum[:])
}
