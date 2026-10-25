// Package publicorigin carries an operator-declared public origin separately
// from the transport's TLS state and peer address. It is not request-header
// inference or proof that a public TLS connection actually occurred.
package publicorigin

import (
	"context"
	"fmt"
	"net/http"
	"net/url"

	"github.com/Suhaibinator/Next.go/internal/zonecontract"
)

type contextKey struct{}

// Origin is immutable and can only be constructed from a validated HTTP(S)
// authority. Its zero value clears an inherited declaration.
type Origin struct {
	value, scheme, host string
}

func Parse(raw string) (Origin, error) {
	value, err := zonecontract.CanonicalOrigin(raw)
	if err != nil {
		return Origin{}, err
	}
	u, err := url.Parse(value)
	if err != nil {
		return Origin{}, fmt.Errorf("invalid public origin")
	}
	return Origin{value: value, scheme: u.Scheme, host: u.Host}, nil
}

func (o Origin) String() string { return o.value }
func (o Origin) Scheme() string { return o.scheme }
func (o Origin) Host() string   { return o.host }

func WithContext(ctx context.Context, origin Origin) context.Context {
	return context.WithValue(ctx, contextKey{}, origin)
}

func FromContext(ctx context.Context) Origin {
	if ctx == nil {
		return Origin{}
	}
	origin, _ := ctx.Value(contextKey{}).(Origin)
	return origin
}

// Present distinguishes an explicit clearing marker from an absent marker.
func Present(ctx context.Context) bool {
	if ctx == nil {
		return false
	}
	_, present := ctx.Value(contextKey{}).(Origin)
	return present
}

// Request uses the fixed deployment declaration when present. Ordinary apps
// retain the actual Host/TLS policy; forwarded headers never select an origin.
func Request(request *http.Request) (Origin, error) {
	if request == nil {
		return Origin{}, fmt.Errorf("public origin requires a request")
	}
	if origin := FromContext(request.Context()); origin.value != "" {
		return origin, nil
	}
	scheme := "http"
	if request.TLS != nil {
		scheme = "https"
	}
	return Parse(scheme + "://" + request.Host)
}
