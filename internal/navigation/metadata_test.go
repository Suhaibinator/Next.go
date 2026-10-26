package navigation

import (
	"encoding/json"
	"strings"
	"testing"
)

const metadataTestOwner = "0123456789abcdef0123456789abcdef"

func metadataTestFrame(t *testing.T, failed bool) StreamFrame {
	t.Helper()
	p := MetadataPayload{Version: MetadataVersion, Owner: metadataTestOwner, Title: "日本語", Description: "safe <text>", Elements: []MetadataElement{{Tag: "link", Attrs: map[string]string{"rel": "canonical", "href": "https://site.test/page"}}}}
	raw, err := EncodeMetadataPayload(p, failed)
	if err != nil {
		t.Fatal(err)
	}
	return StreamFrame{Type: "metadata", Metadata: raw, Failed: failed}
}
func metadataTestShell() StreamFrame {
	return StreamFrame{Type: "shell", Page: json.RawMessage(`{"head":[]}`), MetadataPending: true, MetadataOwner: metadataTestOwner, MetadataIndices: []int{}}
}

func TestMetadataAcknowledgmentIsExplicitAndUnique(t *testing.T) {
	for _, tc := range []struct {
		values       []string
		ack, invalid bool
	}{{nil, false, false}, {[]string{"1"}, true, false}, {[]string{""}, false, true}, {[]string{"2"}, false, true}, {[]string{"1", "1"}, false, true}, {[]string{"1, 1"}, false, true}} {
		ack, err := MetadataAcknowledged(tc.values)
		if ack != tc.ack || (err != nil) != tc.invalid {
			t.Fatalf("%q: %v %v", tc.values, ack, err)
		}
	}
}
func TestMetadataPayloadRoundTripAndFailureHasNoReplacement(t *testing.T) {
	for _, failed := range []bool{false, true} {
		frame := metadataTestFrame(t, failed)
		p, err := DecodeMetadataPayload(frame.Metadata, failed)
		if err != nil {
			t.Fatal(err)
		}
		if p.Owner != metadataTestOwner || p.Version != 1 {
			t.Fatal(p)
		}
		if failed {
			if strings.Contains(string(frame.Metadata), "title") || strings.Contains(string(frame.Metadata), "elements") {
				t.Fatal("failure leaked replacement fields")
			}
		} else if p.Title != "日本語" || p.Description != "safe <text>" || len(p.Elements) != 1 {
			t.Fatal(p)
		}
	}
	raw, err := EncodeMetadataPayload(MetadataPayload{Version: 1, Owner: metadataTestOwner}, false)
	if err != nil || !strings.Contains(string(raw), `"elements":[]`) || !strings.Contains(string(raw), `"title":""`) {
		t.Fatal(string(raw), err)
	}
}
func TestMetadataPayloadStrictTypesAndOwnedDescriptors(t *testing.T) {
	base := string(metadataTestFrame(t, false).Metadata)
	for name, raw := range map[string]string{
		"duplicate":      strings.Replace(base, `"version":1`, `"version":1,"version":1`, 1),
		"unknown":        strings.Replace(base, `"version":1`, `"version":1,"extra":true`, 1),
		"case":           strings.Replace(base, `"title":`, `"Title":`, 1),
		"null-title":     strings.Replace(base, `"title":"日本語"`, `"title":null`, 1),
		"null-owner":     strings.Replace(base, `"owner":"`+metadataTestOwner+`"`, `"owner":null`, 1),
		"bad-owner":      strings.Replace(base, metadataTestOwner, strings.Repeat("A", 32), 1),
		"null-elements":  strings.Replace(base, `[{"tag":"link","attrs":{"href":"https://site.test/page","rel":"canonical"}}]`, `null`, 1),
		"element-case":   strings.Replace(base, `"tag":"link"`, `"Tag":"link"`, 1),
		"null-attribute": strings.Replace(base, `"rel":"canonical"`, `"rel":null`, 1),
		"script":         strings.Replace(base, `"tag":"link"`, `"tag":"script"`, 1),
		"stylesheet":     strings.Replace(base, `"rel":"canonical"`, `"rel":"stylesheet"`, 1),
		"preload":        strings.Replace(base, `"rel":"canonical"`, `"rel":"preload"`, 1),
		"event":          strings.Replace(base, `"rel":"canonical"`, `"rel":"canonical","onclick":"evil"`, 1),
		"unsafe-url":     strings.Replace(base, `https://site.test/page`, `javascript:alert(1)`, 1),
		"credentials":    strings.Replace(base, `https://site.test/page`, `https://user:password@site.test/`, 1),
		"failure-extra":  base,
	} {
		t.Run(name, func(t *testing.T) {
			if _, err := DecodeMetadataPayload([]byte(raw), name == "failure-extra"); err == nil {
				t.Fatalf("accepted %s", raw)
			}
		})
	}
	for _, e := range []MetadataElement{{Tag: "meta", Attrs: map[string]string{"name": "viewport", "content": "x"}}, {Tag: "meta", Attrs: map[string]string{"name": "description", "content": "x"}}, {Tag: "meta", Attrs: map[string]string{"name": "robots", "property": "og:title", "content": "x"}}} {
		if ValidateMetadataElements([]MetadataElement{e}) == nil {
			t.Fatal("accepted forbidden ownership", e)
		}
	}
}
func TestMetadataStreamEveryByteAndLegacyIsolation(t *testing.T) {
	for _, failed := range []bool{false, true} {
		frames := []StreamFrame{metadataTestShell(), {Type: "chunk", ID: "body", HTML: "<p>loaded</p>"}, metadataTestFrame(t, failed), {Type: "done"}}
		var encoded []byte
		for _, f := range frames {
			encoded = append(encoded, streamLine(t, f)...)
		}
		d := NewMetadataStreamDecoder()
		count := 0
		for _, b := range encoded {
			if err := d.Feed([]byte{b}, func(StreamFrame) error { count++; return nil }); err != nil {
				t.Fatal(err)
			}
		}
		if err := d.Finish(); err != nil || count != 4 {
			t.Fatal(err, count)
		}
		legacy := &StreamDecoder{}
		if legacy.Feed(encoded, func(StreamFrame) error { return nil }) == nil {
			t.Fatal("legacy accepted metadata extension")
		}
	}
	// An opted-in new client still accepts an ordinary old-server stream.
	d := NewMetadataStreamDecoder()
	input := append(streamLine(t, StreamFrame{Type: "shell", Page: json.RawMessage(`{}`)}), streamLine(t, StreamFrame{Type: "done"})...)
	if err := d.Feed(input, func(StreamFrame) error { return nil }); err != nil {
		t.Fatal(err)
	}
	if err := d.Finish(); err != nil {
		t.Fatal(err)
	}
}
func TestMetadataStreamOrderingAndExactFields(t *testing.T) {
	shell := string(streamLine(t, metadataTestShell()))
	terminal := string(streamLine(t, metadataTestFrame(t, false)))
	done := string(streamLine(t, StreamFrame{Type: "done"}))
	for name, input := range map[string]string{
		"before-shell":       terminal + shell + done,
		"missing-terminal":   shell + done,
		"duplicate-terminal": shell + terminal + terminal + done,
		"wrong-owner":        shell + strings.Replace(terminal, metadataTestOwner, strings.Repeat("f", 32), 1) + done,
		"unannounced":        string(streamLine(t, StreamFrame{Type: "shell", Page: json.RawMessage(`{}`)})) + terminal + done,
		"after-done":         shell + terminal + done + terminal,
		"null-indices":       strings.Replace(shell, `"metadataIndices":[]`, `"metadataIndices":null`, 1) + terminal + done,
		"null-index":         strings.Replace(shell, `"metadataIndices":[]`, `"metadataIndices":[null]`, 1) + terminal + done,
		"repeated-indices":   strings.Replace(shell, `"metadataIndices":[]`, `"metadataIndices":[1,1]`, 1) + terminal + done,
		"unordered-indices":  strings.Replace(shell, `"metadataIndices":[]`, `"metadataIndices":[1,0]`, 1) + terminal + done,
		"missing-owner":      strings.Replace(shell, `"metadataOwner":"`+metadataTestOwner+`",`, ``, 1) + terminal + done,
		"false-pending":      strings.Replace(shell, `"metadataPending":true`, `"metadataPending":false`, 1) + terminal + done,
		"payload-on-done":    shell + terminal + strings.TrimSuffix(done, "\n")[:len(strings.TrimSuffix(done, "\n"))-1] + `,"metadata":{}}` + "\n",
		"html-on-metadata":   shell + strings.TrimSuffix(terminal, "\n")[:len(strings.TrimSuffix(terminal, "\n"))-1] + `,"html":""}` + "\n" + done,
		"null-failed":        shell + strings.TrimSuffix(terminal, "\n")[:len(strings.TrimSuffix(terminal, "\n"))-1] + `,"failed":null}` + "\n" + done,
	} {
		t.Run(name, func(t *testing.T) {
			d := NewMetadataStreamDecoder()
			err := d.Feed([]byte(input), func(StreamFrame) error { return nil })
			if err == nil {
				err = d.Finish()
			}
			if err == nil {
				t.Fatal("accepted malformed sequence", input)
			}
		})
	}
}
func TestMetadataBoundsAndExactProvenance(t *testing.T) {
	for _, indices := range [][]int{{-1}, {1, 0}, {0, 0}, {2}} {
		if ValidateMetadataIndices(indices, 2) == nil {
			t.Fatal(indices)
		}
	}
	if err := ValidateMetadataIndices([]int{0, 2}, 3); err != nil {
		t.Fatal(err)
	}
	if err := ValidateMetadataIndices(nil, 0); err != nil {
		t.Fatal(err)
	}
	payload := MetadataPayload{Version: 1, Owner: metadataTestOwner, Title: strings.Repeat("x", MaxMetadataBytes)}
	if _, err := EncodeMetadataPayload(payload, false); err == nil {
		t.Fatal("accepted oversize metadata")
	}
	payload.Title = ""
	payload.Elements = make([]MetadataElement, MaxMetadataElements+1)
	if _, err := EncodeMetadataPayload(payload, false); err == nil {
		t.Fatal("accepted excess nodes")
	}
	var encoded []byte
	encoded = append(encoded, streamLine(t, metadataTestShell())...)
	for i := 0; i < 128; i++ {
		encoded = append(encoded, streamLine(t, StreamFrame{Type: "chunk", ID: "body", HTML: "x"})...)
	}
	encoded = append(encoded, streamLine(t, metadataTestFrame(t, false))...)
	encoded = append(encoded, streamLine(t, StreamFrame{Type: "done"})...)
	d := NewMetadataStreamDecoder()
	if err := d.Feed(encoded, func(StreamFrame) error { return nil }); err != nil {
		t.Fatal(err)
	}
	if err := d.Finish(); err != nil {
		t.Fatal(err)
	}
	encoded = append(streamLine(t, metadataTestShell()), encoded[len(streamLine(t, metadataTestShell())):]...)
	encoded = append(encoded[:len(encoded)-len(streamLine(t, StreamFrame{Type: "done"}))], append(streamLine(t, StreamFrame{Type: "chunk", ID: "extra", HTML: "x"}), streamLine(t, StreamFrame{Type: "done"})...)...)
	if NewMetadataStreamDecoder().Feed(encoded, func(StreamFrame) error { return nil }) == nil {
		t.Fatal("accepted excess frames")
	}
}

func TestMetadataShellEmptyIndicesAndLegacyMarshalSchema(t *testing.T) {
	shell := metadataTestShell()
	shell.MetadataIndices = nil
	raw, err := json.Marshal(shell)
	if err != nil || !strings.Contains(string(raw), `"metadataIndices":[]`) {
		t.Fatal(string(raw), err)
	}
	shell.Version = 1
	raw, err = json.Marshal(shell)
	if err != nil {
		t.Fatal(err)
	}
	parsed, err := DecodeMetadataStreamFrame(raw)
	if err != nil || !parsed.MetadataPending || parsed.MetadataIndices == nil || len(parsed.MetadataIndices) != 0 {
		t.Fatal(string(raw), parsed, err)
	}
	legacy := StreamFrame{Version: 1, Type: "shell", Page: json.RawMessage(`{}`)}
	raw, err = json.Marshal(legacy)
	if err != nil || string(raw) != `{"version":1,"type":"shell","page":{}}` {
		t.Fatal("legacy serialized schema changed", string(raw), err)
	}
	if _, err := DecodeStreamFrame(raw); err != nil {
		t.Fatal(err)
	}
}
