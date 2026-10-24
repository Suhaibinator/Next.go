//go:build !js

package category

import (
	"context"
	nextgo "github.com/Suhaibinator/Next.go"
)

func Categories(ctx context.Context) []string {
	if nextgo.CurrentLocale(ctx) == "fr" {
		return []string{"café", "tea"}
	}
	return []string{"coffee", "tea"}
}
func Products(category string) []string {
	switch category {
	case "coffee", "café":
		return []string{"espresso", "filter"}
	case "tea":
		return []string{"green"}
	}
	return nil
}
func StaticParams(ctx context.Context) ([]nextgo.Params, error) {
	var rows []nextgo.Params
	for _, name := range Categories(ctx) {
		rows = append(rows, nextgo.Params{"category": name})
	}
	return rows, nil
}
