//go:build !js

// Package privatehelpers contains routing-private, explicitly imported Go code.
// Its public demonstration text is deliberately rendered by another package.
package privatehelpers

const PublicMessage = "Imported from private_helpers; never registered as a route."

// These intentionally have non-routing signatures. Discovery must not treat
// convention-like symbols inside a private folder as page/handler exports.
