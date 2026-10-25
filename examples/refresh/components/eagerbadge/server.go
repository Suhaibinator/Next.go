//go:build !js

package eagerbadge

import "github.com/Suhaibinator/Next.go/ui"

func View() ui.Node {
	return ui.Island("eagerbadge", Props{}, ui.El("span", ui.Attrs{"data-refresh-eager": ""}, ui.Text("Shared page chrome from the server")))
}
