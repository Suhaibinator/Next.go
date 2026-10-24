//go:build !js

package article

import (
	"context"
	nextgo "github.com/Suhaibinator/Next.go"
)

func StaticParams(context.Context) ([]nextgo.Params, error) {
	return []nextgo.Params{{"slug": "first"}}, nil
}
