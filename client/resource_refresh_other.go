//go:build !js || !wasm

package client

// Native resources have no development browser runtime to retire.
func beginResourceRefreshActivity() (func(), error) { return nil, nil }
