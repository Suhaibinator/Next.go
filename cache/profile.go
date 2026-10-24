package cache

import (
	"fmt"
	"time"
)

// MaxClientStale bounds explicit Go navigation-cache reuse. HTTP/CDN caching
// remains a separate policy, and entry/byte limits still apply in the browser.
const MaxClientStale = 24 * time.Hour

// Profile combines server lifetime ages with the maximum browser age of
// a selected snapshot. Stale is measured from successful generation, not from
// each cache hit. Zero disables browser reuse. Use Profile.Lifetime for data
// caches, whose browser lifetime must be managed by their transport.
type Profile struct{ Stale, Revalidate, Expire time.Duration }

func (p Profile) Validate() error {
	if p.Stale < 0 || p.Stale > MaxClientStale {
		return fmt.Errorf("cache: profile Stale must be between zero and 24h")
	}
	return p.Lifetime().Validate()
}
func (p Profile) Lifetime() Lifetime { return Lifetime{Revalidate: p.Revalidate, Expire: p.Expire} }

// BuiltinProfiles returns an owned map of explicit profiles. There is no
// implicitly cached/default route; applications opt in per route. Names may be
// overridden or supplemented through Config.CacheProfiles.
func BuiltinProfiles() map[string]Profile {
	return map[string]Profile{
		"default": {Stale: 5 * time.Minute, Revalidate: 15 * time.Minute, Expire: Never},
		"seconds": {Stale: 30 * time.Second, Revalidate: time.Second, Expire: time.Minute},
		"minutes": {Stale: 5 * time.Minute, Revalidate: time.Minute, Expire: time.Hour},
		"hours":   {Stale: 5 * time.Minute, Revalidate: time.Hour, Expire: 24 * time.Hour},
		"days":    {Stale: 5 * time.Minute, Revalidate: 24 * time.Hour, Expire: 7 * 24 * time.Hour},
		"weeks":   {Stale: 5 * time.Minute, Revalidate: 7 * 24 * time.Hour, Expire: 30 * 24 * time.Hour},
		"max":     {Stale: 5 * time.Minute, Revalidate: 30 * 24 * time.Hour, Expire: 365 * 24 * time.Hour},
	}
}

// ReuseFor bounds a profile's browser window by this particular generation's
// age and hard expiry. Deduct time elapsed since lookup before advertising it.
func (i Info) ReuseFor(stale time.Duration) time.Duration {
	if i.NoExpiry {
		return max(0, stale-i.Age)
	}
	return max(0, min(stale-i.Age, i.ExpiresIn))
}
