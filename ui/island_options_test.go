package ui

import (
	"context"
	"encoding/json"
	"io"
	"strings"
	"testing"

	"github.com/Suhaibinator/Next.go/internal/navigation"
	"github.com/Suhaibinator/Next.go/internal/renderowner"
)

type countedProps struct{ calls *int }

func (p countedProps) MarshalJSON() ([]byte, error) {
	*p.calls++
	return []byte(`{"price":"€ < & \u0022"}`), nil
}

type countedFallback struct{ calls *int }

func (n countedFallback) Render(w io.Writer) error {
	*n.calls++
	_, err := io.WriteString(w, "<p>SSR € &lt;price&gt;</p>")
	return err
}
func renderOptions(t *testing.T, node Node) (*navigation.MergeDescriptor, string, error) {
	t.Helper()
	rec := renderowner.New()
	ctx := renderowner.With(rec.Context(context.Background()), renderowner.Semantic("page", "/a"))
	body, err := HTMLContext(ctx, node)
	if err != nil {
		return nil, body, err
	}
	descriptor, err := rec.Finish("public", body)
	return descriptor, body, err
}
func TestClientIslandOptionsSingleRenderAndLegacyBytes(t *testing.T) {
	p, f := 0, 0
	node := ClientIslandWithOptions("panel", countedProps{&p}, countedFallback{&f}, IslandOptions{Key: "editor", Schema: "v1"})
	d, body, err := renderOptions(t, node)
	if err != nil || d == nil || p != 1 || f != 1 || d.Validate(body) != nil {
		t.Fatal(d, err, p, f)
	}
	raw := body[d.Islands[0].BodyStart:d.Islands[0].BodyEnd]
	if raw != "<p>SSR € &lt;price&gt;</p>" {
		t.Fatal(raw)
	}
	legacy, _ := HTML(ClientIsland("panel", map[string]string{"x": "<€"}, Text("fallback")))
	without, _ := HTML(ClientIslandWithOptions("panel", map[string]string{"x": "<€"}, Text("fallback"), IslandOptions{Key: "k", Schema: "v1"}))
	if legacy != without {
		t.Fatal("absent recorder changed legacy output")
	}
}
func TestClientIslandDeclaredAncestryDuplicatesAndInvalidOptions(t *testing.T) {
	makeNode := func(key string) Node {
		return ClientIslandWithOptions("panel", nil, Text("SSR"), IslandOptions{Key: key, Schema: "v1"})
	}
	if _, _, err := renderOptions(t, Fragment(makeNode("same"), makeNode("same"))); err == nil {
		t.Fatal("duplicate sibling keys")
	}
	d, body, err := renderOptions(t, Fragment(Keyed("article", "a", nil, makeNode("same")), Keyed("article", "b", nil, makeNode("same"))))
	if err != nil || d == nil || len(d.Islands) != 2 || d.Validate(body) != nil {
		t.Fatal(d, err)
	}
	one, _ := d.Address(d.Islands[0])
	two, _ := d.Address(d.Islands[1])
	if one == two {
		t.Fatal("keyed parents collapsed")
	}
	for _, opts := range []IslandOptions{{}, {Key: "k"}, {Key: "k", Schema: "bad schema"}, {Key: strings.Repeat("x", 257), Schema: "v1"}} {
		if _, err := HTML(ClientIslandWithOptions("panel", nil, Text("SSR"), opts)); err == nil {
			t.Fatal(opts)
		}
	}
	encoded, _ := json.Marshal(d)
	if _, err := navigation.ParseMerge(string(encoded), body); err != nil {
		t.Fatal(err)
	}
}

type duplicatedOutput struct{ node Node }

