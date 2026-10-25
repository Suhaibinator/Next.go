//go:build !js

// Package resourcedata contains the native-only resource example loader. No
// server dependencies or request data are imported into its Go/WASM component.
package resourcedata

import (
	"context"
	"encoding/json"
	"time"

	"github.com/Suhaibinator/Next.go/examples/demo/components/resourcepanel"
	_ "github.com/Suhaibinator/Next.go/serveronly"
)

func Load(ctx context.Context, delay time.Duration) (resourcepanel.Data, []byte, error) {
	if delay > 0 {
		timer := time.NewTimer(delay)
		defer timer.Stop()
		select {
		case <-timer.C:
		case <-ctx.Done():
			return resourcepanel.Data{}, nil, context.Cause(ctx)
		}
	}
	if err := ctx.Err(); err != nil {
		return resourcepanel.Data{}, nil, context.Cause(ctx)
	}
	data := resourcepanel.Data{Message: "Public Go resource", Generated: time.Now().UTC().Format(time.RFC3339Nano)}
	body, err := json.Marshal(data)
	return data, body, err
}
