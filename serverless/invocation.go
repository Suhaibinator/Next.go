package serverless

import (
	"context"
	"strings"
)

// InvocationID returns the Runtime API invocation ID attached by Run, or an
// empty string when absent or unsuitable as bounded correlation metadata.
// It does not read HTTP headers, authenticate a request, or identify a trace.
// A nonportable ID can still be valid for the Runtime API transport.
func InvocationID(ctx context.Context) string {
	if ctx == nil {
		return ""
	}
	id, _ := ctx.Value(invocationIDKey{}).(string)
	return id
}

// GatewayRequestID returns the HTTP API v2 event's requestContext.requestId,
// attached by Run or Request.HTTPRequest, or an empty string when absent or
// nonportable. It is distinct from InvocationID and is not an authentication
// credential. Adapt also exposes it through its converted request context.
func GatewayRequestID(ctx context.Context) string {
	if ctx == nil {
		return ""
	}
	id, _ := ctx.Value(gatewayRequestIDKey{}).(string)
	return id
}

type invocationIDKey struct{}
type gatewayRequestIDKey struct{}

// correlationID bounds metadata independently of transport validation. Never
// truncate or normalize an identifier: doing so could conflate unrelated work.
// Clone prevents a short ID from retaining an entire decoded event envelope.
func correlationID(id string) string {
	if len(id) == 0 || len(id) > 256 {
		return ""
	}
	for i := 0; i < len(id); i++ {
		if id[i] < 0x21 || id[i] > 0x7e {
			return ""
		}
	}
	return strings.Clone(id)
}

func withInvocationID(ctx context.Context, id string) context.Context {
	return context.WithValue(ctx, invocationIDKey{}, correlationID(id))
}

func withGatewayRequestID(ctx context.Context, id string) context.Context {
	// An absent or omitted value must shadow an inherited event's metadata.
	return context.WithValue(ctx, gatewayRequestIDKey{}, correlationID(id))
}
