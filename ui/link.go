package ui

import (
	"fmt"
	"io"
	"strings"
)

// PrefetchMode controls automatic work for a Link. Explicit client.Prefetch
// calls remain independent of link attributes.
type PrefetchMode string

const (
	// PrefetchAuto observes visible links in production, with intent fallback
	// when viewport observation is unavailable. The zero value is equivalent.
	PrefetchAuto PrefetchMode = "auto"
	// PrefetchIntent only speculates after pointer or keyboard focus intent.
	PrefetchIntent PrefetchMode = "intent"
	// PrefetchNone disables both viewport and intent speculation for this link.
	PrefetchNone PrefetchMode = "none"
)

type LinkOptions struct {
	Prefetch PrefetchMode
	Replace  bool
	// Attrs supplies ordinary anchor attributes, including target, download and
	// data-nextgo-reload. href and the prefetch/replace controls belong above.
	Attrs Attrs
}

// Link renders an ordinary escaped anchor that works before WASM starts.
// Its default opts into bounded production viewport prefetch. Actual navigation
// and response reuse still obey the router's URL, authorization and cache rules.
// Plain El("a", ...) anchors retain their existing intent-only behavior.
func Link(href string, options LinkOptions, children ...Node) Node {
	attrs := make(Attrs, len(options.Attrs)+3)
	for key, value := range options.Attrs {
		switch strings.ToLower(key) {
		case "href", "data-nextgo-prefetch", "data-nextgo-replace":
			return nodeFunc(func(io.Writer) error {
				return fmt.Errorf("ui.Link: %s is controlled by Link arguments", key)
			})
		}
		attrs[key] = value
	}
	attrs["href"] = href
	switch options.Prefetch {
	case "", PrefetchAuto:
		attrs["data-nextgo-prefetch"] = "viewport"
	case PrefetchIntent:
		attrs["data-nextgo-prefetch"] = "intent"
	case PrefetchNone:
		attrs["data-nextgo-prefetch"] = "false"
	default:
		return nodeFunc(func(io.Writer) error {
			return fmt.Errorf("ui.Link: invalid prefetch mode %q", options.Prefetch)
		})
	}
	if options.Replace {
		attrs["data-nextgo-replace"] = "true"
	}
	return El("a", attrs, append([]Node(nil), children...)...)
}
