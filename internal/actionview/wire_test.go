package actionview

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"testing"
)

func encoded(t *testing.T, frame Frame) []byte {
	t.Helper()
	data, err := json.Marshal(frame)
	if err != nil {
		t.Fatal(err)
	}
	return append(data, '\n')
}
func resultFrame() Frame {
	return Frame{Version: Version, Type: "result", Result: json.RawMessage(`{"data":{"count":2},"redirect":"/done"}`)}
}
func shellFrame() Frame {
	return Frame{Version: Version, Type: "shell", Page: json.RawMessage(`{"version":3,"html":"<main>First HTML</main>"}`)}
}
func doneFrame() Frame { return Frame{Version: Version, Type: "done"} }
func failureFrame() Frame {
	return Frame{Version: Version, Type: "view-error", Error: &Failure{Code: "view_unavailable", Message: "The action was confirmed; the current view could not be loaded.", Status: 503}}
}

func TestDecoderFragmentation(t *testing.T) {
	frames := []Frame{resultFrame(), shellFrame(), {Version: Version, Type: "chunk", ID: "deferred", HTML: "<h2>Ready λ 🐹</h2>"}, doneFrame()}
	var stream []byte
	for _, frame := range frames {
		stream = append(stream, encoded(t, frame)...)
	}
	for step := 1; step <= len(stream); step++ {
		var decoder Decoder
		var got []Frame
		for start := 0; start < len(stream); start += step {
			end := start + step
			if end > len(stream) {
				end = len(stream)
			}
			if err := decoder.Feed(stream[start:end], func(frame Frame) error { got = append(got, frame); return nil }); err != nil {
				t.Fatalf("fragment%d: %v", step, err)
			}
		}
		if err := decoder.Finish(); err != nil {
			t.Fatal(err)
		}
		if len(got) != 4 || got[0].Type != "result" || got[1].Type != "shell" || got[2].HTML != frames[2].HTML || got[3].Type != "done" {
			t.Fatalf("fragment%d: %#v", step, got)
		}
	}
}

func TestDecodeFrameRejectsAmbiguousOrInvalidWire(t *testing.T) {
	invalid := map[string]string{
		"unknown-field":           `{"version":1,"type":"done","extra":true}`,
		"case-field":              `{"Version":1,"type":"done"}`,
		"duplicate-field":         `{"version":1,"version":1,"type":"done"}`,
		"duplicate-nested-result": `{"version":1,"type":"result","result":{"data":{"count":1,"count":2}}}`,
		"duplicate-nested-page":   `{"version":1,"type":"shell","page":{"meta":{"title":"one","title":"two"}}}`,
		"bad-version":             `{"version":2,"type":"done"}`,
		"missing-version":         `{"type":"done"}`,
		"null-version":            `{"version":null,"type":"done"}`,
		"null-type":               `{"version":1,"type":null}`,
		"null-result":             `{"version":1,"type":"result","result":null}`,
		"missing-result":          `{"version":1,"type":"result"}`,
		"array-result":            `{"version":1,"type":"result","result":[]}`,
		"unknown-result":          `{"version":1,"type":"result","result":{"error":null}}`,
		"unsafe-redirect":         `{"version":1,"type":"result","result":{"data":1,"redirect":"//evil.test"}}`,
		"null-redirect":           `{"version":1,"type":"result","result":{"data":1,"redirect":null}}`,
		"null-page":               `{"version":1,"type":"shell","page":null}`,
		"array-page":              `{"version":1,"type":"shell","page":[]}`,
		"primitive-page":          `{"version":1,"type":"shell","page":1}`,
		"shell-extra":             `{"version":1,"type":"shell","page":{},"id":""}`,
		"missing-boundary":        `{"version":1,"type":"chunk","html":""}`,
		"unsafe-boundary":         `{"version":1,"type":"chunk","id":"node[bad]"}`,
		"null-chunk-html":         `{"version":1,"type":"chunk","id":"node","html":null}`,
		"null-failed":             `{"version":1,"type":"chunk","id":"node","failed":null}`,
		"chunk-extra":             `{"version":1,"type":"chunk","id":"node","page":{}}`,
		"done-extra-zero":         `{"version":1,"type":"done","failed":false}`,
		"null-error":              `{"version":1,"type":"view-error","error":null}`,
		"missing-error":           `{"version":1,"type":"view-error"}`,
		"error-status":            `{"version":1,"type":"view-error","error":{"code":"view_error","message":"Unavailable","status":200}}`,
		"error-unknown":           `{"version":1,"type":"view-error","error":{"code":"view_error","message":"Unavailable","status":503,"detail":"secret"}}`,
		"error-null-code":         `{"version":1,"type":"view-error","error":{"code":null,"message":"Unavailable","status":503}}`,
		"error-missing-message":   `{"version":1,"type":"view-error","error":{"code":"view_error","status":503}}`,
		"unknown-type":            `{"version":1,"type":"other"}`,
		"not-object":              `[]`, "null": `null`, "extra-json": `{"version":1,"type":"done"} {}`,
	}
	for name, data := range invalid {
		t.Run(name, func(t *testing.T) {
			if _, err := DecodeFrame([]byte(data)); err == nil {
				t.Fatal("invalid frame accepted")
			}
		})
	}
	bad := append([]byte(`{"version":1,"type":"chunk","id":"node","html":"`), 255)
	bad = append(bad, []byte(`"}`)...)
	if _, err := DecodeFrame(bad); err == nil {
		t.Fatal("invalid UTF-8 accepted")
	}
	for _, data := range []string{`{"version":1,"type":"result","result":{"data":null}}`, `{"version":1,"type":"result","result":{}}`, `{"version":1,"type":"chunk","id":"node"}`, `{"version":1,"type":"chunk","id":"node","failed":false}`} {
		if _, err := DecodeFrame([]byte(data)); err != nil {
			t.Fatal("valid nullable/empty values rejected", err)
		}
	}
}

