//go:build !js

// Package serveronly is a compile-time import barrier. Blank-import it in a
// package that must never enter a browser bundle. Go rejects a JS build that
// reaches this package because it has no browser implementation.
package serveronly
