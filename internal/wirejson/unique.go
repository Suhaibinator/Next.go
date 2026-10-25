package wirejson

import "github.com/Suhaibinator/Next.go/internal/wirejson/scan"

// Unique validates one JSON value, rejecting duplicate decoded object keys and
// values nested more than 64 levels below the root. Like encoding/json's legacy
// decoder, it accepts malformed UTF-8 and unpaired UTF-16 surrogates in strings,
// replacing them with U+FFFD when comparing keys. It does not convert numbers.
// Callers bound the input size; scanner storage is bounded by that input and
// the nesting limit. No decoded string values or number values are retained.
func Unique(data []byte) error {
	return scan.Unique(data)
}
