//go:build !js

package app

import (
	"time"

	nextgo "github.com/Suhaibinator/Next.go"
)

func configureGeneratedActions(config *nextgo.Config) {
	for i := range config.Routes {
		if config.Routes[i].Pattern == "/generated-actions" {
			config.Routes[i].Cache = &nextgo.CachePolicy{TTL: time.Hour, Tags: []string{"generated-actions-demo"}}
		}
	}
}
