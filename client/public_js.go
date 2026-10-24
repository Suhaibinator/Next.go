//go:build js && wasm

package client

// PublicValue reads an explicitly published runtime setting from this document.
// It does not read process environment variables or compile values into WASM.
func PublicValue(key string) (string, bool) { value, ok := routeManifest.Public[key]; return value, ok }
