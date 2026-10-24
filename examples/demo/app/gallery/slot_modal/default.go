//go:build !js

package slot_modal

import (
	nextgo "github.com/Suhaibinator/Next.go"
	"github.com/Suhaibinator/Next.go/ui"
)

func Default(*nextgo.Context) (nextgo.Page, error) { return nextgo.Page{Body: ui.Fragment()}, nil }
