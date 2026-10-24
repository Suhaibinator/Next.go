//go:build !js

package product

import (
	nextgo "github.com/Suhaibinator/Next.go"
	"time"
)

func Prerender() *nextgo.PrerenderPolicy {
	return &nextgo.PrerenderPolicy{HostIndependent: true, Revalidate: time.Hour, Fallback: nextgo.FallbackNotFound, Tags: []string{"catalog"}}
}
