package cache

import "context"

type bypassKey struct{}

// WithBypass skips every Store read, write, shared flight and background
// refresh reached through this context. Request-local memoization is unaffected.
// Forward the supplied context to nested loaders to preserve this policy.
// Store concurrency limits still apply. Do not recursively enter the same
// admission-limited Store, including keys that would normally be cached hits.
func WithBypass(ctx context.Context) context.Context {
	return context.WithValue(ctx, bypassKey{}, true)
}

func Bypassed(ctx context.Context) bool {
	if ctx == nil {
		return false
	}
	bypass, _ := ctx.Value(bypassKey{}).(bool)
	return bypass
}

func (s *Store) bypass(ctx context.Context, loader Loader) (any, Info, error) {
	if s.slots != nil {
		select {
		case s.slots <- struct{}{}:
			defer func() { <-s.slots }()
		case <-ctx.Done():
			return nil, Info{}, ctx.Err()
		}
	}
	if err := ctx.Err(); err != nil {
		return nil, Info{}, err
	}
	value, err := invoke(ctx, loader)
	if err == nil {
		err = ctx.Err()
	}
	if err != nil {
		return nil, Info{}, err
	}
	return value, Info{State: Bypass}, nil
}
