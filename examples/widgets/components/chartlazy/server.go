//go:build !js

package chartlazy

import (
	"github.com/Suhaibinator/Next.go/examples/widgets/components/chart"
	"github.com/Suhaibinator/Next.go/ui"
)

func View(p Props) ui.Node { return ui.Island("chartlazy", p, chart.Body()) }
