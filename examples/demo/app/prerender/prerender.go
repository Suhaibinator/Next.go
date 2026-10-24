//go:build !js

package prerenderdemo

import (
	nextgo "github.com/Suhaibinator/Next.go"
	"time"
)

func Prerender() *nextgo.PrerenderPolicy {
	return &nextgo.PrerenderPolicy{HostIndependent: true, Revalidate: time.Hour, Tags: []string{"prerender-demo"}}
}
