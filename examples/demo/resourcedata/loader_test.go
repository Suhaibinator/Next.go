//go:build !js

package resourcedata

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"
)

func TestResourceLoaderProducesPublicOwnedDTOAndObservesCancellation(t *testing.T) {
	data, body, err := Load(context.Background(), 0)
	if err != nil || data.Message != "Public Go resource" || !json.Valid(body) {
		t.Fatal(data, string(body), err)
	}
	original := data.Message
	body[0] = 'x'
	if data.Message != original {
		t.Fatal("DTO aliases encoded bytes")
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, body, err := Load(ctx, time.Minute); !errors.Is(err, context.Canceled) || len(body) != 0 {
		t.Fatal(string(body), err)
	}
}
