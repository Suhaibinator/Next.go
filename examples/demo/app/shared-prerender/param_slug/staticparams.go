//go:build !js

package sharedprerender

import (
	"context"

	nextgo "github.com/Suhaibinator/Next.go"
)

func StaticParams(context.Context) ([]nextgo.Params, error) {
	return []nextgo.Params{{"slug": "alpha"}, {"slug": "beta"}}, nil
}
