//go:build js && wasm

package ui

import "github.com/Suhaibinator/Next.go/internal/mediatype"

func parseMediaType(value string) (string, error) {
	typeName, _, err := mediatype.ParseMediaType(value)
	return typeName, err
}
