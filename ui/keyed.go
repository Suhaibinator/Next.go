package ui

import (
	"fmt"
	"github.com/Suhaibinator/Next.go/internal/keyed"
	"github.com/Suhaibinator/Next.go/internal/renderowner"
	"io"
	"maps"
	"strings"
)

// Keyed marks one natural element as a collection row. It introduces no wrapper
// and does not mutate attrs. Keys are public data, stable across value changes.
func Keyed(tag, key string, attrs Attrs, children ...Node) Node {
	owned := maps.Clone(attrs)
	if owned == nil {
		owned = Attrs{}
	}
	var conflict bool
	for name := range owned {
		if strings.EqualFold(name, "data-nextgo-key") {
			conflict = true
		}
	}
	owned["data-nextgo-key"] = key
	return nodeFunc(func(w io.Writer) error {
		if conflict {
			return fmt.Errorf("ui: keyed marker already supplied")
		}
		if err := keyed.ValidateKey(key); err != nil {
			return err
		}
		node := El(tag, owned, children...)
		ctx := RenderContext(w)
		if renderowner.RecorderFrom(ctx) == nil {
			return node.Render(w)
		}
		return RenderTo(renderowner.With(ctx, renderowner.Frame{Kind: "keyed", ID: key, Tag: strings.ToLower(tag)}), w, node)
	})
}
