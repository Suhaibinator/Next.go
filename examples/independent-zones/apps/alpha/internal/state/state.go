//go:build !js

// Package state holds only public, process-local demonstration values.
package state

import (
	"crypto/rand"
	"encoding/hex"
	"sync/atomic"
)

const NativeVersion = "alpha-v1"

var instance = func() string {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		panic(err)
	}
	return hex.EncodeToString(b[:])
}()
var revision atomic.Int64
var requests atomic.Uint64

func Instance() string         { return instance }
func Revision() int64          { return revision.Load() }
func Apply(amount int64) int64 { return revision.Add(amount) }
func NextRequest() uint64      { return requests.Add(1) }
