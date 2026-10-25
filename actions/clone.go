package actions

import "strings"

// Clone returns an independent registration snapshot. Handler, invalidation and
// logging functions are intentionally shared; their captured state must remain
// safe for concurrent requests, just as it must on the original router.
// Subsequent registrations on either router do not affect the other. A nil
// receiver returns nil.
func (r *Router) Clone() *Router {
	if r == nil {
		return nil
	}
	r.mu.RLock()
	defer r.mu.RUnlock()
	copy := &Router{options: cloneOptions(r.options), origins: make(map[string]bool, len(r.origins)), handlers: make(map[string]registration, len(r.handlers))}
	for origin, allowed := range r.origins {
		copy.origins[strings.Clone(origin)] = allowed
	}
	for name, entry := range r.handlers {
		copy.handlers[strings.Clone(name)] = registration{handler: entry.handler, revision: strings.Clone(entry.revision)}
	}
	return copy
}

func cloneOptions(options Options) Options {
	options.UnauthorizedChallenge = strings.Clone(options.UnauthorizedChallenge)
	if options.AllowedOrigins != nil {
		owned := make([]string, len(options.AllowedOrigins))
		for i, origin := range options.AllowedOrigins {
			owned[i] = strings.Clone(origin)
		}
		options.AllowedOrigins = owned
	}
	return options
}
