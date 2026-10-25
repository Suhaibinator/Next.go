//go:build !js

// Package actionstate is an independent process-local generated-action demo.
// Neither its values nor its cache snapshots survive a process restart.
package actionstate

import (
	"context"
	"sync"

	_ "github.com/Suhaibinator/Next.go/serveronly"
)

type Value struct{ Count, Revision, Load int64 }

var mu sync.Mutex
var current Value

func Load(ctx context.Context) (Value, error) {
	mu.Lock()
	defer mu.Unlock()
	if err := ctx.Err(); err != nil {
		return Value{}, err
	}
	current.Load++
	return current, nil
}

func Add(ctx context.Context, amount int64) (Value, error) {
	mu.Lock()
	defer mu.Unlock()
	if err := ctx.Err(); err != nil {
		return Value{}, err
	}
	current.Count += amount
	current.Revision++
	return current, nil
}
