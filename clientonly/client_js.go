//go:build js && wasm

// Package clientonly is a compile-time import barrier. Blank-import it in a
// package that requires browser APIs. Go rejects native imports because this
// package has no server implementation.
package clientonly
