//go:build !js

package metadataclear

import (
	nextgo "github.com/Suhaibinator/Next.go"
	"github.com/Suhaibinator/Next.go/examples/demo/media"
	"github.com/Suhaibinator/Next.go/seo"
)

const inheritedDescription = "Inherited public description awaiting an explicit clear."
const inheritedRobots = "noindex, nofollow"
const finalTitle = "Inherited metadata cleared — Next.go"

// Metadata supplies actual directory-provider values. Empty page literals
// inherit these values until the deferred contribution explicitly clears them.
func Metadata(_ *nextgo.Context, _ seo.Metadata) (seo.Metadata, error) {
	return seo.Metadata{
		Description: inheritedDescription,
		Canonical:   media.SiteURL + "/metadata-clear?phase=inherited",
		Robots:      inheritedRobots,
	}, nil
}
