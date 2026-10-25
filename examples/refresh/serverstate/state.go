//go:build !js

// Package serverstate exposes public opaque diagnostics for this development
// example. The token is not a credential and does not reveal an operating PID.
package serverstate

import (
	"crypto/rand"
	"encoding/hex"
	"sync/atomic"
)

const Source = "native-v1"

var instance = func() string {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		panic(err)
	}
	return hex.EncodeToString(b[:])
}()
var requests atomic.Uint64

func Instance() string    { return instance }
func NextRequest() uint64 { return requests.Add(1) }
