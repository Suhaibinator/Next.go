//go:build !js

// Package shareddata demonstrates public shared JSON data, not a durable database.
package shareddata

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"os"
	"time"

	"github.com/Suhaibinator/Next.go/cache"
	"github.com/Suhaibinator/Next.go/cache/shared"
)

var ErrDisabled = errors.New("shared cache demo is not configured")

type Snapshot struct {
	Generation  string
	GeneratedAt string
	Process     int
}

// Client's credentials stay in native code; only Snapshot is rendered publicly.
// Data schema v1 is explicit and independent of a compiler/build fingerprint.
func Client() (*shared.Store, error) {
	endpoint := os.Getenv("NEXTGO_DEMO_SHARED_ENDPOINT")
	if endpoint == "" {
		return nil, ErrDisabled
	}
	b, err := shared.NewHTTP(endpoint, os.Getenv("NEXTGO_CACHE_TOKEN"), nil)
	if err != nil {
		return nil, err
	}
	return shared.New(b, "demo", "demo-shared-v1", shared.Options{})
}
func Load(ctx context.Context, fail bool, options ...shared.LoadOptions) (Snapshot, cache.Info, error) {
	client, err := Client()
	if err != nil {
		return Snapshot{}, cache.Info{}, err
	}
	return shared.Lookup(ctx, client, "snapshot", shared.Policy{FreshFor: 15 * time.Second, ExpireAfter: time.Minute, Tags: []string{"demo-shared"}, Paths: []string{"/shared-cache"}}, func(ctx context.Context) (Snapshot, error) {
		if err := ctx.Err(); err != nil {
			return Snapshot{}, err
		}
		// Demonstrate a transient repository outage without storing errors or
		// request data in the shared value. Successful generations are unchanged.
		if fail {
			return Snapshot{}, errors.New("demo shared data source unavailable")
		}
		var generation [16]byte
		if _, err := rand.Read(generation[:]); err != nil {
			return Snapshot{}, err
		}
		return Snapshot{Generation: hex.EncodeToString(generation[:]), GeneratedAt: time.Now().UTC().Format(time.RFC3339Nano), Process: os.Getpid()}, nil
	}, options...)
}

func MarkStale(ctx context.Context) error {
	client, err := Client()
	if err != nil {
		return err
	}
	_, err = client.MarkStale(ctx, shared.Selector{Kind: "tag", Value: "demo-shared"}, 30*time.Second)
	return err
}
func Expire(ctx context.Context) error {
	client, err := Client()
	if err != nil {
		return err
	}
	_, err = client.Expire(ctx, shared.Selector{Kind: "tag", Value: "demo-shared"})
	return err
}
