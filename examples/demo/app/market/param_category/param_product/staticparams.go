//go:build !js

package product

import (
	"context"
	nextgo "github.com/Suhaibinator/Next.go"
	category "github.com/Suhaibinator/Next.go/examples/demo/app/market/param_category"
)

func StaticParams(ctx context.Context) ([]nextgo.Params, error) {
	parent := nextgo.StaticParentParams(ctx)
	var rows []nextgo.Params
	for _, name := range category.Products(parent["category"]) {
		rows = append(rows, nextgo.Params{"product": name})
	}
	return rows, nil
}
