package ui

import (
	"context"
	"encoding/json"
	"fmt"
	"html"
	"io"
	"strconv"
	"strings"

	"github.com/Suhaibinator/Next.go/internal/keyed"
	"github.com/Suhaibinator/Next.go/internal/navigation"
	"github.com/Suhaibinator/Next.go/internal/renderowner"
)

// IslandOptions gives an explicit ClientIsland stable identity within its
// declared route/key ancestry. Both values are public browser metadata.
type IslandOptions struct{ Key, Schema string }

// ClientIslandWithOptions has ClientIsland's one successful ClientView ownership
// contract. Compatible typed props updates may retain that owned child; ordinary
// server islands are not granted arbitrary changed-body reconciliation.
func ClientIslandWithOptions(component string, props any, fallback Node, options IslandOptions) Node {
	return nodeFunc(func(w io.Writer) error {
		if RenderContext(w).Value(clientFallbackKey{}) == true {
			return fmt.Errorf("ui: client fallbacks cannot contain islands")
		}
		if keyed.ValidateKey(options.Key) != nil || !navigation.MergeSchema(options.Schema) {
			return fmt.Errorf("ui: invalid client island key/schema")
		}
		if strings.TrimSpace(component) == "" || fallback == nil {
			return fmt.Errorf("ui: client island requires a component and fallback")
		}
		data, err := json.Marshal(props)
		if err != nil {
			return fmt.Errorf("client island %s: %w", component, err)
		}
		var output clientFallbackHTML
		elements := 0
		ctx := context.WithValue(renderowner.WithoutRecorder(RenderContext(w)), clientFallbackKey{}, true)
		ctx = context.WithValue(ctx, clientFallbackElementsKey{}, &elements)
		if err := RenderTo(ctx, &output, nodeFunc(func(w io.Writer) error { return renderClientFallbackNode(w, fallback) })); err != nil {
			return fmt.Errorf("client island %s: %w", component, err)
		}
		ticket, err := renderowner.Begin(RenderContext(w), options.Key, component, options.Schema, string(data))
		if err != nil {
			return err
		}
		attrs := Attrs{"data-nextgo-client": "1", "data-nextgo-island": component, "data-nextgo-props": string(data)}
		if ticket != nil {
			attrs[navigation.MergeNodeAttribute] = strconv.Itoa(ticket.Node())
		}
		var root strings.Builder
		if err := RenderTo(RenderContext(w), &root, El("div", attrs, nodeFunc(func(w io.Writer) error { _, err := io.WriteString(w, output.String()); return err }))); err != nil {
			return err
		}
		if ticket != nil {
			// El's attribute order is deterministic. Derive the known prefix from
			// the captured values; never render the application Node again.
			prefix := `<div data-nextgo-client="1" data-nextgo-island="` + html.EscapeString(component) + `" ` + navigation.MergeNodeAttribute + `="` + strconv.Itoa(ticket.Node()) + `" data-nextgo-props="` + html.EscapeString(string(data)) + `">`
			if !strings.HasPrefix(root.String(), prefix) {
				return navigation.ErrMerge
			}
			if err := ticket.Capture(root.String(), len(prefix), output.Len()); err != nil {
				return err
			}
		}
		_, err = io.WriteString(w, root.String())
		return err
	})
}
