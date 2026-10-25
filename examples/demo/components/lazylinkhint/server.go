//go:build !js

package lazylinkhint

import (
	"github.com/Suhaibinator/Next.go/examples/demo/components/linkhint"
	"github.com/Suhaibinator/Next.go/ui"
)

func View(p Props) ui.Node { return ui.Island("lazylinkhint", p, linkhint.Body(p)) }
