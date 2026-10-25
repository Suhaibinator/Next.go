//go:build !js

// Package actionviewstate owns an independent, public, process-local demo. Its
// values reset at restart; it does not provide a durable mutation transaction.
package actionviewstate

import (
	"context"
	"fmt"
	"sync"

	_ "github.com/Suhaibinator/Next.go/serveronly"
)

type Value struct {
	Count, Revision, Load int64
	ViewFail              bool
}

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

func Add(ctx context.Context, amount int64, viewFail bool) (Value, error) {
	mu.Lock()
	defer mu.Unlock()
	if err := ctx.Err(); err != nil {
		return Value{}, err
	}
	if current.Count+amount > 1_000_000 || current.Revision >= 1_000_000 {
		return Value{}, fmt.Errorf("demo counter capacity reached")
	}
	current.Count += amount
	current.Revision++
	current.ViewFail = viewFail
	return current, nil
}
