package navigation

import (
	"encoding/json"
	"strings"
	"testing"
)

func streamLine(t *testing.T, f StreamFrame) []byte {
	t.Helper()
	f.Version = 1
	data, err := json.Marshal(f)
	if err != nil {
		t.Fatal(err)
	}
	return append(data, '\n')
}
func TestStreamDecoderEveryByteAndCompletion(t *testing.T) {
	var data []byte
	for _, frame := range []StreamFrame{{Type: "shell", Page: json.RawMessage(`{"title":"日本語"}`)}, {Type: "chunk", ID: "content", HTML: "<p>héllo\nworld</p>"}, {Type: "done"}} {
		data = append(data, streamLine(t, frame)...)
	}
	decoder := &StreamDecoder{}
	var kinds []string
	for _, value := range data {
		if err := decoder.Feed([]byte{value}, func(frame StreamFrame) error { kinds = append(kinds, frame.Type); return nil }); err != nil {
			t.Fatal(err)
		}
	}
	if err := decoder.Finish(); err != nil {
		t.Fatal(err)
	}
	if strings.Join(kinds, ",") != "shell,chunk,done" {
		t.Fatal(kinds)
	}
	if err := decoder.Feed([]byte(" \n"), func(StreamFrame) error { return nil }); err == nil {
		t.Fatal("accepted data after done")
	}
}
func TestStreamDecoderRejectsMalformedSequences(t *testing.T) {
	for name, input := range map[string]string{"missing-shell": `{"version":1,"type":"done"}` + "\n", "duplicate-shell": `{"version":1,"type":"shell","page":{}}` + "\n" + `{"version":1,"type":"shell","page":{}}` + "\n", "duplicate-fields": `{"version":1,"version":1,"type":"shell","page":{}}` + "\n", "unknown-field": `{"version":1,"type":"shell","Page":{}}` + "\n", "null-page": `{"version":1,"type":"shell","page":null}` + "\n", "bad-version": `{"version":2,"type":"shell","page":{}}` + "\n", "bad-id": `{"version":1,"type":"shell","page":{}}` + "\n" + `{"version":1,"type":"chunk","id":"bad[selector]"}` + "\n", "truncated": `{"version":1,"type":"shell","page":{}}` + "\n" + `{"version":1,"type":"done"}`} {
		t.Run(name, func(t *testing.T) {
			d := &StreamDecoder{}
			err := d.Feed([]byte(input), func(StreamFrame) error { return nil })
			if err == nil {
				err = d.Finish()
			}
			if err == nil {
				t.Fatal("accepted malformed stream")
			}
		})
	}
}
func TestStreamDecoderLimits(t *testing.T) {
	d := &StreamDecoder{}
	if err := d.Feed([]byte(strings.Repeat("x", MaxStreamFrame+1)), func(StreamFrame) error { return nil }); err == nil {
		t.Fatal("unbounded frame")
	}
	d = &StreamDecoder{}
	if err := d.Feed(make([]byte, MaxStreamBytes+1), func(StreamFrame) error { return nil }); err == nil {
		t.Fatal("unbounded stream")
	}
}
