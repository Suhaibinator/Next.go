package ui

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"strings"
	"testing"
	"unicode/utf8"
)

func jsonHTMLText(t *testing.T, raw string) string {
	t.Helper()
	encoded, err := json.Marshal(raw)
	if err != nil {
		t.Fatal(err)
	}
	var text string
	if err := json.Unmarshal(encoded, &text); err != nil {
		t.Fatal(err)
	}
	return text
}

func TestHTMLUTF8MatchesJSONAcrossEveryWriteSplit(t *testing.T) {
	for _, raw := range []string{
		"ordinary <&\" text", "é 日本 💩 \uFFFD", "NUL\x00CR\r\n",
		"\xff", "\xff\xfe", "\xc0\x80", "\xe2\x82", "\xed\xa0\x80",
		"\xf0\x9f\x92", "\xf4\x90\x80\x80", "a\xe2\x82z\xffb",
	} {
		raw = "<main>" + raw + "</main>"
		want := jsonHTMLText(t, raw)
		for cut := 0; cut <= len(raw); cut++ {
			node := nodeFunc(func(w io.Writer) error {
				if n, err := w.Write([]byte(raw[:cut])); err != nil || n != cut {
					t.Fatalf("first write: %d %v", n, err)
				}
				n, err := io.WriteString(w, raw[cut:])
				if n != len(raw)-cut {
					t.Fatal("second input-byte count", n)
				}
				return err
			})
			got, err := HTML(node)
			if err != nil || got != want || !utf8.ValidString(got) {
				t.Fatalf("raw %q split %d: %q want %q: %v", raw, cut, got, want, err)
			}
		}
	}
	got, err := HTML(El("p", Attrs{"data-value": "\xff\xfe<&"}, Text("\xff\xfe<&")))
	if err != nil || got != `<p data-value="��&lt;&amp;">��&lt;&amp;</p>` {
		t.Fatal("text/attribute escaping changed", got, err)
	}
}

func TestHTMLUTF8CarriesOwnedPrefixWithoutWholeRenderBuffer(t *testing.T) {
	var output bytes.Buffer
	err := RenderTo(context.Background(), &output, nodeFunc(func(w io.Writer) error {
		if _, err := io.WriteString(w, "visible"); err != nil {
			return err
		}
		if output.String() != "visible" {
			t.Fatal("RenderTo buffered complete output")
		}
		prefix := []byte{0xf0, 0x9f, 0x92}
		if n, err := w.Write(prefix); err != nil || n != 3 {
			t.Fatal(n, err)
		}
		prefix[0] = 0xff // Buffered input must not alias the caller's slice.
		if output.String() != "visible" {
			t.Fatal("incomplete rune escaped before completion")
		}
		// Nested framework rendering must share the outer unit's UTF-8 carry.
		return RenderTo(RenderContext(w), w, nodeFunc(func(w io.Writer) error {
			_, err := io.WriteString(w, "\xa9")
			return err
		}))
	}))
	if err != nil || output.String() != "visible💩" {
		t.Fatal(output.String(), err)
	}
	var tail bytes.Buffer
	writer := &htmlUTF8Writer{ctx: context.Background(), dst: &tail}
	if n, err := writer.WriteString("\xf0\x9f\x92"); n != 3 || err != nil || writer.count != 3 || len(writer.pending) != 3 {
		t.Fatal("carry must be at most three owned bytes", n, err, writer.count)
	}
	if err := writer.finish(); err != nil || tail.String() != "���" {
		t.Fatal("incomplete tail must match JSON's per-byte rule", tail.String(), err)
	}
}

type utf8FaultWriter struct {
	bytes.Buffer
	n     int
	err   error
	calls int
}

func (w *utf8FaultWriter) Write(p []byte) (int, error) {
	w.calls++
	if w.n >= 0 && w.n <= len(p) {
		_, _ = w.Buffer.Write(p[:w.n])
	}
	return w.n, w.err
}

// Do not expose Buffer.WriteString: this deliberately tests the bounded generic
// io.Writer fallback rather than a destination's own StringWriter fast path.
type utf8OnlyWriter struct{ fault *utf8FaultWriter }

func (w utf8OnlyWriter) Write(p []byte) (int, error) { return w.fault.Write(p) }

func TestHTMLUTF8WriterErrorsAndShortInputCounts(t *testing.T) {
	failure := errors.New("owned sink failed")
	for _, tc := range []struct {
		name, input string
		n, accepted int
		err, want   error
	}{
		{"short-valid", "abc", 2, 2, nil, io.ErrShortWrite},
		{"full-with-error", "abc", 3, 3, failure, failure},
		{"short-replacement", "\xff", 1, 0, nil, io.ErrShortWrite},
		{"negative-count", "a", -1, 0, nil, io.ErrShortWrite},
		{"oversized-count", "a", 2, 0, nil, io.ErrShortWrite},
	} {
		t.Run(tc.name, func(t *testing.T) {
			fault := &utf8FaultWriter{n: tc.n, err: tc.err}
			err := RenderTo(context.Background(), utf8OnlyWriter{fault}, nodeFunc(func(w io.Writer) error {
				n, err := io.WriteString(w, tc.input)
				if n != tc.accepted || !errors.Is(err, tc.want) {
					t.Fatal("input-byte count/error", n, err)
				}
				if n, err := w.Write([]byte("later")); n != 0 || !errors.Is(err, tc.want) {
					t.Fatal("failed writer was replayed", n, err)
				}
				return nil // A Node cannot turn a failed normalized unit into success.
			}))
			if !errors.Is(err, tc.want) || fault.calls != 1 {
				t.Fatal(err, fault.calls)
			}
		})
	}
	// A split valid rune's earlier accepted bytes are not counted twice if its
	// completion hits a partial destination write.
	fault := &utf8FaultWriter{n: 3}
	w := &htmlUTF8Writer{ctx: context.Background(), dst: utf8OnlyWriter{fault}}
	if n, err := w.Write([]byte{0xf0, 0x9f}); n != 2 || err != nil {
		t.Fatal(n, err)
	}
	if n, err := w.Write([]byte{0x92, 0xa9}); n != 1 || !errors.Is(err, io.ErrShortWrite) {
		t.Fatal("split completion input-byte count", n, err)
	}
}