func (n duplicatedOutput) Render(w io.Writer) error {
	var saved strings.Builder
	if err := n.node.Render(&saved); err != nil {
		return err
	}
	_, err := io.WriteString(w, saved.String()+saved.String())
	return err
}
func TestClientIslandBufferedCustomOutputRequiresUniqueExactOccurrence(t *testing.T) {
	// Forward the render context when deliberately buffering. A truly opaque
	// custom writer loses recorder authority and cannot invent a merge record.
	node := nodeFunc(func(w io.Writer) error {
		var saved strings.Builder
		if err := RenderTo(RenderContext(w), &saved, ClientIslandWithOptions("panel", nil, Text("SSR"), IslandOptions{Key: "k", Schema: "v1"})); err != nil {
			return err
		}
		_, err := io.WriteString(w, saved.String()+saved.String())
		return err
	})
	if _, _, err := renderOptions(t, node); err == nil {
		t.Fatal("duplicated captured markup was accepted")
	}
}

func TestClientIslandOversizedValidPropsKeepOrdinaryFreshRendering(t *testing.T) {
	node := ClientIslandWithOptions("panel", map[string]string{"payload": strings.Repeat("a", navigation.MaxMergeProps)}, Text("meaningful fallback"), IslandOptions{Key: "k", Schema: "v1"})
	d, body, err := renderOptions(t, node)
	if err != nil || d != nil || !strings.Contains(body, "meaningful fallback") {
		t.Fatal("oversize lost cold rendering", d, err)
	}
}

func TestClientIslandCaptureUsesCanonicalRenderUnitBytes(t *testing.T) {
	// Requires the newer canonical renderer foundation selected by root. This
	// custom Node writes one split valid rune and two malformed bytes once.
	calls := 0
	fallback := nodeFunc(func(w io.Writer) error {
		calls++
		for _, part := range [][]byte{{0xe2}, {0x82, 0xac, 0xff}, {0xe2}} {
			if _, err := w.Write(part); err != nil {
				return err
			}
		}
		return nil
	})
	node := ClientIslandWithOptions("panel", nil, fallback, IslandOptions{Key: "k", Schema: "v1"})
	d, body, err := renderOptions(t, node)
	if err != nil || d == nil || calls != 1 || d.Validate(body) != nil {
		t.Fatal(d, err, calls)
	}
	got := body[d.Islands[0].BodyStart:d.Islands[0].BodyEnd]
	if got != "€\ufffd\ufffd" {
		t.Fatalf("noncanonical captured fallback %q", got)
	}
}

func TestClientIslandFallbackHidesRecorderButPreservesExistingMarkerPolicy(t *testing.T) {
	propsCalls, renderCalls := 0, 0
	fallback := nodeFunc(func(w io.Writer) error {
		renderCalls++
		ctx := RenderContext(w)
		if renderowner.RecorderFrom(ctx) != nil || ctx.Value(clientFallbackKey{}) != true {
			t.Fatal("fallback inherited recorder or lost its existing validation context")
		}
		if _, ok := ctx.Value(clientFallbackElementsKey{}).(*int); !ok {
			t.Fatal("fallback element budget context lost")
		}
		return El("p", nil, Text("Reusable fallback")).Render(w)
	})
	makeNode := func(key string) Node {
		return ClientIslandWithOptions("panel", countedProps{&propsCalls}, fallback, IslandOptions{Key: key, Schema: "v1"})
	}
	d, body, err := renderOptions(t, Fragment(makeNode("A"), makeNode("B")))
	if err != nil || d == nil || d.Validate(body) != nil || len(d.Islands) != 2 || len(d.Owners) != 1 || propsCalls != 2 || renderCalls != 2 {
		t.Fatal(d, err, propsCalls, renderCalls)
	}
	// Literal ui.Keyed fallbacks are not a valid old-policy reproducer: their
	// framework marker is rejected before Finish. Do not silently widen that
	// policy while isolating recorder ancestry for permitted fallback Nodes.
	_, _, err = renderOptions(t, ClientIslandWithOptions("panel", nil, Keyed("p", "row", nil, Text("keyed fallback")), IslandOptions{Key: "A", Schema: "v1"}))
	if err == nil || !strings.Contains(err.Error(), "framework markers") {
		t.Fatal("fallback marker policy widened", err)
	}
}
