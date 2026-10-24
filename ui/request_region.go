package ui

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"html"
	"io"
	"strings"
	"unicode/utf8"

	"github.com/Suhaibinator/Next.go/internal/wirejson"
)

const (
	MaxShellBytes            = MaxStreamChunkBytes
	MaxShellRegions          = MaxStreamBoundaries
	MaxRegionPropsBytes      = 64 << 10
	MaxShellRegionPropsBytes = 1 << 20
)

// RegionSpec describes one complete fallback wrapper in captured HTML. Offsets
// are ordered UTF-8 byte offsets, not DOM selectors or application closures.
type RegionSpec struct {
	ID     string          `json:"id"`
	Region string          `json:"region"`
	Props  json.RawMessage `json:"props"`
	Start  int             `json:"start"`
	End    int             `json:"end"`
}

// CapturedShell is trusted compiled HTML, not an upload or HTML sanitizer format.
// Its renderer validates framing and copies retained data before adopting it.
type CapturedShell struct {
	HTML    string       `json:"html"`
	Regions []RegionSpec `json:"regions,omitempty"`
}

type RegionResolver func(context.Context, string, json.RawMessage) (Node, error)
type regionResolverKey struct{}
type shellCaptureKey struct{}
type requestRegionNode struct {
	id, region string
	props      any
	raw        json.RawMessage
	fallback   Node
}

// RequestRegion explicitly separates a public build-time fallback from a named
// request-time server region. Region registrations remain in the server binary;
// only ID, registration name and public static props enter captured artifacts.
func RequestRegion(id, region string, props any, fallback Node) Node {
	return &requestRegionNode{id: id, region: region, props: props, fallback: fallback}
}

// WithRegionResolver binds current-request server implementations. Never use a
// build/request-independent resolver to store personalized region results.
func WithRegionResolver(ctx context.Context, resolver RegionResolver) context.Context {
	return context.WithValue(ctx, regionResolverKey{}, resolver)
}

func regionHeader(id string) string {
	return `<div aria-busy="true" data-nextgo-boundary="` + html.EscapeString(id) + `">`
}
func validRegionName(id string) bool { return len(id) <= 128 && boundaryID.MatchString(id) }
func validateRegionProps(raw json.RawMessage) error {
	if len(raw) == 0 || len(raw) > MaxRegionPropsBytes || !utf8.Valid(raw) || wirejson.Unique(raw) != nil {
		return fmt.Errorf("ui: invalid request-region props")
	}
	return nil
}
func (n *requestRegionNode) Render(w io.Writer) error {
	if !validRegionName(n.id) || !validRegionName(n.region) {
		return fmt.Errorf("ui: invalid request-region identity")
	}
	raw := n.raw
	if raw == nil {
		var err error
		raw, err = json.Marshal(n.props)
		if err != nil {
			return fmt.Errorf("ui: request-region props: %w", err)
		}
	}
	if err := validateRegionProps(raw); err != nil {
		return err
	}
	if capture, ok := w.(*shellCaptureWriter); ok {
		return capture.region(n.id, n.region, raw, n.fallback)
	}
	ctx := RenderContext(w)
	if ctx.Value(shellCaptureKey{}) == true {
		return fmt.Errorf("ui: request region cannot escape its shell capture writer")
	}
	resolver, _ := ctx.Value(regionResolverKey{}).(RegionResolver)
	if resolver == nil {
		return fmt.Errorf("ui: request region requires a request resolver")
	}
	return Defer(n.id, n.fallback, func(work context.Context) (Node, error) { return resolver(work, n.region, bytes.Clone(raw)) }).Render(w)
}

type shellCaptureWriter struct {
	contextWriter
	buffer     *streamBuffer
	specs      []RegionSpec
	known      map[string]bool
	propsBytes int
	fallback   bool
}

func (w *shellCaptureWriter) region(id, region string, props json.RawMessage, fallback Node) error {
	if w.fallback {
		return fmt.Errorf("ui: request-region fallback must be immediate")
	}
	if w.known[id] || len(w.specs) >= MaxShellRegions || len(props) > MaxShellRegionPropsBytes-w.propsBytes {
		return fmt.Errorf("ui: request-region shell exceeds identity or resource limits")
	}
	w.known[id] = true
	start := w.buffer.data.Len()
	if _, err := w.WriteString(regionHeader(id)); err != nil {
		return err
	}
	w.fallback = true
	if fallback != nil {
		if err := fallback.Render(w); err != nil {
			return err
		}
	}
	w.fallback = false
	if _, err := w.WriteString("</div>"); err != nil {
		return err
	}
	w.specs = append(w.specs, RegionSpec{strings.Clone(id), strings.Clone(region), bytes.Clone(props), start, w.buffer.data.Len()})
	w.propsBytes += len(props)
	return nil
}

