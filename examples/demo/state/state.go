//go:build !js

// Package state contains server-only process-local demonstration state.
// A real deployment should use authenticated handlers and durable shared storage.
package state

import (
	"github.com/Suhaibinator/Next.go/cache"
	"sync/atomic"
)

var Cache = cache.New(128, cache.WithMaxBytes(16<<20, nil))
var counter atomic.Int64

var optimisticCounter atomic.Int64

func OptimisticCount() int64                 { return optimisticCounter.Load() }
func OptimisticIncrement(amount int64) int64 { return optimisticCounter.Add(amount) }

func Count() int64                 { return counter.Load() }
func Increment(amount int64) int64 { return counter.Add(amount) }

var reportReviews atomic.Int64

func Reviews() int64      { return reportReviews.Load() }
func ReviewReport() int64 { return reportReviews.Add(1) }