func TestDecoderOrderingTerminalErrorsAndPoison(t *testing.T) {
	result, shell, done := encoded(t, resultFrame()), encoded(t, shellFrame()), encoded(t, doneFrame())
	chunk := encoded(t, Frame{Version: Version, Type: "chunk", ID: "boundary"})
	viewError := encoded(t, failureFrame())
	for name, stream := range map[string][]byte{
		"shell-first": shell, "chunk-first": chunk, "done-first": done, "view-error-first": viewError,
		"duplicate-result":   append(append([]byte(nil), result...), result...),
		"duplicate-shell":    bytes.Join([][]byte{result, shell, shell}, nil),
		"chunk-before-shell": bytes.Join([][]byte{result, chunk}, nil),
		"done-before-shell":  bytes.Join([][]byte{result, done}, nil),
		"duplicate-chunk":    bytes.Join([][]byte{result, shell, chunk, chunk}, nil),
		"after-done":         bytes.Join([][]byte{result, shell, done, result}, nil),
		"after-view-error":   bytes.Join([][]byte{result, viewError, shell}, nil),
	} {
		t.Run(name, func(t *testing.T) {
			var decoder Decoder
			if err := decoder.Feed(stream, nil); err == nil {
				t.Fatal("invalid sequence accepted")
			}
			if err := decoder.Feed(bytes.Join([][]byte{result, shell, done}, nil), nil); err == nil {
				t.Fatal("rejected decoder resumed")
			}
			if err := decoder.Finish(); err == nil {
				t.Fatal("rejected decoder finished")
			}
		})
	}
	for _, stream := range [][]byte{bytes.Join([][]byte{result, viewError}, nil), bytes.Join([][]byte{result, shell, chunk, viewError}, nil)} {
		var decoder Decoder
		if err := decoder.Feed(stream, nil); err != nil {
			t.Fatal(err)
		}
		if err := decoder.Finish(); err != nil {
			t.Fatal(err)
		}
	}
	for _, stream := range [][]byte{nil, result, bytes.Join([][]byte{result, shell}, nil), bytes.TrimSuffix(bytes.Join([][]byte{result, shell, done}, nil), []byte{'\n'})} {
		var decoder Decoder
		if err := decoder.Feed(stream, nil); err != nil {
			t.Fatal(err)
		}
		if err := decoder.Finish(); err == nil {
			t.Fatal("truncated stream accepted")
		}
		if err := decoder.Feed([]byte{'\n'}, nil); err == nil {
			t.Fatal("EOF rejection resumed")
		}
	}
	var decoder Decoder
	stopped := errors.New("consumer stopped")
	if err := decoder.Feed(result, func(Frame) error { return stopped }); !errors.Is(err, stopped) {
		t.Fatal(err)
	}
	if err := decoder.Finish(); !errors.Is(err, stopped) {
		t.Fatal("consumer failure not retained", err)
	}
}

func TestWireLimits(t *testing.T) {
	// Raw result bound is independent of the larger enclosing frame allowance.
	validResult := json.RawMessage(`{"data":"` + strings.Repeat("a", MaxResult-11) + `"}`)
	if len(validResult) != MaxResult {
		t.Fatal(len(validResult))
	}
	data := encoded(t, Frame{Version: Version, Type: "result", Result: validResult})
	if _, err := DecodeFrame(bytes.TrimSuffix(data, []byte{'\n'})); err != nil {
		t.Fatal(err)
	}
	tooLarge := json.RawMessage(`{"data":"` + strings.Repeat("a", MaxResult-10) + `"}`)
	if _, err := DecodeFrame(bytes.TrimSuffix(encoded(t, Frame{Version: Version, Type: "result", Result: tooLarge}), []byte{'\n'})); err == nil {
		t.Fatal("oversized result accepted")
	}
	if _, err := DecodeFrame(bytes.Repeat([]byte{' '}, MaxFrame+1)); err == nil {
		t.Fatal("oversized frame accepted")
	}
	var pending Decoder
	if err := pending.Feed(bytes.Repeat([]byte{' '}, MaxFrame), nil); err != nil {
		t.Fatal(err)
	}
	if err := pending.Feed([]byte{' '}, nil); err == nil {
		t.Fatal("fragmented frame exceeded limit")
	}
	var total Decoder
	if err := total.Feed(bytes.Repeat([]byte{' '}, MaxBytes+1), nil); err == nil {
		t.Fatal("oversized stream accepted")
	}
	for _, count := range []int{128, 129} {
		var stream []byte
		stream = append(stream, encoded(t, resultFrame())...)
		stream = append(stream, encoded(t, shellFrame())...)
		for i := 0; i < count; i++ {
			stream = append(stream, encoded(t, Frame{Version: Version, Type: "chunk", ID: fmt.Sprintf("boundary_%d", i)})...)
		}
		stream = append(stream, encoded(t, doneFrame())...)
		var decoder Decoder
		err := decoder.Feed(stream, nil)
		if count == 128 {
			if err != nil {
				t.Fatal(err)
			}
			if err := decoder.Finish(); err != nil {
				t.Fatal(err)
			}
		} else if err == nil {
			t.Fatal("excess boundaries accepted")
		}
	}
	longRedirect := `{"data":1,"redirect":"/` + strings.Repeat("a", MaxURL) + `"}`
	if _, err := DecodeFrame(bytes.TrimSuffix(encoded(t, Frame{Version: Version, Type: "result", Result: json.RawMessage(longRedirect)}), []byte{'\n'})); err == nil {
		t.Fatal("overlong redirect accepted")
	}
}
