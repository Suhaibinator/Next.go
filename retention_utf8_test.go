package nextgo

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"html"
	"io"
	"net/http"
	"net/http/httptest"
	"regexp"
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/Suhaibinator/Next.go/internal/navigation"
	"github.com/Suhaibinator/Next.go/ui"
)

type canonicalUTF8Node func(io.Writer) error

func (node canonicalUTF8Node) Render(w io.Writer) error { return node(w) }

func canonicalJSONText(t *testing.T, text string) string {
	t.Helper()
	encoded, err := json.Marshal(text)
	if err != nil {
		t.Fatal(err)
	}
	var result string
	if err := json.Unmarshal(encoded, &result); err != nil {
		t.Fatal(err)
	}
	return result
}

func TestRetentionUTF8ExactSSRAndNavigationStamp(t *testing.T) {
	for _, tc := range []struct{ name, text string }{
		{"isolated", "\xff"}, {"run", "\xff\xfe"}, {"truncated", "\xe2\x82"},
		{"invalid-multibyte", "\xed\xa0\x80"}, {"valid-and-controls", "日本 💩 \uFFFD\x00\r\n"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			body := ui.El("main", ui.Attrs{"data-note": tc.text}, ui.Text(tc.text))
			want := canonicalJSONText(t, `<main data-note="`+html.EscapeString(tc.text)+`">`+html.EscapeString(tc.text)+`</main>`)
			assertCanonicalRetention(t, body, want)
		})
	}
	raw := "<main>split 💩 日本 \xff\xe2\x82</main>"
	custom := canonicalUTF8Node(func(w io.Writer) error {
		for i := range len(raw) {
			if _, err := w.Write([]byte{raw[i]}); err != nil {
				return err
			}
		}
		return nil
	})
	assertCanonicalRetention(t, custom, canonicalJSONText(t, raw))
}

func assertCanonicalRetention(t *testing.T, body ui.Node, want string) {
	t.Helper()
	page := Page{Body: body, Title: "Canonical UTF-8", Retention: &RouteRetention{Partition: "public"}}
	app := testApp(t, Config{RetainRoutes: true, Routes: []Route{{Pattern: "/text", Page: func(*Context) (Page, error) { return page, nil }}}})
	initial := httptest.NewRecorder()
	app.ServeHTTP(initial, httptest.NewRequest(http.MethodGet, "/text", nil))
	if initial.Code != http.StatusOK || !utf8.Valid(initial.Body.Bytes()) || !strings.Contains(initial.Body.String(), `<div id="nextgo-app" tabindex="-1">`+want+`</div>`) {
		t.Fatal("SSR did not publish the exact canonical body", initial.Code, initial.Body.String())
	}
	match := regexp.MustCompile(`data-nextgo-retention="([^"]+)"`).FindStringSubmatch(initial.Body.String())
	if len(match) != 2 {
		t.Fatal("normalization must not simply omit retention")
	}
	stamp, err := navigation.ParseRetentionDescriptor(html.UnescapeString(match[1]))
	if err != nil || stamp == nil {
		t.Fatal(stamp, err)
	}
	sum := sha256.Sum256([]byte(want))
	if stamp.Fingerprint != hex.EncodeToString(sum[:]) {
		t.Fatal("stamp hashed pre-normalization bytes", stamp)
	}
	response, envelope := runtimeEnvelope(t, app, "/text", nil)
	if response.Code != http.StatusOK || envelope.HTML != want || envelope.Retention == nil || *envelope.Retention != *stamp || envelope.Retention.Validate(envelope.HTML) != nil {
		t.Fatal("JSON changed the server-stamped body", response.Code, envelope)
	}
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(response.Body.Bytes(), &fields); err != nil {
		t.Fatal(err)
	}
	if _, err := navigation.ParseRetention(string(fields["retention"]), envelope.HTML); err != nil {
		t.Fatal("real JSON response fails the browser's exact retention validation", err)
	}
}

func TestCanonicalUTF8HTMLAndNavigationStreamSemantics(t *testing.T) {
	loaded := "<p>loaded \xff\xfe 💩</p>"
	page := Page{Title: "Stream UTF-8", Stream: true, Body: ui.El("main", nil,
		ui.Text("shell\xff"), ui.Defer("child", ui.Text("waiting\xe2"), func(context.Context) (ui.Node, error) {
			return canonicalUTF8Node(func(w io.Writer) error {
				for i := range len(loaded) {
					if _, err := io.WriteString(w, loaded[i:i+1]); err != nil {
						return err
					}
				}
				return nil
			}), nil
		}))}
	app := testApp(t, Config{Routes: []Route{{Pattern: "/stream", Page: func(*Context) (Page, error) { return page, nil }}}})
	wantShell := `<main>shell�<div aria-busy="true" data-nextgo-boundary="child">waiting�</div></main>`
	wantChunk := "<p>loaded �� 💩</p>"
	initial := httptest.NewRecorder()
	app.ServeHTTP(initial, httptest.NewRequest(http.MethodGet, "/stream", nil))
	if initial.Code != http.StatusOK || !utf8.Valid(initial.Body.Bytes()) || !strings.Contains(initial.Body.String(), wantShell) || !strings.Contains(initial.Body.String(), `<template data-nextgo-chunk="child">`+wantChunk+`</template>`) {
		t.Fatal("initial HTML stream changed its semantic units", initial.Code, initial.Body.String())
	}
	request := httptest.NewRequest(http.MethodGet, "/stream", nil)
	request.Header.Set("X-Nextgo-Navigate", "1")
	request.Header.Set(navigation.StreamHeader, "1")
	response := httptest.NewRecorder()
	app.ServeHTTP(response, request)
	if response.Code != http.StatusOK || !strings.HasPrefix(response.Header().Get("Content-Type"), navigation.StreamMediaType) {
		t.Fatal(response.Code, response.Header())
	}
	decoder := json.NewDecoder(bytes.NewReader(response.Body.Bytes()))
	var kinds []string
	for {
		var frame struct {
			Type string    `json:"type"`
			ID   string    `json:"id"`
			HTML string    `json:"html"`
			Page *Envelope `json:"page"`
		}
		if err := decoder.Decode(&frame); err == io.EOF {
			break
		} else if err != nil {
			t.Fatal(err)
		}
		kinds = append(kinds, frame.Type)
		switch frame.Type {
		case "shell":
			if frame.Page == nil || frame.Page.HTML != wantShell {
				t.Fatal("JSON shell differs from initial HTML stream", frame)
			}
		case "chunk":
			if frame.ID != "child" || frame.HTML != wantChunk {
				t.Fatal("JSON chunk differs from initial HTML stream", frame)
			}
		}
	}
	if strings.Join(kinds, ",") != "shell,chunk,done" {
		t.Fatal("stream framing changed", kinds)
	}
}
