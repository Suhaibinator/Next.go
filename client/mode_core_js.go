//go:build js && wasm && !nextgo_module

package client

// A constant lets the compiler discard module-only forwarding branches.
const moduleMode = false
