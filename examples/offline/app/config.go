//go:build !js

package app

import (
	"os"
	"time"

	nextgo "github.com/Suhaibinator/Next.go"
)

func Configure(c *nextgo.Config) {
	c.BasePath = os.Getenv("NEXTGO_OFFLINE_BASE_PATH")
	for i := range c.Routes {
		switch c.Routes[i].Pattern {
		case "/", "/guide", "/offline":
			// These representations contain fixed, anonymous public content only.
			c.Routes[i].Cache = &nextgo.CachePolicy{TTL: time.Hour}
		}
	}
}
