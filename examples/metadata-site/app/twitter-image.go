//go:build !js

package app

import "context"

func TwitterImage(ctx context.Context) ([]byte, error) {
	return publicCard(ctx, "TWITTER / GO METADATA")
}
