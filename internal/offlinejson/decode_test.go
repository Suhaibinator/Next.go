package offlinejson

import (
	"strings"
	"testing"
)

type child struct {
	Value string `json:"value"`
}
type message struct {
	Version int     `json:"version"`
	Child   child   `json:"child"`
	Items   []child `json:"items"`
}

func TestDecodeUsesExactTagsAndCommitsOnlyCompleteValidValues(t *testing.T) {
	var got message
	if err := Decode([]byte(`{"version":1,"child":{"value":"public"},"items":[{"value":"owned"}]}`), &got); err != nil {
		t.Fatal(err)
	}
	if got.Version != 1 || got.Child.Value != "public" || len(got.Items) != 1 || got.Items[0].Value != "owned" {
		t.Fatal(got)
	}
	before := got
	if err := Decode([]byte(`{"version":2,"child":{"value":42}}`), &got); err == nil {
		t.Fatal("wrong type accepted")
	}
	if got.Version != before.Version || got.Child.Value != before.Child.Value {
		t.Fatal("partial output published")
	}
}

func TestDecodeRejectsUnknownAliasedDuplicateNullAndUnboundedInputs(t *testing.T) {
	cases := map[string][]byte{
		"alias":            []byte(`{"Version":1}`),
		"unknown":          []byte(`{"extra":1}`),
		"duplicate":        []byte(`{"version":1,"version":2}`),
		"nested-duplicate": []byte(`{"child":{"value":"a","value":"b"}}`),
		"null":             []byte(`{"child":null}`),
		"null-slice":       []byte(`{"items":null}`),
		"trailing":         []byte(`{"version":1} {}`),
		"deep":             []byte(`{"items":[[[[[[[[[{}]]]]]]]]]}`),
		"large":            []byte(strings.Repeat(" ", MaxBytes+1)),
		"invalid-utf8":     []byte{'{', '}', 0xff},
	}
	for name, data := range cases {
		t.Run(name, func(t *testing.T) {
			got := message{Version: 7}
			if err := Decode(data, &got); err == nil || got.Version != 7 {
				t.Fatal("invalid input mutated output", err)
			}
		})
	}
	if Decode([]byte(`{}`), nil) == nil || Decode([]byte(`{}`), message{}) == nil {
		t.Fatal("invalid output target accepted")
	}
	var out map[string]any
	if Decode([]byte(`{"unknown":1}`), &out) == nil {
		t.Fatal("open map admitted")
	}
}
