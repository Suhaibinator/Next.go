//go:build !js

package ignored

func Page() string { return "A private subtree never creates a named slot." }
