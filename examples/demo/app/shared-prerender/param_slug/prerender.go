//go:build !js

package sharedprerender

import (
	"os"
	"time"

	nextgo "github.com/Suhaibinator/Next.go"
)

func Prerender() *nextgo.PrerenderPolicy {
	return &nextgo.PrerenderPolicy{
		HostIndependent: true,
		Shared:          os.Getenv("NEXTGO_DEMO_SHARED_PRERENDER") == "true",
		Revalidate:      15 * time.Second,
		Expire:          time.Minute,
		Tags:            []string{"shared-prerender"},
		Fallback:        nextgo.FallbackBlocking,
	}
}
