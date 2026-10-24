//go:build js && wasm && nextgo_module

package client

// Generated lazy executables delegate document ownership to the core. Keep
// this constant so calls to public APIs do not link a second router.
const moduleMode = true
