//go:build !js

package app

import (
	"os"

	nextgo "github.com/Suhaibinator/Next.go"
	"github.com/Suhaibinator/Next.go/examples/static-shell/settings"
)

func Configure(c *nextgo.Config) {
	// These are trusted operator settings, read once while constructing the app.
	// Neither value is request data or serialized into the island's props.
	settings.Configure(os.Getenv("NEXTGO_STATIC_SHELL_MODE"))
	c.BasePath = os.Getenv("NEXTGO_STATIC_SHELL_BASE")
	c.StaticShell = &nextgo.StaticShell{
		Pattern:   "/workspace/*rest?",
		Component: settings.Component(),
	}
}
