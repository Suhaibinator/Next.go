//go:build !js

package workspacelazy

import (
	"context"

	"github.com/Suhaibinator/Next.go/examples/static-shell/components/workspace"
	"github.com/Suhaibinator/Next.go/ui"
)

func View(ctx context.Context) ui.Node {
	return ui.ClientIsland("workspacelazy", Props{Label: "Public workspace", Initial: 0}, workspace.Fallback(ctx))
}