// CaptureShell renders static application/layout work exactly once and records
// named request-region holes. Ordinary Defer callbacks and nested fallback holes
// are rejected rather than executed or serialized.
func CaptureShell(ctx context.Context, node Node) (shell CapturedShell, err error) {
	if ctx == nil {
		return CapturedShell{}, fmt.Errorf("ui: shell capture requires a context")
	}
	if err := ctx.Err(); err != nil {
		return CapturedShell{}, err
	}
	defer func() {
		if v := recover(); v != nil {
			shell = CapturedShell{}
			err = fmt.Errorf("ui: shell capture panic: %v", v)
		}
	}()
	buffer := &streamBuffer{}
	work := context.WithValue(ctx, shellCaptureKey{}, true)
	writer := &shellCaptureWriter{contextWriter: contextWriter{work, buffer}, buffer: buffer, known: map[string]bool{}}
	if node != nil {
		if err := node.Render(writer); err != nil {
			return CapturedShell{}, err
		}
	}
	if err := ctx.Err(); err != nil {
		return CapturedShell{}, err
	}
	shell = CapturedShell{HTML: buffer.data.String(), Regions: writer.specs}
	if err := ValidateShell(shell); err != nil {
		return CapturedShell{}, err
	}
	return shell, nil
}

// ValidateShell validates bounded, ordered framing. HTML must originate from a
// trusted build or authenticated shell cache; this does not sanitize arbitrary HTML.
func ValidateShell(shell CapturedShell) error {
	if len(shell.HTML) > MaxShellBytes || !utf8.ValidString(shell.HTML) || len(shell.Regions) > MaxShellRegions {
		return fmt.Errorf("ui: invalid captured shell size or text")
	}
	cursor, total := 0, 0
	known := map[string]bool{}
	for _, s := range shell.Regions {
		if !validRegionName(s.ID) || !validRegionName(s.Region) || known[s.ID] || s.Start < cursor || s.End < s.Start || s.End > len(shell.HTML) {
			return fmt.Errorf("ui: invalid captured region identity or offsets")
		}
		if err := validateRegionProps(s.Props); err != nil {
			return err
		}
		prefix := regionHeader(s.ID)
		if s.End-s.Start < len(prefix)+len("</div>") || !strings.HasPrefix(shell.HTML[s.Start:s.End], prefix) || !strings.HasSuffix(shell.HTML[s.Start:s.End], "</div>") {
			return fmt.Errorf("ui: invalid captured region framing")
		}
		total += len(s.Props)
		if total > MaxShellRegionPropsBytes {
			return fmt.Errorf("ui: captured region props exceed budget")
		}
		known[s.ID] = true
		cursor = s.End
	}
	return nil
}

type restoredShell struct{ shell CapturedShell }
type capturedHTML string

func (n capturedHTML) Render(w io.Writer) error { _, err := io.WriteString(w, string(n)); return err }

// RestoreShell adopts only trusted compiled data. Static HTML is copied directly;
// each region is rebound through WithRegionResolver at render time. Buffered
// HTML resolves regions in place; streaming emits frozen fallback then chunks.
func RestoreShell(shell CapturedShell) (Node, error) {
	if err := ValidateShell(shell); err != nil {
		return nil, err
	}
	owned := CapturedShell{HTML: strings.Clone(shell.HTML), Regions: make([]RegionSpec, len(shell.Regions))}
	for i, s := range shell.Regions {
		s.ID = strings.Clone(s.ID)
		s.Region = strings.Clone(s.Region)
		s.Props = bytes.Clone(s.Props)
		owned.Regions[i] = s
	}
	return restoredShell{owned}, nil
}
func (n restoredShell) Render(w io.Writer) error {
	cursor := 0
	for _, s := range n.shell.Regions {
		if _, err := io.WriteString(w, n.shell.HTML[cursor:s.Start]); err != nil {
			return err
		}
		fallbackStart := s.Start + len(regionHeader(s.ID))
		fallback := capturedHTML(n.shell.HTML[fallbackStart : s.End-len("</div>")])
		hole := &requestRegionNode{id: s.ID, region: s.Region, raw: s.Props, fallback: fallback}
		if err := hole.Render(w); err != nil {
			return err
		}
		cursor = s.End
	}
	_, err := io.WriteString(w, n.shell.HTML[cursor:])
	return err
}
