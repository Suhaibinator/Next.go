//go:build !js

package clientpanellazy

import (
	"github.com/Suhaibinator/Next.go/examples/demo/components/clientpanel"
	"github.com/Suhaibinator/Next.go/ui"
)

func View(p Props) ui.Node { return ui.ClientIsland("clientpanellazy", p, clientpanel.Fallback(p)) }
