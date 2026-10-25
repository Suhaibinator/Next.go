package nextgo

import (
	"context"

	"github.com/Suhaibinator/Next.go/internal/publicorigin"
)

// PublicOrigin returns a fixed, trusted deployment declaration. Empty means
// this application has no declaration. It does not assert the request's actual
// TLS state or authenticate a caller; Request.TLS and RemoteAddr remain intact.
func PublicOrigin(ctx context.Context) string {
	return publicorigin.FromContext(ctx).String()
}

func bindConfigPublicOrigin(ctx context.Context, config Config) context.Context {
	if config.publicOrigin.String() == "" && !publicorigin.Present(ctx) {
		return ctx
	}
	return publicorigin.WithContext(ctx, config.publicOrigin)
}
