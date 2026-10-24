// Package jsonld validates the bounded, non-executable structured-data payload
// shared by server serialization and browser head updates.
package jsonld

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
)

const MaxBytes = 256 << 10

func Validate(data []byte) error {
	if len(data) == 0 || len(data) > MaxBytes {
		return fmt.Errorf("jsonld: require 1..%d bytes", MaxBytes)
	}
	if !json.Valid(data) {
		return fmt.Errorf("jsonld: invalid JSON")
	}
	trimmed := bytes.TrimSpace(data)
	switch trimmed[0] {
	case '{':
	case '[':
		var entities []json.RawMessage
		if err := json.Unmarshal(trimmed, &entities); err != nil {
			return err
		}
		if len(entities) > 64 {
			return fmt.Errorf("jsonld: too many entities")
		}
		for _, entity := range entities {
			if bytes.TrimSpace(entity)[0] != '{' {
				return fmt.Errorf("jsonld: array entries must be objects")
			}
		}
	default:
		return fmt.Errorf("jsonld: expected object or object array")
	}
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.UseNumber()
	depth := 0
	for {
		token, err := decoder.Token()
		if err == io.EOF {
			break
		}
		if err != nil {
			return err
		}
		if delimiter, ok := token.(json.Delim); ok {
			switch delimiter {
			case '{', '[':
				depth++
			case '}', ']':
				depth--
			}
			if depth > 64 {
				return fmt.Errorf("jsonld: nesting exceeds 64")
			}
		}
	}
	return nil
}
