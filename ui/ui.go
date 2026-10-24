// Package ui builds escaped HTML on the server. It does not implement a virtual DOM.
package ui

import (
	"context"
	"encoding/json"
	"fmt"
	"html"
	"io"
	"net/url"
	"regexp"
	"sort"
	"strings"
)

// Node is a server-renderable value. Custom components are functions returning Nodes.
type Node interface{ Render(io.Writer) error }
type nodeFunc func(io.Writer) error

func (n nodeFunc) Render(w io.Writer) error { return n(w) }

type Attrs map[string]string

var name = regexp.MustCompile(`^[a-zA-Z][a-zA-Z0-9:_-]*$`)
var void = map[string]bool{"area": true, "base": true, "br": true, "col": true, "embed": true, "hr": true, "img": true, "input": true, "link": true, "meta": true, "param": true, "source": true, "track": true, "wbr": true}

func Text(s string) Node {
	return nodeFunc(func(w io.Writer) error { _, err := io.WriteString(w, html.EscapeString(s)); return err })
}
func Fragment(children ...Node) Node {
	return nodeFunc(func(w io.Writer) error {
		for _, c := range children {
			if c != nil {
				if err := c.Render(w); err != nil {
					return err
				}
			}
		}
		return nil
	})
}

// El renders ordinary elements. Script/style markup and inline event handlers are
// deliberately excluded: put CSS in public assets and browser behavior in Go.
func El(tag string, attrs Attrs, children ...Node) Node {
	tag = strings.ToLower(tag)
	return nodeFunc(func(w io.Writer) error {
		if !name.MatchString(tag) || tag == "script" || tag == "style" || tag == "iframe" || tag == "object" || tag == "embed" || tag == "base" || tag == "animate" || tag == "set" || tag == "animatemotion" || tag == "animatetransform" || tag == "discard" {
			return fmt.Errorf("ui: unsupported tag %q", tag)
		}
		var b strings.Builder
		b.WriteString("<" + tag)
		keys := make([]string, 0, len(attrs))
		for k := range attrs {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		for _, k := range keys {
			lower := strings.ToLower(k)
			if !name.MatchString(k) || strings.HasPrefix(lower, "on") || lower == "srcdoc" || lower == "style" || lower == "http-equiv" {
				return fmt.Errorf("ui: unsupported attribute %q", k)
			}
			v := attrs[k]
			if lower == "srcset" && !safeSrcset(v) {
				return fmt.Errorf("ui: unsafe srcset")
			}
			if lower == "href" || lower == "src" || lower == "action" || lower == "formaction" || lower == "xlink:href" || lower == "poster" {
				if !safeURL(v) {
					return fmt.Errorf("ui: unsafe URL in %s", k)
				}
			}
			b.WriteString(" " + k + `="` + html.EscapeString(v) + `"`)
		}
		b.WriteByte('>')
		if _, err := io.WriteString(w, b.String()); err != nil {
			return err
		}
		if void[tag] {
			if len(children) > 0 {
				return fmt.Errorf("ui: void element %s has children", tag)
			}
			return nil
		}
		if err := Fragment(children...).Render(w); err != nil {
			return err
		}
		_, err := io.WriteString(w, "</"+tag+">")
		return err
	})
}

// Responsive image candidates allow local or HTTP(S) URLs plus positive width
// descriptors; data URLs and embedded commas are intentionally unsupported.
func safeSrcsetURL(value string) bool {
	if !safeURL(value) {
		return false
	}
	if strings.HasPrefix(value, "/") {
		return !strings.HasPrefix(value, "//")
	}
	u, err := url.Parse(value)
	return err == nil && (u.Scheme == "http" || u.Scheme == "https") && u.Host != "" && u.User == nil
}

func safeSrcset(value string) bool {
	if strings.TrimSpace(value) == "" {
		return false
	}
	for _, part := range strings.Split(value, ",") {
		fields := strings.Fields(part)
		if len(fields) != 2 || !safeSrcsetURL(fields[0]) || !strings.HasSuffix(fields[1], "w") {
			return false
		}
		var width int
		if _, err := fmt.Sscanf(fields[1], "%dw", &width); err != nil || width <= 0 {
			return false
		}
		if fields[1] != fmt.Sprintf("%dw", width) {
			return false
		}
	}
	return true
}

func safeURL(s string) bool {
	// Browsers strip ASCII controls before interpreting URL schemes.
	s = strings.Map(func(r rune) rune {
		if r <= 32 || r == 127 {
			return -1
		}
		return r
	}, s)
	u, err := url.Parse(s)
	if err != nil {
		return false
	}
	switch strings.ToLower(u.Scheme) {
	case "", "http", "https", "mailto", "tel":
		return true
	}
	return false
}

// Island emits meaningful server HTML plus public JSON props. Hydration attaches
// behavior to this exact root; it never renders this subtree again.
func Island(component string, props any, body Node) Node {
	return nodeFunc(func(w io.Writer) error {
		data, err := json.Marshal(props)
		if err != nil {
			return fmt.Errorf("island %s: %w", component, err)
		}
		return El("div", Attrs{"data-nextgo-island": component, "data-nextgo-props": string(data)}, body).Render(w)
	})
}

func HTML(n Node) (string, error) { return HTMLContext(context.Background(), n) }

// RenderContext returns the cancellation/request context of a framework render.
// Custom Nodes can use it to propagate values to nested rendering work.
func RenderContext(w io.Writer) context.Context {
	if contextual, ok := w.(interface{ renderContext() context.Context }); ok {
		return contextual.renderContext()
	}
	return context.Background()
}

// RenderTo buffers no output itself. It renders through a context-aware writer;
// use RenderStream when deferred nodes should flush independently.
func RenderTo(ctx context.Context, w io.Writer, n Node) (err error) {
	if ctx == nil {
		return fmt.Errorf("ui: nil rendering context")
	}
	if w == nil {
		return fmt.Errorf("ui: nil rendering writer")
	}
	defer func() {
		if v := recover(); v != nil {
			err = fmt.Errorf("ui: render panic: %v", v)
		}
	}()
	if n == nil {
		return nil
	}
	return n.Render(&contextWriter{ctx: ctx, w: w})
}

// HTMLContext renders buffered HTML while propagating cancellation, deadlines
// and request values to deferred loaders throughout framework-owned nodes.
func HTMLContext(ctx context.Context, n Node) (string, error) {
	var b strings.Builder
	err := RenderTo(ctx, &b, n)
	return b.String(), err
}
