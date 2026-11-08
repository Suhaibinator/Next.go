//go:build !js

package counter

import (
	original "github.com/Suhaibinator/Next.go/examples/metadata-site/components/counter"
	"github.com/Suhaibinator/Next.go/ui"
)

func View(p Props) ui.Node { return original.View(p) }
