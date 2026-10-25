//go:build !js || !wasm

package ui

import "mime"

func parseMediaType(value string) (string, error) {
	typeName, _, err := mime.ParseMediaType(value)
	return typeName, err
}
