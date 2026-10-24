package nextgo

import "context"

type publicContextKey struct{}

// PublicValue reads the frozen Config.Public snapshot associated with a request.
// Every value is serialized into the browser document; never put secrets here.
func PublicValue(ctx context.Context, key string) (string, bool) {
	values, _ := ctx.Value(publicContextKey{}).(map[string]string)
	value, ok := values[key]
	return value, ok
}
