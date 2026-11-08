//go:build !js

package privatenative

func Page() string { return PublicMessage }
