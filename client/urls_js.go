//go:build js && wasm

package client

import "github.com/Suhaibinator/Next.go/internal/paths"

var deployment paths.Config

// URL maps an application-absolute path to its browser URL. Navigate, Replace
// and Prefetch accept browser URLs; use this helper for app-relative Go inputs.
func URL(local string) (string, error) { return deployment.URL(local) }
func MustURL(local string) string {
	value, err := URL(local)
	if err != nil {
		panic(err)
	}
	return value
}
func AssetURL(local string) (string, error) { return deployment.AssetURL(local) }
func MustAssetURL(local string) string {
	value, err := AssetURL(local)
	if err != nil {
		panic(err)
	}
	return value
}
