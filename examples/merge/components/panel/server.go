//go:build !js

package panel

import "github.com/Suhaibinator/Next.go/ui"

func View(p Props, key, schema string) ui.Node {
	return ui.ClientIslandWithOptions("panel", p, Body(p, false), ui.IslandOptions{Key: key, Schema: schema})
}