func TestHTMLUTF8ContextAndFailedUnitDoNotFlushPending(t *testing.T) {
	ctx, cancel := context.WithCancel(context.WithValue(context.Background(), renderValueKey{}, "kept"))
	var output bytes.Buffer
	err := RenderTo(ctx, &output, nodeFunc(func(w io.Writer) error {
		if RenderContext(w).Value(renderValueKey{}) != "kept" {
			t.Fatal("writer lost render context")
		}
		if _, err := w.Write([]byte{0xe2, 0x82}); err != nil {
			return err
		}
		cancel()
		return nil
	}))
	if !errors.Is(err, context.Canceled) || output.Len() != 0 {
		t.Fatal("canceled finalization emitted output", output.String(), err)
	}
	failure := errors.New("application failed")
	err = RenderTo(context.Background(), &output, nodeFunc(func(w io.Writer) error {
		_, _ = w.Write([]byte{0xe2})
		return failure
	}))
	if !errors.Is(err, failure) || output.Len() != 0 {
		t.Fatal("failed unit finalized its tail", output.String(), err)
	}
}

func TestHTMLUTF8StreamAndFallbackQuotasChargeCanonicalBytes(t *testing.T) {
	oversized := nodeFunc(func(w io.Writer) error {
		_, err := io.WriteString(w, strings.Repeat("a", MaxStreamChunkBytes-2)+"\xff")
		return err
	})
	emitted := 0
	if err := RenderEvents(context.Background(), oversized, func(StreamEvent) error { emitted++; return nil }); !errors.Is(err, ErrStreamLimit) || emitted != 0 {
		t.Fatal("expanded shell bypassed quota or was published", err, emitted)
	}
	if _, err := CaptureShell(context.Background(), oversized); !errors.Is(err, ErrStreamLimit) {
		t.Fatal("expanded captured HTML bypassed quota", err)
	}
	fallback := nodeFunc(func(w io.Writer) error {
		_, err := io.WriteString(w, strings.Repeat("a", (64<<10)-2)+"\xff")
		return err
	})
	if _, err := HTML(ClientIsland("example", nil, fallback)); err == nil || !strings.Contains(err.Error(), "64 KiB") {
		t.Fatal("expanded client fallback bypassed quota", err)
	}
	// An exact-bound canonical tail must still be accepted.
	var events []StreamEvent
	node := nodeFunc(func(w io.Writer) error {
		_, err := io.WriteString(w, strings.Repeat("a", MaxStreamChunkBytes-3)+"\xff")
		return err
	})
	if err := RenderEvents(context.Background(), node, func(e StreamEvent) error { events = append(events, e); return nil }); err != nil || len(events) != 1 || len(events[0].HTML) != MaxStreamChunkBytes {
		t.Fatal("canonical exact quota rejected", err)
	}
}

func TestHTMLUTF8CapturedRegionOffsetsAndStreamFrames(t *testing.T) {
	node := nodeFunc(func(w io.Writer) error {
		if _, err := io.WriteString(w, "before\xe2"); err != nil {
			return err
		}
		if err := RequestRegion("account", "profile", nil, Text("fallback\xff")).Render(w); err != nil {
			return err
		}
		_, err := io.WriteString(w, "after")
		return err
	})
	shell, err := CaptureShell(context.Background(), node)
	if err != nil || ValidateShell(shell) != nil || len(shell.Regions) != 1 || shell.Regions[0].Start != len("before�") {
		t.Fatal("capture offsets did not follow canonical bytes", shell, err)
	}
	if got := shell.HTML[shell.Regions[0].Start:shell.Regions[0].End]; got != regionHeader("account")+"fallback�</div>" {
		t.Fatal(got)
	}
	restored, err := RestoreShell(shell)
	if err != nil {
		t.Fatal(err)
	}
	ctx := WithRegionResolver(context.Background(), func(context.Context, string, json.RawMessage) (Node, error) { return Text("loaded\xff\xfe"), nil })
	var events []StreamEvent
	if err := RenderEvents(ctx, restored, func(e StreamEvent) error { events = append(events, e); return nil }); err != nil || len(events) != 2 || string(events[0].HTML) != shell.HTML || string(events[1].HTML) != "loaded��" {
		t.Fatal(events, err)
	}
	for _, event := range events {
		if !utf8.Valid(event.HTML) || jsonHTMLText(t, string(event.HTML)) != string(event.HTML) {
			t.Fatal("semantic frame changed through JSON", event)
		}
	}
	var output bytes.Buffer
	if err := RenderStream(ctx, &output, restored); err != nil || !strings.Contains(output.String(), shell.HTML) || !strings.Contains(output.String(), `data-nextgo-chunk="account">loaded��</template>`) || !utf8.Valid(output.Bytes()) {
		t.Fatal(output.String(), err)
	}
}
